package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
)

// Persistent UrsusBoot install (Expert → Install UrsusBoot). The candidate is
// always derived from what this device carries now; nothing here writes a
// static donor image. Ports of UrsusFlasher 0.2.67:
//   - MD: ursusboot_install.build_candidate — the pinned UrsusBoot update.fip
//     goes to physical 0x800 of the live boot area; the BootROM prefix and the
//     stock environment stay byte-exact;
//   - MF: mf_persistent.build_stock_derived_candidate — only the final NT_FW
//     (BL33) payload of the live FIP is replaced in place; every other entry,
//     the prefix and the environment stay byte-exact.
// On the UBI layout the same builders run on the content of the fip volume
// (mf_runtime_install._build_fip_derived for MF, the pinned FIP as is for MD).

const (
	bootAreaSize = 0x80000 // stock boot area: bl2 block + first 0x60000 of ubi
	bootFIPOff   = 0x800
	bootEnvOff   = 0x7C000
	bootFIPMax   = bootEnvOff - bootFIPOff
	mdCertFirst  = 0x77800 // MD: NT_FW must end before the first certificate
	fipMagic     = 0xAA640001
)

var (
	uuidNTFW     = mustUUID("d6d0eea7fcead54b97829934f234b6e4")
	uuidBL31     = mustUUID("47d4086d4cfe98469b952950cbbd5a00")
	uuidTBFW     = mustUUID("5ff9ec0b4d223e4da544c39d81c73f0a")
	uuidChecksum = mustUUID("a2cceab7f8254b279704633a6fd69ad8")
)

func mustUUID(s string) [16]byte {
	var u [16]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		panic("bad UUID " + s)
	}
	copy(u[:], b)
	return u
}

type fipEntry struct {
	UUID             [16]byte
	Off, Size, Flags uint64
	TOC              int // position of the TOC entry inside the FIP
}

func (e fipEntry) end() uint64 { return e.Off + e.Size }

type fipLayout struct {
	Serial  uint32
	Flags   uint64
	Entries []fipEntry
	Term    int    // position of the zero-UUID terminator
	End     uint64 // declared end (the terminator's offset)
}

func (l fipLayout) find(u [16]byte) []fipEntry {
	var out []fipEntry
	for _, e := range l.Entries {
		if e.UUID == u {
			out = append(out, e)
		}
	}
	return out
}

// parseFIP reads the TOC of a FIP that starts at fip[0]. Payloads must not
// overlap the TOC or each other and must end within the declared end, which
// must lie within maxEnd and the buffer.
func parseFIP(fip []byte, maxEnd uint64) (fipLayout, error) {
	var l fipLayout
	if len(fip) < 56 {
		return l, errors.New("FIP area too small")
	}
	if m := binary.LittleEndian.Uint32(fip); m != fipMagic {
		return l, fmt.Errorf("FIP magic 0x%08x", m)
	}
	l.Serial = binary.LittleEndian.Uint32(fip[4:])
	l.Flags = binary.LittleEndian.Uint64(fip[8:])
	l.Term = -1
	var zero [16]byte
	for pos := 16; pos+40 <= len(fip); pos += 40 {
		var e fipEntry
		copy(e.UUID[:], fip[pos:pos+16])
		e.Off = binary.LittleEndian.Uint64(fip[pos+16:])
		e.Size = binary.LittleEndian.Uint64(fip[pos+24:])
		e.Flags = binary.LittleEndian.Uint64(fip[pos+32:])
		e.TOC = pos
		if e.UUID == zero {
			if e.Size != 0 || e.Flags != 0 {
				return l, errors.New("FIP terminator has non-zero size/flags")
			}
			l.Term, l.End = pos, e.Off
			break
		}
		l.Entries = append(l.Entries, e)
		if len(l.Entries) > 64 {
			return l, errors.New("unreasonable FIP entry count")
		}
	}
	if l.Term < 0 {
		return l, errors.New("FIP terminator not found")
	}
	if len(l.Entries) == 0 {
		return l, errors.New("FIP has no payload entries")
	}
	tocEnd := uint64(l.Term + 40)
	if l.End < tocEnd || l.End > maxEnd || l.End > uint64(len(fip)) {
		return l, fmt.Errorf("FIP declared end 0x%x outside 0x%x..0x%x", l.End, tocEnd, min(maxEnd, uint64(len(fip))))
	}
	ordered := append([]fipEntry(nil), l.Entries...)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j].Off < ordered[j-1].Off; j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	prev := tocEnd
	for _, e := range ordered {
		if e.Off < prev || e.end() < e.Off {
			return l, fmt.Errorf("FIP payload %x overlaps at 0x%x", e.UUID, e.Off)
		}
		if e.end() > l.End {
			return l, fmt.Errorf("FIP payload %x exceeds the declared end", e.UUID)
		}
		prev = e.end()
	}
	return l, nil
}

// fipAlignment is the gcd of the payload offsets and the declared end.
func fipAlignment(l fipLayout) (uint64, error) {
	var g uint64
	gcd := func(a, b uint64) uint64 {
		for b != 0 {
			a, b = b, a%b
		}
		return a
	}
	for _, v := range append(offsets(l), l.End) {
		if v != 0 {
			g = gcd(g, v)
		}
	}
	if g < 16 || g > 0x10000 || g&(g-1) != 0 {
		return 0, fmt.Errorf("cannot infer a sane FIP alignment: 0x%x", g)
	}
	return g, nil
}

func offsets(l fipLayout) []uint64 {
	var xs []uint64
	for _, e := range l.Entries {
		xs = append(xs, e.Off)
	}
	return xs
}

// installReport describes a candidate for the log and the confirmation.
type installReport struct {
	Method               string
	SourceSHA, TargetSHA string
	SourceNTSize         uint64
	TargetNTSize         uint64
	NTOff                uint64
	SourceEnd, TargetEnd uint64
	Entries              int
}

func (r installReport) lines() []string {
	return []string{
		"method=" + r.Method,
		fmt.Sprintf("fip entries=%d NT_FW off=0x%x size %d -> %d", r.Entries, r.NTOff, r.SourceNTSize, r.TargetNTSize),
		fmt.Sprintf("fip end 0x%x -> 0x%x", r.SourceEnd, r.TargetEnd),
		"source SHA256=" + r.SourceSHA,
		"target SHA256=" + r.TargetSHA,
	}
}

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// mfDeriveBootArea replaces the final NT_FW payload of the live boot area's
// FIP with bl33 (Airoha LZMA-Alone) and re-proves every invariant.
func mfDeriveBootArea(area, bl33 []byte) ([]byte, installReport, error) {
	var r installReport
	if len(area) != bootAreaSize {
		return nil, r, fmt.Errorf("boot area is 0x%x bytes, expected 0x%x", len(area), bootAreaSize)
	}
	if len(bl33) < 13 || bl33[0] != 0x5D {
		return nil, r, errors.New("MF BL33 is not an Airoha LZMA-Alone payload")
	}
	fip := area[bootFIPOff:bootEnvOff]
	l, err := parseFIP(fip, bootFIPMax)
	if err != nil {
		return nil, r, err
	}
	nts := l.find(uuidNTFW)
	if len(nts) != 1 {
		return nil, r, fmt.Errorf("expected exactly one NT_FW/BL33 entry, found %d", len(nts))
	}
	nt := nts[0]
	for _, e := range l.Entries {
		if e.Off > nt.Off {
			return nil, r, errors.New("NT_FW/BL33 is not the final FIP payload; in-place replacement is not applicable")
		}
	}
	align, err := fipAlignment(l)
	if err != nil {
		return nil, r, err
	}
	newEnd := (nt.Off + uint64(len(bl33)) + align - 1) &^ (align - 1)
	if newEnd > bootFIPMax {
		return nil, r, fmt.Errorf("BL33 does not fit before the stock environment: FIP end 0x%x, max 0x%x", newEnd, bootFIPMax)
	}
	out := append([]byte(nil), area...)
	start := bootFIPOff + int(nt.Off)
	copy(out[start:], bl33)
	if uint64(len(bl33)) < nt.Size {
		pad := area[bootFIPOff+int(nt.end()) : bootFIPOff+int(l.End)]
		b := byte(0)
		if len(pad) > 0 && bytes.Count(pad, pad[:1]) == len(pad) {
			b = pad[0]
		}
		for i := start + len(bl33); i < bootFIPOff+int(nt.end()); i++ {
			out[i] = b
		}
	}
	binary.LittleEndian.PutUint64(out[bootFIPOff+nt.TOC+24:], uint64(len(bl33)))
	binary.LittleEndian.PutUint64(out[bootFIPOff+l.Term+16:], newEnd)

	nl, err := parseFIP(out[bootFIPOff:bootEnvOff], bootFIPMax)
	if err != nil {
		return nil, r, fmt.Errorf("candidate does not parse: %w", err)
	}
	nnt := nl.find(uuidNTFW)
	if len(nnt) != 1 || nnt[0].Off != nt.Off || nnt[0].Flags != nt.Flags || nnt[0].Size != uint64(len(bl33)) {
		return nil, r, errors.New("NT_FW metadata changed beyond its size")
	}
	if nl.Serial != l.Serial || nl.Flags != l.Flags || len(nl.Entries) != len(l.Entries) {
		return nil, r, errors.New("FIP header or entry count changed")
	}
	for i, e := range l.Entries {
		if e.UUID == uuidNTFW {
			continue
		}
		if nl.Entries[i] != e || !bytes.Equal(area[bootFIPOff+int(e.Off):bootFIPOff+int(e.end())], out[bootFIPOff+int(e.Off):bootFIPOff+int(e.end())]) {
			return nil, r, fmt.Errorf("FIP entry %x changed", e.UUID)
		}
	}
	if !bytes.Equal(out[:bootFIPOff], area[:bootFIPOff]) || !bytes.Equal(out[bootEnvOff:], area[bootEnvOff:]) {
		return nil, r, errors.New("the protected prefix or environment changed")
	}
	if !bytes.Equal(out[start:start+len(bl33)], bl33) {
		return nil, r, errors.New("BL33 was not placed exactly")
	}
	r = installReport{Method: "mf-derived-bl33", SourceSHA: shaHex(area), TargetSHA: shaHex(out),
		SourceNTSize: nt.Size, TargetNTSize: uint64(len(bl33)), NTOff: nt.Off,
		SourceEnd: l.End, TargetEnd: nl.End, Entries: len(l.Entries)}
	return out, r, nil
}

// mfDeriveFIP runs the boot-area builder on the content of a UBI fip volume
// and returns only the candidate FIP (up to its declared end).
func mfDeriveFIP(cur, bl33 []byte) ([]byte, installReport, error) {
	if len(cur) > bootFIPMax {
		return nil, installReport{}, fmt.Errorf("current FIP is too large: %d", len(cur))
	}
	fake := make([]byte, bootAreaSize)
	copy(fake[bootFIPOff:], cur)
	out, r, err := mfDeriveBootArea(fake, bl33)
	if err != nil {
		return nil, r, err
	}
	fip := out[bootFIPOff : bootFIPOff+int(r.TargetEnd)]
	r.Method = "mf-derived-bl33-ubi"
	r.SourceSHA, r.TargetSHA = shaHex(cur), shaHex(fip)
	return fip, r, nil
}

// mdCheckFIP proves the pinned MD UrsusBoot FIP: TOC, a single NT_FW that ends
// before the first certificate and the Airoha checksum record.
func mdCheckFIP(fip []byte) (fipLayout, error) {
	if len(fip) >= bootFIPMax {
		return fipLayout{}, fmt.Errorf("FIP does not fit before the stock environment: 0x%x", len(fip))
	}
	l, err := parseFIP(fip, uint64(len(fip)))
	if err != nil {
		return l, err
	}
	if l.End != uint64(len(fip)) {
		return l, fmt.Errorf("FIP declared end 0x%x != size 0x%x", l.End, len(fip))
	}
	nts := l.find(uuidNTFW)
	if len(nts) != 1 {
		return l, errors.New("FIP has no unique NT_FW/BL33 entry")
	}
	if nts[0].end() > mdCertFirst {
		return l, fmt.Errorf("NT_FW ends at 0x%x, past the first certificate 0x%x", nts[0].end(), mdCertFirst)
	}
	cs := l.find(uuidChecksum)
	if len(cs) != 1 || cs[0].Size != 40 {
		return l, errors.New("FIP checksum record missing")
	}
	off := cs[0].Off
	if uint64(binary.LittleEndian.Uint32(fip[off:])) != off {
		return l, errors.New("FIP checksum length does not match its offset")
	}
	if binary.LittleEndian.Uint32(fip[off+4:]) != crc32.ChecksumIEEE(fip[:off]) {
		return l, errors.New("FIP checksum CRC32 mismatch")
	}
	h := sha256.Sum256(fip[:off])
	if !bytes.Equal(fip[off+8:off+40], h[:]) {
		return l, errors.New("FIP checksum SHA256 mismatch")
	}
	return l, nil
}

// mdPlaceFIP puts the pinned MD FIP at physical 0x800 of the live boot area.
// The live area must carry a FIP with one Trusted Boot Firmware entry and a
// stock environment with a valid CRC.
func mdPlaceFIP(area, fip []byte) ([]byte, installReport, error) {
	var r installReport
	if len(area) != bootAreaSize {
		return nil, r, fmt.Errorf("boot area is 0x%x bytes, expected 0x%x", len(area), bootAreaSize)
	}
	nl, err := mdCheckFIP(fip)
	if err != nil {
		return nil, r, err
	}
	l, err := parseFIP(area[bootFIPOff:bootEnvOff], bootFIPMax)
	if err != nil {
		return nil, r, fmt.Errorf("live FIP: %w", err)
	}
	if len(l.find(uuidTBFW)) != 1 {
		return nil, r, errors.New("live FIP does not contain exactly one Trusted Boot Firmware entry")
	}
	env := area[bootEnvOff:]
	if binary.LittleEndian.Uint32(env) != crc32.ChecksumIEEE(env[4:]) {
		return nil, r, errors.New("live stock environment CRC mismatch")
	}
	out := append([]byte(nil), area...)
	copy(out[bootFIPOff:], fip)
	if !bytes.Equal(out[:bootFIPOff], area[:bootFIPOff]) || !bytes.Equal(out[bootEnvOff:], area[bootEnvOff:]) {
		return nil, r, errors.New("the protected prefix or environment changed")
	}
	r = installReport{Method: "md-pinned-fip", SourceSHA: shaHex(area), TargetSHA: shaHex(out),
		Entries: len(nl.Entries), SourceEnd: l.End, TargetEnd: nl.End}
	if nts := l.find(uuidNTFW); len(nts) == 1 {
		r.SourceNTSize = nts[0].Size
	}
	nt := nl.find(uuidNTFW)[0]
	r.NTOff, r.TargetNTSize = nt.Off, nt.Size
	return out, r, nil
}

// mdUBIFIP is the MD candidate for a UBI fip volume: the pinned FIP as is.
func mdUBIFIP(cur, fip []byte) ([]byte, installReport, error) {
	nl, err := mdCheckFIP(fip)
	if err != nil {
		return nil, installReport{}, err
	}
	nt := nl.find(uuidNTFW)[0]
	r := installReport{Method: "md-pinned-fip-ubi", SourceSHA: shaHex(cur), TargetSHA: shaHex(fip),
		Entries: len(nl.Entries), NTOff: nt.Off, TargetNTSize: nt.Size, TargetEnd: nl.End}
	if l, err := parseFIP(cur, uint64(len(cur))); err == nil {
		r.SourceEnd = l.End
		if nts := l.find(uuidNTFW); len(nts) == 1 {
			r.SourceNTSize = nts[0].Size
		}
	}
	return append([]byte(nil), fip...), r, nil
}
