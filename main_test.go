package main

import "testing"

func TestCRC16Xmodem(t *testing.T) {
	if got := crc16Xmodem([]byte("123456789")); got != 0x31c3 {
		t.Fatalf("got %04x", got)
	}
}
func TestPrompt(t *testing.T) {
	if !promptPresent([]byte("\r\nU-Boot> \r\n")) {
		t.Fatal("prompt")
	}
	if promptPresent([]byte("foo > bar")) {
		t.Fatal("false prompt")
	}
}
func TestBadBlocks(t *testing.T) {
	xs, e := parseBadBlocks([]byte("0x00020000\n0x00040000\n"), ubiSize)
	if e != nil || len(xs) != 2 {
		t.Fatalf("%v %v", xs, e)
	}
}
func TestGoodSpans(t *testing.T) {
	s := goodSpans(0, 0x80000, []uint64{0x20000})
	if len(s) != 2 || s[0][0] != 0 || s[0][1] != 0x20000 || s[1][0] != 0x40000 || s[1][1] != 0x40000 {
		t.Fatalf("%v", s)
	}
}
func TestVendorIsInformationalParser(t *testing.T) {
	if identifyVendor([]byte("Fudan Micro FM25G02B")) != "Fudan Micro" {
		t.Fatal("fudan")
	}
	if identifyVendor([]byte("mystery nand")) != "unknown/inconclusive" {
		t.Fatal("unknown")
	}
}

func TestLanguageSelection(t *testing.T) {
	defer setLang("ru")
	args, set := langFromArgs([]string{"probe", "--lang", "en", "--uart", "x"})
	if !set || uiLang != "en" || len(args) != 3 || args[0] != "probe" || args[2] != "x" {
		t.Fatalf("%v %v %s", args, set, uiLang)
	}
	if L("да", "yes") != "yes" {
		t.Fatal("L en")
	}
	if _, set := langFromArgs([]string{"--lang=ru"}); !set || L("да", "yes") != "да" {
		t.Fatal("L ru")
	}
	if setLang("de") {
		t.Fatal("unsupported language accepted")
	}
}
