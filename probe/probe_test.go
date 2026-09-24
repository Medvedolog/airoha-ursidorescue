package probe

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var fastTiming = Timing{
	Poll:          5 * time.Millisecond,
	Quiet:         25 * time.Millisecond,
	PromptSettle:  25 * time.Millisecond,
	IdleKick:      400 * time.Millisecond,
	LinuxSettle:   250 * time.Millisecond,
	CommandDef:    2 * time.Second,
	CommandLong:   5 * time.Second,
	InterruptGap:  10 * time.Millisecond,
	HandshakeWait: 2 * time.Second,
}

// ---------------- §34 safety ----------------

func TestGuardBlocksDestructiveUBoot(t *testing.T) {
	bad := []string{
		"erase", "nand erase", "nand erase.chip", "mtd erase ubi", "mtd write bl2 0x90000000 0 0x20000",
		"mtd wr bl2 0x90000000", "saveenv", "sa", "SAVEENV", "SaveEnv", "env save", "env s",
		"ubi remove fip", "ubi create fip 0x1000", "ubi write 0x90000000 fip 0x100", "ubi rem fip",
		"sf erase 0 0x1000", "sf write 0x90000000 0 0x10", "sf update 0x90000000 0 0x10",
		"nand write 0x90000000 0 0x800", "  saveenv", "version; saveenv", "version && saveenv",
		"version\nsaveenv", "version\rsaveenv", "version\tsaveenv", "printenv || saveenv", "run bootcmd",
		"setenv bootdelay 0", "reset", "boot", "bootm", "go 0x90000000", "mw.b 0x90000000 0 0x10",
		"md.b $loadaddr 0x10", "echo `saveenv`", "printenv 'x'", "mtd dump.raw ubi", "mtd read.raw ubi 0x90000000 0 0x800",
		"ubi part ubi 2048", "crc32 0x90000000 0x100 0x80000000", "hash sha256 0x90000000 0x100 *0x80000000",
		"hash -v sha256 0x90000000 0x100 abc", "MTD LIST", "Mtd list", "mtd  write  bl2", "fdt set /chosen x y",
		"fdt resize", "mii write 0 0 0", "gpio set 5", "gpio toggle 5", "loadx", "tftpboot 0x90000000 x",
		"mtd read bl2 0x90000000 0x0 0x10000000", "md.b 0x90000000 0x100000",
	}
	for _, c := range bad {
		if canon, err := CheckUBoot(c); err == nil || !errors.Is(err, ErrBlocked) {
			t.Errorf("U-Boot %q was allowed as %q", c, canon)
		}
	}
}

func TestGuardAllowsReadOnlyUBoot(t *testing.T) {
	good := map[string]string{
		"version":                             "version",
		"  version  ":                         "version",
		"mtd   list":                          "mtd list",
		"help mtd":                            "help mtd",
		"printenv":                            "printenv",
		"bdinfo":                              "bdinfo",
		"ubi info layout":                     "ubi info layout",
		"mtd dump ubi 0x0 0x1000":             "mtd dump ubi 0x0 0x1000",
		"md.b 0x9de7aa30 0x40":                "md.b 0x9de7aa30 0x40",
		"fdt print /chosen":                   "fdt print /chosen",
		"hash sha256 0x90000000 0x20000":      "hash sha256 0x90000000 0x20000",
		"gpio status -a":                      "gpio status -a",
		"mtd read bl2 0x90000000 0x0 0x20000": "mtd read bl2 0x90000000 0x0 0x20000",
	}
	for in, want := range good {
		got, err := CheckUBoot(in)
		if err != nil || got != want {
			t.Errorf("CheckUBoot(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// Review item 1: attaching UBI may write (auto-resize, fastmap), so the strict
// guard refuses it and only the explicit attach mode allows it.
func TestGuardUBIAttachIsNotReadOnly(t *testing.T) {
	for _, c := range []string{"ubi part ubi", "ubi  part ubi", "ubi part ubi 2048"} {
		if _, err := CheckUBoot(c); err == nil {
			t.Errorf("strict guard allowed %q", c)
		}
	}
	if got, err := CheckUBootAttach("ubi  part ubi"); err != nil || got != "ubi part ubi" {
		t.Errorf("attach mode: %q %v", got, err)
	}
	if _, err := CheckUBootAttach("ubi write 0x90000000 fip 0x100"); err == nil {
		t.Error("attach mode allowed ubi write")
	}
}

// Review item 4: every line written to a device is either guard-approved or a
// fixed internal template; nothing else calls writeLine.
func TestInternalTemplates(t *testing.T) {
	good := []string{internalHush(), internalRC(7), internalLinux(3, "cat /proc/mtd"),
		internalLinux(4, "dd if=/dev/mtd1 bs=4096 count=1 | od -An -v -tx1")}
	for _, l := range good {
		if err := checkInternal(l); err != nil {
			t.Errorf("%q: %v", l, err)
		}
	}
	bad := []string{"echo URSIDO_HUSH_$?; reboot", "echo URSIDO_1_RC_$? && saveenv", "saveenv",
		"echo URSIDO_B_1; reboot 2>&1; echo URSIDO_E_1_$?", "echo URSIDO_B_1; cat /proc/mtd 2>&1; echo URSIDO_E_2_$?",
		"echo URSIDO_B_1; cat  /proc/mtd 2>&1; echo URSIDO_E_1_$?", "echo URSIDO_B_1; cat /proc/mtd; reboot 2>&1; echo URSIDO_E_1_$?"}
	for _, l := range bad {
		if err := checkInternal(l); err == nil {
			t.Errorf("internal template accepted %q", l)
		}
	}
}

func TestAuthLinesAreNarrow(t *testing.T) {
	for _, line := range []string{"exit", "su user_ftp", "su user-telnet"} {
		if err := checkAuthLine(line); err != nil {
			t.Errorf("auth line %q blocked: %v", line, err)
		}
	}
	for _, line := range []string{"su", "su root; reboot", "exit; reboot", "reboot", "su root root"} {
		if err := checkAuthLine(line); err == nil {
			t.Errorf("unsafe auth line %q allowed", line)
		}
	}
}

func TestOnlyInternalWritesLines(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "writeLine" && filepath.Base(name) != "internal.go" {
					t.Errorf("%s: writeLine called outside internal.go", fset.Position(call.Pos()))
				}
				return true
			})
		}
	}
}

func TestGuardLinux(t *testing.T) {
	bad := []string{
		"dd if=/dev/zero of=/dev/mtd0", "dd if=/dev/mtd0 of=/tmp/x bs=4096 count=1 | od -An -v -tx1",
		"mtd write x firmware", "mtd erase firmware", "flash_erase /dev/mtd0 0 0", "nandwrite /dev/mtd0 x",
		"ubirmvol /dev/ubi0 -N fip", "ubimkvol /dev/ubi0 -N x -s 1", "ubiupdatevol /dev/ubi0_0 x", "fw_setenv bootdelay 0",
		"sysupgrade x", "reboot", "cat /proc/mtd; reboot", "cat /proc/mtd && reboot", "cat /proc/mtd || true",
		"cat /proc/mtd > /dev/mtd0", "cat /dev/mtd0", "cat /proc/../dev/mtd0", "echo $(reboot)", "cat `x`",
		"uname -a\nreboot", "UNAME -a", "busybox reboot", "ethtool -s eth0 speed 10", "ethtool -E eth0",
		"ip link set eth0 down", "ip addr add 1.2.3.4/24 dev eth0", "cat /proc/mtd | sh", "dd if=/dev/mtd0 count=1",
		"od -An -v -tx1 /dev/mtd0", "dd if=/dev/mtd0 count=1 conv=notrunc | od -An -v -tx1", "grep -r x /",
		"ls /", "cat /proc/mtd | od -An -v -tx1 | sh",
	}
	for _, c := range bad {
		if canon, err := CheckLinux(c); err == nil || !errors.Is(err, ErrBlocked) {
			t.Errorf("Linux %q was allowed as %q", c, canon)
		}
	}
	good := []string{
		"id -u", "uname -a", "cat /proc/mtd", "dmesg", "ubinfo -a", "fw_printenv", "ip link", "ethtool -i eth0",
		"dd if=/dev/mtd3 bs=4096 count=1 skip=5 | od -An -v -tx1", "od -An -v -tx1 /sys/firmware/fdt",
		"grep -H . /sys/class/mtd/mtd*/name /sys/class/mtd/mtd*/type", "ls -l /sys/class/net", "cat /etc/board.json",
	}
	for _, c := range good {
		if _, err := CheckLinux(c); err != nil {
			t.Errorf("Linux %q blocked: %v", c, err)
		}
	}
}

// The probe package must not carry write primitives: no string literal in its
// non-test code may spell a destructive command, except the guard's word list.
func TestProbeHasNoDestructiveLiterals(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"mtd write", "mtd erase", "nand write", "nand erase", "ubi write", "ubi remove", "ubi create", "saveenv", "env save", "sf write", "sf erase", "sf update", "mw.", "flash_erase", "nandwrite", "fw_setenv", "sysupgrade", "reboot"}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "ursidorescue") {
					t.Errorf("%s imports %s", name, imp.Path.Value)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, _ := strconv.Unquote(lit.Value)
				low := strings.ToLower(strings.TrimSpace(s))
				// "saveenv" is only a capability name read from help output.
				if filepath.Base(name) == "guard.go" || low == "saveenv" {
					return true
				}
				for _, w := range forbidden {
					if strings.HasPrefix(low, w) || strings.Contains(low, "\n"+w) || strings.Contains(low, "; "+w) {
						t.Errorf("%s: literal %q starts a destructive command %q", fset.Position(lit.Pos()), s, w)
					}
				}
				return true
			})
		}
	}
}

// ---------------- §33 parsers ----------------

func TestParseHexDumpMDAndMTD(t *testing.T) {
	data := []byte("UBI#\x01\x00\x00\x00ab cd ef 12  34 \xff\x00")
	for len(data) < 40 {
		data = append(data, byte(len(data)))
	}
	got, err := ParseHexDump(mdText(0x90000000, data), 0x90000000, len(data))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("md.b: %v %x", err, got)
	}
	page := make([]byte, 4096)
	for i := range page {
		page[i] = byte(i)
	}
	got, err = ParseHexDump(mtdDumpText(0x1000, page), 0x1000, 4096)
	if err != nil || !bytes.Equal(got, page) {
		t.Fatalf("mtd dump: %v", err)
	}
	if _, err := ParseHexDump(mdText(0x90000000, data[:16]), 0x90000000, 32); err == nil {
		t.Fatal("short dump accepted")
	}
}

func TestParseLinuxHex(t *testing.T) {
	want := []byte{0xd0, 0x0d, 0xfe, 0xed, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	od := "1+0 records in\n" + odText(want) + "4096 bytes copied\n"
	if got := ParseLinuxHex(od); !bytes.Equal(got, want) {
		t.Fatalf("od: %x", got)
	}
	hc := "00000000  d0 0d fe ed 01 02 03 04  05 06 07 08 09 0a 0b 0c  |................|\n00000010  0d 0e                                             |..|\n00000012\n"
	if got := ParseLinuxHex(hc); !bytes.Equal(got, want) {
		t.Fatalf("hexdump -C: %x", got)
	}
	if got := ParseLinuxHex(hex.EncodeToString(want) + "\n"); !bytes.Equal(got, want) {
		t.Fatalf("xxd -p: %x", got)
	}
}

func TestParseUBootMTDList(t *testing.T) {
	d := newSim(false)
	devs := ParseUBootMTDList(d.uboot("mtd list"))
	if len(devs) != 1 || devs[0].Name != "spi-nand0" || devs[0].EraseSize != 0x20000 || devs[0].MinIO != 0x800 || devs[0].OOB != 128 || devs[0].End != 0x10000000 {
		t.Fatalf("%+v", devs)
	}
	if len(devs[0].Parts) != 2 || devs[0].Parts[1].Name != "ubi" || devs[0].Parts[1].Start != 0x20000 {
		t.Fatalf("parts %+v", devs[0].Parts)
	}
}

func TestParseProcMTDAndSysfs(t *testing.T) {
	d := newSim(false)
	out, _ := d.linuxCmd("cat /proc/mtd")
	pm := ParseProcMTD(out)
	if len(pm) != 2 || pm[1].Name != "ubi" || pm[1].Size != 0xffe0000 || pm[1].EraseSize != 0x20000 {
		t.Fatalf("%+v", pm)
	}
	out, _ = d.linuxCmd("grep -H . /sys/class/mtd/x")
	sys := ParseSysClassMTD(out)
	if sys[1]["offset"] != "131072" || sys[0]["writesize"] != "2048" {
		t.Fatalf("%v", sys)
	}
}

func TestParseUBI(t *testing.T) {
	d := newSim(false)
	u := ParseUBootUBIInfo(d.uboot("ubi info"), d.uboot("ubi info layout"))
	if u.PEBSize != 131072 || u.LEBSize != 126976 || u.VIDHdrOffset != 2048 || u.DataOffset != 4096 || len(u.Volumes) != 4 || u.Volumes[0].Name != "fip" || u.Volumes[0].Type != "dynamic" {
		t.Fatalf("u-boot: %+v", u)
	}
	ub, _ := d.linuxCmd("ubinfo -a")
	dm, _ := d.linuxCmd("dmesg")
	l, ok := ParseUbinfo(ub, dm)
	if !ok || l.LEBSize != 126976 || l.PEBSize != 131072 || l.SubPage != 2048 || l.VIDHdrOffset != 2048 || len(l.Volumes) != 4 || l.Volumes[3].Name != "rootfs" || l.MTD != "mtd1" {
		t.Fatalf("linux: %+v", l)
	}
}

func TestParsePrintenvBDInfoHelpMii(t *testing.T) {
	d := newSim(false)
	env := ParsePrintenv(d.uboot("printenv"))
	if env["loadaddr"] != "0x90000000" || env["serial#"] != "ABC123456" || env["fdtcontroladdr"] != "9de7aa30" {
		t.Fatalf("%v", env)
	}
	bd := ParseBDInfo(d.uboot("bdinfo"))
	if len(bd.DRAMBanks) != 1 || bd.DRAMBanks[0] != [2]uint64{0x80000000, 0x20000000} || bd.RelocAddr != 0x9fe9f000 {
		t.Fatalf("%+v", bd)
	}
	if a, ok := ramWindow(bd, env, 0, 4<<20); !ok || a != 0x90000000 {
		t.Fatalf("ram window %x %v", a, ok)
	}
	if _, ok := ramWindow(bd, map[string]string{"loadaddr": "9e000000"}, 0, 4<<20); ok {
		t.Fatal("address inside U-Boot's relocation margin accepted")
	}
	if _, ok := ramWindow(BDInfo{}, env, 0, 4<<20); ok {
		t.Fatal("window without DRAM banks accepted")
	}
	h := ParseHelp(d.uboot("help"))
	if _, ok := h["mtd"]; !ok || len(h) < 10 {
		t.Fatalf("help %v", h)
	}
	if p := ParseMiiInfo(d.uboot("mii info")); len(p) != 1 || p[0].Addr != 9 {
		t.Fatalf("mii %+v", p)
	}
}

func TestBootLogAndSoC(t *testing.T) {
	b := ParseBootLog("\r\nPress x\r\nCCC\r\nAN7583DRAMC V0.6\r\nNOTICE:  BL31: v2.12.0(release):x\r\nU-Boot 2026.07-OpenWrt (Sep 01)\r\nModel: Nokia XG-040G-MF\r\nspi-nand spi0.0: Fudan SPI NAND was found.\r\n")
	if !b.PressX || !b.XmodemC || len(b.SoCs) != 1 || b.SoCs[0] != "AN7583" || b.BL31 == "" || b.Model != "Nokia XG-040G-MF" || b.FlashVendor != "Fudan" || !strings.HasPrefix(b.UBoot, "U-Boot 2026.07") {
		t.Fatalf("%+v", b)
	}
	if normSoC("en7581") != "AN7581" || normSoC("AN7583") != "AN7583" {
		t.Fatal("normSoC")
	}
}

func TestPromptClassification(t *testing.T) {
	cases := map[string]PromptKind{"AN7581>": PromptUBoot, "=>": PromptUBoot, "U-Boot>": PromptUBoot, "root@OpenWrt:/#": PromptLinuxShell,
		"#": PromptLinuxShell, "$": PromptLinuxShell, "OpenWrt login:": PromptLogin, "Password:": PromptPassword, "bldr>": PromptOtherBootloader, "Hit any key": PromptNone}
	for in, want := range cases {
		if got := ClassifyPrompt(in); got != want {
			t.Errorf("%q: %v want %v", in, got, want)
		}
	}
}

type scriptPort struct {
	chunks [][]byte
	sent   []byte
}

func (p *scriptPort) Name() string { return "script" }
func (p *scriptPort) Write(b []byte) error {
	p.sent = append(p.sent, b...)
	if bytes.Contains(b, []byte("x")) {
		p.chunks = append(p.chunks, []byte("CCC"))
	}
	return nil
}
func (p *scriptPort) Read(b []byte, _ time.Duration) (int, error) {
	if len(p.chunks) == 0 {
		time.Sleep(time.Millisecond)
		return 0, nil
	}
	n := copy(b, p.chunks[0])
	p.chunks = p.chunks[1:]
	return n, nil
}

func TestBootROMDetectionAndHandshake(t *testing.T) {
	dir := t.TempDir()
	port := &scriptPort{chunks: [][]byte{[]byte("\r\nBROM\r\nPress x")}}
	s, err := NewSession(dir, port, nil, fastTiming)
	if err != nil {
		t.Fatal(err)
	}
	o := Collect(s, Options{Layers: Layers{BootROM: true}, BootROMHandshake: true, Timeout: 2 * time.Second, NoLinux: true, LinuxOnly: true})
	s.Close()
	if !o.BootROM || !o.BootROMXmodem || string(port.sent) != "x" {
		t.Fatalf("outcome %+v sent %q", o, port.sent)
	}
	var br map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "bootrom", "bootrom.json"))
	if json.Unmarshal(b, &br) != nil || br["xmodem"] != true || br["receiver_char"] != "C" || br["entry_sequence"] != "press-x" || br["flash_written"] != false {
		t.Fatalf("bootrom.json %s", b)
	}
	var ev []Event
	b, _ = os.ReadFile(filepath.Join(dir, "uart", "events.json"))
	_ = json.Unmarshal(b, &ev)
	var ids []string
	for _, e := range ev {
		ids = append(ids, e.Marker)
	}
	if !strings.Contains(strings.Join(ids, ","), "press_x") || !strings.Contains(strings.Join(ids, ","), "xmodem_c") {
		t.Fatalf("events %v", ids)
	}
}

func TestFDTParseAndDTS(t *testing.T) {
	blob := boardDTB("Test Board")
	f, err := ParseFDT(blob)
	if err != nil {
		t.Fatal(err)
	}
	m := ExtractDTMeta(f, "dt-test")
	if m.Model != "Test Board" || m.Compatible[1] != "airoha,an7581" || len(m.Partitions) != 2 || m.Partitions[1].Offset != 0x20000 || m.Partitions[1].Size != 0xffe0000 {
		t.Fatalf("%+v", m)
	}
	if len(m.Memory) != 1 || m.Memory[0][1] != 0x20000000 {
		t.Fatalf("memory %v", m.Memory)
	}
	if len(m.Buttons) != 1 || m.Buttons[0].GPIO == nil || !m.Buttons[0].GPIO.ActiveLow || *m.Buttons[0].Code != 0x198 {
		t.Fatalf("buttons %+v", m.Buttons)
	}
	if len(m.PHYs) != 1 || m.PHYs[0].PHYID != "0x03a29481" || *m.PHYs[0].MDIOAddr != 9 {
		t.Fatalf("phys %+v", m.PHYs)
	}
	var roles []string
	for _, p := range m.Ports {
		roles = append(roles, p.Label+"="+p.Role)
	}
	if strings.Join(roles, ",") != "cpu=cpu,lan1=lan,wan=wan" {
		t.Fatalf("ports %v", roles)
	}
	dts := f.DTS()
	for _, want := range []string{"/dts-v1/;", `model = "Test Board";`, `compatible = "nokia,xg-040g-md", "airoha,an7581";`, "read-only;", "reg = <0x00020000 0x0ffe0000>;"} {
		if !strings.Contains(dts, want) {
			t.Errorf("DTS lacks %q", want)
		}
	}
	if _, err := ParseFDT(blob[:len(blob)-8]); err == nil {
		t.Fatal("truncated DTB accepted")
	}
}

func TestFIPParserAndSignatures(t *testing.T) {
	h, err := ParseFIP(simFIP())
	if err != nil || len(h.Entries) != 2 || !strings.HasPrefix(h.Entries[0].Name, "BL31") || !strings.HasPrefix(h.Entries[1].Name, "BL33") || h.Entries[1].Offset != 0x888 {
		t.Fatalf("%+v %v", h, err)
	}
	cases := map[string][]byte{
		"fip":      simFIP(),
		"ubi":      []byte("UBI#\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x08\x00\x00\x00\x10\x00"),
		"erased":   bytes.Repeat([]byte{0xff}, 64),
		"squashfs": []byte("hsqs0000"),
		"uimage":   {0x27, 0x05, 0x19, 0x56, 0, 0, 0, 0},
	}
	for want, b := range cases {
		if got := DetectSignature(b).Format; got != want {
			t.Errorf("%s detected as %s", want, got)
		}
	}
	env := append([]byte{0, 0, 0, 0}, []byte("bootcmd=run x\x00bootdelay=3\x00baudrate=115200\x00\x00")...)
	if got := DetectSignature(env); got.Format != "uboot-env" || !got.Heur {
		t.Errorf("env: %+v", got)
	}
}

// ---------------- §23/§24 confidence and conflicts ----------------

func TestResolveConfidenceAndConflict(t *testing.T) {
	p := &Profile{}
	f := p.resolve("x", []obs{{src: "dt-linux:model", val: "A", conf: Medium}, {src: "linux:sysinfo", val: "A", conf: Medium}})
	if f.Value != "A" || f.Confidence != High {
		t.Fatalf("%+v", f)
	}
	f = p.resolve("y", []obs{{src: "uart-log", val: "A", conf: Low}})
	if f.Confidence != Low {
		t.Fatalf("single heuristic should stay low: %+v", f)
	}
	f = p.resolve("flash.total_size", []obs{{src: "dt-linux:x", val: uint64(256 << 20), conf: Medium}, {src: "u-boot:mtd list", val: uint64(128 << 20), conf: Medium}})
	if f.Value != nil || f.Note != "conflict" || len(p.Conflicts) != 1 || len(p.Warnings) != 1 {
		t.Fatalf("conflict not reported: %+v %+v", f, p.Conflicts)
	}
	f = p.resolve("z", nil)
	if f.Value != nil || f.Confidence != Unknown {
		t.Fatalf("%+v", f)
	}
}

func TestMTDConflictDetection(t *testing.T) {
	dir := t.TempDir()
	d := newSim(false)
	mustWrite(t, dir, "uboot/version.txt", d.uboot("version"))
	mustWrite(t, dir, "uboot/mtd-list.txt", d.uboot("mtd list"))
	mustWrite(t, dir, "linux/uname.txt", "Linux x 6.12 #0 aarch64\n")
	// Linux claims a different ubi size than U-Boot.
	mustWrite(t, dir, "linux/proc-mtd.txt", "dev:    size   erasesize  name\nmtd0: 00020000 00020000 \"bl2\"\nmtd1: 07fe0000 00020000 \"ubi\"\n")
	p, err := Analyze(dir, AnalyzeOptions{Tool: "t", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	var ubi *MTDEntry
	for i := range p.MTD {
		if p.MTD[i].Name == "ubi" {
			ubi = &p.MTD[i]
		}
	}
	if ubi == nil || !ubi.Conflict || ubi.Size != nil || ubi.Confidence != Unknown {
		t.Fatalf("ubi entry %+v", ubi)
	}
	found := false
	for _, c := range p.Conflicts {
		if c.Field == "mtd[ubi].size" && len(c.Values) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts %+v", p.Conflicts)
	}
	var bl2 *MTDEntry
	for i := range p.MTD {
		if p.MTD[i].Name == "bl2" {
			bl2 = &p.MTD[i]
		}
	}
	if bl2 == nil || bl2.Conflict || *bl2.Size != 0x20000 || bl2.Confidence != Medium {
		t.Fatalf("bl2 entry %+v", bl2)
	}
}

func mustWrite(t *testing.T, dir, rel, s string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownDeviceProfile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "uart.log", "some vendor loader\r\nbldr> \r\n")
	p, err := Analyze(dir, AnalyzeOptions{Tool: "t", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if p.SoC.Value != "unknown" || p.Completeness["complete"] != false || p.Schema != SchemaV1 {
		t.Fatalf("%+v %+v", p.SoC, p.Completeness)
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "UNKNOWN AIROHA DEVICE") {
		t.Fatalf("warnings %v", p.Warnings)
	}
}

// ---------------- full simulated run ----------------

func TestFullProbeOnSimulatedBoard(t *testing.T) { runFullSim(t, false) }

// The advanced mode may attach UBI; it must say so everywhere.
func TestFullProbeWithUBIAttach(t *testing.T) { runFullSim(t, true) }

func runFullSim(t *testing.T, attach bool) {
	dir := filepath.Join(t.TempDir(), "probe")
	dev := newSim(false)
	var console bytes.Buffer
	s, err := NewSession(dir, dev, &console, fastTiming)
	if err != nil {
		t.Fatal(err)
	}
	out := Collect(s, Options{Layers: AllLayers(), Timeout: 60 * time.Second, UBIAttach: attach})
	s.Close()
	if !out.UBoot || !out.Linux || out.Blocked != 0 {
		t.Fatalf("outcome %+v\nnotes %v\n--- console tail ---\n%s", out, out.Notes, tailStr(console.String(), 3000))
	}
	// Every command the device saw must be one the guard allows.
	check := CheckUBoot
	if attach {
		check = CheckUBootAttach
	}
	for _, c := range dev.UCmds {
		if _, err := check(c); err != nil {
			t.Errorf("device received non-allowlisted U-Boot command %q", c)
		}
		if !attach && strings.HasPrefix(c, "ubi ") {
			t.Errorf("strict probe sent %q", c)
		}
	}
	if out.UBIAttached != attach {
		t.Errorf("UBIAttached=%v", out.UBIAttached)
	}
	for _, c := range dev.LCmds {
		if _, err := CheckLinux(c); err != nil {
			t.Errorf("device received non-allowlisted Linux command %q", c)
		}
	}
	for _, k := range dev.Keys {
		if k != "ctrl-c" {
			t.Errorf("unexpected key %q", k)
		}
	}
	p, err := Analyze(dir, AnalyzeOptions{Tool: "UrsidoRescue", Version: "test", Now: func() time.Time { return time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if p.SoC.Value != "AN7581" || p.SoC.Confidence != High {
		t.Errorf("soc %+v", p.SoC.Fact)
	}
	if p.Device["model"].Value != "Nokia XG-040G-MD" || p.Device["model"].Confidence != High {
		t.Errorf("model %+v", p.Device["model"])
	}
	if p.Device["ram_mib"].Value == nil {
		t.Errorf("ram %+v", p.Device["ram_mib"])
	}
	if p.Flash.Type.Value != "SPI-NAND" || p.Flash.Manufacturer.Value != "Fudan" || p.Flash.EraseSize.Value != uint64(0x20000) || p.Flash.PageSize.Value != uint64(2048) || p.Flash.TotalSize.Value != uint64(256<<20) {
		t.Errorf("flash %+v %+v %+v %+v %+v", p.Flash.Type, p.Flash.Manufacturer, p.Flash.EraseSize, p.Flash.PageSize, p.Flash.TotalSize)
	}
	if len(p.Conflicts) != 0 {
		t.Errorf("unexpected conflicts %+v", p.Conflicts)
	}
	byName := map[string]MTDEntry{}
	for _, e := range p.MTD {
		byName[e.Name] = e
	}
	ubi := byName["ubi"]
	if ubi.Offset == nil || *ubi.Offset != 0x20000 || ubi.Confidence != High || len(ubi.Source) < 3 {
		t.Errorf("ubi mtd %+v", ubi)
	}
	if len(ubi.Contents) == 0 || ubi.Contents[0].Signature.Format != "ubi" {
		t.Errorf("ubi contents %+v", ubi.Contents)
	}
	if !p.UBI.Detected || p.UBI.PEBSize.Value != uint64(131072) || p.UBI.PEBSize.Confidence != High || len(p.UBI.Volumes) != 4 {
		t.Errorf("ubi %+v", p.UBI)
	}
	var fipStage *BootStage
	for i := range p.BootChain {
		if p.BootChain[i].Stage == "bl31/atf" {
			fipStage = &p.BootChain[i]
		}
	}
	if attach {
		if fipStage == nil || fipStage.Location != "ubi:fip" || fipStage.FIP == nil || len(fipStage.FIP.Entries) != 2 || fipStage.Hash["scope"] != "whole object read into RAM by U-Boot" {
			t.Errorf("fip stage %+v", fipStage)
		}
		if p.Completeness["strict_read_only"] != false || !strings.Contains(strings.Join(p.Warnings, "\n"), "NOT strictly read-only") {
			t.Errorf("attach mode not reported: %v %v", p.Completeness, p.Warnings)
		}
	} else {
		if p.Completeness["strict_read_only"] != true || p.UBI.OfflineHeaders == nil || p.UBI.VIDHdrOffset.Confidence != High {
			t.Errorf("strict: %v %+v %+v", p.Completeness, p.UBI.OfflineHeaders, p.UBI.VIDHdrOffset)
		}
		if strings.Contains(console.String(), "UBI мог записать") || !strings.Contains(console.String(), "команд записи не отправлялось") {
			t.Error("strict run printed the wrong closing message")
		}
	}
	if p.Capabilities["commands"].(map[string]bool)["mtd"] != true {
		t.Errorf("caps %+v", p.Capabilities)
	}
	if len(p.Identity) == 0 {
		t.Error("identity locations missing")
	}
	// board.json in the simulator carries macaddr in network and network_device.
	for _, f := range []string{"profile.json", "ursusboot-porting-report.md", "ursusflasher-device-draft.json", "network/network.json"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		for _, secret := range []string{"00:11:22:33:44:5", "ABC123456"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s contains device identity %q", f, secret)
			}
		}
	}
	for _, f := range []string{"uart.log", "transcript.jsonl", "uboot/version.txt", "uboot/help.txt", "uboot/bdinfo.txt", "uboot/env.txt", "uboot/mtd-list.txt",
		"uboot/capabilities.json", "linux/cpuinfo.txt", "linux/proc-mtd.txt", "linux/dmesg.txt", "dt/fdt.dtb", "dt/fdt.dts", "dt/fdt-uboot.dtb", "dt/fdt-linux.dtb",
		"mtd/mtd-map.json", "ubi/ubi.json", "network/network.json", "gpio/gpio.json", "ursusboot-porting-report.md", "ursusflasher-device-draft.json", "uart/events.json", "uart/params.json", "bootrom/bootrom.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	zipPath, err := Export(dir, p, ExportOptions{Now: func() time.Time { return time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(zipPath) != "ursus-probe-nokia-xg-040g-md-an7581-20260924-000000.zip" {
		t.Errorf("bundle name %s", filepath.Base(zipPath))
	}
	verifyBundle(t, zipPath)
	red, err := Export(dir, p, ExportOptions{OutDir: filepath.Join(t.TempDir(), "r"), Redact: true})
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.OpenReader(red)
	defer zr.Close()
	sawNote := false
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if isBinary(f.Name) {
			t.Errorf("redacted bundle contains binary %s", f.Name)
		}
		if strings.HasSuffix(f.Name, "/REDACTED.txt") {
			sawNote = bytes.Contains(b, []byte("dt/fdt.dtb"))
		}
		if bytes.Contains(b, []byte("00:11:22:33:44:5")) || bytes.Contains(b, []byte("ABC123456")) {
			t.Errorf("redacted bundle leaks identity in %s", f.Name)
		}
	}
	if !sawNote {
		t.Error("REDACTED.txt missing or incomplete")
	}
}

// §22: hashes.sha256 covers every file and matches.
func verifyBundle(t *testing.T, path string) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		_, rel, _ := strings.Cut(f.Name, "/")
		files[rel] = b
	}
	h, ok := files["hashes.sha256"]
	if !ok {
		t.Fatal("no hashes.sha256")
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(h)), "\n") {
		sum, name, _ := strings.Cut(line, "  ")
		listed[name] = true
		got := sha256.Sum256(files[name])
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("hash mismatch for %s", name)
		}
	}
	for name := range files {
		if name != "hashes.sha256" && !listed[name] {
			t.Errorf("%s not in hashes.sha256", name)
		}
	}
	for _, need := range []string{"profile.json", "README.txt", "uart.log", "ursusboot-porting-report.md", "ursusflasher-device-draft.json"} {
		if _, ok := files[need]; !ok {
			t.Errorf("bundle lacks %s", need)
		}
	}
	var p Profile
	if err := json.Unmarshal(files["profile.json"], &p); err != nil || p.Schema != SchemaV1 {
		t.Errorf("profile.json in bundle: %v %s", err, p.Schema)
	}
	if len(p.Artifacts) == 0 {
		t.Error("profile has no artifact hashes")
	}
}

func tailStr(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func TestLinuxOnlyWhenUBootMissed(t *testing.T) {
	dir := t.TempDir()
	dev := newSim(false)
	s, _ := NewSession(dir, dev, nil, fastTiming)
	out := Collect(s, Options{Layers: Layers{Linux: true}, Timeout: 30 * time.Second})
	s.Close()
	if out.UBoot || !out.Linux || len(dev.UCmds) != 0 {
		t.Fatalf("%+v ucmds=%v", out, dev.UCmds)
	}
	for _, k := range dev.Keys {
		if k == "ctrl-c" {
			t.Fatal("Linux-only probe interrupted U-Boot")
		}
	}
}

func ExampleCheckUBoot() {
	for _, c := range []string{"mtd  list", "sa", "mtd wr bl2"} {
		canon, err := CheckUBoot(c)
		fmt.Printf("%q -> %q blocked=%v\n", c, canon, err != nil)
	}
	// Output:
	// "mtd  list" -> "mtd list" blocked=false
	// "sa" -> "" blocked=true
	// "mtd wr bl2" -> "" blocked=true
}

// Review item 3: an unknown loader gets nothing — not even the idle Ctrl-C —
// unless --wake is given, and even then an unrecognised "xxx>" gets no command.
type recPort struct {
	out  []byte
	sent []byte
}

func (p *recPort) Name() string { return "rec" }
func (p *recPort) Write(b []byte) error {
	p.sent = append(p.sent, b...)
	if bytes.Contains(b, []byte{0x03}) {
		p.out = append(p.out, "\r\nfoo> "...)
	}
	return nil
}
func (p *recPort) Read(b []byte, timeout time.Duration) (int, error) {
	if len(p.out) == 0 {
		time.Sleep(timeout)
		return 0, nil
	}
	n := copy(b, p.out)
	p.out = p.out[n:]
	return n, nil
}

func TestUnknownLoaderIsPassive(t *testing.T) {
	for _, wake := range []bool{false, true} {
		port := &recPort{out: []byte("\r\nVendorLoader 1.0\r\nHit any key to stop autoboot: 3\r\nfoo> ")}
		s, _ := NewSession(t.TempDir(), port, nil, fastTiming)
		o := Collect(s, Options{Layers: AllLayers(), Timeout: 1500 * time.Millisecond, Wake: wake})
		s.Close()
		want := ""
		if wake {
			want = "\x03"
		}
		if string(port.sent) != want {
			t.Errorf("wake=%v: sent %q, want %q", wake, port.sent, want)
		}
		if o.UBoot || o.UnconfirmedPrompt != "foo>" {
			t.Errorf("wake=%v: outcome %+v", wake, o)
		}
	}
}

func TestWakeAcceptsOnlyKnownUBootPrompts(t *testing.T) {
	for p, ok := range map[string]bool{"=>": true, "U-Boot>": true, "AN7581>": true, "EN7523 >": true, "foo>": false, "ONT>": false, "WAP>": false} {
		if knownUBootPromptRE.MatchString(p) != ok {
			t.Errorf("%q", p)
		}
	}
}

// Review item 2: board.json macaddr/serial must not reach profile.json,
// the reports or the draft.
func TestSanitizerDropsIdentity(t *testing.T) {
	in := map[string]any{"network": map[string]any{"lan": map[string]any{"macaddr": "00:11:22:33:44:55", "ports": []any{"lan1"}}},
		"note": "addr 00:11:22:33:44:55 here", "fip": map[string]any{"uuid": "47d4086d-4cfe-9846-9b95-2950cbbd5a00"},
		"flash": map[string]any{"device_id": "0xa1e1"}, "serial#": "X", "gpon_sn": "Y", "wlan0_mac": "Z", "ethaddr": "W"}
	out, n := sanitize(in)
	b, _ := json.Marshal(out)
	for _, bad := range []string{"00:11:22:33:44:55", `"X"`, `"Y"`, `"Z"`, `"W"`} {
		if bytes.Contains(b, []byte(bad)) {
			t.Errorf("leaked %s: %s", bad, b)
		}
	}
	for _, keep := range []string{"47d4086d-4cfe-9846-9b95-2950cbbd5a00", "0xa1e1", "lan1"} {
		if !bytes.Contains(b, []byte(keep)) {
			t.Errorf("sanitizer removed non-identity value %s", keep)
		}
	}
	if n != 6 {
		t.Errorf("changed %d values", n)
	}
}

func TestOfflineUBIHeaders(t *testing.T) {
	h, ok := ParseUBIHeaders(simUBIHeaders())
	if !ok || !h.ECCRCOK || h.VIDHdrOffset != 2048 || h.DataOffset != 4096 || !h.VIDPresent || h.VolID != 0x7fffefff || h.VolType != "dynamic" {
		t.Fatalf("%+v", h)
	}
	bad := simUBIHeaders()
	bad[16] ^= 1
	if h, _ := ParseUBIHeaders(bad); h.ECCRCOK {
		t.Fatal("corrupted EC header passed CRC")
	}
}
