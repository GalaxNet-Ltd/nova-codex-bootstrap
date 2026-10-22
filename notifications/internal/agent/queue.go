package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"

	_ "modernc.org/sqlite"
)

type QueuedEvent struct {
	EventID     string
	Body        []byte
	Attempts    int
	NextAttempt time.Time
}

type Queue struct {
	db         *sql.DB
	nextMu     sync.Mutex
	lastSource string
}

func OpenQueue(path string) (*Queue, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite pragmas such as busy_timeout and synchronous are scoped to a
	// connection. Keep the queue on one connection so hook ingestion and the
	// upload worker always use the configured locking and durability behavior.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	statements := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS queue_backend(singleton INTEGER PRIMARY KEY CHECK(singleton = 1), endpoint TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS pending_events (
            event_id TEXT PRIMARY KEY,
            body BLOB NOT NULL,
            attempts INTEGER NOT NULL DEFAULT 0,
            next_attempt_at INTEGER NOT NULL,
            created_at INTEGER NOT NULL,
            expires_at INTEGER NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_pending_events_due ON pending_events(next_attempt_at)`,
		`CREATE TABLE IF NOT EXISTS herdr_bindings (
		 binding_key TEXT PRIMARY KEY, session_id TEXT NOT NULL UNIQUE, socket_path TEXT NOT NULL,
		 incarnation TEXT NOT NULL, updated_at INTEGER NOT NULL, enabled INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS herdr_panes (
		 binding_key TEXT NOT NULL REFERENCES herdr_bindings(binding_key) ON DELETE CASCADE,
		 pane_id TEXT NOT NULL, terminal_id TEXT NOT NULL, sequence INTEGER NOT NULL,
		 status TEXT NOT NULL, observed_at INTEGER NOT NULL, PRIMARY KEY(binding_key, pane_id)
		)`,
		`CREATE TABLE IF NOT EXISTS herdr_adapter (
		 singleton INTEGER PRIMARY KEY CHECK(singleton = 1), enabled INTEGER NOT NULL
		)`,
		// Adopt the previous explicit installation without replacing any binding.
		// A prior uninstall disabled every binding and therefore stays disabled.
		`INSERT OR IGNORE INTO herdr_adapter(singleton, enabled)
		 SELECT 1, EXISTS(SELECT 1 FROM herdr_bindings WHERE enabled = 1)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	for _, column := range []struct{ name, definition string }{
		{"backend", "TEXT NOT NULL DEFAULT ''"}, {"source", "TEXT NOT NULL DEFAULT 'codex'"}, {"session_id", "TEXT NOT NULL DEFAULT ''"}, {"pane_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('pending_events') WHERE name = ?`, column.name).Scan(&count); err != nil {
			db.Close()
			return nil, err
		}
		if count == 0 {
			if _, err := db.Exec(`ALTER TABLE pending_events ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_pending_events_source_session ON pending_events(source, session_id, created_at)`); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &Queue{db: db}, nil
}

func (q *Queue) Close() error { return q.db.Close() }

func (q *Queue) Enqueue(ctx context.Context, eventID string, body []byte, now time.Time) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := enqueueEvent(ctx, tx, eventID, body, now); err != nil {
		return err
	}
	return tx.Commit()
}

type queueExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func enqueueEvent(ctx context.Context, executor queueExecer, eventID string, body []byte, now time.Time) error {
	source, session, pane := "codex", "", ""
	expires := now.Add(7 * 24 * time.Hour)
	var event protocol.HostEvent
	if json.Unmarshal(body, &event) == nil && event.EventSource() == protocol.SourceHerdr && event.Valid() {
		source, session, pane = "herdr", event.Target.SessionID, event.Target.PaneID
		expires = event.OccurredAt.Add(30 * time.Minute)
	}
	_, err := executor.ExecContext(ctx, `INSERT OR IGNORE INTO pending_events(event_id, body, next_attempt_at, created_at, expires_at, source, session_id, pane_id, backend)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT endpoint FROM queue_backend WHERE singleton = 1), ''))`, eventID, body, now.Unix(), now.Unix(), expires.Unix(), source, session, pane)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM pending_events WHERE expires_at <= ?`, []any{now.Unix()}},
		{`DELETE FROM pending_events WHERE event_id IN (SELECT event_id FROM pending_events WHERE source = 'herdr' AND session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET 50)`, []any{session}},
		{`DELETE FROM pending_events WHERE event_id IN (SELECT event_id FROM pending_events WHERE source = 'herdr' ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET 200)`, nil},
		// A noisy Herdr session cannot evict the queued Codex lifecycle events.
		{`DELETE FROM pending_events WHERE event_id IN (SELECT event_id FROM pending_events ORDER BY CASE source WHEN 'codex' THEN 0 ELSE 1 END, created_at DESC, rowid DESC LIMIT -1 OFFSET 500)`, nil},
	} {
		if _, err := executor.ExecContext(ctx, item.query, item.args...); err != nil {
			return err
		}
	}
	return nil
}

// ConfigureBackend assigns legacy events once and makes new events belong to
// the currently active endpoint. Previously assigned events never move.
func (q *Queue) ConfigureBackend(ctx context.Context, endpoint string) error {
	normalized, err := parseNotificationEndpoint(endpoint)
	if err != nil {
		return err
	}
	endpoint = strings.TrimRight(normalized.String(), "/")
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE pending_events SET backend = COALESCE((SELECT endpoint FROM queue_backend WHERE singleton = 1), ?) WHERE backend = ''`, endpoint); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO queue_backend(singleton, endpoint) VALUES(1, ?) ON CONFLICT(singleton) DO UPDATE SET endpoint = excluded.endpoint`, endpoint); err != nil {
		return err
	}
	return tx.Commit()
}

func (q *Queue) Next(ctx context.Context, now time.Time) (QueuedEvent, error) {
	return q.next(ctx, now, "")
}

func (q *Queue) NextForBackend(ctx context.Context, now time.Time, backend string) (QueuedEvent, error) {
	return q.next(ctx, now, strings.TrimRight(backend, "/"))
}

func (q *Queue) next(ctx context.Context, now time.Time, backend string) (QueuedEvent, error) {
	q.nextMu.Lock()
	defer q.nextMu.Unlock()
	if _, err := q.db.ExecContext(ctx, `DELETE FROM pending_events WHERE expires_at <= ?`, now.Unix()); err != nil {
		return QueuedEvent{}, err
	}
	var event QueuedEvent
	var next int64
	var source string
	err := q.db.QueryRowContext(ctx, `
        SELECT event_id, body, attempts, next_attempt_at, source
        FROM pending_events WHERE next_attempt_at <= ? AND expires_at > ? AND (? = '' OR backend = ?)
        ORDER BY CASE WHEN source = ? THEN 1 ELSE 0 END, created_at, rowid LIMIT 1`, now.Unix(), now.Unix(), backend, backend, q.lastSource).Scan(&event.EventID, &event.Body, &event.Attempts, &next, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return QueuedEvent{}, sql.ErrNoRows
	}
	event.NextAttempt = time.Unix(next, 0)
	if err == nil {
		q.lastSource = source
	}
	return event, err
}

func (q *Queue) Complete(ctx context.Context, eventID string) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM pending_events WHERE event_id = ?`, eventID)
	return err
}

func (q *Queue) Retry(ctx context.Context, eventID string, attempts int, next time.Time) error {
	_, err := q.db.ExecContext(ctx, `UPDATE pending_events SET attempts = ?, next_attempt_at = ? WHERE event_id = ?`, attempts, next.Unix(), eventID)
	return err
}

func (q *Queue) Depth(ctx context.Context) (int, error) {
	var count int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pending_events`).Scan(&count)
	return count, err
}

// ClearNotificationCapture removes unsent events and disables Herdr capture
// while preserving opaque session bindings for a later reinstall.
func (q *Queue) ClearNotificationCapture(ctx context.Context) error {
	if err := q.SetHerdrCaptureEnabled(ctx, false); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `DELETE FROM pending_events`)
	return err
}
