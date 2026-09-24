package main

import (
	"fmt"
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

	tsBrand   = lipgloss.NewStyle().Bold(true).Foreground(tcAmber)
	tsInk     = lipgloss.NewStyle().Foreground(tcInk)
	tsUART    = lipgloss.NewStyle().Foreground(tcUART)
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
	tsLogBar  = lipgloss.NewStyle().Bold(true).Foreground(tcSand).Background(tcLogBg)
	tsBtn     = lipgloss.NewStyle().Foreground(tcInk).Border(lipgloss.RoundedBorder()).BorderForeground(tcFaint).Padding(0, 2)
	tsBtnOn   = lipgloss.NewStyle().Bold(true).Foreground(tcDark).Background(tcSand).Border(lipgloss.RoundedBorder()).BorderForeground(tcSand).Padding(0, 2)
	tsBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tcAmber).Padding(0, 1)
)

type tuiLogKind int

const (
	logUART tuiLogKind = iota
	logEvent
)

type tuiLogLine struct {
	kind tuiLogKind
	text string
	st   lipgloss.Style
}

const tuiLogCap = 5000

type tuiDialog struct {
	ask     *tuiAskMsg
	confirm *tuiConfirmMsg
	port    []string // port chooser: detected ports
	cursor  int
}

type (
	tuiOpDoneMsg struct {
		err  error
		held []tea.Msg
	}
	tuiStopMsg app.CancelState
	tuiPortMsg struct {
		name string
		err  error
	}
)

type tuiModel struct {
	a  *App
	ui *tuiUI

	w, h int

	menu      []tuiGroup
	tab, cur  int
	busy      bool
	opTitle   string
	opEvents  []string
	result    string
	resultBad bool
	progress  *app.Progress
	cancel    app.CancelState
	lastCtrlC time.Time

	log     []tuiLogLine
	partial string
	scroll  int
	filter  int
	logMax  bool

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
		if p.Done {
			m.progress = nil
		} else {
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
		if msg.err != nil {
			m.toast = msg.err.Error()
		} else if msg.name != "" {
			m.toast = L("Подключено: ", "Connected: ") + msg.name
		} else {
			m.toast = L("Порт отключён", "Port disconnected")
		}
	case tuiOpDoneMsg:
		for _, h := range msg.held {
			m.Update(h)
		}
		m.finish(msg.err)
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
	switch {
	case err == nil:
		m.result, m.resultBad = L("Готово.", "Done."), false
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
	case tea.KeyF10:
		return "q"
	case tea.KeyRunes:
		if len(k.Runes) == 1 {
			r := k.Runes[0]
			if l, ok := map[rune]string{'а': "f", 'А': "f", 'ь': "m", 'Ь': "m", 'ы': "s", 'Ы': "s", 'з': "p", 'З': "p", 'й': "q", 'Й': "q", 'о': "j", 'л': "k"}[r]; ok {
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
	case tea.KeyF2, tea.KeyF3:
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
		m.dlg = &tuiDialog{port: listSerialPorts()}
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
	m.busy, m.opTitle, m.opEvents, m.result, m.toast = true, it.label, nil, "", ""
	a := m.a
	if consoleOwning[it.kind] {
		c := &consoleOp{a: a, ui: m.ui, kind: it.kind}
		return tea.Exec(c, func(error) tea.Msg { return tuiOpDoneMsg{err: c.err, held: c.held} })
	}
	return func() tea.Msg {
		var err error
		if it.porting != "" {
			err = a.portingItem(it.porting)
		} else {
			err = a.RunOperation(it.kind)
		}
		return tuiOpDoneMsg{err: err}
	}
}

func quickLabel(c app.Choice) string {
	switch c.Key {
	case "y":
		return L("Да", "Yes")
	case "n":
		return L("Нет", "No")
	}
	return c.Label
}

func (m *tuiModel) dialogKey(k tea.KeyMsg) tea.Cmd {
	d := m.dlg
	switch {
	case d.port != nil:
		return m.portKey(k)
	case d.confirm != nil:
		form := app.FormFor(d.confirm.req.Risk)
		if d.confirm.req.Phrase == "" && form == app.FormButton {
			// Two buttons: Cancel (default) and Confirm.
			switch {
			case k.Type == tea.KeyLeft || k.Type == tea.KeyRight || k.Type == tea.KeyTab || k.Type == tea.KeyUp || k.Type == tea.KeyDown:
				d.cursor = 1 - d.cursor
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
	case tea.KeyLeft, tea.KeyUp, tea.KeyShiftTab:
		d.cursor = (d.cursor + len(q) - 1) % len(q)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		d.cursor = (d.cursor + 1) % len(q)
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
			return func() tea.Msg { return tuiPortMsg{err: owner.Disconnect()} }
		}
		if name == "" && d.cursor < len(d.port) {
			name = d.port[d.cursor]
		}
		if name == "" {
			return nil
		}
		return func() tea.Msg {
			if err := owner.Connect(name); err != nil {
				return tuiPortMsg{err: fmt.Errorf("%s: %w", name, err)}
			}
			return tuiPortMsg{name: name}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return cmd
}

// ---------- log ----------

func eventStyle(l app.Level) lipgloss.Style {
	switch l {
	case app.LevelOK:
		return tsOK
	case app.LevelWarn:
		return tsSand
	case app.LevelError:
		return tsBad
	}
	return tsInk
}

func (m *tuiModel) addEvent(e app.Event) {
	st := eventStyle(e.Level)
	switch e.Kind {
	case app.KindSectionStart:
		m.push(tuiLogLine{logEvent, "── " + e.Text + " ──", tsBrand})
		return
	case app.KindSectionEnd:
		return
	}
	prefix := ""
	if e.Level != app.LevelNote {
		t := e.Time
		if t.IsZero() {
			t = time.Now()
		}
		prefix = t.Format("15:04:05") + " "
	}
	if e.Label != "" {
		prefix += "[" + e.Label + "] "
	}
	for i, l := range strings.Split(strings.Trim(e.Text, "\n"), "\n") {
		l = stripTerminal(l)
		if i == 0 {
			l = prefix + l
		} else if prefix != "" {
			l = strings.Repeat(" ", len([]rune(prefix))) + l
		}
		m.push(tuiLogLine{logEvent, l, st})
		if m.busy && strings.TrimSpace(l) != "" {
			m.opEvents = append(m.opEvents, l)
			if len(m.opEvents) > 50 {
				m.opEvents = m.opEvents[len(m.opEvents)-50:]
			}
		}
	}
}

// addOutput appends raw device output; an unfinished line stays pending so
// prompts are visible before the newline arrives.
func (m *tuiModel) addOutput(s string) {
	s = m.partial + s
	parts := strings.Split(s, "\n")
	m.partial = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		m.push(tuiLogLine{logUART, uartLine(l), tsUART})
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
		out = append(out, tuiLogLine{logUART, uartLine(m.partial), tsUART})
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
		// The whole menu stays visible; the log gives way down to 5 rows.
		mainLines = m.viewMenu(body - 6)
		mainH = max(mainH, min(len(mainLines), body-6))
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
	return st.Render(left + strings.Repeat(" ", gap) + right)
}

func (m *tuiModel) stopLabel() string {
	c := m.cancel
	switch {
	case !m.busy:
		return ""
	case c.Requested:
		return tsStop.Render(L("ОСТАНОВКА ЗАПРОШЕНА", "STOP REQUESTED"))
	case c.Mode == app.CancelUnavailable:
		return tsStopOff.Render(L("ОСТАНОВКА НЕДОСТУПНА", "STOP UNAVAILABLE"))
	case c.Mode == app.CancelAtCheckpoint:
		return tsStop.Render(L("s СТОП · ", "s STOP · ") + c.Checkpoint)
	}
	return tsStop.Render(L("s СТОП", "s STOP"))
}

// viewTop keeps the STOP label whole: on a narrow screen the version, then
// the operation ID, then the port give way first.
func (m *tuiModel) viewTop() string {
	bg := tsBar.GetBackground()
	on := func(st lipgloss.Style, s string) string { return st.Background(bg).Render(s) }
	port := on(tsBad, "● ") + on(tsMuted, L("порт не выбран (p)", "no port (p)"))
	if name, ok := m.a.portOwner().Connected(); ok {
		port = on(tsOK, "● ") + on(tsInk, name)
	}
	op := ""
	if m.busy && m.cancel.Op != "" {
		op = on(tsFaint, "  op …"+tail(m.cancel.Op, 4))
	}
	sep := on(tsFaint, "  │  ")
	brand := on(tsBrand, " UrsidoRescue")
	right := m.stopLabel()
	var left string
	for _, l := range []string{
		brand + on(tsFaint, " "+appVersion) + sep + port + op,
		brand + sep + port + op,
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
	head := strings.Join(tabs, " ") + tsFaint.Render("   ← → раздел · ↑ ↓ пункт · Enter запуск")
	if uiLang == "en" {
		head = strings.Join(tabs, " ") + tsFaint.Render("   ← → section · ↑ ↓ item · Enter run")
	}
	items := m.menu[m.tab].items
	listW := 0
	for _, it := range items {
		listW = max(listW, lipgloss.Width(it.label)+4)
	}
	var list []string
	for i, it := range items {
		if i == m.cur {
			list = append(list, tsSel.Render(" › "+it.label+" "))
		} else {
			list = append(list, tsInk.Render("   "+it.label))
		}
	}
	it := items[m.cur]
	var desc []string
	if it.hint != "" {
		desc = append(desc, tsSand.Render(it.hint))
	}
	if m.tab == 1 {
		desc = append(desc, tsFaint.Render(L("сессия: ", "session: ")+displayDir(m.a.probeDir)))
	}
	lines := []string{head}
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
	// The winking bear goes where it takes nothing away: big in the top-left
	// corner when the whole menu and description still fit, otherwise small
	// under the list, otherwise small over the description.
	need := 1 + len(list)
	if twoCol {
		need = 1 + max(len(list), len(desc)+len(descText))
	} else {
		need += 1 + len(desc) + len(descText)
	}
	if logo := tuiLogo(h-need-1, m.w); logo != nil && len(logo) == len(bearBig) {
		lines = append(append(logo, ""), lines...)
	} else if small := tuiLogo(len(bearSmall), listW-1); twoCol && small != nil && len(list)+1+len(small) <= h-1 {
		list = append(append(list, ""), small...)
	} else if small := tuiLogo(len(bearSmall), descW); twoCol && small != nil {
		desc = append(append(small, ""), desc...)
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
	lines := []string{tsBrand.Render(m.opTitle)}
	switch {
	case m.busy:
		lines = append(lines, tsMuted.Render(L("выполняется… (s — СТОП)", "running… (s — STOP)")))
	case m.resultBad:
		for _, l := range wrapLines(L("ОШИБКА: ", "FAILED: ")+m.result, m.w) {
			lines = append(lines, tsBad.Render(l))
		}
		lines = append(lines, tsMuted.Render(L("Никаких дополнительных write/erase команд после этой ошибки не отправлено.", "No further write/erase commands were sent after this error.")))
	default:
		lines = append(lines, tsOK.Render(m.result))
	}
	if p := m.progress; p != nil {
		lines = append(lines, m.progressLine(p))
	}
	if m.toast != "" {
		for _, l := range wrapLines(m.toast, m.w) {
			lines = append(lines, tsSand.Render(l))
		}
	}
	if !m.busy {
		lines = append(lines, tsSand.Render(L("Enter — вернуться в меню", "Enter — back to the menu")))
	}
	lines = append(lines, tsFaint.Render(strings.Repeat("┄", m.w)))
	room := h - len(lines)
	var ev []string
	for i := len(m.opEvents) - 1; i >= 0 && len(ev) < room; i-- {
		w := wrapLines(m.opEvents[i], m.w)
		for j := len(w) - 1; j >= 0 && len(ev) < room; j-- {
			ev = append(ev, w[j])
		}
	}
	for i := len(ev) - 1; i >= 0; i-- {
		lines = append(lines, tsUART.Render(ev[i]))
	}
	return lines
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

func (m *tuiModel) viewDialog(h int) []string {
	d := m.dlg
	width := min(m.w-2, 90)
	inner := width - 4
	var body []string
	add := func(st lipgloss.Style, s string) {
		for _, l := range wrapLines(s, inner) {
			body = append(body, st.Render(l))
		}
	}
	list := func(items []string, cursor int) {
		for i, s := range items {
			body = append(body, choiceLine(i == cursor, s, inner))
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
		body = append(body, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, bs...), "\n")...)
	}
	rule := tsFaint.Render(strings.Repeat("─", inner))
	switch {
	case d.port != nil:
		add(tsSand, L("UART-порты", "UART ports"))
		body = append(body, rule)
		list(append(append([]string{}, d.port...), L("Отключить порт", "Disconnect")), d.cursor)
		body = append(body, rule)
		add(tsFaint, L("↑ ↓ выбрать · Enter подключить · Esc закрыть · имя можно ввести вручную:", "↑ ↓ pick · Enter connect · Esc close · or type a name:"))
		body = append(body, m.input.View())
	case d.confirm != nil:
		r := d.confirm.req
		title := r.Title
		if title == "" {
			title = L("Подтверждение", "Confirmation")
		}
		add(tsSand, title)
		for _, s := range r.Summary {
			add(tsInk, s)
		}
		body = append(body, rule)
		add(tsBad, L("Риск: ", "Risk: ")+string(r.Risk))
		for i, act := range r.Actions {
			add(tsInk, fmt.Sprintf("%d. %s", i+1, act))
		}
		if r.CancelNote != "" {
			add(tsMuted, L("Остановка: ", "Stopping: ")+r.CancelNote)
		}
		body = append(body, rule)
		if r.Phrase != "" {
			add(tsInk, L("Введите точно ", "Type exactly ")+tsSand.Render(r.Phrase)+L(" и Enter · Esc — отмена", " and Enter · Esc cancels"))
			body = append(body, m.input.View())
		} else {
			buttons([]string{L("Отмена", "Cancel"), L("Подтвердить", "Confirm")}, d.cursor)
			add(tsFaint, L("← → выбрать · Enter", "← → pick · Enter"))
		}
	case d.ask != nil:
		q := d.ask.req
		if t := strings.TrimSpace(q.Title); t != "" {
			add(tsSand, t)
		}
		if p := strings.TrimSpace(q.Prompt); p != "" && p != ">" {
			add(tsInk, p)
		}
		switch {
		case len(q.Choices) > 0:
			body = append(body, rule)
			var items []string
			for _, c := range q.Choices {
				items = append(items, c.Key+". "+c.Label)
			}
			list(items, d.cursor)
			body = append(body, rule)
			add(tsFaint, L("↑ ↓ выбрать · Enter подтвердить · или введите ответ вручную:", "↑ ↓ pick · Enter confirm · or type an answer:"))
			body = append(body, m.input.View())
		case len(q.Quick) > 0:
			var labels []string
			for _, c := range q.Quick {
				labels = append(labels, quickLabel(c))
			}
			buttons(labels, d.cursor)
			add(tsFaint, L("← → выбрать · Enter", "← → pick · Enter"))
		default:
			body = append(body, m.input.View())
			add(tsFaint, L("Enter — ответить", "Enter — answer"))
		}
	}
	if room := h - 2; room > 0 && len(body) > room {
		body = append(body[:room-1], tsFaint.Render("…"))
	}
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
	right := L("фильтр: ", "filter: ") + names[m.filter] + " (f) · " + L("крупно (m)", "large (m)") + " · PgUp/PgDn "
	if m.scroll > 0 {
		right = L("прокрутка — End к новым · ", "scrolled — End for new · ") + right
	}
	lines := m.filtered()
	rows := h - 1
	end := len(lines) - m.scroll
	// Long lines wrap: fill the rows from the newest line upwards.
	var vis []string
	for i := end - 1; i >= 0 && len(vis) < rows; i-- {
		wrapped := wrapLines(lines[i].text, m.w-2)
		for j := len(wrapped) - 1; j >= 0 && len(vis) < rows; j-- {
			vis = append(vis, tsFaint.Render("│ ")+lines[i].st.Render(wrapped[j]))
		}
	}
	out := []string{bar(tsLogBar, m.w, left, right)}
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
		s = L(" s / Ctrl+C СТОП · PgUp/PgDn лог · f фильтр · m лог крупно", " s / Ctrl+C STOP · PgUp/PgDn log · f filter · m large log")
	case m.result != "":
		s = L(" Enter — в меню · PgUp/PgDn лог · f фильтр · m лог крупно", " Enter — menu · PgUp/PgDn log · f filter · m large log")
	default:
		s = L(" ↑↓ пункт · ←→ раздел · Enter запуск · p порт · f фильтр лога · m лог крупно · q выход", " ↑↓ item · ←→ section · Enter run · p port · f log filter · m large log · q quit")
	}
	if m.w < 80 || m.h < 24 {
		s = L(" окно меньше 80×24 ·", " window below 80×24 ·") + s
	}
	return bar(tsBar.Foreground(tcMuted), m.w, s, "")
}
