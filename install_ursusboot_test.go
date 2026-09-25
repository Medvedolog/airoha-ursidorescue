package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ursidorescue/app"
)

// ubootSim answers the few U-Boot commands the installer's read path sends:
// md.l, crc32, mtd read and the rc echo.
type ubootSim struct {
	mu       sync.Mutex
	ram      map[uint64]byte
	nand     map[string][]byte
	line     []byte
	out      []byte
	corrupt  int // corrupt this many md.l dumps
	crcLie   int // report a wrong crc32 this many times
	commands []string
}

func (u *ubootSim) Name() string      { return "sim" }
func (u *ubootSim) Close() error      { return nil }
func (u *ubootSim) ResetInput() error { return nil }
func (u *ubootSim) Read(b []byte, d time.Duration) (int, error) {
	u.mu.Lock()
	n := copy(b, u.out)
	u.out = u.out[n:]
	u.mu.Unlock()
	if n == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	return n, nil
}
func (u *ubootSim) Write(b []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, c := range b {
		if c != '\r' {
			u.line = append(u.line, c)
			continue
		}
		cmd := string(u.line)
		u.line = nil
		u.commands = append(u.commands, cmd)
		u.out = append(u.out, []byte(cmd+"\r\n"+u.run(cmd)+"AN7581> ")...)
	}
	return nil
}

func hexArg(s string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
	return v
}

func (u *ubootSim) run(cmd string) string {
	f := strings.Fields(cmd)
	switch {
	case f[0] == "echo":
		return strings.Replace(f[1], "$?", "0", 1) + "\r\n"
	case f[0] == "md.l":
		addr, n := hexArg(f[1]), hexArg(f[2])
		var b strings.Builder
		for i := uint64(0); i < n; i += 4 {
			fmt.Fprintf(&b, "%08x:", addr+i*4)
			for j := i; j < i+4 && j < n; j++ {
				var w [4]byte
				for k := range w {
					w[k] = u.ram[addr+j*4+uint64(k)]
				}
				v := binary.LittleEndian.Uint32(w[:])
				if u.corrupt > 0 && j == 0 {
					v ^= 1
				}
				fmt.Fprintf(&b, " %08x", v)
			}
			b.WriteString("    ................\r\n")
		}
		if u.corrupt > 0 {
			u.corrupt--
		}
		return b.String()
	case f[0] == "mw.b":
		addr, v, n := hexArg(f[1]), hexArg(f[2]), hexArg(f[3])
		for i := uint64(0); i < n; i++ {
			u.ram[addr+i] = byte(v)
		}
		return ""
	case f[0] == "crc32":
		addr, n := hexArg(f[1]), hexArg(f[2])
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = u.ram[addr+uint64(i)]
		}
		c := crc32.ChecksumIEEE(buf)
		if u.crcLie > 0 {
			u.crcLie--
			c ^= 1
		}
		return fmt.Sprintf("crc32 for %08x ... %08x ==> %08x\r\n", addr, addr+n-1, c)
	case f[0] == "mtd" && f[1] == "read":
		src := u.nand[f[2]]
		addr, off, n := hexArg(f[3]), hexArg(f[4]), hexArg(f[5])
		for i := uint64(0); i < n; i++ {
			u.ram[addr+i] = src[off+i]
		}
		return "Reading done\r\n"
	}
	return "Unknown command '" + f[0] + "'\r\n"
}

func newSim(bl2, ubi []byte) *ubootSim {
	return &ubootSim{ram: map[uint64]byte{}, nand: map[string][]byte{"bl2": bl2, "ubi": ubi}}
}

func TestUARTDumpRetriesAndLayout(t *testing.T) {
	rec := &app.Recorder{}
	a := &App{ui: rec.UI()}

	bl2 := bytes.Repeat([]byte{0xff}, 0x1000)
	binary.LittleEndian.PutUint32(bl2[bootFIPOff:], fipMagic)
	stock := newSim(bl2, bytes.Repeat([]byte{0x5a}, 0x800))
	if l, err := a.bootLayout(stock); err != nil || l != "stock" {
		t.Fatalf("stock layout: %q %v", l, err)
	}
	ubi := newSim(bl2, append([]byte("UBI#"), make([]byte, 0x7fc)...))
	if l, err := a.bootLayout(ubi); err != nil || l != "ubi" {
		t.Fatalf("ubi layout: %q %v", l, err)
	}
	blank := newSim(bytes.Repeat([]byte{0xff}, 0x1000), bytes.Repeat([]byte{0xff}, 0x800))
	if _, err := a.bootLayout(blank); err == nil {
		t.Fatal("unknown layout accepted")
	}

	sim := newSim(nil, nil)
	want := make([]byte, 0x100)
	for i := range want {
		want[i] = byte(i * 7)
		sim.ram[loadAddr+uint64(i)] = want[i]
	}
	sim.corrupt = 1
	got, err := a.uartDump(sim, loadAddr, uint64(len(want)), "test")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("dump: %v", err)
	}
	var md int
	for _, c := range sim.commands {
		if strings.HasPrefix(c, "md.l") {
			md++
		}
	}
	if md != 2 {
		t.Fatalf("a corrupted dump must be read again once, md.l sent %d times", md)
	}
	if a.quietUART {
		t.Fatal("quiet UART left on")
	}
}

func TestBootAreaReadback(t *testing.T) {
	a := &App{ui: (&app.Recorder{}).UI()}
	area := make([]byte, bootAreaSize)
	for i := range area {
		area[i] = byte(i * 13)
	}
	sim := newSim(area[:bl2Size], area[bl2Size:])
	if err := a.bootAreaReadback(sim, crc32.ChecksumIEEE(area)); err != nil {
		t.Fatal(err)
	}
	sim = newSim(area[:bl2Size], area[bl2Size:])
	if err := a.bootAreaReadback(sim, crc32.ChecksumIEEE(area)^1); err == nil {
		t.Fatal("a wrong CRC passed")
	}
}

// A readback mismatch is read again before it fails the write.
func TestReadbackCRCRetries(t *testing.T) {
	a := &App{ui: (&app.Recorder{}).UI()}
	part := bytes.Repeat([]byte{0x3c}, 0x1000)
	want := crc32.ChecksumIEEE(part)
	sim := newSim(nil, part)
	sim.crcLie = 2
	if err := a.readbackCRC(sim, "ubi", 0, 0x1000, verifyAddr, want); err != nil {
		t.Fatalf("two bad reads then a good one must pass: %v", err)
	}
	sim = newSim(nil, part)
	sim.crcLie = 3
	if err := a.readbackCRC(sim, "ubi", 0, 0x1000, verifyAddr, want); err == nil {
		t.Fatal("three mismatches must fail")
	}
}
