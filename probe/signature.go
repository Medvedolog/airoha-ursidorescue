package probe

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
)

// Signature is what a raw sample looks like.
type Signature struct {
	Format string     `json:"format"`
	Detail string     `json:"detail,omitempty"`
	Heur   bool       `json:"heuristic,omitempty"`
	FIP    *FIPHeader `json:"fip,omitempty"`
}

// FIPHeader is a parsed TF-A FIP table of contents.
type FIPHeader struct {
	Serial  uint32     `json:"toc_serial"`
	Flags   uint64     `json:"flags"`
	Entries []FIPEntry `json:"entries"`
}

// FIPEntry is one ToC entry.
type FIPEntry struct {
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Offset uint64 `json:"offset"`
	Size   uint64 `json:"size"`
	Flags  uint64 `json:"flags,omitempty"`
}

// fipNames maps UUIDs (byte order as stored, as fiptool prints them) to images.
var fipNames = map[string]string{
	"5ff9ec0b-4d22-3e4d-a544-c39d81c73f0a": "BL2 (trusted boot firmware)",
	"47d4086d-4cfe-9846-9b95-2950cbbd5a00": "BL31 (EL3 runtime)",
	"05d0e189-53dc-1347-8d2b-500a4b7a3e38": "BL32 (secure payload)",
	"d6d0eea7-fcea-d54b-9782-9934f234b6e4": "BL33 (non-trusted firmware, e.g. U-Boot)",
}

func uuidString(b []byte) string {
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// ParseFIP parses a FIP ToC from the start of b.
func ParseFIP(b []byte) (*FIPHeader, error) {
	if len(b) < 16 || binary.LittleEndian.Uint32(b) != 0xAA640001 {
		return nil, fmt.Errorf("not a FIP ToC")
	}
	h := &FIPHeader{Serial: binary.LittleEndian.Uint32(b[4:]), Flags: binary.LittleEndian.Uint64(b[8:])}
	for p := 16; p+40 <= len(b); p += 40 {
		u := b[p : p+16]
		if bytes.Equal(u, make([]byte, 16)) {
			return h, nil
		}
		e := FIPEntry{UUID: uuidString(u), Offset: binary.LittleEndian.Uint64(b[p+16:]), Size: binary.LittleEndian.Uint64(b[p+24:]), Flags: binary.LittleEndian.Uint64(b[p+32:])}
		e.Name = fipNames[e.UUID]
		if e.Name == "" {
			e.Name = "unknown image"
		}
		h.Entries = append(h.Entries, e)
		if len(h.Entries) > 32 {
			break
		}
	}
	if len(h.Entries) == 0 {
		return nil, fmt.Errorf("FIP ToC without entries in sample")
	}
	return h, nil
}

func allByte(b []byte, v byte) bool {
	for _, c := range b {
		if c != v {
			return false
		}
	}
	return len(b) > 0
}

// DetectSignature identifies what a sample (normally the first 4 KiB of a
// partition or volume) contains.
func DetectSignature(b []byte) Signature {
	le, be := binary.LittleEndian, binary.BigEndian
	switch {
	case len(b) == 0:
		return Signature{Format: "empty-sample"}
	case allByte(b, 0xff):
		return Signature{Format: "erased", Detail: "all 0xFF"}
	case allByte(b, 0x00):
		return Signature{Format: "zero", Detail: "all 0x00"}
	case len(b) >= 16 && le.Uint32(b) == 0xAA640001:
		h, err := ParseFIP(b)
		if err != nil {
			return Signature{Format: "fip", Detail: err.Error()}
		}
		return Signature{Format: "fip", Detail: fmt.Sprintf("%d entries", len(h.Entries)), FIP: h}
	case bytes.HasPrefix(b, []byte("UBI#")):
		d := "UBI erase-counter header"
		if len(b) >= 24 {
			d += fmt.Sprintf(", VID hdr offset %d, data offset %d", be.Uint32(b[16:]), be.Uint32(b[20:]))
		}
		return Signature{Format: "ubi", Detail: d}
	case len(b) >= 8 && be.Uint32(b) == fdtMagic:
		f, err := ParseFDT(b)
		if err == nil && f.Find("/images") != nil {
			return Signature{Format: "fit", Detail: fmt.Sprintf("FIT image, totalsize %d", be.Uint32(b[4:]))}
		}
		// A sample shorter than the blob cannot be parsed; the header is enough.
		return Signature{Format: "fdt-or-fit", Detail: fmt.Sprintf("flattened device tree, totalsize %d", be.Uint32(b[4:]))}
	case len(b) >= 4 && be.Uint32(b) == 0x27051956:
		d := "legacy uImage"
		if len(b) >= 64 {
			d += `: "` + string(bytes.TrimRight(b[32:64], "\x00")) + `"`
		}
		return Signature{Format: "uimage", Detail: d}
	case bytes.HasPrefix(b, []byte("hsqs")) || bytes.HasPrefix(b, []byte("sqsh")):
		return Signature{Format: "squashfs"}
	case len(b) >= 4 && le.Uint32(b) == 0x06101831:
		return Signature{Format: "ubifs"}
	case bytes.HasPrefix(b, []byte{0x85, 0x19}) || bytes.HasPrefix(b, []byte{0x19, 0x85}):
		return Signature{Format: "jffs2"}
	case bytes.HasPrefix(b, []byte{0x1f, 0x8b}):
		return Signature{Format: "gzip"}
	case bytes.HasPrefix(b, []byte{0xfd, '7', 'z', 'X', 'Z', 0}):
		return Signature{Format: "xz"}
	case bytes.HasPrefix(b, []byte{0x5d, 0x00, 0x00}):
		return Signature{Format: "lzma", Heur: true}
	case bytes.HasPrefix(b, []byte("\x7fELF")):
		return Signature{Format: "elf"}
	case bytes.HasPrefix(b, []byte("HDR0")):
		return Signature{Format: "trx"}
	case bytes.HasPrefix(b, []byte("ANDROID!")):
		return Signature{Format: "android-boot"}
	case len(b) >= 0x40 && bytes.Equal(b[0x38:0x3c], []byte("ARM\x64")):
		return Signature{Format: "arm64-kernel-image"}
	}
	if env, ok := looksLikeEnv(b); ok {
		return Signature{Format: "uboot-env", Detail: env, Heur: true}
	}
	n := 16
	if len(b) < n {
		n = len(b)
	}
	return Signature{Format: "unknown", Detail: "head " + hex.EncodeToString(b[:n])}
}

// looksLikeEnv recognises a U-Boot environment by "key=value\0" records after
// a 4-byte CRC (or 5 bytes for redundant envs). The CRC is checked when the
// whole environment is inside the sample.
func looksLikeEnv(b []byte) (string, bool) {
	for _, off := range []int{4, 5} {
		if len(b) < off+8 {
			continue
		}
		data := b[off:]
		recs, i := 0, 0
		for i < len(data) && recs < 64 {
			end := bytes.IndexByte(data[i:], 0)
			if end <= 0 {
				break
			}
			rec := data[i : i+end]
			eq := bytes.IndexByte(rec, '=')
			if eq <= 0 || !printable(rec) {
				break
			}
			recs++
			i += end + 1
			if i < len(data) && data[i] == 0 {
				break
			}
		}
		if recs >= 3 {
			crcNote := "CRC not checked (sample shorter than env)"
			stored := binary.LittleEndian.Uint32(b)
			if crc32.ChecksumIEEE(b[off:]) == stored {
				crcNote = "CRC32 OK over sample"
			}
			return fmt.Sprintf("%d variables, header %d bytes, %s", recs, off, crcNote), true
		}
	}
	return "", false
}

func printable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// UBIHeaders is what the first page(s) of a UBI PEB say, parsed offline from
// a raw sample. Nothing is attached, so nothing can be written.
type UBIHeaders struct {
	Version      uint8  `json:"version"`
	EraseCounter uint64 `json:"erase_counter"`
	VIDHdrOffset uint32 `json:"vid_header_offset"`
	DataOffset   uint32 `json:"data_offset"`
	ImageSeq     uint32 `json:"image_seq"`
	ECCRCOK      bool   `json:"ec_crc_ok"`
	VIDPresent   bool   `json:"vid_present"`
	VolID        uint32 `json:"vol_id,omitempty"`
	LNum         uint32 `json:"lnum,omitempty"`
	VolType      string `json:"vol_type,omitempty"`
}

// ubiCRC is UBI's crc32 (initial 0xFFFFFFFF, no final inversion).
func ubiCRC(b []byte) uint32 { return ^crc32.ChecksumIEEE(b) }

// ParseUBIHeaders decodes the EC header at 0 and, if it is in the sample,
// the VID header at vid_hdr_offset.
func ParseUBIHeaders(b []byte) (UBIHeaders, bool) {
	var h UBIHeaders
	if len(b) < 64 || !bytes.HasPrefix(b, []byte("UBI#")) {
		return h, false
	}
	be := binary.BigEndian
	h.Version = b[4]
	h.EraseCounter = be.Uint64(b[8:])
	h.VIDHdrOffset = be.Uint32(b[16:])
	h.DataOffset = be.Uint32(b[20:])
	h.ImageSeq = be.Uint32(b[24:])
	h.ECCRCOK = ubiCRC(b[:60]) == be.Uint32(b[60:])
	v := int(h.VIDHdrOffset)
	if v > 0 && v+64 <= len(b) && bytes.HasPrefix(b[v:], []byte("UBI!")) {
		h.VIDPresent = true
		switch b[v+5] {
		case 1:
			h.VolType = "dynamic"
		case 2:
			h.VolType = "static"
		}
		h.VolID = be.Uint32(b[v+8:])
		h.LNum = be.Uint32(b[v+12:])
	}
	return h, true
}
