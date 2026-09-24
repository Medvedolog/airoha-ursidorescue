// Package probe is the read-only hardware discovery ("Porting Collector") of
// UrsidoRescue. Nothing in this package may change flash contents: every
// command it sends to a bootloader or a Linux shell passes through the
// allowlist in this file, and the package does not import any recovery code.
package probe

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrBlocked marks a command the probe-mode guard refused to send.
var ErrBlocked = errors.New("safety violation blocked")

// BlockedError says which command was refused and why.
type BlockedError struct {
	Target  string
	Command string
	Reason  string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s: %s command %q blocked: %s", ErrBlocked, e.Target, e.Command, e.Reason)
}
func (e *BlockedError) Unwrap() error { return ErrBlocked }

// destructiveWords only improve the refusal message; the decision itself is
// made by the allowlist, so anything not listed there is refused as well.
var destructiveWords = []string{
	"erase", "write", "saveenv", "save", "remove", "create", "update", "scrub",
	"format", "resize", "protect", "lock", "unlock", "markbad", "torture",
	"reset", "boot", "run", "go", "setenv", "env", "load", "flash", "upgrade",
	"program", "cp", "mw", "nm", "mm", "rm", "mv", "dd", "tee",
}

func blocked(target, cmd, reason string) error {
	return &BlockedError{Target: target, Command: cmd, Reason: reason}
}

var (
	hexArg      = regexp.MustCompile(`^(?:0x)?[0-9a-fA-F]{1,16}$`)
	decArg      = regexp.MustCompile(`^[0-9]{1,12}$`)
	mtdNameArg  = regexp.MustCompile(`^[A-Za-z0-9_.@:+-]{1,64}$`)
	volNameArg  = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,127}$`)
	helpArg     = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,31}$`)
	envNameArg  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	fdtPathArg  = regexp.MustCompile(`^/[A-Za-z0-9_,.@/+-]{0,255}$`)
	ifaceArg    = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,15}$`)
	ubootChars  = regexp.MustCompile(`^[A-Za-z0-9_./:,@+ -]*$`)
	linuxChars  = regexp.MustCompile(`^[A-Za-z0-9_./:,@+=*| -]*$`)
	linuxPath   = regexp.MustCompile(`^/(?:proc|sys|etc|tmp/sysinfo)(?:/[A-Za-z0-9_.,:@+*-]+)*/?$`)
	mtdCharDev  = regexp.MustCompile(`^/dev/mtd[0-9]{1,2}$`)
	maxMDCount  = uint64(0x80000)
	maxRAMRange = uint64(0x04000000)
)

func parseHexArg(s string) (uint64, bool) {
	if !hexArg.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 64)
	return v, err == nil
}

func allHex(args []string) bool {
	for _, a := range args {
		if _, ok := parseHexArg(a); !ok {
			return false
		}
	}
	return true
}

// ubootRule validates the arguments after the command name.
type ubootRule func(args []string) string

func noArgs(args []string) string {
	if len(args) != 0 {
		return "no arguments allowed"
	}
	return ""
}

// ubootAllow is keyed by the exact, full command name. U-Boot also accepts
// unique abbreviations ("sa" for saveenv, "mtd wr" for mtd write), so only
// full names are ever matched and case is never folded.
var ubootAllow = map[string]ubootRule{
	"version": noArgs,
	"bdinfo":  noArgs,
	"coninfo": noArgs,
	"help": func(a []string) string {
		if len(a) > 1 || (len(a) == 1 && !helpArg.MatchString(a[0])) {
			return "help takes at most one command name"
		}
		return ""
	},
	"printenv": func(a []string) string {
		for _, x := range a {
			if !envNameArg.MatchString(x) {
				return "bad variable name"
			}
		}
		return ""
	},
	"md":   mdRule,
	"md.b": mdRule,
	"md.w": mdRule,
	"md.l": mdRule,
	"mtd": func(a []string) string {
		if len(a) == 0 {
			return "mtd needs a subcommand"
		}
		switch a[0] {
		case "list":
			return noArgs(a[1:])
		case "bad":
			if len(a) != 2 || !mtdNameArg.MatchString(a[1]) {
				return "mtd bad <name>"
			}
			return ""
		case "dump":
			if len(a) < 2 || len(a) > 4 || !mtdNameArg.MatchString(a[1]) || !allHex(a[2:]) {
				return "mtd dump <name> [<off> [<size>]]"
			}
			if len(a) == 4 {
				if n, _ := parseHexArg(a[3]); n == 0 || n > maxMDCount {
					return "mtd dump size out of range"
				}
			}
			return ""
		case "read":
			if len(a) != 5 || !mtdNameArg.MatchString(a[1]) || !allHex(a[2:]) {
				return "mtd read <name> <addr> <off> <size>"
			}
			if n, _ := parseHexArg(a[4]); n == 0 || n > maxRAMRange {
				return "mtd read size out of range"
			}
			return ""
		}
		return "mtd subcommand not allowlisted"
	},
	"ubi": func(a []string) string {
		if len(a) == 0 {
			return "ubi needs a subcommand"
		}
		switch a[0] {
		case "part":
			// A VID-header offset argument would change how UBI is attached.
			if len(a) != 2 || !mtdNameArg.MatchString(a[1]) {
				return "ubi part <mtd-name>"
			}
			return ""
		case "info":
			if len(a) == 1 || (len(a) == 2 && a[1] == "layout") {
				return ""
			}
			return "ubi info [layout]"
		case "list":
			return noArgs(a[1:])
		case "check":
			if len(a) != 2 || !volNameArg.MatchString(a[1]) {
				return "ubi check <volume>"
			}
			return ""
		case "read":
			if len(a) != 4 || !hexArg.MatchString(a[1]) || !volNameArg.MatchString(a[2]) || !hexArg.MatchString(a[3]) {
				return "ubi read <addr> <volume> <size>"
			}
			if n, _ := parseHexArg(a[3]); n == 0 || n > maxRAMRange {
				return "ubi read size out of range"
			}
			return ""
		}
		return "ubi subcommand not allowlisted"
	},
	"fdt": func(a []string) string {
		if len(a) == 0 {
			return "fdt needs a subcommand"
		}
		switch a[0] {
		case "addr":
			if len(a) == 1 || (len(a) == 2 && hexArg.MatchString(a[1])) {
				return ""
			}
			return "fdt addr [<addr>]"
		case "header":
			return noArgs(a[1:])
		case "print", "list":
			if len(a) == 1 || (len(a) == 2 && fdtPathArg.MatchString(a[1])) {
				return ""
			}
			return "fdt print|list [<path>]"
		}
		return "fdt subcommand not allowlisted"
	},
	"nand": func(a []string) string {
		if len(a) == 1 && (a[0] == "info" || a[0] == "bad") {
			return ""
		}
		return "nand info|bad only"
	},
	"spi-nand": func(a []string) string {
		if len(a) == 1 && a[0] == "info" {
			return ""
		}
		return "spi-nand info only"
	},
	"sf": func(a []string) string {
		if len(a) == 1 && a[0] == "probe" {
			return ""
		}
		return "sf probe only"
	},
	"mmc": func(a []string) string {
		if len(a) == 1 && (a[0] == "info" || a[0] == "list") {
			return ""
		}
		return "mmc info|list only"
	},
	"mii": func(a []string) string {
		if len(a) == 1 && (a[0] == "info" || a[0] == "device") {
			return ""
		}
		return "mii info|device only"
	},
	"mdio": func(a []string) string {
		if len(a) == 1 && a[0] == "list" {
			return ""
		}
		return "mdio list only"
	},
	"dm": func(a []string) string {
		if len(a) == 1 && (a[0] == "tree" || a[0] == "uclass") {
			return ""
		}
		return "dm tree|uclass only"
	},
	"gpio": func(a []string) string {
		if len(a) >= 1 && a[0] == "status" && (len(a) == 1 || (len(a) == 2 && a[1] == "-a")) {
			return ""
		}
		return "gpio status [-a] only"
	},
	"pinmux": func(a []string) string {
		if len(a) >= 1 && a[0] == "status" && (len(a) == 1 || (len(a) == 2 && a[1] == "-a")) {
			return ""
		}
		return "pinmux status [-a] only"
	},
	"clk": func(a []string) string {
		if len(a) == 1 && a[0] == "dump" {
			return ""
		}
		return "clk dump only"
	},
	// crc32 with a third argument stores the result to memory.
	"crc32": func(a []string) string {
		if len(a) != 2 || !allHex(a) {
			return "crc32 <addr> <len> only"
		}
		return ""
	},
	// hash with a third argument (or -v) stores or compares against memory.
	"hash": func(a []string) string {
		if len(a) != 3 || a[0] != "sha256" || !allHex(a[1:]) {
			return "hash sha256 <addr> <len> only"
		}
		return ""
	},
}

func mdRule(a []string) string {
	if len(a) != 2 || !allHex(a) {
		return "md <addr> <count>"
	}
	if n, _ := parseHexArg(a[1]); n == 0 || n > maxMDCount {
		return "md count out of range"
	}
	return ""
}

// CheckUBoot validates one U-Boot command line for probe mode and returns the
// canonical form (single spaces) that is the only thing ever sent.
func CheckUBoot(cmd string) (string, error) {
	const target = "U-Boot"
	if strings.ContainsAny(cmd, "\r\n\t") {
		return "", blocked(target, cmd, "control characters or newline")
	}
	if !ubootChars.MatchString(cmd) {
		return "", blocked(target, cmd, "shell metacharacters (; & | $ quotes # etc.) are not allowed")
	}
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return "", blocked(target, cmd, "empty command")
	}
	rule, ok := ubootAllow[f[0]]
	if !ok {
		return "", blocked(target, cmd, reasonFor(f[0]))
	}
	if r := rule(f[1:]); r != "" {
		return "", blocked(target, cmd, r)
	}
	return strings.Join(f, " "), nil
}

func reasonFor(name string) string {
	low := strings.ToLower(name)
	for _, w := range destructiveWords {
		if low == w || (len(low) >= 2 && strings.HasPrefix(w, low)) || strings.Contains(low, w) {
			return "not allowlisted (possible destructive command or abbreviation of " + w + ")"
		}
	}
	if low != name {
		return "not allowlisted (commands are matched case-sensitively by full name)"
	}
	return "not allowlisted"
}

type linuxRule func(args []string, inPipe bool) string

func pathArgs(a []string, min int) string {
	if len(a) < min {
		return "path argument required"
	}
	for _, x := range a {
		if strings.Contains(x, "..") || !linuxPath.MatchString(x) {
			return "path outside /proc, /sys, /etc, /tmp/sysinfo"
		}
	}
	return ""
}

func hexTool(fixed []string) linuxRule {
	return func(a []string, inPipe bool) string {
		if len(a) < len(fixed) {
			return "fixed options required"
		}
		for i, x := range fixed {
			if a[i] != x {
				return "fixed options required: " + strings.Join(fixed, " ")
			}
		}
		rest := a[len(fixed):]
		if inPipe {
			if len(rest) != 0 {
				return "no file argument inside a pipe"
			}
			return ""
		}
		if len(rest) != 1 {
			return "exactly one file"
		}
		return pathArgs(rest, 1)
	}
}

var linuxAllow = map[string]linuxRule{
	"uname": func(a []string, _ bool) string {
		if len(a) == 1 && a[0] == "-a" {
			return ""
		}
		return "uname -a only"
	},
	"cat": func(a []string, _ bool) string { return pathArgs(a, 1) },
	"ls": func(a []string, _ bool) string {
		if len(a) > 0 && a[0] == "-l" {
			a = a[1:]
		}
		return pathArgs(a, 1)
	},
	"grep": func(a []string, _ bool) string {
		if len(a) < 3 || a[0] != "-H" || a[1] != "." {
			return "grep -H . <paths> only"
		}
		return pathArgs(a[2:], 1)
	},
	"dmesg":       func(a []string, _ bool) string { return linuxNoArgs(a) },
	"mount":       func(a []string, _ bool) string { return linuxNoArgs(a) },
	"lsblk":       func(a []string, _ bool) string { return linuxNoArgs(a) },
	"fw_printenv": func(a []string, _ bool) string { return linuxNoArgs(a) },
	"df": func(a []string, _ bool) string {
		if len(a) == 0 || (len(a) == 1 && a[0] == "-h") {
			return ""
		}
		return "df [-h] only"
	},
	"ip": func(a []string, _ bool) string {
		if len(a) == 1 && (a[0] == "link" || a[0] == "addr") {
			return ""
		}
		return "ip link|addr only"
	},
	"ubinfo": func(a []string, _ bool) string {
		if len(a) == 1 && a[0] == "-a" {
			return ""
		}
		return "ubinfo -a only"
	},
	"ethtool": func(a []string, _ bool) string {
		if (len(a) == 1 && ifaceArg.MatchString(a[0]) && !strings.HasPrefix(a[0], "-")) ||
			(len(a) == 2 && a[0] == "-i" && ifaceArg.MatchString(a[1]) && !strings.HasPrefix(a[1], "-")) {
			return ""
		}
		return "ethtool <if> | ethtool -i <if> only"
	},
	"dd": func(a []string, inPipe bool) string {
		seen := map[string]bool{}
		for _, x := range a {
			k, v, ok := strings.Cut(x, "=")
			if !ok || seen[k] {
				return "dd takes key=value operands once each"
			}
			seen[k] = true
			switch k {
			case "if":
				if !mtdCharDev.MatchString(v) {
					return "dd if= must be /dev/mtdN"
				}
			case "bs", "count", "skip":
				if !decArg.MatchString(v) {
					return "dd bs/count/skip must be decimal"
				}
			default:
				return "dd operand " + k + " not allowed"
			}
		}
		if !seen["if"] || !seen["count"] {
			return "dd needs if= and count="
		}
		return ""
	},
	"od":      hexTool([]string{"-An", "-v", "-tx1"}),
	"hexdump": hexTool([]string{"-C"}),
	"xxd":     hexTool([]string{"-p"}),
}

func linuxNoArgs(a []string) string {
	if len(a) != 0 {
		return "no arguments allowed"
	}
	return ""
}

// CheckLinux validates one Linux shell command for probe mode. The only
// allowed composition is "dd ... | od/hexdump/xxd" to read a flash sample.
func CheckLinux(cmd string) (string, error) {
	const target = "Linux"
	if strings.ContainsAny(cmd, "\r\n\t") {
		return "", blocked(target, cmd, "control characters or newline")
	}
	if !linuxChars.MatchString(cmd) {
		return "", blocked(target, cmd, "shell metacharacters (; & $ > < quotes etc.) are not allowed")
	}
	if strings.Contains(cmd, "||") {
		return "", blocked(target, cmd, "|| is not allowed")
	}
	segs := strings.Split(cmd, "|")
	if len(segs) > 2 {
		return "", blocked(target, cmd, "at most one pipe")
	}
	var canon []string
	for i, seg := range segs {
		f := strings.Fields(seg)
		if len(f) == 0 {
			return "", blocked(target, cmd, "empty pipeline segment")
		}
		rule, ok := linuxAllow[f[0]]
		if !ok {
			return "", blocked(target, cmd, reasonFor(f[0]))
		}
		if len(segs) == 2 {
			if i == 0 && f[0] != "dd" {
				return "", blocked(target, cmd, "only dd may feed a pipe")
			}
			if i == 1 && f[0] != "od" && f[0] != "hexdump" && f[0] != "xxd" {
				return "", blocked(target, cmd, "only od/hexdump/xxd may read a pipe")
			}
		} else if f[0] == "dd" {
			return "", blocked(target, cmd, "dd must be piped into a hex dumper")
		}
		if r := rule(f[1:], len(segs) == 2); r != "" {
			return "", blocked(target, cmd, r)
		}
		canon = append(canon, strings.Join(f, " "))
	}
	return strings.Join(canon, " | "), nil
}
