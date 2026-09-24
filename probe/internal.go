package probe

import (
	"errors"
	"fmt"
	"regexp"
)

// The probe writes a line to a device in exactly two ways:
//
//   - sendCommand: a command that passed the guard (CheckUBoot, CheckUBootAttach
//     or CheckLinux); it is validated again right before it is written;
//   - sendInternal: one of the fixed probe templates below, which carry
//     markers so output and return codes can be found.
//
// Raw keys (Ctrl-C, Esc, Enter, BootROM 'x', --stop-key, login answers) go
// through sendKeys and are never lines. writeLine is private to this file;
// TestOnlyInternalWritesLines keeps it that way.

var (
	tmplHush      = regexp.MustCompile(`^echo URSIDO_HUSH_\$\?$`)
	tmplRC        = regexp.MustCompile(`^echo URSIDO_[0-9]{1,6}_RC_\$\?$`)
	tmplLinuxWrap = regexp.MustCompile(`^echo URSIDO_B_([0-9]{1,6}); (.+) 2>&1; echo URSIDO_E_([0-9]{1,6})_\$\?$`)
)

func internalHush() string    { return "echo URSIDO_HUSH_$?" }
func internalRC(n int) string { return fmt.Sprintf("echo URSIDO_%d_RC_$?", n) }
func internalLinux(n int, canon string) string {
	return fmt.Sprintf("echo URSIDO_B_%d; %s 2>&1; echo URSIDO_E_%d_$?", n, canon, n)
}

// checkInternal accepts only the fixed templates; the command inside the
// Linux wrapper must itself pass CheckLinux unchanged.
func checkInternal(line string) error {
	if tmplHush.MatchString(line) || tmplRC.MatchString(line) {
		return nil
	}
	if m := tmplLinuxWrap.FindStringSubmatch(line); m != nil {
		if m[1] != m[3] {
			return errors.New("wrapper markers differ")
		}
		canon, err := CheckLinux(m[2])
		if err != nil {
			return err
		}
		if canon != m[2] {
			return errors.New("wrapped command is not canonical")
		}
		return nil
	}
	return &BlockedError{Target: "internal", Command: line, Reason: "not a fixed probe template"}
}

// sendCommand writes a guard-approved command; check is the guard it came from.
func (s *Session) sendCommand(canon string, check func(string) (string, error)) error {
	again, err := check(canon)
	if err != nil {
		return err
	}
	if again != canon {
		return &BlockedError{Target: "guard", Command: canon, Reason: "command is not in canonical form"}
	}
	return s.writeLine(canon)
}

// sendInternal writes one of the fixed probe templates.
func (s *Session) sendInternal(line string) error {
	if err := checkInternal(line); err != nil {
		return err
	}
	return s.writeLine(line)
}
