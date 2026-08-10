package appservercontrol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// UpdateState is a stable, non-sensitive result suitable for CLI and app
// maintenance diagnostics.
type UpdateState string

const (
	UpdateCurrent         UpdateState = "current"
	UpdateRestartRequired UpdateState = "restart_required"

	currentConfigName = "novascale-codex-host.env"
	legacyConfigName  = "novaaccess-codex-host.env"
	maxConfigBytes    = 64 << 10
	maxSymlinkDepth   = 32
	mtimeTolerance    = 2 * time.Second
)

// UpdateStatus reports whether the configured Codex executable changed after
// the currently running App Server service process started.
type UpdateStatus struct {
	State UpdateState
}

// InspectUpdate never changes or restarts the App Server service.
func InspectUpdate(ctx context.Context, home string) (UpdateStatus, error) {
	return inspectUpdate(ctx, home, runtime.GOOS, os.Getuid(), time.Now(), execRunner{})
}

// RestartIfUpdated restarts the App Server only when InspectUpdate can prove
// that the configured executable changed after the service process started.
// Unknown and unavailable states return an error and never restart anything.
func RestartIfUpdated(ctx context.Context, home string) (bool, error) {
	return restartIfUpdated(ctx, home, runtime.GOOS, os.Getuid(), time.Now(), execRunner{})
}

func restartIfUpdated(
	ctx context.Context,
	home string,
	goos string,
	uid int,
	now time.Time,
	runner commandRunner,
) (bool, error) {
	status, err := inspectUpdate(ctx, home, goos, uid, now, runner)
	if err != nil {
		return false, err
	}
	if status.State != UpdateRestartRequired {
		return false, nil
	}
	if err := restart(ctx, goos, uid, runner); err != nil {
		return false, err
	}
	return true, nil
}

func inspectUpdate(
	ctx context.Context,
	home string,
	goos string,
	uid int,
	now time.Time,
	runner commandRunner,
) (UpdateStatus, error) {
	if err := ctx.Err(); err != nil {
		return UpdateStatus{}, err
	}
	if strings.TrimSpace(home) == "" {
		return UpdateStatus{}, errors.New("home directory is unavailable")
	}
	binaryPath, err := configuredBinary(home)
	if err != nil {
		return UpdateStatus{}, err
	}
	changedAt, err := resolvedBinaryChangedAt(binaryPath)
	if err != nil {
		return UpdateStatus{}, fmt.Errorf("inspect configured Codex executable: %w", err)
	}
	pid, err := servicePID(ctx, goos, uid, runner)
	if err != nil {
		return UpdateStatus{}, err
	}
	elapsed, err := processElapsed(ctx, pid, runner)
	if err != nil {
		return UpdateStatus{}, err
	}
	startedAt := now.Add(-elapsed)
	state := UpdateCurrent
	if changedAt.After(startedAt.Add(mtimeTolerance)) {
		state = UpdateRestartRequired
	}
	return UpdateStatus{State: state}, nil
}

func configuredBinary(home string) (string, error) {
	configDirectory := filepath.Join(home, ".codex")
	paths := []string{
		filepath.Join(configDirectory, currentConfigName),
		filepath.Join(configDirectory, legacyConfigName),
	}
	for _, path := range paths {
		binary, err := binaryFromConfig(path)
		if err == nil {
			return binary, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("Codex host configuration not found in %s", configDirectory)
}

func binaryFromConfig(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Codex host configuration is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("Codex host configuration permissions are too broad")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	reader := bufio.NewScanner(io.LimitReader(file, maxConfigBytes+1))
	reader.Buffer(make([]byte, 4096), maxConfigBytes+1)
	values := make(map[string]string, 2)
	for reader.Scan() {
		line := reader.Text()
		for _, key := range []string{"NOVASCALE_CODEX_BIN", "NOVA_CODEX_BIN"} {
			prefix := key + "="
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			value, err := unquoteConfigValue(strings.TrimPrefix(line, prefix))
			if err != nil {
				return "", fmt.Errorf("invalid %s in Codex host configuration", key)
			}
			values[key] = value
		}
	}
	if err := reader.Err(); err != nil {
		return "", err
	}
	if offset, err := file.Seek(0, io.SeekEnd); err != nil {
		return "", err
	} else if offset > maxConfigBytes {
		return "", fmt.Errorf("Codex host configuration is too large")
	}
	binary := values["NOVASCALE_CODEX_BIN"]
	if binary == "" {
		binary = values["NOVA_CODEX_BIN"]
	}
	if binary == "" || !filepath.IsAbs(binary) {
		return "", fmt.Errorf("Codex host configuration has no absolute executable path")
	}
	return filepath.Clean(binary), nil
}

func unquoteConfigValue(value string) (string, error) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", errors.New("value is not double quoted")
	}
	value = value[1 : len(value)-1]
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character != '\\' {
			result.WriteByte(character)
			continue
		}
		index++
		if index >= len(value) || !strings.ContainsRune("\\\"$`", rune(value[index])) {
			return "", errors.New("unsupported escape")
		}
		result.WriteByte(value[index])
	}
	return result.String(), nil
}

func resolvedBinaryChangedAt(path string) (time.Time, error) {
	current := filepath.Clean(path)
	var changedAt time.Time
	for depth := 0; depth < maxSymlinkDepth; depth++ {
		info, err := os.Lstat(current)
		if err != nil {
			return time.Time{}, err
		}
		if info.ModTime().After(changedAt) {
			changedAt = info.ModTime()
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if !info.Mode().IsRegular() {
				return time.Time{}, errors.New("configured Codex executable is not a regular file")
			}
			return changedAt, nil
		}
		target, err := os.Readlink(current)
		if err != nil {
			return time.Time{}, err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		current = filepath.Clean(target)
	}
	return time.Time{}, errors.New("configured Codex executable has too many symbolic links")
}

func servicePID(ctx context.Context, goos string, uid int, runner commandRunner) (int, error) {
	var output []byte
	var err error
	switch goos {
	case "darwin":
		if uid < 0 {
			return 0, errors.New("invalid user id")
		}
		output, err = runner.Output(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", uid, serviceLabel))
		if err != nil {
			return 0, fmt.Errorf("query Codex App Server service: %w", err)
		}
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "pid" && fields[1] == "=" {
				return positivePID(fields[2])
			}
		}
		return 0, errors.New("Codex App Server service is not running")
	case "linux":
		output, err = runner.Output(ctx, "systemctl", "--user", "show", "--property=MainPID", "--value", serviceFile)
		if err != nil {
			return 0, fmt.Errorf("query Codex App Server service: %w", err)
		}
		return positivePID(strings.TrimSpace(string(output)))
	default:
		return 0, fmt.Errorf("unsupported operating system: %s", goos)
	}
}

func positivePID(value string) (int, error) {
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return 0, errors.New("Codex App Server service is not running")
	}
	return pid, nil
}

func processElapsed(ctx context.Context, pid int, runner commandRunner) (time.Duration, error) {
	output, err := runner.Output(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "etime=")
	if err != nil {
		return 0, fmt.Errorf("query Codex App Server process: %w", err)
	}
	elapsed, err := parseElapsed(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("query Codex App Server process: %w", err)
	}
	return elapsed, nil
}

func parseElapsed(value string) (time.Duration, error) {
	if value == "" {
		return 0, errors.New("empty elapsed time")
	}
	var days int64
	clock := value
	if separator := strings.IndexByte(value, '-'); separator >= 0 {
		parsedDays, err := strconv.ParseInt(value[:separator], 10, 64)
		if err != nil || parsedDays < 0 {
			return 0, errors.New("invalid elapsed time")
		}
		days = parsedDays
		clock = value[separator+1:]
	}
	parts := strings.Split(clock, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, errors.New("invalid elapsed time")
	}
	values := make([]int64, len(parts))
	for index, part := range parts {
		parsed, err := strconv.ParseInt(part, 10, 64)
		if err != nil || parsed < 0 {
			return 0, errors.New("invalid elapsed time")
		}
		values[index] = parsed
	}
	var hours, minutes, seconds int64
	if len(values) == 2 {
		minutes, seconds = values[0], values[1]
	} else {
		hours, minutes, seconds = values[0], values[1], values[2]
	}
	if minutes >= 60 || seconds >= 60 {
		return 0, errors.New("invalid elapsed time")
	}
	return time.Duration(days)*24*time.Hour + time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, nil
}
