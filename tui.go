package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"ursidorescue/app"
)

// runTUI is the full-screen terminal front end (doc/UI_SPEC_RU.md §5.3). The
// core runs every operation in its own goroutine and talks to the screen only
// through app.UI (tuiUI below); the screen never decides anything itself.
func (a *App) runTUI() error {
	if t := os.Getenv("TERM"); t == "dumb" {
		return fmt.Errorf("TERM=dumb: %w", errTUIUnavailable)
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return fmt.Errorf(L("ввод или вывод — не терминал: %w", "input or output is not a terminal: %w"), errTUIUnavailable)
	}
	a.portOwner() // created here, before any operation goroutine can race for it
	bridge := &tuiUI{console: newConsoleUI(a.reader)}
	a.front, a.ui, a.frontEnd = bridge, bridge, "tui"
	m := newTUIModel(a, bridge)
	p := tea.NewProgram(m, tea.WithAltScreen())
	bridge.send = p.Send
	_, err := p.Run()
	return err
}

var errTUIUnavailable = errors.New("full-screen TUI unavailable")

// tuiUI is app.UI for the TUI. Calls come from the operation goroutine: they
// are turned into messages for the Bubble Tea loop, and Ask/Confirm wait for
// the operator's answer on a channel.
//
// A console-owning operation (UART terminal, UART Shell) gets the real
// terminal while the TUI is suspended; during it the calls go to a plain
// console UI and events are kept to be shown when the TUI comes back.
type tuiUI struct {
	send    func(tea.Msg)
	console app.UI

	mu          sync.Mutex
	passthrough bool
	held        []tea.Msg
}

type (
	tuiEventMsg    app.Event
	tuiProgressMsg app.Progress
	tuiOutputMsg   []byte
	tuiArtifactMsg app.Artifact
	tuiCancelMsg   app.CancelState
	tuiAskMsg      struct {
		req   app.AskRequest
		reply chan string
	}
	tuiConfirmMsg struct {
		req   app.ConfirmRequest
		reply chan string
	}
)

func (u *tuiUI) setPassthrough(on bool) []tea.Msg {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.passthrough = on
	held := u.held
	u.held = nil
	return held
}

// deliver hands a message to the TUI, or keeps it while the terminal belongs
// to a console-owning operation. It reports whether the console is active.
func (u *tuiUI) deliver(msg tea.Msg) bool {
	u.mu.Lock()
	if u.passthrough {
		u.held = append(u.held, msg)
		u.mu.Unlock()
		return true
	}
	u.mu.Unlock()
	u.send(msg)
	return false
}

func (u *tuiUI) consoleActive() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.passthrough
}

func (u *tuiUI) Event(e app.Event) {
	if u.deliver(tuiEventMsg(e)) {
		u.console.Event(e)
	}
}

func (u *tuiUI) Progress(p app.Progress) {
	if u.deliver(tuiProgressMsg(p)) {
		u.console.Progress(p)
	}
}

func (u *tuiUI) Output(b []byte) {
	if u.deliver(tuiOutputMsg(append([]byte(nil), b...))) {
		u.console.Output(b)
	}
}

func (u *tuiUI) Artifact(x app.Artifact) {
	if u.deliver(tuiArtifactMsg(x)) {
		u.console.Artifact(x)
	}
}

func (u *tuiUI) Cancel(c app.CancelState) {
	if u.deliver(tuiCancelMsg(c)) {
		u.console.Cancel(c)
	}
}

func (u *tuiUI) Ask(q app.AskRequest) (string, error) {
	if u.consoleActive() {
		return u.console.Ask(q)
	}
	reply := make(chan string, 1)
	u.send(tuiAskMsg{req: q, reply: reply})
	return <-reply, nil
}

func (u *tuiUI) Confirm(r app.ConfirmRequest) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if u.consoleActive() {
		return u.console.Confirm(r)
	}
	if r.Phrase == "" && app.FormFor(r.Risk) == app.FormNone {
		return nil
	}
	reply := make(chan string, 1)
	u.send(tuiConfirmMsg{req: r, reply: reply})
	return app.CheckAnswer(r, <-reply)
}

// consoleOp runs a console-owning operation through tea.Exec: Bubble Tea
// releases the terminal, runs it, and restores the screen afterwards.
type consoleOp struct {
	a    *App
	ui   *tuiUI
	kind string
	err  error
	held []tea.Msg
}

func (c *consoleOp) SetStdin(io.Reader)  {}
func (c *consoleOp) SetStdout(io.Writer) {}
func (c *consoleOp) SetStderr(io.Writer) {}

func (c *consoleOp) Run() error {
	c.ui.setPassthrough(true)
	fmt.Println()
	c.err = c.a.RunOperation(c.kind)
	if c.err != nil {
		c.a.showErr(c.err)
	}
	c.held = c.ui.setPassthrough(false)
	return c.err
}

// consoleOwning are the operations that need the real terminal.
var consoleOwning = map[string]bool{"terminal": true, "shell": true, "ram-uboot": true}

// tuiStopText is the TUI's rendering of the core's answer to STOP.
func tuiStopText(c app.CancelState) string {
	switch {
	case !c.Requested:
		return L("Остановка сейчас недоступна: ", "Stopping is unavailable now: ") + c.Reason +
			L(". Шаг будет доведён до проверки. Второй Ctrl+C за 3 с — принудительный выход.",
				". The step will run to its check. A second Ctrl+C within 3 s forces exit.")
	case c.Mode == app.CancelAtCheckpoint:
		return L("Остановка запрошена: остановлюсь ", "Stop requested: will stop ") + c.Checkpoint
	default:
		return L("Остановка запрошена: следующая команда не будет отправлена.", "Stop requested: the next command will not be sent.")
	}
}

// stripTerminal removes ANSI escape sequences and control characters from
// device output so it cannot redraw the TUI.
func stripTerminal(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b:
			i = skipEscape(s, i)
		case c == '\t':
			b.WriteString("    ")
		case c < 0x20 || c == 0x7f:
		default:
			b.WriteByte(c)
		}
	}
	return strings.ToValidUTF8(b.String(), "�")
}

// skipEscape returns the index of the last byte of the escape sequence at i.
func skipEscape(s string, i int) int {
	if i+1 >= len(s) {
		return i
	}
	switch s[i+1] {
	case '[':
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j
			}
		}
		return len(s) - 1
	case ']':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 1
			}
		}
		return len(s) - 1
	}
	return i + 1
}
