package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHerdrBindingRoutesResolveOnlyEnabledCanonicalLocalSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	paths := Paths{ConfigFile: filepath.Join(root, "agent.json"), Database: filepath.Join(root, "agent.db")}
	if err := SaveConfig(paths.ConfigFile, Config{Version: 1, HostID: "test-host", Endpoint: "https://notify.invalid", RegistrationState: RegistrationActive}); err != nil {
		t.Fatal(err)
	}
	queue, err := OpenQueue(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.SetHerdrCaptureEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	expected := map[string]string{}
	for _, name := range []string{"default", "away", "disabled"} {
		socket, err := ResolveHerdrSocket(name, "")
		if err != nil {
			t.Fatal(err)
		}
		binding, err := queue.BindHerdr(context.Background(), socket, now)
		if err != nil {
			t.Fatal(err)
		}
		if name == "disabled" {
			if _, err := queue.db.Exec(`UPDATE herdr_bindings SET enabled = 0 WHERE binding_key = ?`, binding.BindingKey); err != nil {
				t.Fatal(err)
			}
		} else {
			expected[binding.SessionID] = name
		}
	}
	for _, path := range []string{filepath.Join(root, "outside", "herdr.sock"), filepath.Join(root, "config", "herdr", "sessions", "bad name", "herdr.sock")} {
		if _, err := queue.BindHerdr(context.Background(), path, now); err != nil {
			t.Fatal(err)
		}
	}
	queue.Close()
	routes, err := ReadHerdrBindingRoutes(context.Background(), paths)
	if err != nil || len(routes.Bindings) != 2 || routes.HostID != "test-host" || routes.Endpoint != "https://notify.invalid" {
		t.Fatalf("unexpected binding routes: %+v, %v", routes, err)
	}
	for _, route := range routes.Bindings {
		if expected[route.SessionID] != route.SessionName {
			t.Fatal("opaque binding mapped to wrong local session")
		}
	}
	data, err := json.Marshal(routes)
	if err != nil || strings.Contains(string(data), root) || strings.Contains(string(data), ".sock") || strings.Contains(string(data), "bindingKey") {
		t.Fatal("SSH response exposed unnecessary filesystem metadata")
	}
}

func TestHerdrBindingLookupDoesNotCreateAnUnconfiguredQueue(t *testing.T) {
	root := t.TempDir()
	paths := Paths{ConfigFile: filepath.Join(root, "agent.json"), Database: filepath.Join(root, "missing.db")}
	if err := SaveConfig(paths.ConfigFile, Config{Version: 1, HostID: "test-host", Endpoint: "https://notify.invalid", RegistrationState: RegistrationActive}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHerdrBindingRoutes(context.Background(), paths); err == nil {
		t.Fatal("missing queue was accepted")
	}
	if _, err := os.Stat(paths.Database); !os.IsNotExist(err) {
		t.Fatal("read-only binding lookup created a queue")
	}
}
