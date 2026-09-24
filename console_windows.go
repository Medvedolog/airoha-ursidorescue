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

func consoleRaw() (*consoleState, error) {
	h := syscall.Handle(os.Stdin.Fd())
	var old uint32
	r, _, _ := pGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&old)))
	if r == 0 {
		return nil, syscall.GetLastError()
	}
	// Keep raw keyboard delivery for UART, but leave QuickEdit enabled so the
	// Windows console still supports native selection/copy/paste. EXTENDED_FLAGS
	// is required when changing QUICK_EDIT_MODE.
	const (
		enableProcessedInput = 0x0001
		enableLineInput      = 0x0002
		enableEchoInput      = 0x0004
		enableQuickEditMode  = 0x0040
		enableExtendedFlags  = 0x0080
		enableVTInput        = 0x0200
	)
	mode := old &^ (enableProcessedInput | enableLineInput | enableEchoInput)
	mode |= enableVTInput | enableExtendedFlags | enableQuickEditMode
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
