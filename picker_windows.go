//go:build windows

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// The Windows file dialog is the common Open dialog (comdlg32
// GetOpenFileNameW), called directly: no cgo, no PowerShell.

var (
	comdlg32                 = syscall.NewLazyDLL("comdlg32.dll")
	pGetOpenFileNameW        = comdlg32.NewProc("GetOpenFileNameW")
	pCommDlgExtendedError    = comdlg32.NewProc("CommDlgExtendedError")
	pGetConsoleWindow        = k32.NewProc("GetConsoleWindow")
	errFilePickerUnavailable = fmt.Errorf("comdlg32.dll")
)

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

const (
	ofnNoChangeDir     = 0x00000008
	ofnPathMustExist   = 0x00000800
	ofnFileMustExist   = 0x00001000
	ofnExplorer        = 0x00080000
	ofnDontAddToRecent = 0x02000000
)

func filePickerAvailable() bool { return pGetOpenFileNameW.Find() == nil }

// utf16z encodes s with NUL separators kept (the filter is a list of
// NUL-separated strings ending in two NULs).
func utf16z(s string) *uint16 {
	u := utf16.Encode([]rune(s + "\x00"))
	return &u[0]
}

// pickFile returns the chosen path, "" when the dialog was cancelled.
func pickFile(title, dir string) (string, error) {
	if !filePickerAvailable() {
		return "", errFilePickerUnavailable
	}
	type result struct {
		path string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		// The dialog runs its own message loop on this thread.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		buf := make([]uint16, 32768)
		owner, _, _ := pGetConsoleWindow.Call()
		ofn := openFileNameW{
			hwndOwner:    owner,
			lpstrFilter:  utf16z(L("Образы и бэкапы (*.bin;*.gz;*.fip;*.itb;*.img)", "Images and backups (*.bin;*.gz;*.fip;*.itb;*.img)") + "\x00*.bin;*.gz;*.fip;*.itb;*.img\x00" + L("Все файлы (*.*)", "All files (*.*)") + "\x00*.*\x00"),
			nFilterIndex: 1,
			lpstrFile:    &buf[0],
			nMaxFile:     uint32(len(buf)),
			lpstrTitle:   utf16z(title),
			flags:        ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir | ofnDontAddToRecent,
		}
		if dir != "" {
			ofn.lpstrInitialDir = utf16z(dir)
		}
		ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
		r, _, _ := pGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
		if r == 0 {
			if code, _, _ := pCommDlgExtendedError.Call(); code != 0 {
				done <- result{err: fmt.Errorf("GetOpenFileNameW error 0x%x", code)}
				return
			}
			done <- result{} // cancelled
			return
		}
		done <- result{path: syscall.UTF16ToString(buf)}
	}()
	r := <-done
	return r.path, r.err
}
