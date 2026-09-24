package app

import (
	"errors"
	"testing"
	"time"
)

type fakePort struct {
	name   string
	closed int
	writes []string
}

func (p *fakePort) Name() string                            { return p.name }
func (p *fakePort) Close() error                            { p.closed++; return nil }
func (p *fakePort) ResetInput() error                       { return nil }
func (p *fakePort) Write(b []byte) error                    { p.writes = append(p.writes, string(b)); return nil }
func (p *fakePort) Read([]byte, time.Duration) (int, error) { return 0, nil }

func newFakeOwner() (*PortOwner, map[string]*fakePort) {
	opened := map[string]*fakePort{}
	return NewPortOwner(func(name string) (Port, error) {
		if name == "missing" {
			return nil, errors.New("no such port")
		}
		p := &fakePort{name: name}
		opened[name] = p
		return p, nil
	}), opened
}

func TestPortOwnerLeasesOneOperationAtATime(t *testing.T) {
	o, opened := newFakeOwner()
	if _, err := o.Acquire("op-1"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("acquire before connect: %v", err)
	}
	if err := o.Connect("missing"); err == nil {
		t.Fatal("open error must be returned")
	}
	if err := o.Connect("COM6"); err != nil {
		t.Fatal(err)
	}
	l, err := o.Acquire("op-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Acquire("op-2"); !errors.Is(err, ErrPortBusy) {
		t.Fatalf("second lease must be refused, got %v", err)
	}
	if err := o.Disconnect(); !errors.Is(err, ErrPortBusy) {
		t.Fatalf("disconnect under a lease must be refused, got %v", err)
	}
	if err := o.Connect("COM7"); !errors.Is(err, ErrPortBusy) {
		t.Fatalf("switching ports under a lease must be refused, got %v", err)
	}
	if o.Holder() != "op-1" {
		t.Fatalf("holder = %q", o.Holder())
	}
	if err := l.Write([]byte("x")); err != nil || len(opened["COM6"].writes) != 1 {
		t.Fatal("lease must write through")
	}
	_ = l.Close()
	_ = l.Close() // idempotent
	if opened["COM6"].closed != 0 {
		t.Fatal("closing a lease must not close the port")
	}
	if err := l.Write([]byte("y")); err == nil {
		t.Fatal("a returned lease must not write")
	}
	if _, err := o.Acquire("op-2"); err != nil {
		t.Fatalf("port must be free after release: %v", err)
	}
}

func TestPortOwnerConnectDisconnect(t *testing.T) {
	o, opened := newFakeOwner()
	_ = o.Connect("COM6")
	if err := o.Connect("COM6"); err != nil || len(opened) != 1 {
		t.Fatal("reconnecting the same port must be a no-op")
	}
	_ = o.Connect("COM7")
	if opened["COM6"].closed != 1 {
		t.Fatal("switching ports must close the old one")
	}
	if name, ok := o.Connected(); !ok || name != "COM7" {
		t.Fatalf("connected = %q %v", name, ok)
	}
	if err := o.Disconnect(); err != nil || opened["COM7"].closed != 1 {
		t.Fatal("disconnect must close the port")
	}
	if _, ok := o.Connected(); ok {
		t.Fatal("still connected after disconnect")
	}
}
