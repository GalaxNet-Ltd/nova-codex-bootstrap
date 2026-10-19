package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/agent"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

func runHerdrHook(arguments []string) {
	defer func() { _ = recover(); agent.WriteNeutralVerdict(os.Stdout) }()
	paths, err := agent.DefaultPaths()
	if err != nil {
		return
	}
	flags := flag.NewFlagSet("herdr-hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", paths.Socket, "notification daemon socket")
	if flags.Parse(arguments) != nil || flags.NArg() != 0 {
		return
	}
	event, err := agent.NormalizeHerdrHook(os.Getenv("HERDR_PLUGIN_EVENT"), os.Getenv("HERDR_PLUGIN_EVENT_JSON"), os.Getenv("HERDR_SOCKET_PATH"))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	_ = agent.SendHerdrIPC(ctx, *socket, event)
}

func runHerdr(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: novascale-agent herdr <status|install|uninstall|test|bindings> --json")
	}
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("herdr", flag.ContinueOnError)
	session := flags.String("session", "", "optional exact-session lookup for status or test; capture is host-wide")
	socket := flags.String("socket", "", "explicit Herdr API socket")
	_ = flags.Bool("json", false, "structured output")
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	agentPath := flags.String("agent-path", binary, "installed agent executable")
	installation := flags.String("installation", "", "test notification recipient installation")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected Herdr arguments")
	}
	herdrSocket := ""
	if *session != "" || *socket != "" {
		herdrSocket, err = agent.ResolveHerdrSocket(*session, *socket)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch arguments[0] {
	case "bindings":
		bindings, err := agent.ReadHerdrBindingRoutes(ctx, paths)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(bindings)
	case "test":
		if !protocol.ValidID(*installation) {
			return errors.New("test recipient installation is required")
		}
		info, err := agent.ReadDaemonInfo(ctx, paths.Socket)
		if err != nil {
			return errors.New("notification daemon is unavailable")
		}
		capable := false
		for _, capability := range info.Capabilities {
			if capability == agent.HerdrCapability {
				capable = true
			}
		}
		if !capable {
			return errors.New("notification daemon requires an upgrade")
		}
		queue, err := agent.OpenQueue(paths.Database)
		if err != nil {
			return err
		}
		defer queue.Close()
		enabled, err := queue.HerdrCaptureEnabled(ctx)
		if err != nil || !enabled {
			return errors.New("Herdr notifications are not configured")
		}
		var binding agent.HerdrBinding
		if herdrSocket != "" {
			binding, err = queue.HerdrBinding(ctx, agent.HerdrBindingKey(herdrSocket))
			if err != nil || !binding.Enabled {
				return errors.New("Herdr session binding is unavailable")
			}
		} else {
			bindings, bindingErr := queue.HerdrBindings(ctx)
			if bindingErr != nil {
				return bindingErr
			}
			if len(bindings) > 0 {
				binding = bindings[0]
			} else {
				// A host-level delivery test works before any Herdr session exists.
				// This opaque test target never creates or starts a real session.
				binding.SessionID, err = agent.NewUUID()
				if err != nil {
					return err
				}
			}
		}
		id, err := agent.NewUUID()
		if err != nil {
			return err
		}
		body, err := json.Marshal(protocol.HostEvent{SchemaVersion: protocol.HerdrVersion, Source: protocol.SourceHerdr, EventType: protocol.EventTestNotification, Target: &protocol.HerdrTarget{SessionID: binding.SessionID, TestInstallationID: *installation}, OccurredAt: agent.ClockNow().UTC()})
		if err != nil {
			return err
		}
		if err := queue.Enqueue(ctx, id, body, agent.ClockNow()); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "queued", "eventId": id})
	case "status":
		status, err := agent.InspectHerdr(ctx, paths, herdrSocket, version)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	case "install":
		installedPath, err := filepath.Abs(*agentPath)
		if err != nil {
			return err
		}
		status, err := agent.InstallHerdr(ctx, paths, herdrSocket, installedPath, version)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	case "uninstall":
		if err := agent.UninstallHerdr(ctx, paths); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "removed"})
	default:
		return errors.New("unknown Herdr operation")
	}
}
