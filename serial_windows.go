//go:build windows

package main

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

const (
	genericRead  = 0x80000000
	genericWrite = 0x40000000
	openExisting = 3
	purgeRxAbort = 0x0002
	purgeRxClear = 0x0008
)

type dcb struct {
	DCBlength uint32
	BaudRate  uint32
	Flags     uint32
	Reserved  uint16
	XonLim    uint16
	XoffLim   uint16
	ByteSize  byte
	Parity    byte
	StopBits  byte
	XonChar   byte
	XoffChar  byte
	ErrorChar byte
	EofChar   byte
	EvtChar   byte
	Reserved1 uint16
}
type commTimeouts struct{ ReadIntervalTimeout, ReadTotalTimeoutMultiplier, ReadTotalTimeoutConstant, WriteTotalTimeoutMultiplier, WriteTotalTimeoutConstant uint32 }

type windowsSerial struct {
	h    syscall.Handle
	name string
}

var (
	k32              = syscall.NewLazyDLL("kernel32.dll")
	pCreateFileW     = k32.NewProc("CreateFileW")
	pGetCommState    = k32.NewProc("GetCommState")
	pSetCommState    = k32.NewProc("SetCommState")
	pSetCommTimeouts = k32.NewProc("SetCommTimeouts")
	pSetupComm       = k32.NewProc("SetupComm")
	pPurgeComm       = k32.NewProc("PurgeComm")
	pReadFile        = k32.NewProc("ReadFile")
	pWriteFile       = k32.NewProc("WriteFile")
	pQueryDosDeviceW = k32.NewProc("QueryDosDeviceW")
	pCloseHandle     = k32.NewProc("CloseHandle")
)

func winErr(r uintptr) error {
	if r == 0 {
		return syscall.GetLastError()
	}
	return nil
}
func openSerial(name string) (Serial, error) {
	dev := name
	if len(name) >= 3 && (name[0] == 'C' || name[0] == 'c') {
		dev = `\\.\` + name
	}
	p, _ := syscall.UTF16PtrFromString(dev)
	r, _, e := pCreateFileW.Call(uintptr(unsafe.Pointer(p)), genericRead|genericWrite, 0, 0, openExisting, 0, 0)
	if r == uintptr(syscall.InvalidHandle) {
		return nil, e
	}
	s := &windowsSerial{h: syscall.Handle(r), name: name}
	if rr, _, _ := pSetupComm.Call(r, 65536, 65536); rr == 0 {
		s.Close()
		return nil, syscall.GetLastError()
	}
	var d dcb
	d.DCBlength = uint32(unsafe.Sizeof(d))
	if rr, _, _ := pGetCommState.Call(r, uintptr(unsafe.Pointer(&d))); rr == 0 {
		s.Close()
		return nil, syscall.GetLastError()
	}
	d.BaudRate = 115200
	d.Flags = 1
	d.XonLim = 2048
	d.XoffLim = 512
	d.ByteSize = 8
	d.Parity = 0
	d.StopBits = 0
	d.XonChar = 0x11
	d.XoffChar = 0x13
	if rr, _, _ := pSetCommState.Call(r, uintptr(unsafe.Pointer(&d))); rr == 0 {
		s.Close()
		return nil, syscall.GetLastError()
	}
	if err := s.ResetInput(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *windowsSerial) Name() string { return s.name }
func (s *windowsSerial) Close() error { r, _, _ := pCloseHandle.Call(uintptr(s.h)); return winErr(r) }
func (s *windowsSerial) ResetInput() error {
	r, _, _ := pPurgeComm.Call(uintptr(s.h), purgeRxAbort|purgeRxClear)
	return winErr(r)
}
func (s *windowsSerial) setTimeout(d time.Duration) error {
	ms := uint32(d / time.Millisecond)
	t := commTimeouts{0xffffffff, 0xffffffff, ms, 0, 10000}
	r, _, _ := pSetCommTimeouts.Call(uintptr(s.h), uintptr(unsafe.Pointer(&t)))
	return winErr(r)
}
func (s *windowsSerial) Read(p []byte, timeout time.Duration) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.setTimeout(timeout); err != nil {
		return 0, err
	}
	var got uint32
	r, _, _ := pReadFile.Call(uintptr(s.h), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&got)), 0)
	if r == 0 {
		return 0, syscall.GetLastError()
	}
	return int(got), nil
}
func (s *windowsSerial) Write(p []byte) error {
	for len(p) > 0 {
		nmax := len(p)
		if nmax > 65536 {
			nmax = 65536
		}
		var got uint32
		r, _, _ := pWriteFile.Call(uintptr(s.h), uintptr(unsafe.Pointer(&p[0])), uintptr(nmax), uintptr(unsafe.Pointer(&got)), 0)
		if r == 0 {
			return syscall.GetLastError()
		}
		if got == 0 {
			return fmt.Errorf("serial write returned zero")
		}
		p = p[int(got):]
	}
	return nil
}
func listSerialPorts() []string {
	// Query the DOS device namespace so the menu shows only COM ports that
	// actually exist. This detects USB-UART and virtual COM drivers without
	// opening the port or disturbing another application that owns it.
	out := make([]string, 0, 16)
	target := make([]uint16, 4096)
	for i := 1; i <= 256; i++ {
		name := fmt.Sprintf("COM%d", i)
		p, err := syscall.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		r, _, _ := pQueryDosDeviceW.Call(
			uintptr(unsafe.Pointer(p)),
			uintptr(unsafe.Pointer(&target[0])),
			uintptr(len(target)),
		)
		if r != 0 {
			out = append(out, name)
		}
	}
	return out
}
