package main

import (
	"fmt"
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
			if f := chromeFooter(120, ci); !strings.Contains(f, "Ctrl+Q") && !strings.Contains(f, "F10") {
				t.Fatalf("footer lacks the exit key: %q", f)
			}
			if p := chromePlate(80, "UART", "COM6", simple); !strings.Contains(p, "COM6") || strings.Contains(strings.ReplaceAll(p, "\r\n", ""), "\n") {
				t.Fatalf("plate: %q", p)
			}
		}
	}
	setLang("ru")
}

// feedAll runs chunks through the parser and records bytes and actions in
// stream order, as "data" and "<act>" entries.
func feedAll(p *chromeParser, chunks ...string) []string {
	var got []string
	for _, c := range chunks {
		for _, pc := range p.feed([]byte(c), true) {
			if len(pc.data) > 0 {
				got = append(got, string(pc.data))
			}
			if pc.act != actNone {
				got = append(got, fmt.Sprintf("<%d>", pc.act))
			}
		}
	}
	return got
}

func TestChromeParserAltScreen(t *testing.T) {
	var p chromeParser
	got := feedAll(&p, "top\r\n\x1b[?1049h\x1b[H\x1b[2JTOP", "\x1b[?104", "9lAN7581> ")
	want := []string{"top\r\n", fmt.Sprintf("<%d>", actSuspend), "\x1b[?1049h\x1b[H\x1b[2JTOP",
		"\x1b[?1049l", fmt.Sprintf("<%d>", actResume), "AN7581> "}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("split exit:\n got %q\nwant %q", got, want)
	}
}

// A U-Boot bootmenu: cursor hidden, clear, menu; cursor shown on exit.
func TestChromeParserSoftFullscreen(t *testing.T) {
	var p chromeParser
	got := feedAll(&p, "\x1b[?25l\x1b[2J\x1b[1;1H  1. Boot\x1b[?2", "5hStarting kernel")
	want := []string{"\x1b[?25l", fmt.Sprintf("<%d>", actSuspend), "\x1b[2J\x1b[1;1H  1. Boot", "\x1b[?25h",
		fmt.Sprintf("<%d>", actResumeClear), "Starting kernel"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bootmenu:\n got %q\nwant %q", got, want)
	}
}

// A shell "clear" and bare cursor show/hide do not take the screen.
func TestChromeParserBareSequences(t *testing.T) {
	var p chromeParser
	got := feedAll(&p, "\x1b[?25h", "\x1b[H\x1b[2J# ", "\x1b[?25lx\x1b[?25h", "\x1b[5;10Hy")
	want := []string{"\x1b[?25h", "\x1b[3;1H\x1b[2J", fmt.Sprintf("<%d>", actRefresh), "# ",
		"\x1b[?25lx\x1b[?25h", "\x1b[5;10Hy"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bare:\n got %q\nwant %q", got, want)
	}
	if p.mode != modeNone {
		t.Fatal("bare sequences changed the mode")
	}
}

// Nothing is lost or reordered, whatever the read boundaries.
func TestChromeParserAnySplit(t *testing.T) {
	stream := "a\x1b[?1049hb\x1b[31mc\x1b[?1049ld\x1b[?25l\x1b[2Je\x1b[?25hf\x1bMg"
	for cut := 0; cut <= len(stream); cut++ {
		var p chromeParser
		var data strings.Builder
		var acts []chromeAct
		for _, c := range []string{stream[:cut], stream[cut:]} {
			for _, pc := range p.feed([]byte(c), false) {
				data.Write(pc.data)
				if pc.act != actNone {
					acts = append(acts, pc.act)
				}
			}
		}
		if data.String() != stream {
			t.Fatalf("cut %d: bytes changed: %q", cut, data.String())
		}
		if fmt.Sprint(acts) != fmt.Sprint([]chromeAct{actSuspend, actResume, actSuspend, actResumeClear}) {
			t.Fatalf("cut %d: actions %v", cut, acts)
		}
	}
}

func TestChromeFollowsWidthOnlyResize(t *testing.T) {
	saved := chromeSize
	defer func() { chromeSize = saved }()
	chromeSize = func() (int, int) { return 24, 120 }
	tt := &uartTerm{a: &App{}, s: &fakeSerial{name: "COM6"}, raw: true, chrome: &termChrome{on: true, rows: 24, cols: 80}}
	tt.deviceOutput([]byte("x"))
	if tt.chrome.cols != 120 {
		t.Fatalf("cols %d after a width-only resize", tt.chrome.cols)
	}
}

func TestTopBarPhase(t *testing.T) {
	m := &tuiModel{busy: true, opTitle: "Установить UrsusBoot (UART)"}
	m.progress = &app.Progress{Label: "READ", Current: 37, Total: 100}
	if name, step := m.phase(); name != m.opTitle || step != "READ 37%" {
		t.Fatalf("phase %q %q", name, step)
	}
	m.progress = nil
	m.overall = &app.Progress{Current: 1, Total: 6}
	if _, step := m.phase(); !strings.HasSuffix(step, "2/6") {
		t.Fatalf("step %q", step)
	}
	m.busy = false
	if name, step := m.phase(); name != "" || step != "" {
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

// A new operation starts without the previous one's progress.
func TestStartClearsProgress(t *testing.T) {
	a := &App{ui: (&app.Recorder{}).UI(), portOverride: "COM1"}
	m := newTUIModel(a, &tuiUI{})
	m.overall = &app.Progress{Current: 6, Total: 6}
	m.progress = &app.Progress{Label: "READ", Current: 1, Total: 2}
	_ = m.start(tuiItem{label: "x", kind: "support-bundle"})
	if m.overall != nil || m.progress != nil {
		t.Fatal("progress survived into the next operation")
	}
}

// F2/F3/F4/F10 are found in any chunk, in the xterm, Linux console and rxvt
// forms, and Windows keys arrive as the xterm ones.
func TestFKeys(t *testing.T) {
	for in, want := range map[string]byte{"ab\x1bOQcd": 's', "\x1b[13~": 'r', "\x1b[[D": 'l', "x\x1b[21~": 'q'} {
		if i, n, act := findFKey([]byte(in)); i < 0 || act != want || in[i:i+n][0] != 0x1b {
			t.Fatalf("%q: %d %d %c", in, i, n, act)
		}
	}
	if i, _, _ := findFKey([]byte("\x1b[A\x1b[3~ls")); i >= 0 {
		t.Fatal("arrows and Delete are not terminal commands")
	}
	if got := string(translateWindowsConsoleKey(winVKF2, 0, 0, 1)); got != "\x1bOQ" {
		t.Fatalf("Windows F2 = %q", got)
	}
	// Local only in the terminal and while no fullscreen program runs.
	tt := &uartTerm{chrome: &termChrome{on: true}}
	if !tt.fkeysLocal() {
		t.Fatal("terminal: F-keys are local")
	}
	tt.chrome.suspended = true
	if tt.fkeysLocal() {
		t.Fatal("a fullscreen device program gets the F-keys")
	}
	if (&uartTerm{simple: true}).fkeysLocal() {
		t.Fatal("the transparent console has no local F-keys")
	}
}
