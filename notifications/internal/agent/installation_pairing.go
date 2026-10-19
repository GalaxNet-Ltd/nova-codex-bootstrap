package agent

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/signing"
	"strings"
	"time"
)

const InstallationPairingCapability = "installation-pairing-v1"

// This bounded proof authorizes only the requested phone and source. The host
// key stays local. The backend verifies freshness and consumes its nonce once.
func InstallationPairingProof(config Config, key ed25519.PrivateKey, installationID, source string, now time.Time) (map[string]string, error) {
	if installationID == "" || len(installationID) > 256 || strings.ContainsAny(installationID, "\r\n\x00/\\") || (source != "codex" && source != "herdr") || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid installation pairing request")
	}
	nonce, err := NewUUID()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		HostID         string `json:"hostId"`
		InstallationID string `json:"installationId"`
		Source         string `json:"source"`
		Nonce          string `json:"nonce"`
		Timestamp      int64  `json:"timestamp"`
	}{config.HostID, installationID, source, nonce, now.Unix()})
	if err != nil {
		return nil, err
	}
	return map[string]string{"payload": base64.RawURLEncoding.EncodeToString(body), "signature": signing.Sign(key, now.Unix(), "installation-pairing:"+nonce, body)}, nil
}
