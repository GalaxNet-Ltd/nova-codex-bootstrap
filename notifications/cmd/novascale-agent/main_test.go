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

func TestEndpointPrintsOnlyConfiguredPublicEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths, err := agent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "https://notify.example.test"
	if err := agent.SaveConfig(paths.ConfigFile, agent.Config{
		Version: 1, HostID: "11111111-2222-4333-8444-555555555555",
		Endpoint: endpoint, RegistrationState: agent.RegistrationActive,
	}); err != nil {
		t.Fatal(err)
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writeEnd
	runError := runEndpoint(nil)
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
	if string(output) != endpoint+"\n" {
		t.Fatalf("endpoint output = %q", string(output))
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

func TestSwitchBackendPreservesIdentityQueueAndWrapperState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths, err := agent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	const hostID = "11111111-2222-4333-8444-555555555555"
	if _, _, err := agent.GenerateIdentity(paths.KeyFile); err != nil {
		t.Fatal(err)
	}
	keyBefore, err := os.ReadFile(paths.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveConfig(paths.ConfigFile, agent.Config{
		Version:           1,
		HostID:            hostID,
		Endpoint:          "https://sandbox.notify.example.test",
		RegistrationState: agent.RegistrationActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Database, []byte("preserved-queue"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapperToken := filepath.Join(home, ".codex", "novascale-app-server-token")
	if err := os.MkdirAll(filepath.Dir(wrapperToken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapperToken, []byte("preserved-wrapper-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	tokenSource := filepath.Join(home, "production-setup-token")
	if err := os.WriteFile(tokenSource, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runSwitchBackend([]string{
		"--endpoint", "https://notify.example.test",
		"--setup-token-file", tokenSource,
	}); err != nil {
		t.Fatal(err)
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if config.HostID != hostID || config.Endpoint != "https://notify.example.test" || config.RegistrationState != agent.RegistrationPending {
		t.Fatalf("switched config = %+v", config)
	}
	keyAfter, err := os.ReadFile(paths.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("host private key changed during backend switch")
	}
	queueAfter, err := os.ReadFile(paths.Database)
	if err != nil || string(queueAfter) != "preserved-queue" {
		t.Fatalf("queue state changed: %q, error = %v", string(queueAfter), err)
	}
	wrapperAfter, err := os.ReadFile(wrapperToken)
	if err != nil || string(wrapperAfter) != "preserved-wrapper-token" {
		t.Fatalf("wrapper state changed: %q, error = %v", string(wrapperAfter), err)
	}
	staged, err := agent.ReadSetupTokenFile(paths.SetupTokenFile)
	if err != nil || staged != token {
		t.Fatal("destination backend setup token was not staged")
	}
}

func TestSwitchBackendLeavesMatchingActiveEnrollmentUnchangedUnlessForced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths, err := agent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := agent.GenerateIdentity(paths.KeyFile); err != nil {
		t.Fatal(err)
	}
	config := agent.Config{
		Version: 1, HostID: "11111111-2222-4333-8444-555555555555",
		Endpoint: "https://notify.example.test/", RegistrationState: agent.RegistrationActive,
	}
	if err := agent.SaveConfig(paths.ConfigFile, config); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	tokenSource := filepath.Join(home, "setup-token")
	if err := os.WriteFile(tokenSource, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"--endpoint", "https://notify.example.test", "--setup-token-file", tokenSource}
	if err := runSwitchBackend(arguments); err != nil {
		t.Fatal(err)
	}
	unchanged, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged != config {
		t.Fatalf("matching active enrollment changed: %+v", unchanged)
	}
	if _, err := os.Stat(paths.SetupTokenFile); !os.IsNotExist(err) {
		t.Fatalf("no-op switch staged a setup token: %v", err)
	}
	if err := runSwitchBackend(append(arguments, "--force")); err != nil {
		t.Fatal(err)
	}
	forced, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if forced.RegistrationState != agent.RegistrationPending || forced.HostID != config.HostID {
		t.Fatalf("forced re-enrollment config = %+v", forced)
	}
}
