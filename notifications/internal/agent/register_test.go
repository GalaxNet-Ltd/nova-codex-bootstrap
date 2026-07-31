package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/signing"
)

func TestRegisterHostProvesPrivateKeyPossession(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return nil, err
		}
		var registration RegisterRequest
		if err := json.Unmarshal(body, &registration); err != nil {
			t.Error(err)
		}
		if registration.HostID != "host-1" || registration.PublicKey != base64.RawURLEncoding.EncodeToString(publicKey) {
			t.Errorf("unexpected registration: %+v", registration)
		}
		if request.Header.Get("Authorization") != "" {
			t.Error("registration unexpectedly sent bearer authorization")
		}
		if err := signing.Verify(publicKey, now.Unix(), registrationSignatureID(registration.HostID), body, request.Header.Get("X-NovaScale-Signature")); err != nil {
			t.Errorf("registration signature failed: %v", err)
		}
		return &http.Response{StatusCode: http.StatusCreated, Status: "201 Created", Body: http.NoBody, Header: make(http.Header)}, nil
	})}

	if err := RegisterHost(context.Background(), client, "https://notify.example.com", "host-1", privateKey, "test", now); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationEndpointRequiresHTTPSOutsideLoopback(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		valid    bool
	}{
		{name: "production HTTPS", endpoint: "https://notify.example.com", valid: true},
		{name: "IPv4 loopback development", endpoint: "http://127.0.0.1:8080", valid: true},
		{name: "IPv6 loopback development", endpoint: "http://[::1]:8080", valid: true},
		{name: "localhost development", endpoint: "http://localhost:8080", valid: true},
		{name: "remote HTTP", endpoint: "http://notify.example.com", valid: false},
		{name: "embedded credentials", endpoint: "https://user@example.com", valid: false},
		{name: "unsupported scheme", endpoint: "ftp://notify.example.com", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseNotificationEndpoint(test.endpoint)
			if (err == nil) != test.valid {
				t.Fatalf("parseNotificationEndpoint(%q) error = %v, valid = %v", test.endpoint, err, test.valid)
			}
		})
	}
}
