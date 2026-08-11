package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/signing"
)

type Daemon struct {
	Config               Config
	ConfigPath           string
	LegacySetupTokenPath string
	EnrollmentLock       string
	PrivateKey           ed25519.PrivateKey
	Queue                *Queue
	SocketPath           string
	Version              string
	Client               *http.Client
	now                  func() time.Time
	wake                 chan struct{}
	configMu             sync.RWMutex
	enrollmentPoll       time.Duration
	enrollmentMin        time.Duration
	enrollmentMax        time.Duration
}

func (d *Daemon) Run(ctx context.Context) error {
	d.setDefaults()
	listener, err := ListenIPC(d.SocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		d.uploadLoop(ctx)
	}()
	go func() {
		defer workers.Done()
		d.enrollmentLoop(ctx)
	}()
	defer workers.Wait()

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go d.handleConnection(ctx, connection)
	}
}

func (d *Daemon) handleConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if err := verifyPeer(connection); err != nil {
		_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "unauthorized peer"})
		return
	}
	decoder := json.NewDecoder(io.LimitReader(connection, MaxIPCMessage))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "invalid event"})
		return
	}
	var infoRequest protocol.DaemonInfoRequest
	if json.Unmarshal(raw, &infoRequest) == nil && infoRequest.Type == protocol.DaemonInfoRequestType {
		depth, err := d.Queue.Depth(ctx)
		if err != nil {
			_ = json.NewEncoder(connection).Encode(protocol.DaemonInfoResponse{Error: "queue unavailable"})
			return
		}
		_ = json.NewEncoder(connection).Encode(protocol.DaemonInfoResponse{
			Version:           d.Version,
			QueueDepth:        depth,
			RegistrationState: d.configSnapshot().RegistrationState,
		})
		return
	}
	var event protocol.HookEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "invalid event"})
		return
	}
	if err := validateHookEvent(event); err != nil {
		_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "unsupported event"})
		return
	}

	hostEvent := protocol.HostEvent{
		SchemaVersion: protocol.Version,
		EventType:     event.EventType,
		ThreadID:      event.ThreadID,
		TurnID:        event.TurnID,
		RequestID:     event.RequestID,
		OccurredAt:    event.OccurredAt,
	}
	body, err := json.Marshal(hostEvent)
	if err != nil {
		_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "encode event"})
		return
	}
	eventID, err := d.eventID(event)
	if err != nil {
		_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "create event id"})
		return
	}
	if err := d.Queue.Enqueue(ctx, eventID, body, d.now()); err != nil {
		_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "queue unavailable"})
		return
	}
	_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Accepted: true})
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func validateHookEvent(event protocol.HookEvent) error {
	if event.ProtocolVersion != protocol.Version || !event.EventType.Valid() || event.ThreadID == "" || event.TurnID == "" || event.OccurredAt.IsZero() {
		return errors.New("invalid hook event")
	}
	return nil
}

func (d *Daemon) eventID(event protocol.HookEvent) (string, error) {
	if event.RequestID == "" {
		return NewUUID()
	}
	canonical := strings.Join([]string{d.configSnapshot().HostID, string(event.EventType), event.ThreadID, event.TurnID, event.RequestID}, "\n")
	hash := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(hash[:]), nil
}

func (d *Daemon) uploadLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		d.uploadDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.wake:
		}
	}
}

func (d *Daemon) uploadDue(ctx context.Context) {
	for ctx.Err() == nil {
		if d.configSnapshot().RegistrationState != RegistrationActive {
			return
		}
		event, err := d.Queue.Next(ctx, d.now())
		if errors.Is(err, sql.ErrNoRows) || err != nil {
			return
		}
		if err := d.upload(ctx, event); err == nil {
			_ = d.Queue.Complete(ctx, event.EventID)
			continue
		}
		attempts := event.Attempts + 1
		delay := time.Duration(math.Min(math.Pow(2, float64(attempts)), 3600)) * time.Second
		_ = d.Queue.Retry(ctx, event.EventID, attempts, d.now().Add(delay))
		return
	}
}

func (d *Daemon) upload(ctx context.Context, event QueuedEvent) error {
	config := d.configSnapshot()
	endpoint, err := parseNotificationEndpoint(config.Endpoint)
	if err != nil {
		return err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/host-events"
	timestamp := d.now().Unix()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(event.Body)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-NovaScale-Host", config.HostID)
	request.Header.Set("X-NovaScale-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-NovaScale-Event-ID", event.EventID)
	request.Header.Set("X-NovaScale-Signature", signing.Sign(d.PrivateKey, timestamp, event.EventID, event.Body))
	response, err := d.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if (response.StatusCode >= 200 && response.StatusCode < 300) || response.StatusCode == http.StatusConflict {
		return nil
	}
	return errors.New("event upload rejected: " + response.Status)
}
