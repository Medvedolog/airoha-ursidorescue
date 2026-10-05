package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ursidorescue/app"
)

func TestNetworkIPAsksInsteadOfFailing(t *testing.T) {
	if detectLocalIP() != "" {
		t.Skip("this host has a 192.168.1.x address")
	}
	saved := localIPWait
	localIPWait = 10 * time.Millisecond
	defer func() { localIPWait = saved }()
	// type the router's own address (rejected), then cancel
	rec := &app.Recorder{Answers: []string{"m", "192.168.1.1", "n"}}
	a := &App{ui: rec.UI()}
	_, err := a.networkIP()
	if !errors.Is(err, app.ErrCancelled) {
		t.Fatalf("cancel must cancel, got %v", err)
	}
	q := rec.Asks[0]
	if !strings.Contains(q.Title, "192.168.1.2") || len(q.Quick) != 3 {
		t.Fatalf("the question must say which addresses fit and offer retry/type/cancel: %+v", q)
	}
	warned := false
	for _, e := range rec.Events {
		warned = warned || strings.Contains(e.Text, "192.168.1.1 ")
	}
	if !warned {
		t.Fatal("192.168.1.1 is the router and must be refused with a reason")
	}
}
