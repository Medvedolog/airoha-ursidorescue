package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"ursidorescue/app"
)

// Palette of UrsusBoot (doc/ui-mockup/style.css). Bars use a background so
// the screen zones are visibly separated; lipgloss drops colours under
// NO_COLOR, and the rules and bars still separate the zones by characters.
var (
	tcInk    = lipgloss.Color("#f2e8da")
	tcUART   = lipgloss.Color("#d9cbb8")
	tcMuted  = lipgloss.Color("#a89179")
	tcFaint  = lipgloss.Color("#7d6752")
	tcAmber  = lipgloss.Color("#d99a4d")
	tcSand   = lipgloss.Color("#efc079")
	tcOK     = lipgloss.Color("#7cc493")
	tcBad    = lipgloss.Color("#e8837a")
	tcStopBg = lipgloss.Color("#b23a2e")
	tcBarBg  = lipgloss.Color("#3a2616")
	tcLogBg  = lipgloss.Color("#2e1f16")
	tcDark   = lipgloss.Color("#241610")
	tcLime   = lipgloss.Color("#8fb04a") // device output: dark lime
	tcBordo  = lipgloss.Color("#c4394f") // errors: bordeaux, readable on dark
	tcAmber2 = lipgloss.Color("#c8873a")
	tcTS     = lipgloss.Color("#5f4d3e") // timestamps: barely visible

	tsBrand   = lipgloss.NewStyle().Bold(true).Foreground(tcAmber)
	tsInk     = lipgloss.NewStyle().Foreground(tcInk)
	tsMenu    = lipgloss.NewStyle().Bold(true).Foreground(tcInk)
	tsUART    = lipgloss.NewStyle().Foreground(tcLime)
	tsErr     = lipgloss.NewStyle().Foreground(tcBordo)
	tsErrB    = lipgloss.NewStyle().Bold(true).Foreground(tcBordo)
	tsOKB     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5fdc5f")) // distinct from the lime log text
	tsErrTag  = lipgloss.NewStyle().Bold(true).Foreground(tcBordo)
	tsTag     = lipgloss.NewStyle().Bold(true).Foreground(tcAmber)
	tsAmber2  = lipgloss.NewStyle().Foreground(tcAmber2)
	tsTS      = lipgloss.NewStyle().Foreground(tcTS)
	tsMuted   = lipgloss.NewStyle().Foreground(tcMuted)
	tsFaint   = lipgloss.NewStyle().Foreground(tcFaint)
	tsSand    = lipgloss.NewStyle().Bold(true).Foreground(tcSand)
	tsOK      = lipgloss.NewStyle().Foreground(tcOK)
	tsBad     = lipgloss.NewStyle().Foreground(tcBad)
	tsRule    = lipgloss.NewStyle().Foreground(tcAmber)
	tsSel     = lipgloss.NewStyle().Bold(true).Foreground(tcDark).Background(tcSand)
	tsStop    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(tcStopBg).Padding(0, 1)
	tsStopOff = lipgloss.NewStyle().Bold(true).Foreground(tcSand).Background(tcDark).Padding(0, 1)
	tsTab     = lipgloss.NewStyle().Foreground(tcMuted).Padding(0, 1)
	tsTabOn   = lipgloss.NewStyle().Bold(true).Foreground(tcDark).Background(tcAmber).Padding(0, 1)
	tsBar     = lipgloss.NewStyle().Foreground(tcInk).Background(tcBarBg)
	tsLogBar  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#d6e8a8")).Background(lipgloss.Color("#2f4418"))
	tsDoneBar = lipgloss.NewStyle().Bold(true).Foreground(tcDark).Background(tcOK)
	tsFailBar = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(tcStopBg)
	tsWarnBar = lipgloss.NewStyle().Bold(true).Foreground(tcDark).Background(tcSand)
	tsHelpBar = lipgloss.NewStyle().Foreground(lipgloss.Color("#a9c27a")).Background(lipgloss.Color("#2f4418")) // continues the log frame
	tsGutter  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6f8f3a"))
	tsBtn     = lipgloss.NewStyle().Foreground(tcInk).Border(lipgloss.RoundedBorder()).BorderForeground(tcFaint).Padding(0, 2)
	tsBtnOn   = lipgloss.NewStyle().Bold(true).Foreground(tcDark).Background(tcSand).Border(lipgloss.RoundedBorder()).BorderForeground(tcSand).Padding(0, 2)
	tsBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tcAmber).Padding(0, 1)
)

type tuiLogKind int

const (
	logUART tuiLogKind = iota
	logEvent
)

// tuiLogLine is one log entry. The time is kept apart from the text: the
// running log hides it (the session files keep it), the operation panel
// shows it as a faint column so texts stay aligned.
type tuiLogLine struct {
	kind  tuiLogKind
	ts    string // "15:04:05", "" for notes and device output
	label string // "XMODEM", "NET"…; drawn as a coloured tag
	text  string
	st    lipgloss.Style
	lst   lipgloss.Style // label style
	mark  string         // √ ! × for statuses, readable without colour (in every Windows console font)
	note  bool           // operator guidance (LevelNote), drawn like device output
}

const tuiLogCap = 5000

type tuiDialog struct {
	ask     *tuiAskMsg
	confirm *tuiConfirmMsg
	isPort  bool     // the port chooser (p); port may be empty
	port    []string // detected ports
	then    *tuiItem // a full-screen item waiting for this port
	cursor  int
	scroll  int // first shown line of a dialog taller than the screen
}

type (
	tuiOpDoneMsg struct {
		err  error
		held []tea.Msg
		// sess and op name the session this item ran in ("" when it
		// opened none, such as "view profile").
		sess, op string
	}
	tuiStopMsg app.CancelState
	tuiPortMsg struct {
		name string
		err  error
		then *tuiItem // started once the port is connected
	}
)

type tuiModel struct {
	a  *App
	ui *tuiUI

	w, h int

	menu       []tuiGroup
	tab, cur   int
	busy       bool
	opTitle    string
	opEvents   []tuiLogLine
	result     string
	resultBad  bool
	resultWarn bool // finished, but not everything asked was obtained
	resultNote string
	lastSess   string // session of the finished item, for the report
	lastOp     string
	progress   *app.Progress // the current transfer (XMODEM, TFTP…)
	overall    *app.Progress // the whole operation (chunk N of M)
	cancel     app.CancelState
	lastCtrlC  time.Time

	log     []tuiLogLine
	partial string
	scroll  int
	filter  int
	logMax  bool
	// logHidden folds the log to its bar: the panel keeps the program's
	// steps and commands, the menu gets the room.
	logHidden bool

	dlg   *tuiDialog
	input textinput.Model
	toast string
}

func newTUIModel(a *App, ui *tuiUI) *tuiModel {
	in := textinput.New()
	in.Prompt = "› "
	in.CharLimit = 512
	return &tuiModel{a: a, ui: ui, menu: tuiMenu(), input: in, w: 80, h: 24}
}

func (m *tuiModel) Init() tea.Cmd { return nil }

// ---------- update ----------

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m, m.key(msg)
	case tuiEventMsg:
		m.addEvent(app.Event(msg))
	case tuiProgressMsg:
		p := app.Progress(msg)
		switch {
		case p.Overall && p.Done:
			m.overall = nil
		case p.Overall:
			m.overall = &p
		case p.Done:
			m.progress, m.overall = nil, nil
		default:
			m.progress = &p
		}
	case tuiOutputMsg:
		m.addOutput(string(msg))
	case tuiArtifactMsg:
		m.addEvent(app.Event{Level: app.LevelOK, Label: L("ФАЙЛ", "FILE"), Text: strings.TrimSpace(msg.Label + " " + msg.Path)})
	case tuiCancelMsg:
		m.cancel = app.CancelState(msg)
	case tuiAskMsg:
		m.openDialog(&tuiDialog{ask: &msg, cursor: defaultIndex(msg.req)})
	case tuiConfirmMsg:
		m.openDialog(&tuiDialog{confirm: &msg})
	case tuiStopMsg:
		m.toast = tuiStopText(app.CancelState(msg))
	case tuiPortMsg:
		if errors.Is(msg.err, errPortInUse) {
			m.toast = msg.err.Error() + L(". Закройте её и нажмите p ещё раз.", ". Close it and press p again.")
		} else if msg.err != nil {
			m.toast = msg.err.Error()
		} else if msg.name != "" {
			m.toast = L("Подключено: ", "Connected: ") + msg.name
			if it := msg.then; it != nil {
				return m, m.start(*it)
			}
		} else {
			m.toast = L("Порт отключён", "Port disconnected")
		}
	case tuiOpDoneMsg:
		for _, h := range msg.held {
			m.Update(h)
		}
		m.finish(msg.err)
		m.lastSess, m.lastOp = msg.sess, msg.op
		if msg.sess != "" {
			m.addEvent(app.Event{Level: app.LevelNote, Text: L("Сессия (приложите к отчёту): ", "Session (attach to a report): ") + msg.sess})
		}
	}
	return m, nil
}

// defaultIndex puts the cursor on the answer an empty reply means.
func defaultIndex(q app.AskRequest) int {
	opts := q.Choices
	if len(opts) == 0 {
		opts = q.Quick
	}
	for i, c := range opts {
		if c.Key == q.Default {
			return i
		}
	}
	return 0
}

func (m *tuiModel) openDialog(d *tuiDialog) {
	m.dlg = d
	m.input.SetValue("")
	m.input.Focus()
}

func (m *tuiModel) finish(err error) {
	m.busy = false
	m.progress = nil
	m.resultBad, m.resultWarn = false, false
	var pc probeCodeError
	switch {
	case err == nil:
		m.result = L("Готово.", "Done.")
		// Every step ran and checked out: the overall bar says so, whatever
		// the last step the wizard reported (BL2 is the step after 30/30).
		if m.overall != nil {
			done := *m.overall
			done.Current, done.Detail = done.Total, L("всё выполнено и проверено", "all done and verified")
			m.overall = &done
		}
	case errors.As(err, &pc):
		m.result, m.resultWarn, m.resultNote = err.Error(), true, pc.effects
		m.addEvent(app.Event{Level: app.LevelWarn, Label: "PROBE", Text: err.Error()})
	default:
		m.result, m.resultBad = err.Error(), true
		m.addEvent(app.Event{Level: app.LevelError, Label: L("СТОП", "STOP"), Text: err.Error()})
	}
}

// hotkey maps a key to its command letter in either keyboard layout, so
// f/m/s/p/q work with the Russian layout too (а/ь/ы/з/й), and F-keys work
// everywhere.
func hotkey(k tea.KeyMsg) string {
	switch k.Type {
	case tea.KeyF2:
		return "f"
	case tea.KeyF3:
		return "m"
	case tea.KeyF4:
		return "p"
	case tea.KeyF5:
		return "h"
	case tea.KeyF10:
		return "q"
	case tea.KeyRunes:
		if len(k.Runes) == 1 {
			r := k.Runes[0]
			if l, ok := map[rune]string{'а': "f", 'А': "f", 'ь': "m", 'Ь': "m", 'ы': "s", 'Ы': "s", 'з': "p", 'З': "p", 'р': "h", 'Р': "h", 'й': "q", 'Й': "q", 'о': "j", 'л': "k", 'д': "l", 'Д': "l"}[r]; ok {
				return l
			}
			return strings.ToLower(string(r))
		}
	}
	return k.String()
}

func (m *tuiModel) key(k tea.KeyMsg) tea.Cmd {
	if k.Type == tea.KeyCtrlC {
		return m.ctrlC()
	}
	// Log controls work in every state.
	switch k.Type {
	case tea.KeyPgUp:
		m.scrollBy(m.logRows() - 1)
		return nil
	case tea.KeyPgDown:
		m.scrollBy(-(m.logRows() - 1))
		return nil
	case tea.KeyEnd:
		if m.dlg == nil {
			m.scroll = 0
			return nil
		}
	case tea.KeyF2, tea.KeyF3, tea.KeyF5:
		if m.dlg != nil {
			m.logKey(hotkey(k))
			return nil
		}
	}
	if m.dlg != nil {
		return m.dialogKey(k)
	}
	m.toast = ""
	hk := hotkey(k)
	if m.logKey(hk) {
		return nil
	}
	if hk == "s" && m.busy {
		return m.stop()
	}
	if m.busy {
		return nil
	}
	if m.result != "" {
		if k.Type == tea.KeyEnter || k.Type == tea.KeyEsc {
			m.result, m.opTitle, m.opEvents = "", "", nil
		}
		return nil
	}
	items := m.menu[m.tab].items
	switch {
	case hk == "q":
		return tea.Quit
	case hk == "l":
		// Switch the language; the menu is rebuilt in it.
		if uiLang == "ru" {
			setLang("en")
		} else {
			setLang("ru")
		}
		m.a.lang = uiLang
		m.menu = tuiMenu()
	case k.Type == tea.KeyLeft || k.Type == tea.KeyShiftTab:
		m.tab = (m.tab + len(m.menu) - 1) % len(m.menu)
		m.cur = 0
	case k.Type == tea.KeyRight || k.Type == tea.KeyTab:
		m.tab = (m.tab + 1) % len(m.menu)
		m.cur = 0
	case k.Type == tea.KeyUp || hk == "k":
		m.cur = (m.cur + len(items) - 1) % len(items)
	case k.Type == tea.KeyDown || hk == "j":
		m.cur = (m.cur + 1) % len(items)
	case hk == "p":
		m.dlg = &tuiDialog{isPort: true, port: listSerialPorts()}
		m.input.SetValue("")
		m.input.Focus()
	case k.Type == tea.KeyEnter:
		return m.start(items[m.cur])
	}
	return nil
}

// logKey handles the log's f (filter) and m (large log) keys.
func (m *tuiModel) logKey(hk string) bool {
	switch hk {
	case "f":
		m.filter = (m.filter + 1) % 3
		m.scroll = 0
		return true
	case "m":
		m.logMax = !m.logMax
		m.logHidden = false
		return true
	case "h":
		m.logHidden = !m.logHidden
		m.scroll = 0
		return true
	}
	return false
}

func (m *tuiModel) ctrlC() tea.Cmd {
	if !m.busy {
		return tea.Quit
	}
	if !m.lastCtrlC.IsZero() && time.Since(m.lastCtrlC) < 3*time.Second {
		return tea.Quit
	}
	m.lastCtrlC = time.Now()
	return m.stop()
}

// stop asks the core; RequestStop reports through the UI, so it must run
// outside the Bubble Tea loop.
func (m *tuiModel) stop() tea.Cmd {
	a := m.a
	return func() tea.Msg { return tuiStopMsg(a.RequestStop()) }
}

func (m *tuiModel) start(it tuiItem) tea.Cmd {
	a := m.a
	// A full-screen item picks its port here, in the TUI, rather than in
	// the plain console after the screen was handed over.
	if _, ok := a.portOwner().Connected(); consoleOwning[it.kind] && !ok && a.portOverride == "" && a.rememberedPort() == "" {
		m.dlg = &tuiDialog{isPort: true, port: listSerialPorts(), then: &it}
		m.input.SetValue("")
		m.input.Focus()
		return nil
	}
	m.busy, m.opTitle, m.opEvents, m.result, m.toast = true, it.label, nil, "", ""
	m.progress, m.overall = nil, nil // the previous operation's stay on its result screen only
	if consoleOwning[it.kind] {
		c := &consoleOp{a: a, ui: m.ui, kind: it.kind}
		_, before := a.LastOperation()
		return tea.Exec(c, func(error) tea.Msg {
			sess, op := sinceOp(a, before)
			return tuiOpDoneMsg{err: c.err, held: c.held, sess: sess, op: op}
		})
	}
	_, before := a.LastOperation()
	return func() tea.Msg {
		var err error
		if it.porting != "" {
			err = a.portingItem(it.porting)
		} else {
			err = a.RunOperation(it.kind)
		}
		sess, op := sinceOp(a, before)
		return tuiOpDoneMsg{err: err, sess: sess, op: op}
	}
}

// quickLabel names a button. Only the generic yes/no parsed from "[y/N]"
// are translated; answers with their own label (Cancel, Retry…) keep it.
func quickLabel(c app.Choice) string {
	switch {
	case c.Key == "y" && c.Label == "yes":
		return L("Да", "Yes")
	case c.Key == "n" && c.Label == "no":
		return L("Нет", "No")
	}
	return c.Label
}

// consoleLetters is the "(r) … [r]:" tail console prompts use; the TUI shows
// buttons instead.
var consoleLetters = regexp.MustCompile(`\s*\[[^\]]*\]\s*:?\s*$`)

func (m *tuiModel) dialogKey(k tea.KeyMsg) tea.Cmd {
	d := m.dlg
	switch {
	case d.isPort:
		return m.portKey(k)
	case d.confirm != nil:
		form := app.FormFor(d.confirm.req.Risk)
		if d.confirm.req.Phrase == "" && form == app.FormButton {
			// Two buttons: Cancel (default) and Confirm.
			switch {
			case k.Type == tea.KeyLeft || k.Type == tea.KeyRight || k.Type == tea.KeyTab || k.Type == tea.KeyShiftTab:
				d.cursor = 1 - d.cursor
			case k.Type == tea.KeyUp:
				d.scroll--
			case k.Type == tea.KeyDown:
				d.scroll++
			case k.Type == tea.KeyEnter:
				m.answer(d.confirm.reply, map[int]string{0: "n", 1: "y"}[d.cursor])
			case k.Type == tea.KeyEsc || hotkey(k) == "n" || hotkey(k) == "т":
				m.answer(d.confirm.reply, "n")
			case hotkey(k) == "y" || hotkey(k) == "д":
				m.answer(d.confirm.reply, "y")
			}
			return nil
		}
		switch k.Type {
		case tea.KeyUp:
			d.scroll-- // the text above the pinned answer line
			return nil
		case tea.KeyDown:
			d.scroll++
			return nil
		case tea.KeyEsc:
			m.answer(d.confirm.reply, "")
			return nil
		case tea.KeyEnter:
			m.answer(d.confirm.reply, m.input.Value())
			return nil
		}
	case d.ask != nil:
		q := d.ask.req
		if len(q.Choices) == 0 && len(q.Quick) > 0 {
			return m.quickKey(k)
		}
		ch := q.Choices
		switch k.Type {
		case tea.KeyUp:
			if len(ch) > 0 {
				d.cursor = (d.cursor + len(ch) - 1) % len(ch)
			}
			return nil
		case tea.KeyDown:
			if len(ch) > 0 {
				d.cursor = (d.cursor + 1) % len(ch)
			}
			return nil
		case tea.KeyEsc:
			m.input.SetValue("")
			return nil
		case tea.KeyEnter:
			v := m.input.Value()
			if v == "" && len(ch) > 0 {
				v = ch[d.cursor].Key
			}
			m.answer(d.ask.reply, v)
			return nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return cmd
}

// quickKey drives button answers (yes/no, bl2/ubi, reset/stay): arrows and
// Enter, or the answer's letter in either layout.
func (m *tuiModel) quickKey(k tea.KeyMsg) tea.Cmd {
	d := m.dlg
	q := d.ask.req.Quick
	switch k.Type {
	case tea.KeyLeft, tea.KeyShiftTab:
		d.cursor = (d.cursor + len(q) - 1) % len(q)
	case tea.KeyRight, tea.KeyTab:
		d.cursor = (d.cursor + 1) % len(q)
	case tea.KeyUp:
		d.scroll--
	case tea.KeyDown:
		d.scroll++
	case tea.KeyEnter:
		m.answer(d.ask.reply, q[d.cursor].Key)
	case tea.KeyEsc:
		m.answer(d.ask.reply, d.ask.req.Default)
	default:
		hk := hotkey(k)
		alias := map[string]string{"д": "y", "т": "n"}
		if a, ok := alias[hk]; ok {
			hk = a
		}
		for _, c := range q {
			if c.Key != "" && c.Key == hk {
				m.answer(d.ask.reply, c.Key)
				return nil
			}
		}
	}
	return nil
}

func (m *tuiModel) answer(reply chan string, v string) {
	m.dlg = nil
	m.input.Blur()
	reply <- v
}

func (m *tuiModel) portKey(k tea.KeyMsg) tea.Cmd {
	d := m.dlg
	n := len(d.port) + 1 // + "disconnect"
	switch k.Type {
	case tea.KeyEsc:
		m.dlg = nil
		return nil
	case tea.KeyUp:
		d.cursor = (d.cursor + n - 1) % n
		return nil
	case tea.KeyDown:
		d.cursor = (d.cursor + 1) % n
		return nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.input.Value())
		m.dlg = nil
		owner := m.a.portOwner()
		if name == "" && d.cursor == len(d.port) {
			m.a.rememberPort("")
			return func() tea.Msg { return tuiPortMsg{err: owner.Disconnect()} }
		}
		if name == "" && d.cursor < len(d.port) {
			name = d.port[d.cursor]
		}
		if name == "" {
			return nil
		}
		return func() tea.Msg {
			if err := owner.Connect(name); errors.Is(err, errPortInUse) {
				return tuiPortMsg{err: err}
			} else if err != nil {
				return tuiPortMsg{err: fmt.Errorf("%s: %w", name, err)}
			}
			m.a.rememberPort(name)
			return tuiPortMsg{name: name, then: d.then}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return cmd
}

// ---------- log ----------

// tuiTone colours an event like the console and UrsusFlasher do: by its
// level, and for plain events by what it says.
func tuiTone(l app.Level, text string) (st, tag lipgloss.Style, mark string) {
	switch l {
	case app.LevelError:
		return tsErrB, tsErrTag, "× "
	case app.LevelWarn:
		return tsSand, tsTag, "! "
	case app.LevelOK:
		return tsOKB, tsTag, "√ "
	}
	u := strings.ToUpper(text)
	switch {
	case strings.Contains(u, "PASS") || strings.Contains(u, "ГОТОВО") || strings.Contains(u, "ЗАВЕРШЕН") || strings.Contains(u, "[OK]"):
		return tsOKB, tsTag, "√ "
	case strings.Contains(u, "FAIL") || strings.Contains(u, "ERROR") || strings.Contains(u, "ОШИБ") ||
		strings.Contains(u, "ОТМЕН") || strings.Contains(u, "CANCEL"):
		return tsErrB, tsErrTag, "× "
	case strings.Contains(u, "WARN") || strings.Contains(u, "ВНИМАН"):
		return tsSand, tsTag, "! "
	case strings.Contains(u, "TFTP") || strings.Contains(u, "XMODEM") || strings.Contains(u, "ОЖИД") || strings.Contains(u, "WAIT"):
		return tsAmber2, tsTag, ""
	}
	return tsInk, tsTag, ""
}

func (m *tuiModel) addEvent(e app.Event) {
	switch e.Kind {
	case app.KindSectionStart:
		m.push(tuiLogLine{kind: logEvent, text: "── " + e.Text + " ──", st: tsBrand})
		return
	case app.KindSectionEnd:
		return
	}
	ts := ""
	if e.Level != app.LevelNote {
		t := e.Time
		if t.IsZero() {
			t = time.Now()
		}
		ts = t.Format("15:04:05")
	}
	for i, l := range strings.Split(strings.Trim(e.Text, "\n"), "\n") {
		l = stripTerminal(l)
		st, tag, mark := tuiTone(e.Level, e.Label+" "+l)
		line := tuiLogLine{kind: logEvent, text: l, st: st, lst: tag, note: e.Level == app.LevelNote}
		if i == 0 {
			line.ts, line.label, line.mark = ts, e.Label, mark
		} else if e.Label != "" || mark != "" {
			line.text = strings.Repeat(" ", len([]rune(mark))+len([]rune(e.Label))+3*btoi(e.Label != "")) + l
		}
		m.push(line)
		if m.busy && strings.TrimSpace(l) != "" {
			m.opEvents = append(m.opEvents, line)
			if len(m.opEvents) > 50 {
				m.opEvents = m.opEvents[len(m.opEvents)-50:]
			}
		}
	}
}

// render draws a log line wrapped to w. With withTS the time takes a faint
// fixed column (blank for lines without a time) so texts stay aligned.
func (l tuiLogLine) render(w int, withTS bool) []string {
	col := ""
	if withTS {
		ts := l.ts
		if ts == "" {
			ts = strings.Repeat(" ", 8)
		}
		col = tsTS.Render(ts + " ")
		w -= 9
	}
	head := ""
	if l.label != "" {
		head = "[" + l.label + "] "
	}
	var out []string
	for i, t := range wrapLines(l.mark+head+l.text, w) {
		if i == 0 && l.mark+head != "" {
			rest := strings.TrimPrefix(strings.TrimPrefix(t, l.mark), head)
			t = l.st.Render(l.mark) + l.lst.Render(head) + l.st.Render(rest)
		} else {
			t = l.st.Render(t)
		}
		if i > 0 && withTS {
			col = tsTS.Render(strings.Repeat(" ", 9))
		}
		out = append(out, col+t)
	}
	return out
}

// addOutput appends raw device output; an unfinished line stays pending so
// prompts are visible before the newline arrives.
func (m *tuiModel) addOutput(s string) {
	s = m.partial + s
	parts := strings.Split(s, "\n")
	m.partial = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		m.push(tuiLogLine{kind: logUART, text: uartLine(l), st: tsUART})
	}
}

// uartLine keeps what a terminal would show after carriage returns.
func uartLine(l string) string {
	l = strings.TrimRight(l, "\r")
	if i := strings.LastIndex(l, "\r"); i >= 0 {
		l = l[i+1:]
	}
	return stripTerminal(l)
}

func (m *tuiModel) push(l tuiLogLine) {
	m.log = append(m.log, l)
	if len(m.log) > tuiLogCap {
		m.log = m.log[len(m.log)-tuiLogCap:]
	}
	if m.scroll > 0 && m.visible(l) {
		m.scroll++
	}
}

func (m *tuiModel) visible(l tuiLogLine) bool {
	return m.filter == 0 || (m.filter == 1 && l.kind == logUART) || (m.filter == 2 && l.kind == logEvent)
}

func (m *tuiModel) filtered() []tuiLogLine {
	var out []tuiLogLine
	for _, l := range m.log {
		if m.visible(l) {
			out = append(out, l)
		}
	}
	if m.partial != "" && m.filter != 2 {
		out = append(out, tuiLogLine{kind: logUART, text: uartLine(m.partial), st: tsUART})
	}
	return out
}

func (m *tuiModel) scrollBy(n int) {
	m.scroll += n
	if top := len(m.filtered()) - m.logRows(); m.scroll > top {
		m.scroll = top
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// ---------- layout ----------
//
//	top bar            (background)
//	── section rule ──
//	main area          menu │ description, operation, or dialog
//	▌ UART-лог bar      (background)
//	log lines
//	help bar           (background)

// logRows is the number of log text rows. The log always stays on screen
// (≈35 %, at least 5 rows).
func (m *tuiModel) logRows() int {
	body := m.h - 3
	if m.logHidden && m.dlg == nil {
		return 0
	}
	if m.logMax && m.dlg == nil {
		return max(5, body-5)
	}
	return max(5, min(m.h*35/100, body-9))
}

func (m *tuiModel) View() string {
	body := m.h - 3 // top bar, section rule, help bar
	logH := m.logRows() + 1
	mainH := body - logH
	var title string
	var mainLines []string
	switch {
	case m.dlg != nil:
		title = L("ВОПРОС", "QUESTION")
		// The dialog takes what it needs; the rest stays with the log.
		mainLines = m.viewDialog(body - 6)
		mainH = min(len(mainLines), body-6)
		logH = body - mainH
	case m.busy || m.result != "":
		title = L("ОПЕРАЦИЯ", "OPERATION")
		mainLines = m.viewOperation(mainH)
	default:
		title = L("МЕНЮ", "MENU")
		// Idle menu: the menu and the item description come first; the log
		// gives way down to 3 rows and grows back once an operation runs.
		room := body - 4
		if m.logHidden {
			room = body - 1
		}
		mainLines = m.viewMenu(room)
		mainH = max(mainH, min(len(mainLines), room))
		logH = body - mainH
	}
	out := []string{m.viewTop(), m.rule(title)}
	out = append(out, fit(mainLines, mainH)...)
	out = append(out, m.viewLog(logH)...)
	out = append(out, m.viewHelp())
	for i, l := range out {
		out[i] = ansi.Truncate(l, m.w, "…")
	}
	return strings.Join(fit(out, m.h), "\n")
}

// fit pads or cuts lines to exactly n.
func fit(lines []string, n int) []string {
	if n < 0 {
		n = 0
	}
	if len(lines) > n {
		return lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}

// rule is a titled full-width separator: "── TITLE ─────────".
func (m *tuiModel) rule(title string) string {
	head := "── " + title + " "
	return tsRule.Render(head + strings.Repeat("─", max(0, m.w-lipgloss.Width(head))))
}

// bar paints a full-width line on a background.
func bar(st lipgloss.Style, w int, left, right string) string {
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	// Each part gets the background itself: coloured pieces inside left end
	// with a reset, which would leave the gap unfilled.
	return st.Render(left) + st.Render(strings.Repeat(" ", gap)) + right
}

func (m *tuiModel) stopLabel() string {
	c := m.cancel
	switch {
	case !m.busy:
		return ""
	case c.Requested:
		return tsStop.Render(L("ОСТАНОВКА ЗАПРОШЕНА", "STOP REQUESTED"))
	case c.Mode == app.CancelUnavailable:
		return tsStopOff.Render(L("СТОП недоступен", "STOP unavailable"))
	}
	// s and Ctrl+C are the same STOP; when it takes effect is said in the
	// operation panel.
	return tsStop.Render(L("■ СТОП: s / Ctrl+C", "■ STOP: s / Ctrl+C"))
}

// stopNote says what STOP does right now, in words.
func (m *tuiModel) stopNote() string {
	c := m.cancel
	switch {
	case c.Requested && c.Mode == app.CancelAtCheckpoint:
		return L("Остановка запрошена: операция остановится ", "Stop requested: the operation will stop ") + c.Checkpoint
	case c.Requested:
		return L("Остановка запрошена.", "Stop requested.")
	case c.Mode == app.CancelUnavailable:
		return L("СТОП сейчас недоступен: ", "STOP is unavailable now: ") + c.Reason
	case c.Mode == app.CancelAtCheckpoint:
		return L("СТОП (s или Ctrl+C) не прервёт текущий шаг: операция остановится ", "STOP (s or Ctrl+C) will not cut the current step: the operation stops ") + c.Checkpoint
	}
	return L("СТОП (s или Ctrl+C) — остановить сразу, до следующей команды.", "STOP (s or Ctrl+C): stop at once, before the next command.")
}

// viewTop keeps the STOP label whole: on a narrow screen the version, then
// the operation ID, then the port give way first.
func (m *tuiModel) viewTop() string {
	bg := tsBar.GetBackground()
	on := func(st lipgloss.Style, s string) string { return st.Background(bg).Render(s) }
	port := on(tsBad, "● ") + on(tsMuted, L("порт не выбран (p)", "no port (p)"))
	if name, ok := m.a.portOwner().Connected(); ok {
		port = on(tsOK, "● ") + on(tsInk, name)
	} else if name := m.a.rememberedPort(); name != "" {
		// Chosen before and released between operations, so other programs
		// can use it; the next operation opens it again.
		port = on(tsMuted, "○ ") + on(tsInk, name) + on(tsMuted, L(" свободен", " free"))
	}
	sep := on(tsFaint, "  │  ")
	// While an operation runs: its name and phase (the operation ID is on the
	// result screen, not here).
	// The operation's name whole, or only its step when the name does not
	// fit: a cut-off name reads worse than none (it is in the panel below).
	name, step := m.phase()
	full, short := "", ""
	if name != "" {
		full = sep + on(tsSand, name)
		if step != "" {
			full += on(tsFaint, " · ") + on(tsSand, step)
			short = sep + on(tsSand, step)
		}
	}
	brand := on(tsBrand, " UrsidoRescue")
	right := m.stopLabel()
	if !m.busy && m.result != "" {
		switch {
		case m.resultBad:
			right = tsFailBar.Padding(0, 1).Render(L("× ОШИБКА", "× FAILED"))
		case m.resultWarn:
			right = tsWarnBar.Padding(0, 1).Render(L("! НЕ ПОЛНОСТЬЮ", "! INCOMPLETE"))
		default:
			right = tsDoneBar.Padding(0, 1).Render(L("√ ГОТОВО", "√ DONE"))
		}
	}
	var left string
	for _, l := range []string{
		brand + on(tsFaint, " "+appVersion) + sep + port + full,
		brand + sep + port + full,
		brand + sep + port + short,
		brand + short,
		brand + sep + port,
		brand,
	} {
		left = l
		if lipgloss.Width(l)+lipgloss.Width(right)+1 <= m.w {
			break
		}
	}
	if room := m.w - lipgloss.Width(left) - 1; lipgloss.Width(right) > room {
		right = ansi.Truncate(right, max(0, room), "…")
	}
	return bar(tsBar, m.w, left, right)
}

// phase is the running operation and where it is: "UrsusBoot · READ 37%",
// or the overall step when no transfer runs.
func (m *tuiModel) phase() (name, step string) {
	if !m.busy {
		return "", ""
	}
	switch p := m.progress; {
	case p != nil && p.Total > 0:
		step = fmt.Sprintf("%s %d%%", p.Label, p.Current*100/p.Total)
	case m.overall != nil && m.overall.Total > 0:
		step = fmt.Sprintf(L("шаг %d/%d", "step %d/%d"), min(m.overall.Current+1, m.overall.Total), m.overall.Total)
	}
	return m.opTitle, step
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// wrapLines wraps text to width w, keeping paragraph breaks.
func wrapLines(text string, w int) []string {
	w = max(10, w)
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Split(para, " ") {
			for lipgloss.Width(word) > w { // a word longer than the line
				if line != "" {
					out, line = append(out, line), ""
				}
				cut := ansi.Truncate(word, w, "")
				out, word = append(out, cut), word[len(cut):]
			}
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
				line += " " + word
			default:
				out, line = append(out, line), word
			}
		}
		out = append(out, line)
	}
	return out
}

func (m *tuiModel) viewMenu(h int) []string {
	var tabs []string
	for i, g := range m.menu {
		if i == m.tab {
			tabs = append(tabs, tsTabOn.Render(g.title))
		} else {
			tabs = append(tabs, tsTab.Render(g.title))
		}
	}
	head := strings.Join(tabs, " ")
	// Navigation hints sit under the list, where the eye ends reading it.
	nav := []string{
		tsFaint.Render(L("   ↑ ↓  пункт  ·  Enter  запуск", "   ↑ ↓  item  ·  Enter  run")),
		tsFaint.Render(L("   ← →  раздел", "   ← →  section")),
	}
	items := m.menu[m.tab].items
	listW := 0
	for _, it := range items {
		listW = max(listW, lipgloss.Width(it.label)+4, lipgloss.Width(it.hint)+6)
	}
	// buildList draws the items; spaced gives each item two rows (the
	// label in bold, its hint faint below), so the menu reads larger on a
	// big screen. A terminal program cannot change its font size.
	buildList := func(spaced bool) []string {
		var list []string
		for i, it := range items {
			label := " › " + it.label + " "
			if i == m.cur {
				label = tsSel.Render(label + strings.Repeat(" ", max(0, listW-lipgloss.Width(label)-1)))
			} else if spaced {
				label = tsMenu.Render("   " + it.label)
			} else {
				label = tsInk.Render("   " + it.label)
			}
			list = append(list, label)
			if spaced {
				list = append(list, tsFaint.Render("     "+it.hint))
			}
		}
		return list
	}
	it := items[m.cur]
	var desc []string
	if m.tab == 1 {
		desc = append(desc, tsFaint.Render(L("сессия: ", "session: ")+displayDir(m.a.probeDir)))
	}
	lines := []string{head, ""} // a blank row sets the tabs apart from the list
	descW := m.w - listW - 3
	twoCol := descW >= 30
	var descText []string
	for _, l := range wrapLines(it.desc, max(descW, m.w)) {
		descText = append(descText, tsMuted.Render(l))
	}
	if twoCol {
		descText = descText[:0]
		for _, l := range wrapLines(it.desc, descW) {
			descText = append(descText, tsMuted.Render(l))
		}
	}
	list := buildList(false)
	spaced := false
	if twoCol {
		descLen := len(desc) + len(descText)
		if it.hint != "" {
			descLen++
		}
		// Spaced only with room left for at least the small bear under it.
		if sp := buildList(true); 2+max(len(sp)+1+len(nav)+1+len(bearSmall), descLen) <= h {
			list, spaced = sp, true
		}
	}
	// The hints go under the list when they fit; the help bar at the bottom
	// always has the same keys.
	if 2+len(list)+1+len(nav) <= h {
		list = append(append(list, ""), nav...)
	}
	if !spaced && it.hint != "" {
		desc = append([]string{tsSand.Render(it.hint)}, desc...)
	}
	// The bear only takes free space: big in the top-left corner when
	// the whole menu and description still fit under it, otherwise small in
	// the rows the list leaves empty beside a longer description, otherwise
	// not at all. The description always wins.
	need := 2 + len(list)
	if twoCol {
		need = 2 + max(len(list), len(desc)+len(descText))
	} else {
		need += 1 + len(desc) + len(descText)
	}
	if logo := tuiLogo(h-need-1, m.w); logo != nil && len(logo) == len(bearBig) {
		lines = append(append(logo, ""), lines...)
	} else if small := tuiLogo(len(bearSmall), listW-1); twoCol && small != nil &&
		2+len(list)+1+len(small) <= h {
		list = append(append(list, ""), small...)
	}
	if twoCol {
		// Two columns: the list │ what the selected item does.
		desc = append(desc, descText...)
		rows := max(len(list), min(len(desc), h-len(lines)))
		if len(desc) > rows {
			desc = append(desc[:rows-1], tsFaint.Render("… "+L("подробно — doc/MENU_RU.md", "details: doc/MENU_EN.md")))
		}
		for i := 0; i < rows; i++ {
			l, d := "", ""
			if i < len(list) {
				l = list[i]
			}
			if i < len(desc) {
				d = desc[i]
			}
			l += strings.Repeat(" ", max(0, listW-lipgloss.Width(l)))
			lines = append(lines, l+tsFaint.Render(" │ ")+d)
		}
	} else {
		lines = append(lines, list...)
		lines = append(lines, tsFaint.Render(strings.Repeat("┄", m.w)))
		lines = append(lines, desc...)
		lines = append(lines, descText...)
	}
	if m.toast != "" {
		lines = append(lines[:min(len(lines), h-1)], tsSand.Render(m.toast))
	}
	return lines
}

func (m *tuiModel) viewOperation(h int) []string {
	var lines []string
	switch {
	case m.busy:
		lines = append(lines, tsBrand.Render(m.opTitle))
		lines = append(lines, tsMuted.Render(L("выполняется…", "running…")))
		for _, l := range wrapLines(m.stopNote(), m.w) {
			lines = append(lines, tsMuted.Render(l))
		}
	case m.resultWarn:
		lines = append(lines, bar(tsWarnBar, m.w, " ! "+L("НЕ ПОЛНОСТЬЮ", "INCOMPLETE")+" · "+m.opTitle, ""))
		for _, l := range wrapLines(L("НЕ ПОЛНОСТЬЮ: ", "INCOMPLETE: ")+m.result, m.w) {
			lines = append(lines, tsSand.Render(l))
		}
		for _, l := range wrapLines(capFirst(m.resultNote)+" "+L("Сводка профиля и причина — в логе ниже.", "The profile summary and the reason are in the log below."), m.w) {
			lines = append(lines, tsMuted.Render(l))
		}
	case m.resultBad:
		lines = append(lines, bar(tsFailBar, m.w, " × "+L("ОШИБКА", "FAILED")+" · "+m.opTitle, ""))
		for _, l := range wrapLines(L("ОШИБКА: ", "FAILED: ")+m.result, m.w) {
			lines = append(lines, tsErr.Render(l))
		}
		lines = append(lines, tsMuted.Render(L("Никаких дополнительных write/erase команд после этой ошибки не отправлено.", "No further write/erase commands were sent after this error.")))
	default:
		lines = append(lines, bar(tsDoneBar, m.w, " √ "+L("ГОТОВО", "DONE")+" · "+m.opTitle+L(" — завершено успешно", " — completed successfully"), ""))
	}
	if p := m.overall; p != nil {
		lines = append(lines, m.overallLine(p))
	}
	if p := m.progress; p != nil {
		lines = append(lines, m.progressLine(p))
	}
	if m.toast != "" {
		for _, l := range wrapLines(m.toast, m.w) {
			lines = append(lines, tsSand.Render(l))
		}
	}
	if !m.busy && m.lastOp != "" {
		lines = append(lines, tsFaint.Render(L("сессия ", "session ")+"…"+tail(filepath.Base(m.lastSess), 9)+" · op …"+tail(m.lastOp, 4)+
			L(" · полный путь — в логе", " · full path in the log")))
	}
	if !m.busy {
		lines = append(lines, tsSand.Render(L("Enter — вернуться в меню", "Enter — back to the menu")))
	}
	lines = append(lines, tsFaint.Render(strings.Repeat("┄", m.w)))
	room := h - len(lines)
	var ev []string
	for i := len(m.opEvents) - 1; i >= 0 && len(ev) < room; i-- {
		w := m.opEvents[i].render(m.w, true)
		for j := len(w) - 1; j >= 0 && len(ev) < room; j-- {
			ev = append(ev, w[j])
		}
	}
	for i := len(ev) - 1; i >= 0; i-- {
		lines = append(lines, ev[i])
	}
	return lines
}

// overallLine is the whole operation's progress: a wide bar with the step
// count, so a 30-chunk write shows where it is at a glance.
func (m *tuiModel) overallLine(p *app.Progress) string {
	w := max(10, min(40, m.w-50))
	n := 0
	if p.Total > 0 {
		n = max(0, min(w, int(p.Current*int64(w)/p.Total)))
	}
	pct := int64(0)
	if p.Total > 0 {
		pct = p.Current * 100 / p.Total
	}
	return tsSand.Render(L("Общий прогресс ", "Overall ")) + tsOK.Render(strings.Repeat("█", n)) + tsFaint.Render(strings.Repeat("░", w-n)) +
		tsSand.Render(fmt.Sprintf(" %d%% ", pct)) + tsInk.Render(p.Detail)
}

func (m *tuiModel) progressLine(p *app.Progress) string {
	s := tsSand.Render("[" + p.Label + "] ")
	if p.Total > 0 {
		const w = 24
		n := int(p.Current * w / p.Total)
		n = max(0, min(n, w))
		s += tsOK.Render(strings.Repeat("█", n)) + tsFaint.Render(strings.Repeat("░", w-n)) +
			fmt.Sprintf(" %d%% ", p.Current*100/p.Total)
	}
	return s + tsInk.Render(p.Detail)
}

// viewDialog draws the dialog in at most h lines. The answer part (input,
// buttons, key hints) is pinned at the bottom; the text above it scrolls
// with ↑/↓ when it does not fit, so the operator can always read every
// action and the STOP policy and still see where to answer.
func (m *tuiModel) viewDialog(h int) []string {
	d := m.dlg
	width := min(m.w-2, 90)
	inner := width - 4
	var head, foot []string
	focus := -1 // a head line that must stay visible (the selected item)
	add := func(dst *[]string, st lipgloss.Style, s string) {
		for _, l := range wrapLines(s, inner) {
			*dst = append(*dst, st.Render(l))
		}
	}
	list := func(items []string, cursor int) {
		for i, s := range items {
			if i == cursor {
				focus = len(head)
			}
			head = append(head, choiceLine(i == cursor, s, inner))
		}
	}
	buttons := func(labels []string, cursor int) {
		var bs []string
		for i, l := range labels {
			if i == cursor {
				bs = append(bs, tsBtnOn.Render(l))
			} else {
				bs = append(bs, tsBtn.Render(l))
			}
		}
		foot = append(foot, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, bs...), "\n")...)
	}
	rule := tsFaint.Render(strings.Repeat("─", inner))
	switch {
	case d.isPort:
		add(&head, tsSand, L("UART-порты", "UART ports"))
		head = append(head, rule)
		if len(d.port) == 0 {
			add(&head, tsMuted, L("Порты не найдены. Проверьте кабель и драйвер (CH340, CP210x, FTDI) или введите имя вручную: COM6, /dev/ttyUSB0.", "No ports found. Check the cable and the driver (CH340, CP210x, FTDI) or type a name: COM6, /dev/ttyUSB0."))
		}
		list(append(append([]string{}, d.port...), L("Отключить порт", "Disconnect")), d.cursor)
		foot = append(foot, rule)
		add(&foot, tsFaint, L("↑ ↓ выбрать · Enter подключить · Esc закрыть · имя можно ввести вручную:", "↑ ↓ pick · Enter connect · Esc close · or type a name:"))
		foot = append(foot, m.input.View())
	case d.confirm != nil:
		r := d.confirm.req
		title := r.Title
		if title == "" {
			title = L("Подтверждение", "Confirmation")
		}
		add(&head, tsSand, title)
		for _, s := range r.Summary {
			add(&head, tsInk, s)
		}
		head = append(head, rule)
		add(&head, tsBad, L("Риск: ", "Risk: ")+string(r.Risk))
		for i, act := range r.Actions {
			add(&head, tsInk, fmt.Sprintf("%d. %s", i+1, act))
		}
		if r.CancelNote != "" {
			add(&head, tsMuted, L("Остановка: ", "Stopping: ")+r.CancelNote)
		}
		foot = append(foot, rule)
		if r.Phrase != "" {
			add(&foot, tsInk, L("Введите точно ", "Type exactly ")+tsSand.Render(r.Phrase)+L(" и Enter · Esc — отмена", " and Enter · Esc cancels"))
			foot = append(foot, m.input.View())
		} else {
			buttons([]string{L("Отмена", "Cancel"), L("Подтвердить", "Confirm")}, d.cursor)
			add(&foot, tsFaint, L("← → выбрать · Enter", "← → pick · Enter"))
		}
	case d.ask != nil:
		q := d.ask.req
		if t := strings.TrimSpace(q.Title); t != "" {
			add(&head, tsSand, t)
		}
		p := strings.TrimSpace(q.Prompt)
		if len(q.Quick) > 0 || len(q.Choices) > 0 {
			// Buttons replace the console's letters; a titled question needs
			// no console prompt line at all.
			p = strings.TrimSpace(consoleLetters.ReplaceAllString(p, ""))
			if strings.TrimSpace(q.Title) != "" && len(q.Choices) == 0 {
				p = ""
			}
		}
		if p != "" && p != ">" {
			add(&head, tsInk, p)
		}
		switch {
		case len(q.Choices) > 0:
			head = append(head, rule)
			var items []string
			for _, c := range q.Choices {
				items = append(items, c.Key+". "+c.Label)
			}
			list(items, d.cursor)
			foot = append(foot, rule)
			add(&foot, tsFaint, L("↑ ↓ выбрать · Enter подтвердить · или введите ответ вручную:", "↑ ↓ pick · Enter confirm · or type an answer:"))
			foot = append(foot, m.input.View())
		case len(q.Quick) > 0:
			var labels []string
			for _, c := range q.Quick {
				labels = append(labels, quickLabel(c))
			}
			buttons(labels, d.cursor)
			add(&foot, tsFaint, L("← → выбрать · Enter", "← → pick · Enter"))
		default:
			foot = append(foot, m.input.View())
			add(&foot, tsFaint, L("Enter — ответить", "Enter — answer"))
		}
	}
	room := h - 2 - len(foot) // border
	if room < len(head) {
		room-- // the scroll indicator line
		room = max(1, room)
		if focus >= 0 {
			// Keep the selected item in view.
			if focus < d.scroll {
				d.scroll = focus
			} else if focus >= d.scroll+room {
				d.scroll = focus - room + 1
			}
		}
		d.scroll = max(0, min(d.scroll, len(head)-room))
		shown := append([]string{}, head[d.scroll:d.scroll+room]...)
		more := L(fmt.Sprintf("↑ ↓ прокрутка текста · строки %d–%d из %d", d.scroll+1, d.scroll+room, len(head)),
			fmt.Sprintf("↑ ↓ scroll the text · lines %d–%d of %d", d.scroll+1, d.scroll+room, len(head)))
		head = append(shown, tsSand.Render(more))
	} else {
		d.scroll = 0
	}
	body := append(head, foot...)
	return strings.Split(tsBox.Width(width).Render(strings.Join(body, "\n")), "\n")
}

func choiceLine(sel bool, s string, w int) string {
	if sel {
		return tsSel.Render(ansi.Truncate(" › "+s+" ", w, "…"))
	}
	return tsInk.Render(ansi.Truncate("   "+s, w, "…"))
}

func (m *tuiModel) viewLog(h int) []string {
	names := []string{L("всё", "all"), "UART", L("события", "events")}
	left := " ▌ " + L("ЛОГ UART И СОБЫТИЙ", "UART AND EVENT LOG")
	right := L("фильтр: ", "filter: ") + names[m.filter] + " (F2) · " + L("крупно (F3)", "large (F3)") + " · PgUp/PgDn "
	if m.scroll > 0 {
		right = L("прокрутка — End к новым · ", "scrolled — End for new · ") + right
	}
	lines := m.filtered()
	rows := h - 1
	end := len(lines) - m.scroll
	// Long lines wrap: fill the rows from the newest line upwards.
	var vis []string
	for i := end - 1; i >= 0 && len(vis) < rows; i-- {
		l := lines[i]
		if l.note {
			l.st = tsUART // notes are dark lime like device output; statuses and program steps stand out
		}
		wrapped := l.render(m.w-2, false)
		for j := len(wrapped) - 1; j >= 0 && len(vis) < rows; j-- {
			vis = append(vis, tsGutter.Render("│ ")+wrapped[j])
		}
	}
	if m.logHidden {
		right = L("скрыт — F5 показать ", "hidden — F5 to show ")
	} else {
		right += L("· F5 скрыть ", "· F5 hide ")
	}
	out := []string{bar(tsLogBar, m.w, left, tsLogBar.Render(right))}
	if m.logHidden {
		return fit(out, h)
	}
	for i := len(vis) - 1; i >= 0; i-- {
		out = append(out, vis[i])
	}
	return fit(out, h)
}

func (m *tuiModel) viewHelp() string {
	var s string
	switch {
	case m.dlg != nil:
		s = L(" ↑↓←→ выбор · Enter ответ · PgUp/PgDn лог · Ctrl+C СТОП", " ↑↓←→ pick · Enter answer · PgUp/PgDn log · Ctrl+C STOP")
	case m.busy:
		s = L(" s / Ctrl+C СТОП · PgUp/PgDn лог · F2 фильтр · F3 крупно · F5 скрыть", " s / Ctrl+C STOP · PgUp/PgDn log · F2 filter · F3 large · F5 hide")
	case m.result != "":
		s = L(" Enter — в меню · PgUp/PgDn лог · F2 фильтр · F3 крупно · F5 скрыть", " Enter — menu · PgUp/PgDn log · F2 filter · F3 large · F5 hide")
	default:
		s = L(" ↑↓←→ выбор · Enter запуск · F4 порт · F2 фильтр · F3 лог · l язык · F10 выход", " ↑↓←→ pick · Enter run · F4 port · F2 filter · F3 log · l language · F10 quit")
	}
	if m.w < 80 || m.h < 24 {
		s = L(" окно меньше 80×24 ·", " window below 80×24 ·") + s
	}
	return bar(tsHelpBar, m.w, s, "")
}

// sinceOp returns the latest operation if it started after before.
func sinceOp(a *App, before string) (sess, op string) {
	sess, op = a.LastOperation()
	if op == before {
		return "", ""
	}
	return sess, op
}

// capFirst upper-cases the first letter of a sentence.
func capFirst(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	}
	return string(r)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
