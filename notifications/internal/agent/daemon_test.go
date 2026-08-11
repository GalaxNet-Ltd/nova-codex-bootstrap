package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/signing"
)

func TestDaemonQueueSignsAndUploadsEvent(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uploaded := make(chan struct{}, 1)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Error(readErr)
			return nil, readErr
		}
		timestamp, parseErr := strconv.ParseInt(request.Header.Get("X-NovaScale-Timestamp"), 10, 64)
		if parseErr != nil || signing.Verify(publicKey, timestamp, request.Header.Get("X-NovaScale-Event-ID"), body, request.Header.Get("X-NovaScale-Signature")) != nil {
			t.Error("backend received an invalid host signature")
			return testResponse(http.StatusUnauthorized, "401 Unauthorized"), nil
		}
		uploaded <- struct{}{}
		return testResponse(http.StatusAccepted, "202 Accepted"), nil
	})}

	directory := t.TempDir()
	queue, err := OpenQueue(filepath.Join(directory, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	daemon := &Daemon{
		Config:     Config{Version: 1, HostID: "host-1", Endpoint: "https://notify.test", RegistrationState: "active"},
		PrivateKey: privateKey, Queue: queue, Client: client, now: time.Now,
	}
	event := protocol.HostEvent{
		SchemaVersion: 1, EventType: protocol.EventTurnStopped,
		ThreadID: "thread-1", TurnID: "turn-1", OccurredAt: time.Now().UTC(),
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(context.Background(), "event-1", body, time.Now()); err != nil {
		t.Fatal(err)
	}
	daemon.uploadDue(context.Background())
	select {
	case <-uploaded:
	case <-time.After(time.Second):
		depth, _ := queue.Depth(context.Background())
		t.Fatalf("event was not uploaded; queue depth=%d", depth)
	}
}

func TestDaemonReportsLiveVersionAndQueueDepth(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "novascale-agent-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove temporary directory: %v", err)
		}
	})
	queue, err := OpenQueue(filepath.Join(directory, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	if err := queue.Enqueue(context.Background(), "event-1", []byte(`{"test":true}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := queue.Retry(context.Background(), "event-1", 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	socketPath := filepath.Join(directory, "agent.sock")
	daemon := &Daemon{
		Config: Config{Version: 1, HostID: "host-1", Endpoint: "https://notify.test", RegistrationState: "active"},
		Queue:  queue, SocketPath: socketPath, Version: "1.2.3-test",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx)
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon shutdown: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("daemon did not stop")
		}
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}

	queryContext, queryCancel := context.WithTimeout(context.Background(), time.Second)
	defer queryCancel()
	info, err := ReadDaemonInfo(queryContext, socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.2.3-test" || info.QueueDepth != 1 || info.RegistrationState != RegistrationActive {
		t.Fatalf("daemon info = %+v", info)
	}
}

func TestDaemonEnrollmentRetriesThenReleasesQueuedEvents(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("/tmp", "novascale-enrollment-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	configPath := filepath.Join(directory, "config.json")
	tokenPath := filepath.Join(directory, "pending-setup-token")
	socketPath := filepath.Join(directory, "agent.sock")
	config := Config{
		Version: 1, HostID: "host-1", Endpoint: "https://notify.test",
		RegistrationState: RegistrationPending,
	}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("obsolete-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enrollmentToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	queue, err := OpenQueue(filepath.Join(directory, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	eventBody, err := json.Marshal(protocol.HostEvent{
		SchemaVersion: 1, EventType: protocol.EventTurnStopped,
		ThreadID: "thread-1", TurnID: "turn-1", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(context.Background(), "event-1", eventBody, time.Now()); err != nil {
		t.Fatal(err)
	}

	var intentAttempts, registrationAttempts int
	eventUploaded := make(chan struct{}, 1)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/v1/hosts/enrollment-intents":
			intentAttempts++
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Error(readErr)
			}
			timestamp, parseErr := strconv.ParseInt(request.Header.Get("X-NovaScale-Timestamp"), 10, 64)
			if parseErr != nil || signing.Verify(publicKey, timestamp, hostEnrollmentIntentSignatureID(config.HostID), body, request.Header.Get("X-NovaScale-Signature")) != nil {
				t.Error("enrollment intent signature was invalid")
			}
			return testJSONResponse(http.StatusCreated, map[string]any{
				"enrollmentToken": enrollmentToken,
				"expiresAt":       time.Now().Add(time.Hour).UTC(),
			}), nil
		case "/v1/hosts/register":
			registrationAttempts++
			if request.Header.Get("Authorization") != "Bearer "+enrollmentToken {
				t.Error("registration did not use the autonomous enrollment token")
			}
			if registrationAttempts == 1 {
				return testResponse(http.StatusServiceUnavailable, "503 Service Unavailable"), nil
			}
			return testResponse(http.StatusCreated, "201 Created"), nil
		case "/v1/host-events":
			timestamp, parseErr := strconv.ParseInt(request.Header.Get("X-NovaScale-Timestamp"), 10, 64)
			if parseErr != nil || signing.Verify(publicKey, timestamp, request.Header.Get("X-NovaScale-Event-ID"), eventBody, request.Header.Get("X-NovaScale-Signature")) != nil {
				t.Error("queued event signature was invalid")
			}
			select {
			case eventUploaded <- struct{}{}:
			default:
			}
			return testResponse(http.StatusAccepted, "202 Accepted"), nil
		default:
			t.Errorf("unexpected request path %q", request.URL.Path)
			return testResponse(http.StatusNotFound, "404 Not Found"), nil
		}
	})}
	daemon := &Daemon{
		Config: config, ConfigPath: configPath, LegacySetupTokenPath: tokenPath,
		PrivateKey: privateKey, Queue: queue, SocketPath: socketPath,
		Version: "test", Client: client,
		enrollmentPoll: 5 * time.Millisecond, enrollmentMin: 5 * time.Millisecond,
		enrollmentMax: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()
	select {
	case <-eventUploaded:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("daemon did not enroll and release its queued event")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if registrationAttempts < 2 {
		t.Fatalf("registration attempts = %d, want at least 2", registrationAttempts)
	}
	if intentAttempts != registrationAttempts {
		t.Fatalf("intent attempts = %d, registration attempts = %d", intentAttempts, registrationAttempts)
	}
	saved, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RegistrationState != RegistrationActive {
		t.Fatalf("registration state = %q, want active", saved.RegistrationState)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("pending setup token still exists after enrollment: %v", err)
	}
}

func TestDaemonEnrollmentSurvivesRestartAndRecordsIdentityConflict(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	tokenPath := filepath.Join(directory, "pending-setup-token")
	config := Config{
		Version: 1, HostID: "host-1", Endpoint: "https://notify.test",
		RegistrationState: RegistrationPending,
	}
	if err := SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("obsolete-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enrollmentToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	transient := &Daemon{
		Config: config, ConfigPath: configPath, LegacySetupTokenPath: tokenPath,
		PrivateKey: privateKey, Version: "test", now: time.Now,
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return testResponse(http.StatusBadGateway, "502 Bad Gateway"), nil
		})},
	}
	if result := transient.attemptEnrollment(context.Background()); result != enrollmentRetry {
		t.Fatalf("transient enrollment result = %v, want retry", result)
	}
	if _, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("transient failure removed legacy token before enrollment resolved: %v", err)
	}

	saved, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	resumed := &Daemon{
		Config: saved, ConfigPath: configPath, LegacySetupTokenPath: tokenPath,
		PrivateKey: privateKey, Version: "test", now: time.Now,
		Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case "/v1/hosts/enrollment-intents":
				return testJSONResponse(http.StatusCreated, map[string]any{
					"enrollmentToken": enrollmentToken,
					"expiresAt":       time.Now().Add(time.Hour).UTC(),
				}), nil
			case "/v1/hosts/register":
				return testResponse(http.StatusCreated, "201 Created"), nil
			default:
				return testResponse(http.StatusNotFound, "404 Not Found"), nil
			}
		})},
	}
	if result := resumed.attemptEnrollment(context.Background()); result != enrollmentSucceeded {
		t.Fatalf("resumed enrollment result = %v, want success", result)
	}

	rejectionTokenPath := filepath.Join(directory, "rejected-setup-token")
	if err := os.WriteFile(rejectionTokenPath, []byte("obsolete-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rejectedConfig := saved
	rejectedConfig.RegistrationState = RegistrationPending
	if err := SaveConfig(configPath, rejectedConfig); err != nil {
		t.Fatal(err)
	}
	rejected := &Daemon{
		Config: rejectedConfig, ConfigPath: configPath, LegacySetupTokenPath: rejectionTokenPath,
		PrivateKey: privateKey, Version: "test", now: time.Now,
		Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case "/v1/hosts/enrollment-intents":
				return testJSONResponse(http.StatusCreated, map[string]any{
					"enrollmentToken": enrollmentToken,
					"expiresAt":       time.Now().Add(time.Hour).UTC(),
				}), nil
			case "/v1/hosts/register":
				return testResponse(http.StatusConflict, "409 Conflict"), nil
			default:
				return testResponse(http.StatusNotFound, "404 Not Found"), nil
			}
		})},
	}
	if result := rejected.attemptEnrollment(context.Background()); result != enrollmentWaiting {
		t.Fatalf("rejected enrollment result = %v, want waiting", result)
	}
	rejectedSaved, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if rejectedSaved.RegistrationState != RegistrationIdentityConflict {
		t.Fatalf("rejected registration state = %q", rejectedSaved.RegistrationState)
	}
	if _, err := os.Stat(rejectionTokenPath); !os.IsNotExist(err) {
		t.Fatalf("rejected setup token still exists: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testResponse(statusCode int, status string) *http.Response {
	return &http.Response{StatusCode: statusCode, Status: status, Body: io.NopCloser(emptyReader{}), Header: make(http.Header)}
}

func testJSONResponse(statusCode int, value any) *http.Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     strconv.Itoa(statusCode),
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}
}

type emptyReader struct{}

func (emptyReader) Read(_ []byte) (int, error) { return 0, io.EOF }
