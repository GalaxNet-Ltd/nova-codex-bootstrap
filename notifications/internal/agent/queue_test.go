package agent

import (
	"context"
	"path/filepath"
	"testing"
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
