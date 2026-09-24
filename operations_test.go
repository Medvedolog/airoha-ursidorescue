package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ursidorescue/app"
)

func TestCatalogueRiskClasses(t *testing.T) {
	want := map[string]app.Risk{
		"stock-restore": app.Erase, "fip-repair": app.Write, "physical-restore": app.Erase,
		"itb-boot": app.NonPersistent, "diagnostics": app.ReadOnly, "support-bundle": app.ReadOnly,
		"ram-uboot": app.NonPersistent, "ubi-volume": app.Write, "raw-mtd": app.Write,
		"terminal": app.Manual, "shell": app.Manual,
	}
	for k, r := range want {
		op, ok := operationCatalog[k]
		if !ok || op.Risk != r || op.run == nil {
			t.Errorf("catalogue %q = %+v, want risk %s", k, op, r)
		}
	}
	if len(operationCatalog) != len(want) {
		t.Errorf("catalogue has %d entries, test knows %d", len(operationCatalog), len(want))
	}
}

func readSessionMeta(t *testing.T, work string) (string, map[string]any) {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(app.SessionsDir(work), "*"))
	if len(dirs) != 1 {
		t.Fatalf("want one session, got %v", dirs)
	}
	b, err := os.ReadFile(filepath.Join(dirs[0], "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return dirs[0], m
}

func TestRunOperationSessionLifecycle(t *testing.T) {
	cases := []struct {
		name   string
		run    func(a *App) error
		result string
	}{
		{"ok", func(a *App) error { a.event("hello"); return nil }, "success"},
		{"cancel", func(a *App) error { return a.confirm(app.Write, "WRITE FIP") }, "cancelled"},
		{"fail", func(a *App) error { return errors.New("boom") }, "failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work := t.TempDir()
			rec := &app.Recorder{}
			a := &App{work: work, front: rec.UI(), frontEnd: "test"}
			a.ui = a.front
			operationCatalog["test-op"] = operation{app.Write, c.run}
			defer delete(operationCatalog, "test-op")

			err := a.RunOperation("test-op")
			if (err != nil) != (c.result != "success") {
				t.Fatalf("err = %v for %s", err, c.result)
			}
			if c.result == "cancelled" && !errors.Is(err, app.ErrCancelled) {
				t.Fatalf("cancel must wrap app.ErrCancelled, got %v", err)
			}
			dir, meta := readSessionMeta(t, work)
			if meta["result"] != c.result || meta["kind"] != "test-op" || meta["front_end"] != "test" {
				t.Fatalf("session.json = %v", meta)
			}
			ops, _ := os.ReadFile(filepath.Join(dir, "operations.jsonl"))
			if !strings.Contains(string(ops), `"event":"start"`) || !strings.Contains(string(ops), `"result":"`+c.result+`"`) {
				t.Fatalf("operations.jsonl:\n%s", ops)
			}
			if a.sess != nil || a.ui != a.front {
				t.Fatal("RunOperation must restore the UI and clear the session")
			}
			for _, e := range rec.Events {
				if e.Op == "" {
					t.Fatalf("event without operation ID: %+v", e)
				}
			}
		})
	}
}

func TestProbeSessionSpansItems(t *testing.T) {
	work := t.TempDir()
	rec := &app.Recorder{}
	a := &App{work: work, front: rec.UI(), frontEnd: "test"}
	a.ui = a.front
	for i := 0; i < 2; i++ {
		if err := a.runProbeOperation("probe", app.ReadOnly, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	first := a.currentProbeDir()
	second := a.newProbeDir()
	if first == second {
		t.Fatal("N must start a new probe session")
	}
	a.closeSessions()
	dirs, _ := filepath.Glob(filepath.Join(app.SessionsDir(work), "*-probe-*"))
	if len(dirs) != 2 {
		t.Fatalf("want 2 probe sessions, got %v", dirs)
	}
	if got := latestProbeDir(work); got != second {
		t.Fatalf("latestProbeDir = %s, want %s", got, second)
	}
	b, _ := os.ReadFile(filepath.Join(first, "session.json"))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if ops, _ := m["operations"].([]any); len(ops) != 2 {
		t.Fatalf("first probe session should hold 2 operations: %v", m["operations"])
	}
}
