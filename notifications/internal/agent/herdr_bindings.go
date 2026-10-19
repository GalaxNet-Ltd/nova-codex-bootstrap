package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

const HerdrBindingsCapability = "herdr-bindings-v1"

// Bindings map Herdr's session names to opaque notification routing identities.
// Only the validated name is also permitted as optional display metadata.
type HerdrBindingRoute struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
}

type HerdrBindingRoutes struct {
	HostID   string              `json:"hostId"`
	Endpoint string              `json:"endpoint"`
	Bindings []HerdrBindingRoute `json:"bindings"`
}

func ReadHerdrBindingRoutes(ctx context.Context, paths Paths) (HerdrBindingRoutes, error) {
	config, err := LoadConfig(paths.ConfigFile)
	if err != nil {
		return HerdrBindingRoutes{}, err
	}
	result := HerdrBindingRoutes{HostID: config.HostID, Endpoint: config.Endpoint, Bindings: []HerdrBindingRoute{}}
	if _, err := os.Stat(paths.Database); err != nil {
		return HerdrBindingRoutes{}, err
	}
	queue, err := OpenQueue(paths.Database)
	if err != nil {
		return HerdrBindingRoutes{}, err
	}
	defer queue.Close()
	bindings, err := queue.HerdrBindings(ctx)
	if err != nil {
		return HerdrBindingRoutes{}, err
	}
	for _, binding := range bindings {
		name := herdrBindingSessionName(binding.SocketPath)
		if name == "" {
			continue
		}
		result.Bindings = append(result.Bindings, HerdrBindingRoute{SessionID: binding.SessionID, SessionName: name})
	}
	return result, nil
}

// Match Herdr's SessionInfo.name and managed session layout. Custom socket
// locators have no authoritative managed name and must never become labels.
func herdrBindingSessionName(socketPath string) string {
	defaultSocket, err := ResolveHerdrSocket("default", "")
	if err != nil {
		return ""
	}
	if filepath.Clean(socketPath) == defaultSocket {
		return "default"
	}
	sessionRoot := filepath.Join(filepath.Dir(defaultSocket), "sessions")
	relative, err := filepath.Rel(sessionRoot, filepath.Clean(socketPath))
	parts := strings.Split(relative, string(filepath.Separator))
	if err != nil || len(parts) != 2 || parts[1] != "herdr.sock" || !protocol.ValidHerdrSessionName(parts[0]) {
		return ""
	}
	canonical, err := ResolveHerdrSocket(parts[0], "")
	if err != nil || canonical != filepath.Clean(socketPath) {
		return ""
	}
	return parts[0]
}
