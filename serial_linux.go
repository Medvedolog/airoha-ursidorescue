//go:build linux

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"syscall"
	"time"
	"unsafe"
)

type linuxSerial struct {
	fd   int
	name string
}

func openSerial(name string) (Serial, error) {
	fd, err := syscall.Open(name, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	var tio syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&tio)))
	if errno != 0 {
		syscall.Close(fd)
		return nil, errno
	}
	// Raw 115200 8N1, local, no HW/SW flow control.
	tio.Iflag = 0
	tio.Oflag = 0
	tio.Lflag = 0
	tio.Cflag = syscall.B115200 | syscall.CS8 | syscall.CLOCAL | syscall.CREAD
	tio.Cc[syscall.VMIN] = 0
	tio.Cc[syscall.VTIME] = 0
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&tio)))
	if errno != 0 {
		syscall.Close(fd)
		return nil, errno
	}
	return &linuxSerial{fd: fd, name: name}, nil
}

func (s *linuxSerial) Name() string { return s.name }
func (s *linuxSerial) Close() error { return syscall.Close(s.fd) }
func (s *linuxSerial) ResetInput() error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(s.fd), uintptr(0x540B), uintptr(syscall.TCIFLUSH))
	if errno != 0 {
		return errno
	}
	return nil
}
func (s *linuxSerial) Write(p []byte) error {
	for len(p) > 0 {
		n, err := syscall.Write(s.fd, p)
		if err == syscall.EAGAIN {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
		if n <= 0 {
			return errors.New("serial write returned zero")
		}
		p = p[n:]
	}
	return nil
}
func (s *linuxSerial) Read(p []byte, timeout time.Duration) (int, error) {
	var rfds syscall.FdSet
	fd := s.fd
	if fd/64 >= len(rfds.Bits) {
		return 0, fmt.Errorf("fd too high for select: %d", fd)
	}
	rfds.Bits[fd/64] |= 1 << uint(fd%64)
	tv := syscall.NsecToTimeval(timeout.Nanoseconds())
	n, err := syscall.Select(fd+1, &rfds, nil, nil, &tv)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	nr, err := syscall.Read(fd, p)
	if err == syscall.EAGAIN {
		return 0, nil
	}
	return nr, err
}

func listSerialPorts() []string {
	patterns := []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyAMA*", "/dev/ttyS*"}
	seen := map[string]bool{}
	var out []string
	for _, pat := range patterns {
		xs, _ := filepath.Glob(pat)
		for _, x := range xs {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	sort.Strings(out)
	return out
}
