package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/signing"
	"testing"
	"time"
)

func TestInstallationPairingProofBindsPhoneSourceAndPreservesKey(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	original := append([]byte(nil), key...)
	now := time.Now()
	config := Config{HostID: "existing-agent-id"}
	previousNonce := ""
	for _, source := range []string{"codex", "herdr"} {
		proof, err := InstallationPairingProof(config, key, "phone-id", source, now)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(proof["payload"])
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			HostID         string `json:"hostId"`
			InstallationID string `json:"installationId"`
			Source         string `json:"source"`
			Nonce          string `json:"nonce"`
			Timestamp      int64  `json:"timestamp"`
		}
		if err = json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body.HostID != config.HostID || body.InstallationID != "phone-id" || body.Source != source || body.Timestamp != now.Unix() || body.Nonce == "" || body.Nonce == previousNonce {
			t.Fatal("proof not correctly bound")
		}
		if err = signing.Verify(public, body.Timestamp, "installation-pairing:"+body.Nonce, raw, proof["signature"]); err != nil {
			t.Fatal(err)
		}
		previousNonce = body.Nonce
	}
	if string(original) != string(key) {
		t.Fatal("proof mutated agent identity")
	}
	for _, id := range []string{"", "../bad", "bad\nidentity"} {
		if _, err := InstallationPairingProof(config, key, id, "codex", now); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
	if _, err := InstallationPairingProof(config, key, "phone", "other", now); err == nil {
		t.Fatal("invalid source accepted")
	}
}
