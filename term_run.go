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
	prompt    string
	quit      bool
	shownW    int  // rune width of the input line currently on screen (line mode)
	lineShown bool // an input line is drawn and needs erasing before device output
}

// runTerminal opens the port and runs the interactive terminal.
func (a *App) runTerminal() error {
	port, e := a.choosePort()
	if e != nil {
		return e
	}
	s, e := openSerial(port)
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
	// Raw passthrough is the default: device output is printed verbatim, so it
	// is clean and copyable, and the device's own line editing/history works.
	t := &uartTerm{a: a, s: s, prompt: "] ", raw: true}
	t.ed.hidx = 0
	fmt.Println(L("\nUART-терминал 115200 8N1 — прозрачный режим (вывод как есть, копируется).",
		"\nUART terminal 115200 8N1 — raw passthrough (verbatim output, copyable)."))
	fmt.Println(L("Ctrl+] — меню: l — построчный ввод с историей ↑/↓, s/r — XMODEM отправка/приём, g — лог, q — выход.",
		"Ctrl+] — menu: l line-input with ↑/↓ history, s/r XMODEM send/receive, g log, q quit."))
	fmt.Println(L("Всё пишется в лог.", "Everything is logged."))
	state, e := consoleRaw()
	if e != nil {
		return fmt.Errorf(L("raw-консоль: %w", "raw console: %w"), e)
	}
	defer consoleRestore(state)

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
		n, err := os.Stdin.Read(ib)
		if err != nil {
			return err
		}
		for _, b := range ib[:n] {
			if t.raw {
				// Raw passthrough: forward every byte verbatim (clean paste),
				// except Ctrl+] which opens the menu.
				if b == 0x1d {
					t.menu()
				} else {
					_ = t.s.Write([]byte{b})
				}
				if t.quit {
					return nil
				}
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

// readLoop prints device output verbatim. In line mode it first erases the
// pending input line (with spaces, no escape codes) and redraws it after, so
// what the operator is typing survives asynchronous output while the device
// output itself stays clean and copyable.
func (t *uartTerm) readLoop(done chan<- error) {
	buf := make([]byte, 4096)
	for {
		n, err := t.s.Read(buf, 200*time.Millisecond)
		if err != nil {
			done <- err
			return
		}
		if n == 0 {
			continue
		}
		d := append([]byte(nil), buf[:n]...)
		t.mu.Lock()
		if !t.raw && t.lineShown {
			t.eraseLineLocked()
		}
		os.Stdout.Write(d)
		t.a.logBytes(d, false)
		if !t.raw {
			t.drawLineLocked(0)
		}
		t.mu.Unlock()
	}
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
func (t *uartTerm) menu() {
	t.mu.Lock()
	if !t.raw && t.lineShown {
		t.eraseLineLocked()
	}
	t.mu.Unlock()
	mode := L("сейчас: прозрачный", "now: raw")
	if !t.raw {
		mode = L("сейчас: построчный", "now: line")
	}
	fmt.Print(L("\r\n[меню ("+mode+"): l=прозрачный/построчный, s=XMODEM отпр, r=XMODEM приём, g=лог, q=выход, Enter=назад] ",
		"\r\n[menu ("+mode+"): l=raw/line, s=XMODEM send, r=XMODEM recv, g=log, q=quit, Enter=back] "))
	c := t.readByte()
	fmt.Print("\r\n")
	switch c {
	case 's', 'S':
		t.xmodemSendInteractive()
	case 'r', 'R':
		t.xmodemRecvInteractive()
	case 'l', 'L':
		t.raw = !t.raw
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
		n, err := os.Stdin.Read(ib)
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
		n, err := os.Stdin.Read(ib)
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
	if e := t.a.xmodemSend(t.s, abs, "manual"); e != nil {
		fmt.Printf(L("\r\nXMODEM отправка не удалась: %v\r\n", "\r\nXMODEM send failed: %v\r\n"), e)
		return
	}
	fmt.Print(L("\r\nXMODEM отправка завершена.\r\n", "\r\nXMODEM send complete.\r\n"))
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
