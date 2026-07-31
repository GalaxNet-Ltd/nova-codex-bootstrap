package appservercontrol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type recordedCommand struct {
	name      string
	arguments []string
}

type recordingRunner struct {
	commands []recordedCommand
	err      error
}

func (r *recordingRunner) Run(_ context.Context, name string, arguments ...string) error {
	r.commands = append(r.commands, recordedCommand{name: name, arguments: append([]string(nil), arguments...)})
	return r.err
}

func TestRestartDarwin(t *testing.T) {
	runner := &recordingRunner{}
	if err := restart(context.Background(), "darwin", 501, runner); err != nil {
		t.Fatal(err)
	}
	want := []recordedCommand{{
		name:      "launchctl",
		arguments: []string{"kickstart", "-k", "gui/501/dev.galaxnet.novascale.codex"},
	}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestRestartLinux(t *testing.T) {
	runner := &recordingRunner{}
	if err := restart(context.Background(), "linux", 1000, runner); err != nil {
		t.Fatal(err)
	}
	want := []recordedCommand{{
		name:      "systemctl",
		arguments: []string{"--user", "restart", "novascale-codex.service"},
	}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestRestartRejectsUnsupportedOS(t *testing.T) {
	runner := &recordingRunner{}
	err := restart(context.Background(), "windows", 1000, runner)
	if err == nil || !strings.Contains(err.Error(), "unsupported operating system") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestRestartRejectsInvalidDarwinUID(t *testing.T) {
	runner := &recordingRunner{}
	err := restart(context.Background(), "darwin", -1, runner)
	if err == nil || !strings.Contains(err.Error(), "invalid user id") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestRestartReturnsRunnerError(t *testing.T) {
	runner := &recordingRunner{err: errors.New("service unavailable")}
	err := restart(context.Background(), "linux", 1000, runner)
	if err == nil || !strings.Contains(err.Error(), "restart Codex App Server service") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestartHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &recordingRunner{}
	if err := restart(ctx, "linux", 1000, runner); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}
