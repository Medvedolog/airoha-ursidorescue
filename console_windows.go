//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

type consoleState struct{ mode uint32 }

var pGetConsoleMode = k32.NewProc("GetConsoleMode")
var pSetConsoleMode = k32.NewProc("SetConsoleMode")
var pGetConsoleScreenBufferInfo = k32.NewProc("GetConsoleScreenBufferInfo")
var pReadConsoleInputW = k32.NewProc("ReadConsoleInputW")
var pGetUserDefaultUILanguage = k32.NewProc("GetUserDefaultUILanguage")

// systemLang is the Windows display language: Russian → "ru", else "en".
func systemLang() string {
	r, _, _ := pGetUserDefaultUILanguage.Call()
	if r&0x3ff == 0x19 { // LANG_RUSSIAN
		return "ru"
	}
	return "en"
}

func consoleRaw() (*consoleState, error) {
	h := syscall.Handle(os.Stdin.Fd())
	var old uint32
	r, _, _ := pGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&old)))
	if r == 0 {
		return nil, syscall.GetLastError()
	}
	// ReadConsoleInputW below consumes KEY_EVENT_RECORDs directly. Do not enable
	// VIRTUAL_TERMINAL_INPUT here: mixing VT translation with os/File reads was
	// the cause of test7 dropping normal keys/Enter on real Windows consoles.
	// QuickEdit stays enabled for native mouse selection/copy/paste.
	const (
		enableProcessedInput = 0x0001
		enableLineInput      = 0x0002
		enableEchoInput      = 0x0004
		enableQuickEditMode  = 0x0040
		enableExtendedFlags  = 0x0080
		enableVTInput        = 0x0200
	)
	mode := old &^ (enableProcessedInput | enableLineInput | enableEchoInput | enableVTInput)
	mode |= enableExtendedFlags | enableQuickEditMode
	r, _, _ = pSetConsoleMode.Call(uintptr(h), uintptr(mode))
	if r == 0 {
		return nil, syscall.GetLastError()
	}
	return &consoleState{old}, nil
}
func consoleRestore(s *consoleState) {
	if s == nil {
		return
	}
	pSetConsoleMode.Call(uintptr(syscall.Handle(os.Stdin.Fd())), uintptr(s.mode))
}

// enableVTOutput turns on ENABLE_VIRTUAL_TERMINAL_PROCESSING on stdout so ANSI
// SGR/erase sequences render on modern Windows consoles.
func enableVTOutput() {
	h := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	if r, _, _ := pGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return
	}
	pSetConsoleMode.Call(uintptr(h), uintptr(mode|0x0004))
}

type winCoord struct{ X, Y int16 }
type winSmallRect struct{ Left, Top, Right, Bottom int16 }
type winConsoleScreenBufferInfo struct {
	Size              winCoord
	CursorPosition    winCoord
	Attributes        uint16
	Window            winSmallRect
	MaximumWindowSize winCoord
}

func consoleRows() int {
	h := syscall.Handle(os.Stdout.Fd())
	var info winConsoleScreenBufferInfo
	if r, _, _ := pGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info))); r != 0 {
		n := int(info.Window.Bottom-info.Window.Top) + 1
		if n > 0 {
			return n
		}
	}
	return 24
}

const winKeyEvent = 0x0001

type winInputRecord struct {
	EventType uint16
	_         uint16
	Event     [16]byte
}

type winKeyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

var consoleInputPending []byte

// consoleReadInput reads Windows KEY_EVENT_RECORDs rather than relying on
// ReadFile/VT-input translation. That gives deterministic Enter, arrows,
// control shortcuts, Unicode-layout detection and pasted text.
func consoleReadInput(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if len(consoleInputPending) > 0 {
		n := copy(buf, consoleInputPending)
		consoleInputPending = consoleInputPending[n:]
		return n, nil
	}
	h := syscall.Handle(os.Stdin.Fd())
	for {
		var rec winInputRecord
		var nrec uint32
		r, _, _ := pReadConsoleInputW.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&rec)),
			1,
			uintptr(unsafe.Pointer(&nrec)),
		)
		if r == 0 {
			return 0, syscall.GetLastError()
		}
		if nrec == 0 || rec.EventType != winKeyEvent {
			continue
		}
		key := (*winKeyEventRecord)(unsafe.Pointer(&rec.Event[0]))
		if key.KeyDown == 0 {
			continue
		}
		out := translateWindowsConsoleKey(
			key.VirtualKeyCode,
			rune(key.UnicodeChar),
			key.ControlKeyState,
			key.RepeatCount,
		)
		if len(out) == 0 {
			continue
		}
		n := copy(buf, out)
		if n < len(out) {
			consoleInputPending = append(consoleInputPending[:0], out[n:]...)
		}
		return n, nil
	}
}
