package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/agent"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/hookconfig"
)

type uninstallOptions struct {
	paths                         agent.Paths
	home, binary, hooks, platform string
	purge                         bool
	run                           func(context.Context, string, ...string) ([]byte, error)
	removeHerdr                   func(context.Context, agent.Paths) error
}

func runUninstall(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := flags.Bool("purge", false, "also delete the host identity and all local notification state")
	dryRun := flags.Bool("dry-run", false, "show the cleanup plan without changing files or services")
	hooks := flags.String("hooks-file", filepath.Join(home, ".codex", "hooks.json"), "Codex hooks.json path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("uninstall takes no positional arguments")
	}
	options := uninstallOptions{paths: paths, home: home, binary: binary, hooks: *hooks,
		platform: runtime.GOOS, purge: *purge, removeHerdr: agent.UninstallHerdr,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}}
	fmt.Println("Remove the NovaScale notification service, its Codex hooks, Herdr plugin, and agent executable.")
	fmt.Println("The Codex App Server service and unrelated hooks are retained. Backend records are not deleted.")
	if *purge {
		fmt.Println("Delete the notification host ID, signing key, configuration, queue, and local state.")
	} else {
		fmt.Println("Keep the notification host ID, signing key, configuration, and saved session identities for reinstall.")
	}
	if *dryRun {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := uninstallAgent(ctx, options); err != nil {
		return err
	}
	fmt.Println("NovaScale notification agent uninstalled.")
	return nil
}

func uninstallAgent(ctx context.Context, options uninstallOptions) error {
	if options.platform != "darwin" && options.platform != "linux" {
		return errors.New("agent uninstall supports macOS and Linux")
	}
	// Refuse recursive deletion through redirected directories. Never remove a
	// caller's XDG root or the parent directory of the notification installation.
	for _, directory := range []string{options.paths.ConfigDir, options.paths.StateDir} {
		if filepath.Base(directory) != "novascale-agent" || !filepath.IsAbs(directory) {
			return errors.New("unexpected notification state directory")
		}
		if info, err := os.Lstat(directory); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("notification state directory is a symbolic link")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := stopNotificationService(ctx, options); err != nil {
		return err
	}
	// Remove only this agent's commands; other hook groups and settings survive.
	if _, err := hookconfig.Uninstall(options.hooks, shellCommand(options.binary)+" hook"); err != nil {
		return err
	}
	manifest := filepath.Join(options.paths.ConfigDir, "herdr-plugin", "herdr-plugin.toml")
	if _, err := os.Stat(manifest); err == nil {
		if err := options.removeHerdr(ctx, options.paths); err != nil {
			return fmt.Errorf("remove notification Herdr plugin before deleting the agent: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(options.paths.Database); err == nil {
		queue, err := agent.OpenQueue(options.paths.Database)
		if err != nil {
			return err
		}
		err = queue.ClearNotificationCapture(ctx)
		closeErr := queue.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(filepath.Join(options.paths.ConfigDir, "herdr-plugin")); err != nil {
		return err
	}
	if err := removeIfPresent(options.paths.Socket); err != nil {
		return err
	}
	if options.purge {
		if err := os.RemoveAll(options.paths.StateDir); err != nil {
			return err
		}
		if err := os.RemoveAll(options.paths.ConfigDir); err != nil {
			return err
		}
	}
	return removeIfPresent(options.binary)
}

func stopNotificationService(ctx context.Context, options uninstallOptions) error {
	var serviceFile string
	if options.platform == "darwin" {
		label := "gui/" + strconv.Itoa(os.Getuid()) + "/dev.galaxnet.novascale.agent"
		serviceFile = filepath.Join(options.home, "Library", "LaunchAgents", "dev.galaxnet.novascale.agent.plist")
		if _, err := options.run(ctx, "launchctl", "print", label); err == nil {
			if _, err := options.run(ctx, "launchctl", "bootout", label); err != nil {
				return errors.New("could not stop the notification launch agent")
			}
		} else if runningNotificationDaemon(ctx, options.paths.Socket) {
			return errors.New("notification daemon is still running; stop it before uninstalling")
		}
	} else {
		const name = "novascale-agent.service"
		serviceFile = filepath.Join(filepath.Dir(options.paths.ConfigDir), "systemd", "user", name)
		output, err := options.run(ctx, "systemctl", "--user", "show", "--property=LoadState", "--value", name)
		if err == nil && strings.TrimSpace(string(output)) != "not-found" && strings.TrimSpace(string(output)) != "" {
			if _, err := options.run(ctx, "systemctl", "--user", "disable", "--now", name); err != nil {
				return errors.New("could not stop and disable the notification service")
			}
		} else if runningNotificationDaemon(ctx, options.paths.Socket) {
			return errors.New("notification daemon is still running; stop it before uninstalling")
		}
	}
	if runningNotificationDaemon(ctx, options.paths.Socket) {
		return errors.New("notification daemon has not stopped")
	}
	if err := removeIfPresent(serviceFile); err != nil {
		return err
	}
	if options.platform == "linux" {
		_, _ = options.run(ctx, "systemctl", "--user", "daemon-reload")
	}
	return nil
}

func runningNotificationDaemon(ctx context.Context, socket string) bool {
	if _, err := os.Stat(socket); err != nil {
		return false
	}
	check, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err := agent.ReadDaemonInfo(check, socket)
	return err == nil
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
