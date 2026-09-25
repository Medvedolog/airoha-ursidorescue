package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// Terminal chrome: when the UART terminal is started from the TUI it keeps the
// UrsidoRescue look (a header bar and a key-hint bar) around the raw device
// stream instead of dropping the operator into a bare black console. Only the
// presentation changes: the bytes still go verbatim between the UART and the
// real terminal (term_run.go). The bars sit outside an ANSI scroll region
// (DECSTBM), so device output scrolls between them.
//
// A screen-oriented program on the device (top, vi, a U-Boot bootmenu) owns
// the whole screen: as soon as its control sequences show up the region is
// reset and the chrome steps aside; it comes back when the program leaves the
// alternate screen, or when the operator opens the Ctrl+] menu.
//
// The text console (--console) keeps its plain output.

type termChrome struct {
	title      string
	rows, cols int
	on         bool // bars drawn, scroll region set
	suspended  bool // a fullscreen device program owns the screen
	parser     chromeParser
}

type chromeInfo struct {
	port, session string
	raw, pager    bool
	log           bool
	simple        bool
}

// chromeSize is the terminal size; a variable so tests can resize.
var chromeSize = func() (rows, cols int) {
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 0, 0
	}
	return h, w
}

func (t *uartTerm) chromeInfoLocked() chromeInfo {
	ci := chromeInfo{port: t.s.Name(), raw: t.raw, pager: t.pagerEnabled, simple: t.simple}
	t.a.logMu.Lock()
	ci.log = t.a.logFile != nil
	t.a.logMu.Unlock()
	if t.a.sess != nil {
		ci.session = tail(filepath.Base(t.a.sess.Dir), 5)
	}
	return ci
}

func chromeMode(ci chromeInfo) string {
	if ci.raw {
		return "RAW"
	}
	return L("СТРОКИ", "LINE")
}

// chromeHeader is the top bar in the TUI's top-bar colours.
func chromeHeader(w int, ci chromeInfo) string {
	bg := tsBar.GetBackground()
	on := func(st lipgloss.Style, s string) string { return st.Background(bg).Render(s) }
	sep := on(tsFaint, "  │  ")
	logDot := on(tsBad, " ○")
	if ci.log {
		logDot = on(tsOK, " ●")
	}
	brand := on(tsBrand, " UrsidoRescue")
	port := on(tsInk, ci.port) + on(tsOK, " ●")
	for _, l := range []string{
		brand + on(tsFaint, " "+appVersion) + sep + port + sep + on(tsInk, "UART 115200 8N1") + sep + on(tsSand, chromeMode(ci)) + sep + on(tsInk, "LOG") + logDot,
		brand + sep + port + sep + on(tsInk, "115200 8N1") + sep + on(tsSand, chromeMode(ci)) + sep + on(tsInk, "LOG") + logDot,
		brand + sep + port + sep + on(tsSand, chromeMode(ci)),
		brand + sep + port,
	} {
		if lipgloss.Width(l) < w {
			return bar(tsBar, w, l, "")
		}
	}
	return bar(tsBar, w, brand, "")
}

// chromeRule is the section rule under the header.
func chromeRule(w int, title string) string {
	head := "── " + title + " "
	if n := w - lipgloss.Width(head); n > 0 {
		head += strings.Repeat("─", n)
	}
	return tsRule.Render(truncW(head, w))
}

// chromeFooter is the key-hint bar in the TUI's help-bar colours.
func chromeFooter(w int, ci chromeInfo) string {
	var keys []string
	if ci.simple {
		keys = []string{L("Ctrl+] / Ctrl+Q назад", "Ctrl+] / Ctrl+Q back")}
	} else {
		keys = []string{L("Ctrl+] меню", "Ctrl+] menu"), L("Ctrl+Q назад", "Ctrl+Q back")}
	}
	pager := L("Ctrl+P пейджер", "Ctrl+P pager")
	if ci.pager {
		pager += L(" (вкл)", " (on)")
	}
	keys = append(keys, pager)
	right := chromeMode(ci)
	if ci.session != "" {
		right += L("  сессия …", "  session …") + ci.session
	}
	right += " "
	left := " " + strings.Join(keys, " · ")
	if lipgloss.Width(left)+lipgloss.Width(right)+1 > w {
		return bar(tsHelpBar, w, truncW(left, w-1), "")
	}
	return bar(tsHelpBar, w, left, tsHelpBar.Render(right))
}

func truncW(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w {
		r = r[:len(r)-1]
	}
	return string(r)
}

// chromePlate is the entry notice printed once inside the scroll region, so an
// empty UART does not look like a broken program.
func chromePlate(w int, title, port string, simple bool) string {
	lines := []string{
		tsBrand.Render(title),
		tsInk.Render(port + " · 115200 8N1 · " + L("сам ничего не отправляет — только ваши клавиши", "sends nothing by itself, only your keys")),
	}
	if simple {
		lines = append(lines, tsInk.Render(L("Ctrl+] или Ctrl+Q — назад в меню · Ctrl+P — пейджер", "Ctrl+] or Ctrl+Q back to the menu · Ctrl+P pager")))
	} else {
		lines = append(lines, tsInk.Render(L("Ctrl+] — меню: XMODEM, построчный ввод, лог · Ctrl+Q — назад · Ctrl+P — пейджер", "Ctrl+] menu: XMODEM, line input, log · Ctrl+Q back · Ctrl+P pager")))
	}
	lines = append(lines, tsMuted.Render(L("Пустой экран — это нормально: ждём вывод устройства. Нажмите Enter, чтобы увидеть приглашение.",
		"An empty screen is normal: waiting for the device. Press Enter to see its prompt.")))
	box := tsBox.Width(max(20, min(w-2, 96)))
	return strings.ReplaceAll(box.Render(strings.Join(lines, "\n")), "\n", "\r\n") + "\r\n"
}

// chromeStartLocked draws the bars and sets the scroll region. The old screen
// content is pushed into the scrollback rather than erased.
func (t *uartTerm) chromeStartLocked(clear bool) {
	c := t.chrome
	c.rows, c.cols = chromeSize()
	if c.rows < 10 || c.cols < 40 {
		c.on = false
		return
	}
	var b strings.Builder
	if clear {
		b.WriteString("\x1b[r\x1b[2J")
	} else {
		// Push what is on screen into the scrollback, then start clean.
		b.WriteString("\x1b[r")
		b.WriteString(strings.Repeat("\r\n", c.rows))
		b.WriteString("\x1b[H\x1b[2J")
	}
	c.on, c.suspended = true, false
	b.WriteString(t.chromeBarsLocked())
	fmt.Fprintf(&b, "\x1b[3;%dr\x1b[3;1H", c.rows-2)
	os.Stdout.WriteString(b.String())
}

// chromeBarsLocked renders the four fixed rows with absolute positioning.
func (t *uartTerm) chromeBarsLocked() string {
	c := t.chrome
	ci := t.chromeInfoLocked()
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[1;1H\x1b[2K%s", chromeHeader(c.cols, ci))
	fmt.Fprintf(&b, "\x1b[2;1H\x1b[2K%s", chromeRule(c.cols, c.title))
	fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K%s", c.rows-1, tsGutter.Render(strings.Repeat("─", c.cols)))
	fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K%s", c.rows, chromeFooter(c.cols, ci))
	return b.String()
}

// chromeRefreshLocked redraws the bars (a mode changed) and follows a window
// resize, keeping the cursor where the device left it.
func (t *uartTerm) chromeRefreshLocked() {
	c := t.chrome
	if c == nil || !c.on || c.suspended {
		return
	}
	rows, cols := chromeSize()
	if rows < 10 || cols < 40 {
		os.Stdout.WriteString("\x1b7\x1b[r\x1b8")
		c.on = false
		return
	}
	c.rows, c.cols = rows, cols
	os.Stdout.WriteString("\x1b7" + t.chromeBarsLocked() + fmt.Sprintf("\x1b[3;%dr", rows-2) + "\x1b8")
}

// chromeSizeCheckLocked follows a window resize, rows or columns.
func (t *uartTerm) chromeSizeCheckLocked() {
	c := t.chrome
	if c == nil || !c.on || c.suspended {
		return
	}
	if rows, cols := chromeSize(); rows != c.rows || cols != c.cols {
		t.chromeRefreshLocked()
	}
}

// chromeApplyLocked carries out what the parser saw in the device stream.
func (t *uartTerm) chromeApplyLocked(act chromeAct) {
	c := t.chrome
	switch act {
	case actSuspend:
		if c.on && !c.suspended {
			c.suspended = true
			os.Stdout.WriteString("\x1b7\x1b[r\x1b8")
		}
	case actResume:
		// Leaving the alternate screen brings the main screen back as it was,
		// bars included; only the scroll region needs setting again.
		if c.suspended {
			c.suspended = false
			t.chromeRefreshLocked()
		}
	case actResumeClear:
		if c.suspended {
			t.chromeStartLocked(true)
		}
	case actRefresh:
		t.chromeRefreshLocked()
	}
}

// chromeResumeLocked brings the chrome back on the operator's request (the
// Ctrl+] menu), whatever the device is doing.
func (t *uartTerm) chromeResumeLocked() {
	if c := t.chrome; c != nil && c.suspended {
		c.parser.mode, c.parser.hidden = modeNone, false
		t.chromeStartLocked(true)
	}
}

// chromeStopLocked resets the scroll region when the terminal closes.
func (t *uartTerm) chromeStopLocked() {
	c := t.chrome
	if c == nil || !c.on {
		return
	}
	fmt.Fprintf(os.Stdout, "\x1b[r\x1b[%d;1H\r\n", max(1, c.rows))
	c.on = false
}

// chromeRows is the height of the scrolling area, for the pager.
func (t *uartTerm) chromeRowsLocked() int {
	if c := t.chrome; c != nil && c.on && !c.suspended {
		return c.rows - 4
	}
	return 0
}
