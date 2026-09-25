package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"ursidorescue/app"
)

func TestChromeBarsFit(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		setLang(lang)
		for _, simple := range []bool{false, true} {
			ci := chromeInfo{port: "COM6", session: "a3f91", raw: true, log: true, simple: simple}
			for w := 40; w <= 200; w += 7 {
				for name, s := range map[string]string{"header": chromeHeader(w, ci), "footer": chromeFooter(w, ci), "rule": chromeRule(w, "UART")} {
					if got := lipgloss.Width(s); got != w && name != "rule" || got > w {
						t.Fatalf("%s %s w=%d: width %d", lang, name, w, got)
					}
				}
			}
			if h := chromeHeader(120, ci); !strings.Contains(h, "COM6") || !strings.Contains(h, "RAW") {
				t.Fatalf("header lacks the port or the mode: %q", h)
			}
			if f := chromeFooter(120, ci); !strings.Contains(f, "Ctrl+Q") {
				t.Fatalf("footer lacks the exit key: %q", f)
			}
			if p := chromePlate(80, "UART", "COM6", simple); !strings.Contains(p, "COM6") || strings.Contains(strings.ReplaceAll(p, "\r\n", ""), "\n") {
				t.Fatalf("plate: %q", p)
			}
		}
	}
	setLang("ru")
}

// A fullscreen program on the device takes the chrome down; leaving the
// alternate screen brings it back.
func TestChromeStepsAsideForFullscreen(t *testing.T) {
	tt := &uartTerm{chrome: &termChrome{on: true, rows: 24, cols: 80}}
	if tt.chromeDeviceLocked([]byte("\x1b[?1049h\x1b[H\x1b[2J")) || !tt.chrome.suspended {
		t.Fatal("fullscreen output must suspend the chrome")
	}
	if tt.chromeRowsLocked() != 0 {
		t.Fatal("the pager must use the whole screen while suspended")
	}
	if !tt.chromeDeviceLocked([]byte("bye\x1b[?1049l")) {
		t.Fatal("leaving the alternate screen must restore the chrome")
	}
	plain := &uartTerm{}
	if plain.chromeDeviceLocked([]byte("\x1b[2J")) {
		t.Fatal("no chrome in the text console")
	}
}

func TestTopBarPhase(t *testing.T) {
	m := &tuiModel{busy: true, opTitle: "Установить UrsusBoot (UART)"}
	m.progress = &app.Progress{Label: "READ", Current: 37, Total: 100}
	if ph := m.phase(); !strings.Contains(ph, "READ 37%") {
		t.Fatalf("phase %q", ph)
	}
	m.progress = nil
	m.overall = &app.Progress{Current: 1, Total: 6}
	if ph := m.phase(); !strings.HasSuffix(ph, "2/6") {
		t.Fatalf("phase %q", ph)
	}
	m.busy = false
	if m.phase() != "" {
		t.Fatal("no phase when idle")
	}
}

// A full-screen item without a port asks for it in the TUI first.
func TestFullScreenItemPicksPortInTUI(t *testing.T) {
	a := &App{ui: (&app.Recorder{}).UI()}
	m := newTUIModel(a, &tuiUI{})
	it := tuiItem{label: "console", kind: "shell"}
	if cmd := m.start(it); cmd != nil || m.busy {
		t.Fatal("the item must wait for the port")
	}
	if m.dlg == nil || !m.dlg.isPort || m.dlg.then == nil || m.dlg.then.kind != "shell" {
		t.Fatalf("port dialog with the pending item expected: %+v", m.dlg)
	}
}
