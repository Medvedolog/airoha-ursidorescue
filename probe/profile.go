package probe

import (
	"fmt"
	"sort"
	"strings"
)

// SchemaV1 is the profile schema id. New fields may be added; existing ones
// keep their meaning, so readers must ignore unknown keys.
const SchemaV1 = "ursus-profile-v1"

// Confidence levels (spec §23).
const (
	High    = "high"
	Medium  = "medium"
	Low     = "low"
	Unknown = "unknown"
)

// Fact is one conclusion with where it came from.
type Fact struct {
	Value      any      `json:"value"`
	Source     []string `json:"source"`
	Confidence string   `json:"confidence"`
	Note       string   `json:"note,omitempty"`
}

// SourcedValue is one source's opinion in a conflict.
type SourcedValue struct {
	Source string `json:"source"`
	Value  any    `json:"value"`
}

// Conflict is reported, never resolved automatically (spec §24).
type Conflict struct {
	Field  string         `json:"field"`
	Values []SourcedValue `json:"values"`
	Note   string         `json:"note,omitempty"`
}

// obs is one observation fed to resolve.
type obs struct {
	src  string
	val  any
	conf string
	key  string // normalised comparison key; defaults to fmt.Sprint(val)
}

func rank(c string) int {
	switch c {
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	}
	return 0
}

// sourceKind groups sources so that two files from the same origin do not
// count as independent confirmation.
func sourceKind(src string) string {
	k, _, _ := strings.Cut(src, ":")
	return k
}

// Profile is ursus-profile-v1.
type Profile struct {
	Schema       string            `json:"schema"`
	Generator    map[string]string `json:"generator"`
	Created      string            `json:"created"`
	Device       map[string]*Fact  `json:"device"`
	SoC          SoCInfo           `json:"soc"`
	BootROM      map[string]any    `json:"bootrom"`
	BootChain    []BootStage       `json:"boot_chain"`
	UART         map[string]any    `json:"uart"`
	Flash        FlashInfo         `json:"flash"`
	MTD          []MTDEntry        `json:"mtd"`
	UBI          UBIInfo           `json:"ubi"`
	UBoot        map[string]any    `json:"uboot"`
	DT           map[string]any    `json:"dt"`
	Network      map[string]any    `json:"network"`
	GPIO         map[string]any    `json:"gpio"`
	Linux        map[string]any    `json:"linux"`
	Capabilities map[string]any    `json:"capabilities"`
	Identity     []IdentityEntry   `json:"identity"`
	Artifacts    map[string]string `json:"artifacts"`
	Completeness map[string]any    `json:"completeness"`
	Warnings     []string          `json:"warnings"`
	Conflicts    []Conflict        `json:"conflicts"`
	Extra        map[string]any    `json:"extra,omitempty"`
}

// SoCInfo is the soc section: the model as a Fact plus details.
type SoCInfo struct {
	Fact
	Family         *Fact    `json:"family,omitempty"`
	Revision       *Fact    `json:"revision,omitempty"`
	ChipID         *Fact    `json:"chip_id,omitempty"`
	RawIdentifiers []string `json:"raw_identifiers,omitempty"`
}

// FlashInfo is the flash section.
type FlashInfo struct {
	Type         *Fact          `json:"type"`
	Manufacturer *Fact          `json:"manufacturer"`
	DeviceID     *Fact          `json:"device_id"`
	TotalSize    *Fact          `json:"total_size"`
	PageSize     *Fact          `json:"page_size"`
	EraseSize    *Fact          `json:"erase_block_size"`
	OOBSize      *Fact          `json:"oob_size"`
	ECC          *Fact          `json:"ecc"`
	MinIO        *Fact          `json:"min_io_unit"`
	SubPage      *Fact          `json:"sub_page_size"`
	BadBlocks    map[string]any `json:"bad_blocks,omitempty"`
	Devices      []MTDDevice    `json:"uboot_devices,omitempty"`
	RawLines     []string       `json:"raw_lines,omitempty"`
}

// MTDEntry is one partition in the merged map (spec §11).
type MTDEntry struct {
	Index          *int           `json:"index,omitempty"`
	Name           string         `json:"name"`
	Offset         *uint64        `json:"offset"`
	Size           *uint64        `json:"size"`
	EraseSize      *uint64        `json:"erase_size"`
	Parent         string         `json:"parent,omitempty"`
	Source         []string       `json:"source"`
	Confidence     string         `json:"confidence"`
	Conflict       bool           `json:"conflict"`
	DeviceSpecific bool           `json:"device_specific,omitempty"`
	SafeToClone    *bool          `json:"safe_to_clone,omitempty"`
	Contents       []SampleResult `json:"contents,omitempty"`
}

// SampleResult is a signature found in a raw sample.
type SampleResult struct {
	Kind      string    `json:"kind"`
	Offset    uint64    `json:"offset"`
	Length    int       `json:"length"`
	Source    string    `json:"source"`
	SHA256    string    `json:"sha256"`
	File      string    `json:"file"`
	Signature Signature `json:"signature"`
}

// UBIInfo is the ubi section (spec §12).
type UBIInfo struct {
	Detected     bool           `json:"detected"`
	MTD          *Fact          `json:"mtd,omitempty"`
	PEBSize      *Fact          `json:"peb_size,omitempty"`
	LEBSize      *Fact          `json:"leb_size,omitempty"`
	MinIO        *Fact          `json:"min_io,omitempty"`
	VIDHdrOffset *Fact          `json:"vid_header_offset,omitempty"`
	DataOffset   *Fact          `json:"data_offset,omitempty"`
	VolumeCount  *Fact          `json:"volume_count,omitempty"`
	Volumes      []UBIVolumeOut `json:"volumes,omitempty"`
}

// UBIVolumeOut is a merged volume.
type UBIVolumeOut struct {
	ID           *Fact          `json:"id"`
	Name         string         `json:"name"`
	Type         *Fact          `json:"type,omitempty"`
	ReservedPEBs *Fact          `json:"reserved_pebs,omitempty"`
	SizeBytes    *Fact          `json:"size_bytes,omitempty"`
	Contents     []SampleResult `json:"contents,omitempty"`
	Hash         map[string]any `json:"hash,omitempty"`
}

// BootStage is one element of the boot chain (spec §13).
type BootStage struct {
	Stage      string         `json:"stage"`
	Detected   *bool          `json:"detected"`
	Version    string         `json:"version,omitempty"`
	Evidence   []string       `json:"evidence,omitempty"`
	Location   string         `json:"location,omitempty"`
	Offset     *uint64        `json:"offset,omitempty"`
	Size       *uint64        `json:"size,omitempty"`
	Format     string         `json:"format,omitempty"`
	Signature  string         `json:"detected_signature,omitempty"`
	FIP        *FIPHeader     `json:"fip,omitempty"`
	Hash       map[string]any `json:"hash,omitempty"`
	Source     []string       `json:"source,omitempty"`
	Confidence string         `json:"confidence"`
}

// IdentityEntry marks where device-specific identity lives (spec §17). Values
// are never copied into the profile.
type IdentityEntry struct {
	Partition      string   `json:"partition,omitempty"`
	Location       string   `json:"location,omitempty"`
	What           []string `json:"what,omitempty"`
	DeviceSpecific bool     `json:"device_specific"`
	SafeToClone    bool     `json:"safe_to_clone"`
	Evidence       []string `json:"evidence"`
}

func (p *Profile) warn(format string, a ...any) {
	w := fmt.Sprintf(format, a...)
	for _, x := range p.Warnings {
		if x == w {
			return
		}
	}
	p.Warnings = append(p.Warnings, w)
}

// resolve turns observations into one Fact. Agreement of two independent
// source kinds is "high"; disagreement is a conflict and the value stays
// null; nothing observed is "unknown".
func (p *Profile) resolve(field string, os []obs) *Fact {
	var clean []obs
	for _, o := range os {
		if o.val == nil {
			continue
		}
		if s, ok := o.val.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		if o.key == "" {
			o.key = strings.ToLower(strings.TrimSpace(fmt.Sprint(o.val)))
		}
		clean = append(clean, o)
	}
	if len(clean) == 0 {
		return &Fact{Value: nil, Source: []string{}, Confidence: Unknown}
	}
	groups := map[string][]obs{}
	var order []string
	for _, o := range clean {
		if _, ok := groups[o.key]; !ok {
			order = append(order, o.key)
		}
		groups[o.key] = append(groups[o.key], o)
	}
	if len(groups) > 1 {
		c := Conflict{Field: field}
		var srcs []string
		for _, k := range order {
			for _, o := range groups[k] {
				c.Values = append(c.Values, SourcedValue{Source: o.src, Value: o.val})
				srcs = append(srcs, o.src)
			}
		}
		p.Conflicts = append(p.Conflicts, c)
		p.warn("CONFLICT %s: sources disagree (%s) — value not chosen automatically", field, conflictText(c))
		return &Fact{Value: nil, Source: srcs, Confidence: Unknown, Note: "conflict"}
	}
	g := groups[order[0]]
	f := &Fact{Value: g[0].val}
	kinds := map[string]bool{}
	best := 0
	for _, o := range g {
		f.Source = append(f.Source, o.src)
		kinds[sourceKind(o.src)] = true
		if r := rank(o.conf); r > best {
			best = r
		}
	}
	f.Confidence = [...]string{Unknown, Low, Medium, High}[best]
	if len(kinds) >= 2 && best >= rank(Low) {
		if best >= rank(Medium) {
			f.Confidence = High
		} else {
			f.Confidence = Medium
		}
	}
	sort.Strings(f.Source)
	return f
}

func conflictText(c Conflict) string {
	var parts []string
	for _, v := range c.Values {
		parts = append(parts, fmt.Sprintf("%s=%v", v.Source, fmtVal(v.Value)))
	}
	return strings.Join(parts, "; ")
}

func fmtVal(v any) string {
	switch x := v.(type) {
	case uint64:
		return fmt.Sprintf("0x%x", x)
	case *uint64:
		if x == nil {
			return "null"
		}
		return fmt.Sprintf("0x%x", *x)
	}
	return fmt.Sprint(v)
}

func u64p(v uint64) *uint64 { return &v }
func boolp(v bool) *bool    { return &v }
