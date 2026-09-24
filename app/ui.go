// Package app is the application layer between UrsidoRescue's core and its
// front ends (the console today; TUI and Web-GUI later, see
// doc/UI_SPEC_RU.md §6). The core reports what happens and asks the operator
// only through UI: it never prints to or reads from a terminal itself, so
// every front end gets the same events for the same operation.
package app

import (
	"errors"
	"time"
)

// Level says how an event should be presented.
type Level int

const (
	// LevelNote is plain operator guidance, shown without a timestamp.
	LevelNote Level = iota
	// LevelInfo is an ordinary timestamped event.
	LevelInfo
	// LevelOK reports a passed check.
	LevelOK
	// LevelWarn needs the operator's attention but does not stop anything.
	LevelWarn
	// LevelError reports a failure.
	LevelError
)

// Kind separates ordinary lines from grouped blocks (a front end may draw a
// frame or a panel around a section).
type Kind int

const (
	KindLine Kind = iota
	KindSectionStart
	KindSectionEnd
)

// Event is one state change, detection, warning or error.
type Event struct {
	Time  time.Time
	Level Level
	Kind  Kind
	// Label is a short tag such as "NET" or "STOP"; empty for plain events.
	Label string
	Text  string
	// Op is the operation ID the event belongs to, when known.
	Op string
}

// Progress reports how far an operation is. Total == 0 means the amount is
// unknown and the front end must not draw a fake percentage.
type Progress struct {
	Op      string
	Label   string
	Current int64
	Total   int64
	Unit    string
	// Detail is a ready human-readable line for simple front ends.
	Detail string
	// Done marks the end of this progress sequence.
	Done bool
}

// AskKind selects what an Ask expects back.
type AskKind int

const (
	AskText AskKind = iota
	AskPath
	AskChoice
)

// Choice is one option of an AskChoice request.
type Choice struct {
	Key   string
	Label string
}

// AskRequest asks the operator for a value, a path or a choice. The core
// validates the answer; a front end only collects it.
type AskRequest struct {
	Kind    AskKind
	Op      string
	Title   string
	Choices []Choice
	Prompt  string
}

// ConfirmRequest is the single confirmation before an operation with risk.
// Its form follows Risk (FormFor); Phrase is mandatory where the form is
// FormPhrase and may be given for lower classes to be stricter.
type ConfirmRequest struct {
	Op      string
	Risk    Risk
	Title   string
	Summary []string
	// Actions lists what the operation will do, in order (erase, write,
	// BL2 last...). The risk class is the highest of them.
	Actions []string
	// CancelNote says when STOP will take effect once the operation runs
	// (doc/UI_SPEC_RU.md §17), from the same policy that drives CancelState.
	CancelNote string
	Phrase     string
}

// CancelMode is what STOP does at this moment (doc/UI_SPEC_RU.md §17).
type CancelMode int

const (
	// CancelNow stops at once.
	CancelNow CancelMode = iota
	// CancelAtCheckpoint stops at the next safe checkpoint.
	CancelAtCheckpoint
	// CancelUnavailable cannot stop until the current step finishes.
	CancelUnavailable
)

// CancelState tells the front end how to label STOP. The core sends it on
// every change; front ends never guess it.
type CancelState struct {
	Op   string
	Mode CancelMode
	// Checkpoint names the next safe point ("after chunk 10/30").
	Checkpoint string
	// Reason says why stopping is unavailable ("BL2 is being verified").
	Reason string
	// Requested is true once STOP was pressed and the core is heading for
	// the checkpoint.
	Requested bool
}

// Artifact is a file the operation produced.
type Artifact struct {
	Op     string
	Kind   string
	Label  string
	Path   string
	SHA256 string
}

// UI is what every front end implements.
type UI interface {
	Event(Event)
	Progress(Progress)
	Ask(AskRequest) (string, error)
	// Confirm returns nil only when the operator confirmed exactly as the
	// request's form demands, ErrCancelled otherwise.
	Confirm(ConfirmRequest) error
	// Output passes raw device bytes (UART) through for display.
	Output([]byte)
	Artifact(Artifact)
	// Cancel reports what STOP would do now.
	Cancel(CancelState)
}

// ErrCancelled is returned by Confirm when the operator did not confirm.
var ErrCancelled = errors.New("operation cancelled by the operator")
