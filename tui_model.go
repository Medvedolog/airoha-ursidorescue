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

// Palette of UrsusBoot (doc/ui-mockup/style.css). Foreground only, so the
// terminal's own background stays; lipgloss drops colours under NO_COLOR.
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

	tsBrand   = lipgloss.NewStyle().Bold(true).Foreground(tcAmber)
	tsInk     = lipgloss.NewStyle().Foreground(tcInk)
	tsUART    = lipgloss.NewStyle().Foreground(tcUART)
	tsMuted   = lipgloss.NewStyle().Foreground(tcMuted)
	tsFaint   = lipgloss.NewStyle().Foreground(tcFaint)
	tsSand    = lipgloss.NewStyle().Bold(true).Foreground(tcSand)
	tsOK      = lipgloss.NewStyle().Foreground(tcOK)
	tsBad     = lipgloss.NewStyle().Foreground(tcBad)
	tsSel     = lipgloss.NewStyle().Bold(true).Foreground(tcSand)
	tsStop    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(tcStopBg).Padding(0, 1)
	tsStopOff = lipgloss.NewStyle().Bold(true).Foreground(tcSand).Padding(0, 1)
	tsTab     = lipgloss.NewStyle().Foreground(tcMuted).Padding(0, 1)
	tsTabOn   = lipgloss.NewStyle().Bold(true).Foreground(tcSand).Underline(true).Padding(0, 1)
	tsBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tcAmber).Padding(0, 1)
)

// tuiItem is one menu entry; run is called in the operation goroutine.
type tuiItem struct {
	label, hint string
	kind        string // catalogue operation, "" for Porting items
	porting     string // Porting item key
}

type tuiGroup struct {
	title string
	items []tuiItem
}

// tuiMenu mirrors the console menus (doc/MENU_RU.md), so both front ends
// offer the same operations under the same names.
func tuiMenu() []tuiGroup {
	return []tuiGroup{
		{L("Главное", "Main"), []tuiItem{
			{label: L("Восстановить заводскую Nokia", "Restore stock Nokia"), hint: "mtd16 / all_flash", kind: "stock-restore"},
			{label: L("Починить загрузку / заменить FIP", "Repair boot / replace the FIP"), hint: L("UBI-том fip", "UBI volume fip"), kind: "fip-repair"},
			{label: L("Восстановить весь NAND", "Restore the full NAND"), hint: "physical 256 MiB", kind: "physical-restore"},
			{label: L("Загрузить ITB в RAM", "Boot an ITB from RAM"), hint: L("без записи", "no write"), kind: "itb-boot"},
			{label: L("Диагностика NAND / UBI / U-Boot", "NAND / UBI / U-Boot diagnostics"), hint: L("только чтение", "read-only"), kind: "diagnostics"},
			{label: L("Собрать пакет логов", "Build a log bundle"), hint: L("для отчёта", "for a report"), kind: "support-bundle"},
		}},
		{L("Портирование", "Porting"), []tuiItem{
			{label: L("Полный probe нового устройства", "Full probe of a new device"), hint: "+ porting bundle", porting: "1"},
			{label: L("Профиль BootROM", "BootROM profile"), porting: "2"},
			{label: L("Профиль U-Boot", "U-Boot profile"), porting: "3"},
			{label: L("Профиль Linux", "Linux profile"), porting: "4"},
			{label: L("Карта flash / MTD / UBI", "Flash / MTD / UBI map"), porting: "5"},
			{label: L("DTB / device tree", "DTB / device tree"), porting: "6"},
			{label: L("Сеть / PHY / коммутатор", "Network / PHY / switch"), porting: "7"},
			{label: L("Экспорт porting bundle", "Export the porting bundle"), porting: "8"},
			{label: L("Показать собранный профиль", "View the collected profile"), porting: "9"},
			{label: L("ADVANCED: UBI attach", "ADVANCED: UBI attach"), hint: L("НЕ только чтение", "NOT read-only"), porting: "A"},
			{label: L("Новая probe-сессия", "New probe session"), porting: "N"},
		}},
		{L("Эксперт", "Expert"), []tuiItem{
			{label: L("UART-терминал", "UART terminal"), hint: L("на весь экран", "full screen"), kind: "terminal"},
			{label: L("RAM U-Boot и prompt", "RAM U-Boot and prompt"), hint: L("на весь экран", "full screen"), kind: "ram-uboot"},
			{label: L("Записать UBI-том из файла", "Write a UBI volume from a file"), kind: "ubi-volume"},
			{label: L("Raw-запись в MTD bl2/ubi", "Raw write into MTD bl2/ubi"), kind: "raw-mtd"},
			{label: L("Диагностика", "Diagnostics"), kind: "diagnostics"},
			{label: L("UART Shell", "UART Shell"), hint: L("на весь экран", "full screen"), kind: "shell"},
		}},
	}
}

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
		m.openDialog(&tuiDialog{ask: &msg})
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

func (m *tuiModel) openDialog(d *tuiDialog) {
	m.dlg = d
	m.input.SetValue("")
	m.input.EchoMode = textinput.EchoNormal
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
	}
	if m.dlg != nil {
		return m.dialogKey(k)
	}
	m.toast = ""
	switch k.String() {
	case "f":
		m.filter = (m.filter + 1) % 3
		m.scroll = 0
		return nil
	case "m":
		m.logMax = !m.logMax
		return nil
	case "s":
		if m.busy {
			return m.stop()
		}
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
	switch k.String() {
	case "q":
		return tea.Quit
	case "left", "shift+tab":
		m.tab = (m.tab + len(m.menu) - 1) % len(m.menu)
		m.cur = 0
	case "right", "tab":
		m.tab = (m.tab + 1) % len(m.menu)
		m.cur = 0
	case "up", "k":
		m.cur = (m.cur + len(items) - 1) % len(items)
	case "down", "j":
		m.cur = (m.cur + 1) % len(items)
	case "p":
		m.dlg = &tuiDialog{port: listSerialPorts()}
		m.input.SetValue("")
		m.input.Focus()
	case "enter":
		return m.start(items[m.cur])
	}
	return nil
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

func (m *tuiModel) dialogKey(k tea.KeyMsg) tea.Cmd {
	d := m.dlg
	switch {
	case d.port != nil:
		return m.portKey(k)
	case d.confirm != nil:
		form := app.FormFor(d.confirm.req.Risk)
		if d.confirm.req.Phrase == "" && form == app.FormButton {
			switch strings.ToLower(k.String()) {
			case "y", "д":
				m.answer(d.confirm.reply, "y")
			case "n", "н", "esc", "enter":
				m.answer(d.confirm.reply, "n")
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
		ch := d.ask.req.Choices
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
	case app.LevelNote:
		return tsInk
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

// logRows is the number of log text rows. The log always stays on screen
// (≈35 %, at least 5 rows); a dialog takes priority over its extra height.
func (m *tuiModel) logRows() int {
	body := m.h - 2
	if m.logMax && m.dlg == nil {
		return max(5, body-4)
	}
	return max(5, min(m.h*35/100, body-8))
}

func (m *tuiModel) View() string {
	body := m.h - 2
	logH := m.logRows() + 1
	mainH := body - logH
	var mainLines []string
	switch {
	case m.dlg != nil:
		// The dialog takes what it needs; the rest stays with the log.
		mainLines = m.viewDialog(body - 6)
		mainH = min(len(mainLines), body-6)
		logH = body - mainH
	case m.busy || m.result != "":
		mainLines = m.viewOperation(mainH)
	default:
		mainLines = m.viewMenu(mainH)
	}
	out := []string{m.viewTop()}
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
	port := tsBad.Render("● ") + tsMuted.Render(L("порт не выбран", "no port"))
	if name, ok := m.a.portOwner().Connected(); ok {
		port = tsOK.Render("● ") + tsInk.Render(name)
	}
	op := ""
	if m.busy && m.cancel.Op != "" {
		op = "  " + tsFaint.Render("op …"+tail(m.cancel.Op, 4))
	}
	brand := tsBrand.Render("UrsidoRescue")
	right := m.stopLabel()
	var left string
	for _, l := range []string{
		brand + " " + tsFaint.Render(appVersion) + "  " + port + op,
		brand + "  " + port + op,
		brand + "  " + port,
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
	gap := max(1, m.w-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
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
	lines := []string{strings.Join(tabs, " "), ""}
	for i, it := range m.menu[m.tab].items {
		mark, st := "  ", tsInk
		if i == m.cur {
			mark, st = "› ", tsSel
		}
		l := mark + st.Render(it.label)
		if it.hint != "" {
			l += "  " + tsFaint.Render(it.hint)
		}
		lines = append(lines, l)
	}
	if m.tab == 1 && len(lines) < h {
		lines = append(lines, tsFaint.Render(L("Сессия: ", "Session: ")+displayDir(m.a.probeDir)))
	}
	if m.toast != "" {
		lines = append(lines, "", tsSand.Render(m.toast))
	}
	return lines
}

func (m *tuiModel) viewOperation(h int) []string {
	lines := []string{tsBrand.Render(m.opTitle)}
	switch {
	case m.busy:
		lines = append(lines, tsMuted.Render(L("выполняется…", "running…")))
	case m.resultBad:
		lines = append(lines, tsBad.Render(L("ОШИБКА: ", "FAILED: ")+m.result),
			tsMuted.Render(L("Никаких дополнительных write/erase команд после этой ошибки не отправлено.", "No further write/erase commands were sent after this error.")))
	default:
		lines = append(lines, tsOK.Render(m.result))
	}
	if p := m.progress; p != nil {
		lines = append(lines, m.progressLine(p))
	}
	if m.toast != "" {
		lines = append(lines, tsSand.Render(m.toast))
	}
	if !m.busy {
		lines = append(lines, tsFaint.Render(L("Enter — в меню", "Enter — back to the menu")))
	}
	lines = append(lines, "")
	room := h - len(lines)
	ev := m.opEvents
	if room > 0 && len(ev) > room {
		ev = ev[len(ev)-room:]
	}
	for _, e := range ev {
		lines = append(lines, tsUART.Render(e))
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
	width := min(m.w-2, 78)
	inner := width - 4
	var body []string
	add := func(st lipgloss.Style, s string) {
		for _, l := range strings.Split(lipgloss.NewStyle().Width(inner).Render(s), "\n") {
			body = append(body, st.Render(l))
		}
	}
	switch {
	case d.port != nil:
		add(tsSand, L("UART-порты", "UART ports"))
		for i, p := range d.port {
			body = append(body, choiceLine(i == d.cursor, p))
		}
		body = append(body, choiceLine(d.cursor == len(d.port), L("Отключить", "Disconnect")))
		add(tsMuted, L("Enter — подключить; можно ввести имя вручную. Esc — закрыть.", "Enter connects; you can type a name. Esc closes."))
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
		add(tsBad, L("Риск: ", "Risk: ")+string(r.Risk))
		for i, act := range r.Actions {
			add(tsInk, fmt.Sprintf("%d. %s", i+1, act))
		}
		if r.CancelNote != "" {
			add(tsMuted, L("Остановка: ", "Stopping: ")+r.CancelNote)
		}
		if r.Phrase != "" {
			add(tsInk, L("Введите точно ", "Type exactly ")+tsSand.Render(r.Phrase)+L(" и Enter; Esc — отмена", " and Enter; Esc cancels"))
			body = append(body, m.input.View())
		} else {
			add(tsSand, L("y — подтвердить · n / Esc — отмена", "y confirm · n / Esc cancel"))
		}
	case d.ask != nil:
		q := d.ask.req
		if t := strings.TrimSpace(q.Title); t != "" {
			add(tsSand, t)
		}
		for i, c := range q.Choices {
			body = append(body, choiceLine(i == d.cursor, c.Key+". "+c.Label))
		}
		add(tsInk, strings.TrimSpace(q.Prompt))
		if len(q.Choices) > 0 {
			add(tsFaint, L("↑↓ и Enter — выбрать пункт, или введите ответ", "↑↓ and Enter pick an item, or type an answer"))
		}
		body = append(body, m.input.View())
	}
	if room := h - 2; room > 0 && len(body) > room {
		body = append(body[:room-1], tsFaint.Render("…"))
	}
	return strings.Split(tsBox.Width(width).Render(strings.Join(body, "\n")), "\n")
}

func choiceLine(sel bool, s string) string {
	if sel {
		return tsSel.Render("› " + s)
	}
	return tsInk.Render("  " + s)
}

func (m *tuiModel) viewLog(h int) []string {
	names := []string{L("всё", "all"), "UART", L("события", "events")}
	head := tsBrand.Render("UART") + " " + tsMuted.Render("· f "+names[m.filter])
	if m.scroll > 0 {
		head += tsSand.Render(L("  · прокрутка, End — к новым", "  · scrolled, End for new lines"))
	}
	lines := m.filtered()
	rows := h - 1
	end := len(lines) - m.scroll
	// Long lines wrap: fill the rows from the newest line upwards.
	var vis []string
	for i := end - 1; i >= 0 && len(vis) < rows; i-- {
		wrapped := strings.Split(ansi.Wrap(lines[i].text, max(20, m.w), ""), "\n")
		for j := len(wrapped) - 1; j >= 0 && len(vis) < rows; j-- {
			vis = append(vis, lines[i].st.Render(wrapped[j]))
		}
	}
	out := []string{head}
	for i := len(vis) - 1; i >= 0; i-- {
		out = append(out, vis[i])
	}
	return fit(out, h)
}

func (m *tuiModel) viewHelp() string {
	var s string
	switch {
	case m.dlg != nil:
		s = L("Enter ответ · PgUp/PgDn лог · Ctrl+C СТОП", "Enter answer · PgUp/PgDn log · Ctrl+C STOP")
	case m.busy:
		s = L("s / Ctrl+C СТОП · PgUp/PgDn лог · f фильтр · m лог крупно", "s / Ctrl+C STOP · PgUp/PgDn log · f filter · m large log")
	default:
		s = L("↑↓ выбор · ←→ раздел · Enter запуск · p порт · f фильтр · m лог · q выход", "↑↓ select · ←→ section · Enter run · p port · f filter · m log · q quit")
	}
	if m.w < 80 || m.h < 24 {
		s = L("окно меньше 80×24 · ", "window below 80×24 · ") + s
	}
	return tsFaint.Render(s)
}
