package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHerdrMetadataContract(t *testing.T) {
	m := (HerdrMetadata{WorkspaceID: "workspace", AgentStatus: "blocked", Agent: "claude", DisplayAgent: "Claude",
		Title: "Review database migration", StateLabels: map[string]string{"blocked": "Approve migration", "working": "Running", "cwd": "/must-not-leave"}}).Normalized()
	if !m.ValidFor(EventAttentionRequired) || m.ValidFor(EventProgressUpdated) || m.ValidFor(EventTestNotification) {
		t.Fatal("status/event mismatch accepted")
	}
	if _, found := m.StateLabels["cwd"]; found {
		t.Fatal("unknown label key escaped")
	}
	event := HostEvent{SchemaVersion: 2, Source: SourceHerdr, EventType: EventAttentionRequired, OccurredAt: time.Now(), Target: &HerdrTarget{SessionID: "session", PaneID: "pane"}, HerdrMetadata: m}
	if !event.Valid() {
		t.Fatal("valid metadata rejected")
	}
	if m.Body() != "Claude · Review database migration · Approve migration" {
		t.Fatal("unexpected presentation")
	}
	event.Source = SourceCodex
	if event.Valid() {
		t.Fatal("Herdr presentation attached to Codex")
	}
	m.Title = strings.Repeat("x", 257)
	if m.ValidFor(EventAttentionRequired) {
		t.Fatal("unbounded title accepted")
	}
}

func TestHerdrSessionNameIsOptionalBoundedDisplayMetadata(t *testing.T) {
	for _, name := range []string{"", "default", "test", "work-2.1", strings.Repeat("x", 64)} {
		m := HerdrMetadata{SessionName: name, AgentStatus: "blocked"}
		if !m.ValidFor(EventAttentionRequired) {
			t.Fatalf("valid Herdr name rejected: %q", name)
		}
		if m.Normalized().SessionName != name {
			t.Fatal("native name changed")
		}
	}
	for _, name := range []string{".", "..", "../secret", "/private/session", "two names", "test\n", strings.Repeat("x", 65), "tést"} {
		m := HerdrMetadata{SessionName: name, AgentStatus: "blocked"}
		if m.ValidFor(EventAttentionRequired) || m.Normalized().SessionName != "" {
			t.Fatal("invalid session display name accepted")
		}
	}
}

func TestHerdrMetadataBoundsEscapesAndUnicode(t *testing.T) {
	m := (HerdrMetadata{WorkspaceID: strings.Repeat("\"", 256), AgentStatus: "done", Agent: strings.Repeat("a", 100),
		DisplayAgent: strings.Repeat("\\", 100), Title: strings.Repeat("项目\"\\", 200), StateLabels: map[string]string{
			"working": strings.Repeat("\"", 100), "blocked": strings.Repeat("\\", 100), "idle": strings.Repeat("\"", 100),
			"done": strings.Repeat("\"", 100), "unknown": strings.Repeat("\"", 100)}}).Normalized()
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > 1200 || !m.ValidFor(EventProgressUpdated) {
		t.Fatal("metadata budget/round trip failed")
	}
	if got := DisplayText("  hello\nworld\u001b\u202e ", 50); got != "hello world" {
		t.Fatalf("unsafe display text: %q", got)
	}
}

func TestHerdrMetadataBoundsEveryFieldEvenWithHTMLEscaping(t *testing.T) {
	m := (HerdrMetadata{WorkspaceID: strings.Repeat("<", 256), AgentStatus: strings.Repeat("&", 2000),
		Agent: strings.Repeat("<", 64), DisplayAgent: strings.Repeat(">", 64)}).Normalized()
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > 1200 || m.WorkspaceID != "" || m.ValidFor(EventAttentionRequired) {
		t.Fatal("untrusted fixed fields exceeded bounds or became a valid event")
	}
}
