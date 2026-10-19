package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestHerdrHookAlwaysReturnsNeutralSuccessForMalformedInputOrMissingDaemon(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	for _, arguments := range [][]string{{"--unknown"}, {"--socket", "/tmp/novascale-no-such-daemon.sock"}} {
		readEnd, writeEnd, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		original := os.Stdout
		os.Stdout = writeEnd
		runHerdrHook(arguments)
		writeEnd.Close()
		os.Stdout = original
		output, err := io.ReadAll(readEnd)
		readEnd.Close()
		if err != nil || string(output) != "{}\n" {
			t.Fatal("Herdr hook supplied a verdict or failed its neutral response")
		}
	}
}
