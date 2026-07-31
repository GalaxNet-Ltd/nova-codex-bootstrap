package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/signing"
)

type RegisterRequest struct {
	HostID       string `json:"hostId"`
	PublicKey    string `json:"publicKey"`
	AgentVersion string `json:"agentVersion"`
}

type RegistrationError struct {
	StatusCode int
}

func (e *RegistrationError) Error() string {
	return "host registration rejected with HTTP status " + strconv.Itoa(e.StatusCode)
}

func (e *RegistrationError) Permanent() bool {
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return e.StatusCode >= 300 && e.StatusCode < 500
}

func RegisterHost(ctx context.Context, client *http.Client, endpoint, setupToken, hostID string, privateKey ed25519.PrivateKey, version string, now time.Time) error {
	if !validSetupToken(setupToken) {
		return errors.New("invalid setup token")
	}
	base, err := parseNotificationEndpoint(endpoint)
	if err != nil {
		return err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/v1/hosts/register"
	body, err := json.Marshal(RegisterRequest{
		HostID:       hostID,
		PublicKey:    base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		AgentVersion: version,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+setupToken)
	request.Header.Set("X-NovaScale-Timestamp", strconv.FormatInt(now.Unix(), 10))
	request.Header.Set("X-NovaScale-Signature", signing.Sign(privateKey, now.Unix(), registrationSignatureID(hostID), body))
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &RegistrationError{StatusCode: response.StatusCode}
	}
	return nil
}

func parseNotificationEndpoint(raw string) (*url.URL, error) {
	base, err := url.Parse(raw)
	if err != nil || base.Host == "" || base.User != nil || base.Fragment != "" {
		return nil, errors.New("invalid notification endpoint")
	}
	switch strings.ToLower(base.Scheme) {
	case "https":
		return base, nil
	case "http":
		host := strings.TrimSuffix(base.Hostname(), ".")
		if strings.EqualFold(host, "localhost") {
			return base, nil
		}
		if address := net.ParseIP(host); address != nil && address.IsLoopback() {
			return base, nil
		}
	}
	return nil, errors.New("notification endpoint must use HTTPS outside loopback development")
}

func registrationSignatureID(hostID string) string {
	return "host-registration:" + hostID
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}
