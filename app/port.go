package app

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Port is the serial port as the core uses it.
type Port interface {
	Name() string
	Close() error
	ResetInput() error
	Write([]byte) error
	Read([]byte, time.Duration) (int, error)
}

// ErrPortBusy is returned when another operation holds the port.
var ErrPortBusy = errors.New("the serial port is held by another operation")

// ErrNotConnected is returned when no port is connected.
var ErrNotConnected = errors.New("no serial port is connected")

// PortOwner owns the serial port for the application layer
// (doc/UI_SPEC_RU.md §9): front ends connect and disconnect it, operations
// lease it one at a time. A lease's Close only returns the lease; the port
// stays open until Disconnect.
type PortOwner struct {
	mu     sync.Mutex
	open   func(name string) (Port, error)
	port   Port
	holder string
}

// NewPortOwner uses open to open a named port.
func NewPortOwner(open func(name string) (Port, error)) *PortOwner {
	return &PortOwner{open: open}
}

// Connect opens name. Connecting to the port already open is a no-op; a
// different port replaces it, but never while an operation holds it.
func (o *PortOwner) Connect(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.port != nil && o.port.Name() == name {
		return nil
	}
	if o.holder != "" {
		return ErrPortBusy
	}
	p, err := o.open(name)
	if err != nil {
		return err
	}
	if o.port != nil {
		_ = o.port.Close()
	}
	o.port = p
	return nil
}

// Disconnect closes the port unless an operation holds it.
func (o *PortOwner) Disconnect() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.holder != "" {
		return ErrPortBusy
	}
	if o.port == nil {
		return nil
	}
	err := o.port.Close()
	o.port = nil
	return err
}

// Connected returns the open port's name.
func (o *PortOwner) Connected() (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.port == nil {
		return "", false
	}
	return o.port.Name(), true
}

// Holder is the operation ID that holds the port, "" when free.
func (o *PortOwner) Holder() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.holder
}

// Acquire leases the connected port to op. Only one lease exists at a time.
func (o *PortOwner) Acquire(op string) (Port, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.port == nil {
		return nil, ErrNotConnected
	}
	if o.holder != "" {
		return nil, fmt.Errorf("%w (%s)", ErrPortBusy, o.holder)
	}
	if op == "" {
		op = "unnamed"
	}
	o.holder = op
	return &lease{owner: o, port: o.port, op: op}, nil
}

func (o *PortOwner) release(l *lease) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.holder == l.op {
		o.holder = ""
	}
}

// lease is a Port handed to one operation; Close returns it to the owner.
type lease struct {
	owner  *PortOwner
	port   Port
	op     string
	mu     sync.Mutex
	closed bool
}

func (l *lease) Name() string { return l.port.Name() }

func (l *lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	l.owner.release(l)
	return nil
}

func (l *lease) live() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("serial port lease already returned")
	}
	return nil
}

func (l *lease) ResetInput() error {
	if err := l.live(); err != nil {
		return err
	}
	return l.port.ResetInput()
}

func (l *lease) Write(b []byte) error {
	if err := l.live(); err != nil {
		return err
	}
	return l.port.Write(b)
}

func (l *lease) Read(b []byte, d time.Duration) (int, error) {
	if err := l.live(); err != nil {
		return 0, err
	}
	return l.port.Read(b, d)
}
