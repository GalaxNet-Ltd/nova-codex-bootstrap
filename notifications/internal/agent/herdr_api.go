package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
	"golang.org/x/sys/unix"
)

const HerdrCapability = "herdr-v2"

// Decode the documented status-event presentation fields from current typed
// state. Paths, tokens, terminal titles, screen content and commands stay local.
type HerdrAgent struct {
	TerminalID          string            `json:"terminal_id"`
	PaneID              string            `json:"pane_id"`
	Agent               string            `json:"agent"`
	Status              string            `json:"agent_status"`
	Sequence            uint64            `json:"state_change_seq"`
	CompletionSequence  *uint64           `json:"completion_seq"`
	CompletionSupported bool              `json:"-"`
	WorkspaceID         string            `json:"workspace_id"`
	Title               string            `json:"title"`
	DisplayAgent        string            `json:"display_agent"`
	StateLabels         map[string]string `json:"state_labels"`
	AgentSession        *struct {
		Agent string `json:"agent"`
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"agent_session"`
}

func (a HerdrAgent) Metadata() *protocol.HerdrMetadata {
	return (protocol.HerdrMetadata{WorkspaceID: a.WorkspaceID, AgentStatus: a.Status, Agent: a.Agent,
		Title: a.Title, DisplayAgent: a.DisplayAgent, StateLabels: a.StateLabels}).Normalized()
}

func (a HerdrAgent) CodexThreadID() string {
	if a.Agent != "codex" || a.AgentSession == nil || a.AgentSession.Agent != "codex" || a.AgentSession.Kind != "id" {
		return ""
	}
	value := a.AgentSession.Value
	if len(value) != 36 {
		return ""
	}
	for index, ch := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if ch != '-' {
				return ""
			}
			continue
		}
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return ""
		}
	}
	return value
}

func HerdrBindingKey(socketPath string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(socketPath)))
	return hex.EncodeToString(digest[:])
}

func ResolveHerdrSocket(session, explicit string) (string, error) {
	if explicit != "" {
		if !filepath.IsAbs(explicit) || len(explicit) > 4096 || strings.ContainsAny(explicit, "\x00\r\n") {
			return "", errors.New("invalid Herdr socket path")
		}
		return filepath.Clean(explicit), nil
	}
	if session == "" || session == "default" {
		session = ""
	} else {
		if len(session) > 64 || session == "." || session == ".." {
			return "", errors.New("invalid Herdr session name")
		}
		for _, ch := range session {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
				return "", errors.New("invalid Herdr session name")
			}
		}
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	if session == "" {
		return filepath.Join(base, "herdr", "herdr.sock"), nil
	}
	return filepath.Join(base, "herdr", "sessions", session, "herdr.sock"), nil
}

func herdrRequest(ctx context.Context, socketPath, method string, params any, result any) error {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return errors.New("Herdr socket unavailable")
	}
	defer connection.Close()
	deadline := time.Now().Add(500 * time.Millisecond)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(map[string]any{"id": "novascale-notifications", "method": method, "params": params}); err != nil {
		return errors.New("Herdr request failed")
	}
	var reply struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	reader := bufio.NewReader(io.LimitReader(connection, 1024*1024))
	if err := json.NewDecoder(reader).Decode(&reply); err != nil || reply.ID != "novascale-notifications" || reply.Error != nil || len(reply.Result) == 0 {
		return errors.New("Herdr request rejected")
	}
	if result != nil {
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return errors.New("invalid Herdr response")
		}
	}
	return nil
}

// The JSON API has no completion-evidence capability flag. Use its typed ping
// version (the running server, never the installed executable) for the feature
// introduced in stable 0.9.2. Unknown/pre-release versions keep legacy behavior.
func herdrSupportsCompletionEvidence(version string) bool {
	version, _, _ = strings.Cut(version, "+")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	var numbers [3]uint64
	for i, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		numbers[i] = value
	}
	return numbers[0] > 0 || numbers[1] > 9 || (numbers[1] == 9 && numbers[2] >= 2)
}

func ReadHerdrAgents(ctx context.Context, socketPath string) ([]HerdrAgent, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	info, err := os.Lstat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, "", errors.New("Herdr socket unavailable")
	}
	var stat unix.Stat_t
	if err := unix.Lstat(socketPath, &stat); err != nil || stat.Uid != uint32(os.Getuid()) {
		return nil, "", errors.New("Herdr socket belongs to another user")
	}
	// This is a local socket incarnation marker, not a claimed Herdr boot ID.
	incarnation := fmt.Sprintf("%d:%d:%d", stat.Dev, stat.Ino, info.ModTime().UnixNano())
	var reply struct {
		Agents []HerdrAgent `json:"agents"`
	}
	var status struct {
		Version string `json:"version"`
	}
	if err := herdrRequest(ctx, socketPath, "ping", map[string]any{}, &status); err != nil {
		return nil, "", err
	}
	if err := herdrRequest(ctx, socketPath, "agent.list", map[string]any{}, &reply); err != nil {
		return nil, "", err
	}
	// Herdr closes ordinary API connections after one request. Reject a socket
	// replacement between the typed version and state reads.
	current, err := os.Lstat(socketPath)
	if err != nil || !os.SameFile(info, current) || !info.ModTime().Equal(current.ModTime()) {
		return nil, "", errors.New("Herdr socket unavailable")
	}
	if len(reply.Agents) > 512 {
		return nil, "", errors.New("Herdr agent metadata exceeds limit")
	}
	for index := range reply.Agents {
		agent := &reply.Agents[index]
		agent.CompletionSupported = herdrSupportsCompletionEvidence(status.Version)
		if !protocol.ValidID(agent.PaneID) || !protocol.ValidID(agent.TerminalID) || !validHerdrStatus(agent.Status) || agent.Sequence > 1<<63-1 {
			return nil, "", errors.New("invalid Herdr agent metadata")
		}
		if agent.CompletionSequence != nil && *agent.CompletionSequence > agent.Sequence {
			return nil, "", errors.New("invalid Herdr completion sequence")
		}
	}
	return reply.Agents, incarnation, nil
}

func validHerdrStatus(status string) bool {
	return status == "working" || status == "blocked" || status == "idle" || status == "done" || status == "unknown"
}

func NormalizeHerdrHook(trigger, payload, socketPath string) (protocol.HerdrDirtyEvent, error) {
	if len(payload) > MaxHookInput || len(socketPath) > 4096 || !filepath.IsAbs(socketPath) || strings.ContainsAny(socketPath, "\x00\r\n") {
		return protocol.HerdrDirtyEvent{}, errors.New("invalid Herdr socket")
	}
	socketPath = filepath.Clean(socketPath)
	if trigger == "startup" {
		return protocol.HerdrDirtyEvent{Type: protocol.HerdrDirtyEventType, BindingKey: HerdrBindingKey(socketPath), SocketPath: socketPath, Baseline: true}, nil
	}
	var envelope struct {
		Event string `json:"event"`
		Data  struct {
			Type   string `json:"type"`
			PaneID string `json:"pane_id"`
			Status string `json:"agent_status"`
		} `json:"data"`
	}
	if trigger != "pane.agent_status_changed" || len(payload) > MaxHookInput || json.Unmarshal([]byte(payload), &envelope) != nil || envelope.Event != "pane_agent_status_changed" || envelope.Data.Type != "pane_agent_status_changed" || !protocol.ValidID(envelope.Data.PaneID) || !validHerdrStatus(envelope.Data.Status) || !filepath.IsAbs(socketPath) {
		return protocol.HerdrDirtyEvent{}, errors.New("invalid Herdr hook")
	}
	return protocol.HerdrDirtyEvent{Type: protocol.HerdrDirtyEventType, BindingKey: HerdrBindingKey(socketPath), PaneID: envelope.Data.PaneID, SocketPath: socketPath}, nil
}

func HerdrBackendSupported(ctx context.Context, client *http.Client, endpoint string) error {
	url, err := parseNotificationEndpoint(endpoint)
	if err != nil {
		return err
	}
	url.Path = strings.TrimRight(url.Path, "/") + "/v2/capabilities"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("notification backend unavailable")
	}
	defer response.Body.Close()
	var capabilities struct {
		Herdr               bool `json:"herdr"`
		SharedPushAllowance bool `json:"sharedPushAllowance"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&capabilities) != nil || !capabilities.Herdr || !capabilities.SharedPushAllowance {
		return errors.New("notification backend does not support Herdr")
	}
	return nil
}
