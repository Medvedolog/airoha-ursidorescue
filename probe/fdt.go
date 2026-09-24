package probe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const fdtMagic = 0xd00dfeed

// FDTNode is one device-tree node.
type FDTNode struct {
	Name     string
	Path     string
	Props    []FDTProp
	Children []*FDTNode
	Parent   *FDTNode
}

// FDTProp is one property.
type FDTProp struct {
	Name  string
	Value []byte
}

// FDT is a parsed flattened device tree.
type FDT struct {
	Root       *FDTNode
	Reserve    [][2]uint64
	Version    uint32
	TotalSize  uint32
	byPhandle  map[uint32]*FDTNode
	nodesCount int
}

// FDTTotalSize reads totalsize from a header, or 0 if the magic is wrong.
func FDTTotalSize(h []byte) uint32 {
	if len(h) < 8 || binary.BigEndian.Uint32(h) != fdtMagic {
		return 0
	}
	return binary.BigEndian.Uint32(h[4:])
}

// ParseFDT parses a DTB blob.
func ParseFDT(b []byte) (*FDT, error) {
	if len(b) < 40 || binary.BigEndian.Uint32(b) != fdtMagic {
		return nil, errors.New("not a DTB (bad magic)")
	}
	be := binary.BigEndian
	total := be.Uint32(b[4:])
	offStruct, offStrings, offRsv := be.Uint32(b[8:]), be.Uint32(b[12:]), be.Uint32(b[16:])
	version := be.Uint32(b[20:])
	if int(total) > len(b) {
		return nil, fmt.Errorf("DTB truncated: totalsize %d > %d", total, len(b))
	}
	b = b[:total]
	sizeStrings := uint32(len(b)) - offStrings
	if version >= 3 && len(b) >= 36 {
		sizeStrings = be.Uint32(b[32:])
	}
	if offStruct >= total || offStrings > total || uint64(offStrings)+uint64(sizeStrings) > uint64(total) {
		return nil, errors.New("DTB header offsets out of range")
	}
	strs := b[offStrings : offStrings+sizeStrings]
	f := &FDT{Version: version, TotalSize: total, byPhandle: map[uint32]*FDTNode{}}
	for p := int(offRsv); p+16 <= len(b); p += 16 {
		a, s := be.Uint64(b[p:]), be.Uint64(b[p+8:])
		if a == 0 && s == 0 {
			break
		}
		f.Reserve = append(f.Reserve, [2]uint64{a, s})
	}
	p := int(offStruct)
	var cur *FDTNode
	align := func() { p = (p + 3) &^ 3 }
	for p+4 <= len(b) {
		tok := be.Uint32(b[p:])
		p += 4
		switch tok {
		case 1: // BEGIN_NODE
			end := p
			for end < len(b) && b[end] != 0 {
				end++
			}
			if end >= len(b) {
				return nil, errors.New("DTB node name unterminated")
			}
			n := &FDTNode{Name: string(b[p:end]), Parent: cur}
			p = end + 1
			align()
			if cur == nil {
				n.Path = "/"
				f.Root = n
			} else {
				n.Path = strings.TrimSuffix(cur.Path, "/") + "/" + n.Name
				cur.Children = append(cur.Children, n)
			}
			f.nodesCount++
			cur = n
		case 2: // END_NODE
			if cur == nil {
				return nil, errors.New("DTB END_NODE without node")
			}
			cur = cur.Parent
		case 3: // PROP
			if p+8 > len(b) || cur == nil {
				return nil, errors.New("DTB property outside node")
			}
			ln, nameoff := be.Uint32(b[p:]), be.Uint32(b[p+4:])
			p += 8
			if uint64(p)+uint64(ln) > uint64(len(b)) || nameoff >= uint32(len(strs)) {
				return nil, errors.New("DTB property out of range")
			}
			ne := int(nameoff)
			for ne < len(strs) && strs[ne] != 0 {
				ne++
			}
			prop := FDTProp{Name: string(strs[nameoff:ne]), Value: append([]byte(nil), b[p:p+int(ln)]...)}
			cur.Props = append(cur.Props, prop)
			if (prop.Name == "phandle" || prop.Name == "linux,phandle") && len(prop.Value) == 4 {
				f.byPhandle[be.Uint32(prop.Value)] = cur
			}
			p += int(ln)
			align()
		case 4: // NOP
		case 9: // END
			if f.Root == nil {
				return nil, errors.New("DTB without root node")
			}
			return f, nil
		default:
			return nil, fmt.Errorf("DTB bad token 0x%x at 0x%x", tok, p-4)
		}
	}
	if f.Root == nil {
		return nil, errors.New("DTB without root node")
	}
	return f, nil
}

// Prop returns a property value.
func (n *FDTNode) Prop(name string) ([]byte, bool) {
	for _, p := range n.Props {
		if p.Name == name {
			return p.Value, true
		}
	}
	return nil, false
}

// Str returns the first string of a property.
func (n *FDTNode) Str(name string) string {
	v, ok := n.Prop(name)
	if !ok {
		return ""
	}
	if i := strings.IndexByte(string(v), 0); i >= 0 {
		return string(v[:i])
	}
	return string(v)
}

// Strings returns all strings of a string-list property.
func (n *FDTNode) Strings(name string) []string {
	v, ok := n.Prop(name)
	if !ok || len(v) == 0 {
		return nil
	}
	var out []string
	for _, s := range strings.Split(strings.TrimRight(string(v), "\x00"), "\x00") {
		out = append(out, s)
	}
	return out
}

// Cells returns a property as big-endian u32 cells.
func (n *FDTNode) Cells(name string) []uint32 {
	v, ok := n.Prop(name)
	if !ok || len(v)%4 != 0 {
		return nil
	}
	out := make([]uint32, len(v)/4)
	for i := range out {
		out[i] = binary.BigEndian.Uint32(v[i*4:])
	}
	return out
}

// Has reports whether a property exists.
func (n *FDTNode) Has(name string) bool { _, ok := n.Prop(name); return ok }

// Compatible reports whether any compatible string contains sub.
func (n *FDTNode) Compatible(sub string) bool {
	for _, c := range n.Strings("compatible") {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// BaseName is the node name without the unit address.
func (n *FDTNode) BaseName() string {
	name, _, _ := strings.Cut(n.Name, "@")
	return name
}

// Walk visits every node depth-first.
func (f *FDT) Walk(fn func(*FDTNode)) {
	var rec func(*FDTNode)
	rec = func(n *FDTNode) {
		fn(n)
		for _, c := range n.Children {
			rec(c)
		}
	}
	if f.Root != nil {
		rec(f.Root)
	}
}

// Find returns the node at an absolute path.
func (f *FDT) Find(path string) *FDTNode {
	var hit *FDTNode
	f.Walk(func(n *FDTNode) {
		if hit == nil && n.Path == path {
			hit = n
		}
	})
	return hit
}

// ByPhandle resolves a phandle.
func (f *FDT) ByPhandle(ph uint32) *FDTNode { return f.byPhandle[ph] }

// NodeCount is the number of nodes.
func (f *FDT) NodeCount() int { return f.nodesCount }

// CellCounts returns #address-cells and #size-cells a node's children use.
func (n *FDTNode) CellCounts() (int, int) {
	ac, sc := 2, 1
	if v := n.Cells("#address-cells"); len(v) == 1 {
		ac = int(v[0])
	}
	if v := n.Cells("#size-cells"); len(v) == 1 {
		sc = int(v[0])
	}
	return ac, sc
}

// Reg decodes "reg" using the parent's cell counts.
func (n *FDTNode) Reg() [][2]uint64 {
	if n.Parent == nil {
		return nil
	}
	ac, sc := n.Parent.CellCounts()
	c := n.Cells("reg")
	if ac < 1 || ac > 2 || sc < 0 || sc > 2 || len(c) == 0 || len(c)%(ac+sc) != 0 {
		return nil
	}
	var out [][2]uint64
	for i := 0; i < len(c); i += ac + sc {
		var a, s uint64
		for j := 0; j < ac; j++ {
			a = a<<32 | uint64(c[i+j])
		}
		for j := 0; j < sc; j++ {
			s = s<<32 | uint64(c[i+ac+j])
		}
		out = append(out, [2]uint64{a, s})
	}
	return out
}

func printableStrings(v []byte) bool {
	if len(v) == 0 || v[len(v)-1] != 0 {
		return false
	}
	prevNul := true
	for _, c := range v[:len(v)-1] {
		if c == 0 {
			if prevNul {
				return false
			}
			prevNul = true
			continue
		}
		if c < 0x20 || c > 0x7e {
			return false
		}
		prevNul = false
	}
	return !prevNul
}

func formatProp(p FDTProp) string {
	v := p.Value
	switch {
	case len(v) == 0:
		return p.Name + ";"
	case printableStrings(v):
		parts := strings.Split(strings.TrimRight(string(v), "\x00"), "\x00")
		for i, s := range parts {
			parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
		}
		return p.Name + " = " + strings.Join(parts, ", ") + ";"
	case len(v)%4 == 0:
		cells := make([]string, len(v)/4)
		for i := range cells {
			cells[i] = fmt.Sprintf("0x%08x", binary.BigEndian.Uint32(v[i*4:]))
		}
		return p.Name + " = <" + strings.Join(cells, " ") + ">;"
	default:
		bs := make([]string, len(v))
		for i, c := range v {
			bs[i] = fmt.Sprintf("%02x", c)
		}
		return p.Name + " = [" + strings.Join(bs, " ") + "];"
	}
}

// DTS decompiles the tree to source (phandle references stay numeric).
func (f *FDT) DTS() string {
	var sb strings.Builder
	sb.WriteString("/dts-v1/;\n")
	for _, r := range f.Reserve {
		fmt.Fprintf(&sb, "/memreserve/ 0x%016x 0x%016x;\n", r[0], r[1])
	}
	sb.WriteString("\n")
	var rec func(n *FDTNode, depth int)
	rec = func(n *FDTNode, depth int) {
		ind := strings.Repeat("\t", depth)
		name := n.Name
		if n.Parent == nil {
			name = "/"
		}
		fmt.Fprintf(&sb, "%s%s {\n", ind, name)
		props := append([]FDTProp(nil), n.Props...)
		for _, p := range props {
			fmt.Fprintf(&sb, "%s\t%s\n", ind, formatProp(p))
		}
		for i, c := range n.Children {
			if i > 0 || len(props) > 0 {
				sb.WriteString("\n")
			}
			rec(c, depth+1)
		}
		fmt.Fprintf(&sb, "%s};\n", ind)
	}
	rec(f.Root, 0)
	return sb.String()
}

// sortedKeys is a small helper for deterministic output.
func sortedKeys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
