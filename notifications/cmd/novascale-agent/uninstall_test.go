package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/agent"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/hookconfig"
)

func TestUninstallKeepsIdentityAndUnrelatedHooksAndNeverStopsWrapper(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
			paths, err := agent.DefaultPaths()
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(home, "novascale-agent")
			hooks := filepath.Join(home, "hooks.json")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(os.WriteFile(binary, []byte("fixture"), 0700))
			_, _, err = agent.GenerateIdentity(paths.KeyFile)
			must(err)
			must(agent.SaveConfig(paths.ConfigFile, agent.Config{Version: 1, HostID: "retained-host", Endpoint: "https://production.test", RegistrationState: agent.RegistrationActive}))
			_, err = hookconfig.Install(hooks, shellCommand(binary)+" hook")
			must(err)
			_, err = hookconfig.Install(hooks, "/another/program hook")
			must(err)
			must(os.MkdirAll(paths.StateDir, 0700))
			queue, err := agent.OpenQueue(paths.Database)
			must(err)
			must(queue.Enqueue(context.Background(), "pending", []byte(`{}`), time.Now()))
			must(queue.Close())
			serviceFile := filepath.Join(filepath.Dir(paths.ConfigDir), "systemd", "user", "novascale-agent.service")
			if platform == "darwin" {
				serviceFile = filepath.Join(home, "Library", "LaunchAgents", "dev.galaxnet.novascale.agent.plist")
			}
			must(os.MkdirAll(filepath.Dir(serviceFile), 0700))
			must(os.WriteFile(serviceFile, []byte("fixture"), 0600))
			manifest := filepath.Join(paths.ConfigDir, "herdr-plugin", "herdr-plugin.toml")
			must(os.MkdirAll(filepath.Dir(manifest), 0700))
			must(os.WriteFile(manifest, []byte("fixture"), 0600))
			removedHerdr := false
			calls := []string{}
			options := uninstallOptions{paths: paths, home: home, binary: binary, hooks: hooks, platform: platform,
				run: func(_ context.Context, name string, args ...string) ([]byte, error) {
					call := name + " " + strings.Join(args, " ")
					calls = append(calls, call)
					if strings.Contains(call, "novascale-codex") {
						t.Fatal("touched Codex app-server")
					}
					return []byte("loaded"), nil
				}, removeHerdr: func(context.Context, agent.Paths) error { removedHerdr = true; return nil }}
			must(uninstallAgent(context.Background(), options))
			if !removedHerdr {
				t.Fatal("Herdr registry removal was skipped")
			}
			if _, err := os.Stat(manifest); !os.IsNotExist(err) {
				t.Fatal("Herdr plugin manifest remains")
			}
			if _, err := os.Stat(serviceFile); !os.IsNotExist(err) {
				t.Fatal("notification service file remains")
			}
			config, err := agent.LoadConfig(paths.ConfigFile)
			must(err)
			if config.HostID != "retained-host" {
				t.Fatal("host identity changed")
			}
			if _, err := agent.LoadPrivateKey(paths.KeyFile); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(binary); !os.IsNotExist(err) {
				t.Fatal("executable remains")
			}
			if present, err := hookconfig.Installed(hooks, "/another/program hook"); err != nil || !present {
				t.Fatal("unrelated hook was removed")
			}
			if present, err := hookconfig.Installed(hooks, shellCommand(binary)+" hook"); err != nil || present {
				t.Fatal("agent hook remains")
			}
			queue, err = agent.OpenQueue(paths.Database)
			must(err)
			if n, err := queue.Depth(context.Background()); err != nil || n != 0 {
				t.Fatal("pending events remain")
			}
			must(queue.Close())
			// Repeating cleanup and then explicit purge are both supported.
			must(uninstallAgent(context.Background(), options))
			options.purge = true
			must(uninstallAgent(context.Background(), options))
			if _, err := os.Stat(paths.ConfigDir); !os.IsNotExist(err) {
				t.Fatal("purge retained config")
			}
			if _, err := os.Stat(paths.StateDir); !os.IsNotExist(err) {
				t.Fatal("purge retained state")
			}
			if len(calls) == 0 {
				t.Fatal("service cleanup not attempted")
			}
		})
	}
}

func TestUninstallStopsBeforeDeletingFilesWhenServiceStopFails(t *testing.T) {
	home := t.TempDir()
	paths := agent.Paths{ConfigDir: filepath.Join(home, "config", "novascale-agent"), StateDir: filepath.Join(home, "state", "novascale-agent")}
	binary := filepath.Join(home, "novascale-agent")
	if err := os.WriteFile(binary, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	options := uninstallOptions{paths: paths, home: home, binary: binary, platform: "linux", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "disable") {
			return nil, errors.New("fixture stop failed")
		}
		return []byte("loaded"), nil
	}}
	if err := uninstallAgent(context.Background(), options); err == nil {
		t.Fatal("failed service stop ignored")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal("deleted executable while service may be running")
	}
}

func TestSwitchBackendPresetsRetainIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	if err := runInit([]string{"--endpoint", "https://production.test"}); err != nil {
		t.Fatal(err)
	}
	paths, _ := agent.DefaultPaths()
	before, _ := agent.LoadConfig(paths.ConfigFile)
	for _, preset := range []string{"staging", "production"} {
		if err := runSwitchBackend([]string{preset}); err != nil {
			t.Fatal(err)
		}
		config, err := agent.LoadConfig(paths.ConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		if config.HostID != before.HostID {
			t.Fatal("changed host identity")
		}
		expected := "https://notify-staging.galaxnet.dev"
		if preset == "production" {
			expected = "https://nova-push.galaxnet.dev"
		}
		if config.Endpoint != expected {
			t.Fatal("wrong preset endpoint")
		}
	}
	if err := runSwitchBackend([]string{"--endpoint", "https://other.test", "production"}); err == nil {
		t.Fatal("accepted conflicting backend selectors")
	}
}
