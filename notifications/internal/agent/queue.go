package agent

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

type QueuedEvent struct {
	EventID     string
	Body        []byte
	Attempts    int
	NextAttempt time.Time
}

type Queue struct {
	db *sql.DB
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
		`CREATE TABLE IF NOT EXISTS pending_events (
            event_id TEXT PRIMARY KEY,
            body BLOB NOT NULL,
            attempts INTEGER NOT NULL DEFAULT 0,
            next_attempt_at INTEGER NOT NULL,
            created_at INTEGER NOT NULL,
            expires_at INTEGER NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_pending_events_due ON pending_events(next_attempt_at)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &Queue{db: db}, nil
}

func (q *Queue) Close() error { return q.db.Close() }

func (q *Queue) Enqueue(ctx context.Context, eventID string, body []byte, now time.Time) error {
	_, err := q.db.ExecContext(ctx, `
        INSERT OR IGNORE INTO pending_events(event_id, body, next_attempt_at, created_at, expires_at)
        VALUES (?, ?, ?, ?, ?)`, eventID, body, now.Unix(), now.Unix(), now.Add(7*24*time.Hour).Unix())
	if err != nil {
		return err
	}
	_, _ = q.db.ExecContext(ctx, `DELETE FROM pending_events WHERE expires_at < ?`, now.Unix())
	_, _ = q.db.ExecContext(ctx, `DELETE FROM pending_events WHERE event_id IN (
        SELECT event_id FROM pending_events ORDER BY created_at DESC LIMIT -1 OFFSET 500
    )`)
	return nil
}

func (q *Queue) Next(ctx context.Context, now time.Time) (QueuedEvent, error) {
	var event QueuedEvent
	var next int64
	err := q.db.QueryRowContext(ctx, `
        SELECT event_id, body, attempts, next_attempt_at
        FROM pending_events WHERE next_attempt_at <= ?
        ORDER BY created_at LIMIT 1`, now.Unix()).Scan(&event.EventID, &event.Body, &event.Attempts, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return QueuedEvent{}, sql.ErrNoRows
	}
	event.NextAttempt = time.Unix(next, 0)
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
