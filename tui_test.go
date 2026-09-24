package main

import (
	"errors"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ursidorescue/app"
)

func testTUI(t *testing.T, w, h int) (*tuiModel, *tuiUI, chan tea.Msg) {
	t.Helper()
	msgs := make(chan tea.Msg, 64)
	ui := &tuiUI{send: func(m tea.Msg) { msgs <- m }, console: (&app.Recorder{}).UI()}
	a := &App{work: t.TempDir(), frontEnd: "tui"}
	a.ports = app.NewPortOwner(func(string) (app.Port, error) { return nil, errors.New("no port in tests") })
	a.front, a.ui = ui, ui
	m := newTUIModel(a, ui)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m, ui, msgs
}

func typeKeys(m *tuiModel, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func checkFits(t *testing.T, m *tuiModel, what string) string {
	t.Helper()
	v := m.View()
	lines := strings.Split(v, "\n")
	if len(lines) != m.h {
		t.Errorf("%s: %d lines, want exactly %d", what, len(lines), m.h)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.w {
			t.Errorf("%s: line %d is %d wide (> %d): %q", what, i, w, m.w, l)
		}
	}
	return v
}

func confirmReq() app.ConfirmRequest {
	return app.ConfirmRequest{Risk: app.Erase, Phrase: "RESTORE STOCK BACKUP",
		Actions:    []string{"erase the ubi region (mtd erase ubi)", "write 30 chunks and verify each", "write BL2 last"},
		CancelNote: "before erase: at once; while writing chunks: after the current chunk; BL2: unavailable"}
}

func TestTUIFits80x24(t *testing.T) {
	m, _, _ := testTUI(t, 80, 24)
	checkFits(t, m, "menu")
	for i := 0; i < 40; i++ {
		m.Update(tuiEventMsg{Level: app.LevelInfo, Label: "NET", Text: strings.Repeat("long event text ", 12)})
		m.Update(tuiOutputMsg("AN7583> mtd list\r\n\x1b[1;31mcolour\x1b[0m line\n"))
	}
	checkFits(t, m, "menu with log")
	m.busy, m.opTitle = true, "Restore stock"
	m.Update(tuiProgressMsg{Label: "IBU", Current: 9, Total: 30, Detail: "chunk 10/30"})
	m.Update(tuiCancelMsg{Mode: app.CancelAtCheckpoint, Checkpoint: "after chunk 10/30", Op: "op-x-a121"})
	v := checkFits(t, m, "operation")
	if !strings.Contains(v, "after chunk 10/30") || !strings.Contains(v, "…a121") {
		t.Errorf("operation view lacks the STOP checkpoint or the op ID:\n%s", v)
	}
	m.Update(tuiConfirmMsg{req: confirmReq(), reply: make(chan string, 1)})
	v = checkFits(t, m, "confirm dialog")
	for _, want := range []string{"ERASE", "3. write BL2 last", "RESTORE STOCK BACKUP", "UART"} {
		if !strings.Contains(v, want) {
			t.Errorf("confirm dialog at 80x24 lacks %q:\n%s", want, v)
		}
	}
	m.dlg = &tuiDialog{port: []string{"/dev/ttyUSB0", "/dev/ttyUSB1"}}
	checkFits(t, m, "port dialog")
	m.dlg, m.logMax = nil, true
	checkFits(t, m, "maximized log")
}

func TestTUIConfirmThroughBridge(t *testing.T) {
	for _, tc := range []struct {
		typed string
		ok    bool
	}{{"RESTORE STOCK BACKUP", true}, {"restore stock backup", false}} {
		m, ui, msgs := testTUI(t, 80, 24)
		done := make(chan error, 1)
		go func() { done <- ui.Confirm(confirmReq()) }()
		m.Update(<-msgs)
		if m.dlg == nil || m.dlg.confirm == nil {
			t.Fatal("Confirm must open the confirmation dialog")
		}
		typeKeys(m, tc.typed)
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		err := <-done
		if (err == nil) != tc.ok {
			t.Errorf("typed %q: err=%v, want ok=%v", tc.typed, err, tc.ok)
		}
		if m.dlg != nil {
			t.Error("the dialog must close after the answer")
		}
	}
}

func TestTUIButtonConfirmAndChoice(t *testing.T) {
	m, ui, msgs := testTUI(t, 80, 24)
	done := make(chan error, 1)
	go func() { done <- ui.Confirm(app.ConfirmRequest{Risk: app.SettingsChange}) }()
	m.Update(<-msgs)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if err := <-done; !errors.Is(err, app.ErrCancelled) {
		t.Fatalf("Esc on a button confirmation must cancel, got %v", err)
	}

	ans := make(chan string, 1)
	go func() {
		v, _ := ui.Ask(app.AskRequest{Kind: app.AskChoice, Choices: []app.Choice{{Key: "1", Label: "Auto"}, {Key: "3", Label: "MF"}}, Prompt: "Choice [1]: "})
		ans <- v
	}()
	m.Update(<-msgs)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v := <-ans; v != "3" {
		t.Fatalf("↓ Enter must answer the second choice's key, got %q", v)
	}
}

func TestTUIStopLabels(t *testing.T) {
	m, _, _ := testTUI(t, 100, 30)
	m.busy = true
	for _, tc := range []struct {
		c    app.CancelState
		want string
	}{
		{app.CancelState{Mode: app.CancelNow}, "СТОП"},
		{app.CancelState{Mode: app.CancelAtCheckpoint, Checkpoint: "после части 3/30"}, "после части 3/30"},
		{app.CancelState{Mode: app.CancelUnavailable, Reason: "BL2"}, "ОСТАНОВКА НЕДОСТУПНА"},
		{app.CancelState{Mode: app.CancelAtCheckpoint, Requested: true}, "ОСТАНОВКА ЗАПРОШЕНА"},
	} {
		m.Update(tuiCancelMsg(tc.c))
		if top := m.viewTop(); !strings.Contains(top, tc.want) {
			t.Errorf("%+v: top bar %q lacks %q", tc.c, top, tc.want)
		}
	}
	if !strings.Contains(tuiStopText(app.CancelState{Mode: app.CancelUnavailable, Reason: "BL2 is being verified"}), "BL2 is being verified") {
		t.Error("a refused STOP must show the core's reason")
	}
}

func TestTUIRunsOperationAndStops(t *testing.T) {
	m, _, msgs := testTUI(t, 80, 24)
	saved := operationCatalog["diagnostics"]
	defer func() { operationCatalog["diagnostics"] = saved }()
	release := make(chan struct{})
	operationCatalog["diagnostics"] = operation{saved.Risk, func(a *App) error {
		a.status("TEST", "step one", app.LevelInfo)
		<-release
		return a.stopBeforeCommand("mtd list")
	}}
	cmd := m.start(tuiItem{label: "Diagnostics", kind: "diagnostics"})
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var mu sync.Mutex
	drain := func() {
		mu.Lock()
		defer mu.Unlock()
		for {
			select {
			case msg := <-msgs:
				m.Update(msg)
			default:
				return
			}
		}
	}
	for !m.a.Busy() {
		drain()
	}
	stop := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if stop == nil {
		t.Fatal("s during an operation must request STOP")
	}
	m.Update(stop())
	close(release)
	res := <-done
	drain()
	m.Update(res)
	if !m.resultBad || !strings.Contains(m.result, L("остановлено", "stopped")) {
		t.Fatalf("operation must end cancelled after STOP, result %q", m.result)
	}
	if v := m.View(); !strings.Contains(v, "step one") {
		t.Errorf("operation events must reach the screen:\n%s", v)
	}
}

func TestTUIHoldsEventsWhileConsoleOwnsTerminal(t *testing.T) {
	_, ui, msgs := testTUI(t, 80, 24)
	rec := &app.Recorder{}
	ui.console = rec.UI()
	ui.setPassthrough(true)
	ui.Event(app.Event{Text: "while in the terminal"})
	if len(msgs) != 0 {
		t.Fatal("nothing may be sent to the suspended TUI")
	}
	if len(rec.Events) != 1 {
		t.Fatal("the console must show the event while it owns the terminal")
	}
	held := ui.setPassthrough(false)
	if len(held) != 1 {
		t.Fatalf("held events must be replayed to the TUI, got %d", len(held))
	}
}

func TestStripTerminal(t *testing.T) {
	in := "\x1b[2J\x1b[1;31mred\x1b[0m\ttab\x07\x1b]0;title\x07end"
	if got := stripTerminal(in); got != "red    tabend" {
		t.Fatalf("stripTerminal = %q", got)
	}
	if got := uartLine("progress 10%\rprogress 99%\r"); got != "progress 99%" {
		t.Fatalf("uartLine keeps the text after the last CR, got %q", got)
	}
}

func TestWrapLinesKeepsHyphenatedWords(t *testing.T) {
	got := wrapLines("Nokia XG-040G-MD/MF from U-Boot and a verylongwordthatmustbecut", 14)
	for _, l := range got {
		if lipgloss.Width(l) > 14 {
			t.Fatalf("line %q is wider than 14", l)
		}
	}
	joined := strings.Join(got, "|")
	if !strings.Contains(joined, "XG-040G-MD/MF") || !strings.Contains(joined, "U-Boot") {
		t.Fatalf("hyphenated words must not be split: %q", joined)
	}
}
