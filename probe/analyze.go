package probe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type src struct {
	dir string
}

func (s src) text(rel string) string {
	b, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return normNL(string(b))
}

func (s src) bytes(rel string) []byte {
	b, _ := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
	return b
}

func (s src) has(rel string) bool {
	st, err := os.Stat(filepath.Join(s.dir, filepath.FromSlash(rel)))
	return err == nil && !st.IsDir() && st.Size() > 0
}

func (s src) glob(pattern string) []string {
	m, _ := filepath.Glob(filepath.Join(s.dir, filepath.FromSlash(pattern)))
	var out []string
	for _, x := range m {
		r, _ := filepath.Rel(s.dir, x)
		out = append(out, filepath.ToSlash(r))
	}
	sort.Strings(out)
	return out
}

// commandOK reports whether a collected command file exists and its last
// recorded status was not a failure.
func (s src) commandOK(rel string) bool {
	if !s.has(rel) {
		return false
	}
	st := s.text(strings.SplitN(rel, "/", 2)[0] + "/_status.jsonl")
	ok := true
	for _, line := range strings.Split(st, "\n") {
		var c cmdStatus
		if json.Unmarshal([]byte(line), &c) == nil && c.File == rel {
			ok = c.Error == "" && c.RC <= 0
		}
	}
	return ok && !looksFailed(s.text(rel))
}

var (
	sensitiveEnvRE = regexp.MustCompile(`(?i)^(eth[0-9]*addr|ethaddr|wlan.*mac|.*mac(addr(ess)?)?[0-9]*|serial#?|serial_?(no|num(ber)?)|sn|.*_sn|gpon.*|ploam.*|.*passw(or)?d.*|.*_key|.*secret.*|.*token.*|.*pin|wps.*|ssid.*|uuid|device_?id|board_?serial|hw_?serial|loid.*)$`)
	identityPartRE = regexp.MustCompile(`(?i)^(factory|art|cal(ib(ration)?|data)?|eeprom|rf|rfdata|mac|macaddr|identity|romfile|mib[0-9]*|ri|bosa|nvram|config|persist|devinfo|hwinfo|board_?data|serial|gpon|ploam|u-?boot-?env[0-9]*|env|reserve[d]?|bdinfo|wifi_?cal|radio|odm|oem|mfg|manufacture)$`)
	hwRevEnvRE     = regexp.MustCompile(`(?i)^(hw_?ver(sion)?|hardware_?ver(sion)?|board_?rev(ision)?|hwrev)$`)
	socNormRE      = regexp.MustCompile(`(?i)^([ae])n(75[0-9]{2})$`)
	macRE          = regexp.MustCompile(`(?i)\b([0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`)
	sizeWithUnitRE = regexp.MustCompile(`(?i)([0-9.]+)\s*(KiB|MiB|GiB|kB|MB|GB)`)
)

// normSoC maps EcoNet-era names to the Airoha ones used by OpenWrt/UrsusBoot.
func normSoC(s string) string {
	m := socNormRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return strings.ToUpper(strings.TrimSpace(s))
	}
	if strings.ToUpper(m[1]) == "E" && m[2] == "7581" {
		return "AN7581"
	}
	return strings.ToUpper(m[1]) + "N" + m[2]
}

func parseSizeUnit(s string) (uint64, bool) {
	m := sizeWithUnitRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	mult := map[string]float64{"kib": 1 << 10, "kb": 1 << 10, "mib": 1 << 20, "mb": 1 << 20, "gib": 1 << 30, "gb": 1 << 30}[strings.ToLower(m[2])]
	return uint64(f * mult), true
}

// AnalyzeOptions describe the tool that wrote the profile.
type AnalyzeOptions struct {
	Tool    string
	Version string
	Now     func() time.Time
}

// Analyze builds the profile and every derived file from a probe directory.
// It is deterministic for a given set of collected files.
func Analyze(dir string, ao AnalyzeOptions) (*Profile, error) {
	if ao.Now == nil {
		ao.Now = time.Now
	}
	s := src{dir}
	p := &Profile{
		Schema:       SchemaV1,
		Generator:    map[string]string{"tool": ao.Tool, "version": ao.Version},
		Created:      ao.Now().UTC().Format(time.RFC3339),
		Device:       map[string]*Fact{},
		BootROM:      map[string]any{},
		UART:         map[string]any{},
		UBoot:        map[string]any{"detected": false},
		DT:           map[string]any{},
		Network:      map[string]any{},
		GPIO:         map[string]any{},
		Linux:        map[string]any{"detected": false},
		Capabilities: map[string]any{},
		Artifacts:    map[string]string{},
		Completeness: map[string]any{},
		Warnings:     []string{},
		Conflicts:    []Conflict{},
		MTD:          []MTDEntry{},
		Identity:     []IdentityEntry{},
		BootChain:    []BootStage{},
	}
	a := &analysis{s: s, p: p}
	a.load()
	a.uart()
	a.bootrom()
	a.dts()
	a.uboot()
	a.linux()
	a.soc()
	a.device()
	a.flash()
	a.mtdMap()
	a.ubi()
	a.bootChain()
	a.network()
	a.gpio()
	a.identity()
	a.completeness()
	if err := a.writeDerived(); err != nil {
		return p, err
	}
	a.artifacts()
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return p, err
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), b, 0o644); err != nil {
		return p, err
	}
	if err := WriteReports(dir, p); err != nil {
		return p, err
	}
	return p, nil
}

type analysis struct {
	s src
	p *Profile

	log       string
	boot      BootLog
	ubSource  string
	ubPrefix  string
	ubConf    string
	env       map[string]string
	fwenv     map[string]string
	bd        BDInfo
	help      map[string]string
	ubDevs    []MTDDevice
	procMTD   []ProcMTD
	sysMTD    map[int]map[string]string
	dmesg     string
	metas     []DTMeta
	samples   []SampleRecord
	hashes    []map[string]any
	ubiU      []UBIDevice
	ubiL      *UBIDevice
	ubootSeen bool
	linuxSeen bool
}

func (a *analysis) load() {
	s := a.s
	// Boot messages only: command responses (hex dumps, DT strings) would
	// otherwise be mistaken for boot-log evidence.
	a.log = s.text("uart/boot-console.log")
	if a.log == "" {
		a.log = s.text("uart.log")
	}
	a.boot = ParseBootLog(a.log)
	a.ubSource = "device"
	var sess map[string]any
	if json.Unmarshal(s.bytes("uboot/session.json"), &sess) == nil {
		if v, ok := sess["source"].(string); ok && v != "" {
			a.ubSource = v
		}
	}
	a.ubPrefix, a.ubConf = "u-boot", Medium
	if a.ubSource == "ursido-ram-uboot" {
		// Our own RAM U-Boot describes the board as UrsidoRescue assumes it.
		a.ubPrefix, a.ubConf = "ram-uboot", Low
		a.p.warn("U-Boot data comes from UrsidoRescue's own RAM U-Boot: its environment, device tree and partition table are assumptions, not device facts (flash chip probing is real)")
	}
	a.ubootSeen = s.commandOK("uboot/version.txt")
	a.env = ParsePrintenv(s.text("uboot/env.txt"))
	a.fwenv = ParsePrintenv(s.text("linux/fw_printenv.txt"))
	a.bd = ParseBDInfo(s.text("uboot/bdinfo.txt"))
	a.help = ParseHelp(s.text("uboot/help.txt"))
	a.ubDevs = ParseUBootMTDList(s.text("uboot/mtd-list.txt"))
	a.procMTD = ParseProcMTD(s.text("linux/proc-mtd.txt"))
	a.sysMTD = ParseSysClassMTD(s.text("flash/sys-class-mtd.txt"))
	a.dmesg = s.text("linux/dmesg.txt")
	a.linuxSeen = s.has("linux/uname.txt")
	a.samples = readSamples(s.dir)
	for _, line := range strings.Split(s.text("flash/hashes.jsonl"), "\n") {
		var h map[string]any
		if json.Unmarshal([]byte(line), &h) == nil {
			a.hashes = append(a.hashes, h)
		}
	}
	for _, info := range s.glob("ubi/uboot-*-info.txt") {
		layout := strings.TrimSuffix(info, "-info.txt") + "-layout.txt"
		d := ParseUBootUBIInfo(s.text(info), s.text(layout))
		if d.PEBSize != 0 || len(d.Volumes) > 0 {
			a.ubiU = append(a.ubiU, d)
		}
	}
	if d, ok := ParseUbinfo(s.text("ubi/ubinfo.txt"), a.dmesg); ok {
		if d.MTD != "" {
			if n, err := strconv.Atoi(strings.TrimPrefix(d.MTD, "mtd")); err == nil {
				for _, pm := range a.procMTD {
					if pm.Index == n {
						d.MTD = pm.Name
					}
				}
			}
		}
		a.ubiL = &d
	}
}

func (a *analysis) uart() {
	var params map[string]any
	_ = json.Unmarshal(a.s.bytes("uart/params.json"), &params)
	for k, v := range params {
		a.p.UART[k] = v
	}
	if w, ok := params["warning"].(string); ok {
		a.p.warn("UART: %s", w)
	}
	var ev []Event
	_ = json.Unmarshal(a.s.bytes("uart/events.json"), &ev)
	first := map[string]int64{}
	count := map[string]int{}
	for _, e := range ev {
		if _, ok := first[e.Marker]; !ok {
			first[e.Marker] = e.OffsetMS
		}
		count[e.Marker]++
	}
	markers := map[string]any{}
	for _, k := range sortedKeys(first) {
		markers[k] = map[string]any{"first_offset_ms": first[k], "count": count[k]}
	}
	a.p.UART["markers"] = markers
	a.p.UART["log"] = "uart.log"
	a.p.UART["events"] = "uart/events.json"
}

func (a *analysis) bootrom() {
	var br map[string]any
	if json.Unmarshal(a.s.bytes("bootrom/bootrom.json"), &br) == nil {
		for k, v := range br {
			if k == "marker_events" {
				continue
			}
			a.p.BootROM[k] = v
		}
	}
	if a.boot.PressX {
		a.p.BootROM["detected"] = true
		a.p.BootROM["press_x"] = true
		if a.p.BootROM["entry_sequence"] == nil || a.p.BootROM["entry_sequence"] == "" {
			a.p.BootROM["entry_sequence"] = "press-x"
		}
	}
	if _, ok := a.p.BootROM["detected"]; !ok {
		a.p.BootROM["detected"] = false
	}
	a.p.BootROM["source"] = []string{"uart-log", "bootrom/bootrom.json"}
}

func (a *analysis) dts() {
	for _, name := range []string{"linux", "uboot"} {
		rel := "dt/fdt-" + name + ".dtb"
		b := a.s.bytes(rel)
		if len(b) == 0 {
			continue
		}
		f, err := ParseFDT(b)
		if err != nil {
			a.p.warn("%s: %v", rel, err)
			continue
		}
		label := "dt-" + name
		if name == "uboot" && a.ubSource == "ursido-ram-uboot" {
			label = "dt-ram-uboot"
		}
		meta := ExtractDTMeta(f, label)
		a.metas = append(a.metas, meta)
		_ = os.WriteFile(filepath.Join(a.s.dir, "dt", "fdt-"+name+".dts"), []byte(f.DTS()), 0o644)
		mb, _ := json.MarshalIndent(meta, "", "  ")
		_ = os.WriteFile(filepath.Join(a.s.dir, "dt", "dt-meta-"+name+".json"), mb, 0o644)
		// fdt.dtb/fdt.dts is the kernel's tree when present, else U-Boot's.
		if !a.s.has("dt/fdt.dtb") || name == "linux" {
			_ = os.WriteFile(filepath.Join(a.s.dir, "dt", "fdt.dtb"), b, 0o644)
			_ = os.WriteFile(filepath.Join(a.s.dir, "dt", "fdt.dts"), []byte(f.DTS()), 0o644)
		}
	}
	if len(a.metas) == 0 {
		a.p.DT["available"] = false
		return
	}
	a.p.DT["available"] = true
	var sources []string
	for _, m := range a.metas {
		sources = append(sources, m.Source)
	}
	a.p.DT["sources"] = sources
	primary := a.metas[0]
	a.p.DT["primary"] = primary.Source
	a.p.DT["model"] = primary.Model
	a.p.DT["compatible"] = primary.Compatible
	var mem []string
	var total uint64
	for _, r := range primary.Memory {
		mem = append(mem, fmt.Sprintf("0x%x+0x%x", r[0], r[1]))
		total += r[1]
	}
	a.p.DT["memory"] = mem
	a.p.DT["chosen"] = primary.Chosen
	a.p.DT["aliases"] = primary.Aliases
	counts := map[string]int{}
	for k, v := range primary.Nodes {
		counts[k] = len(v)
	}
	a.p.DT["node_counts"] = counts
	a.p.DT["files"] = map[string]string{"dtb": "dt/fdt.dtb", "dts": "dt/fdt.dts"}
	a.p.DT["metadata"] = "dt/dt-meta-*.json"
	if len(a.metas) == 2 && strings.Join(a.metas[0].Compatible, ",") != strings.Join(a.metas[1].Compatible, ",") {
		a.p.warn("device trees differ: %s compatible=%v vs %s compatible=%v", a.metas[0].Source, a.metas[0].Compatible, a.metas[1].Source, a.metas[1].Compatible)
	}
}

func (a *analysis) uboot() {
	s := a.s
	if !a.ubootSeen {
		return
	}
	u := a.p.UBoot
	u["detected"] = true
	u["source"] = a.ubSource
	var sess map[string]any
	if json.Unmarshal(s.bytes("uboot/session.json"), &sess) == nil {
		u["prompt"] = sess["prompt"]
		u["hush"] = sess["hush"]
	}
	ver := strings.TrimSpace(s.text("uboot/version.txt"))
	if m := ubootVerRE.FindStringSubmatch(ver); m != nil {
		u["version"] = m[1]
	}
	u["version_text"] = ver
	for _, k := range []string{"bootcmd", "bootargs", "bootdelay", "loadaddr", "fdtcontroladdr", "fdtfile", "board", "board_name", "soc", "arch", "cpu", "vendor", "ipaddr", "serverip", "bootmenu_0", "preboot", "stdin", "stdout"} {
		if v, ok := a.env[k]; ok {
			u[k] = v
		}
	}
	var keys, sens []string
	for _, k := range sortedKeys(a.env) {
		if sensitiveEnvRE.MatchString(k) {
			sens = append(sens, k)
		} else {
			keys = append(keys, k)
		}
	}
	u["env_keys"] = keys
	u["env_sensitive_keys_redacted"] = sens
	u["env_file"] = "uboot/env.txt (raw, device-specific)"
	if len(a.bd.DRAMBanks) > 0 {
		var banks []string
		for _, b := range a.bd.DRAMBanks {
			banks = append(banks, fmt.Sprintf("0x%x+0x%x", b[0], b[1]))
		}
		u["dram_banks"] = banks
	}
	if a.bd.RelocAddr != 0 {
		u["relocaddr"] = fmt.Sprintf("0x%x", a.bd.RelocAddr)
	}
	var win map[string]any
	if json.Unmarshal(s.bytes("uboot/ram-window.json"), &win) == nil {
		u["ram_window"] = win
	}
	cmds := map[string]bool{}
	for k := range a.help {
		cmds[k] = true
	}
	want := []string{"mtd", "ubi", "nand", "sf", "mmc", "fdt", "tftpboot", "httpd", "crc32", "hash", "md", "mii", "mdio", "loadx", "loady", "loadb", "bootmenu", "dhcp", "ping", "usb", "gpio", "dm", "saveenv", "env", "setexpr", "source", "bootm", "bootz", "booti"}
	named := map[string]bool{}
	for _, k := range want {
		named[k] = cmds[k]
	}
	caps := map[string]any{"commands": named, "all_commands": sortedKeys(cmds), "source": a.ubPrefix + ":help", "note": "commands are listed from 'help'; none were executed to test them"}
	if len(cmds) == 0 {
		caps["note"] = "help output unavailable; capabilities unknown"
		a.p.warn("U-Boot capabilities unknown (help not collected)")
	}
	derived := map[string]bool{
		"uart_xmodem_load": cmds["loadx"] || cmds["loady"],
		"tftp_recovery":    cmds["tftpboot"],
		"http_recovery":    cmds["httpd"],
		"mtd_access":       cmds["mtd"],
		"ubi_access":       cmds["ubi"],
		"sha256_hash":      cmds["hash"],
	}
	caps["derived"] = derived
	a.p.Capabilities = caps
}

func (a *analysis) linux() {
	s := a.s
	if !a.linuxSeen {
		return
	}
	l := a.p.Linux
	l["detected"] = true
	uname := strings.TrimSpace(s.text("linux/uname.txt"))
	l["uname"] = uname
	if f := strings.Fields(uname); len(f) >= 3 {
		l["kernel"] = f[2]
	}
	rel := ParseShellVars(s.text("linux/openwrt_release.txt"))
	osr := ParseShellVars(s.text("linux/os-release.txt"))
	switch {
	case rel["DISTRIB_DESCRIPTION"] != "":
		l["os"] = rel["DISTRIB_DESCRIPTION"]
		l["os_family"] = "openwrt"
		l["target"] = rel["DISTRIB_TARGET"]
	case osr["PRETTY_NAME"] != "":
		l["os"] = osr["PRETTY_NAME"]
		l["os_family"] = osr["ID"]
	default:
		l["os_family"] = "vendor/unknown"
	}
	l["cmdline"] = strings.TrimSpace(s.text("linux/cmdline.txt"))
	cpu := s.text("linux/cpuinfo.txt")
	procs := 0
	parts := map[string]bool{}
	for _, line := range strings.Split(cpu, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			procs++
		case "CPU part":
			parts[v] = true
		case "Hardware", "machine", "model name":
			l["cpu_"+strings.ReplaceAll(strings.ToLower(k), " ", "_")] = v
		}
	}
	l["cpu_count"] = procs
	l["cpu_parts"] = sortedKeys(parts)
	if mt := ParseKV(s.text("linux/meminfo.txt"))["MemTotal"]; mt != "" {
		l["mem_total"] = mt
	}
	l["fw_printenv"] = s.has("linux/fw_printenv.txt") && len(a.fwenv) > 0
	var sys map[string]string = map[string]string{}
	for _, line := range strings.Split(s.text("linux/sysinfo.txt"), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			sys[filepath.Base(k)] = strings.TrimSpace(v)
		}
	}
	if len(sys) > 0 {
		l["sysinfo"] = sys
	}
}

func (a *analysis) soc() {
	var os []obs
	var raw []string
	for _, v := range a.boot.SoCs {
		n := normSoC(v)
		os = append(os, obs{src: "uart-log", val: n, conf: Low})
		raw = append(raw, "uart-log: "+v)
	}
	for _, m := range a.metas {
		for _, c := range m.Compatible {
			vendor, model, ok := strings.Cut(c, ",")
			if !ok {
				continue
			}
			if mm := socRE.FindStringSubmatch(model + " "); mm != nil && (vendor == "airoha" || vendor == "econet" || vendor == "mediatek") {
				os = append(os, obs{src: m.Source + ":compatible", val: normSoC(mm[1]), conf: confFor(m.Source)})
				raw = append(raw, m.Source+": "+c)
			}
		}
	}
	for _, k := range []string{"soc", "cpu", "board"} {
		if v := a.env[k]; v != "" {
			raw = append(raw, a.ubPrefix+" env "+k+"="+v)
			if mm := socRE.FindStringSubmatch(v + " "); mm != nil {
				os = append(os, obs{src: a.ubPrefix + ":env." + k, val: normSoC(mm[1]), conf: a.ubConf})
			}
		}
	}
	cpu := a.s.text("linux/cpuinfo.txt")
	if mm := socRE.FindStringSubmatch(cpu); mm != nil {
		os = append(os, obs{src: "linux:cpuinfo", val: normSoC(mm[1]), conf: Medium})
		raw = append(raw, "linux cpuinfo: "+mm[1])
	}
	// The log can mention several SoC names (e.g. EN7581 and AN7581 for one chip);
	// normalisation makes those agree, genuinely different names conflict.
	f := a.p.resolve("soc.model", dedupeObs(os))
	a.p.SoC.Fact = *f
	if f.Value != nil {
		fam := "Airoha AN75xx"
		if v, _ := f.Value.(string); strings.HasPrefix(v, "EN") {
			fam = "EcoNet/Airoha EN75xx"
		}
		a.p.SoC.Family = &Fact{Value: fam, Source: f.Source, Confidence: f.Confidence}
	} else {
		a.p.SoC.Value = nil
		if len(os) == 0 {
			a.p.SoC.Value = "unknown"
			a.p.SoC.Confidence = Unknown
			a.p.warn("UNKNOWN AIROHA DEVICE: SoC not identified; raw identifiers kept")
		}
	}
	var revs, ids []obs
	for _, line := range a.boot.IDLines {
		raw = append(raw, "uart-log: "+line)
		low := strings.ToLower(line)
		if strings.Contains(low, "rev") {
			revs = append(revs, obs{src: "uart-log", val: line, conf: Low})
		}
		if strings.Contains(low, "chip") && strings.Contains(low, "id") {
			ids = append(ids, obs{src: "uart-log", val: line, conf: Low})
		}
	}
	if len(revs) > 0 {
		a.p.SoC.Revision = &Fact{Value: revs[0].val, Source: []string{"uart-log"}, Confidence: Low, Note: "raw log line; not interpreted"}
	}
	if len(ids) > 0 {
		a.p.SoC.ChipID = &Fact{Value: ids[0].val, Source: []string{"uart-log"}, Confidence: Low, Note: "raw log line; not interpreted"}
	}
	a.p.SoC.RawIdentifiers = uniqueStrings(raw)
}

func confFor(dtSource string) string {
	if dtSource == "dt-ram-uboot" {
		return Low
	}
	return Medium
}

// dedupeObs keeps one observation per source+value (a log mentions a SoC many times).
func dedupeObs(os []obs) []obs {
	seen := map[string]bool{}
	var out []obs
	for _, o := range os {
		k := o.src + "|" + fmt.Sprint(o.val)
		if !seen[k] {
			seen[k] = true
			out = append(out, o)
		}
	}
	return out
}

func uniqueStrings(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func (a *analysis) device() {
	var model, compat, vendor, board, hwrev, ram []obs
	for _, m := range a.metas {
		c := confFor(m.Source)
		if m.Model != "" {
			model = append(model, obs{src: m.Source + ":model", val: m.Model, conf: c})
		}
		if len(m.Compatible) > 0 {
			compat = append(compat, obs{src: m.Source + ":compatible", val: m.Compatible[0], conf: c})
			if v, _, ok := strings.Cut(m.Compatible[0], ","); ok {
				vendor = append(vendor, obs{src: m.Source + ":compatible", val: v, conf: c})
			}
		}
		var total uint64
		for _, r := range m.Memory {
			total += r[1]
		}
		if total > 0 {
			ram = append(ram, obs{src: m.Source + ":memory", val: total >> 20, conf: c, key: fmt.Sprint(total >> 20)})
		}
	}
	if a.boot.Model != "" {
		model = append(model, obs{src: "uart-log:Model", val: a.boot.Model, conf: Low})
	}
	if v := strings.TrimRight(strings.TrimSpace(a.s.text("dt/linux-model.txt")), "\x00"); v != "" && len(a.metas) == 0 {
		model = append(model, obs{src: "linux:devicetree/model", val: v, conf: Medium})
	}
	sys := map[string]string{}
	for _, line := range strings.Split(a.s.text("linux/sysinfo.txt"), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			sys[filepath.Base(k)] = strings.TrimSpace(v)
		}
	}
	if sys["model"] != "" {
		model = append(model, obs{src: "linux:sysinfo/model", val: sys["model"], conf: Medium})
	}
	if sys["board_name"] != "" {
		board = append(board, obs{src: "linux:sysinfo/board_name", val: sys["board_name"], conf: Medium})
	}
	for _, k := range sortedKeys(a.env) {
		if hwRevEnvRE.MatchString(k) {
			hwrev = append(hwrev, obs{src: a.ubPrefix + ":env." + k, val: a.env[k], conf: a.ubConf})
		}
	}
	for _, k := range sortedKeys(a.fwenv) {
		if hwRevEnvRE.MatchString(k) {
			hwrev = append(hwrev, obs{src: "linux:fw_printenv." + k, val: a.fwenv[k], conf: Medium})
		}
	}
	if a.boot.DRAM != "" {
		if v, ok := parseSizeUnit(a.boot.DRAM); ok {
			ram = append(ram, obs{src: "uart-log:DRAM", val: v >> 20, conf: Low, key: fmt.Sprint(v >> 20)})
		}
	}
	if len(a.bd.DRAMBanks) > 0 && a.ubSource == "device" {
		var t uint64
		for _, b := range a.bd.DRAMBanks {
			t += b[1]
		}
		ram = append(ram, obs{src: a.ubPrefix + ":bdinfo", val: t >> 20, conf: a.ubConf, key: fmt.Sprint(t >> 20)})
	}
	a.p.Device["model"] = a.p.resolve("device.model", model)
	a.p.Device["compatible"] = a.p.resolve("device.compatible", compat)
	a.p.Device["vendor"] = a.p.resolve("device.vendor", vendor)
	a.p.Device["board_name"] = a.p.resolve("device.board_name", board)
	a.p.Device["hardware_revision"] = a.p.resolve("device.hardware_revision", hwrev)
	f := a.p.resolve("device.ram_mib", ram)
	if mt := ParseKV(a.s.text("linux/meminfo.txt"))["MemTotal"]; mt != "" {
		f.Note = strings.TrimSpace(f.Note + " Linux MemTotal " + mt + " (usable, not physical)")
	}
	a.p.Device["ram_mib"] = f
	if f := a.p.Device["model"]; f.Value == nil && a.p.SoC.Value == "unknown" {
		a.p.Device["model"].Note = "UNKNOWN AIROHA DEVICE"
	}
}

func flashTypeFrom(t, driver, name, path string) string {
	low := strings.ToLower(t + " " + driver + " " + name + " " + path)
	switch {
	case strings.Contains(low, "nand") && strings.Contains(low, "spi"):
		return "SPI-NAND"
	case strings.Contains(low, "nand"):
		return "raw NAND"
	case strings.Contains(low, "nor") && strings.Contains(low, "spi"):
		return "SPI-NOR"
	case strings.Contains(low, "nor"):
		return "NOR"
	case strings.Contains(low, "mmc"):
		return "eMMC"
	}
	return ""
}

func (a *analysis) flash() {
	var typ, man, id, total, page, erase, oob, ecc, minio, sub []obs
	up, uc := a.ubPrefix, a.ubConf
	for _, d := range a.ubDevs {
		if strings.EqualFold(d.Type, "UBI volume") {
			continue
		}
		if t := flashTypeFrom(d.Type, d.Driver, d.Name, d.Path); t != "" {
			typ = append(typ, obs{src: up + ":mtd list", val: t, conf: Medium})
		}
		if d.End > d.Start {
			total = append(total, obs{src: up + ":mtd list", val: d.End - d.Start, conf: Medium})
		}
		if d.MinIO > 0 {
			page = append(page, obs{src: up + ":mtd list", val: d.MinIO, conf: Medium})
			minio = append(minio, obs{src: up + ":mtd list", val: d.MinIO, conf: Medium})
		}
		if d.EraseSize > 0 {
			erase = append(erase, obs{src: up + ":mtd list", val: d.EraseSize, conf: Medium})
		}
		if d.OOB > 0 {
			oob = append(oob, obs{src: up + ":mtd list", val: d.OOB, conf: Medium})
		}
		if d.ECCStrength > 0 {
			ecc = append(ecc, obs{src: up + ":mtd list", val: fmt.Sprintf("%d bits / %d bytes", d.ECCStrength, d.ECCStep), conf: Medium})
		}
		_ = uc
	}
	a.p.Flash.Devices = a.ubDevs
	// Linux: every partition of one chip reports the chip's geometry.
	geo := map[string]map[string]bool{}
	for _, attrs := range a.sysMTD {
		for _, k := range []string{"type", "writesize", "erasesize", "oobsize", "subpagesize"} {
			if v := attrs[k]; v != "" && attrs["type"] != "ubi" {
				if geo[k] == nil {
					geo[k] = map[string]bool{}
				}
				geo[k][v] = true
			}
		}
	}
	one := func(k string) (string, bool) {
		if len(geo[k]) != 1 {
			return "", false
		}
		for v := range geo[k] {
			return v, true
		}
		return "", false
	}
	dmesgSPI := strings.Contains(strings.ToLower(a.dmesg), "spi-nand") || strings.Contains(strings.ToLower(a.dmesg), "spi_nand")
	if v, ok := one("type"); ok {
		t := flashTypeFrom(v, "", "", "")
		if t == "raw NAND" && dmesgSPI {
			t = "SPI-NAND"
		}
		if t != "" {
			typ = append(typ, obs{src: "linux:sysfs", val: t, conf: Medium})
		}
	}
	if v, ok := one("writesize"); ok {
		n, _ := parseNum(v)
		page = append(page, obs{src: "linux:sysfs", val: n, conf: Medium})
		minio = append(minio, obs{src: "linux:sysfs", val: n, conf: Medium})
	}
	if v, ok := one("erasesize"); ok {
		n, _ := parseNum(v)
		erase = append(erase, obs{src: "linux:sysfs", val: n, conf: Medium})
	} else if len(a.procMTD) > 0 {
		es := map[uint64]bool{}
		for _, m := range a.procMTD {
			es[m.EraseSize] = true
		}
		if len(es) == 1 {
			erase = append(erase, obs{src: "linux:/proc/mtd", val: a.procMTD[0].EraseSize, conf: Medium})
		}
	}
	if v, ok := one("oobsize"); ok {
		n, _ := parseNum(v)
		if n > 0 {
			oob = append(oob, obs{src: "linux:sysfs", val: n, conf: Medium})
		}
	}
	if v, ok := one("subpagesize"); ok {
		n, _ := parseNum(v)
		sub = append(sub, obs{src: "linux:sysfs", val: n, conf: Medium})
	}
	if a.ubiL != nil && a.ubiL.SubPage > 0 {
		sub = append(sub, obs{src: "linux:dmesg-ubi", val: a.ubiL.SubPage, conf: Medium})
	}
	// "X SPI NAND was found" comes from the driver's ID table.
	for _, pair := range []struct{ name, text string }{{"uart-log", a.log}, {"linux:dmesg", a.dmesg}} {
		bl := ParseBootLog(pair.text)
		if bl.FlashVendor != "" {
			c := Low
			if spinandFound.MatchString(pair.text) {
				c = Medium
			}
			man = append(man, obs{src: pair.name, val: bl.FlashVendor, conf: c})
		}
		if g := spinandGeom.FindStringSubmatch(pair.text); g != nil {
			if v, ok := parseSizeUnit(g[1]); ok {
				total = append(total, obs{src: pair.name + ":spi-nand", val: v, conf: Medium})
			}
			if mm := regexp.MustCompile(`page size: ([0-9]+)`).FindStringSubmatch(g[1]); mm != nil {
				n, _ := parseNum(mm[1])
				page = append(page, obs{src: pair.name + ":spi-nand", val: n, conf: Medium})
			}
			if mm := regexp.MustCompile(`block size: ([0-9]+) KiB`).FindStringSubmatch(g[1]); mm != nil {
				n, _ := parseNum(mm[1])
				erase = append(erase, obs{src: pair.name + ":spi-nand", val: n << 10, conf: Medium})
			}
			if mm := regexp.MustCompile(`OOB size: ([0-9]+)`).FindStringSubmatch(g[1]); mm != nil {
				n, _ := parseNum(mm[1])
				oob = append(oob, obs{src: pair.name + ":spi-nand", val: n, conf: Medium})
			}
		}
		for _, line := range bl.FlashLines {
			if mm := regexp.MustCompile(`(?i)\b(?:id|jedec)\b[^0-9a-f]{0,12}((?:0x)?[0-9a-f]{2}(?:[ ,:]+(?:0x)?[0-9a-f]{2}){1,7})`).FindStringSubmatch(line); mm != nil {
				id = append(id, obs{src: pair.name, val: strings.ToLower(mm[1]), conf: Low})
			}
		}
		a.p.Flash.RawLines = append(a.p.Flash.RawLines, bl.FlashLines...)
	}
	if v := vendorFrom(a.s.text("uboot/nand-info.txt") + a.s.text("uboot/spi-nand-info.txt")); v != "" {
		man = append(man, obs{src: up + ":nand info", val: v, conf: Medium})
	}
	if a.s.commandOK("uboot/mmc-info.txt") && strings.Contains(a.s.text("uboot/mmc-info.txt"), "Capacity") {
		typ = append(typ, obs{src: up + ":mmc info", val: "eMMC", conf: Medium})
	}
	a.p.Flash.RawLines = uniqueStrings(a.p.Flash.RawLines)
	p := a.p
	p.Flash.Type = p.resolve("flash.type", dedupeObs(typ))
	p.Flash.Manufacturer = p.resolve("flash.manufacturer", dedupeObs(man))
	p.Flash.DeviceID = p.resolve("flash.device_id", dedupeObs(id))
	p.Flash.TotalSize = p.resolve("flash.total_size", dedupeObs(total))
	p.Flash.PageSize = p.resolve("flash.page_size", dedupeObs(page))
	p.Flash.EraseSize = p.resolve("flash.erase_block_size", dedupeObs(erase))
	p.Flash.OOBSize = p.resolve("flash.oob_size", dedupeObs(oob))
	p.Flash.ECC = p.resolve("flash.ecc", dedupeObs(ecc))
	p.Flash.MinIO = p.resolve("flash.min_io_unit", dedupeObs(minio))
	p.Flash.SubPage = p.resolve("flash.sub_page_size", dedupeObs(sub))
	bad := map[string]any{}
	for _, f := range a.s.glob("uboot/mtd-bad-*.txt") {
		name := strings.TrimSuffix(strings.TrimPrefix(f, "uboot/mtd-bad-"), ".txt")
		var list []string
		for _, line := range strings.Split(a.s.text(f), "\n") {
			t := strings.TrimSpace(line)
			if regexp.MustCompile(`^0x[0-9a-fA-F]+$`).MatchString(t) {
				list = append(list, t)
			}
		}
		if a.s.commandOK(f) {
			bad[name] = map[string]any{"count": len(list), "offsets": list, "source": up + ":mtd bad"}
		}
	}
	if len(bad) > 0 {
		p.Flash.BadBlocks = bad
	}
}

type mtdObs struct {
	src         string
	conf        string
	index       *int
	off, sz, es *uint64
	parent      string
}

func (a *analysis) mtdMap() {
	byName := map[string][]mtdObs{}
	var order []string
	add := func(name string, o mtdObs) {
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = append(byName[name], o)
	}
	for _, d := range a.ubDevs {
		if len(d.Parts) == 0 && d.End > d.Start {
			add(d.Name, mtdObs{src: a.ubPrefix + ":mtd list", conf: a.ubConf, off: u64p(0), sz: u64p(d.End - d.Start), es: nz(d.EraseSize), parent: ""})
		}
		for _, pt := range d.Parts {
			add(pt.Name, mtdObs{src: a.ubPrefix + ":mtd list", conf: a.ubConf, off: u64p(pt.Start), sz: u64p(pt.End - pt.Start), es: nz(d.EraseSize), parent: d.Name})
		}
	}
	for _, pm := range a.procMTD {
		i := pm.Index
		o := mtdObs{src: "linux:/proc/mtd", conf: Medium, index: &i, sz: u64p(pm.Size), es: u64p(pm.EraseSize)}
		if attrs := a.sysMTD[pm.Index]; attrs != nil {
			if v, ok := parseNum(attrs["offset"]); ok && attrs["offset"] != "" {
				o.off = u64p(v)
				o.src = "linux:/proc/mtd+sysfs"
			}
		}
		add(pm.Name, o)
	}
	for _, m := range a.metas {
		for _, pt := range m.Partitions {
			add(pt.Name, mtdObs{src: m.Source + ":partitions", conf: confFor(m.Source), off: u64p(pt.Offset), sz: u64p(pt.Size), parent: pt.Device})
		}
	}
	for _, name := range order {
		os := byName[name]
		e := MTDEntry{Name: name}
		var offs, szs, ess []obs
		for _, o := range os {
			e.Source = append(e.Source, o.src)
			if o.index != nil {
				e.Index = o.index
			}
			if o.parent != "" && e.Parent == "" {
				e.Parent = o.parent
			}
			if o.off != nil {
				offs = append(offs, obs{src: o.src, val: *o.off, conf: o.conf})
			}
			if o.sz != nil {
				szs = append(szs, obs{src: o.src, val: *o.sz, conf: o.conf})
			}
			if o.es != nil {
				ess = append(ess, obs{src: o.src, val: *o.es, conf: o.conf})
			}
		}
		nconf := len(a.p.Conflicts)
		fo := a.p.resolve("mtd["+name+"].offset", offs)
		fs := a.p.resolve("mtd["+name+"].size", szs)
		fe := a.p.resolve("mtd["+name+"].erase_size", ess)
		e.Conflict = len(a.p.Conflicts) > nconf
		e.Offset, e.Size, e.EraseSize = factU64(fo), factU64(fs), factU64(fe)
		e.Confidence = minConf(fo, fs)
		if identityPartRE.MatchString(name) {
			e.DeviceSpecific = true
			e.SafeToClone = boolp(false)
		}
		e.Contents = a.contentsFor(name)
		sort.Strings(e.Source)
		a.p.MTD = append(a.p.MTD, e)
	}
}

func nz(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}

func factU64(f *Fact) *uint64 {
	if f == nil || f.Value == nil {
		return nil
	}
	if v, ok := f.Value.(uint64); ok {
		return &v
	}
	return nil
}

func minConf(fs ...*Fact) string {
	best := 3
	for _, f := range fs {
		r := rank(f.Confidence)
		if f.Value == nil {
			r = 0
		}
		if r < best {
			best = r
		}
	}
	return [...]string{Unknown, Low, Medium, High}[best]
}

func (a *analysis) contentsFor(name string) []SampleResult {
	var out []SampleResult
	for _, r := range a.samples {
		if r.MTD != name {
			continue
		}
		b := a.s.bytes(r.File)
		out = append(out, SampleResult{Kind: r.Kind, Offset: r.Offset, Length: len(b), Source: r.Source, SHA256: r.SHA256, File: r.File, Signature: DetectSignature(b)})
	}
	return out
}

func (a *analysis) ubi() {
	p := a.p
	type vsrc struct {
		src  string
		conf string
		d    UBIDevice
	}
	var devs []vsrc
	for _, d := range a.ubiU {
		devs = append(devs, vsrc{a.ubPrefix + ":ubi info", a.ubConf, d})
	}
	if a.ubiL != nil {
		devs = append(devs, vsrc{"linux:ubinfo+dmesg", Medium, *a.ubiL})
	}
	if len(devs) == 0 {
		for _, e := range p.MTD {
			for _, c := range e.Contents {
				if c.Signature.Format == "ubi" {
					p.UBI.Detected = true
					p.UBI.MTD = &Fact{Value: e.Name, Source: []string{c.Source + ":sample"}, Confidence: Medium, Note: "UBI header found in raw sample; not attached"}
				}
			}
		}
		return
	}
	p.UBI.Detected = true
	field := func(name string, get func(UBIDevice) any) *Fact {
		var os []obs
		for _, d := range devs {
			v := get(d.d)
			switch x := v.(type) {
			case uint64:
				if x == 0 {
					continue
				}
			case string:
				if x == "" {
					continue
				}
			case *uint64:
				if x == nil {
					continue
				}
				v = *x
			}
			os = append(os, obs{src: d.src, val: v, conf: d.conf})
		}
		return p.resolve("ubi."+name, os)
	}
	p.UBI.MTD = field("mtd", func(d UBIDevice) any { return d.MTD })
	p.UBI.PEBSize = field("peb_size", func(d UBIDevice) any { return d.PEBSize })
	p.UBI.LEBSize = field("leb_size", func(d UBIDevice) any { return d.LEBSize })
	p.UBI.MinIO = field("min_io", func(d UBIDevice) any { return d.MinIO })
	p.UBI.VIDHdrOffset = field("vid_header_offset", func(d UBIDevice) any { return d.VIDHdrOffset })
	p.UBI.DataOffset = field("data_offset", func(d UBIDevice) any { return d.DataOffset })
	p.UBI.VolumeCount = field("volume_count", func(d UBIDevice) any { return d.VolumeCount })
	names := map[string][]struct {
		src, conf string
		v         UBIVolume
	}{}
	var order []string
	for _, d := range devs {
		for _, v := range d.d.Volumes {
			if _, ok := names[v.Name]; !ok {
				order = append(order, v.Name)
			}
			names[v.Name] = append(names[v.Name], struct {
				src, conf string
				v         UBIVolume
			}{d.src, d.conf, v})
		}
	}
	for _, n := range order {
		var ids, types, rsv, sz []obs
		for _, x := range names[n] {
			ids = append(ids, obs{src: x.src, val: x.v.ID, conf: x.conf})
			if x.v.Type != "" {
				types = append(types, obs{src: x.src, val: x.v.Type, conf: x.conf})
			}
			if x.v.ReservedPEBs > 0 {
				rsv = append(rsv, obs{src: x.src, val: x.v.ReservedPEBs, conf: x.conf})
			}
			if x.v.SizeBytes > 0 {
				sz = append(sz, obs{src: x.src, val: x.v.SizeBytes, conf: x.conf})
			}
		}
		vo := UBIVolumeOut{Name: n, ID: p.resolve("ubi.volume["+n+"].id", ids), Type: p.resolve("ubi.volume["+n+"].type", types),
			ReservedPEBs: p.resolve("ubi.volume["+n+"].reserved_pebs", rsv), SizeBytes: p.resolve("ubi.volume["+n+"].size_bytes", sz)}
		vo.Contents = a.contentsFor("ubi:" + n)
		for _, h := range a.hashes {
			if h["object"] == "ubi:"+n {
				vo.Hash = h
			}
		}
		p.UBI.Volumes = append(p.UBI.Volumes, vo)
	}
	if len(a.ubiU) > 0 {
		p.warn("U-Boot 'ubi part' was used to attach UBI: UBI itself may scrub or move blocks while attaching; the probe attached only partitions whose first PEB carries a UBI header")
	}
}

func (a *analysis) bootChain() {
	p := a.p
	ev := p.UART["markers"].(map[string]any)
	seen := func(m string) bool { _, ok := ev[m]; return ok }
	stage := func(name string, det bool, conf string) BootStage {
		st := BootStage{Stage: name, Confidence: conf}
		if det {
			st.Detected = boolp(true)
		}
		return st
	}
	// location helpers
	findSample := func(pred func(SampleResult) bool) (string, *SampleResult) {
		for _, e := range p.MTD {
			for i := range e.Contents {
				if pred(e.Contents[i]) {
					return e.Name, &e.Contents[i]
				}
			}
		}
		for _, v := range p.UBI.Volumes {
			for i := range v.Contents {
				if pred(v.Contents[i]) {
					return "ubi:" + v.Name, &v.Contents[i]
				}
			}
		}
		return "", nil
	}
	entryFor := func(name string) *MTDEntry {
		for i := range p.MTD {
			if p.MTD[i].Name == name {
				return &p.MTD[i]
			}
		}
		return nil
	}
	hashFor := func(obj string) map[string]any {
		for _, h := range a.hashes {
			if h["object"] == obj {
				return h
			}
		}
		return nil
	}
	fill := func(st *BootStage, loc string, smp *SampleResult) {
		st.Location = loc
		if e := entryFor(loc); e != nil {
			st.Offset, st.Size = e.Offset, e.Size
		}
		if smp != nil {
			st.Format = smp.Signature.Format
			st.Signature = smp.Signature.Detail
			st.FIP = smp.Signature.FIP
			if st.Hash == nil {
				st.Hash = map[string]any{"sha256": smp.SHA256, "scope": fmt.Sprintf("sample %s %d bytes at +0x%x (%s)", smp.Kind, smp.Length, smp.Offset, smp.Source)}
			}
		}
		if h := hashFor(loc); h != nil {
			st.Hash = h
			st.Hash["scope"] = "whole object read into RAM by U-Boot"
		}
	}

	br := stage("bootrom", a.boot.PressX || seen("xmodem_c") || p.BootROM["detected"] == true, Medium)
	br.Location = "SoC mask ROM"
	br.Source = []string{"uart-log"}
	if br.Detected != nil {
		br.Evidence = append(br.Evidence, "Press x / XMODEM receiver")
	} else {
		br.Confidence = Unknown
	}
	p.BootChain = append(p.BootChain, br)

	bl2 := stage("preloader/bl2", seen("bl2") || seen("dramc") || a.boot.BL2 != "", Low)
	bl2.Version = a.boot.BL2
	bl2.Source = []string{"uart-log"}
	if name, smp := findSample(func(s SampleResult) bool { return false }); name != "" {
		_ = smp
	}
	for _, e := range p.MTD {
		if regexp.MustCompile(`(?i)^(bl2|preloader|spl|boot0|bootloader)$`).MatchString(e.Name) {
			var smp *SampleResult
			for i := range e.Contents {
				if e.Contents[i].Kind == "head" {
					smp = &e.Contents[i]
				}
			}
			fill(&bl2, e.Name, smp)
			bl2.Evidence = append(bl2.Evidence, "partition name "+e.Name)
			break
		}
	}
	if bl2.Detected == nil && bl2.Location == "" {
		bl2.Confidence = Unknown
	}
	p.BootChain = append(p.BootChain, bl2)

	fipLoc, fipSmp := findSample(func(s SampleResult) bool { return s.Signature.Format == "fip" && s.Kind != "tail" })
	bl31 := stage("bl31/atf", seen("bl31") || a.boot.BL31 != "", Low)
	bl31.Version = a.boot.BL31
	bl31.Source = []string{"uart-log"}
	if fipLoc != "" {
		fill(&bl31, fipLoc, fipSmp)
		bl31.Source = append(bl31.Source, fipSmp.Source+":sample")
		bl31.Evidence = append(bl31.Evidence, "FIP ToC in "+fipLoc)
		bl31.Confidence = Medium
		if bl31.Detected != nil {
			bl31.Confidence = High
		}
	}
	if bl31.Detected == nil && bl31.Location == "" {
		bl31.Confidence = Unknown
	}
	p.BootChain = append(p.BootChain, bl31)

	bl33 := stage("bl33/u-boot", seen("uboot") || a.ubootSeen, Medium)
	if v, ok := p.UBoot["version"].(string); ok {
		bl33.Version = v
	} else {
		bl33.Version = a.boot.UBoot
	}
	bl33.Source = []string{"uart-log"}
	if a.ubootSeen {
		bl33.Source = append(bl33.Source, a.ubPrefix+":version")
	}
	if fipLoc != "" && fipSmp.Signature.FIP != nil {
		for _, e := range fipSmp.Signature.FIP.Entries {
			if strings.HasPrefix(e.Name, "BL33") {
				fill(&bl33, fipLoc, fipSmp)
				bl33.Evidence = append(bl33.Evidence, "BL33 entry in FIP "+fipLoc)
			}
		}
	}
	if bl33.Location == "" {
		for _, e := range p.MTD {
			if regexp.MustCompile(`(?i)^u-?boot`).MatchString(e.Name) && !identityPartRE.MatchString(e.Name) {
				fill(&bl33, e.Name, firstHead(e.Contents))
				bl33.Evidence = append(bl33.Evidence, "partition name "+e.Name)
				break
			}
		}
	}
	if a.ubSource == "ursido-ram-uboot" {
		bl33.Evidence = append(bl33.Evidence, "the U-Boot that answered was UrsidoRescue's RAM U-Boot, not the device's own")
	}
	if a.boot.TCBoot {
		bl33.Evidence = append(bl33.Evidence, "vendor tcboot seen in log")
	}
	if bl33.Detected == nil && bl33.Location == "" {
		bl33.Confidence = Unknown
	}
	p.BootChain = append(p.BootChain, bl33)

	kern := stage("kernel", seen("linux") || a.linuxSeen, Medium)
	kern.Version = a.boot.Kernel
	if v, ok := p.Linux["kernel"].(string); ok {
		kern.Version = v
	}
	kern.Source = []string{"uart-log"}
	if loc, smp := findSample(func(s SampleResult) bool {
		return s.Kind != "tail" && (s.Signature.Format == "fit" || s.Signature.Format == "uimage" || s.Signature.Format == "arm64-kernel-image" || s.Signature.Format == "fdt-or-fit")
	}); loc != "" {
		fill(&kern, loc, smp)
		kern.Source = append(kern.Source, smp.Source+":sample")
		kern.Evidence = append(kern.Evidence, smp.Signature.Format+" in "+loc)
	}
	if kern.Detected == nil && kern.Location == "" {
		kern.Confidence = Unknown
	}
	p.BootChain = append(p.BootChain, kern)

	osStage := stage("os/rootfs", a.linuxSeen || seen("openwrt"), Medium)
	if v, ok := p.Linux["os"].(string); ok {
		osStage.Version = v
	} else {
		osStage.Version = a.boot.OS
	}
	if loc, smp := findSample(func(s SampleResult) bool {
		return s.Kind != "tail" && (s.Signature.Format == "squashfs" || s.Signature.Format == "ubifs" || s.Signature.Format == "jffs2")
	}); loc != "" {
		fill(&osStage, loc, smp)
		osStage.Evidence = append(osStage.Evidence, smp.Signature.Format+" in "+loc)
	}
	if osStage.Detected == nil && osStage.Location == "" {
		osStage.Confidence = Unknown
	}
	p.BootChain = append(p.BootChain, osStage)
}

func firstHead(cs []SampleResult) *SampleResult {
	for i := range cs {
		if cs[i].Kind == "head" {
			return &cs[i]
		}
	}
	return nil
}

func (a *analysis) network() {
	p := a.p
	n := p.Network
	if len(a.metas) > 0 {
		m := a.metas[0]
		var eth []DTNodeInfo
		eth = append(eth, m.Nodes["ethernet"]...)
		n["dt_source"] = m.Source
		n["ethernet_controllers"] = eth
		n["switch"] = m.Nodes["switch"]
		n["mdio"] = m.Nodes["mdio"]
		n["pcs"] = m.Nodes["pcs"]
		n["phys"] = m.PHYs
		n["ports"] = m.Ports
		n["interface_modes"] = m.PhyModes
		var fixed []string
		wanlan := map[string][]string{}
		for _, pt := range m.Ports {
			if pt.FixedLink {
				fixed = append(fixed, pt.Path)
			}
			if pt.Role != "" && pt.Label != "" {
				wanlan[pt.Role] = append(wanlan[pt.Role], pt.Label)
			}
		}
		n["fixed_link"] = fixed
		if len(wanlan) > 0 {
			n["wan_lan_from_dt_labels"] = wanlan
		}
	}
	if phys := ParseMiiInfo(a.s.text("network/uboot-mii-info.txt")); len(phys) > 0 {
		n["uboot_mii_phys"] = phys
	}
	if a.s.has("network/uboot-mdio-list.txt") {
		n["uboot_mdio_list"] = "network/uboot-mdio-list.txt"
	}
	var ethEnv []string
	for _, k := range sortedKeys(a.env) {
		if strings.HasPrefix(k, "eth") {
			ethEnv = append(ethEnv, k)
		}
	}
	if len(ethEnv) > 0 {
		n["uboot_eth_env_keys"] = ethEnv
	}
	if ifs := ParseIPLink(a.s.text("network/ip-link.txt")); len(ifs) > 0 {
		for i := range ifs {
			kv := ParseKV(a.s.text("network/ethtool-i-" + safeName(ifs[i].Name) + ".txt"))
			ifs[i].Driver = kv["driver"]
		}
		n["linux_interfaces"] = ifs
	}
	var bj map[string]any
	if json.Unmarshal(a.s.bytes("linux/board.json"), &bj) == nil {
		if nw, ok := bj["network"]; ok {
			n["openwrt_board_json_network"] = nw
		}
		if md, ok := bj["model"]; ok {
			n["openwrt_board_json_model"] = md
		}
	}
	if len(n) == 0 {
		p.warn("network hardware: no data (no device tree, U-Boot mii or Linux ip link)")
	}
}

var debugGPIORE = regexp.MustCompile(`^\s*gpio-([0-9]+)\s+\(([^|)]*)\|?([^)]*)\)\s+(in|out)\s+(hi|lo)(.*)$`)

func (a *analysis) gpio() {
	g := a.p.GPIO
	if len(a.metas) > 0 {
		m := a.metas[0]
		g["dt_source"] = m.Source
		g["buttons"] = m.Buttons
		g["leds"] = m.LEDs
		g["controllers"] = m.Nodes["gpio"]
		g["pinctrl"] = m.Nodes["pinctrl"]
	}
	var lines []map[string]any
	for _, line := range strings.Split(a.s.text("gpio/linux-debug-gpio.txt"), "\n") {
		if mm := debugGPIORE.FindStringSubmatch(line); mm != nil {
			lines = append(lines, map[string]any{"gpio": mm[1], "name": strings.TrimSpace(mm[2]), "consumer": strings.TrimSpace(mm[3]), "dir": mm[4], "value": mm[5], "active_low": strings.Contains(mm[6], "ACTIVE LOW")})
		}
	}
	if len(lines) > 0 {
		g["linux_debug_gpio"] = lines
	}
	if a.s.has("gpio/uboot-gpio-status.txt") {
		g["uboot_gpio_status"] = "gpio/uboot-gpio-status.txt"
	}
	g["note"] = "collected passively; no GPIO was toggled"
}

func (a *analysis) identity() {
	p := a.p
	var envKeys []string
	for _, k := range sortedKeys(a.env) {
		if sensitiveEnvRE.MatchString(k) {
			envKeys = append(envKeys, k)
		}
	}
	for _, k := range sortedKeys(a.fwenv) {
		if sensitiveEnvRE.MatchString(k) {
			envKeys = append(envKeys, "fw:"+k)
		}
	}
	if len(envKeys) > 0 {
		p.Identity = append(p.Identity, IdentityEntry{Location: "bootloader environment", What: uniqueStrings(envKeys), DeviceSpecific: true, SafeToClone: false, Evidence: []string{"env key names (values not copied into the profile)"}})
	}
	for _, e := range p.MTD {
		if e.DeviceSpecific {
			p.Identity = append(p.Identity, IdentityEntry{Partition: e.Name, DeviceSpecific: true, SafeToClone: false, Evidence: []string{"partition name"}})
		}
	}
	for _, m := range a.metas {
		for _, u := range m.NVMEMUsers {
			ent := IdentityEntry{Partition: u["partition"], What: []string{u["what"]}, DeviceSpecific: true, SafeToClone: false, Evidence: []string{m.Source + ": " + u["user"] + " -> " + u["cell"]}}
			if ent.Partition == "" {
				ent.Location = u["cell"]
			}
			p.Identity = append(p.Identity, ent)
		}
		for _, pt := range m.Partitions {
			if pt.NVMEM {
				p.Identity = append(p.Identity, IdentityEntry{Partition: pt.Name, DeviceSpecific: true, SafeToClone: false, Evidence: []string{m.Source + ": nvmem-layout on partition"}})
			}
		}
	}
	// The profile never carries identity values; drop MACs that slipped into free text.
	for i, w := range p.Warnings {
		p.Warnings[i] = macRE.ReplaceAllString(w, "xx:xx:xx:xx:xx:xx")
	}
}

func (a *analysis) completeness() {
	p := a.p
	var missing []string
	if p.SoC.Value == nil || p.SoC.Value == "unknown" {
		missing = append(missing, "soc")
	}
	if len(p.MTD) == 0 {
		missing = append(missing, "mtd")
	}
	if p.Flash.Type.Value == nil {
		missing = append(missing, "flash.type")
	}
	if !a.ubootSeen && !a.linuxSeen {
		missing = append(missing, "uboot-or-linux")
	}
	if len(a.metas) == 0 {
		missing = append(missing, "dt")
	}
	p.Completeness["missing"] = missing
	p.Completeness["complete"] = len(missing) == 0
	p.Completeness["uboot"] = a.ubootSeen
	p.Completeness["linux"] = a.linuxSeen
	p.Completeness["bootrom"] = p.BootROM["detected"]
	if len(p.Conflicts) > 0 {
		p.Completeness["conflicts"] = len(p.Conflicts)
	}
	var st []string
	for _, line := range strings.Split(a.s.text("transcript.jsonl"), "\n") {
		var e TranscriptEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.Transport == "guard" {
			st = append(st, e.Result)
		}
	}
	if len(st) > 0 {
		p.Completeness["safety_blocks"] = st
		p.warn("%d command(s) were blocked by the probe safety guard (see transcript.jsonl)", len(st))
	}
}

func (a *analysis) writeDerived() error {
	p := a.p
	w := func(rel string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(a.s.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, b, 0o644)
	}
	if err := w("mtd/mtd-map.json", map[string]any{"mtd": p.MTD, "conflicts": filterConflicts(p.Conflicts, "mtd[")}); err != nil {
		return err
	}
	if err := w("ubi/ubi.json", p.UBI); err != nil {
		return err
	}
	if a.ubootSeen {
		if err := w("uboot/capabilities.json", p.Capabilities); err != nil {
			return err
		}
	}
	if err := w("network/network.json", p.Network); err != nil {
		return err
	}
	if err := w("gpio/gpio.json", p.GPIO); err != nil {
		return err
	}
	return w("flash/flash.json", p.Flash)
}

func filterConflicts(cs []Conflict, prefix string) []Conflict {
	out := []Conflict{}
	for _, c := range cs {
		if strings.HasPrefix(c.Field, prefix) {
			out = append(out, c)
		}
	}
	return out
}

var skipArtifact = map[string]bool{"profile.json": true, "hashes.sha256": true, "README.txt": true, "ursusboot-porting-report.md": true, "ursusflasher-device-draft.json": true}

func (a *analysis) artifacts() {
	_ = filepath.WalkDir(a.s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(a.s.dir, path)
		rel = filepath.ToSlash(rel)
		if skipArtifact[rel] || strings.HasSuffix(rel, ".zip") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		a.p.Artifacts[rel] = hex.EncodeToString(sum[:])
		return nil
	})
}
