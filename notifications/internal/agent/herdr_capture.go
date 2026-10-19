package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

const HerdrHostCaptureCapability = "herdr-host-capture-v1"

func (q *Queue) HerdrCaptureEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := q.db.QueryRowContext(ctx, `SELECT enabled FROM herdr_adapter WHERE singleton = 1`).Scan(&enabled)
	return enabled, err
}

func (q *Queue) SetHerdrCaptureEnabled(ctx context.Context, enabled bool) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE herdr_adapter SET enabled = ? WHERE singleton = 1`, enabled); err != nil {
		return err
	}
	if !enabled {
		for _, statement := range []string{
			`UPDATE herdr_bindings SET enabled = 0`,
			`DELETE FROM pending_events WHERE source = 'herdr'`,
			`DELETE FROM herdr_panes`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Discover canonical sockets only. Noncanonical sockets arrive through the
// plugin's same-user local IPC. Socket paths stay local; only validated managed
// session names may be display metadata. Discovery never starts a session.
func HerdrSockets() []string {
	path, err := ResolveHerdrSocket("default", "")
	if err != nil {
		return nil
	}
	paths := []string{path}
	directory, err := os.Open(filepath.Join(filepath.Dir(path), "sessions"))
	if err == nil {
		entries, _ := directory.ReadDir(128)
		directory.Close()
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if socket, err := ResolveHerdrSocket(entry.Name(), ""); err == nil {
				paths = append(paths, socket)
			}
		}
	}
	result := []string{}
	for _, socket := range paths {
		if info, err := os.Lstat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			result = append(result, socket)
		}
	}
	return result
}

func (d *Daemon) discoverHerdr(ctx context.Context) {
	enabled, err := d.Queue.HerdrCaptureEnabled(ctx)
	if err != nil || !enabled {
		return
	}
	for _, socket := range HerdrSockets() {
		if ctx.Err() != nil {
			return
		}
		binding, err := d.Queue.HerdrBinding(ctx, HerdrBindingKey(socket))
		if err != nil || !binding.Enabled || binding.Incarnation == "" {
			d.reconcileHerdr(ctx, protocol.HerdrDirtyEvent{BindingKey: HerdrBindingKey(socket), SocketPath: socket, Baseline: true})
		}
	}
}

// Use Herdr's own offline-capable registry CLI instead of editing its files or
// choosing a server/session as an installation target. Commands are fixed argv
// and local-only; runtime session overrides must not narrow global setup.
func herdrPluginCommand(ctx context.Context, arguments ...string) (json.RawMessage, error) {
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary != "" && (!filepath.IsAbs(binary) || strings.ContainsAny(binary, "\x00\r\n")) {
		return nil, errors.New("invalid Herdr executable")
	}
	var err error
	if binary == "" {
		binary, err = exec.LookPath("herdr")
	}
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return nil, errors.New("Herdr executable unavailable")
		}
		binary = filepath.Join(home, ".local", "bin", "herdr")
	}
	command := exec.CommandContext(ctx, binary, append([]string{"plugin"}, arguments...)...)
	command.Env = []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "HERDR_SOCKET_PATH" && key != "HERDR_SESSION" && key != "HERDR_SESSION_NAME" {
			command.Env = append(command.Env, value)
		}
	}
	command.Stderr = io.Discard
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, errors.New("Herdr plugin command unavailable")
	}
	if err := command.Start(); err != nil {
		return nil, errors.New("Herdr plugin command unavailable")
	}
	data, readErr := io.ReadAll(io.LimitReader(output, 1024*1024+1))
	if readErr != nil || len(data) > 1024*1024 {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if readErr != nil || waitErr != nil || len(data) > 1024*1024 {
		return nil, errors.New("Herdr plugin command failed")
	}
	return data, nil
}

func readGlobalHerdrPlugin(ctx context.Context) (bool, bool) {
	data, err := herdrPluginCommand(ctx, "list", "--plugin", HerdrPluginID, "--json")
	if err != nil {
		return false, false
	}
	var reply struct {
		Result struct {
			Plugins []struct {
				ID       string   `json:"plugin_id"`
				Enabled  bool     `json:"enabled"`
				Warnings []string `json:"warnings"`
			} `json:"plugins"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &reply) != nil {
		return false, false
	}
	for _, plugin := range reply.Result.Plugins {
		if plugin.ID == HerdrPluginID {
			return true, plugin.Enabled && len(plugin.Warnings) == 0
		}
	}
	return false, false
}
