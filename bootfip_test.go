package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"strings"
	"testing"
)

type testEntry struct {
	uuid      [16]byte
	off, size uint64
}

// makeFIP builds a FIP with the given entries (payload bytes = a pattern of
// the entry index) and declared end; the rest is filled with pad.
func makeFIP(t *testing.T, entries []testEntry, end uint64, pad byte) []byte {
	t.Helper()
	fip := bytes.Repeat([]byte{pad}, int(end))
	binary.LittleEndian.PutUint32(fip, fipMagic)
	binary.LittleEndian.PutUint32(fip[4:], 0x12345678)
	pos := 16
	for i, e := range entries {
		copy(fip[pos:], e.uuid[:])
		binary.LittleEndian.PutUint64(fip[pos+16:], e.off)
		binary.LittleEndian.PutUint64(fip[pos+24:], e.size)
		pos += 40
		for j := uint64(0); j < e.size; j++ {
			fip[e.off+j] = byte(0x10*i + 1)
		}
	}
	for j := 0; j < 40; j++ {
		fip[pos+j] = 0
	}
	binary.LittleEndian.PutUint64(fip[pos+16:], end)
	return fip
}

func makeArea(fip []byte) []byte {
	area := bytes.Repeat([]byte{0xff}, bootAreaSize)
	for i := 0; i < bootFIPOff; i++ {
		area[i] = byte(i)
	}
	copy(area[bootFIPOff:], fip)
	env := area[bootEnvOff:]
	copy(env[4:], "bootcmd=nokia\x00\x00")
	binary.LittleEndian.PutUint32(env, crc32.ChecksumIEEE(env[4:]))
	return area
}

func lzmaStub(n int) []byte {
	b := bytes.Repeat([]byte{0xA5}, n)
	b[0] = 0x5D
	return b
}

var mfEntries = []testEntry{{uuidTBFW, 0x200, 0x100}, {uuidBL31, 0x400, 0x300}, {uuidNTFW, 0x800, 0x600}}

func TestMFDeriveBootAreaGrow(t *testing.T) {
	area := makeArea(makeFIP(t, mfEntries, 0xE00, 0x00))
	bl33 := lzmaStub(0x900)
	out, r, err := mfDeriveBootArea(area, bl33)
	if err != nil {
		t.Fatal(err)
	}
	l, err := parseFIP(out[bootFIPOff:bootEnvOff], bootFIPMax)
	if err != nil {
		t.Fatal(err)
	}
	nt := l.find(uuidNTFW)[0]
	if nt.Size != 0x900 || l.End != 0x1200 || r.TargetEnd != 0x1200 {
		t.Fatalf("NT size 0x%x end 0x%x, want 0x900/0x1200 (alignment 0x200)", nt.Size, l.End)
	}
	if !bytes.Equal(out[bootFIPOff+0x800:bootFIPOff+0x1100], bl33) {
		t.Fatal("BL33 not in place")
	}
	if !bytes.Equal(out[:bootFIPOff], area[:bootFIPOff]) || !bytes.Equal(out[bootEnvOff:], area[bootEnvOff:]) {
		t.Fatal("prefix/env changed")
	}
	if !bytes.Equal(out[bootFIPOff+0x400:bootFIPOff+0x700], area[bootFIPOff+0x400:bootFIPOff+0x700]) {
		t.Fatal("BL31 changed")
	}
}

func TestMFDeriveBootAreaShrinkPads(t *testing.T) {
	area := makeArea(makeFIP(t, mfEntries, 0x1000, 0xEE))
	out, r, err := mfDeriveBootArea(area, lzmaStub(0x100))
	if err != nil {
		t.Fatal(err)
	}
	if r.TargetEnd != 0xA00 {
		t.Fatalf("end 0x%x, want 0xA00", r.TargetEnd)
	}
	for i := bootFIPOff + 0x900; i < bootFIPOff+0xE00; i++ {
		if out[i] != 0xEE {
			t.Fatalf("stale BL33 byte at 0x%x = %02x", i, out[i])
		}
	}
}

func TestMFDeriveRefusals(t *testing.T) {
	area := makeArea(makeFIP(t, mfEntries, 0xE00, 0))
	if _, _, err := mfDeriveBootArea(area, bytes.Repeat([]byte{1}, 100)); err == nil {
		t.Fatal("accepted a non-LZMA BL33")
	}
	notLast := []testEntry{{uuidNTFW, 0x200, 0x100}, {uuidBL31, 0x400, 0x300}}
	if _, _, err := mfDeriveBootArea(makeArea(makeFIP(t, notLast, 0x800, 0)), lzmaStub(0x80)); err == nil || !strings.Contains(err.Error(), "final") {
		t.Fatalf("NT_FW not final: %v", err)
	}
	if _, _, err := mfDeriveBootArea(area, lzmaStub(bootFIPMax)); err == nil {
		t.Fatal("accepted a BL33 past the environment")
	}
	broken := append([]byte(nil), area...)
	broken[bootFIPOff] ^= 1
	if _, _, err := mfDeriveBootArea(broken, lzmaStub(0x80)); err == nil {
		t.Fatal("accepted a bad magic")
	}
}

func TestMFDeriveFIPFromUBI(t *testing.T) {
	cur := makeFIP(t, mfEntries, 0xE00, 0)
	fip, r, err := mfDeriveFIP(append(cur, 0xff, 0xff), lzmaStub(0x900))
	if err != nil {
		t.Fatal(err)
	}
	if len(fip) != 0x1200 || r.TargetSHA != shaHex(fip) {
		t.Fatalf("FIP len 0x%x", len(fip))
	}
	if !bytes.Equal(fip[0x400:0x700], cur[0x400:0x700]) || !bytes.Equal(fip[:16], cur[:16]) {
		t.Fatal("header or BL31 changed")
	}
}

// The pinned payloads: the MD FIP passes its own proof and lands on a stock
// area; the MF BL33 fits a stock-like FIP with NT_FW at 0x27800.
func TestPinnedUrsusBootPayloads(t *testing.T) {
	md, err := os.ReadFile(profiles["md"].BootRel)
	if err != nil {
		t.Fatal(err)
	}
	l, err := mdCheckFIP(md)
	if err != nil {
		t.Fatal(err)
	}
	if nt := l.find(uuidNTFW)[0]; shaHex(md[nt.Off:nt.end()]) != "d6b7a64f470bf0d732bbe3c1df9ee6c539facc1a15edef874eb3afdf48e52e65" {
		t.Fatal("MD NT_FW is not the t67 u-boot.lzma")
	}
	stock := makeArea(makeFIP(t, mfEntries, 0xE00, 0))
	out, _, err := mdPlaceFIP(stock, md)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[bootFIPOff:bootFIPOff+len(md)], md) || !bytes.Equal(out[bootEnvOff:], stock[bootEnvOff:]) {
		t.Fatal("MD placement")
	}
	stock[bootEnvOff+10] ^= 1
	if _, _, err := mdPlaceFIP(stock, md); err == nil {
		t.Fatal("accepted a bad env CRC")
	}
	bad := append([]byte(nil), md...)
	bad[0x30000] ^= 1
	if _, err := mdCheckFIP(bad); err == nil {
		t.Fatal("checksum did not catch a changed BL33")
	}

	mf, err := os.ReadFile(profiles["mf"].BootRel)
	if err != nil {
		t.Fatal(err)
	}
	ents := []testEntry{{uuidTBFW, 0x400, 0x10000}, {uuidBL31, 0x10400, 0x17400}, {uuidNTFW, 0x27800, 0x40000}}
	if _, _, err := mfDeriveBootArea(makeArea(makeFIP(t, ents, 0x67800, 0)), mf); err != nil {
		t.Fatal(err)
	}
}

func TestParseMDL(t *testing.T) {
	var b strings.Builder
	b.WriteString("md.l 0x90000000 0x8\r\n")
	b.WriteString("90000000: aa640001 12345678 00000000 00000000    ..d.xV4.........\r\n")
	b.WriteString("90000010: 23494255 00000001 deadbeef 0badf00d    UBI#............\r\n")
	b.WriteString("AN7581> ")
	got, err := parseMDL([]byte(b.String()), 0x90000000, 32)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(got) != fipMagic || string(got[16:20]) != "UBI#" {
		t.Fatalf("%x", got)
	}
	if _, err := parseMDL([]byte(b.String()), 0x90000000, 48); err == nil {
		t.Fatal("short dump accepted")
	}
	gap := strings.Replace(b.String(), "90000010:", "90000020:", 1)
	if _, err := parseMDL([]byte(gap), 0x90000000, 32); err == nil {
		t.Fatal("gap accepted")
	}
}

func TestParseUBIVolumes(t *testing.T) {
	var b strings.Builder
	for _, v := range []struct {
		name      string
		typ, used int
	}{{"fip", 4, 324886}, {"ubootenv", 3, 126976}} {
		fmt.Fprintf(&b, "ubi0: Volume information dump:\nubi0: \tvol_id          0\nubi0: \treserved_pebs   5\nubi0: \talignment       1\nubi0: \tvol_type        %d\nubi0: \tusable_leb_size 126976\nubi0: \tused_bytes      %d\nubi0: \tname            %s\n", v.typ, v.used, v.name)
	}
	vols := parseUBIVolumes([]byte(b.String()))
	if len(vols) != 2 || vols[0].Name != "fip" || vols[0].Type != 4 || vols[0].UsedBytes != 324886 || vols[0].capacity() != 5*126976 {
		t.Fatalf("%+v", vols)
	}
	if vols[1].Name != "ubootenv" || vols[1].Type != 3 {
		t.Fatalf("%+v", vols[1])
	}
}
