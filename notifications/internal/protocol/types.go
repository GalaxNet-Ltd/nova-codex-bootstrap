package protocol

import (
	"strings"
	"time"
)

const Version = 1
const HerdrVersion = 2

type Source string

const (
	SourceCodex Source = "codex"
	SourceHerdr Source = "herdr"
)

const DaemonInfoRequestType = "daemon-info"

type EventType string

const (
	EventApprovalRequired  EventType = "approval_required"
	EventTurnStopped       EventType = "turn_stopped"
	EventAttentionRequired EventType = "attention_required"
	EventProgressUpdated   EventType = "progress_updated"
	EventTestNotification  EventType = "test_notification"
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
	SchemaVersion int            `json:"schemaVersion"`
	EventType     EventType      `json:"eventType"`
	ThreadID      string         `json:"threadId,omitempty"`
	TurnID        string         `json:"turnId,omitempty"`
	RequestID     string         `json:"requestId,omitempty"`
	OccurredAt    time.Time      `json:"occurredAt"`
	Source        Source         `json:"source,omitempty"`
	Target        *HerdrTarget   `json:"target,omitempty"`
	HerdrMetadata *HerdrMetadata `json:"herdrMetadata,omitempty"`
}

// HerdrTarget contains only opaque routing hints, never terminal content or names.
type HerdrTarget struct {
	SessionID          string `json:"sessionId"`
	PaneID             string `json:"paneId,omitempty"`
	CodexThreadID      string `json:"codexThreadId,omitempty"`
	TestInstallationID string `json:"testInstallationId,omitempty"`
}

func (e HostEvent) EventSource() Source {
	if e.Source == "" && e.SchemaVersion == Version {
		return SourceCodex
	}
	return e.Source
}

func (e HostEvent) Valid() bool {
	if e.OccurredAt.IsZero() || (e.HerdrMetadata != nil && (e.EventSource() != SourceHerdr || !e.HerdrMetadata.ValidFor(e.EventType))) {
		return false
	}
	if e.SchemaVersion == Version {
		return (e.Source == "" || e.Source == SourceCodex) && e.Target == nil && e.EventType.Valid() && ValidID(e.ThreadID) && ValidID(e.TurnID) && (e.RequestID == "" || ValidID(e.RequestID))
	}
	if e.SchemaVersion != HerdrVersion || e.Source != SourceHerdr || e.ThreadID != "" || e.TurnID != "" || e.RequestID != "" || e.Target == nil || !ValidID(e.Target.SessionID) {
		return false
	}
	if e.EventType == EventTestNotification {
		return e.Target.PaneID == "" && e.Target.CodexThreadID == "" && ValidID(e.Target.TestInstallationID)
	}
	return ValidID(e.Target.PaneID) && e.Target.TestInstallationID == "" && (e.Target.CodexThreadID == "" || ValidID(e.Target.CodexThreadID)) && (e.EventType == EventAttentionRequired || e.EventType == EventProgressUpdated)
}

func ValidID(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00")
}

type IPCResponse struct {
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
}

type DaemonInfoRequest struct {
	Type string `json:"type"`
}

type DaemonInfoResponse struct {
	Version           string   `json:"version,omitempty"`
	Capabilities      []string `json:"capabilities,omitempty"`
	QueueDepth        int      `json:"queueDepth,omitempty"`
	RegistrationState string   `json:"registrationState,omitempty"`
	Error             string   `json:"error,omitempty"`
}

const HerdrDirtyEventType = "herdr-dirty"

type HerdrDirtyEvent struct {
	Type       string `json:"type"`
	BindingKey string `json:"bindingKey"`
	PaneID     string `json:"paneId"`
	SocketPath string `json:"socketPath,omitempty"`
	Baseline   bool   `json:"baseline,omitempty"`
}
