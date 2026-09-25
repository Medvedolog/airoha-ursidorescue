//go:build windows

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// The Windows folder dialog is the Explorer-style common item dialog
// (IFileOpenDialog with FOS_PICKFOLDERS): the same window as for files, in
// folder mode. It is a COM object, called through its vtable with plain
// syscalls, no cgo.

var (
	ole32                        = syscall.NewLazyDLL("ole32.dll")
	shell32                      = syscall.NewLazyDLL("shell32.dll")
	pCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	pCoUninitialize              = ole32.NewProc("CoUninitialize")
	pCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	pCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	pSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	clsidFileOpenDialog          = comGUID(0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7})
	iidIFileOpenDialog           = comGUID(0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60})
	iidIShellItem                = comGUID(0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE})
)

type guid struct {
	d1     uint32
	d2, d3 uint16
	d4     [8]byte
}

func comGUID(d1 uint32, d2, d3 uint16, d4 [8]byte) guid { return guid{d1, d2, d3, d4} }

const (
	clsctxInprocServer   = 0x1
	coinitApartment      = 0x2
	coinitDisableOLE1DDE = 0x4
	fosNoChangeDir       = 0x8
	fosPickFolders       = 0x20
	fosForceFileSystem   = 0x40
	fosPathMustExist     = 0x800
	sigdnFileSysPath     = 0x80058000
	hresultCancelled     = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
	rpcEChangedMode      = 0x80010106
)

// IFileOpenDialog vtable slots (IUnknown, IModalWindow, IFileDialog).
const (
	vRelease        = 2
	vShow           = 3
	vSetOptions     = 9
	vGetOptions     = 10
	vSetFolder      = 12
	vSetTitle       = 17
	vGetResult      = 20
	vGetDisplayName = 5 // IShellItem
)

// comCall calls method slot of the COM object obj and returns its HRESULT.
func comCall(obj unsafe.Pointer, slot int, args ...uintptr) uint32 {
	vtbl := *(**[32]uintptr)(obj)
	r, _, _ := syscall.SyscallN(vtbl[slot], append([]uintptr{uintptr(obj)}, args...)...)
	return uint32(r)
}

func failed(hr uint32) bool { return int32(hr) < 0 }

// pickDir returns the chosen folder, "" when the dialog was cancelled.
func pickDir(title, dir string) (string, error) {
	if pCoCreateInstance.Find() != nil {
		return "", fmt.Errorf("ole32.dll")
	}
	type result struct {
		path string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		p, err := pickDirOnThread(title, dir)
		done <- result{p, err}
	}()
	r := <-done
	return r.path, r.err
}

func pickDirOnThread(title, dir string) (string, error) {
	hr, _, _ := pCoInitializeEx.Call(0, coinitApartment|coinitDisableOLE1DDE)
	if failed(uint32(hr)) && uint32(hr) != rpcEChangedMode {
		return "", fmt.Errorf("CoInitializeEx 0x%08x", uint32(hr))
	}
	if !failed(uint32(hr)) {
		defer pCoUninitialize.Call()
	}
	var dlg unsafe.Pointer
	hr, _, _ = pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if failed(uint32(hr)) {
		return "", fmt.Errorf("IFileOpenDialog 0x%08x", uint32(hr))
	}
	defer comCall(dlg, vRelease)

	var opts uint32
	comCall(dlg, vGetOptions, uintptr(unsafe.Pointer(&opts)))
	comCall(dlg, vSetOptions, uintptr(opts|fosPickFolders|fosForceFileSystem|fosPathMustExist|fosNoChangeDir))
	if t, err := syscall.UTF16PtrFromString(title); err == nil {
		comCall(dlg, vSetTitle, uintptr(unsafe.Pointer(t)))
	}
	if dir != "" {
		if d, err := syscall.UTF16PtrFromString(dir); err == nil {
			var folder unsafe.Pointer
			hr, _, _ = pSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(d)), 0,
				uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&folder)))
			if !failed(uint32(hr)) && folder != nil {
				comCall(dlg, vSetFolder, uintptr(folder))
				comCall(folder, vRelease)
			}
		}
	}
	owner, _, _ := pGetConsoleWindow.Call()
	switch hr := comCall(dlg, vShow, owner); {
	case hr == hresultCancelled:
		return "", nil
	case failed(hr):
		return "", fmt.Errorf("IFileOpenDialog.Show 0x%08x", hr)
	}
	var item unsafe.Pointer
	if hr := comCall(dlg, vGetResult, uintptr(unsafe.Pointer(&item))); failed(hr) || item == nil {
		return "", fmt.Errorf("IFileOpenDialog.GetResult 0x%08x", hr)
	}
	defer comCall(item, vRelease)
	var name *uint16
	if hr := comCall(item, vGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&name))); failed(hr) || name == nil {
		return "", fmt.Errorf("IShellItem.GetDisplayName 0x%08x", hr)
	}
	defer pCoTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
	return utf16PtrToString(name), nil
}

// utf16PtrToString reads a NUL-terminated UTF-16 string owned by COM.
func utf16PtrToString(p *uint16) string {
	var s []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return syscall.UTF16ToString(s)
}
