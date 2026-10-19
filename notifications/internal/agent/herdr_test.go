package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

func TestHerdrStatusDistinguishesNewBinaryFromLegacyLiveDaemon(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "nova-herdr-old-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	paths := Paths{ConfigFile: filepath.Join(directory, "config.json"), Socket: filepath.Join(directory, "agent.sock"), Database: filepath.Join(directory, "agent.db")}
	if err := SaveConfig(paths.ConfigFile, Config{Version: 1, HostID: "test-host", Endpoint: "https://notify.invalid", RegistrationState: RegistrationActive}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(time.Second))
		var request protocol.DaemonInfoRequest
		if json.NewDecoder(connection).Decode(&request) == nil {
			json.NewEncoder(connection).Encode(map[string]any{"version": "1.0.0-old", "registrationState": "active", "queueDepth": 0})
		}
	}()
	status, err := InspectHerdr(context.Background(), paths, filepath.Join(directory, "missing-herdr.sock"), "2.0.0-new")
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.AgentVersion != "2.0.0-new" || status.DaemonVersion != "1.0.0-old" {
		t.Fatal("installed binary was mistaken for a capable live daemon")
	}
	body, err := json.Marshal(status)
	if err != nil || !strings.Contains(string(body), `"daemonCapabilities":[]`) {
		t.Fatal("legacy daemon capabilities must remain a decodable empty array")
	}
}

func herdrTestQueue(t *testing.T) (*Queue, HerdrBinding, time.Time) {
	t.Helper()
	queue, err := OpenQueue(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { queue.Close() })
	if err := queue.SetHerdrCaptureEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	binding, err := queue.BindHerdr(context.Background(), filepath.Join(t.TempDir(), "private-session.sock"), now)
	if err != nil {
		t.Fatal(err)
	}
	return queue, binding, now
}

func TestHerdrHookSignalExcludesPrivateContext(t *testing.T) {
	payload := `{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed","pane_id":"w1:p1","workspace_id":"w1","agent_status":"blocked","title":"PRIVATE_TITLE","state_labels":{"cwd":"PRIVATE_DIRECTORY"},"tool_input":"PRIVATE_COMMAND","last_assistant_message":"PRIVATE_OUTPUT"}}`
	signal, err := NormalizeHerdrHook("pane.agent_status_changed", payload, "/private/socket-name.sock")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(signal)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"PRIVATE_", "title", "state_labels", "tool_input", "workspace_id"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("local signal contains private field %q", value)
		}
	}
	if len(signal.BindingKey) != 64 || signal.PaneID != "w1:p1" {
		t.Fatal("invalid minimized signal")
	}
	if signal.SocketPath != "/private/socket-name.sock" {
		t.Fatal("local socket hint was lost")
	}
	for _, input := range []string{"{}", payload + "{}", strings.Repeat("x", MaxHookInput+1), strings.ReplaceAll(payload, "pane_agent_status_changed", "pane.agent_status_changed")} {
		if _, err := NormalizeHerdrHook("pane.agent_status_changed", input, "/private/socket-name.sock"); err == nil {
			t.Fatal("invalid hook payload accepted")
		}
	}
}

func TestHerdrStateReconcilesEpisodesAndIgnoresPresentation(t *testing.T) {
	queue, binding, now := herdrTestQueue(t)
	ctx := context.Background()
	agent := HerdrAgent{PaneID: "pane", TerminalID: "terminal", Agent: "codex", Status: "working", Sequence: 1}
	observe := func(status string, sequence uint64, baseline bool) {
		t.Helper()
		agent.Status, agent.Sequence = status, sequence
		if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{agent}, "socket-incarnation", baseline, now); err != nil {
			t.Fatal(err)
		}
	}
	depth := func(want int) {
		t.Helper()
		got, err := queue.Depth(ctx)
		if err != nil || got != want {
			t.Fatalf("queue depth %d, want %d; error %v", got, want, err)
		}
	}
	observe("working", 1, true)
	depth(0)
	observe("blocked", 2, false)
	depth(1)
	observe("blocked", 2, false)
	observe("blocked", 3, false)
	depth(1)
	observe("working", 4, false)
	depth(1)
	observe("done", 5, false)
	depth(2)
	observe("idle", 5, false)
	observe("idle", 6, false)
	depth(2)
	observe("working", 7, false)
	observe("idle", 8, false)
	depth(3)
	observe("idle", 8, false)
	depth(3)
	for range 3 {
		queued, err := queue.Next(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		var event protocol.HostEvent
		if err := json.Unmarshal(queued.Body, &event); err != nil || !event.Valid() || event.Source != protocol.SourceHerdr || event.Target.SessionID != binding.SessionID {
			t.Fatal("invalid normalized event")
		}
		if strings.Contains(string(queued.Body), "private-session") || event.ThreadID != "" || event.TurnID != "" {
			t.Fatal("Herdr event leaked local session locator or fabricated Codex target")
		}
		if err := queue.Complete(ctx, queued.EventID); err != nil {
			t.Fatal(err)
		}
	}
	depth(0)
}

func TestHerdrRestartReplacementAndColdBaselineCannotReplay(t *testing.T) {
	queue, binding, now := herdrTestQueue(t)
	ctx := context.Background()
	agent := HerdrAgent{PaneID: "pane", TerminalID: "terminal-1", Agent: "claude", Status: "working", Sequence: 10}
	if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{agent}, "first", true, now); err != nil {
		t.Fatal(err)
	}
	agent.Status, agent.Sequence = "done", 11
	if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{agent}, "first", false, now); err != nil {
		t.Fatal(err)
	}
	if depth, _ := queue.Depth(ctx); depth != 1 {
		t.Fatal("missing legitimate progress event")
	}
	agent.TerminalID, agent.Sequence, agent.Status = "terminal-2", 1, "blocked"
	if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{agent}, "second", false, now); err != nil {
		t.Fatal(err)
	}
	if depth, _ := queue.Depth(ctx); depth != 0 {
		t.Fatal("restart retained old event or replayed blocked baseline")
	}
	agent.Status, agent.Sequence = "working", 2
	if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{agent}, "second", false, now); err != nil {
		t.Fatal(err)
	}
	agent.Status, agent.Sequence = "done", 3
	if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{agent}, "second", true, now); err != nil {
		t.Fatal(err)
	}
	if depth, _ := queue.Depth(ctx); depth != 0 {
		t.Fatal("cold daemon baseline invented completion")
	}
	if err := queue.ReconcileHerdr(ctx, binding, nil, "second", false, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := queue.db.QueryRow(`SELECT COUNT(*) FROM herdr_panes`).Scan(&count); err != nil || count != 0 {
		t.Fatal("removed pane state was retained")
	}
}

func TestHerdrBindingsPersistWithoutSessionNamesAndDisabledAdapterIgnoresLateHooks(t *testing.T) {
	queue, binding, now := herdrTestQueue(t)
	ctx := context.Background()
	bindings, err := queue.HerdrBindings(ctx)
	if err != nil || len(bindings) != 1 || !bindings[0].Enabled {
		t.Fatal("daemon startup did not recover the active binding")
	}
	reused, err := queue.BindHerdr(ctx, binding.SocketPath, now)
	if err != nil || reused.SessionID != binding.SessionID {
		t.Fatal("repeat setup replaced opaque session identity")
	}
	if binding.SessionID == binding.BindingKey || strings.Contains(binding.SessionID, "private-session") {
		t.Fatal("public session ID was derived from private locator")
	}
	if _, err := queue.db.Exec(`UPDATE herdr_bindings SET enabled = 0`); err != nil {
		t.Fatal(err)
	}
	agent := HerdrAgent{PaneID: "pane", TerminalID: "terminal", Agent: "codex", Status: "blocked", Sequence: 1}
	if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{agent}, "incarnation", false, now); err != nil {
		t.Fatal(err)
	}
	if depth, _ := queue.Depth(ctx); depth != 0 {
		t.Fatal("late hook bypassed disabled adapter")
	}
	if bindings, err := queue.HerdrBindings(ctx); err != nil || len(bindings) != 0 {
		t.Fatal("daemon startup would reactivate an explicitly removed adapter")
	}
}

func TestHerdrMetadataCorrelatesOnlyProvenCodexIDs(t *testing.T) {
	const thread = "01999999-1111-7222-8333-444444444444"
	for _, test := range []struct {
		agent, kind, value string
		expected           string
	}{
		{"codex", "id", thread, thread}, {"codex", "path", "/private/transcript.jsonl", ""}, {"claude", "id", thread, ""}, {"codex", "id", "private-session-name", ""},
	} {
		var agent HerdrAgent
		input := fmt.Sprintf(`{"terminal_id":"t1","pane_id":"p1","agent":%q,"agent_status":"blocked","state_change_seq":2,"title":"PRIVATE_TITLE","cwd":"PRIVATE_PATH","agent_session":{"agent":%q,"kind":%q,"value":%q}}`, test.agent, test.agent, test.kind, test.value)
		if err := json.Unmarshal([]byte(input), &agent); err != nil {
			t.Fatal(err)
		}
		if agent.CodexThreadID() != test.expected {
			t.Fatal("unproven session metadata became a Codex thread ID")
		}
	}
}

func TestQueueFairnessBoundsAndExpiryAtDequeue(t *testing.T) {
	queue, binding, now := herdrTestQueue(t)
	ctx := context.Background()
	for index := range 300 {
		if err := queue.Enqueue(ctx, fmt.Sprintf("codex-%d", index), []byte(`{"legacy":true}`), now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 400 {
		event := protocol.HostEvent{SchemaVersion: 2, Source: protocol.SourceHerdr, EventType: protocol.EventProgressUpdated, Target: &protocol.HerdrTarget{SessionID: fmt.Sprintf("%s-%d", binding.SessionID, index%8), PaneID: "pane"}, OccurredAt: now}
		body, _ := json.Marshal(event)
		if err := queue.Enqueue(ctx, fmt.Sprintf("herdr-%d", index), body, now); err != nil {
			t.Fatal(err)
		}
	}
	var codex, herdr int
	if err := queue.db.QueryRow(`SELECT COUNT(*) FROM pending_events WHERE source = 'codex'`).Scan(&codex); err != nil {
		t.Fatal(err)
	}
	if err := queue.db.QueryRow(`SELECT COUNT(*) FROM pending_events WHERE source = 'herdr'`).Scan(&herdr); err != nil {
		t.Fatal(err)
	}
	if codex != 300 || herdr != 200 {
		t.Fatalf("source bounds = %d Codex / %d Herdr", codex, herdr)
	}
	first, err := queue.Next(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	queue.Complete(ctx, first.EventID)
	second, err := queue.Next(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.EventID, "codex-") || !strings.HasPrefix(second.EventID, "herdr-") {
		t.Fatal("queue did not drain both sources fairly")
	}
	// No enqueue occurs after time moves forward. Expiry must still be applied.
	if _, err := queue.Next(ctx, now.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := queue.db.QueryRow(`SELECT COUNT(*) FROM pending_events WHERE source = 'herdr'`).Scan(&herdr); err != nil || herdr != 0 {
		t.Fatal("expired Herdr events survived dequeue")
	}
	if _, err := queue.Next(ctx, now.Add(8*24*time.Hour)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired legacy events survived dequeue")
	}
}

func TestOldQueueMigrationPreservesCodexBodyAndRetryIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := db.Exec(`CREATE TABLE pending_events(event_id TEXT PRIMARY KEY, body BLOB NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"schemaVersion":1,"eventType":"turn_stopped","threadId":"thread","turnId":"turn"}`)
	if _, err := db.Exec(`INSERT INTO pending_events VALUES ('retained-id', ?, 3, ?, ?, ?)`, body, now.Unix(), now.Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	queue, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	event, err := queue.Next(context.Background(), now)
	if err != nil || event.EventID != "retained-id" || event.Attempts != 3 || string(event.Body) != string(body) {
		t.Fatal("upgrade altered queued Codex event")
	}
}

func TestHerdrSocketResolutionMatchesPinnedSessionNames(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	for _, name := range []string{"default", "work.2026", "host-session_1"} {
		path, err := ResolveHerdrSocket(name, "")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(base, "herdr", "sessions", name, "herdr.sock")
		if name == "default" {
			want = filepath.Join(base, "herdr", "herdr.sock")
		}
		if path != want {
			t.Fatal("socket resolution differs from pinned Herdr")
		}
	}
	for _, invalid := range []string{"..", ".", "../another-user", "name with spaces", strings.Repeat("x", 65)} {
		if _, err := ResolveHerdrSocket(invalid, ""); err == nil {
			t.Fatal("unsafe session name accepted")
		}
	}
}
