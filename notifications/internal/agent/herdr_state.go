package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

type HerdrBinding struct {
	BindingKey  string `json:"-"`
	SessionID   string `json:"sessionId"`
	SocketPath  string `json:"-"`
	Incarnation string `json:"-"`
	Enabled     bool   `json:"-"`
}

func (q *Queue) BindHerdr(ctx context.Context, socketPath string, now time.Time) (HerdrBinding, error) {
	key := HerdrBindingKey(socketPath)
	if binding, err := q.HerdrBinding(ctx, key); err == nil {
		result, err := q.db.ExecContext(ctx, `UPDATE herdr_bindings SET updated_at = ?, enabled = 1 WHERE binding_key = ? AND (SELECT enabled FROM herdr_adapter WHERE singleton = 1) = 1`, now.Unix(), key)
		if err == nil {
			if changed, _ := result.RowsAffected(); changed == 0 {
				return HerdrBinding{}, sql.ErrNoRows
			}
		}
		binding.Enabled = true
		return binding, err
	} else if !errors.Is(err, sql.ErrNoRows) {
		return HerdrBinding{}, err
	}
	id, err := NewUUID()
	if err != nil {
		return HerdrBinding{}, err
	}
	_, err = q.db.ExecContext(ctx, `INSERT INTO herdr_bindings(binding_key, session_id, socket_path, incarnation, updated_at)
	 SELECT ?, ?, ?, '', ? WHERE (SELECT COUNT(*) FROM herdr_bindings) < 128 AND (SELECT enabled FROM herdr_adapter WHERE singleton = 1) = 1
	 ON CONFLICT(binding_key) DO UPDATE SET updated_at = excluded.updated_at`, key, id, socketPath, now.Unix())
	if err != nil {
		return HerdrBinding{}, err
	}
	return q.HerdrBinding(ctx, key)
}

func (q *Queue) HerdrBinding(ctx context.Context, key string) (HerdrBinding, error) {
	var binding HerdrBinding
	err := q.db.QueryRowContext(ctx, `SELECT binding_key, session_id, socket_path, incarnation, enabled FROM herdr_bindings WHERE binding_key = ?`, key).Scan(&binding.BindingKey, &binding.SessionID, &binding.SocketPath, &binding.Incarnation, &binding.Enabled)
	return binding, err
}

func (q *Queue) HerdrBindings(ctx context.Context) ([]HerdrBinding, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT binding_key, session_id, socket_path, incarnation, enabled FROM herdr_bindings WHERE enabled = 1 ORDER BY updated_at DESC LIMIT 128`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []HerdrBinding{}
	for rows.Next() {
		var binding HerdrBinding
		if err := rows.Scan(&binding.BindingKey, &binding.SessionID, &binding.SocketPath, &binding.Incarnation, &binding.Enabled); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

// ReconcileHerdr is one durable transaction for observations and normalized
// events. It observes typed current state, never hook-process arrival order.
func (q *Queue) ReconcileHerdr(ctx context.Context, binding HerdrBinding, agents []HerdrAgent, incarnation string, baseline bool, now time.Time) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousIncarnation string
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT incarnation, enabled AND (SELECT enabled FROM herdr_adapter WHERE singleton = 1) FROM herdr_bindings WHERE binding_key = ?`, binding.BindingKey).Scan(&previousIncarnation, &enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if incarnation != previousIncarnation {
		baseline = true
		if _, err := tx.ExecContext(ctx, `DELETE FROM herdr_panes WHERE binding_key = ?`, binding.BindingKey); err != nil {
			return err
		}
		if previousIncarnation != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM pending_events WHERE source = 'herdr' AND session_id = ?`, binding.SessionID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE herdr_bindings SET incarnation = ?, updated_at = ? WHERE binding_key = ?`, incarnation, now.Unix(), binding.BindingKey); err != nil {
		return err
	}
	for _, agent := range agents {
		var terminal, status string
		var sequence uint64
		err := tx.QueryRowContext(ctx, `SELECT terminal_id, sequence, status FROM herdr_panes WHERE binding_key = ? AND pane_id = ?`, binding.BindingKey, agent.PaneID).Scan(&terminal, &sequence, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		newPane := errors.Is(err, sql.ErrNoRows)
		initial := newPane || terminal != agent.TerminalID || agent.Sequence < sequence
		typeToSend := protocol.EventType("")
		// The first observation of every pane/terminal is a silent baseline,
		// including a real approval first discovered already blocked. Later
		// transitions notify without requiring an observed working state.
		if !baseline && !initial && agent.Agent != "" && agent.Sequence > sequence {
			if agent.Status == "blocked" && status != "blocked" {
				typeToSend = protocol.EventAttentionRequired
			}
			completed := status == "working"
			if agent.CompletionSupported {
				// Absence on a capable server explicitly means no completed work.
				// Positive evidence also covers working states coalesced between reads.
				completed = agent.CompletionSequence != nil && *agent.CompletionSequence == agent.Sequence
			}
			if completed && (agent.Status == "idle" || agent.Status == "done") {
				typeToSend = protocol.EventProgressUpdated
			}
		}
		if initial && terminal != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM pending_events WHERE source = 'herdr' AND session_id = ? AND pane_id = ?`, binding.SessionID, agent.PaneID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO herdr_panes(binding_key, pane_id, terminal_id, sequence, status, observed_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(binding_key, pane_id) DO UPDATE SET terminal_id = excluded.terminal_id, sequence = excluded.sequence, status = excluded.status, observed_at = excluded.observed_at`, binding.BindingKey, agent.PaneID, agent.TerminalID, agent.Sequence, agent.Status, now.Unix()); err != nil {
			return err
		}
		if typeToSend != "" {
			event := protocol.HostEvent{SchemaVersion: protocol.HerdrVersion, Source: protocol.SourceHerdr, EventType: typeToSend, Target: &protocol.HerdrTarget{SessionID: binding.SessionID, PaneID: agent.PaneID, CodexThreadID: agent.CodexThreadID()}, OccurredAt: now.UTC()}
			event.HerdrMetadata = agent.Metadata()
			event.HerdrMetadata.SessionName = herdrBindingSessionName(binding.SocketPath)
			event.HerdrMetadata = event.HerdrMetadata.Normalized()
			body, err := json.Marshal(event)
			if err != nil {
				return err
			}
			id, err := NewUUID()
			if err != nil {
				return err
			}
			if err := enqueueEvent(ctx, tx, id, body, now); err != nil {
				return err
			}
		}
	}
	// Removed panes disappear from the snapshot; bounded local state must not
	// authorize a later reused pane number or grow forever.
	keep := map[string]bool{}
	for _, agent := range agents {
		keep[agent.PaneID] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT pane_id FROM herdr_panes WHERE binding_key = ?`, binding.BindingKey)
	if err != nil {
		return err
	}
	removed := []string{}
	for rows.Next() {
		var pane string
		if err := rows.Scan(&pane); err != nil {
			rows.Close()
			return err
		}
		if !keep[pane] {
			removed = append(removed, pane)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, pane := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM herdr_panes WHERE binding_key = ? AND pane_id = ?`, binding.BindingKey, pane); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM pending_events WHERE source = 'herdr' AND session_id = ? AND pane_id = ?`, binding.SessionID, pane); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *Daemon) reconcileHerdr(ctx context.Context, event protocol.HerdrDirtyEvent) {
	enabled, err := d.Queue.HerdrCaptureEnabled(ctx)
	if err != nil || !enabled {
		return
	}
	binding, err := d.Queue.HerdrBinding(ctx, event.BindingKey)
	needsBinding := err != nil || !binding.Enabled
	socket := binding.SocketPath
	if needsBinding {
		socket = event.SocketPath
	}
	if socket == "" {
		return
	}
	agents, incarnation, err := ReadHerdrAgents(ctx, socket)
	if err != nil {
		return
	}
	if needsBinding {
		binding, err = d.Queue.BindHerdr(ctx, socket, d.now())
		if err != nil {
			return
		}
	}
	if d.Queue.ReconcileHerdr(ctx, binding, agents, incarnation, event.Baseline, d.now()) == nil {
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
}

func (d *Daemon) herdrLoop(ctx context.Context) {
	bindings, err := d.Queue.HerdrBindings(ctx)
	if err == nil {
		for _, binding := range bindings {
			if ctx.Err() != nil {
				return
			}
			d.reconcileHerdr(ctx, protocol.HerdrDirtyEvent{BindingKey: binding.BindingKey, Baseline: true})
		}
	}
	d.discoverHerdr(ctx)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	discovery := time.NewTicker(2 * time.Second)
	defer discovery.Stop()
	dirty := map[string]protocol.HerdrDirtyEvent{}
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-d.herdrDirty:
			if len(dirty) < 128 {
				event.Baseline = event.Baseline || dirty[event.BindingKey].Baseline
				dirty[event.BindingKey] = event
			}
		case <-discovery.C:
			d.discoverHerdr(ctx)
		case <-ticker.C:
			for key, event := range dirty {
				delete(dirty, key)
				d.reconcileHerdr(ctx, event)
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
}
