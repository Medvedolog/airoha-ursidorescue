package main

import (
	"errors"
	"runtime"
	"testing"

	"ursidorescue/app"
)

type namedSerial struct {
	recSerial
	name string
}

func (s *namedSerial) Name() string { return s.name }

func TestBusyPortOffersRetry(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("port names in this test are Linux paths")
	}
	for _, tc := range []struct {
		answers []string
		wantErr bool
	}{
		{[]string{"/dev/ttyFAKE0", "r"}, false},
		{[]string{"/dev/ttyFAKE0", "n"}, true},
	} {
		rec := &app.Recorder{Answers: tc.answers}
		a := &App{ui: rec.UI()}
		opens := 0
		a.ports = app.NewPortOwner(func(name string) (app.Port, error) {
			opens++
			if opens == 1 {
				return nil, portInUse(name)
			}
			return &namedSerial{name: name}, nil
		})
		s, err := a.openPort()
		if tc.wantErr {
			if !errors.Is(err, app.ErrCancelled) {
				t.Fatalf("cancel after a busy port must be a cancellation, got %v", err)
			}
			continue
		}
		if err != nil || s == nil || opens != 2 {
			t.Fatalf("retry after closing the other program must connect: err=%v opens=%d", err, opens)
		}
		s.Close()
		warned := false
		for _, e := range rec.Events {
			warned = warned || (e.Level == app.LevelWarn && errors.Is(portInUse("x"), errPortInUse))
		}
		if !warned {
			t.Fatal("the operator must be told the port is busy")
		}
		if q := rec.Asks[len(rec.Asks)-1]; len(q.Quick) != 3 || q.Default != "r" {
			t.Fatalf("retry / another port / cancel must be offered as buttons: %+v", q)
		}
	}
}
