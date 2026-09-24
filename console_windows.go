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

func consoleRaw() (*consoleState, error) {
	h := syscall.Handle(os.Stdin.Fd())
	var old uint32
	r, _, _ := pGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&old)))
	if r == 0 {
		return nil, syscall.GetLastError()
	}
	// Clear ECHO_INPUT|LINE_INPUT|PROCESSED_INPUT|QUICK_EDIT_MODE and set
	// VIRTUAL_TERMINAL_INPUT so arrow/Home/End keys arrive as ANSI sequences.
	mode := (old &^ (0x0002 | 0x0004 | 0x0001 | 0x0040)) | 0x0200
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
