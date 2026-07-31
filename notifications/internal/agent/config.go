package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDir  string
	StateDir   string
	ConfigFile string
	KeyFile    string
	Database   string
	Socket     string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	configBase := os.Getenv("XDG_CONFIG_HOME")
	if configBase == "" {
		configBase = filepath.Join(home, ".config")
	}
	stateBase := os.Getenv("XDG_STATE_HOME")
	if stateBase == "" {
		stateBase = filepath.Join(home, ".local", "state")
	}
	configDir := filepath.Join(configBase, "novascale-agent")
	stateDir := filepath.Join(stateBase, "novascale-agent")
	return Paths{
		ConfigDir:  configDir,
		StateDir:   stateDir,
		ConfigFile: filepath.Join(configDir, "config.json"),
		KeyFile:    filepath.Join(configDir, "host-key"),
		Database:   filepath.Join(stateDir, "agent.db"),
		Socket:     filepath.Join(stateDir, "agent.sock"),
	}, nil
}

type Config struct {
	Version           int    `json:"version"`
	HostID            string `json:"hostId"`
	Endpoint          string `json:"endpoint"`
	RegistrationState string `json:"registrationState"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}
	if config.Version != 1 || config.HostID == "" || config.Endpoint == "" {
		return Config{}, errors.New("invalid agent configuration")
	}
	if _, err := parseNotificationEndpoint(config.Endpoint); err != nil {
		return Config{}, errors.New("invalid notification endpoint in agent configuration")
	}
	return config, nil
}

func SaveConfig(path string, config Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
