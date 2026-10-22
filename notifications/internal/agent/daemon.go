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
	"path/filepath"
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
	herdrDirty           chan protocol.HerdrDirtyEvent
	configMu             sync.RWMutex
	enrollmentPoll       time.Duration
	enrollmentMin        time.Duration
	enrollmentMax        time.Duration
}

func (d *Daemon) Run(ctx context.Context) error {
	d.setDefaults()
	if err := d.Queue.ConfigureBackend(ctx, d.configSnapshot().Endpoint); err != nil {
		return err
	}
	d.herdrDirty = make(chan protocol.HerdrDirtyEvent, 128)
	listener, err := ListenIPC(d.SocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()

	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		d.uploadLoop(ctx)
	}()
	go func() {
		defer workers.Done()
		d.enrollmentLoop(ctx)
	}()
	go func() { defer workers.Done(); d.herdrLoop(ctx) }()
	defer workers.Wait()

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	connections := make(chan struct{}, 64)
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case connections <- struct{}{}:
			go func() { defer func() { <-connections }(); d.handleConnection(ctx, connection) }()
		default:
			connection.Close()
		}
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
			Capabilities:      []string{"codex-v1", HerdrCapability, HerdrBindingsCapability, HerdrHostCaptureCapability},
			QueueDepth:        depth,
			RegistrationState: d.configSnapshot().RegistrationState,
		})
		return
	}
	var dirty protocol.HerdrDirtyEvent
	if json.Unmarshal(raw, &dirty) == nil && dirty.Type == protocol.HerdrDirtyEventType {
		if len(dirty.BindingKey) != 64 || (!protocol.ValidID(dirty.PaneID) && !(dirty.Baseline && dirty.PaneID == "")) {
			_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "invalid Herdr signal"})
			return
		}
		if _, err := hex.DecodeString(dirty.BindingKey); err != nil {
			_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "invalid Herdr binding"})
			return
		}
		if dirty.SocketPath != "" && (len(dirty.SocketPath) > 4096 || !filepath.IsAbs(dirty.SocketPath) || strings.ContainsAny(dirty.SocketPath, "\x00\r\n") || HerdrBindingKey(dirty.SocketPath) != dirty.BindingKey) {
			_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "invalid Herdr socket"})
			return
		}
		select {
		case d.herdrDirty <- dirty:
			_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Accepted: true})
		default:
			_ = json.NewEncoder(connection).Encode(protocol.IPCResponse{Error: "Herdr signal queue full"})
		}
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
	hostEvent := protocol.HostEvent{SchemaVersion: event.ProtocolVersion, EventType: event.EventType, ThreadID: event.ThreadID, TurnID: event.TurnID, RequestID: event.RequestID, OccurredAt: event.OccurredAt}
	if event.ProtocolVersion != protocol.Version || !hostEvent.Valid() {
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
		config := d.configSnapshot()
		if config.RegistrationState != RegistrationActive {
			return
		}
		event, err := d.Queue.NextForBackend(ctx, d.now(), config.Endpoint)
		if errors.Is(err, sql.ErrNoRows) || err != nil {
			return
		}
		if err := d.uploadToBackend(ctx, event, config); err == nil {
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
	return d.uploadToBackend(ctx, event, d.configSnapshot())
}

func (d *Daemon) uploadToBackend(ctx context.Context, event QueuedEvent, config Config) error {
	endpoint, err := parseNotificationEndpoint(config.Endpoint)
	if err != nil {
		return err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/host-events"
	var hostEvent protocol.HostEvent
	if json.Unmarshal(event.Body, &hostEvent) == nil && hostEvent.EventSource() == protocol.SourceHerdr {
		endpoint.Path = strings.TrimSuffix(endpoint.Path, "/v1/host-events") + "/v2/host-events"
	}
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
	if response.StatusCode == http.StatusBadRequest && hostEvent.HerdrMetadata != nil {
		// Older v2 backends reject unknown fields. Retry the same event identity
		// once without optional presentation metadata, with a fresh signature.
		hostEvent.HerdrMetadata = nil
		body, err := json.Marshal(hostEvent)
		if err != nil {
			return err
		}
		event.Body = body
		response.Body.Close()
		return d.uploadToBackend(ctx, event, config)
	}
	if hostEvent.EventSource() == protocol.SourceHerdr && (response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnprocessableEntity || response.StatusCode == http.StatusNotFound) {
		// Permanent schema/freshness/capability rejection must not occupy the
		// shared queue indefinitely. Codex's existing retry behavior is retained.
		return nil
	}
	if hostEvent.EventType == protocol.EventTestNotification && response.StatusCode == http.StatusTooManyRequests {
		return nil
	}
	return errors.New("event upload rejected: " + response.Status)
}
