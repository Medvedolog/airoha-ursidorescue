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
	a      *App
	s      Serial
	mu     sync.Mutex // guards stdout and the shared editor display
	ed     lineEditor
	dec    ansiDecoder
	raw    bool
	prompt string
	quit   bool
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
	t := &uartTerm{a: a, s: s, prompt: "] "}
	t.ed.hidx = 0
	fmt.Println(L("\nUART-терминал 115200 8N1. Строка отправляется по Enter; ↑/↓ — история; Ctrl+] — меню.",
		"\nUART terminal 115200 8N1. Enter sends the line; ↑/↓ history; Ctrl+] menu."))
	fmt.Println(L("Всё логируется. В меню: s — XMODEM отправить, r — принять, t — сырой режим, q — выход.",
		"Everything is logged. Menu: s XMODEM send, r receive, t raw mode, q quit."))
	state, e := consoleRaw()
	if e != nil {
		return fmt.Errorf(L("raw-консоль: %w", "raw console: %w"), e)
	}
	defer consoleRestore(state)

	rxErr := make(chan error, 1)
	go t.readLoop(rxErr)
	t.redraw()

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

// readLoop prints device output, erasing and redrawing the input line so the
// line the operator is typing stays intact under asynchronous output.
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
		if !t.raw {
			fmt.Print("\r\x1b[K") // clear the input line before device output
		}
		os.Stdout.Write(d)
		t.a.logBytes(d, false)
		if !t.raw {
			t.renderLocked()
		}
		t.mu.Unlock()
	}
}

func (t *uartTerm) redraw() {
	t.mu.Lock()
	t.renderLocked()
	t.mu.Unlock()
}

func (t *uartTerm) renderLocked() {
	if t.raw {
		return
	}
	fmt.Print(t.ed.render(t.prompt))
}

func (t *uartTerm) onKey(ev keyEvent) {
	if ev.kind == kMenu {
		t.menu()
		return
	}
	if t.raw {
		// Raw mode: forward the byte(s) verbatim, except the menu key above.
		t.forwardRaw(ev)
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
	t.renderLocked()
	t.mu.Unlock()
	if send {
		fmt.Print("\r\n")
		t.logSent(line)
		_ = t.s.Write(append([]byte(line), '\r'))
	}
}

func (t *uartTerm) forwardRaw(ev keyEvent) {
	var b []byte
	switch ev.kind {
	case kRune:
		b = []byte(string(ev.r))
	case kEnter:
		b = []byte{'\r'}
	case kBackspace:
		b = []byte{0x7f}
	case kCtrlC:
		b = []byte{0x03}
	case kInterrupt:
		b = []byte{0x04}
	case kUp:
		b = []byte("\x1b[A")
	case kDown:
		b = []byte("\x1b[B")
	case kLeft:
		b = []byte("\x1b[D")
	case kRight:
		b = []byte("\x1b[C")
	}
	if len(b) > 0 {
		_ = t.s.Write(b)
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
	fmt.Print(L("\r\n[меню: s=XMODEM отпр, r=XMODEM приём, t=сырой/строчный, l=лог, q=выход, Enter=назад] ",
		"\r\n[menu: s=XMODEM send, r=XMODEM recv, t=raw/line, l=log, q=quit, Enter=back] "))
	c := t.readByte()
	fmt.Print("\r\n")
	switch c {
	case 's', 'S':
		t.xmodemSendInteractive()
	case 'r', 'R':
		t.xmodemRecvInteractive()
	case 't', 'T':
		t.raw = !t.raw
		if t.raw {
			fmt.Print(L("[сырой режим: ввод идёт как есть, истории нет]\r\n", "[raw mode: input passes through, no history]\r\n"))
		} else {
			fmt.Print(L("[строчный режим]\r\n", "[line mode]\r\n"))
		}
	case 'l', 'L':
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
