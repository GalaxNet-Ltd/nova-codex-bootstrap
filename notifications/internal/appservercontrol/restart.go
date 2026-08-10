package appservercontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

const (
	serviceLabel = "dev.galaxnet.novascale.codex"
	serviceFile  = "novascale-codex.service"
)

type commandRunner interface {
	Run(context.Context, string, ...string) error
	Output(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, arguments ...string) error {
	return exec.CommandContext(ctx, name, arguments...).Run()
}

func (execRunner) Output(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, arguments...).Output()
}

// Restart requests a restart of the user-level Codex App Server service.
// It does not alter service configuration or rotate the capability token.
func Restart(ctx context.Context) error {
	return restart(ctx, runtime.GOOS, os.Getuid(), execRunner{})
}

func restart(ctx context.Context, goos string, uid int, runner commandRunner) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var name string
	var arguments []string
	switch goos {
	case "darwin":
		if uid < 0 {
			return errors.New("invalid user id")
		}
		name = "launchctl"
		arguments = []string{"kickstart", "-k", fmt.Sprintf("gui/%d/%s", uid, serviceLabel)}
	case "linux":
		name = "systemctl"
		arguments = []string{"--user", "restart", serviceFile}
	default:
		return fmt.Errorf("unsupported operating system: %s", goos)
	}

	if err := runner.Run(ctx, name, arguments...); err != nil {
		return fmt.Errorf("restart Codex App Server service: %w", err)
	}
	return nil
}
