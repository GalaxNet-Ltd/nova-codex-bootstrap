package protocol

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HerdrMetadata is the presentation subset of Herdr's documented
// pane.agent_status_changed event. PaneID is already in HerdrTarget.
// Never add arbitrary agent.list fields or forward the raw hook envelope.
type HerdrMetadata struct {
	SessionName  string            `json:"sessionName,omitempty"`
	WorkspaceID  string            `json:"workspaceId,omitempty"`
	AgentStatus  string            `json:"agentStatus"`
	Agent        string            `json:"agent,omitempty"`
	Title        string            `json:"title,omitempty"`
	DisplayAgent string            `json:"displayAgent,omitempty"`
	StateLabels  map[string]string `json:"stateLabels,omitempty"`
}

var herdrStatuses = []string{"working", "blocked", "idle", "done", "unknown"}

func DisplayText(value string, maxBytes int) string {
	var out strings.Builder
	for _, r := range strings.Join(strings.Fields(value), " ") {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if out.Len()+utf8.RuneLen(r) > maxBytes {
			break
		}
		out.WriteRune(r)
	}
	return strings.TrimSpace(out.String())
}

func (m HerdrMetadata) Normalized() *HerdrMetadata {
	if !ValidHerdrSessionName(m.SessionName) {
		m.SessionName = ""
	}
	m.WorkspaceID = DisplayText(m.WorkspaceID, 256)
	m.AgentStatus = DisplayText(m.AgentStatus, 32)
	m.Agent = DisplayText(m.Agent, 64)
	m.Title = DisplayText(m.Title, 256)
	m.DisplayAgent = DisplayText(m.DisplayAgent, 64)
	labels := map[string]string{}
	for _, key := range herdrStatuses {
		if value := DisplayText(m.StateLabels[key], 96); value != "" {
			labels[key] = value
		}
	}
	m.StateLabels = labels
	// Bound JSON bytes too (escaping can expand even short text).
	for {
		data, _ := json.Marshal(m)
		if len(data) <= 1200 {
			return &m
		}
		// Preserve opaque workspace identity or omit it; never shorten it
		// further to make room for presentation. Routing uses HerdrTarget.
		workspace, _ := json.Marshal(m.WorkspaceID)
		if len(workspace) > 600 {
			m.WorkspaceID = ""
		}
		m.Agent = DisplayText(m.Agent, len(m.Agent)/2)
		m.DisplayAgent = DisplayText(m.DisplayAgent, len(m.DisplayAgent)/2)
		m.Title = DisplayText(m.Title, len(m.Title)/2)
		for key, value := range m.StateLabels {
			if shorter := DisplayText(value, len(value)/2); shorter != "" {
				m.StateLabels[key] = shorter
			} else {
				delete(m.StateLabels, key)
			}
		}
	}
}

// Herdr 0.9.5 SessionInfo.name uses this bounded ASCII spelling. A session
// display name is optional and never replaces the opaque routing identity.
func ValidHerdrSessionName(name string) bool {
	if name == "" || len(name) > 64 || name == "." || name == ".." {
		return false
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
			return false
		}
	}
	return true
}

func (m HerdrMetadata) ValidFor(event EventType) bool {
	if (event == EventAttentionRequired && m.AgentStatus != "blocked") ||
		(event == EventProgressUpdated && m.AgentStatus != "idle" && m.AgentStatus != "done") ||
		(event != EventAttentionRequired && event != EventProgressUpdated) {
		return false
	}
	if m.WorkspaceID != "" && !ValidID(m.WorkspaceID) {
		return false
	}
	original, _ := json.Marshal(m)
	normalized, _ := json.Marshal(m.Normalized())
	return string(original) == string(normalized)
}

// Text supplied by the remote agent is displayed as text, never executed.
func (m HerdrMetadata) Body() string {
	agent := m.DisplayAgent
	if agent == "" {
		agent = m.Agent
	}
	parts := []string{}
	for _, value := range []string{agent, m.Title, m.StateLabels[m.AgentStatus]} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return DisplayText(strings.Join(parts, " · "), 400)
}
