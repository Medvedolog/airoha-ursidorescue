package main

import (
	"encoding/binary"
	"testing"
)

func TestParseTFTPWRQWithBlockSize(t *testing.T) {
	p := []byte{0, 2}
	p = append(p, []byte("ram.bin")...)
	p = append(p, 0)
	p = append(p, []byte("octet")...)
	p = append(p, 0)
	p = append(p, []byte("blksize")...)
	p = append(p, 0)
	p = append(p, []byte("1468")...)
	p = append(p, 0)

	op, name, mode, opts, err := parseTFTPRequest(p)
	if err != nil {
		t.Fatal(err)
	}
	if op != 2 || name != "ram.bin" || mode != "octet" || opts["blksize"] != "1468" {
		t.Fatalf("parsed op=%d name=%q mode=%q opts=%v", op, name, mode, opts)
	}
}

func TestTFTPAckEncoding(t *testing.T) {
	block := uint16(0x1234)
	ack := []byte{0, 4, byte(block >> 8), byte(block)}
	if binary.BigEndian.Uint16(ack[:2]) != 4 || binary.BigEndian.Uint16(ack[2:]) != block {
		t.Fatalf("bad ACK %x", ack)
	}
}

func TestWSPromptSeen(t *testing.T) {
	for _, s := range []string{"UrsusBoot> ", "\r\nUrsusBoot>\r\n", "noise\r\nUrsusBoot> "} {
		if !wsPromptSeen([]byte(s)) {
			t.Fatalf("prompt not seen in %q", s)
		}
	}
	if wsPromptSeen([]byte("echo UrsusBoot> not-a-prompt")) {
		t.Fatal("false prompt")
	}
}

func TestWSRAMRangeLimits(t *testing.T) {
	if wsRAMMin >= wsRAMMax || wsRAMMaxFile != 64*1024*1024 {
		t.Fatal("unexpected RAM limits")
	}
}
