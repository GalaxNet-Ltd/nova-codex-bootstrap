package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

func TestHerdrFirstObservationIsSilentForEveryAgent(t *testing.T) {
	for _, label := range []string{"codex", "claude", "gemini", "custom-agent"} {
		for _, initial := range []string{"blocked", "working", "idle", "done", "unknown"} {
			t.Run(label+"/"+initial, func(t *testing.T) {
				q, binding, now := herdrTestQueue(t)
				ctx := context.Background()
				if err := q.ReconcileHerdr(ctx, binding, nil, "server", true, now); err != nil {
					t.Fatal(err)
				}
				sequence := uint64(10)
				a := HerdrAgent{PaneID: "new-pane", TerminalID: "terminal", Agent: label, Status: initial,
					Sequence: sequence, CompletionSupported: true, CompletionSequence: &sequence}
				observe := func(want int) {
					t.Helper()
					if err := q.ReconcileHerdr(ctx, binding, []HerdrAgent{a}, "server", false, now); err != nil {
						t.Fatal(err)
					}
					if depth, err := q.Depth(ctx); err != nil || depth != want {
						t.Fatalf("queue depth %d, want %d: %v", depth, want, err)
					}
				}
				observe(0) // Even completion evidence cannot replay a first observation.
				observe(0)
				a.CompletionSequence = nil
				if initial == "blocked" {
					a.Sequence++
					observe(0) // Repeated blocked is still the same observed episode.
					a.Status, a.Sequence = "idle", a.Sequence+1
					observe(0)
				}
				// Starting in working must not consume the first actual approval.
				// Idle/unknown to blocked also qualifies; working is not required.
				a.Status, a.Sequence = "blocked", a.Sequence+1
				observe(1)
				observe(1)
			})
		}
	}
}

func TestHerdrNewPaneBaselineDoesNotSilenceExistingSibling(t *testing.T) {
	q, binding, now := herdrTestQueue(t)
	ctx := context.Background()
	existing := HerdrAgent{PaneID: "existing", TerminalID: "one", Agent: "claude", Status: "working", Sequence: 1}
	if err := q.ReconcileHerdr(ctx, binding, []HerdrAgent{existing}, "server", true, now); err != nil {
		t.Fatal(err)
	}
	existing.Status, existing.Sequence = "blocked", 2
	newcomer := HerdrAgent{PaneID: "new", TerminalID: "two", Agent: "codex", Status: "blocked", Sequence: 1}
	if err := q.ReconcileHerdr(ctx, binding, []HerdrAgent{existing, newcomer}, "server", false, now); err != nil {
		t.Fatal(err)
	}
	if depth, err := q.Depth(ctx); err != nil || depth != 1 {
		t.Fatalf("queue depth %d, want 1: %v", depth, err)
	}
	event, err := q.Next(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	var body protocol.HostEvent
	if err := json.Unmarshal(event.Body, &body); err != nil || body.Target == nil || body.Target.PaneID != existing.PaneID {
		t.Fatal("sibling attention was lost or first observation notified")
	}
}

func TestHerdrBaselinePersistsAndPreservesCodexCorrelation(t *testing.T) {
	q, binding, now := herdrTestQueue(t)
	ctx := context.Background()
	var a HerdrAgent
	const thread = "01999999-1111-7222-8333-444444444444"
	if err := json.Unmarshal([]byte(`{"pane_id":"pane","terminal_id":"terminal","agent":"codex",
		"agent_status":"blocked","state_change_seq":10,
		"agent_session":{"agent":"codex","kind":"id","value":"`+thread+`"}}`), &a); err != nil {
		t.Fatal(err)
	}
	if err := q.ReconcileHerdr(ctx, binding, nil, "server", true, now); err != nil {
		t.Fatal(err)
	}
	if err := q.ReconcileHerdr(ctx, binding, []HerdrAgent{a}, "server", false, now); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := q.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for _, baseline := range []bool{true, false} {
		if err := q.ReconcileHerdr(ctx, binding, []HerdrAgent{a}, "server", baseline, now); err != nil {
			t.Fatal(err)
		}
	}
	if depth, err := q.Depth(ctx); err != nil || depth != 0 {
		t.Fatalf("restart replayed initial blocked: %d %v", depth, err)
	}
	for _, status := range []string{"working", "blocked"} {
		a.Status, a.Sequence = status, a.Sequence+1
		if err := q.ReconcileHerdr(ctx, binding, []HerdrAgent{a}, "server", false, now); err != nil {
			t.Fatal(err)
		}
	}
	event, err := q.Next(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	var body protocol.HostEvent
	if err := json.Unmarshal(event.Body, &body); err != nil || body.Target == nil ||
		body.Target.CodexThreadID != thread || body.EventType != protocol.EventAttentionRequired ||
		body.HerdrMetadata == nil || body.HerdrMetadata.AgentStatus != "blocked" {
		t.Fatal("baseline policy lost Codex correlation or attention metadata")
	}
}
