package probe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Port is the UART as the probe sees it. UrsidoRescue's serial ports satisfy it.
type Port interface {
	Read(p []byte, timeout time.Duration) (int, error)
	Write(p []byte) error
	Name() string
}

// Timing collects every wait the probe makes, so tests can run fast.
type Timing struct {
	Poll          time.Duration // single Read timeout
	Quiet         time.Duration // silence that ends a burst of output
	PromptSettle  time.Duration // silence after a prompt before trusting it
	IdleKick      time.Duration // silence at start before one Ctrl-C
	LinuxSettle   time.Duration // silence after a kernel boot before looking for a shell
	CommandDef    time.Duration // default command timeout
	CommandLong   time.Duration // dumps and hashes
	InterruptGap  time.Duration // between autoboot interrupt keys
	HandshakeWait time.Duration // BootROM x -> C
}

// DefaultTiming is used on real hardware.
var DefaultTiming = Timing{
	Poll:          150 * time.Millisecond,
	Quiet:         400 * time.Millisecond,
	PromptSettle:  450 * time.Millisecond,
	IdleKick:      8 * time.Second,
	LinuxSettle:   4 * time.Second,
	CommandDef:    30 * time.Second,
	CommandLong:   5 * time.Minute,
	InterruptGap:  150 * time.Millisecond,
	HandshakeWait: 15 * time.Second,
}

// Event is one timestamped marker seen on the UART.
type Event struct {
	Time     string  `json:"time"`
	OffsetMS int64   `json:"offset_ms"`
	Marker   string  `json:"marker"`
	Line     string  `json:"line,omitempty"`
	Count    int     `json:"count,omitempty"`
	GapMS    []int64 `json:"gap_ms,omitempty"`
}

// TranscriptEntry is one line of transcript.jsonl (spec §29).
type TranscriptEntry struct {
	Time      string `json:"time"`
	Transport string `json:"transport"`
	Command   string `json:"command"`
	Response  string `json:"response,omitempty"`
	File      string `json:"file,omitempty"`
	Result    string `json:"result"`
	RC        *int   `json:"rc,omitempty"`
	Duration  int64  `json:"duration_ms"`
}

type markerDef struct {
	id string
	re *regexp.Regexp
}

var lineMarkers = []markerDef{
	{"press_x", regexp.MustCompile(`(?i)press\s+x`)},
	{"bl2", regexp.MustCompile(`\bBL2\b`)},
	{"bl21", regexp.MustCompile(`(?i)\bBL21\b`)},
	{"bl22", regexp.MustCompile(`(?i)\bBL22\b`)},
	{"bl23", regexp.MustCompile(`(?i)\bBL23\b`)},
	{"bl31", regexp.MustCompile(`\bBL31\b`)},
	{"bl33", regexp.MustCompile(`\bBL33\b`)},
	{"atf", regexp.MustCompile(`(?i)\b(?:ATF|TF-A|Trusted Firmware)\b|NOTICE:\s+BL`)},
	{"dramc", regexp.MustCompile(`(?i)[AE]N75\d\dDRAMC|dram flow done`)},
	{"uboot", regexp.MustCompile(`U-Boot(?: SPL)? \d{4}\.\d{2}`)},
	{"an7581", regexp.MustCompile(`(?i)\bAN7581|\bEN7581`)},
	{"an7583", regexp.MustCompile(`(?i)\bAN7583|\bEN7583`)},
	{"soc_generic", regexp.MustCompile(`(?i)\b[AE]N75\d\d`)},
	{"autoboot", regexp.MustCompile(`(?i)hit any key to stop autoboot|autoboot in \d|stop autoboot|press any key to (?:stop|abort)|autobooting in`)},
	{"bootmenu", regexp.MustCompile(`(?i)press up/down to move|run default boot command`)},
	{"tcboot", regexp.MustCompile(`(?i)tcboot|boot command mode|\bbldr>`)},
	{"starting_kernel", regexp.MustCompile(`(?i)starting kernel`)},
	{"linux", regexp.MustCompile(`Booting Linux on physical CPU|Linux version \d`)},
	{"openwrt", regexp.MustCompile(`OpenWrt`)},
	{"console_activate", regexp.MustCompile(`(?i)please press enter to activate this console`)},
	{"login", regexp.MustCompile(`(?i)\blogin:\s*$`)},
	{"kernel_panic", regexp.MustCompile(`(?i)kernel panic`)},
	{"destructive_autoboot", regexp.MustCompile(`(?i)mtd erase|erasing 0x|nand erase|writing to nand`)},
}

// Session is one probe run on one UART: it owns the log, the event list and
// the transcript, and it is the only thing that writes to the port.
type Session struct {
	Dir    string
	Port   Port
	Out    io.Writer
	T      Timing
	now    func() time.Time
	start  time.Time
	mu     sync.Mutex
	uart   *os.File
	boot   *os.File // device output outside command responses
	trans  *os.File
	inCmd  int
	events []Event
	seen   map[string]int
	line   []byte
	cRun   int
	cTimes []time.Time
	rx     int64
	binary int64
	tail   []byte
	mute   bool

	Blocked []string
}

// NewSession opens (appending) uart.log and transcript.jsonl in dir.
func NewSession(dir string, port Port, out io.Writer, t Timing) (*Session, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	u, err := os.OpenFile(filepath.Join(dir, "uart.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	tr, err := os.OpenFile(filepath.Join(dir, "transcript.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		u.Close()
		return nil, err
	}
	if out == nil {
		out = io.Discard
	}
	if err := os.MkdirAll(filepath.Join(dir, "uart"), 0o755); err != nil {
		u.Close()
		tr.Close()
		return nil, err
	}
	bl, err := os.OpenFile(filepath.Join(dir, "uart", "boot-console.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		u.Close()
		tr.Close()
		return nil, err
	}
	s := &Session{Dir: dir, Port: port, Out: out, T: t, now: time.Now, uart: u, boot: bl, trans: tr, seen: map[string]int{}}
	s.start = s.now()
	_ = s.loadEvents()
	return s, nil
}

func (s *Session) loadEvents() error {
	b, err := os.ReadFile(filepath.Join(s.Dir, "uart", "events.json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &s.events)
}

// Close flushes events and closes the log files.
func (s *Session) Close() error {
	s.flushLine()
	s.saveEvents()
	s.uart.Close()
	s.boot.Close()
	return s.trans.Close()
}

func (s *Session) saveEvents() {
	_ = os.MkdirAll(filepath.Join(s.Dir, "uart"), 0o755)
	b, _ := json.MarshalIndent(s.events, "", "  ")
	_ = os.WriteFile(filepath.Join(s.Dir, "uart", "events.json"), b, 0o644)
}

// Info prints an operator message and records it in the UART log.
func (s *Session) Info(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(s.Out, "\n[%s] %s\n", s.now().Format("15:04:05"), msg)
	s.mu.Lock()
	fmt.Fprintf(s.uart, "\n[URSIDO %s] %s\n", s.now().Format(time.RFC3339), msg)
	s.mu.Unlock()
}

// Seen reports how many times a marker was observed in this session.
func (s *Session) Seen(marker string) int { return s.seen[marker] }

// Events returns the markers recorded so far.
func (s *Session) Events() []Event { return append([]Event(nil), s.events...) }

// RxBytes is the number of bytes received.
func (s *Session) RxBytes() int64 { return s.rx }

// Tail returns up to the last 64 KiB received.
func (s *Session) Tail() []byte { return s.tail }

func (s *Session) record(marker, line string) {
	now := s.now()
	s.seen[marker]++
	if s.seen[marker] > 20 {
		return
	}
	s.events = append(s.events, Event{Time: now.Format(time.RFC3339Nano), OffsetMS: now.Sub(s.start).Milliseconds(), Marker: marker, Line: strings.TrimSpace(line)})
}

func (s *Session) scan(b []byte) {
	for _, c := range b {
		if c == 'C' {
			s.cRun++
			s.cTimes = append(s.cTimes, s.now())
			if s.cRun == 3 {
				s.record("xmodem_c", "CCC")
			}
			if len(s.cTimes) > 16 {
				s.cTimes = s.cTimes[len(s.cTimes)-16:]
			}
		} else if c != '\r' && c != '\n' && c != ' ' {
			s.cRun = 0
		}
		if c >= 0x80 || (c < 0x20 && c != '\r' && c != '\n' && c != '\t' && c != 0x1b && c != 0x08) {
			s.binary++
		}
		if c == '\n' {
			s.flushLine()
			continue
		}
		if len(s.line) < 4096 {
			s.line = append(s.line, c)
		}
	}
	// "Press x" and login prompts are not newline-terminated.
	if len(s.line) > 0 {
		l := string(s.line)
		for _, m := range []string{"press_x", "login"} {
			if s.seen[m+"@line"] == 0 && markerByID(m).re.MatchString(l) {
				s.seen[m+"@line"] = 1
				s.record(m, l)
			}
		}
	}
}

func markerByID(id string) markerDef {
	for _, m := range lineMarkers {
		if m.id == id {
			return m
		}
	}
	return markerDef{}
}

func (s *Session) flushLine() {
	if len(s.line) == 0 {
		return
	}
	l := strings.TrimRight(string(s.line), "\r")
	partial := s.seen["press_x@line"] > 0 || s.seen["login@line"] > 0
	for _, m := range lineMarkers {
		if (m.id == "press_x" || m.id == "login") && partial && s.seen[m.id+"@line"] > 0 {
			continue
		}
		if m.re.MatchString(l) {
			s.record(m.id, l)
		}
	}
	delete(s.seen, "press_x@line")
	delete(s.seen, "login@line")
	s.line = s.line[:0]
}

// CGaps returns the gaps between the last received 'C' bytes (BootROM receiver cadence).
func (s *Session) CGaps() []int64 {
	var out []int64
	for i := 1; i < len(s.cTimes); i++ {
		out = append(out, s.cTimes[i].Sub(s.cTimes[i-1]).Milliseconds())
	}
	return out
}

// Read reads once from the port, logs, echoes and scans what arrived.
func (s *Session) Read(timeout time.Duration) ([]byte, error) {
	buf := make([]byte, 4096)
	n, err := s.Port.Read(buf, timeout)
	if n > 0 {
		d := append([]byte(nil), buf[:n]...)
		s.mu.Lock()
		_, _ = s.uart.Write(d)
		if s.inCmd == 0 {
			_, _ = s.boot.Write(d)
		}
		s.mu.Unlock()
		if !s.mute {
			_, _ = s.Out.Write(d)
		}
		s.rx += int64(n)
		s.tail = append(s.tail, d...)
		if len(s.tail) > 65536 {
			s.tail = s.tail[len(s.tail)-65536:]
		}
		if s.inCmd == 0 {
			s.scan(d)
		}
		return d, err
	}
	return nil, err
}

// Drain reads until the line has been quiet for `quiet` or `max` passed.
func (s *Session) Drain(quiet, max time.Duration) ([]byte, error) {
	end := s.now().Add(max)
	qd := s.now().Add(quiet)
	var out []byte
	for s.now().Before(end) {
		d, err := s.Read(s.T.Poll)
		if err != nil {
			return out, err
		}
		if len(d) > 0 {
			out = append(out, d...)
			qd = s.now().Add(quiet)
		} else if s.now().After(qd) {
			break
		}
	}
	return out, nil
}

// sendKeys writes raw control bytes (interrupt keys, Enter). They are never
// commands, and they are logged.
func (s *Session) sendKeys(label string, b []byte) error {
	s.mu.Lock()
	fmt.Fprintf(s.uart, "\n[URSIDO %s] >> %s\n", s.now().Format(time.RFC3339), label)
	s.mu.Unlock()
	return s.Port.Write(b)
}

// sendLine types a line slowly enough for bootloaders without FIFOs. Callers
// must already have passed the text through the guard.
func (s *Session) sendLine(line string) error {
	if strings.ContainsAny(line, "\r\n") || line == "" {
		return errors.New("invalid line")
	}
	b := []byte(line)
	for len(b) > 0 {
		n := 16
		if len(b) < n {
			n = len(b)
		}
		if err := s.Port.Write(b[:n]); err != nil {
			return err
		}
		b = b[n:]
		time.Sleep(3 * time.Millisecond)
	}
	return s.Port.Write([]byte{'\r'})
}

// command marks the span of a command exchange so its response stays out of
// uart/boot-console.log (a DTB hex dump must not look like boot messages).
func (s *Session) command() func() {
	s.inCmd++
	return func() { s.inCmd-- }
}

func (s *Session) block(err error) {
	s.Blocked = append(s.Blocked, err.Error())
	s.Info("[SAFETY] %v", err)
	s.transcript(TranscriptEntry{Transport: "guard", Command: blockedCommand(err), Result: "blocked: " + err.Error()})
}

func blockedCommand(err error) string {
	var be *BlockedError
	if errors.As(err, &be) {
		return be.Command
	}
	return ""
}

func (s *Session) transcript(e TranscriptEntry) {
	if e.Time == "" {
		e.Time = s.now().Format(time.RFC3339Nano)
	}
	if len(e.Response) > 65536 {
		e.Response = e.Response[:65536] + "\n[truncated; full output in file]"
	}
	b, _ := json.Marshal(e)
	s.mu.Lock()
	_, _ = s.trans.Write(append(b, '\n'))
	s.mu.Unlock()
}

// BinaryRatio is the share of non-text bytes received; high values hint at a
// baud-rate mismatch.
func (s *Session) BinaryRatio() float64 {
	if s.rx == 0 {
		return 0
	}
	return float64(s.binary) / float64(s.rx)
}

// WriteFile stores a collected artifact relative to the session directory.
func (s *Session) WriteFile(rel string, data []byte) error {
	p := filepath.Join(s.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// lastLine returns the last non-empty line of b with CR and trailing spaces removed.
func lastLine(b []byte) string {
	s := strings.ReplaceAll(string(b), "\r", "")
	s = strings.TrimRight(s, " \t\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(stripANSI(s))
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// bulky reports whether a command prints a large hex dump that should not be
// echoed to the operator console (it is still logged in full).
func bulky(cmd string) bool {
	return strings.HasPrefix(cmd, "md") || strings.Contains(cmd, " dump ") || strings.Contains(cmd, "| ") ||
		strings.HasPrefix(cmd, "od ") || strings.HasPrefix(cmd, "hexdump ") || strings.HasPrefix(cmd, "xxd ") || cmd == "dmesg"
}
