package agent

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestQueueCanBeOpenedForConcurrentStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	serving, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer serving.Close()

	status, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer status.Close()
	if depth, err := status.Depth(context.Background()); err != nil || depth != 0 {
		t.Fatalf("Depth() = %d, %v", depth, err)
	}
}

func TestQueueUsesSingleSQLiteConnection(t *testing.T) {
	queue, err := OpenQueue(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()

	if maximum := queue.db.Stats().MaxOpenConnections; maximum != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", maximum)
	}
}

func TestQueueKeepsEventsBoundToTheirOriginalBackendAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	queue, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, now := context.Background(), time.Now()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(queue.Enqueue(ctx, "legacy-production", []byte(`{}`), now))
	must(queue.ConfigureBackend(ctx, "https://production.test"))
	must(queue.ConfigureBackend(ctx, "https://staging.test"))
	must(queue.Enqueue(ctx, "new-staging", []byte(`{}`), now))
	event, err := queue.NextForBackend(ctx, now, "https://staging.test")
	must(err)
	if event.EventID != "new-staging" {
		t.Fatal("production event crossed into staging")
	}
	must(queue.Complete(ctx, event.EventID))
	if _, err := queue.NextForBackend(ctx, now, "https://staging.test"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unexpected staging event: %v", err)
	}
	must(queue.Close())
	queue, err = OpenQueue(path)
	must(err)
	defer queue.Close()
	must(queue.ConfigureBackend(ctx, "https://production.test"))
	event, err = queue.NextForBackend(ctx, now, "https://production.test")
	must(err)
	if event.EventID != "legacy-production" {
		t.Fatal("production queue was lost")
	}
}
