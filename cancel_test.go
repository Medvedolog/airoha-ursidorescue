package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ursidorescue/app"
)

// recSerial records writes; reads return nothing.
type recSerial struct{ writes [][]byte }

func (r *recSerial) Name() string                            { return "rec" }
func (r *recSerial) Close() error                            { return nil }
func (r *recSerial) ResetInput() error                       { return nil }
func (r *recSerial) Read([]byte, time.Duration) (int, error) { return 0, nil }
func (r *recSerial) Write(b []byte) error {
	r.writes = append(r.writes, append([]byte(nil), b...))
	return nil
}

func TestEveryWritingOperationHasACancelPolicy(t *testing.T) {
	for kind, op := range operationCatalog {
		if app.FormFor(op.Risk) == app.FormPhrase || op.Risk == app.NonPersistent && kind == "itb-boot" {
			if _, ok := cancelNotes[kind]; !ok {
				t.Errorf("%s (%s) has no STOP policy text", kind, op.Risk)
			}
		}
	}
}

func TestConfirmCarriesActionsAndCancelNote(t *testing.T) {
	rec := &app.Recorder{ConfirmAnswers: []string{"RESTORE STOCK BACKUP"}}
	a := &App{ui: rec.UI(), opKind: "stock-restore"}
	if err := a.confirmOp(app.Erase, "RESTORE STOCK BACKUP", []string{"erase", "write", "bl2"}); err != nil {
		t.Fatal(err)
	}
	c := rec.Confirms[0]
	if len(c.Actions) != 3 || c.CancelNote != cancelNotes["stock-restore"]() || c.Risk != app.Erase {
		t.Fatalf("confirm request: %+v", c)
	}
}

func TestInitialCancelStatePerOperation(t *testing.T) {
	for kind, want := range map[string]app.CancelMode{"diagnostics": app.CancelNow, "terminal": app.CancelUnavailable} {
		rec := &app.Recorder{}
		a := &App{work: t.TempDir(), front: rec.UI(), frontEnd: "test"}
		a.ui = a.front
		saved := operationCatalog[kind]
		operationCatalog[kind] = operation{saved.Risk, func(*App) error { return nil }}
		_ = a.RunOperation(kind)
		operationCatalog[kind] = saved
		if len(rec.Cancels) == 0 || rec.Cancels[0].Mode != want {
			t.Errorf("%s: first cancel state %+v, want mode %v", kind, rec.Cancels, want)
		}
		if last := rec.Cancels[len(rec.Cancels)-1]; last.Mode != app.CancelNow {
			t.Errorf("%s: after the operation STOP must be reset to Now, got %+v", kind, last)
		}
	}
}

func TestStopTakesEffectOnlyWhereSafe(t *testing.T) {
	rec := &app.Recorder{}
	a := &App{ui: rec.UI()}
	a.cancelNow()
	a.RequestStop()
	if last := rec.Cancels[len(rec.Cancels)-1]; !last.Requested {
		t.Fatal("RequestStop must report the pending stop")
	}

	s := &recSerial{}
	_, _, err := a.ubootCommandRaw(s, "mtd list", 50*time.Millisecond)
	if !errors.Is(err, app.ErrCancelled) || len(s.writes) != 0 {
		t.Fatalf("in the Now phase STOP must refuse the next command without sending it: err=%v writes=%d", err, len(s.writes))
	}

	// Mid-write phases: commands keep going; only explicit checkpoints stop.
	for _, mode := range []app.CancelMode{app.CancelAtCheckpoint, app.CancelUnavailable} {
		s = &recSerial{}
		a.setCancel(mode, "after chunk 3/30", "BL2")
		_, _, err = a.ubootCommandRaw(s, "mtd write bl2 0x90000000 0x0 0x20000", 50*time.Millisecond)
		if errors.Is(err, app.ErrCancelled) || len(s.writes) == 0 {
			t.Fatalf("mode %v: a started write step must not be cut by STOP (err=%v, writes=%d)", mode, err, len(s.writes))
		}
	}
	if err := a.checkpoint("after chunk 3/30"); !errors.Is(err, app.ErrCancelled) {
		t.Fatalf("checkpoint with STOP pending must cancel, got %v", err)
	}
	a.stop.Reset()
	if err := a.checkpoint("x"); err != nil {
		t.Fatal("checkpoint without STOP must pass")
	}
}

func TestXmodemStopSendsCancel(t *testing.T) {
	f := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(f, make([]byte, 300), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &app.Recorder{}
	a := &App{ui: rec.UI()}
	a.stop.Request()
	s := &recSerial{}
	_, err := a.xmodemSend(s, f, "test")
	if !errors.Is(err, app.ErrCancelled) {
		t.Fatalf("STOP in the data phase must cancel, got %v", err)
	}
	last := s.writes[len(s.writes)-1]
	if string(last) != "\x18\x18\x18" {
		t.Fatalf("aborted data phase must end with CAN CAN CAN, got %q", last)
	}
}
