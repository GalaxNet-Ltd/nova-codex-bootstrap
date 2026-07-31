package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/protocol"
)

const MaxIPCMessage = 16 * 1024

func SendIPC(ctx context.Context, socketPath string, event protocol.HookEvent) error {
	connection, err := dialIPC(ctx, socketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(event); err != nil {
		return err
	}
	var response protocol.IPCResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return err
	}
	if !response.Accepted {
		return errors.New("daemon rejected hook event")
	}
	return nil
}

func ReadDaemonInfo(ctx context.Context, socketPath string) (protocol.DaemonInfoResponse, error) {
	connection, err := dialIPC(ctx, socketPath)
	if err != nil {
		return protocol.DaemonInfoResponse{}, err
	}
	defer connection.Close()
	request := protocol.DaemonInfoRequest{Type: protocol.DaemonInfoRequestType}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return protocol.DaemonInfoResponse{}, err
	}
	var response protocol.DaemonInfoResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return protocol.DaemonInfoResponse{}, err
	}
	if response.Error != "" {
		return protocol.DaemonInfoResponse{}, errors.New(response.Error)
	}
	if response.Version == "" {
		return protocol.DaemonInfoResponse{}, errors.New("daemon did not report a version")
	}
	return response, nil
}

func dialIPC(ctx context.Context, socketPath string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	_ = connection.SetDeadline(time.Now().Add(500 * time.Millisecond))
	return connection, nil
}

func ListenIPC(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("refusing to replace non-socket IPC path")
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
