package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Session is one working session on disk (doc/UI_SPEC_RU.md §8):
//
//	work/sessions/<id>/
//	  session.json       metadata, written on Close
//	  session.log        human-readable events
//	  uart.log           raw UART, opened by the core when it talks to a port
//	  operations.jsonl   structured operation records
//	  errors.log         failures
//	  artifacts/         produced files
//
// A Porting probe writes its own uart.log and transcript.jsonl into the same
// directory through the probe package.
type Session struct {
	ID      string
	Kind    string
	Dir     string
	Started time.Time

	mu       sync.Mutex
	meta     sessionMeta
	eventLog *os.File
	opsLog   *os.File
	errLog   *os.File
	closed   bool
}

type sessionMeta struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Tool       string    `json:"tool"`
	Version    string    `json:"version"`
	FrontEnd   string    `json:"front_end"`
	Started    time.Time `json:"started"`
	Finished   time.Time `json:"finished,omitempty"`
	Result     string    `json:"result,omitempty"`
	Operations []string  `json:"operations"`
}

var kindRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func shortHex() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewID returns "<prefix><date>-<time>-<kind>-<4 hex>".
func NewID(prefix, kind string, now time.Time) string {
	return prefix + now.Format("20060102-150405") + "-" + kind + "-" + shortHex()
}

// SessionsDir is where sessions live under a work directory.
func SessionsDir(work string) string { return filepath.Join(work, "sessions") }

// NewSession creates work/sessions/<id>/ and opens its logs.
func NewSession(work, kind, tool, version, frontEnd string) (*Session, error) {
	if !kindRE.MatchString(kind) {
		return nil, fmt.Errorf("session: invalid kind %q", kind)
	}
	now := time.Now()
	id := NewID("", kind, now)
	dir := filepath.Join(SessionsDir(work), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Session{ID: id, Kind: kind, Dir: dir, Started: now,
		meta: sessionMeta{ID: id, Kind: kind, Tool: tool, Version: version, FrontEnd: frontEnd, Started: now, Operations: []string{}}}
	var err error
	open := func(name string) *os.File {
		if err != nil {
			return nil
		}
		var f *os.File
		f, err = os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		return f
	}
	s.eventLog = open("session.log")
	s.opsLog = open("operations.jsonl")
	s.errLog = open("errors.log")
	if err != nil {
		s.Close("failed")
		return nil, err
	}
	if err := s.writeMeta(); err != nil {
		s.Close("failed")
		return nil, err
	}
	return s, nil
}

// UARTPath is the session's raw UART log.
func (s *Session) UARTPath() string { return filepath.Join(s.Dir, "uart.log") }

// ArtifactsDir returns (and creates) the artifacts directory.
func (s *Session) ArtifactsDir() string {
	d := filepath.Join(s.Dir, "artifacts")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// WorkDir returns (and creates) a scratch directory inside the session.
func (s *Session) WorkDir(name string) string {
	d := filepath.Join(s.Dir, name)
	_ = os.MkdirAll(d, 0o755)
	return d
}

// NewOperation registers an operation and returns its ID.
func (s *Session) NewOperation(kind string) string {
	id := NewID("op-", kind, time.Now())
	s.mu.Lock()
	s.meta.Operations = append(s.meta.Operations, id)
	s.mu.Unlock()
	_ = s.writeMeta()
	return id
}

// LogEvent appends one human-readable line to session.log.
func (s *Session) LogEvent(t time.Time, label, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.eventLog == nil {
		return
	}
	if t.IsZero() {
		t = time.Now()
	}
	line := t.Format("2006-01-02T15:04:05.000Z07:00") + " "
	if label != "" {
		line += "[" + label + "] "
	}
	_, _ = s.eventLog.WriteString(line + text + "\n")
}

// Record appends one structured record to operations.jsonl. "ts" is added.
func (s *Session) Record(rec map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.opsLog == nil {
		return
	}
	out := map[string]any{"ts": time.Now().Format(time.RFC3339Nano)}
	for k, v := range rec {
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	_, _ = s.opsLog.Write(append(b, '\n'))
}

// LogError appends a failure to errors.log.
func (s *Session) LogError(op string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.errLog == nil || err == nil {
		return
	}
	_, _ = s.errLog.WriteString(time.Now().Format(time.RFC3339) + " " + op + " " + err.Error() + "\n")
}

func (s *Session) writeMeta() error {
	s.mu.Lock()
	b, err := json.MarshalIndent(s.meta, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir, "session.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.Dir, "session.json"))
}

// Close records the result and closes the logs. It is safe to call twice.
func (s *Session) Close(result string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.meta.Finished = time.Now()
	s.meta.Result = result
	s.mu.Unlock()
	_ = s.writeMeta()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, f := range []*os.File{s.eventLog, s.opsLog, s.errLog} {
		if f != nil {
			_ = f.Close()
		}
	}
}

// SessionUI wraps a front end: it forwards everything and also writes events,
// questions and confirmations into the session. Ask answers are NOT logged:
// they can be credentials (a Linux login typed by the operator).
type SessionUI struct {
	Inner   UI
	Session *Session
	// Op is stamped on events that carry none.
	Op string
}

func (u *SessionUI) Event(e Event) {
	if e.Op == "" {
		e.Op = u.Op
	}
	switch e.Kind {
	case KindSectionStart:
		u.Session.LogEvent(e.Time, "", "== "+e.Text+" ==")
	case KindLine:
		u.Session.LogEvent(e.Time, e.Label, e.Text)
	}
	u.Inner.Event(e)
}

func (u *SessionUI) Progress(p Progress) {
	if p.Op == "" {
		p.Op = u.Op
	}
	u.Inner.Progress(p)
}

func (u *SessionUI) Ask(q AskRequest) (string, error) {
	if q.Op == "" {
		q.Op = u.Op
	}
	u.Session.LogEvent(time.Time{}, "ASK", q.Prompt)
	return u.Inner.Ask(q)
}

func (u *SessionUI) Confirm(c ConfirmRequest) error {
	if c.Op == "" {
		c.Op = u.Op
	}
	err := u.Inner.Confirm(c)
	res := "confirmed"
	if err != nil {
		res = "not confirmed: " + err.Error()
	}
	u.Session.Record(map[string]any{"op": c.Op, "event": "confirm", "risk": string(c.Risk), "actions": c.Actions,
		"cancel_note": c.CancelNote, "phrase": c.Phrase, "result": res})
	return err
}

func (u *SessionUI) Output(b []byte) { u.Inner.Output(b) }

// Cancel forwards the STOP state and records each change, so a log shows
// exactly when stopping was possible.
func (u *SessionUI) Cancel(c CancelState) {
	if c.Op == "" {
		c.Op = u.Op
	}
	modes := map[CancelMode]string{CancelNow: "now", CancelAtCheckpoint: "checkpoint", CancelUnavailable: "unavailable"}
	u.Session.Record(map[string]any{"op": c.Op, "event": "cancel_state", "mode": modes[c.Mode],
		"checkpoint": c.Checkpoint, "reason": c.Reason, "requested": c.Requested})
	u.Inner.Cancel(c)
}

func (u *SessionUI) Artifact(a Artifact) {
	if a.Op == "" {
		a.Op = u.Op
	}
	u.Session.Record(map[string]any{"op": a.Op, "event": "artifact", "kind": a.Kind, "path": a.Path, "sha256": a.SHA256})
	u.Inner.Artifact(a)
}
