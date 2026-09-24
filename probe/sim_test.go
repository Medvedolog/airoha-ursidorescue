package probe

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------- DTB builder (tests only) ----------------

type bNode struct {
	name     string
	props    [][2]any // name, value ([]byte | string | []string | []uint32 | nil)
	children []*bNode
}

func nd(name string, props [][2]any, kids ...*bNode) *bNode {
	return &bNode{name: name, props: props, children: kids}
}

func propBytes(v any) []byte {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return x
	case string:
		return append([]byte(x), 0)
	case []string:
		var b []byte
		for _, s := range x {
			b = append(append(b, s...), 0)
		}
		return b
	case []uint32:
		b := make([]byte, 4*len(x))
		for i, c := range x {
			binary.BigEndian.PutUint32(b[i*4:], c)
		}
		return b
	}
	panic(fmt.Sprintf("bad prop %T", v))
}

func buildDTB(root *bNode) []byte {
	var st, strs []byte
	off := map[string]int{}
	put := func(v uint32) { st = binary.BigEndian.AppendUint32(st, v) }
	pad := func() {
		for len(st)%4 != 0 {
			st = append(st, 0)
		}
	}
	var rec func(n *bNode)
	rec = func(n *bNode) {
		put(1)
		st = append(append(st, n.name...), 0)
		pad()
		for _, p := range n.props {
			name := p[0].(string)
			val := propBytes(p[1])
			o, ok := off[name]
			if !ok {
				o = len(strs)
				off[name] = o
				strs = append(append(strs, name...), 0)
			}
			put(3)
			put(uint32(len(val)))
			put(uint32(o))
			st = append(st, val...)
			pad()
		}
		for _, c := range n.children {
			rec(c)
		}
		put(2)
	}
	rec(root)
	put(9)
	hdr := make([]byte, 40)
	rsv := make([]byte, 16)
	offRsv := 40
	offStruct := offRsv + len(rsv)
	offStrings := offStruct + len(st)
	total := offStrings + len(strs)
	be := binary.BigEndian
	be.PutUint32(hdr[0:], fdtMagic)
	be.PutUint32(hdr[4:], uint32(total))
	be.PutUint32(hdr[8:], uint32(offStruct))
	be.PutUint32(hdr[12:], uint32(offStrings))
	be.PutUint32(hdr[16:], uint32(offRsv))
	be.PutUint32(hdr[20:], 17)
	be.PutUint32(hdr[24:], 16)
	be.PutUint32(hdr[32:], uint32(len(strs)))
	be.PutUint32(hdr[36:], uint32(len(st)))
	out := append(append(append(hdr, rsv...), st...), strs...)
	return out
}

// boardDTB is a small but realistic AN7581 board tree.
func boardDTB(model string) []byte {
	return buildDTB(nd("", [][2]any{
		{"model", model},
		{"compatible", []string{"nokia,xg-040g-md", "airoha,an7581"}},
		{"#address-cells", []uint32{2}}, {"#size-cells", []uint32{2}},
	},
		nd("chosen", [][2]any{{"bootargs", "console=ttyS0,115200"}, {"stdout-path", "serial0:115200n8"}}),
		nd("aliases", [][2]any{{"serial0", "/serial@1fbf0000"}}),
		nd("memory@80000000", [][2]any{{"device_type", "memory"}, {"reg", []uint32{0, 0x80000000, 0, 0x20000000}}}),
		nd("serial@1fbf0000", [][2]any{{"compatible", "airoha,en7523-uart"}, {"reg", []uint32{0, 0x1fbf0000, 0, 0x30}}}),
		nd("gpio@1fbf0200", [][2]any{{"compatible", "airoha,en7523-gpio"}, {"gpio-controller", nil}, {"#gpio-cells", []uint32{2}}, {"phandle", []uint32{1}}}),
		nd("keys", [][2]any{{"compatible", "gpio-keys"}},
			nd("reset", [][2]any{{"label", "reset"}, {"linux,code", []uint32{0x198}}, {"gpios", []uint32{1, 0, 1}}})),
		nd("leds", [][2]any{{"compatible", "gpio-leds"}},
			nd("led-0", [][2]any{{"function", "status"}, {"color", []uint32{2}}, {"gpios", []uint32{1, 5, 0}}})),
		nd("spi@1fa10000", [][2]any{{"compatible", "airoha,en7581-snand"}, {"#address-cells", []uint32{1}}, {"#size-cells", []uint32{0}}, {"reg", []uint32{0, 0x1fa10000, 0, 0x140}}},
			nd("nand@0", [][2]any{{"compatible", "spi-nand"}, {"reg", []uint32{0}}},
				nd("partitions", [][2]any{{"compatible", "fixed-partitions"}, {"#address-cells", []uint32{1}}, {"#size-cells", []uint32{1}}},
					nd("partition@0", [][2]any{{"label", "bl2"}, {"reg", []uint32{0, 0x20000}}, {"read-only", nil}}),
					nd("partition@20000", [][2]any{{"label", "ubi"}, {"reg", []uint32{0x20000, 0xffe0000}}})))),
		nd("ethernet@1fb50000", [][2]any{{"compatible", "airoha,en7581-eth"}, {"reg", []uint32{0, 0x1fb50000, 0, 0x2600}}, {"#address-cells", []uint32{1}}, {"#size-cells", []uint32{0}}},
			nd("ethernet@1", [][2]any{{"compatible", "airoha,eth-mac"}, {"reg", []uint32{1}}, {"phy-mode", "internal"}})),
		nd("switch@1fb58000", [][2]any{{"compatible", "airoha,en7581-switch"}, {"reg", []uint32{0, 0x1fb58000, 0, 0x8000}}},
			nd("ports", [][2]any{{"#address-cells", []uint32{1}}, {"#size-cells", []uint32{0}}},
				nd("port@0", [][2]any{{"reg", []uint32{0}}, {"label", "cpu"}, {"ethernet", []uint32{9}}, {"phy-mode", "internal"}}),
				nd("port@1", [][2]any{{"reg", []uint32{1}}, {"label", "lan1"}, {"phy-mode", "internal"}, {"phy-handle", []uint32{2}}}),
				nd("port@4", [][2]any{{"reg", []uint32{4}}, {"label", "wan"}, {"phy-mode", "usxgmii"}})),
			nd("mdio", [][2]any{{"#address-cells", []uint32{1}}, {"#size-cells", []uint32{0}}},
				nd("ethernet-phy@9", [][2]any{{"compatible", "ethernet-phy-id03a2.9481"}, {"reg", []uint32{9}}, {"phandle", []uint32{2}}}))),
	))
}

// simFIP is a FIP with BL31 and BL33 entries.
func simFIP() []byte {
	b := make([]byte, 0x1800)
	le := binary.LittleEndian
	le.PutUint32(b[0:], 0xAA640001)
	le.PutUint32(b[4:], 0x12345678)
	bl31, _ := hex.DecodeString("47d4086d4cfe98469b952950cbbd5a00")
	bl33, _ := hex.DecodeString("d6d0eea7fcead54b97829934f234b6e4")
	copy(b[16:], bl31)
	le.PutUint64(b[32:], 0x88)
	le.PutUint64(b[40:], 0x800)
	copy(b[56:], bl33)
	le.PutUint64(b[72:], 0x888)
	le.PutUint64(b[80:], 0xf00)
	for i := 0x88; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	return b
}

// ---------------- simulated device ----------------

type simDev struct {
	mu        sync.Mutex
	now       func() time.Time
	queue     []simEvt
	out       []byte
	line      []byte
	state     string // boot, autoboot, uboot, linuxboot, linuxwait, linux, bootrom
	boots     int
	lastIn    time.Time
	lastRC    int
	executed  int
	UCmds     []string
	LCmds     []string
	Keys      []string
	ram       map[uint64][]byte
	dtb       []byte
	fip       []byte
	pressX    bool
	noLinux   bool
	autoDeadl time.Time
}

type simEvt struct {
	at    time.Time
	data  string
	state string
}

const simPrompt = "AN7581> "

func newSim(pressX bool) *simDev {
	d := &simDev{now: time.Now, ram: map[uint64][]byte{}, dtb: boardDTB("Nokia XG-040G-MD"), fip: simFIP(), pressX: pressX}
	d.ram[0x9de7aa30] = d.dtb
	d.powerOn()
	return d
}

func (d *simDev) Name() string { return "sim" }

func (d *simDev) sched(after time.Duration, data, state string) {
	d.queue = append(d.queue, simEvt{at: d.now().Add(after), data: data, state: state})
}

func (d *simDev) powerOn() {
	d.boots++
	d.state = "boot"
	d.queue = nil
	if d.pressX && d.boots == 1 {
		d.sched(10*time.Millisecond, "\r\nPress x", "bootrom")
		return
	}
	log := "\r\nNOTICE:  BL2: v2.12.0(release):v2.12.0\r\nAN7581DRAMC V0.6\r\ndram flow done\r\nNOTICE:  BL2: Booting BL31\r\n" +
		"NOTICE:  BL31: v2.12.0(release):v2.12.0\r\n\r\nU-Boot 2026.07-OpenWrt (Sep 01 2026 - 00:00:00 +0000)\r\n\r\nCPU:   Airoha AN7581\r\nModel: Nokia XG-040G-MD\r\nDRAM:  512 MiB\r\n" +
		"spi-nand: Fudan SPI NAND was found.\r\nspi-nand: 256 MiB, block size: 128 KiB, page size: 2048, OOB size: 128\r\n"
	d.sched(20*time.Millisecond, log, "")
	d.sched(60*time.Millisecond, "Hit any key to stop autoboot:  1 ", "autoboot")
}

func (d *simDev) bootLinux() {
	d.state = "linuxboot"
	d.sched(10*time.Millisecond, "\r\nStarting kernel ...\r\n\r\n[    0.000000] Booting Linux on physical CPU 0x0000000000 [0x411fd050]\r\n[    0.000000] Linux version 6.12.40 (builder@x) #0 SMP\r\n", "")
	if d.noLinux {
		return
	}
	d.sched(80*time.Millisecond, "[    2.000000] spi-nand spi0.0: Fudan SPI NAND was found.\r\n[    5.000000] ubi0: attaching mtd1\r\n", "")
	d.sched(160*time.Millisecond, "\r\nPlease press Enter to activate this console.\r\n", "linuxwait")
}

func (d *simDev) Read(p []byte, timeout time.Duration) (int, error) {
	end := d.now().Add(timeout)
	for {
		d.mu.Lock()
		now := d.now()
		for len(d.queue) > 0 && !d.queue[0].at.After(now) {
			e := d.queue[0]
			d.queue = d.queue[1:]
			d.out = append(d.out, e.data...)
			if e.state != "" {
				d.state = e.state
				if e.state == "autoboot" {
					d.autoDeadl = now.Add(700 * time.Millisecond)
				}
			}
		}
		if d.state == "autoboot" && now.After(d.autoDeadl) {
			d.bootLinux()
		}
		if d.state == "bootrom" && len(d.out) == 0 && d.executed == 0 && len(d.Keys) > 0 && d.Keys[len(d.Keys)-1] == "x" {
			d.out = append(d.out, "CCC"...)
		}
		// The operator power-cycles once U-Boot has been probed and the line is idle.
		if d.state == "uboot" && d.executed > 10 && now.Sub(d.lastIn) > 500*time.Millisecond && len(d.out) == 0 {
			d.powerOn()
		}
		if d.state == "bootrom" && len(d.Keys) > 0 && d.Keys[len(d.Keys)-1] == "x" && now.Sub(d.lastIn) > 400*time.Millisecond && len(d.out) == 0 {
			d.Keys = append(d.Keys, "power-cycle")
			d.powerOn()
		}
		if len(d.out) > 0 {
			n := copy(p, d.out[:min(len(d.out), 512)])
			d.out = d.out[n:]
			d.mu.Unlock()
			return n, nil
		}
		d.mu.Unlock()
		if !d.now().Before(end) {
			return 0, nil
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (d *simDev) Write(p []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastIn = d.now()
	for _, c := range p {
		switch d.state {
		case "bootrom":
			if c == 'x' {
				d.Keys = append(d.Keys, "x")
			}
		case "autoboot":
			if c == 0x03 {
				d.Keys = append(d.Keys, "ctrl-c")
				d.state = "uboot"
				d.out = append(d.out, "\r\n"+simPrompt...)
			}
		case "uboot":
			switch c {
			case 0x03:
				d.line = d.line[:0]
				d.out = append(d.out, "<INTERRUPT>\r\n"+simPrompt...)
			case 0x1b:
			case '\r':
				line := string(d.line)
				d.line = d.line[:0]
				d.out = append(d.out, "\r\n"...)
				if line != "" {
					d.out = append(d.out, normLF(d.uboot(line))...)
				}
				d.out = append(d.out, simPrompt...)
			default:
				d.line = append(d.line, c)
				d.out = append(d.out, c)
			}
		case "linuxwait":
			if c == '\r' {
				d.state = "linux"
				d.out = append(d.out, "\r\n\r\nBusyBox v1.37.0 built-in shell (ash)\r\n\r\nroot@OpenWrt:/# "...)
			}
		case "linux":
			switch c {
			case 0x03:
				d.line = d.line[:0]
				d.out = append(d.out, "^C\r\nroot@OpenWrt:/# "...)
			case '\r':
				line := string(d.line)
				d.line = d.line[:0]
				d.out = append(d.out, "\r\n"...)
				d.out = append(d.out, normLF(d.linux(line))...)
				d.out = append(d.out, "root@OpenWrt:/# "...)
			default:
				d.line = append(d.line, c)
				d.out = append(d.out, c)
			}
		}
	}
	return nil
}

func normLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

var simRCRE = regexp.MustCompile(`^echo (URSIDO_[0-9A-Z_]+)_\$\?$`)

func (d *simDev) flash(name string, off, n uint64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((off + uint64(i)) * 13)
	}
	if name == "ubi" && off == 0 && n >= 64 {
		copy(b, "UBI#\x01\x00\x00\x00")
		binary.BigEndian.PutUint32(b[16:], 2048)
		binary.BigEndian.PutUint32(b[20:], 4096)
	}
	return b
}

func mtdDumpText(start uint64, b []byte) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Reading %d byte(s) (%d page(s)) at offset 0x%08x\n", len(b), len(b)/2048, start)
	for p := 0; p < len(b); p += 2048 {
		fmt.Fprintf(&sb, "\nDump 2048 data bytes from 0x%08x:\n", start+uint64(p))
		for i := p; i < p+2048 && i < len(b); i += 16 {
			fmt.Fprintf(&sb, "0x%08x:\t", start+uint64(i))
			for j := 0; j < 8; j++ {
				fmt.Fprintf(&sb, "%02x ", b[i+j])
			}
			sb.WriteString(" ")
			for j := 8; j < 16; j++ {
				fmt.Fprintf(&sb, "%02x ", b[i+j])
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func mdText(addr uint64, b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i += 16 {
		fmt.Fprintf(&sb, "%08x:", addr+uint64(i))
		end := min(i+16, len(b))
		for _, c := range b[i:end] {
			fmt.Fprintf(&sb, " %02x", c)
		}
		sb.WriteString(strings.Repeat("   ", 16-(end-i)) + "    ")
		for _, c := range b[i:end] {
			if c >= 0x20 && c < 0x7f {
				sb.WriteByte(c)
			} else {
				sb.WriteByte('.')
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (d *simDev) ramRead(addr, n uint64) ([]byte, bool) {
	for base, b := range d.ram {
		if addr >= base && addr+n <= base+uint64(len(b)) {
			return b[addr-base : addr-base+n], true
		}
	}
	return nil, false
}

func hexU(s string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
	return v
}

var ubiLayout = map[string]int{"fip": 0x1800, "ubootenv": 0x1f000, "kernel": 0x400000, "rootfs": 0x1000000}

func (d *simDev) uboot(line string) string {
	if m := simRCRE.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf("%s_%d\n", m[1], d.lastRC)
	}
	d.UCmds = append(d.UCmds, line)
	d.executed++
	d.lastRC = 0
	f := strings.Fields(line)
	switch {
	case line == "version":
		return "U-Boot 2026.07-OpenWrt (Sep 01 2026 - 00:00:00 +0000)\n\naarch64-openwrt-linux-musl-gcc (OpenWrt GCC 14.3.0) 14.3.0\n"
	case line == "help":
		var sb strings.Builder
		for _, c := range []string{"bdinfo", "coninfo", "crc32", "dm", "echo", "fdt", "gpio", "hash", "help", "loadx", "md", "mdio", "mii", "mtd", "printenv", "reset", "saveenv", "setenv", "tftpboot", "ubi", "version"} {
			fmt.Fprintf(&sb, "%-10s- %s command\n", c, c)
		}
		return sb.String()
	case line == "help mtd":
		return "mtd - MTD utils\n\nUsage:\nmtd - generic operations on memory technology devices\n\nmtd list\nmtd read[.raw][.oob]  <name> <addr> [<off> [<size>]]\nmtd dump[.raw][.oob]  <name> [<off> [<size>]]\nmtd bad <name>\n"
	case line == "bdinfo":
		return "boot_params = 0x0000000000000000\nDRAM bank   = 0x0000000000000000\n-> start    = 0x0000000080000000\n-> size     = 0x0000000020000000\nrelocaddr   = 0x000000009fe9f000\nreloc off   = 0x000000001fe9f000\nfdt_blob    = 0x000000009de7aa30\n"
	case line == "printenv":
		return "arch=arm\nbaudrate=115200\nbootcmd=run ubi_read_production\nbootdelay=1\nethaddr=00:11:22:33:44:55\nfdtcontroladdr=9de7aa30\nloadaddr=0x90000000\nserial#=ABC123456\n\nEnvironment size: 180/65532 bytes\n"
	case line == "coninfo":
		return "List of available devices\nserial@1fbf0000 00000007 IO stdin stdout stderr\n"
	case line == "dm tree":
		return " Class     Index  Probed  Driver                Name\n root          0  [ + ]   root_driver           root_driver\n"
	case line == "mtd list":
		return "List of MTD devices:\n* spi-nand0\n  - device: nand@0\n  - parent: spi@1fa10000\n  - driver: spi_nand\n  - path: /spi@1fa10000/nand@0\n  - type: NAND flash\n  - block size: 0x20000 bytes\n  - min I/O: 0x800 bytes\n  - OOB size: 128 bytes\n  - OOB available: 64 bytes\n  - ECC strength: 8 bits\n  - ECC step size: 512 bytes\n  - bitflip threshold: 6 bits\n  - 0x000000000000-0x000010000000 : \"spi-nand0\"\n\t  - 0x000000000000-0x000000020000 : \"bl2\"\n\t  - 0x000000020000-0x000010000000 : \"ubi\"\n"
	case line == "mtd bad spi-nand0":
		return "MTD device spi-nand0 bad blocks list:\n0x0000054c0000\n"
	case len(f) == 5 && f[0] == "mtd" && f[1] == "dump":
		off, n := hexU(f[3]), hexU(f[4])
		return mtdDumpText(off, d.flash(f[2], off, n))
	case len(f) == 6 && f[0] == "mtd" && f[1] == "read":
		addr, off, n := hexU(f[3]), hexU(f[4]), hexU(f[5])
		d.ram[addr] = d.flash(f[2], off, n)
		return fmt.Sprintf("Reading %d byte(s) (%d page(s)) at offset 0x%08x\n", n, n/2048, off)
	case line == "ubi part ubi":
		return "ubi0: attaching mtd1\nubi0: attached mtd1 (name \"ubi\", size 255 MiB)\n"
	case line == "ubi info":
		return "ubi0: MTD device name:            \"ubi\"\nubi0: MTD device size:            255 MiB\nubi0: physical eraseblock size:   131072 bytes (128 KiB)\nubi0: logical eraseblock size:    126976 bytes\nubi0: number of good PEBs:        2046\nubi0: number of bad PEBs:         1\nubi0: smallest flash I/O unit:    2048\nubi0: VID header offset:          2048 (aligned 2048)\nubi0: data offset:                4096\nubi0: number of user volumes:     4\n"
	case line == "ubi info layout":
		var sb strings.Builder
		for i, n := range []string{"fip", "ubootenv", "kernel", "rootfs"} {
			sz := ubiLayout[n]
			pebs := (sz + 126975) / 126976
			fmt.Fprintf(&sb, "Volume information dump:\n\tvol_id          %d\n\treserved_pebs   %d\n\talignment       1\n\tdata_pad        0\n\tvol_type        3\n\tname_len        %d\n\tusable_leb_size 126976\n\tused_ebs        %d\n\tused_bytes      %d\n\tlast_eb_bytes   0\n\tcorrupted       0\n\tupd_marker      0\n\tskip_check      0\n\tname            %s\n", i, pebs, len(n), pebs, sz, n)
		}
		return sb.String()
	case len(f) == 5 && f[0] == "ubi" && f[1] == "read":
		addr, vol, n := hexU(f[2]), f[3], hexU(f[4])
		var src []byte
		if vol == "fip" {
			src = d.fip
		} else {
			src = d.flash("vol-"+vol, 0, uint64(ubiLayout[vol]))
		}
		if n > uint64(len(src)) {
			d.lastRC = 1
			return "Read size exceeds volume\n"
		}
		d.ram[addr] = append([]byte(nil), src[:n]...)
		return fmt.Sprintf("Read %d bytes from volume %s to %x\n", n, vol, addr)
	case len(f) == 3 && f[0] == "md.b":
		addr, n := hexU(f[1]), hexU(f[2])
		b, ok := d.ramRead(addr, n)
		if !ok {
			b = make([]byte, n)
		}
		return mdText(addr, b)
	case len(f) == 4 && f[0] == "hash" && f[1] == "sha256":
		addr, n := hexU(f[2]), hexU(f[3])
		b, _ := d.ramRead(addr, n)
		sum := sha256.Sum256(b)
		return fmt.Sprintf("sha256 for %08x ... %08x ==> %s\n", addr, addr+n-1, hex.EncodeToString(sum[:]))
	case len(f) == 3 && f[0] == "crc32":
		addr, n := hexU(f[1]), hexU(f[2])
		b, _ := d.ramRead(addr, n)
		return fmt.Sprintf("crc32 for %08x ... %08x ==> %08x\n", addr, addr+n-1, crc32.ChecksumIEEE(b))
	case line == "fdt addr":
		return "Working FDT set to 9de7aa30\n"
	case line == "mii info":
		return "PHY 0x09: OUI = 0x00E8, Model = 0x21, Rev = 0x01, 1000baseT, FDX\n"
	case line == "mdio list":
		return "mdio:\n9 - Generic PHY <--> ethernet@1fb50000\n"
	case line == "gpio status -a":
		return "Bank gpio0:\ngpio0: input: 1 [ ] reset\n"
	}
	d.lastRC = 1
	return fmt.Sprintf("Unknown command '%s' - try 'help'\n", f[0])
}

var simWrapRE = regexp.MustCompile(`^echo (URSIDO_B_[0-9]+); (.*) 2>&1; echo (URSIDO_E_[0-9]+)_\$\?$`)

func odText(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i += 16 {
		for _, c := range b[i:min(i+16, len(b))] {
			fmt.Fprintf(&sb, " %02x", c)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (d *simDev) linux(line string) string {
	m := simWrapRE.FindStringSubmatch(line)
	if m == nil {
		return "sh: syntax error\n"
	}
	cmd := m[2]
	d.LCmds = append(d.LCmds, cmd)
	out, rc := d.linuxCmd(cmd)
	return m[1] + "\n" + out + fmt.Sprintf("%s_%d\n", m[3], rc)
}

func (d *simDev) linuxCmd(cmd string) (string, int) {
	switch cmd {
	case "uname -a":
		return "Linux OpenWrt 6.12.40 #0 SMP Mon Sep  1 00:00:00 2026 aarch64 GNU/Linux\n", 0
	case "cat /proc/cpuinfo":
		return strings.Repeat("processor\t: 0\nBogoMIPS\t: 50.00\nCPU implementer\t: 0x41\nCPU part\t: 0xd05\n\n", 4), 0
	case "cat /proc/cmdline":
		return "console=ttyS0,115200 earlycon\n", 0
	case "cat /proc/meminfo":
		return "MemTotal:         496512 kB\nMemFree:          400000 kB\n", 0
	case "mount":
		return "/dev/root on /rom type squashfs (ro)\n", 0
	case "df":
		return "Filesystem 1K-blocks Used Available Use% Mounted on\n", 0
	case "lsblk":
		return "/bin/ash: lsblk: not found\n", 127
	case "fw_printenv":
		return "bootcmd=run ubi_read_production\nethaddr=00:11:22:33:44:55\n", 0
	case "cat /etc/openwrt_release":
		return "DISTRIB_ID='OpenWrt'\nDISTRIB_RELEASE='SNAPSHOT'\nDISTRIB_TARGET='airoha/an7581'\nDISTRIB_DESCRIPTION='OpenWrt SNAPSHOT r31000'\n", 0
	case "cat /etc/os-release":
		return "NAME=\"OpenWrt\"\nID=\"openwrt\"\nPRETTY_NAME=\"OpenWrt SNAPSHOT\"\n", 0
	case "grep -H . /tmp/sysinfo/model /tmp/sysinfo/board_name":
		return "/tmp/sysinfo/model:Nokia XG-040G-MD\n/tmp/sysinfo/board_name:nokia,xg-040g-md\n", 0
	case "cat /etc/board.json":
		return `{"model":{"id":"nokia,xg-040g-md","name":"Nokia XG-040G-MD"},"network":{"lan":{"ports":["lan1"],"protocol":"static"},"wan":{"device":"wan","protocol":"dhcp"}}}` + "\n", 0
	case "dmesg":
		return "[    2.000000] spi-nand spi0.0: Fudan SPI NAND was found.\n[    2.000100] spi-nand spi0.0: 256 MiB, block size: 128 KiB, page size: 2048, OOB size: 128\n" +
			"[    5.000000] ubi0: attaching mtd1\n[    5.100000] ubi0: PEB size: 131072 bytes (128 KiB), LEB size: 126976 bytes\n[    5.100001] ubi0: min./max. I/O unit sizes: 2048/2048, sub-page size 2048\n[    5.100002] ubi0: VID header offset: 2048 (aligned 2048), data offset: 4096\n[    5.100003] ubi0: good PEBs: 2046, bad PEBs: 1, corrupted PEBs: 0\n", 0
	case "cat /proc/mtd":
		return "dev:    size   erasesize  name\nmtd0: 00020000 00020000 \"bl2\"\nmtd1: 0ffe0000 00020000 \"ubi\"\n", 0
	case "ubinfo -a":
		var sb strings.Builder
		sb.WriteString("UBI version:                    1\nCount of UBI devices:           1\nUBI control device major/minor: 10:59\nPresent UBI devices:            ubi0\n\nubi0\nVolumes count:                           4\nLogical eraseblock size:                 126976 bytes, 124.0 KiB\nMinimum input/output unit size:          2048 bytes\nCount of bad physical eraseblocks:       1\n")
		for i, n := range []string{"fip", "ubootenv", "kernel", "rootfs"} {
			sz := ubiLayout[n]
			pebs := (sz + 126975) / 126976
			fmt.Fprintf(&sb, "\nVolume ID:   %d (on ubi0)\nType:        dynamic\nAlignment:   1\nSize:        %d LEBs (%d bytes, 1.0 MiB)\nState:       OK\nName:        %s\nCharacter device major/minor: 248:%d\n", i, pebs, pebs*126976, n, i+1)
		}
		return sb.String(), 0
	case "cat /sys/firmware/devicetree/base/model":
		return "Nokia XG-040G-MD\x00\n", 0
	case "cat /sys/firmware/devicetree/base/compatible":
		return "nokia,xg-040g-md\x00airoha,an7581\x00\n", 0
	case "od -An -v -tx1 /sys/firmware/fdt":
		return odText(d.dtb), 0
	case "ip link":
		return "1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN\n    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00\n2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1504\n    link/ether 00:11:22:33:44:55 brd ff:ff:ff:ff:ff:ff\n3: lan1@eth0: <BROADCAST,MULTICAST,UP> mtu 1500\n4: wan@eth0: <BROADCAST,MULTICAST,UP> mtu 1500\n", 0
	case "ip addr":
		return "2: eth0: <UP>\n    inet 192.168.1.1/24\n", 0
	case "ls -l /sys/class/net":
		return "lrwxrwxrwx eth0 -> ../../devices/platform/1fb50000.ethernet/net/eth0\n", 0
	case "ethtool -i eth0", "ethtool -i lan1", "ethtool -i wan":
		return "driver: airoha_eth\nbus-info: 1fb50000.ethernet\n", 0
	case "ethtool eth0", "ethtool lan1", "ethtool wan":
		return "Settings for x:\n\tSpeed: 1000Mb/s\n", 0
	case "cat /sys/kernel/debug/gpio":
		return "gpiochip0: GPIOs 512-543, parent: platform/1fbf0200.pinctrl:\n gpio-512 (                    |reset               ) in  hi IRQ ACTIVE LOW\n", 0
	case "ls /sys/class/leds":
		return "green:status\n", 0
	}
	if strings.HasPrefix(cmd, "grep -H . /sys/class/mtd/") {
		return "/sys/class/mtd/mtd0/name:bl2\n/sys/class/mtd/mtd1/name:ubi\n/sys/class/mtd/mtd0/type:nand\n/sys/class/mtd/mtd1/type:nand\n/sys/class/mtd/mtd0/size:131072\n/sys/class/mtd/mtd1/size:268304384\n/sys/class/mtd/mtd0/erasesize:131072\n/sys/class/mtd/mtd1/erasesize:131072\n/sys/class/mtd/mtd0/writesize:2048\n/sys/class/mtd/mtd1/writesize:2048\n/sys/class/mtd/mtd0/oobsize:128\n/sys/class/mtd/mtd1/oobsize:128\n/sys/class/mtd/mtd0/subpagesize:2048\n/sys/class/mtd/mtd1/subpagesize:2048\n/sys/class/mtd/mtd0/offset:0\n/sys/class/mtd/mtd1/offset:131072\n", 0
	}
	return "/bin/ash: not found\n", 127
}
