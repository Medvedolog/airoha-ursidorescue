package probe

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func normNL(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "")
}

func parseNum(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	base := 10
	if strings.HasPrefix(strings.ToLower(s), "0x") {
		s, base = s[2:], 16
	}
	v, err := strconv.ParseUint(s, base, 64)
	return v, err == nil
}

// ---------------- hex dumps ----------------

var dumpLineRE = regexp.MustCompile(`^\s*(?:0x)?([0-9a-fA-F]{4,16}):\s?(.*)$`)
var hexByteRE = regexp.MustCompile(`^[0-9a-fA-F]{1,2}$`)

// ParseHexDump decodes U-Boot "md.b" and "mtd dump" output: lines of
// "<addr>: b0 b1 ..." with an optional ASCII column. Addresses must be
// contiguous from start and exactly count bytes must be recovered.
func ParseHexDump(text string, start uint64, count int) ([]byte, error) {
	out := make([]byte, 0, count)
	next := start
	for _, line := range strings.Split(normNL(text), "\n") {
		if len(out) >= count {
			break
		}
		m := dumpLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		addr, err := strconv.ParseUint(m[1], 16, 64)
		if err != nil || addr != next {
			continue
		}
		want := count - len(out)
		if want > 16 {
			want = 16
		}
		toks := strings.Fields(m[2])
		if len(toks) < want {
			return nil, fmt.Errorf("short dump line at 0x%x", addr)
		}
		for _, t := range toks[:want] {
			if !hexByteRE.MatchString(t) {
				return nil, fmt.Errorf("bad byte %q at 0x%x", t, addr)
			}
			v, _ := strconv.ParseUint(t, 16, 8)
			out = append(out, byte(v))
		}
		next += uint64(want)
	}
	if len(out) != count {
		return out, fmt.Errorf("dump incomplete: %d of %d bytes", len(out), count)
	}
	return out, nil
}

var hexPairsLineRE = regexp.MustCompile(`^\s*(?:[0-9a-fA-F]{2}\s+)*[0-9a-fA-F]{2}\s*$`)
var hexdumpCRE = regexp.MustCompile(`^([0-9a-fA-F]{8})\s+((?:[0-9a-fA-F]{2}\s+){1,16})`)
var xxdPlainRE = regexp.MustCompile(`^[0-9a-fA-F]{2,}$`)

// ParseLinuxHex decodes "od -An -v -tx1", "hexdump -C" or "xxd -p" output.
// The format is decided once for the whole text, then only matching lines are
// read, so dd statistics, offsets and kernel messages are skipped.
func ParseLinuxHex(text string) []byte {
	lines := strings.Split(normNL(text), "\n")
	mode := "od"
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if hexdumpCRE.MatchString(t) {
			mode = "hexdump"
			break
		}
		if len(t) >= 32 && xxdPlainRE.MatchString(t) {
			mode = "xxd"
		}
	}
	var out []byte
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch mode {
		case "hexdump":
			if m := hexdumpCRE.FindStringSubmatch(t); m != nil {
				for _, x := range strings.Fields(m[2]) {
					v, _ := strconv.ParseUint(x, 16, 8)
					out = append(out, byte(v))
				}
			}
		case "xxd":
			if xxdPlainRE.MatchString(t) && len(t)%2 == 0 {
				for i := 0; i < len(t); i += 2 {
					v, _ := strconv.ParseUint(t[i:i+2], 16, 8)
					out = append(out, byte(v))
				}
			}
		default:
			if hexPairsLineRE.MatchString(t) {
				for _, x := range strings.Fields(t) {
					v, _ := strconv.ParseUint(x, 16, 8)
					out = append(out, byte(v))
				}
			}
		}
	}
	return out
}

// ---------------- MTD ----------------

// MTDDevice is one "* name" block of U-Boot "mtd list".
type MTDDevice struct {
	Name        string    `json:"name"`
	Device      string    `json:"device,omitempty"`
	Parent      string    `json:"parent,omitempty"`
	Driver      string    `json:"driver,omitempty"`
	Path        string    `json:"path,omitempty"`
	Type        string    `json:"type,omitempty"`
	EraseSize   uint64    `json:"erase_size,omitempty"`
	MinIO       uint64    `json:"min_io,omitempty"`
	OOB         uint64    `json:"oob_size,omitempty"`
	OOBAvail    uint64    `json:"oob_available,omitempty"`
	ECCStrength uint64    `json:"ecc_strength,omitempty"`
	ECCStep     uint64    `json:"ecc_step_size,omitempty"`
	Start       uint64    `json:"start"`
	End         uint64    `json:"end"`
	Parts       []MTDPart `json:"partitions,omitempty"`
}

// MTDPart is a partition line inside a device block.
type MTDPart struct {
	Name  string `json:"name"`
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
	Level int    `json:"level"`
}

var (
	mtdDevRE   = regexp.MustCompile(`^\* (\S+)\s*$`)
	mtdKVRE    = regexp.MustCompile(`^\s+- ([A-Za-z /.]+?): (.*)$`)
	mtdRangeRE = regexp.MustCompile(`^(\t*)\s*- 0x([0-9a-fA-F]+)-0x([0-9a-fA-F]+) : "(.*)"\s*$`)
	firstNumRE = regexp.MustCompile(`(0x[0-9a-fA-F]+|[0-9]+)`)
)

func firstNum(s string) uint64 {
	m := firstNumRE.FindString(s)
	v, _ := parseNum(m)
	return v
}

// ParseUBootMTDList parses U-Boot "mtd list".
func ParseUBootMTDList(text string) []MTDDevice {
	var devs []MTDDevice
	var cur *MTDDevice
	for _, line := range strings.Split(normNL(text), "\n") {
		if m := mtdDevRE.FindStringSubmatch(line); m != nil {
			devs = append(devs, MTDDevice{Name: m[1]})
			cur = &devs[len(devs)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if m := mtdRangeRE.FindStringSubmatch(line); m != nil {
			a, _ := strconv.ParseUint(m[2], 16, 64)
			b, _ := strconv.ParseUint(m[3], 16, 64)
			level := len(m[1])
			if m[4] == cur.Name && level == 0 && cur.End == 0 {
				cur.Start, cur.End = a, b
			} else {
				if level == 0 {
					level = 1
				}
				cur.Parts = append(cur.Parts, MTDPart{Name: m[4], Start: a, End: b, Level: level})
			}
			continue
		}
		if m := mtdKVRE.FindStringSubmatch(line); m != nil {
			k, v := strings.ToLower(strings.TrimSpace(m[1])), strings.TrimSpace(m[2])
			switch k {
			case "device":
				cur.Device = v
			case "parent":
				cur.Parent = v
			case "driver":
				cur.Driver = v
			case "path":
				cur.Path = v
			case "type":
				cur.Type = v
			case "block size":
				cur.EraseSize = firstNum(v)
			case "min i/o":
				cur.MinIO = firstNum(v)
			case "oob size":
				cur.OOB = firstNum(v)
			case "oob available":
				cur.OOBAvail = firstNum(v)
			case "ecc strength":
				cur.ECCStrength = firstNum(v)
			case "ecc step size":
				cur.ECCStep = firstNum(v)
			}
		}
	}
	return devs
}

// ProcMTD is one line of Linux /proc/mtd.
type ProcMTD struct {
	Index     int    `json:"index"`
	Size      uint64 `json:"size"`
	EraseSize uint64 `json:"erase_size"`
	Name      string `json:"name"`
}

var procMTDRE = regexp.MustCompile(`(?m)^mtd([0-9]+):\s+([0-9a-fA-F]+)\s+([0-9a-fA-F]+)\s+"(.*)"\s*$`)

// ParseProcMTD parses /proc/mtd.
func ParseProcMTD(text string) []ProcMTD {
	var out []ProcMTD
	for _, m := range procMTDRE.FindAllStringSubmatch(normNL(text), -1) {
		i, _ := strconv.Atoi(m[1])
		sz, _ := strconv.ParseUint(m[2], 16, 64)
		es, _ := strconv.ParseUint(m[3], 16, 64)
		out = append(out, ProcMTD{Index: i, Size: sz, EraseSize: es, Name: m[4]})
	}
	return out
}

var sysMTDRE = regexp.MustCompile(`(?m)^/sys/class/mtd/mtd([0-9]+)/([a-z_]+):(.*)$`)

// ParseSysClassMTD parses "grep -H . /sys/class/mtd/mtd*/attr" output.
func ParseSysClassMTD(text string) map[int]map[string]string {
	out := map[int]map[string]string{}
	for _, m := range sysMTDRE.FindAllStringSubmatch(normNL(text), -1) {
		i, _ := strconv.Atoi(m[1])
		if out[i] == nil {
			out[i] = map[string]string{}
		}
		out[i][m[2]] = strings.TrimSpace(m[3])
	}
	return out
}

// ---------------- UBI ----------------

// UBIDevice is the attached UBI device as reported by U-Boot or Linux.
type UBIDevice struct {
	MTD          string      `json:"mtd,omitempty"`
	PEBSize      uint64      `json:"peb_size,omitempty"`
	LEBSize      uint64      `json:"leb_size,omitempty"`
	MinIO        uint64      `json:"min_io,omitempty"`
	SubPage      uint64      `json:"sub_page_size,omitempty"`
	VIDHdrOffset uint64      `json:"vid_header_offset,omitempty"`
	DataOffset   uint64      `json:"data_offset,omitempty"`
	GoodPEBs     uint64      `json:"good_pebs,omitempty"`
	BadPEBs      *uint64     `json:"bad_pebs,omitempty"`
	VolumeCount  *uint64     `json:"volume_count,omitempty"`
	Volumes      []UBIVolume `json:"volumes,omitempty"`
}

// UBIVolume is one UBI volume.
type UBIVolume struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	ReservedPEBs uint64 `json:"reserved_pebs,omitempty"`
	SizeBytes    uint64 `json:"size_bytes,omitempty"`
	UsedBytes    uint64 `json:"used_bytes,omitempty"`
	Alignment    uint64 `json:"alignment,omitempty"`
}

func findUint(text string, re *regexp.Regexp) (uint64, bool) {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	return parseNum(m[1])
}

var (
	ubUMTD     = regexp.MustCompile(`MTD device name:\s+"([^"]+)"`)
	ubUPEB     = regexp.MustCompile(`physical eraseblock size:\s+([0-9]+)`)
	ubULEB     = regexp.MustCompile(`logical eraseblock size:\s+([0-9]+)`)
	ubUGood    = regexp.MustCompile(`number of good PEBs:\s+([0-9]+)`)
	ubUBad     = regexp.MustCompile(`number of bad PEBs:\s+([0-9]+)`)
	ubUMinIO   = regexp.MustCompile(`smallest flash I/O unit:\s+([0-9]+)`)
	ubUVID     = regexp.MustCompile(`VID header offset:\s+([0-9]+)`)
	ubUData    = regexp.MustCompile(`data offset:\s+([0-9]+)`)
	ubUUserVol = regexp.MustCompile(`number of user volumes:\s+([0-9]+)`)
	ubVolBlock = regexp.MustCompile(`(?s)Volume information dump:(.*?)(?:\n\s*\n|\z|Volume information dump:)`)
)

// ParseUBootUBIInfo parses "ubi info" and "ubi info layout" together.
func ParseUBootUBIInfo(info, layout string) UBIDevice {
	var d UBIDevice
	info = normNL(info)
	if m := ubUMTD.FindStringSubmatch(info); m != nil {
		d.MTD = m[1]
	}
	d.PEBSize, _ = findUint(info, ubUPEB)
	d.LEBSize, _ = findUint(info, ubULEB)
	d.GoodPEBs, _ = findUint(info, ubUGood)
	if v, ok := findUint(info, ubUBad); ok {
		d.BadPEBs = &v
	}
	d.MinIO, _ = findUint(info, ubUMinIO)
	d.VIDHdrOffset, _ = findUint(info, ubUVID)
	d.DataOffset, _ = findUint(info, ubUData)
	if v, ok := findUint(info, ubUUserVol); ok {
		d.VolumeCount = &v
	}
	d.Volumes = parseUBootVolumes(layout, d.LEBSize)
	return d
}

func parseUBootVolumes(layout string, leb uint64) []UBIVolume {
	var vols []UBIVolume
	parts := strings.Split(normNL(layout), "Volume information dump:")
	for _, p := range parts[1:] {
		kv := map[string]string{}
		for _, line := range strings.Split(p, "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kv[f[0]] = strings.Join(f[1:], " ")
			}
		}
		id, err := strconv.Atoi(kv["vol_id"])
		if err != nil {
			continue
		}
		v := UBIVolume{ID: id, Name: kv["name"]}
		switch kv["vol_type"] {
		case "3":
			v.Type = "dynamic"
		case "4":
			v.Type = "static"
		}
		v.ReservedPEBs, _ = parseNum(kv["reserved_pebs"])
		v.UsedBytes, _ = parseNum(kv["used_bytes"])
		v.Alignment, _ = parseNum(kv["alignment"])
		ul, _ := parseNum(kv["usable_leb_size"])
		if ul == 0 {
			ul = leb
		}
		v.SizeBytes = v.ReservedPEBs * ul
		vols = append(vols, v)
	}
	sort.Slice(vols, func(i, j int) bool { return vols[i].ID < vols[j].ID })
	return vols
}

var (
	ubiLDev     = regexp.MustCompile(`(?m)^ubi([0-9]+)\s*$`)
	ubiLLEB     = regexp.MustCompile(`Logical eraseblock size:\s+([0-9]+)`)
	ubiLMinIO   = regexp.MustCompile(`Minimum input/output unit size:\s+([0-9]+)`)
	ubiLVolCnt  = regexp.MustCompile(`Volumes count:\s+([0-9]+)`)
	ubiLBad     = regexp.MustCompile(`Count of bad physical eraseblocks:\s+([0-9]+)`)
	ubiLVolID   = regexp.MustCompile(`Volume ID:\s+([0-9]+)`)
	ubiLType    = regexp.MustCompile(`Type:\s+(\w+)`)
	ubiLAlign   = regexp.MustCompile(`Alignment:\s+([0-9]+)`)
	ubiLSize    = regexp.MustCompile(`Size:\s+([0-9]+) LEBs \(([0-9]+) bytes`)
	ubiLName    = regexp.MustCompile(`(?m)Name:\s+(.*)$`)
	dmesgPEB    = regexp.MustCompile(`ubi[0-9]+: PEB size: ([0-9]+) bytes.*LEB size: ([0-9]+) bytes`)
	dmesgIO     = regexp.MustCompile(`ubi[0-9]+: min\./max\. I/O unit sizes: ([0-9]+)/[0-9]+, sub-page size ([0-9]+)`)
	dmesgVID    = regexp.MustCompile(`ubi[0-9]+: VID header offset: ([0-9]+) \(aligned [0-9]+\), data offset: ([0-9]+)`)
	dmesgAttach = regexp.MustCompile(`ubi[0-9]+: attaching mtd([0-9]+)`)
	dmesgGood   = regexp.MustCompile(`ubi[0-9]+: good PEBs: ([0-9]+), bad PEBs: ([0-9]+)`)
)

// ParseUbinfo parses "ubinfo -a" (first device) and fills PEB and header
// geometry from the kernel's attach messages when dmesg is given.
func ParseUbinfo(text, dmesg string) (UBIDevice, bool) {
	text = normNL(text)
	var d UBIDevice
	if !ubiLDev.MatchString(text) {
		return d, false
	}
	d.LEBSize, _ = findUint(text, ubiLLEB)
	d.MinIO, _ = findUint(text, ubiLMinIO)
	if v, ok := findUint(text, ubiLVolCnt); ok {
		d.VolumeCount = &v
	}
	if v, ok := findUint(text, ubiLBad); ok {
		d.BadPEBs = &v
	}
	blocks := strings.Split(text, "Volume ID:")
	for _, b := range blocks[1:] {
		b = "Volume ID:" + b
		var v UBIVolume
		id, _ := findUint(b, ubiLVolID)
		v.ID = int(id)
		if m := ubiLType.FindStringSubmatch(b); m != nil {
			v.Type = m[1]
		}
		v.Alignment, _ = findUint(b, ubiLAlign)
		if m := ubiLSize.FindStringSubmatch(b); m != nil {
			v.ReservedPEBs, _ = parseNum(m[1])
			v.SizeBytes, _ = parseNum(m[2])
		}
		if m := ubiLName.FindStringSubmatch(b); m != nil {
			v.Name = strings.TrimSpace(m[1])
		}
		d.Volumes = append(d.Volumes, v)
	}
	dmesg = normNL(dmesg)
	if m := dmesgPEB.FindStringSubmatch(dmesg); m != nil {
		d.PEBSize, _ = parseNum(m[1])
		if d.LEBSize == 0 {
			d.LEBSize, _ = parseNum(m[2])
		}
	}
	if m := dmesgIO.FindStringSubmatch(dmesg); m != nil {
		d.SubPage, _ = parseNum(m[2])
	}
	if m := dmesgVID.FindStringSubmatch(dmesg); m != nil {
		d.VIDHdrOffset, _ = parseNum(m[1])
		d.DataOffset, _ = parseNum(m[2])
	}
	if m := dmesgGood.FindStringSubmatch(dmesg); m != nil {
		d.GoodPEBs, _ = parseNum(m[1])
	}
	if m := dmesgAttach.FindStringSubmatch(dmesg); m != nil {
		d.MTD = "mtd" + m[1]
	}
	return d, true
}

// ---------------- U-Boot misc ----------------

var envLineRE = regexp.MustCompile(`^([A-Za-z0-9_.#:+-]+)=(.*)$`)

// ParsePrintenv parses printenv / fw_printenv output.
func ParsePrintenv(text string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(normNL(text), "\n") {
		if m := envLineRE.FindStringSubmatch(strings.TrimRight(line, " ")); m != nil {
			env[m[1]] = m[2]
		}
	}
	return env
}

// BDInfo is the part of "bdinfo" the probe uses.
type BDInfo struct {
	Values    map[string]string `json:"values"`
	DRAMBanks [][2]uint64       `json:"dram_banks,omitempty"`
	RelocAddr uint64            `json:"relocaddr,omitempty"`
	FDTBlob   uint64            `json:"fdt_blob,omitempty"`
}

var bdLineRE = regexp.MustCompile(`^\s*(\S.*?)\s*= (.*)$`)

// ParseBDInfo parses U-Boot "bdinfo".
func ParseBDInfo(text string) BDInfo {
	bd := BDInfo{Values: map[string]string{}}
	var start uint64
	haveStart := false
	for _, line := range strings.Split(normNL(text), "\n") {
		m := bdLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		k, v := m[1], strings.TrimSpace(m[2])
		switch k {
		case "-> start":
			start, haveStart = firstNum(v), true
		case "-> size":
			if haveStart {
				bd.DRAMBanks = append(bd.DRAMBanks, [2]uint64{start, firstNum(v)})
				haveStart = false
			}
		case "relocaddr":
			bd.RelocAddr = firstNum(v)
		case "fdt_blob":
			bd.FDTBlob = firstNum(v)
		}
		if _, dup := bd.Values[k]; !dup {
			bd.Values[k] = v
		}
	}
	return bd
}

var helpLineRE = regexp.MustCompile(`^([A-Za-z0-9_.?-]+)\s+- (.*)$`)

// ParseHelp returns U-Boot command names from "help".
func ParseHelp(text string) map[string]string {
	cmds := map[string]string{}
	for _, line := range strings.Split(normNL(text), "\n") {
		if m := helpLineRE.FindStringSubmatch(strings.TrimRight(line, " ")); m != nil {
			cmds[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return cmds
}

// PHYInfo is one PHY reported by U-Boot "mii info".
type PHYInfo struct {
	Addr  uint64 `json:"mdio_addr"`
	OUI   string `json:"oui"`
	Model string `json:"model"`
	Rev   string `json:"rev"`
	ID    string `json:"phy_id,omitempty"`
	Line  string `json:"raw"`
}

var miiRE = regexp.MustCompile(`PHY 0x([0-9a-fA-F]+): OUI = 0x([0-9a-fA-F]+), Model = 0x([0-9a-fA-F]+), Rev = 0x([0-9a-fA-F]+)`)

// ParseMiiInfo parses U-Boot "mii info".
func ParseMiiInfo(text string) []PHYInfo {
	var out []PHYInfo
	for _, line := range strings.Split(normNL(text), "\n") {
		m := miiRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		a, _ := strconv.ParseUint(m[1], 16, 64)
		out = append(out, PHYInfo{Addr: a, OUI: "0x" + m[2], Model: "0x" + m[3], Rev: "0x" + m[4], Line: strings.TrimSpace(line)})
	}
	return out
}

// ---------------- Linux misc ----------------

// NetIf is one interface from "ip link".
type NetIf struct {
	Name   string `json:"name"`
	Master string `json:"dsa_master,omitempty"`
	Flags  string `json:"flags,omitempty"`
	Driver string `json:"driver,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

var ipLinkRE = regexp.MustCompile(`(?m)^[0-9]+: ([^:@ ]+)(?:@([^: ]+))?: <([^>]*)>`)

// ParseIPLink parses "ip link".
func ParseIPLink(text string) []NetIf {
	var out []NetIf
	for _, m := range ipLinkRE.FindAllStringSubmatch(normNL(text), -1) {
		n := NetIf{Name: m[1], Flags: m[3]}
		if m[2] != "" && m[2] != "NONE" {
			n.Master = m[2]
		}
		out = append(out, n)
	}
	return out
}

// ParseKV parses "key: value" lines (ethtool -i, cpuinfo, meminfo).
func ParseKV(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(normNL(text), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, dup := out[k]; !dup {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// ParseShellVars parses KEY='value' files such as /etc/openwrt_release.
func ParseShellVars(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(normNL(text), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			continue
		}
		out[k] = strings.Trim(v, `"'`)
	}
	return out
}

// ---------------- boot log ----------------

// BootLog is what the UART text says about the boot chain and the hardware.
type BootLog struct {
	SoCs        []string `json:"socs,omitempty"`
	BL2         string   `json:"bl2,omitempty"`
	BL31        string   `json:"bl31,omitempty"`
	UBoot       string   `json:"uboot,omitempty"`
	Model       string   `json:"model,omitempty"`
	DRAM        string   `json:"dram,omitempty"`
	CPU         string   `json:"cpu,omitempty"`
	Kernel      string   `json:"kernel,omitempty"`
	OS          string   `json:"os,omitempty"`
	FlashLines  []string `json:"flash_lines,omitempty"`
	FlashVendor string   `json:"flash_vendor,omitempty"`
	FlashGeom   string   `json:"flash_geometry,omitempty"`
	IDLines     []string `json:"id_lines,omitempty"`
	PressX      bool     `json:"press_x"`
	XmodemC     bool     `json:"xmodem_c"`
	TCBoot      bool     `json:"tcboot"`
}

var (
	socRE        = regexp.MustCompile(`(?i)\b([ae]n75[0-9]{2})(?:[^0-9]|$)`)
	bl2VerRE     = regexp.MustCompile(`NOTICE:\s+BL2:\s+(v[^\s].*)$`)
	bl31VerRE    = regexp.MustCompile(`NOTICE:\s+BL31:\s+(v[^\s].*)$`)
	ubootVerRE   = regexp.MustCompile(`(U-Boot(?: SPL)? \d{4}\.\d{2}[^\s]*(?: \([^)]*\))?)`)
	modelLineRE  = regexp.MustCompile(`^\s*Model:\s+(.+)$`)
	dramLineRE   = regexp.MustCompile(`^\s*DRAM:\s+(.+)$`)
	cpuLineRE    = regexp.MustCompile(`^\s*(?:CPU|SoC):\s+(.+)$`)
	kernelVerRE  = regexp.MustCompile(`Linux version (\S+)`)
	osRE         = regexp.MustCompile(`(OpenWrt [^\s,]+(?: [^\s,]+)?)`)
	flashLineRE  = regexp.MustCompile(`(?i)spi[- _]?nand|spi[- _]?nor|\bnand\b|jedec|flash id|\bmmc\d?\b|emmc`)
	spinandFound = regexp.MustCompile(`(?i)(\S+) SPI NAND was found`)
	spinandGeom  = regexp.MustCompile(`(?i)([0-9]+ MiB, block size: [0-9]+ KiB, page size: [0-9]+, OOB size: [0-9]+)`)
	idLineRE     = regexp.MustCompile(`(?i)chip[ _-]?id|chip[ _-]?rev|soc[ _-]?rev|efuse|\bhw[ _-]?ver|board[ _-]?id`)
)

// knownFlashVendors are names that appear in SPI-NAND/NOR identification.
var knownFlashVendors = []string{"Fudan", "SkyHigh", "Winbond", "Macronix", "GigaDevice", "Micron", "Toshiba", "Kioxia", "ESMT", "XTX", "Dosilicon", "Foresee", "Etron", "Zbit", "Paragon", "HeYangTek", "Alliance", "ATO", "Spansion", "Cypress", "Samsung", "Hynix"}

// ParseBootLog extracts boot-chain and identity hints from UART text.
func ParseBootLog(text string) BootLog {
	var b BootLog
	seenSoC := map[string]bool{}
	for _, raw := range strings.Split(normNL(text), "\n") {
		line := strings.TrimSpace(stripANSI(raw))
		if line == "" {
			continue
		}
		for _, m := range socRE.FindAllStringSubmatch(line, -1) {
			v := strings.ToUpper(m[1])
			if !seenSoC[v] {
				seenSoC[v] = true
				b.SoCs = append(b.SoCs, v)
			}
		}
		if m := bl2VerRE.FindStringSubmatch(line); m != nil && b.BL2 == "" {
			b.BL2 = m[1]
		}
		if m := bl31VerRE.FindStringSubmatch(line); m != nil && b.BL31 == "" {
			b.BL31 = m[1]
		}
		if m := ubootVerRE.FindStringSubmatch(line); m != nil && b.UBoot == "" {
			b.UBoot = m[1]
		}
		if m := modelLineRE.FindStringSubmatch(line); m != nil && b.Model == "" {
			b.Model = m[1]
		}
		if m := dramLineRE.FindStringSubmatch(line); m != nil && b.DRAM == "" {
			b.DRAM = m[1]
		}
		if m := cpuLineRE.FindStringSubmatch(line); m != nil && b.CPU == "" {
			b.CPU = m[1]
		}
		if m := kernelVerRE.FindStringSubmatch(line); m != nil && b.Kernel == "" {
			b.Kernel = m[1]
		}
		if m := osRE.FindStringSubmatch(line); m != nil && b.OS == "" {
			b.OS = m[1]
		}
		if flashLineRE.MatchString(line) && len(b.FlashLines) < 40 {
			b.FlashLines = append(b.FlashLines, line)
		}
		if m := spinandFound.FindStringSubmatch(line); m != nil && b.FlashVendor == "" {
			b.FlashVendor = m[1]
		}
		if m := spinandGeom.FindStringSubmatch(line); m != nil && b.FlashGeom == "" {
			b.FlashGeom = m[1]
		}
		if idLineRE.MatchString(line) && len(b.IDLines) < 40 {
			b.IDLines = append(b.IDLines, line)
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "press x") {
			b.PressX = true
		}
		if strings.Contains(line, "CCC") {
			b.XmodemC = true
		}
		if strings.Contains(low, "tcboot") || strings.Contains(low, "bldr>") {
			b.TCBoot = true
		}
	}
	if b.FlashVendor == "" {
		b.FlashVendor = vendorFrom(strings.Join(b.FlashLines, "\n"))
	}
	return b
}

func vendorFrom(text string) string {
	low := strings.ToLower(text)
	for _, v := range knownFlashVendors {
		if strings.Contains(low, strings.ToLower(v)) {
			return v
		}
	}
	return ""
}

// ErrNoData is returned by analysis helpers when a source file is absent.
var ErrNoData = errors.New("no data")
