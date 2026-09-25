package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// uartTerm is one interactive terminal session on a serial port.
type uartTerm struct {
	a         *App
	s         Serial
	mu        sync.Mutex // guards stdout and the shared editor display
	ed        lineEditor
	dec       ansiDecoder
	raw       bool
	simple    bool
	prompt    string
	quit      bool
	shownW    int  // rune width of the input line currently on screen (line mode)
	lineShown bool // an input line is drawn and needs erasing before device output

	pagerEnabled    bool
	pagerWaiting    bool
	pagerLines      int
	pagerPending    []byte
	pagerANSIProbe  []byte
	lastASCIINotice time.Time

	chrome *termChrome // UrsidoRescue bars around the stream (TUI only); nil in the text console
}

// runTerminal opens the port and runs the interactive terminal.
func (a *App) runTerminal() error {
	s, e := a.openPort()
	if e != nil {
		return e
	}
	defer s.Close()
	if _, e = a.startLog("term"); e != nil {
		return e
	}
	defer a.closeLog()
	return a.runTerminalOn(s)
}

func (a *App) runTerminalOn(s Serial) error {
	return a.runTerminalOnMode(s, false)
}

func (a *App) runTerminalOnMode(s Serial, simple bool) error {
	// Raw passthrough is the default: device output is printed verbatim, so it
	// is clean and copyable, and the device's own line editing/history works.
	t := &uartTerm{a: a, s: s, prompt: "] ", raw: true, simple: simple}
	t.ed.hidx = 0
	title := L("UART-терминал + XMODEM", "UART terminal + XMODEM")
	if simple {
		title = L("Прозрачная UART-консоль", "Transparent UART console")
	}
	if a.frontEnd == "tui" {
		t.chrome = &termChrome{title: strings.ToUpper(title)}
	} else if simple {
		fmt.Printf(L("\nПрозрачная UART-консоль %s — 115200 8N1, без flow control\n", "\nTransparent UART console %s — 115200 8N1, no flow\n"), s.Name())
		fmt.Println(L("Ctrl+] / Ctrl+Q — выход, Ctrl+P — pager для обычного текста; полноэкранные TUI обходят его автоматически.",
			"Ctrl+] / Ctrl+Q — exit, Ctrl+P — text pager; fullscreen TUIs bypass it automatically."))
		fmt.Println(L("Никаких автоматических x/Enter/Ctrl-C не отправляется.", "No automatic x/Enter/Ctrl-C is sent."))
	} else {
		fmt.Println(L("\nUART-терминал + XMODEM, 115200 8N1 — прозрачный режим (вывод как есть, копируется).",
			"\nUART terminal + XMODEM, 115200 8N1 — raw passthrough (verbatim output, copyable)."))
		fmt.Println(L("Ctrl+] — меню: l — построчный ввод с историей ↑/↓, s/r — XMODEM отправка/приём, g — лог, q — выход.",
			"Ctrl+] — menu: l line-input with ↑/↓ history, s/r XMODEM send/receive, g log, q quit."))
		fmt.Println(L("Ctrl+Q — быстрый выход, Ctrl+P — pager для обычного текстового вывода. top/vi и другие TUI отключают pager автоматически.",
			"Ctrl+Q — quick exit, Ctrl+P — pager for normal text output. top/vi and other TUIs disable it automatically."))
	}
	if t.chrome == nil {
		fmt.Println(L("В Windows QuickEdit/clipboard остаётся включён. Всё пишется в лог.",
			"On Windows QuickEdit/clipboard stays enabled. Everything is logged."))
	}
	state, e := consoleRaw()
	if e != nil {
		return fmt.Errorf(L("raw-консоль: %w", "raw console: %w"), e)
	}
	defer consoleRestore(state)
	if t.chrome != nil {
		t.mu.Lock()
		t.chromeStartLocked(false)
		os.Stdout.WriteString(chromePlate(max(40, t.chrome.cols), title, s.Name(), simple))
		t.mu.Unlock()
		defer func() {
			t.mu.Lock()
			t.chromeStopLocked()
			t.mu.Unlock()
		}()
	}

	rxErr := make(chan error, 1)
	go t.readLoop(rxErr)

	ib := make([]byte, 256)
	for !t.quit {
		select {
		case err := <-rxErr:
			fmt.Print("\r\n")
			return err
		default:
		}
		n, err := consoleReadInput(ib)
		if err != nil {
			return err
		}
		if t.raw {
			// Forward whole console chunks, not one UART write per byte. This keeps
			// ANSI arrow/history sequences together and makes pasted text fast.
			if err := t.writeRawInput(ib[:n]); err != nil {
				return err
			}
			if t.quit {
				return nil
			}
			continue
		}
		for _, b := range ib[:n] {
			if b >= 0x80 {
				t.warnNonASCII()
				continue
			}
			if b == 0x11 { // Ctrl+Q is always local; never send it to the router.
				t.quit = true
				fmt.Print(L("\r\n[выход из UART-терминала: Ctrl+Q]\r\n", "\r\n[UART terminal exit: Ctrl+Q]\r\n"))
				return nil
			}
			if b == 0x10 {
				t.togglePager()
				continue
			}
			if t.pagerIsWaiting() && (b == '\r' || b == '\n') {
				t.pagerContinue()
				continue
			}
			for _, ev := range t.dec.push(b) {
				t.onKey(ev)
				if t.quit {
					return nil
				}
			}
		}
	}
	return nil
}

// writeRawInput forwards normal console input in the same chunks in which it
// arrived. Ctrl+] opens the local menu and Ctrl+Q leaves the terminal; neither
// control byte is sent to the router.
func filterASCIICommandInput(p []byte) (out []byte, blocked bool) {
	out = make([]byte, 0, len(p))
	for _, b := range p {
		if b >= 0x80 {
			blocked = true
			continue
		}
		out = append(out, b)
	}
	return out, blocked
}

func splitOutputPage(data []byte, maxLines int) (head, tail []byte, lines int, full bool) {
	if maxLines < 1 {
		maxLines = 1
	}
	for i, b := range data {
		if b == '\n' {
			lines++
			if lines >= maxLines {
				return data[:i+1], data[i+1:], lines, true
			}
		}
	}
	return data, nil, lines, false
}

// hasFullscreenANSI detects terminal-control sequences used by screen-oriented
// programs such as top/vi/less. A line pager must never split these streams:
// cursor addressing and display clears only make sense when delivered
// immediately and in order.
func hasFullscreenANSI(p []byte) bool {
	for i := 0; i+2 < len(p); i++ {
		if p[i] != 0x1b || p[i+1] != '[' {
			continue
		}
		for j := i + 2; j < len(p) && j-i < 32; j++ {
			b := p[j]
			if b < 0x40 || b > 0x7e {
				continue
			}
			params := string(p[i+2 : j])
			switch b {
			case 'H', 'f', 'J':
				return true
			case 'h', 'l':
				if strings.Contains(params, "?1049") ||
					strings.Contains(params, "?1047") ||
					strings.Contains(params, "?47") ||
					strings.Contains(params, "?25") {
					return true
				}
			}
			break
		}
	}
	return false
}

func (t *uartTerm) writeRawInput(p []byte) error {
	var blocked bool
	p, blocked = filterASCIICommandInput(p)
	if blocked {
		t.warnNonASCII()
	}
	for len(p) > 0 {
		if t.pagerIsWaiting() {
			i := -1
			for n, b := range p {
				if b == '\r' || b == '\n' || b == 0x11 || b == 0x10 {
					i = n
					break
				}
			}
			if i < 0 {
				return nil
			}
			ctrl := p[i]
			p = p[i+1:]
			switch ctrl {
			case '\r', '\n':
				t.pagerContinue()
			case 0x10:
				t.togglePager()
			case 0x11:
				t.quit = true
				fmt.Print(L("\r\n[выход из UART-терминала: Ctrl+Q]\r\n", "\r\n[UART terminal exit: Ctrl+Q]\r\n"))
				return nil
			}
			continue
		}
		i := -1
		for n, b := range p {
			if b == 0x1d || b == 0x11 || b == 0x10 {
				i = n
				break
			}
		}
		if i < 0 {
			return t.s.Write(p)
		}
		if i > 0 {
			if err := t.s.Write(p[:i]); err != nil {
				return err
			}
		}
		ctrl := p[i]
		p = p[i+1:]
		if ctrl == 0x11 {
			t.quit = true
			fmt.Print(L("\r\n[выход из UART-терминала: Ctrl+Q]\r\n", "\r\n[UART terminal exit: Ctrl+Q]\r\n"))
			return nil
		}
		if ctrl == 0x10 {
			t.togglePager()
			continue
		}
		if t.simple {
			t.quit = true
			fmt.Print(L("\r\n[выход из прозрачной UART-консоли]\r\n", "\r\n[transparent UART console exit]\r\n"))
			return nil
		}
		var choice byte
		if len(p) > 0 {
			choice = p[0]
			p = p[1:]
		}
		t.menuChoice(choice)
		if t.quit {
			return nil
		}
	}
	return nil
}

// readLoop prints device output verbatim. In line mode it first erases the
// pending input line (with spaces, no escape codes) and redraws it after, so
// what the operator is typing survives asynchronous output while the device
// output itself stays clean and copyable.
func (t *uartTerm) readLoop(done chan<- error) {
	buf := make([]byte, 4096)
	for {
		n, err := t.s.Read(buf, 20*time.Millisecond)
		if err != nil {
			done <- err
			return
		}
		if n == 0 {
			continue
		}
		d := append([]byte(nil), buf[:n]...)
		t.a.logBytes(d, false)
		t.deviceOutput(d)
	}
}

func (t *uartTerm) warnNonASCII() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.lastASCIINotice) < time.Second {
		return
	}
	t.lastASCIINotice = time.Now()
	fmt.Print(L("\r\n[ввод отклонён: UART-команды только ASCII/латиница — переключите раскладку]\r\n",
		"\r\n[input blocked: UART commands are ASCII/Latin only — switch keyboard layout]\r\n"))
}

func (t *uartTerm) pagerIsWaiting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pagerWaiting
}

func (t *uartTerm) pageSizeLocked() int {
	if r := t.chromeRowsLocked(); r > 2 {
		return r - 1
	}
	r := consoleRows()
	if r < 8 {
		return 20
	}
	return r - 3
}

func (t *uartTerm) togglePager() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pagerEnabled = !t.pagerEnabled
	t.pagerWaiting = false
	t.pagerLines = 0
	t.chromeRefreshLocked()
	if !t.pagerEnabled {
		if len(t.pagerPending) > 0 {
			os.Stdout.Write(t.pagerPending)
			t.pagerPending = nil
		}
		fmt.Print(L("\r\n[pager: выкл]\r\n", "\r\n[pager: off]\r\n"))
		return
	}
	fmt.Printf(L("\r\n[pager: вкл, %d строк на страницу]\r\n", "\r\n[pager: on, %d lines per page]\r\n"), t.pageSizeLocked())
}

func (t *uartTerm) pagerContinue() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.pagerEnabled {
		return
	}
	t.pagerWaiting = false
	t.pagerLines = 0
	fmt.Print("\r\n")
	t.flushPagerLocked()
}

func (t *uartTerm) flushPagerLocked() {
	if !t.pagerEnabled || t.pagerWaiting || len(t.pagerPending) == 0 {
		return
	}
	remaining := t.pageSizeLocked() - t.pagerLines
	head, tail, lines, full := splitOutputPage(t.pagerPending, remaining)
	if len(head) > 0 {
		os.Stdout.Write(head)
	}
	t.pagerPending = append(t.pagerPending[:0], tail...)
	t.pagerLines += lines
	if full {
		t.pagerWaiting = true
		fmt.Print(L("\r\n-- ещё -- Enter: продолжить, Ctrl+P: без пауз, Ctrl+Q: выход --",
			"\r\n-- More -- Enter: continue, Ctrl+P: disable pager, Ctrl+Q: exit --"))
	}
}

func (t *uartTerm) deviceOutput(d []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.chrome
	if c == nil {
		t.outputLocked(d)
		return
	}
	t.chromeSizeCheckLocked()
	// Device bytes and chrome actions in stream order: a program's exit
	// sequence is followed by the resume, then by whatever it printed next.
	for _, pc := range c.parser.feed(d, c.on && !c.suspended) {
		if len(pc.data) > 0 {
			t.outputLocked(pc.data)
		}
		t.chromeApplyLocked(pc.act)
	}
}

func (t *uartTerm) outputLocked(d []byte) {
	// Probe across read boundaries: a CSI sequence is only a few bytes, but a
	// serial read may split it anywhere.
	probe := make([]byte, 0, len(t.pagerANSIProbe)+len(d))
	probe = append(probe, t.pagerANSIProbe...)
	probe = append(probe, d...)
	if len(probe) > 64 {
		t.pagerANSIProbe = append(t.pagerANSIProbe[:0], probe[len(probe)-64:]...)
	} else {
		t.pagerANSIProbe = append(t.pagerANSIProbe[:0], probe...)
	}

	if t.pagerEnabled && hasFullscreenANSI(probe) {
		// Do not page screen-oriented programs. Flush anything held by the pager,
		// then permanently turn it off for this session until Ctrl+P enables it
		// again. The TUI's own clear/home sequence immediately follows, so this
		// notice does not corrupt its screen model.
		t.pagerEnabled = false
		t.pagerWaiting = false
		t.pagerLines = 0
		if len(t.pagerPending) > 0 {
			os.Stdout.Write(t.pagerPending)
			t.pagerPending = nil
		}
		fmt.Print(L("\r\n[pager: автоотключение для полноэкранного ANSI/TUI]\r\n",
			"\r\n[pager: auto-disabled for fullscreen ANSI/TUI]\r\n"))
	}

	if !t.pagerEnabled {
		if !t.raw && t.lineShown {
			t.eraseLineLocked()
		}
		os.Stdout.Write(d)
		if !t.raw {
			t.drawLineLocked(0)
		}
		return
	}
	t.pagerPending = append(t.pagerPending, d...)
	if len(t.pagerPending) > 4<<20 {
		fmt.Print(L("\r\n[pager: буфер >4 MiB, пауза отключена]\r\n", "\r\n[pager: buffer >4 MiB, disabling pause]\r\n"))
		t.pagerEnabled = false
		t.pagerWaiting = false
		os.Stdout.Write(t.pagerPending)
		t.pagerPending = nil
		return
	}
	t.flushPagerLocked()
}

// eraseLineLocked clears the current input line using only CR and spaces.
func (t *uartTerm) eraseLineLocked() {
	if t.shownW > 0 {
		fmt.Print("\r" + strings.Repeat(" ", t.shownW) + "\r")
	}
	t.shownW = 0
	t.lineShown = false
}

// drawLineLocked (re)draws the prompt and buffer; prevW is the width already on
// screen (0 when the line was just erased).
func (t *uartTerm) drawLineLocked(prevW int) {
	if t.raw {
		return
	}
	seq, w := t.ed.render(t.prompt, prevW)
	fmt.Print(seq)
	t.shownW = w
	t.lineShown = true
}

func (t *uartTerm) redraw() {
	t.mu.Lock()
	t.drawLineLocked(t.shownW)
	t.mu.Unlock()
}

func (t *uartTerm) onKey(ev keyEvent) {
	if ev.kind == kMenu {
		t.menu()
		return
	}
	switch ev.kind {
	case kCtrlC:
		t.logSent("<Ctrl-C>")
		_ = t.s.Write([]byte{0x03})
		return
	case kCtrlZ:
		t.logSent("<Ctrl-Z>")
		_ = t.s.Write([]byte{0x1a})
		return
	case kInterrupt:
		return
	}
	line, send := t.ed.handle(ev)
	t.mu.Lock()
	prev := t.shownW
	if send {
		t.eraseLineLocked()
		fmt.Print(t.prompt + line + "\r\n")
	} else {
		t.drawLineLocked(prev)
	}
	t.mu.Unlock()
	if send {
		t.logSent(line)
		_ = t.s.Write(append([]byte(line), '\r'))
	}
}

// logSent records an operator-sent line in the UART log.
func (t *uartTerm) logSent(line string) {
	t.a.logMu.Lock()
	if t.a.logFile != nil {
		fmt.Fprintf(t.a.logFile, "\n[URSIDO SENT %s] %s\n", time.Now().Format(time.RFC3339), line)
		_ = t.a.logFile.Sync()
	}
	t.a.logMu.Unlock()
}

// menu leaves raw single-key handling for a short prompt.
func (t *uartTerm) menu() { t.menuChoice(0) }

func (t *uartTerm) menuChoice(prefetched byte) {
	t.mu.Lock()
	if !t.raw && t.lineShown {
		t.eraseLineLocked()
	}
	t.chromeResumeLocked()
	t.mu.Unlock()
	mode := L("сейчас: прозрачный", "now: raw")
	if !t.raw {
		mode = L("сейчас: построчный", "now: line")
	}
	fmt.Print(L("\r\n[меню ("+mode+"): l=прозрачный/построчный, s=XMODEM отпр, r=XMODEM приём, g=лог, q=выход, Enter=назад] ",
		"\r\n[menu ("+mode+"): l=raw/line, s=XMODEM send, r=XMODEM recv, g=log, q=quit, Enter=back] "))
	c := prefetched
	if c == 0 {
		c = t.readByte()
	}
	fmt.Print("\r\n")
	switch c {
	case 's', 'S':
		t.xmodemSendInteractive()
	case 'r', 'R':
		t.xmodemRecvInteractive()
	case 'l', 'L':
		t.raw = !t.raw
		t.mu.Lock()
		t.chromeRefreshLocked()
		t.mu.Unlock()
		if t.raw {
			fmt.Print(L("[прозрачный режим: вывод как есть, копируется; история — стрелками устройства]\r\n",
				"[raw mode: verbatim output, copyable; history via the device's own arrows]\r\n"))
		} else {
			fmt.Print(L("[построчный режим: ↑/↓ — история, строка уходит по Enter]\r\n",
				"[line mode: ↑/↓ history, Enter sends the line]\r\n"))
		}
	case 'g', 'G':
		t.a.logMu.Lock()
		f := t.a.logFile
		t.a.logMu.Unlock()
		if f != nil {
			fmt.Printf(L("[лог: %s]\r\n", "[log: %s]\r\n"), f.Name())
		}
	case 'q', 'Q':
		t.quit = true
		fmt.Print(L("[выход из терминала]\r\n", "[terminal exit]\r\n"))
		return
	}
	t.redraw()
}

// readByte reads one meaningful key (skips lone control noise) in raw mode.
func (t *uartTerm) readByte() byte {
	ib := make([]byte, 1)
	for {
		n, err := consoleReadInput(ib)
		if err != nil {
			return 0
		}
		if n == 1 {
			return ib[0]
		}
	}
}

// readRawLine reads a line for a menu prompt while the console is raw.
func (t *uartTerm) readRawLine(prompt string) string {
	fmt.Print(prompt)
	var b []rune
	ib := make([]byte, 1)
	for {
		n, err := consoleReadInput(ib)
		if err != nil || n == 0 {
			return ""
		}
		switch ib[0] {
		case '\r', '\n':
			fmt.Print("\r\n")
			return strings.TrimSpace(string(b))
		case 0x7f, 0x08:
			if len(b) > 0 {
				b = b[:len(b)-1]
				fmt.Print("\b \b")
			}
		case 0x03, 0x1d:
			fmt.Print("\r\n")
			return ""
		default:
			if ib[0] >= 0x20 {
				b = append(b, rune(ib[0]))
				fmt.Print(string(ib[0]))
			}
		}
	}
}

func (t *uartTerm) xmodemSendInteractive() {
	p := t.readRawLine(L("Файл для XMODEM-отправки: ", "File to XMODEM-send: "))
	if p == "" {
		return
	}
	abs, e := filepath.Abs(strings.Trim(p, "\""))
	if e != nil || !fileExists(abs) {
		fmt.Printf(L("файл не найден: %s\r\n", "file not found: %s\r\n"), abs)
		return
	}
	fmt.Print(L("Запустите на устройстве приём (например loadx/loady) и нажмите Enter...\r\n",
		"Start the receiver on the device (e.g. loadx/loady), then press Enter...\r\n"))
	t.readByte()
	t.logSent("<XMODEM send " + filepath.Base(abs) + ">")
	// xmodemSend prints its own progress and logs the raw bytes via a.logBytes.
	res, e := t.a.xmodemSend(t.s, abs, "manual")
	if e != nil {
		fmt.Printf(L("\r\nXMODEM отправка не удалась: %v\r\n", "\r\nXMODEM send failed: %v\r\n"), e)
		return
	}
	if len(res.Trailing) > 0 {
		_, _ = os.Stdout.Write(res.Trailing)
	}
	if res.EOTAck {
		fmt.Print(L("\r\nXMODEM отправка завершена и EOT подтверждён.\r\n", "\r\nXMODEM send complete; EOT acknowledged.\r\n"))
	} else {
		fmt.Print(L("\r\nВсе блоки XMODEM подтверждены; EOT ACK не получен. Проверьте состояние устройства в терминале.\r\n",
			"\r\nAll XMODEM data blocks were ACKed; EOT ACK was not received. Verify the device state in the terminal.\r\n"))
	}
}

func (t *uartTerm) xmodemRecvInteractive() {
	p := t.readRawLine(L("Файл для сохранения (XMODEM-приём): ", "File to save (XMODEM-receive): "))
	if p == "" {
		return
	}
	abs, e := filepath.Abs(strings.Trim(p, "\""))
	if e != nil {
		return
	}
	if fileExists(abs) {
		if strings.ToLower(t.readRawLine(fmt.Sprintf(L("%s существует. Перезаписать? [y/N]: ", "%s exists. Overwrite? [y/N]: "), abs))) != "y" {
			return
		}
	}
	fmt.Print(L("Запустите на устройстве отправку и нажмите Enter...\r\n",
		"Start the sender on the device, then press Enter...\r\n"))
	t.readByte()
	t.logSent("<XMODEM receive " + filepath.Base(abs) + ">")
	data, err := xmodemReceive(t.s, func(n int) {
		fmt.Printf(L("\r[XMODEM] принято блоков: %d", "\r[XMODEM] blocks received: %d"), n)
	}, nil)
	if err != nil {
		fmt.Printf(L("\r\nXMODEM приём не удался: %v\r\n", "\r\nXMODEM receive failed: %v\r\n"), err)
		return
	}
	if e := os.WriteFile(abs, data, 0o644); e != nil {
		fmt.Printf(L("\r\nне удалось записать файл: %v\r\n", "\r\ncould not write the file: %v\r\n"), e)
		return
	}
	sha, _ := shaFile(abs)
	fmt.Printf(L("\r\nXMODEM приём завершён: %d байт, SHA256=%s\r\n", "\r\nXMODEM receive complete: %d bytes, SHA256=%s\r\n"), len(data), sha)
}
