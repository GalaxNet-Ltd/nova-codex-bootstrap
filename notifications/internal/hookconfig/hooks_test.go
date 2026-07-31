package hookconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const testCommand = "'/Users/Test User/.local/bin/novascale-agent' hook"

func TestInstallIsAdditiveAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	original := `{
  "custom": {"preserved": true},
  "hooks": {
    "Stop": [{"matcher":"ignored","hooks":[{"type":"command","command":"existing-command"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := Install(path, testCommand)
	if err != nil || !changed {
		t.Fatalf("Install() = %v, %v", changed, err)
	}
	installed, err := Installed(path, testCommand)
	if err != nil || !installed {
		t.Fatalf("Installed() = %v, %v", installed, err)
	}
	changed, err = Install(path, testCommand)
	if err != nil || changed {
		t.Fatalf("second Install() = %v, %v", changed, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["custom"] == nil {
		t.Fatal("unrelated root field was not preserved")
	}
	if string(data) == "" || !containsText(string(data), "existing-command") {
		t.Fatal("existing hook was not preserved")
	}
	if _, err := os.Stat(path + ".novascale-backup"); err != nil {
		t.Fatal("original hooks backup was not created")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("hooks mode = %v", info.Mode().Perm())
	}
}

func TestUninstallRemovesOnlyNovaScaleHandlers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := Install(path, testCommand); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document rawObject
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	hooks, err := hooksObject(document)
	if err != nil {
		t.Fatal(err)
	}
	hooks["PermissionRequest"] = mustJSON(appendMustGroup(t, hooks["PermissionRequest"], newGroup("existing-command")))
	document["hooks"] = mustJSON(hooks)
	if err := save(path, document, nil); err != nil {
		t.Fatal(err)
	}

	changed, err := Uninstall(path, testCommand)
	if err != nil || !changed {
		t.Fatalf("Uninstall() = %v, %v", changed, err)
	}
	installed, err := Installed(path, testCommand)
	if err != nil || installed {
		t.Fatalf("Installed() after uninstall = %v, %v", installed, err)
	}
	remaining, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(string(remaining), "existing-command") {
		t.Fatal("unrelated handler was removed")
	}
}

func TestInstallRejectsInvalidDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(path, testCommand); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func appendMustGroup(t *testing.T, raw json.RawMessage, group rawObject) []rawObject {
	t.Helper()
	var groups []rawObject
	if err := json.Unmarshal(raw, &groups); err != nil {
		t.Fatal(err)
	}
	return append(groups, group)
}

func containsText(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
