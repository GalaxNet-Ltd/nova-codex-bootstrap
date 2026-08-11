package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/agent"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/appservercontrol"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/hookconfig"
)

var version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if os.Args[1] == "hook" {
		runHook(os.Args[2:])
		return
	}

	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(os.Args[2:])
	case "switch-backend":
		err = runSwitchBackend(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "status":
		err = runStatus(os.Args[2:])
	case "registration-state":
		err = runRegistrationState(os.Args[2:])
	case "host-id":
		err = runHostID(os.Args[2:])
	case "endpoint":
		err = runEndpoint(os.Args[2:])
	case "daemon-version":
		err = runDaemonVersion(os.Args[2:])
	case "app-server":
		err = runAppServer(os.Args[2:])
	case "hooks":
		err = runHooks(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "novascale-agent:", err)
		os.Exit(1)
	}
}

func runHooks(arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("usage: novascale-agent hooks <install|uninstall|status> [options]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("hooks "+arguments[0], flag.ContinueOnError)
	agentPath := flags.String("agent-path", executable, "absolute installed novascale-agent path")
	hooksFile := flags.String("hooks-file", filepath.Join(home, ".codex", "hooks.json"), "Codex hooks.json path")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected hooks arguments: %s", strings.Join(flags.Args(), " "))
	}
	absoluteAgent, err := filepath.Abs(*agentPath)
	if err != nil {
		return err
	}
	command := shellCommand(absoluteAgent) + " hook"

	switch arguments[0] {
	case "install":
		changed, err := hookconfig.Install(*hooksFile, command)
		if err != nil {
			return err
		}
		if changed {
			fmt.Println("NovaScale Codex hooks installed. Review and trust them with /hooks in Codex CLI.")
		} else {
			fmt.Println("NovaScale Codex hooks are already installed.")
		}
		return nil
	case "uninstall":
		changed, err := hookconfig.Uninstall(*hooksFile, command)
		if err != nil {
			return err
		}
		if changed {
			fmt.Println("NovaScale Codex hooks removed.")
		} else {
			fmt.Println("NovaScale Codex hooks were not installed.")
		}
		return nil
	case "status":
		installed, err := hookconfig.Installed(*hooksFile, command)
		if err != nil {
			return err
		}
		if !installed {
			return fmt.Errorf("NovaScale Codex hooks are not installed")
		}
		fmt.Println("NovaScale Codex hooks are installed for PermissionRequest and Stop.")
		return nil
	default:
		return fmt.Errorf("usage: novascale-agent hooks <install|uninstall|status> [options]")
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func shellCommand(value string) string {
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("_./-", character) {
			continue
		}
		return shellQuote(value)
	}
	return value
}

func runAppServer(arguments []string) error {
	if len(arguments) != 1 {
		return fmt.Errorf("usage: novascale-agent app-server <update-status|restart-if-updated|restart>")
	}
	timeout := 15 * time.Second
	if arguments[0] == "update-status" {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	switch arguments[0] {
	case "update-status":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		status, err := appservercontrol.InspectUpdate(ctx, home)
		if err != nil {
			return err
		}
		fmt.Println(status.State)
		return nil
	case "restart-if-updated":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		restarted, err := appservercontrol.RestartIfUpdated(ctx, home)
		if err != nil {
			return err
		}
		if restarted {
			fmt.Println("NovaScale Codex App Server restart requested for the updated Codex executable.")
		} else {
			fmt.Println("NovaScale Codex App Server is already using the configured Codex executable.")
		}
		return nil
	case "restart":
		if err := appservercontrol.Restart(ctx); err != nil {
			return err
		}
		fmt.Println("NovaScale Codex App Server restart requested.")
		return nil
	default:
		return fmt.Errorf("usage: novascale-agent app-server <update-status|restart-if-updated|restart>")
	}
}

func runHook(arguments []string) {
	// The hook path must never provide a Codex verdict, even for malformed
	// flags, input, IPC failures, or a stopped daemon.
	defer func() {
		_ = recover()
		agent.WriteNeutralVerdict(os.Stdout)
	}()
	paths, err := agent.DefaultPaths()
	if err != nil {
		return
	}
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", paths.Socket, "daemon socket")
	if flags.Parse(arguments) != nil {
		return
	}
	event, err := agent.NormalizeHook(os.Stdin, time.Now())
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	_ = agent.SendIPC(ctx, *socket, event)
}

func runInit(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	endpoint := flags.String("endpoint", "", "notification endpoint (required for a new identity)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("init takes no positional arguments")
	}
	unlock, err := agent.LockEnrollment(paths.EnrollmentLock)
	if err != nil {
		return err
	}
	defer unlock()
	var config agent.Config
	if existing, loadErr := agent.LoadConfig(paths.ConfigFile); loadErr == nil {
		if _, keyErr := agent.LoadPrivateKey(paths.KeyFile); keyErr != nil {
			return fmt.Errorf("load existing host identity: %w", keyErr)
		}
		config = existing
		if *endpoint != "" {
			if !sameNotificationEndpoint(config.Endpoint, *endpoint) {
				return fmt.Errorf("agent identity already uses another backend; run switch-backend explicitly")
			}
		}
		if config.RegistrationState == agent.RegistrationActive {
			_ = os.Remove(paths.LegacySetupTokenFile)
			fmt.Println("NovaScale notification agent is already enrolled:", config.HostID)
			return nil
		}
		if config.RegistrationState == agent.RegistrationNeedsSetupToken {
			config.RegistrationState = agent.RegistrationPending
		}
	} else if !os.IsNotExist(loadErr) {
		return loadErr
	} else {
		if *endpoint == "" {
			return fmt.Errorf("--endpoint is required for a new agent identity")
		}
		hostID, newErr := agent.NewUUID()
		if newErr != nil {
			return newErr
		}
		_, _, newErr = agent.GenerateIdentity(paths.KeyFile)
		if newErr != nil {
			return newErr
		}
		config = agent.Config{
			Version: 1, HostID: hostID, Endpoint: *endpoint,
			RegistrationState: agent.RegistrationPending,
		}
	}
	if err := agent.ValidateConfig(config); err != nil {
		return err
	}
	if err := agent.SaveConfig(paths.ConfigFile, config); err != nil {
		return err
	}
	_ = os.Remove(paths.LegacySetupTokenFile)
	fmt.Println("NovaScale notification enrollment staged:", config.HostID)
	fmt.Println("The agent daemon will enroll this host in the background.")
	return nil
}

func runSwitchBackend(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("switch-backend", flag.ContinueOnError)
	endpoint := flags.String("endpoint", "", "new notification backend endpoint")
	force := flags.Bool("force", false, "re-enroll even when the endpoint has not changed")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("switch-backend takes no positional arguments")
	}
	if *endpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	unlock, err := agent.LockEnrollment(paths.EnrollmentLock)
	if err != nil {
		return err
	}
	defer unlock()

	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("load existing agent identity: %w", err)
	}
	if _, err := agent.LoadPrivateKey(paths.KeyFile); err != nil {
		return fmt.Errorf("load existing host key: %w", err)
	}
	next := config
	next.Endpoint = *endpoint
	next.RegistrationState = agent.RegistrationPending
	if err := agent.ValidateConfig(next); err != nil {
		return err
	}
	if !*force && config.RegistrationState == agent.RegistrationActive && sameNotificationEndpoint(config.Endpoint, next.Endpoint) {
		fmt.Println("NovaScale notification agent already uses this backend; enrollment was left active.")
		return nil
	}
	if err := agent.SaveConfig(paths.ConfigFile, next); err != nil {
		return err
	}
	_ = os.Remove(paths.LegacySetupTokenFile)
	if sameNotificationEndpoint(config.Endpoint, next.Endpoint) {
		fmt.Println("NovaScale notification re-enrollment staged for existing host:", config.HostID)
	} else {
		fmt.Println("NovaScale notification backend switch staged for existing host:", config.HostID)
	}
	return nil
}

func sameNotificationEndpoint(left, right string) bool {
	return strings.TrimRight(left, "/") == strings.TrimRight(right, "/")
}

func runServe(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	socket := flags.String("socket", paths.Socket, "daemon socket")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		return err
	}
	privateKey, err := agent.LoadPrivateKey(paths.KeyFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(paths.Database), 0o700); err != nil {
		return err
	}
	queue, err := agent.OpenQueue(paths.Database)
	if err != nil {
		return err
	}
	defer queue.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	daemon := agent.Daemon{
		Config: config, ConfigPath: paths.ConfigFile, LegacySetupTokenPath: paths.LegacySetupTokenFile,
		EnrollmentLock: paths.EnrollmentLock,
		PrivateKey:     ed25519.PrivateKey(privateKey), Queue: queue,
		SocketPath: *socket, Version: version,
	}
	return daemon.Run(ctx)
}

func runDaemonVersion(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("daemon-version", flag.ContinueOnError)
	socket := flags.String("socket", paths.Socket, "daemon socket")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("daemon-version takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := agent.ReadDaemonInfo(ctx, *socket)
	if err != nil {
		return fmt.Errorf("query live daemon: %w", err)
	}
	fmt.Println(info.Version)
	return nil
}

func runStatus(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	if len(arguments) != 0 {
		return fmt.Errorf("status takes no arguments")
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		return err
	}
	queueDepth := "unavailable"
	if queue, err := agent.OpenQueue(paths.Database); err == nil {
		defer queue.Close()
		if depth, err := queue.Depth(context.Background()); err == nil {
			queueDepth = fmt.Sprintf("%d", depth)
		}
	}
	publicKey := "unavailable"
	if privateKey, err := agent.LoadPrivateKey(paths.KeyFile); err == nil {
		publicKey = base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))
	}
	fmt.Println("NovaScale Agent")
	fmt.Println("Version:      ", version)
	fmt.Println("Host ID:      ", config.HostID)
	fmt.Println("Registration: ", config.RegistrationState)
	fmt.Println("Endpoint:     ", config.Endpoint)
	fmt.Println("Socket:       ", paths.Socket)
	fmt.Println("Queue:        ", queueDepth)
	fmt.Println("Public key:   ", publicKey)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if info, err := agent.ReadDaemonInfo(ctx, paths.Socket); err == nil {
		fmt.Println("Live version: ", info.Version)
		fmt.Println("Live queue:   ", info.QueueDepth)
		fmt.Println("Live registration:", info.RegistrationState)
	} else {
		fmt.Println("Live version:  unavailable")
	}
	updateContext, updateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer updateCancel()
	if home, err := os.UserHomeDir(); err == nil {
		if status, err := appservercontrol.InspectUpdate(updateContext, home); err == nil {
			fmt.Println("App Server update:", status.State)
		} else {
			fmt.Println("App Server update: unavailable")
		}
	} else {
		fmt.Println("App Server update: unavailable")
	}
	return nil
}

func runRegistrationState(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	if len(arguments) != 0 {
		return fmt.Errorf("registration-state takes no arguments")
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		return err
	}
	fmt.Println(config.RegistrationState)
	return nil
}

func runHostID(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	if len(arguments) != 0 {
		return fmt.Errorf("host-id takes no arguments")
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		return err
	}
	fmt.Println(config.HostID)
	return nil
}

func runEndpoint(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	if len(arguments) != 0 {
		return fmt.Errorf("endpoint takes no arguments")
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		return err
	}
	fmt.Println(config.Endpoint)
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: novascale-agent <init|switch-backend|serve|hook|status|registration-state|host-id|endpoint|daemon-version|hooks|app-server|version>")
}
