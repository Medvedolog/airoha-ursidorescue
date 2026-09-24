package probe

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PromptKind classifies the last line the device printed.
type PromptKind int

const (
	PromptNone PromptKind = iota
	PromptUBoot
	PromptOtherBootloader
	PromptLinuxShell
	PromptLogin
	PromptPassword
)

func (k PromptKind) String() string {
	return [...]string{"none", "u-boot", "other-bootloader", "linux-shell", "login", "password"}[k]
}

var (
	ubootPromptRE = regexp.MustCompile(`^(?:[A-Za-z0-9_.-]{1,32} ?>|=>)$`)
	otherBootRE   = regexp.MustCompile(`(?i)^(?:bldr|tcboot|cfe|redboot|brom)\s*>$`)
	shellPromptRE = regexp.MustCompile(`^(?:\S.{0,80})?[#$]$`)
	loginRE       = regexp.MustCompile(`(?i)\blogin:$`)
	passwordRE    = regexp.MustCompile(`(?i)password:$`)
)

// ClassifyPrompt looks only at the shape of the last line.
func ClassifyPrompt(line string) PromptKind {
	l := strings.TrimSpace(line)
	switch {
	case l == "":
		return PromptNone
	case loginRE.MatchString(l):
		return PromptLogin
	case passwordRE.MatchString(l):
		return PromptPassword
	case otherBootRE.MatchString(l):
		return PromptOtherBootloader
	case ubootPromptRE.MatchString(l):
		return PromptUBoot
	case shellPromptRE.MatchString(l) && len(l) <= 96:
		return PromptLinuxShell
	}
	return PromptNone
}

// UBoot drives a U-Boot prompt. Exec is the only way to send a command and it
// always goes through CheckUBoot first.
type UBoot struct {
	s      *Session
	Prompt string
	Hush   bool
	Source string // "device" or "ursido-ram-uboot"
	// AllowUBIAttach permits "ubi part" (advanced, not read-only mode).
	AllowUBIAttach bool
	seq            int
}

func (u *UBoot) check(cmd string) (string, error) {
	if u.AllowUBIAttach {
		return CheckUBootAttach(cmd)
	}
	return CheckUBoot(cmd)
}

// NewUBoot wraps a session already sitting at a U-Boot prompt.
func NewUBoot(s *Session, prompt, source string) *UBoot {
	return &UBoot{s: s, Prompt: prompt, Source: source}
}

func (u *UBoot) atPrompt(b []byte) bool {
	return lastLine(b) == u.Prompt
}

func (u *UBoot) readUntilPrompt(timeout time.Duration) ([]byte, error) {
	end := u.s.now().Add(timeout)
	var out []byte
	for u.s.now().Before(end) {
		d, err := u.s.Read(u.s.T.Poll)
		if err != nil {
			return out, err
		}
		if len(d) == 0 {
			continue
		}
		out = append(out, d...)
		low := strings.ToLower(string(out))
		if strings.Contains(low, "starting kernel") || strings.Contains(low, "booting linux on physical cpu") {
			return out, errors.New("device left U-Boot and started Linux")
		}
		if u.atPrompt(out) {
			more, _ := u.s.Drain(u.s.T.Quiet/2, u.s.T.Quiet*2)
			out = append(out, more...)
			if u.atPrompt(out) {
				return out, nil
			}
		}
	}
	return out, fmt.Errorf("U-Boot prompt %q not seen within %s", u.Prompt, timeout)
}

// DetectHush checks whether $? is expanded, so return codes can be read.
func (u *UBoot) DetectHush() {
	_, _ = u.s.Drain(u.s.T.Quiet/2, u.s.T.Quiet*3)
	defer u.s.command()()
	if err := u.s.sendInternal(internalHush()); err != nil {
		return
	}
	out, _ := u.readUntilPrompt(u.s.T.CommandDef)
	u.Hush = regexp.MustCompile(`URSIDO_HUSH_[0-9]+`).Match(out)
	u.s.transcript(TranscriptEntry{Transport: "u-boot/internal", Command: internalHush(), Response: string(out), Result: fmt.Sprintf("hush=%v", u.Hush)})
}

// Exec runs one allowlisted command and returns its output without the
// echoed command line and the trailing prompt; rc is -1 if unknown.
func (u *UBoot) Exec(cmd string, timeout time.Duration) (string, int, error) {
	canon, err := u.check(cmd)
	if err != nil {
		u.s.block(err)
		return "", -1, err
	}
	if timeout == 0 {
		timeout = u.s.T.CommandDef
	}
	started := u.s.now()
	_, _ = u.s.Drain(u.s.T.Quiet/2, u.s.T.Quiet*3)
	defer u.s.command()()
	if bulky(canon) {
		fmt.Fprintf(u.s.Out, L("\n[probe] %s … (вывод пишется в лог)\n", "\n[probe] %s … (output goes to the log)\n"), canon)
		u.s.mute = true
		defer func() { u.s.mute = false }()
	}
	if err = u.s.sendCommand(canon, u.check); err != nil {
		return "", -1, err
	}
	raw, err := u.readUntilPrompt(timeout)
	body := cleanOutput(raw, canon, u.Prompt)
	rc := -1
	if err == nil && u.Hush {
		u.seq++
		marker := fmt.Sprintf("URSIDO_%d_RC", u.seq)
		if e := u.s.sendInternal(internalRC(u.seq)); e == nil {
			st, e2 := u.readUntilPrompt(u.s.T.CommandDef)
			if m := regexp.MustCompile(marker + `_([0-9]+)`).FindSubmatch(st); e2 == nil && len(m) == 2 {
				rc, _ = strconv.Atoi(string(m[1]))
			}
		}
	}
	if err == nil && rc == -1 && looksFailed(body) {
		rc = 1
	}
	res := "ok"
	if err != nil {
		res = "error: " + err.Error()
	} else if rc > 0 {
		res = fmt.Sprintf("failed rc=%d", rc)
	}
	var rcp *int
	if rc >= 0 {
		rcp = &rc
	}
	u.s.transcript(TranscriptEntry{Transport: "u-boot", Command: canon, Response: body, Result: res, RC: rcp, Duration: u.s.now().Sub(started).Milliseconds()})
	return body, rc, err
}

var failRE = regexp.MustCompile(`(?i)unknown command|usage:|command '.*' failed|not supported|no such|error:|failed to`)

func looksFailed(body string) bool {
	head := body
	if len(head) > 400 {
		head = head[:400]
	}
	return failRE.MatchString(head)
}

// cleanOutput removes the echoed command and the final prompt line.
func cleanOutput(raw []byte, cmd, prompt string) string {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(s, "\n")
	start := 0
	for i, l := range lines {
		if strings.Contains(l, cmd) {
			start = i + 1
			break
		}
		if i > 3 {
			break
		}
	}
	lines = lines[start:]
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" || (prompt != "" && last == prompt) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n") + "\n"
}

// Linux drives a Linux shell. Exec wraps each allowlisted command between
// begin/end markers so that output is found even among kernel messages.
type Linux struct {
	s   *Session
	seq int
}

// NewLinux wraps a session sitting at a Linux shell prompt.
func NewLinux(s *Session) *Linux { return &Linux{s: s} }

// Exec runs one allowlisted command; rc is the shell's exit status.
func (l *Linux) Exec(cmd string, timeout time.Duration) (string, int, error) {
	canon, err := CheckLinux(cmd)
	if err != nil {
		l.s.block(err)
		return "", -1, err
	}
	if timeout == 0 {
		timeout = l.s.T.CommandDef
	}
	l.seq++
	b := fmt.Sprintf("URSIDO_B_%d", l.seq)
	e := fmt.Sprintf("URSIDO_E_%d", l.seq)
	if _, err = CheckLinux(canon); err != nil {
		l.s.block(err)
		return "", -1, err
	}
	started := l.s.now()
	_, _ = l.s.Drain(l.s.T.Quiet/2, l.s.T.Quiet*3)
	defer l.s.command()()
	if bulky(canon) {
		fmt.Fprintf(l.s.Out, L("\n[probe] %s … (вывод пишется в лог)\n", "\n[probe] %s … (output goes to the log)\n"), canon)
		l.s.mute = true
		defer func() { l.s.mute = false }()
	}
	// The wrapper is a fixed template around an already-validated command.
	if err = l.s.sendInternal(internalLinux(l.seq, canon)); err != nil {
		return "", -1, err
	}
	endRE := regexp.MustCompile(e + `_([0-9]+)`)
	beginRE := regexp.MustCompile(`(?m)^` + b + `\r?$`)
	deadline := l.s.now().Add(timeout)
	var out []byte
	rc := -1
	for l.s.now().Before(deadline) {
		d, rerr := l.s.Read(l.s.T.Poll)
		if rerr != nil {
			err = rerr
			break
		}
		out = append(out, d...)
		if m := endRE.FindSubmatchIndex(out); m != nil {
			rc, _ = strconv.Atoi(string(out[m[2]:m[3]]))
			body := out[:m[0]]
			if bi := beginRE.FindIndex(body); bi != nil {
				body = body[bi[1]:]
			}
			text := strings.TrimLeft(strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\r", ""), "\n")
			_, _ = l.s.Drain(l.s.T.Quiet/2, l.s.T.Quiet*2)
			rcv := rc
			res := "ok"
			if rc != 0 {
				res = fmt.Sprintf("failed rc=%d", rc)
			}
			l.s.transcript(TranscriptEntry{Transport: "linux", Command: canon, Response: text, Result: res, RC: &rcv, Duration: l.s.now().Sub(started).Milliseconds()})
			return text, rc, nil
		}
	}
	if err == nil {
		err = fmt.Errorf("linux command timeout: %s", canon)
	}
	l.s.transcript(TranscriptEntry{Transport: "linux", Command: canon, Response: string(out), Result: "error: " + err.Error(), Duration: l.s.now().Sub(started).Milliseconds()})
	return string(out), -1, err
}
