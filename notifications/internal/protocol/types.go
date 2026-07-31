package protocol

import "time"

const Version = 1
const DaemonInfoRequestType = "daemon-info"

type EventType string

const (
	EventApprovalRequired EventType = "approval_required"
	EventTurnStopped      EventType = "turn_stopped"
)

func (e EventType) Valid() bool {
	return e == EventApprovalRequired || e == EventTurnStopped
}

// HookEvent is the complete IPC payload. It deliberately excludes commands,
// patches, prompts, transcript paths, working directories, and model output.
type HookEvent struct {
	ProtocolVersion int       `json:"protocolVersion"`
	EventType       EventType `json:"eventType"`
	ThreadID        string    `json:"threadId"`
	TurnID          string    `json:"turnId"`
	RequestID       string    `json:"requestId,omitempty"`
	OccurredAt      time.Time `json:"occurredAt"`
}

type HostEvent struct {
	SchemaVersion int       `json:"schemaVersion"`
	EventType     EventType `json:"eventType"`
	ThreadID      string    `json:"threadId"`
	TurnID        string    `json:"turnId"`
	RequestID     string    `json:"requestId,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
}

type IPCResponse struct {
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
}

type DaemonInfoRequest struct {
	Type string `json:"type"`
}

type DaemonInfoResponse struct {
	Version    string `json:"version,omitempty"`
	QueueDepth int    `json:"queueDepth,omitempty"`
	Error      string `json:"error,omitempty"`
}
