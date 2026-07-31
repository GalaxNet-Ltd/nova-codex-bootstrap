package agent

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSetupTokenFileRequiresProtectedRegularFile(t *testing.T) {
	root := t.TempDir()
	validToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	validPath := filepath.Join(root, "setup-token")
	if err := os.WriteFile(validPath, []byte(validToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := ReadSetupTokenFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if token != validToken {
		t.Fatal("setup-token file contents changed while reading")
	}

	unsafePath := filepath.Join(root, "unsafe-token")
	if err := os.WriteFile(unsafePath, []byte(validToken+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSetupTokenFile(unsafePath); err == nil {
		t.Fatal("group-readable setup-token file was accepted")
	}

	invalidPath := filepath.Join(root, "invalid-token")
	if err := os.WriteFile(invalidPath, []byte("not-a-setup-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSetupTokenFile(invalidPath); err == nil {
		t.Fatal("malformed setup token was accepted")
	}

	symlinkPath := filepath.Join(root, "setup-token-link")
	if err := os.Symlink(validPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSetupTokenFile(symlinkPath); err == nil {
		t.Fatal("setup-token symlink was accepted")
	}
}

func TestStageSetupTokenCreatesProtectedAgentOwnedCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source-token")
	destination := filepath.Join(root, "agent", "pending-setup-token")
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	if err := os.WriteFile(source, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := StageSetupToken(source, destination); err != nil {
		t.Fatal(err)
	}
	staged, err := ReadSetupTokenFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if staged != token {
		t.Fatal("staged setup token changed")
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("staged setup-token permissions = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("caller-owned setup-token file was removed: %v", err)
	}
	if err := StageSetupToken(destination, destination); err != nil {
		t.Fatalf("agent-owned setup token could not be revalidated in place: %v", err)
	}
}
