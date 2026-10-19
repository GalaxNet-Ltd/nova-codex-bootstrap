package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/signing"
)

func TestHerdrDisplayNameMatchesManagedSessionAndNeverUsesCustomSocketPath(t *testing.T) {
	for _, name := range []string{"default", "test", "custom"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			queue, _, now := herdrTestQueue(t)
			socket, err := ResolveHerdrSocket(name, "")
			if err != nil {
				t.Fatal(err)
			}
			expected := name
			if name == "custom" {
				socket = filepath.Join(t.TempDir(), "private-locator.sock")
				expected = ""
			}
			binding, err := queue.BindHerdr(context.Background(), socket, now)
			if err != nil {
				t.Fatal(err)
			}
			pane := HerdrAgent{TerminalID: "terminal", PaneID: "pane", Agent: "codex", Status: "working", Sequence: 1}
			if err := queue.ReconcileHerdr(context.Background(), binding, []HerdrAgent{pane}, "incarnation", true, now); err != nil {
				t.Fatal(err)
			}
			pane.Status, pane.Sequence = "blocked", 2
			if err := queue.ReconcileHerdr(context.Background(), binding, []HerdrAgent{pane}, "incarnation", false, now); err != nil {
				t.Fatal(err)
			}
			event, err := queue.Next(context.Background(), now)
			if err != nil {
				t.Fatal(err)
			}
			var decoded protocol.HostEvent
			if err := json.Unmarshal(event.Body, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.HerdrMetadata == nil || decoded.HerdrMetadata.SessionName != expected || decoded.Target.SessionID != binding.SessionID {
				t.Fatal("name or exact identity changed")
			}
			if strings.Contains(string(event.Body), socket) || strings.Contains(string(event.Body), "private-locator") {
				t.Fatal("filesystem locator escaped")
			}
		})
	}
}

func TestHerdrMetadataUsesTypedStateAndExcludesUnrelatedAgentFields(t *testing.T) {
	queue, binding, now := herdrTestQueue(t)
	var agent HerdrAgent
	if err := json.Unmarshal([]byte(`{"terminal_id":"terminal","pane_id":"pane","workspace_id":"workspace","agent":"claude","display_agent":"Claude","agent_status":"working","state_change_seq":1,"title":"Review migration","state_labels":{"blocked":"Approval needed"},"cwd":"PRIVATE_PATH","tokens":{"secret":"PRIVATE_SECRET"},"terminal_title":"PRIVATE_TERMINAL","tool_input":"PRIVATE_COMMAND","last_assistant_message":"PRIVATE_OUTPUT"}`), &agent); err != nil {
		t.Fatal(err)
	}
	if err := queue.ReconcileHerdr(context.Background(), binding, []HerdrAgent{agent}, "incarnation", true, now); err != nil {
		t.Fatal(err)
	}
	agent.Status = "blocked"
	agent.Sequence = 2
	if err := queue.ReconcileHerdr(context.Background(), binding, []HerdrAgent{agent}, "incarnation", false, now); err != nil {
		t.Fatal(err)
	}
	event, err := queue.Next(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(event.Body), "PRIVATE_") {
		t.Fatal("unreviewed fields escaped")
	}
	var decoded protocol.HostEvent
	if json.Unmarshal(event.Body, &decoded) != nil || !decoded.Valid() || decoded.HerdrMetadata == nil || decoded.HerdrMetadata.WorkspaceID != "workspace" || decoded.HerdrMetadata.Title != "Review migration" {
		t.Fatal("documented metadata missing")
	}
}

func TestHerdrMetadataFallsBackOnOlderBackendAndResignsSameEvent(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		timestamp, _ := strconv.ParseInt(request.Header.Get("X-NovaScale-Timestamp"), 10, 64)
		if request.Header.Get("X-NovaScale-Event-ID") != "same-event" || signing.Verify(public, timestamp, "same-event", body, request.Header.Get("X-NovaScale-Signature")) != nil {
			t.Fatal("fallback changed identity or signature")
		}
		var event protocol.HostEvent
		if json.Unmarshal(body, &event) != nil || !event.Valid() {
			t.Fatal("invalid event")
		}
		if calls == 1 {
			if event.HerdrMetadata == nil {
				t.Fatal("first attempt lost details")
			}
			return testResponse(400, "400 Bad Request"), nil
		}
		if event.HerdrMetadata != nil {
			t.Fatal("fallback retained unsupported metadata")
		}
		return testResponse(202, "202 Accepted"), nil
	})}
	event := protocol.HostEvent{SchemaVersion: 2, Source: protocol.SourceHerdr, EventType: protocol.EventAttentionRequired, OccurredAt: time.Now(), Target: &protocol.HerdrTarget{SessionID: "session", PaneID: "pane"}, HerdrMetadata: (&protocol.HerdrMetadata{AgentStatus: "blocked"}).Normalized()}
	body, _ := json.Marshal(event)
	daemon := Daemon{Config: Config{HostID: "host", Endpoint: "https://notify.test"}, PrivateKey: private, Client: client, now: time.Now}
	if err := daemon.upload(context.Background(), QueuedEvent{EventID: "same-event", Body: body}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("attempts=%d", calls)
	}
}
