package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/agent"
)

func TestHostIDPrintsOnlyConfiguredPublicIdentifier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths, err := agent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	const hostID = "11111111-2222-4333-8444-555555555555"
	if err := agent.SaveConfig(paths.ConfigFile, agent.Config{
		Version:           1,
		HostID:            hostID,
		Endpoint:          "https://notify.example.test",
		RegistrationState: agent.RegistrationActive,
	}); err != nil {
		t.Fatal(err)
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writeEnd
	runError := runHostID(nil)
	_ = writeEnd.Close()
	os.Stdout = originalStdout
	output, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatal(err)
	}
	_ = readEnd.Close()

	if runError != nil {
		t.Fatal(runError)
	}
	if string(output) != hostID+"\n" {
		t.Fatalf("host-id output = %q", string(output))
	}
}

func TestInitOnlyStagesEnrollmentWithoutNetwork(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	stateHome := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32))
	source := filepath.Join(home, "setup-token")
	if err := os.WriteFile(source, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// No server exists at this endpoint. Success proves init did not make the
	// enrollment request itself.
	if err := runInit([]string{
		"--endpoint", "https://127.0.0.1:1",
		"--setup-token-file", source,
	}); err != nil {
		t.Fatal(err)
	}
	paths, err := agent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if config.RegistrationState != agent.RegistrationPending {
		t.Fatalf("registration state = %q, want pending", config.RegistrationState)
	}
	staged, err := agent.ReadSetupTokenFile(paths.SetupTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if staged != token {
		t.Fatal("agent-owned setup token changed")
	}
	configBytes, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(configBytes, []byte(token)) {
		t.Fatal("setup token leaked into agent configuration")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("caller-owned setup-token file was removed: %v", err)
	}
	lockInfo, err := os.Stat(paths.EnrollmentLock)
	if err != nil {
		t.Fatal(err)
	}
	if lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("enrollment lock permissions = %o, want 600", lockInfo.Mode().Perm())
	}
}
