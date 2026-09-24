package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file is the interactive UART terminal: local line editing with command
// history, manual XMODEM send/receive, a raw passthrough mode, and full session
// logging. It only talks to the UART the operator opened; it has no notion of
// device credentials and never guesses any.

// keyKind is one decoded key press.
type keyKind int

const (
	kRune keyKind = iota
	kEnter
	kBackspace
	kDelete
	kLeft
	kRight
	kHome
	kEnd
	kUp
	kDown
	kCtrlC     // 0x03 — forwarded to the device
	kMenu      // 0x1d (Ctrl+]) — opens the terminal menu
	kInterrupt // 0x04 (Ctrl+D) — reserved, ignored in line mode
)

type keyEvent struct {
	kind keyKind
	r    rune
}

// ansiDecoder turns a byte stream into key events, understanding the CSI
// escape sequences that arrow/Home/End/Delete keys produce.
type ansiDecoder struct {
	esc []byte // buffered escape sequence, empty when not in one
}

// push feeds one byte and returns any completed key events.
func (d *ansiDecoder) push(b byte) []keyEvent {
	if len(d.esc) > 0 {
		d.esc = append(d.esc, b)
		return d.decodeEsc()
	}
	switch b {
	case 0x1b:
		d.esc = []byte{0x1b}
		return nil
	case '\r', '\n':
		return []keyEvent{{kind: kEnter}}
	case 0x7f, 0x08:
		return []keyEvent{{kind: kBackspace}}
	case 0x03:
		return []keyEvent{{kind: kCtrlC}}
	case 0x1d:
		return []keyEvent{{kind: kMenu}}
	case 0x01:
		return []keyEvent{{kind: kHome}}
	case 0x05:
		return []keyEvent{{kind: kEnd}}
	case 0x04:
		return []keyEvent{{kind: kInterrupt}}
	}
	if b < 0x20 {
		return nil // other control bytes are dropped in line mode
	}
	return []keyEvent{{kind: kRune, r: rune(b)}}
}

// decodeEsc runs while an escape sequence is being collected.
func (d *ansiDecoder) decodeEsc() []keyEvent {
	s := d.esc
	// A lone ESC followed by a non-'[' byte: treat ESC as nothing, reprocess b.
	if len(s) == 2 && s[1] != '[' && s[1] != 'O' {
		d.esc = nil
		return d.push(s[1])
	}
	if len(s) < 3 {
		return nil
	}
	final := s[len(s)-1]
	// Wait for the final byte of a CSI sequence (letter or '~').
	if !((final >= 'A' && final <= 'Z') || final == '~') {
		if len(s) > 8 {
			d.esc = nil // give up on a runaway sequence
		}
		return nil
	}
	seq := string(s)
	d.esc = nil
	switch seq {
	case "\x1b[A", "\x1bOA":
		return []keyEvent{{kind: kUp}}
	case "\x1b[B", "\x1bOB":
		return []keyEvent{{kind: kDown}}
	case "\x1b[C", "\x1bOC":
		return []keyEvent{{kind: kRight}}
	case "\x1b[D", "\x1bOD":
		return []keyEvent{{kind: kLeft}}
	case "\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~":
		return []keyEvent{{kind: kHome}}
	case "\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~":
		return []keyEvent{{kind: kEnd}}
	case "\x1b[3~":
		return []keyEvent{{kind: kDelete}}
	}
	return nil // unknown sequence: ignore
}

// lineEditor is the editable input line with command history. It is pure so it
// can be tested without a terminal.
type lineEditor struct {
	buf   []rune
	pos   int
	hist  []string
	hidx  int    // index into hist; == len(hist) means the fresh line
	stash string // fresh line saved while browsing history
}

// handle applies one key event. When the line is submitted it returns the
// line and send=true; the caller sends it and it becomes visible in history.
func (e *lineEditor) handle(ev keyEvent) (line string, send bool) {
	switch ev.kind {
	case kRune:
		e.buf = append(e.buf, 0)
		copy(e.buf[e.pos+1:], e.buf[e.pos:])
		e.buf[e.pos] = ev.r
		e.pos++
	case kBackspace:
		if e.pos > 0 {
			e.buf = append(e.buf[:e.pos-1], e.buf[e.pos:]...)
			e.pos--
		}
	case kDelete:
		if e.pos < len(e.buf) {
			e.buf = append(e.buf[:e.pos], e.buf[e.pos+1:]...)
		}
	case kLeft:
		if e.pos > 0 {
			e.pos--
		}
	case kRight:
		if e.pos < len(e.buf) {
			e.pos++
		}
	case kHome:
		e.pos = 0
	case kEnd:
		e.pos = len(e.buf)
	case kUp:
		e.historyPrev()
	case kDown:
		e.historyNext()
	case kEnter:
		line = string(e.buf)
		e.commit(line)
		return line, true
	}
	return "", false
}

func (e *lineEditor) commit(line string) {
	if t := strings.TrimSpace(line); t != "" && (len(e.hist) == 0 || e.hist[len(e.hist)-1] != line) {
		e.hist = append(e.hist, line)
	}
	e.buf = e.buf[:0]
	e.pos = 0
	e.hidx = len(e.hist)
	e.stash = ""
}

func (e *lineEditor) historyPrev() {
	if e.hidx == 0 || len(e.hist) == 0 {
		return
	}
	if e.hidx == len(e.hist) {
		e.stash = string(e.buf)
	}
	e.hidx--
	e.set(e.hist[e.hidx])
}

func (e *lineEditor) historyNext() {
	if e.hidx >= len(e.hist) {
		return
	}
	e.hidx++
	if e.hidx == len(e.hist) {
		e.set(e.stash)
	} else {
		e.set(e.hist[e.hidx])
	}
}

func (e *lineEditor) set(s string) {
	e.buf = []rune(s)
	e.pos = len(e.buf)
}

// render draws the prompt and the line, leaving the cursor at e.pos.
func (e *lineEditor) render(prompt string) string {
	var b strings.Builder
	b.WriteString("\r\x1b[K") // return to start, clear to end of line
	b.WriteString(prompt)
	b.WriteString(string(e.buf))
	if back := len(e.buf) - e.pos; back > 0 {
		fmt.Fprintf(&b, "\x1b[%dD", back)
	}
	return b.String()
}

// ---------------- XMODEM receive ----------------

const (
	xSOH = 0x01
	xSTX = 0x02
	xEOT = 0x04
	xACK = 0x06
	xNAK = 0x15
	xCAN = 0x18
	xSUB = 0x1a
)

// xmodemReceive receives a file over XMODEM-CRC (128- and 1K-byte blocks) into
// a byte slice. It requests CRC mode with 'C'. onProgress may be nil.
func xmodemReceive(s Serial, onProgress func(blocks int), read func([]byte, time.Duration) (int, error)) ([]byte, error) {
	if read == nil {
		read = s.Read
	}
	var out []byte
	expect := byte(1)
	started := false
	deadline := time.Now().Add(90 * time.Second)
	// Kick the sender into CRC mode.
	_ = s.Write([]byte{'C'})
	lastC := time.Now()
	hdr := make([]byte, 1)
	blocks := 0
	for time.Now().Before(deadline) {
		n, err := read(hdr, 1500*time.Millisecond)
		if err != nil {
			return out, err
		}
		if n == 0 {
			if !started && time.Since(lastC) >= 3*time.Second {
				_ = s.Write([]byte{'C'})
				lastC = time.Now()
			}
			continue
		}
		switch hdr[0] {
		case xEOT:
			_ = s.Write([]byte{xACK})
			// Trim the trailing SUB padding of the last block.
			for len(out) > 0 && out[len(out)-1] == xSUB {
				out = out[:len(out)-1]
			}
			return out, nil
		case xCAN:
			return out, errors.New("sender cancelled XMODEM")
		case xSOH, xSTX:
			started = true
			size := 128
			if hdr[0] == xSTX {
				size = 1024
			}
			blk := make([]byte, size+4) // seq, ~seq, data..., crc16
			if !readFull(read, blk, 5*time.Second) {
				_ = s.Write([]byte{xNAK})
				continue
			}
			seq, nseq := blk[0], blk[1]
			data := blk[2 : 2+size]
			crc := uint16(blk[2+size])<<8 | uint16(blk[2+size+1])
			if seq != 255-nseq || crc16Xmodem(data) != crc {
				_ = s.Write([]byte{xNAK})
				continue
			}
			if seq == expect {
				out = append(out, data...)
				expect++
				blocks++
				if onProgress != nil {
					onProgress(blocks)
				}
			} // a repeated previous block is ACKed but not stored
			_ = s.Write([]byte{xACK})
		default:
			// noise before the first block; keep waiting
		}
	}
	_ = s.Write([]byte{xCAN, xCAN, xCAN})
	return out, errors.New("XMODEM receive timeout")
}

func readFull(read func([]byte, time.Duration) (int, error), buf []byte, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	got := 0
	for got < len(buf) && time.Now().Before(end) {
		n, err := read(buf[got:], 400*time.Millisecond)
		if err != nil {
			return false
		}
		got += n
	}
	return got == len(buf)
}
