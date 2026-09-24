package probe

import (
	"fmt"
	"regexp"
	"strings"
)

// DTNodeInfo is a compact description of a node in a category.
type DTNodeInfo struct {
	Path       string   `json:"path"`
	Compatible []string `json:"compatible,omitempty"`
	Status     string   `json:"status,omitempty"`
	Label      string   `json:"label,omitempty"`
	Reg        []string `json:"reg,omitempty"`
}

// DTPartition is a flash partition declared in the device tree.
type DTPartition struct {
	Device   string `json:"device"`
	Name     string `json:"name"`
	Offset   uint64 `json:"offset"`
	Size     uint64 `json:"size"`
	ReadOnly bool   `json:"read_only,omitempty"`
	NVMEM    bool   `json:"nvmem_layout,omitempty"`
}

// DTGPIO is a decoded GPIO specifier.
type DTGPIO struct {
	Controller string   `json:"controller"`
	Args       []uint32 `json:"args"`
	ActiveLow  bool     `json:"active_low"`
}

// DTButton is a gpio-keys child.
type DTButton struct {
	Path  string  `json:"path"`
	Label string  `json:"label,omitempty"`
	Code  *uint32 `json:"linux_code,omitempty"`
	GPIO  *DTGPIO `json:"gpio,omitempty"`
}

// DTLED is a gpio-leds child.
type DTLED struct {
	Path         string  `json:"path"`
	Label        string  `json:"label,omitempty"`
	Function     string  `json:"function,omitempty"`
	Color        *uint32 `json:"color,omitempty"`
	DefaultState string  `json:"default_state,omitempty"`
	GPIO         *DTGPIO `json:"gpio,omitempty"`
}

// DTPort is a switch or MAC port.
type DTPort struct {
	Path      string `json:"path"`
	Reg       string `json:"reg,omitempty"`
	Label     string `json:"label,omitempty"`
	PhyMode   string `json:"phy_mode,omitempty"`
	PhyHandle string `json:"phy_handle,omitempty"`
	FixedLink bool   `json:"fixed_link,omitempty"`
	CPUPort   bool   `json:"cpu_port,omitempty"`
	Role      string `json:"role,omitempty"`
}

// DTPHY is an MDIO PHY node.
type DTPHY struct {
	Path     string  `json:"path"`
	MDIOAddr *uint64 `json:"mdio_addr,omitempty"`
	PHYID    string  `json:"phy_id,omitempty"`
	Bus      string  `json:"bus,omitempty"`
}

// DTMeta is what the probe extracts from one DTB.
type DTMeta struct {
	Source     string                  `json:"source"`
	Model      string                  `json:"model,omitempty"`
	Compatible []string                `json:"compatible,omitempty"`
	Memory     [][2]uint64             `json:"memory,omitempty"`
	Chosen     map[string]string       `json:"chosen,omitempty"`
	Aliases    map[string]string       `json:"aliases,omitempty"`
	Nodes      map[string][]DTNodeInfo `json:"nodes"`
	Partitions []DTPartition           `json:"partitions,omitempty"`
	Buttons    []DTButton              `json:"buttons,omitempty"`
	LEDs       []DTLED                 `json:"leds,omitempty"`
	Ports      []DTPort                `json:"ports,omitempty"`
	PHYs       []DTPHY                 `json:"phys,omitempty"`
	PhyModes   []string                `json:"interface_modes,omitempty"`
	NVMEMUsers []map[string]string     `json:"nvmem_users,omitempty"`
	NodeCount  int                     `json:"node_count"`
}

var phyIDRE = regexp.MustCompile(`ethernet-phy-id([0-9a-fA-F]{4})\.([0-9a-fA-F]{4})`)

// dtCategories are the node classes of spec §14.
var dtCategories = []struct {
	name string
	fn   func(n *FDTNode) bool
}{
	{"serial", func(n *FDTNode) bool {
		return n.BaseName() == "serial" || n.Compatible("uart") || n.Compatible("serial")
	}},
	{"gpio", func(n *FDTNode) bool { return n.Has("gpio-controller") }},
	{"leds", func(n *FDTNode) bool { return n.Compatible("gpio-leds") || n.Compatible("pwm-leds") }},
	{"buttons", func(n *FDTNode) bool { return n.Compatible("gpio-keys") }},
	{"ethernet", func(n *FDTNode) bool {
		return (n.BaseName() == "ethernet" || n.Compatible("-eth") || n.Compatible("eth-mac") || n.Compatible("ethernet")) && !n.Compatible("ethernet-phy")
	}},
	{"mdio", func(n *FDTNode) bool { return strings.Contains(n.BaseName(), "mdio") || n.Compatible("mdio") }},
	{"phy", func(n *FDTNode) bool { return n.BaseName() == "ethernet-phy" || n.Compatible("ethernet-phy") }},
	{"switch", func(n *FDTNode) bool {
		return n.BaseName() == "switch" || n.Compatible("switch") || n.Compatible("mt7530") || n.Compatible("mt7531") || n.Compatible("an8855")
	}},
	{"pcs", func(n *FDTNode) bool { return strings.Contains(n.BaseName(), "pcs") || n.Compatible("pcs") }},
	{"nand", func(n *FDTNode) bool { return n.BaseName() == "nand" || n.Compatible("nand") }},
	{"spi", func(n *FDTNode) bool {
		return n.BaseName() == "spi" || (n.Compatible("spi") && !n.Compatible("nand") && !n.Compatible("nor"))
	}},
	{"spi_nor", func(n *FDTNode) bool { return n.Compatible("jedec,spi-nor") || n.Compatible("spi-nor") }},
	{"mmc", func(n *FDTNode) bool { return n.BaseName() == "mmc" || n.Compatible("mmc") || n.Compatible("sdhci") }},
	{"pcie", func(n *FDTNode) bool { return strings.HasPrefix(n.BaseName(), "pcie") || n.Compatible("pcie") }},
	{"usb", func(n *FDTNode) bool {
		return strings.HasPrefix(n.BaseName(), "usb") || n.Compatible("usb") || n.Compatible("xhci")
	}},
	{"watchdog", func(n *FDTNode) bool {
		return n.BaseName() == "watchdog" || n.Compatible("wdt") || n.Compatible("watchdog")
	}},
	{"clocks", func(n *FDTNode) bool { return n.Has("#clock-cells") }},
	{"reset_controllers", func(n *FDTNode) bool { return n.Has("#reset-cells") }},
	{"pinctrl", func(n *FDTNode) bool { return n.BaseName() == "pinctrl" || n.Compatible("pinctrl") }},
	{"nvmem", func(n *FDTNode) bool {
		return n.Compatible("nvmem") || n.Compatible("efuse") || n.Compatible("eeprom") || n.BaseName() == "nvmem-layout" || n.BaseName() == "efuse"
	}},
	{"memory", func(n *FDTNode) bool { return n.BaseName() == "memory" }},
	{"cpus", func(n *FDTNode) bool { return n.Parent != nil && n.Parent.Name == "cpus" && n.BaseName() == "cpu" }},
}

func regStrings(n *FDTNode) []string {
	var out []string
	for _, r := range n.Reg() {
		out = append(out, fmt.Sprintf("0x%x+0x%x", r[0], r[1]))
	}
	if len(out) == 0 {
		if c := n.Cells("reg"); len(c) > 0 {
			for _, x := range c {
				out = append(out, fmt.Sprintf("0x%x", x))
			}
		}
	}
	return out
}

func nodeInfo(n *FDTNode) DTNodeInfo {
	return DTNodeInfo{Path: n.Path, Compatible: n.Strings("compatible"), Status: n.Str("status"), Label: n.Str("label"), Reg: regStrings(n)}
}

// decodeSpec decodes the first specifier of a phandle list property.
func (f *FDT) decodeSpec(n *FDTNode, prop, cellsName string) *DTGPIO {
	c := n.Cells(prop)
	if len(c) < 1 {
		return nil
	}
	ctl := f.ByPhandle(c[0])
	if ctl == nil {
		return &DTGPIO{Controller: fmt.Sprintf("phandle 0x%x (unresolved)", c[0]), Args: c[1:]}
	}
	nc := 2
	if v := ctl.Cells(cellsName); len(v) == 1 {
		nc = int(v[0])
	}
	args := c[1:]
	if len(args) > nc {
		args = args[:nc]
	}
	g := &DTGPIO{Controller: ctl.Path, Args: args}
	if len(args) >= 2 {
		g.ActiveLow = args[len(args)-1]&1 == 1
	}
	return g
}

// ExtractDTMeta pulls the porting-relevant facts out of a tree.
func ExtractDTMeta(f *FDT, source string) DTMeta {
	m := DTMeta{Source: source, Nodes: map[string][]DTNodeInfo{}, Chosen: map[string]string{}, Aliases: map[string]string{}, NodeCount: f.NodeCount()}
	m.Model = f.Root.Str("model")
	m.Compatible = f.Root.Strings("compatible")
	modes := map[string]bool{}
	f.Walk(func(n *FDTNode) {
		for _, c := range dtCategories {
			if c.fn(n) {
				m.Nodes[c.name] = append(m.Nodes[c.name], nodeInfo(n))
			}
		}
		if n.BaseName() == "memory" && n.Parent == f.Root {
			m.Memory = append(m.Memory, n.Reg()...)
		}
		if n.Parent != nil && n.Parent.Compatible("fixed-partitions") || (n.Parent != nil && n.Parent.BaseName() == "partitions" && n.Has("reg")) {
			if r := n.Reg(); len(r) > 0 {
				dev := n.Parent.Path
				if n.Parent.Parent != nil {
					dev = n.Parent.Parent.Path
				}
				name := n.Str("label")
				if name == "" {
					name = n.BaseName()
				}
				m.Partitions = append(m.Partitions, DTPartition{Device: dev, Name: name, Offset: r[0][0], Size: r[0][1], ReadOnly: n.Has("read-only"), NVMEM: hasChild(n, "nvmem-layout")})
			}
		}
		if n.Parent != nil && n.Parent.Compatible("gpio-keys") {
			b := DTButton{Path: n.Path, Label: n.Str("label"), GPIO: f.decodeSpec(n, "gpios", "#gpio-cells")}
			if c := n.Cells("linux,code"); len(c) == 1 {
				b.Code = &c[0]
			}
			m.Buttons = append(m.Buttons, b)
		}
		if n.Parent != nil && (n.Parent.Compatible("gpio-leds") || n.Parent.Compatible("pwm-leds")) {
			l := DTLED{Path: n.Path, Label: n.Str("label"), Function: n.Str("function"), DefaultState: n.Str("default-state"), GPIO: f.decodeSpec(n, "gpios", "#gpio-cells")}
			if c := n.Cells("color"); len(c) == 1 {
				l.Color = &c[0]
			}
			m.LEDs = append(m.LEDs, l)
		}
		for _, pm := range []string{"phy-mode", "phy-connection-type"} {
			if v := n.Str(pm); v != "" {
				modes[v] = true
			}
		}
		if isPort(n) {
			p := DTPort{Path: n.Path, Label: n.Str("label"), PhyMode: firstNonEmpty(n.Str("phy-mode"), n.Str("phy-connection-type")), FixedLink: n.Has("fixed-link") || hasChild(n, "fixed-link"), CPUPort: n.Has("ethernet")}
			if r := n.Cells("reg"); len(r) == 1 {
				p.Reg = fmt.Sprint(r[0])
			}
			if ph := n.Cells("phy-handle"); len(ph) == 1 {
				if t := f.ByPhandle(ph[0]); t != nil {
					p.PhyHandle = t.Path
				}
			}
			p.Role = portRole(p.Label, p.CPUPort)
			m.Ports = append(m.Ports, p)
		}
		if n.BaseName() == "ethernet-phy" || n.Compatible("ethernet-phy") {
			ph := DTPHY{Path: n.Path}
			if r := n.Cells("reg"); len(r) == 1 {
				ph.MDIOAddr = u64p(uint64(r[0]))
			}
			for _, c := range n.Strings("compatible") {
				if mm := phyIDRE.FindStringSubmatch(c); mm != nil {
					ph.PHYID = "0x" + strings.ToLower(mm[1]+mm[2])
				}
			}
			if n.Parent != nil {
				ph.Bus = n.Parent.Path
			}
			m.PHYs = append(m.PHYs, ph)
		}
		if n.Has("nvmem-cells") {
			names := n.Strings("nvmem-cell-names")
			for i, ph := range n.Cells("nvmem-cells") {
				cell := f.ByPhandle(ph)
				if cell == nil {
					continue
				}
				what := ""
				if i < len(names) {
					what = names[i]
				}
				m.NVMEMUsers = append(m.NVMEMUsers, map[string]string{"user": n.Path, "cell": cell.Path, "what": what, "partition": partitionOf(cell)})
			}
		}
	})
	if ch := f.Find("/chosen"); ch != nil {
		for _, p := range ch.Props {
			if printableStrings(p.Value) {
				m.Chosen[p.Name] = strings.TrimRight(string(p.Value), "\x00")
			}
		}
	}
	if al := f.Find("/aliases"); al != nil {
		for _, p := range al.Props {
			if printableStrings(p.Value) {
				m.Aliases[p.Name] = strings.TrimRight(string(p.Value), "\x00")
			}
		}
	}
	for k := range modes {
		m.PhyModes = append(m.PhyModes, k)
	}
	sortStrings(m.PhyModes)
	return m
}

func isPort(n *FDTNode) bool {
	if n.Parent == nil {
		return false
	}
	pb := n.Parent.BaseName()
	b := n.BaseName()
	return (pb == "ports" || pb == "ethernet-ports") && (b == "port" || b == "ethernet-port")
}

func portRole(label string, cpu bool) string {
	l := strings.ToLower(label)
	switch {
	case cpu:
		return "cpu"
	case strings.HasPrefix(l, "wan"):
		return "wan"
	case strings.HasPrefix(l, "lan"):
		return "lan"
	}
	return ""
}

func hasChild(n *FDTNode, base string) bool {
	for _, c := range n.Children {
		if c.BaseName() == base {
			return true
		}
	}
	return false
}

func partitionOf(n *FDTNode) string {
	for p := n; p != nil; p = p.Parent {
		if p.Parent != nil && (p.Parent.Compatible("fixed-partitions") || p.Parent.BaseName() == "partitions") {
			if l := p.Str("label"); l != "" {
				return l
			}
			return p.BaseName()
		}
	}
	return ""
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
