package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// feed decodes a whole byte string into key events.
func feed(dec *ansiDecoder, s string) []keyEvent {
	var evs []keyEvent
	for _, b := range []byte(s) {
		evs = append(evs, dec.push(b)...)
	}
	return evs
}

func TestAnsiDecoder(t *testing.T) {
	var d ansiDecoder
	got := feed(&d, "ab\x1b[A\x1b[D\x7f\r\x03\x1d\x1b[3~\x1b[H\x1b[F")
	want := []keyKind{kRune, kRune, kUp, kLeft, kBackspace, kEnter, kCtrlC, kMenu, kDelete, kHome, kEnd}
	if len(got) != len(want) {
		t.Fatalf("got %d events %+v", len(got), got)
	}
	for i, k := range want {
		if got[i].kind != k {
			t.Errorf("event %d: kind %d want %d", i, got[i].kind, k)
		}
	}
	if got[0].r != 'a' || got[1].r != 'b' {
		t.Errorf("runes %q %q", got[0].r, got[1].r)
	}
	// A lone ESC before a printable byte must not swallow it.
	d = ansiDecoder{}
	ev := feed(&d, "\x1bx")
	if len(ev) != 1 || ev[0].kind != kRune || ev[0].r != 'x' {
		t.Fatalf("lone ESC: %+v", ev)
	}
}

// typeLine drives an editor with decoded bytes and returns any submitted line.
func typeLine(e *lineEditor, s string) (lines []string) {
	var d ansiDecoder
	for _, ev := range feed(&d, s) {
		if line, send := e.handle(ev); send {
			lines = append(lines, line)
		}
	}
	return
}

func TestLineEditorEditing(t *testing.T) {
	var e lineEditor
	// "helo" then Left, insert "l" -> "hello"
	typeLine(&e, "helo\x1b[Dl")
	if string(e.buf) != "hello" {
		t.Fatalf("buf %q", string(e.buf))
	}
	// Home, delete first char, End, backspace
	typeLine(&e, "\x1b[H\x1b[3~\x1b[F\x7f")
	if string(e.buf) != "ell" {
		t.Fatalf("buf %q", string(e.buf))
	}
}

func TestLineEditorHistory(t *testing.T) {
	var e lineEditor
	lines := typeLine(&e, "version\rmtd list\r")
	if len(lines) != 2 || lines[1] != "mtd list" {
		t.Fatalf("lines %v", lines)
	}
	// Up, Up -> "version"; Down -> "mtd list"
	typeLine(&e, "\x1b[A\x1b[A")
	if string(e.buf) != "version" {
		t.Fatalf("up: %q", string(e.buf))
	}
	typeLine(&e, "\x1b[B")
	if string(e.buf) != "mtd list" {
		t.Fatalf("down: %q", string(e.buf))
	}
	// Down again returns to the stashed fresh line (empty here).
	typeLine(&e, "\x1b[B")
	if string(e.buf) != "" {
		t.Fatalf("stash: %q", string(e.buf))
	}
	// A blank line and an immediate duplicate are not added to history.
	typeLine(&e, "\r")
	typeLine(&e, "version\rversion\r")
	if n := len(e.hist); n != 3 {
		t.Fatalf("history has %d entries: %v", n, e.hist)
	}
}

func TestLineEditorStashPreservesTyping(t *testing.T) {
	var e lineEditor
	typeLine(&e, "printenv\r")
	// Start typing, then browse history, then come back down.
	typeLine(&e, "abc\x1b[A")
	if string(e.buf) != "printenv" {
		t.Fatalf("up: %q", string(e.buf))
	}
	typeLine(&e, "\x1b[B")
	if string(e.buf) != "abc" {
		t.Fatalf("stash restore: %q", string(e.buf))
	}
}

func TestRenderPortableAndCursor(t *testing.T) {
	var e lineEditor
	typeLine(&e, "abcd\x1b[D\x1b[D") // cursor two left of end
	out, w := e.render("] ", 0)
	if w != 6 {
		t.Fatalf("width %d", w)
	}
	// Portable only: no ESC anywhere.
	if strings.Contains(out, "\x1b") {
		t.Fatalf("render leaked an escape sequence: %q", out)
	}
	if !strings.HasPrefix(out, "\r] abcd") {
		t.Fatalf("render %q", out)
	}
	if !strings.HasSuffix(out, "\b\b") { // cursor moved two left of end
		t.Fatalf("cursor move %q", out)
	}
	// Redraw of a shorter line pads and backs over the old tail.
	e2 := lineEditor{}
	typeLine(&e2, "ab")
	out2, _ := e2.render("] ", 10)
	if !strings.Contains(out2, "    ") { // spaces to erase the longer previous line
		t.Fatalf("no pad in %q", out2)
	}
	if strings.Contains(out2, "\x1b") {
		t.Fatalf("pad leaked escape: %q", out2)
	}
}

type captureSerial struct {
	writes [][]byte
}

func (s *captureSerial) Name() string      { return "capture" }
func (s *captureSerial) Close() error      { return nil }
func (s *captureSerial) ResetInput() error { return nil }
func (s *captureSerial) Read([]byte, time.Duration) (int, error) { return 0, nil }
func (s *captureSerial) Write(p []byte) error {
	s.writes = append(s.writes, append([]byte(nil), p...))
	return nil
}

func TestRawInputBatchesEscapeSequence(t *testing.T) {
	s := &captureSerial{}
	tr := &uartTerm{s: s, raw: true}
	if err := tr.writeRawInput([]byte("\x1b[B")); err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 1 || !bytes.Equal(s.writes[0], []byte("\x1b[B")) {
		t.Fatalf("writes = %q; want one atomic down-arrow sequence", s.writes)
	}
}

func TestRawInputCtrlQStopsLocally(t *testing.T) {
	s := &captureSerial{}
	tr := &uartTerm{s: s, raw: true}
	if err := tr.writeRawInput([]byte("abc\x11ignored")); err != nil {
		t.Fatal(err)
	}
	if !tr.quit {
		t.Fatal("Ctrl+Q did not stop the terminal")
	}
	if len(s.writes) != 1 || string(s.writes[0]) != "abc" {
		t.Fatalf("writes = %q; Ctrl+Q or trailing bytes reached UART", s.writes)
	}
}

// xSender is a fake XMODEM sender driving xmodemReceive over an in-memory link.
type xSender struct {
	toRx   chan byte // bytes the receiver reads
	fromRx chan byte // bytes the receiver writes (ACK/NAK/C)
}

func (x *xSender) Name() string      { return "xmodem" }
func (x *xSender) Close() error      { return nil }
func (x *xSender) ResetInput() error { return nil }
func (x *xSender) Write(p []byte) error {
	for _, b := range p {
		x.fromRx <- b
	}
	return nil
}
func (x *xSender) Read(p []byte, timeout time.Duration) (int, error) {
	select {
	case b := <-x.toRx:
		p[0] = b
		return 1, nil
	case <-time.After(timeout):
		return 0, nil
	}
}
func (x *xSender) waitCtrl(timeout time.Duration) byte {
	select {
	case b := <-x.fromRx:
		return b
	case <-time.After(timeout):
		return 0
	}
}

func TestXmodemReceive(t *testing.T) {
	payload := bytes.Repeat([]byte("Airoha recovery blob "), 40) // ~840 bytes -> 7 blocks
	x := &xSender{toRx: make(chan byte, 4096), fromRx: make(chan byte, 4096)}
	done := make(chan struct{})
	var got []byte
	var rerr error
	go func() {
		got, rerr = xmodemReceive(x, nil, nil)
		close(done)
	}()
	// Wait for the receiver's initial 'C' (CRC mode request).
	if c := x.waitCtrl(3 * time.Second); c != 'C' {
		t.Fatalf("expected C, got %d", c)
	}
	blocks := (len(payload) + 127) / 128
	for i := 0; i < blocks; i++ {
		data := make([]byte, 128)
		for j := range data {
			data[j] = xSUB
		}
		copy(data, payload[i*128:min((i+1)*128, len(payload))])
		seq := byte(i + 1)
		pkt := []byte{xSOH, seq, 255 - seq}
		pkt = append(pkt, data...)
		crc := crc16Xmodem(data)
		pkt = append(pkt, byte(crc>>8), byte(crc))
		for _, b := range pkt {
			x.toRx <- b
		}
		if c := x.waitCtrl(3 * time.Second); c != xACK {
			t.Fatalf("block %d not ACKed, got %d", i+1, c)
		}
	}
	x.toRx <- xEOT
	if c := x.waitCtrl(3 * time.Second); c != xACK {
		t.Fatalf("EOT not ACKed, got %d", c)
	}
	<-done
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestXmodemReceiveRejectsBadCRC(t *testing.T) {
	x := &xSender{toRx: make(chan byte, 4096), fromRx: make(chan byte, 4096)}
	done := make(chan struct{})
	go func() { xmodemReceive(x, nil, nil); close(done) }()
	x.waitCtrl(3 * time.Second)
	data := make([]byte, 128)
	pkt := []byte{xSOH, 1, 254}
	pkt = append(pkt, data...)
	pkt = append(pkt, 0xff, 0xff) // wrong CRC
	for _, b := range pkt {
		x.toRx <- b
	}
	if c := x.waitCtrl(3 * time.Second); c != xNAK {
		t.Fatalf("bad CRC should NAK, got %d", c)
	}
	x.toRx <- xCAN
	<-done
}
