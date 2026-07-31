package agent

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxSetupTokenFileSize = 256

func ReadSetupTokenFile(path string) (string, error) {
	if path == "" {
		return "", errors.New("setup-token file is required")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return "", errors.New("setup-token input must be a protected regular file")
	}
	permissions := pathInfo.Mode().Perm()
	if permissions != 0o400 && permissions != 0o600 {
		return "", errors.New("setup-token file permissions must be 0400 or 0600")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("setup-token file could not be opened")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return "", errors.New("setup-token input changed while it was opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSetupTokenFileSize+1))
	if err != nil || len(data) == 0 || len(data) > maxSetupTokenFileSize {
		return "", errors.New("setup-token file has an invalid size")
	}
	token := strings.TrimRight(string(data), "\r\n")
	if strings.TrimSpace(token) != token || !validSetupToken(token) {
		return "", errors.New("setup-token file does not contain a valid token")
	}
	return token, nil
}

func validSetupToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32
}

// StageSetupToken copies a caller-owned setup token into the agent's protected
// configuration directory so the daemon can retry enrollment across restarts.
func StageSetupToken(sourcePath, destinationPath string) error {
	token, err := ReadSetupTokenFile(sourcePath)
	if err != nil {
		return err
	}
	directory := filepath.Dir(destinationPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".pending-setup-token-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := fmt.Fprintln(temporary, token); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}
