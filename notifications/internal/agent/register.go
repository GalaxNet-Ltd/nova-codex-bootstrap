package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
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

type enrollmentTokenResponse struct {
	EnrollmentToken string    `json:"enrollmentToken"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type RegistrationError struct {
	StatusCode int
}

func (e *RegistrationError) Error() string {
	return "host enrollment request rejected with HTTP status " + strconv.Itoa(e.StatusCode)
}

func RequestHostEnrollmentToken(ctx context.Context, client *http.Client, endpoint, hostID string, privateKey ed25519.PrivateKey, version string, now time.Time) (string, error) {
	body, err := registrationBody(hostID, privateKey, version)
	if err != nil {
		return "", err
	}
	base, err := parseNotificationEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/v1/hosts/enrollment-intents"
	request, err := signedEnrollmentRequest(ctx, base.String(), body, hostEnrollmentIntentSignatureID(hostID), privateKey, now)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &RegistrationError{StatusCode: response.StatusCode}
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(responseBody) > 4096 {
		return "", errors.New("invalid host enrollment response")
	}
	var decoded enrollmentTokenResponse
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil || !validEnrollmentToken(decoded.EnrollmentToken) || !decoded.ExpiresAt.After(now) {
		return "", errors.New("invalid host enrollment response")
	}
	return decoded.EnrollmentToken, nil
}

func RegisterHost(ctx context.Context, client *http.Client, endpoint, enrollmentToken, hostID string, privateKey ed25519.PrivateKey, version string, now time.Time) error {
	if !validEnrollmentToken(enrollmentToken) {
		return errors.New("invalid host enrollment token")
	}
	base, err := parseNotificationEndpoint(endpoint)
	if err != nil {
		return err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/v1/hosts/register"
	body, err := registrationBody(hostID, privateKey, version)
	if err != nil {
		return err
	}
	request, err := signedEnrollmentRequest(ctx, base.String(), body, registrationSignatureID(hostID), privateKey, now)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+enrollmentToken)
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

func registrationBody(hostID string, privateKey ed25519.PrivateKey, version string) ([]byte, error) {
	return json.Marshal(RegisterRequest{
		HostID:       hostID,
		PublicKey:    base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		AgentVersion: version,
	})
}

func signedEnrollmentRequest(
	ctx context.Context,
	endpoint string,
	body []byte,
	signatureID string,
	privateKey ed25519.PrivateKey,
	now time.Time,
) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-NovaScale-Timestamp", strconv.FormatInt(now.Unix(), 10))
	request.Header.Set("X-NovaScale-Signature", signing.Sign(privateKey, now.Unix(), signatureID, body))
	return request, nil
}

func validEnrollmentToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32
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

func hostEnrollmentIntentSignatureID(hostID string) string {
	return "host-enrollment-intent:" + hostID
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}
