package agent

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const HerdrPluginID = "novascale.notifications"

type HerdrStatus struct {
	HostID             string   `json:"hostId"`
	Endpoint           string   `json:"endpoint"`
	RegistrationState  string   `json:"registrationState"`
	AgentVersion       string   `json:"agentVersion"`
	DaemonVersion      string   `json:"daemonVersion,omitempty"`
	DaemonCapabilities []string `json:"daemonCapabilities"`
	SessionID          string   `json:"sessionId,omitempty"`
	SocketReady        bool     `json:"socketReady"`
	PluginInstalled    bool     `json:"pluginInstalled"`
	PluginEnabled      bool     `json:"pluginEnabled"`
	Ready              bool     `json:"ready"`
	HostReady          bool     `json:"hostReady"`
}

func HerdrManifest(binaryPath string) (string, error) {
	if !filepath.IsAbs(binaryPath) {
		return "", errors.New("agent path must be absolute")
	}
	command := "command = [" + strconv.Quote(binaryPath) + ", \"herdr-hook\"]\n"
	return "id = \"" + HerdrPluginID + "\"\nname = \"NovaScale Notifications\"\nversion = \"1.1.0\"\nmin_herdr_version = \"0.9.1\"\nplatforms = [\"linux\", \"macos\"]\n\n[[startup]]\n" + command + "\n[[events]]\non = \"pane.agent_status_changed\"\n" + command, nil
}

func InspectHerdr(ctx context.Context, paths Paths, socketPath, version string) (HerdrStatus, error) {
	config, err := LoadConfig(paths.ConfigFile)
	if err != nil {
		return HerdrStatus{}, errors.New("notification agent is not configured")
	}
	status := HerdrStatus{HostID: config.HostID, Endpoint: config.Endpoint, RegistrationState: config.RegistrationState, AgentVersion: version, DaemonCapabilities: []string{}}
	bindingEnabled, captureEnabled := false, false
	info, err := ReadDaemonInfo(ctx, paths.Socket)
	capable, hostCapture := false, false
	if err == nil {
		status.DaemonVersion = info.Version
		status.DaemonCapabilities = append(status.DaemonCapabilities, info.Capabilities...)
		status.RegistrationState = info.RegistrationState
		for _, capability := range info.Capabilities {
			if capability == HerdrCapability {
				capable = true
			}
			if capability == HerdrHostCaptureCapability {
				hostCapture = true
			}
		}
	}
	var queue *Queue
	if _, err := os.Stat(paths.Database); err == nil {
		queue, err = OpenQueue(paths.Database)
		if err != nil {
			return HerdrStatus{}, errors.New("notification state unavailable")
		}
		defer queue.Close()
		captureEnabled, _ = queue.HerdrCaptureEnabled(ctx)
	}
	if socketPath == "" {
		status.PluginInstalled, status.PluginEnabled = readGlobalHerdrPlugin(ctx)
		status.HostReady = capable && hostCapture && captureEnabled && status.PluginEnabled && status.RegistrationState == RegistrationActive
		status.Ready = status.HostReady
		return status, nil
	}
	agents, incarnation, agentsErr := ReadHerdrAgents(ctx, socketPath)
	if agentsErr == nil {
		status.SocketReady = true
	}
	var reply struct {
		Plugins []struct {
			ID       string   `json:"plugin_id"`
			Enabled  bool     `json:"enabled"`
			Warnings []string `json:"warnings"`
		} `json:"plugins"`
	}
	if err := herdrRequest(ctx, socketPath, "plugin.list", map[string]string{"plugin_id": HerdrPluginID}, &reply); err == nil {
		for _, plugin := range reply.Plugins {
			if plugin.ID == HerdrPluginID {
				status.PluginInstalled = true
				status.PluginEnabled = plugin.Enabled && len(plugin.Warnings) == 0
			}
		}
	}
	if queue != nil {
		binding, bindingErr := queue.HerdrBinding(ctx, HerdrBindingKey(socketPath))
		if captureEnabled && hostCapture && status.PluginEnabled && agentsErr == nil && (bindingErr != nil || !binding.Enabled) {
			binding, bindingErr = queue.BindHerdr(ctx, socketPath, ClockNow())
			if bindingErr == nil {
				bindingErr = queue.ReconcileHerdr(ctx, binding, agents, incarnation, true, ClockNow())
			}
		}
		if bindingErr == nil {
			status.SessionID, bindingEnabled = binding.SessionID, binding.Enabled
		}
	}
	status.HostReady = capable && hostCapture && captureEnabled && status.PluginEnabled && status.RegistrationState == RegistrationActive
	status.Ready = status.HostReady && status.SocketReady && bindingEnabled && status.SessionID != ""
	return status, nil
}

func InstallHerdr(ctx context.Context, paths Paths, socketPath, binaryPath, version string) (HerdrStatus, error) {
	config, err := LoadConfig(paths.ConfigFile)
	if err != nil {
		return HerdrStatus{}, errors.New("notification agent is not configured")
	}
	if err := HerdrBackendSupported(ctx, &http.Client{Timeout: 5 * time.Second}, config.Endpoint); err != nil {
		return HerdrStatus{}, err
	}
	info, err := ReadDaemonInfo(ctx, paths.Socket)
	if err != nil {
		return HerdrStatus{}, errors.New("notification daemon is not running")
	}
	capable, hostCapture := false, false
	for _, capability := range info.Capabilities {
		if capability == HerdrCapability {
			capable = true
		}
		if capability == HerdrHostCaptureCapability {
			hostCapture = true
		}
	}
	if !capable || !hostCapture {
		return HerdrStatus{}, errors.New("running notification daemon requires an upgrade")
	}
	manifest, err := HerdrManifest(binaryPath)
	if err != nil {
		return HerdrStatus{}, err
	}
	directory := filepath.Join(paths.ConfigDir, "herdr-plugin")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return HerdrStatus{}, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return HerdrStatus{}, err
	}
	manifestPath := filepath.Join(directory, "herdr-plugin.toml")
	if err := os.WriteFile(manifestPath+".tmp", []byte(manifest), 0o600); err != nil {
		return HerdrStatus{}, err
	}
	if err := os.Rename(manifestPath+".tmp", manifestPath); err != nil {
		return HerdrStatus{}, err
	}
	queue, err := OpenQueue(paths.Database)
	if err != nil {
		return HerdrStatus{}, err
	}
	defer queue.Close()
	if _, err := herdrPluginCommand(ctx, "link", directory, "--enabled"); err != nil {
		return HerdrStatus{}, err
	}
	if err := queue.SetHerdrCaptureEnabled(ctx, true); err != nil {
		return HerdrStatus{}, err
	}
	// Baseline every existing server. Future/custom sessions register through
	// the plugin startup/status hooks without a phone opening that session.
	sockets := HerdrSockets()
	if socketPath != "" {
		sockets = append(sockets, socketPath)
	}
	for _, socket := range sockets {
		agents, incarnation, err := ReadHerdrAgents(ctx, socket)
		if err != nil {
			continue
		}
		binding, err := queue.BindHerdr(ctx, socket, ClockNow())
		if err != nil {
			return HerdrStatus{}, err
		}
		if err := queue.ReconcileHerdr(ctx, binding, agents, incarnation, true, ClockNow()); err != nil {
			return HerdrStatus{}, err
		}
	}
	return InspectHerdr(ctx, paths, socketPath, version)
}

func UninstallHerdr(ctx context.Context, paths Paths) error {
	// Retain opaque bindings for existing phones, but discard stale observations
	// and unsent Herdr hints. The shared Codex daemon, queue and hooks remain.
	queue, err := OpenQueue(paths.Database)
	if err != nil {
		return err
	}
	defer queue.Close()
	if err := queue.SetHerdrCaptureEnabled(ctx, false); err != nil {
		return err
	}
	_, err = herdrPluginCommand(ctx, "uninstall", HerdrPluginID)
	return err
}
