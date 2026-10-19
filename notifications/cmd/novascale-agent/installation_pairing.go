package main

import (
	"encoding/json"
	"errors"
	"flag"
	"github.com/GalaxNet-Ltd/nova-codex-bootstrap/internal/agent"
	"io"
	"os"
)

func runInstallationPairing(arguments []string) error {
	paths, err := agent.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("authorize-installation", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	installation := flags.String("installation", "", "phone installation ID")
	source := flags.String("source", "", "codex or herdr")
	if err = flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return errors.New("invalid installation pairing arguments")
	}
	config, err := agent.LoadConfig(paths.ConfigFile)
	if err != nil {
		return err
	}
	key, err := agent.LoadPrivateKey(paths.KeyFile)
	if err != nil {
		return err
	}
	proof, err := agent.InstallationPairingProof(config, key, *installation, *source, agent.ClockNow())
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(proof)
}
