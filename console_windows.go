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
	mode := old &^ (0x0002 | 0x0004 | 0x0001 | 0x0040)
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
