package agent

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

const MaxHookInput = 64 * 1024

type codexHookInput struct {
	SessionID     string `json:"session_id"`
	TurnID        string `json:"turn_id"`
	HookEventName string `json:"hook_event_name"`
	RequestID     string `json:"request_id"`
	ToolUseID     string `json:"tool_use_id"`
}

// NormalizeHook reads the bounded Codex payload and returns only whitelisted
// identifiers. Unknown fields, including tool_input and assistant output, are
// never copied into the IPC event.
func NormalizeHook(input io.Reader, now time.Time) (protocol.HookEvent, error) {
	var source codexHookInput
	limited := &io.LimitedReader{R: input, N: MaxHookInput + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(&source); err != nil {
		return protocol.HookEvent{}, errors.New("invalid hook JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return protocol.HookEvent{}, errors.New("hook input contains trailing JSON")
	}
	if limited.N <= 0 {
		return protocol.HookEvent{}, errors.New("hook input exceeds size limit")
	}
	if source.SessionID == "" || source.TurnID == "" {
		return protocol.HookEvent{}, errors.New("hook is missing session_id or turn_id")
	}

	event := protocol.HookEvent{
		ProtocolVersion: protocol.Version,
		ThreadID:        source.SessionID,
		TurnID:          source.TurnID,
		OccurredAt:      now.UTC(),
	}
	if source.RequestID != "" {
		event.RequestID = source.RequestID
	} else {
		event.RequestID = source.ToolUseID
	}

	switch source.HookEventName {
	case "PermissionRequest":
		event.EventType = protocol.EventApprovalRequired
	case "Stop":
		event.EventType = protocol.EventTurnStopped
	default:
		return protocol.HookEvent{}, errors.New("unsupported hook event")
	}
	return event, nil
}

// WriteNeutralVerdict is the only hook response NovaScale emits. An empty JSON
// object supplies no approval, denial, blocking, or continuation decision.
func WriteNeutralVerdict(output io.Writer) {
	_, _ = io.WriteString(output, "{}\n")
}
