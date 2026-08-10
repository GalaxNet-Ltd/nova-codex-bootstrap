package appservercontrol

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInspectUpdateReportsRestartRequiredForNewerDarwinBinary(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	writeCodexConfiguration(t, home, now.Add(-time.Minute))
	runner := &recordingRunner{outputs: map[string][]byte{
		commandKey("launchctl", "print", "gui/501/"+serviceLabel): []byte("state = running\n\tpid = 123\n"),
		commandKey("ps", "-p", "123", "-o", "etime="):             []byte("01:00:00\n"),
	}}

	status, err := inspectUpdate(context.Background(), home, "darwin", 501, now, runner)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != UpdateRestartRequired {
		t.Fatalf("state = %q, want %q", status.State, UpdateRestartRequired)
	}
}

func TestInspectUpdateReportsCurrentForOlderLinuxBinary(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	writeCodexConfiguration(t, home, now.Add(-2*time.Hour))
	runner := &recordingRunner{outputs: map[string][]byte{
		commandKey("systemctl", "--user", "show", "--property=MainPID", "--value", serviceFile): []byte("456\n"),
		commandKey("ps", "-p", "456", "-o", "etime="):                                           []byte("01:00:00\n"),
	}}

	status, err := inspectUpdate(context.Background(), home, "linux", 1000, now, runner)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != UpdateCurrent {
		t.Fatalf("state = %q, want %q", status.State, UpdateCurrent)
	}
}

func TestRestartIfUpdatedRestartsOnlyWhenRequired(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	writeCodexConfiguration(t, home, now.Add(-time.Minute))
	runner := &recordingRunner{outputs: map[string][]byte{
		commandKey("systemctl", "--user", "show", "--property=MainPID", "--value", serviceFile): []byte("456\n"),
		commandKey("ps", "-p", "456", "-o", "etime="):                                           []byte("01:00:00\n"),
	}}

	restarted, err := restartIfUpdated(context.Background(), home, "linux", 1000, now, runner)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("restart-if-updated did not restart a stale service")
	}
	wantLast := recordedCommand{name: "systemctl", arguments: []string{"--user", "restart", serviceFile}}
	if got := runner.commands[len(runner.commands)-1]; !reflect.DeepEqual(got, wantLast) {
		t.Fatalf("last command = %#v, want %#v", got, wantLast)
	}
}

func TestRestartIfUpdatedDoesNotRestartCurrentService(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	writeCodexConfiguration(t, home, now.Add(-2*time.Hour))
	runner := &recordingRunner{outputs: map[string][]byte{
		commandKey("systemctl", "--user", "show", "--property=MainPID", "--value", serviceFile): []byte("456\n"),
		commandKey("ps", "-p", "456", "-o", "etime="):                                           []byte("01:00:00\n"),
	}}

	restarted, err := restartIfUpdated(context.Background(), home, "linux", 1000, now, runner)
	if err != nil {
		t.Fatal(err)
	}
	if restarted {
		t.Fatal("restart-if-updated restarted a current service")
	}
	for _, command := range runner.commands {
		if reflect.DeepEqual(command, recordedCommand{name: "systemctl", arguments: []string{"--user", "restart", serviceFile}}) {
			t.Fatal("restart command was issued for a current service")
		}
	}
}

func TestRestartIfUpdatedDoesNotRestartWhenStateIsUnknown(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	writeCodexConfiguration(t, home, now.Add(-time.Minute))
	runner := &recordingRunner{outputs: map[string][]byte{}}

	restarted, err := restartIfUpdated(context.Background(), home, "linux", 1000, now, runner)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("error = %v, want stopped-service error", err)
	}
	if restarted {
		t.Fatal("restart-if-updated restarted an unavailable service")
	}
	for _, command := range runner.commands {
		if command.name == "systemctl" && reflect.DeepEqual(command.arguments, []string{"--user", "restart", serviceFile}) {
			t.Fatal("restart command was issued for an unavailable service")
		}
	}
}

func TestBinaryFromConfigDecodesSetupEscapes(t *testing.T) {
	home := t.TempDir()
	configDirectory := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, `Codex $build`, `co"dex`)
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(want)
	config := filepath.Join(configDirectory, currentConfigName)
	if err := os.WriteFile(config, []byte(`NOVASCALE_CODEX_BIN="`+escaped+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := binaryFromConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("binary path = %q, want %q", got, want)
	}
}

func TestParseElapsed(t *testing.T) {
	tests := map[string]time.Duration{
		"00:07":      7 * time.Second,
		"01:02:03":   time.Hour + 2*time.Minute + 3*time.Second,
		"2-03:04:05": 51*time.Hour + 4*time.Minute + 5*time.Second,
	}
	for value, want := range tests {
		got, err := parseElapsed(value)
		if err != nil {
			t.Fatalf("parseElapsed(%q): %v", value, err)
		}
		if got != want {
			t.Fatalf("parseElapsed(%q) = %s, want %s", value, got, want)
		}
	}
	for _, value := range []string{"", "1", "1:60", "-1:00", "1-x:00:00"} {
		if _, err := parseElapsed(value); err == nil {
			t.Fatalf("parseElapsed(%q) unexpectedly succeeded", value)
		}
	}
}

func writeCodexConfiguration(t *testing.T, home string, binaryMTime time.Time) string {
	t.Helper()
	configDirectory := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(home, ".local", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(binary, binaryMTime, binaryMTime); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configDirectory, currentConfigName)
	if err := os.WriteFile(config, []byte(`NOVASCALE_CODEX_BIN="`+binary+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return binary
}
