package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// screen is a tiny terminal: printable runes, \r, \n and \b, no escapes.
func screen(out string) []string {
	lines := []string{""}
	row, col := 0, 0
	for _, r := range out {
		switch r {
		case '\r':
			col = 0
		case '\n':
			row++
			if row == len(lines) {
				lines = append(lines, "")
			}
		case '\b':
			if col > 0 {
				col--
			}
		default:
			l := []rune(lines[row])
			for len(l) <= col {
				l = append(l, ' ')
			}
			l[col] = r
			lines[row] = string(l)
			col++
		}
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	saved := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = saved
	b, _ := io.ReadAll(r)
	return string(b)
}

// Line mode must not draw its "] " over the device's prompt (seen on
// hardware as "] ot@OpenWrt:~#").
func TestLineModeKeepsDevicePrompt(t *testing.T) {
	fs := &fakeSerial{name: "COM6"}
	tt := &uartTerm{a: &App{}, s: fs, prompt: "] "}
	out := captureStdout(t, func() {
		tt.deviceOutput([]byte("root@OpenWrt:~# "))
		for _, r := range "ls" {
			tt.onKey(keyEvent{kind: kRune, r: r})
		}
		tt.onKey(keyEvent{kind: kEnter})
		tt.deviceOutput([]byte("ls\r\nbin  etc\r\nroot@OpenWrt:~# "))
		for i := 0; i < 3; i++ {
			tt.onKey(keyEvent{kind: kEnter})
			tt.deviceOutput([]byte("\r\nroot@OpenWrt:~# "))
		}
		tt.onKey(keyEvent{kind: kRune, r: 'x'})
	})
	got := screen(out)
	want := []string{"root@OpenWrt:~# ls", "bin  etc", "root@OpenWrt:~#", "root@OpenWrt:~#", "root@OpenWrt:~#", "root@OpenWrt:~# x"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("screen:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
