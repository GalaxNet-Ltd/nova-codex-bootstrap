package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/agent"
)

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
