package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
	if info.Version != "1.2.3-test" || info.QueueDepth != 1 {
		t.Fatalf("daemon info = %+v", info)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testResponse(statusCode int, status string) *http.Response {
	return &http.Response{StatusCode: statusCode, Status: status, Body: io.NopCloser(emptyReader{})}
}

type emptyReader struct{}

func (emptyReader) Read(_ []byte) (int, error) { return 0, io.EOF }
