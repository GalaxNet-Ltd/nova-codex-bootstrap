package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

func TestNormalizeHookWhitelistsPermissionIdentifiers(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	input := `{
      "session_id":"thread-1",
      "turn_id":"turn-1",
      "hook_event_name":"PermissionRequest",
      "tool_use_id":"request-1",
      "cwd":"/secret/project",
      "tool_input":{"command":"rm -rf sensitive"},
      "last_assistant_message":"private response"
    }`
	event, err := NormalizeHook(strings.NewReader(input), now)
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != protocol.EventApprovalRequired || event.ThreadID != "thread-1" || event.TurnID != "turn-1" || event.RequestID != "request-1" {
		t.Fatalf("unexpected normalized event: %#v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"secret/project", "rm -rf", "private response"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("normalized event leaked %q: %s", sensitive, encoded)
		}
	}
}

func TestWriteNeutralVerdict(t *testing.T) {
	var output bytes.Buffer
	WriteNeutralVerdict(&output)
	if output.String() != "{}\n" {
		t.Fatalf("unexpected hook verdict %q", output.String())
	}
}
