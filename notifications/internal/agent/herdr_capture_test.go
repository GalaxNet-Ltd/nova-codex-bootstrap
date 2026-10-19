package agent

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestHerdrCaptureIsGlobalExplicitAndCannotBeReenabledByALateBinding(t *testing.T) {
	ctx := context.Background()
	queue, err := OpenQueue(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	socket := filepath.Join(t.TempDir(), "session.sock")
	if enabled, err := queue.HerdrCaptureEnabled(ctx); err != nil || enabled {
		t.Fatal("fresh agent silently enabled Herdr")
	}
	if _, err := queue.BindHerdr(ctx, socket, time.Now()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("binding bypassed capture consent")
	}
	if err := queue.SetHerdrCaptureEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	binding, err := queue.BindHerdr(ctx, socket, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.SetHerdrCaptureEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.BindHerdr(ctx, socket, time.Now()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("late hook recreated an enabled binding after uninstall")
	}
	if err := queue.ReconcileHerdr(ctx, binding, []HerdrAgent{{PaneID: "pane", TerminalID: "terminal", Agent: "claude", Status: "blocked", Sequence: 1}}, "incarnation", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if depth, _ := queue.Depth(ctx); depth != 0 {
		t.Fatal("disabled capture queued a late hint")
	}
	if err := queue.SetHerdrCaptureEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	reused, err := queue.BindHerdr(ctx, socket, time.Now())
	if err != nil || reused.SessionID != binding.SessionID {
		t.Fatal("reinstall replaced the opaque session identity")
	}
}

func TestHerdrCaptureMigrationPreservesLegacyBindingsAndUninstall(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "installed", false: "uninstalled"}[enabled], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "agent.db")
			queue, err := OpenQueue(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := queue.SetHerdrCaptureEnabled(ctx, true); err != nil {
				t.Fatal(err)
			}
			binding, err := queue.BindHerdr(ctx, "/local/herdr.sock", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !enabled {
				if err := queue.SetHerdrCaptureEnabled(ctx, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := queue.db.Exec(`DROP TABLE herdr_adapter`); err != nil {
				t.Fatal(err)
			}
			queue.Close()
			queue, err = OpenQueue(path)
			if err != nil {
				t.Fatal(err)
			}
			defer queue.Close()
			if migrated, err := queue.HerdrCaptureEnabled(ctx); err != nil || migrated != enabled {
				t.Fatal("migration changed prior integration intent")
			}
			saved, err := queue.HerdrBinding(ctx, binding.BindingKey)
			if err != nil || saved.SessionID != binding.SessionID || saved.Enabled != enabled {
				t.Fatal("migration changed a legacy binding")
			}
		})
	}
}

func TestHerdrStartupHintRegistersItsLocalSocketWithoutSessionSelection(t *testing.T) {
	event, err := NormalizeHerdrHook("startup", "", "/local/custom-herdr.sock")
	if err != nil || !event.Baseline || event.PaneID != "" || event.SocketPath != "/local/custom-herdr.sock" || event.BindingKey != HerdrBindingKey(event.SocketPath) {
		t.Fatal("startup cannot discover its originating server")
	}
	for _, socket := range []string{"relative.sock", "/local/bad\n.sock", "/local/bad\x00.sock"} {
		if _, err := NormalizeHerdrHook("startup", "", socket); err == nil {
			t.Fatal("invalid local startup locator accepted")
		}
	}
}
