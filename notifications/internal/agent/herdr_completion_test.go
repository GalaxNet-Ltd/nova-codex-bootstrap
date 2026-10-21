package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHerdrCompletionFeatureVersions(t *testing.T) {
	for version, want := range map[string]bool{
		"0.9.1": false, "0.9.2": true, "0.9.5": true, "0.10.0": true,
		"1.0.0": true, "0.9.5+build": true, "0.9.2-preview": false,
		"": false, "herdr 0.9.5": false, "0.9": false, "0.+9.5": false,
	} {
		if got := herdrSupportsCompletionEvidence(version); got != want {
			t.Errorf("version %q: %v, want %v", version, got, want)
		}
	}
}

func TestHerdrCompletionEvidenceAndLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name, before              string
		supported, evidence, want bool
	}{
		{"short completed turn", "idle", true, true, true},
		{"completion after blocked", "blocked", true, true, true},
		{"server suppressed completion", "working", true, false, false},
		{"idle sequence alone is not completion", "idle", true, false, false},
		{"legacy observed transition", "working", false, false, true},
		{"legacy unobserved transition", "idle", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, binding, now := herdrTestQueue(t)
			a := HerdrAgent{PaneID: "pane", TerminalID: "terminal", Agent: "claude", Status: tc.before, Sequence: 10, CompletionSupported: tc.supported}
			observe := func(baseline bool) {
				t.Helper()
				if err := q.ReconcileHerdr(context.Background(), binding, []HerdrAgent{a}, "incarnation", baseline, now); err != nil {
					t.Fatal(err)
				}
			}
			observe(true)
			a.Status, a.Sequence = "idle", 12
			if tc.evidence {
				seq := a.Sequence
				a.CompletionSequence = &seq
			}
			observe(false)
			observe(false)
			want := 0
			if tc.want {
				want = 1
			}
			if got, err := q.Depth(context.Background()); err != nil || got != want {
				t.Fatalf("depth %d, want %d: %v", got, want, err)
			}
			if tc.want {
				event, err := q.Next(context.Background(), now)
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				if err := json.Unmarshal(event.Body, &body); err != nil {
					t.Fatal(err)
				}
				if body["eventType"] != "progress_updated" || body["completion_seq"] != nil {
					t.Fatal("completion evidence changed the outbound event contract")
				}
			}
		})
	}
}

func TestHerdrCompletionEvidenceDoesNotReplayBaselineReplacementOrOldEvidence(t *testing.T) {
	q, binding, now := herdrTestQueue(t)
	seq := uint64(10)
	a := HerdrAgent{PaneID: "pane", TerminalID: "terminal", Agent: "claude", Status: "idle", Sequence: 10, CompletionSupported: true, CompletionSequence: &seq}
	observe := func(incarnation string, baseline bool) {
		t.Helper()
		if err := q.ReconcileHerdr(context.Background(), binding, []HerdrAgent{a}, incarnation, baseline, now); err != nil {
			t.Fatal(err)
		}
	}
	observe("first", true)
	observe("first", false)
	a.Sequence = 11 // Old evidence cannot turn an unrelated sequence into completed work.
	observe("first", false)
	seq = 12
	a.Sequence = 12
	observe("first", true) // Daemon restart or reinstall baselines existing completion.
	observe("first", false)
	observe("second", false) // Server restart also baselines it.
	a.TerminalID = "replacement"
	a.Sequence = 1
	seq = 1
	observe("second", false)
	if got, err := q.Depth(context.Background()); err != nil || got != 0 {
		t.Fatalf("replayed completion: %d: %v", got, err)
	}
	a.Sequence = 3
	seq = 3
	observe("second", false)
	observe("second", false)
	if got, err := q.Depth(context.Background()); err != nil || got != 1 {
		t.Fatalf("fresh completion was not delivered exactly once: %d: %v", got, err)
	}
}

func TestReadHerdrAgentsUsesRunningServerVersionWithOneRequestPerConnection(t *testing.T) {
	for _, version := range []string{"0.9.1", "0.9.5"} {
		t.Run(version, func(t *testing.T) {
			directory, err := os.MkdirTemp("/tmp", "nh-api-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(directory)
			path := filepath.Join(directory, "h.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				for _, method := range []string{"ping", "agent.list"} {
					c, err := listener.Accept()
					if err != nil {
						done <- err
						return
					}
					defer c.Close()
					c.SetDeadline(time.Now().Add(2 * time.Second))
					decoder := json.NewDecoder(bufio.NewReader(c))
					var req struct{ ID, Method string }
					if err := decoder.Decode(&req); err != nil {
						done <- err
						return
					}
					if req.Method != method {
						done <- os.ErrInvalid
						return
					}
					var result any = map[string]any{"version": version}
					if method == "agent.list" {
						result = map[string]any{"agents": []any{map[string]any{"pane_id": "pane", "terminal_id": "terminal", "agent": "claude", "agent_status": "idle", "state_change_seq": 12, "completion_seq": 12}}}
					}
					if err := json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
						done <- err
						return
					}
					c.Close() // The real API serves exactly one request per connection.
				}
				done <- nil
			}()
			agents, incarnation, err := ReadHerdrAgents(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if len(agents) != 1 || incarnation == "" || agents[0].CompletionSupported != (version == "0.9.5") || agents[0].CompletionSequence == nil || *agents[0].CompletionSequence != 12 {
				t.Fatal("incorrect typed completion evidence")
			}
		})
	}
}
