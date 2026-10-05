//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

type consoleState struct{ t syscall.Termios }

func consoleRaw() (*consoleState, error) {
	fd := int(os.Stdin.Fd())
	var old syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&old)))
	if e != 0 {
		return nil, e
	}
	n := old
	n.Iflag &^= syscall.ICRNL | syscall.IXON
	n.Oflag &^= syscall.OPOST
	n.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	n.Cflag |= syscall.CS8
	n.Cc[syscall.VMIN] = 1
	n.Cc[syscall.VTIME] = 0
	_, _, e = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&n)))
	if e != 0 {
		return nil, e
	}
	return &consoleState{old}, nil
}
func consoleRestore(s *consoleState) {
	if s == nil {
		return
	}
	fd := int(os.Stdin.Fd())
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&s.t)))
}

// enableVTOutput is a no-op on Linux; ANSI is always understood.
func enableVTOutput() {}

type consoleWinSize struct {
	Row, Col, Xpixel, Ypixel uint16
}

func consoleRows() int {
	var ws consoleWinSize
	fd := int(os.Stdout.Fd())
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if e == 0 && ws.Row > 0 {
		return int(ws.Row)
	}
	return 24
}

func consoleReadInput(buf []byte) (int, error) {
	return os.Stdin.Read(buf)
}

// systemLang: Linux has no separate UI language; the locale variables decide.
func systemLang() string { return "" }
