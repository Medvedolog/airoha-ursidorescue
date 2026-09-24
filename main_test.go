package main

import "testing"

func TestEventTone(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"TFTP PASS", uiOK},
		{"XMODEM retry", uiAmber2},
		{"WARNING network", uiSand},
		{"ошибка записи", uiBad},
		{"U-Boot: version", uiInk},
	}
	for _, tc := range cases {
		if got := eventTone(tc.msg); got != tc.want {
			t.Fatalf("eventTone(%q)=%q want %q", tc.msg, got, tc.want)
		}
	}
}

func TestGenericRAMFileLimitIs128MiB(t *testing.T) {
	if got := maxGenericRAMFile / (1024 * 1024); got != 128 {
		t.Fatalf("maxGenericRAMFile = %d MiB, want 128", got)
	}
}

func TestCRC16Xmodem(t *testing.T) {
	if got := crc16Xmodem([]byte("123456789")); got != 0x31c3 {
		t.Fatalf("got %04x", got)
	}
}

func TestXmodemReplyNoiseAndCancel(t *testing.T) {
	can := 0
	if got := scanXmodemReply([]byte{0x18}, &can); got != xmodemReplyNone || can != 1 {
		t.Fatalf("single CAN must not cancel: reply=%v can=%d", got, can)
	}
	if got := scanXmodemReply([]byte{0x06}, &can); got != xmodemReplyACK || can != 0 {
		t.Fatalf("ACK after one noisy CAN must win: reply=%v can=%d", got, can)
	}
	can = 0
	if got := scanXmodemReply([]byte{0x18, 0x18}, &can); got != xmodemReplyCancel {
		t.Fatalf("CAN CAN must cancel: reply=%v", got)
	}
	can = 0
	if got := scanXmodemReply([]byte{'C'}, &can); got != xmodemReplyRetry {
		t.Fatalf("CRC request must trigger immediate retry: reply=%v", got)
	}
	can = 0
	if got := scanXmodemReply([]byte{0x15}, &can); got != xmodemReplyRetry {
		t.Fatalf("NAK must trigger immediate retry: reply=%v", got)
	}
	can = 0
	if got := scanXmodemReply([]byte{'C', 0x06}, &can); got != xmodemReplyACK {
		t.Fatalf("ACK in the same read must win over earlier C noise: reply=%v", got)
	}
}

func TestXmodemEOTHandoffClassifier(t *testing.T) {
	can := 0
	if got, handoff := scanXmodemEOTReply([]byte{0x15}, &can); got != xmodemReplyRetry || handoff {
		t.Fatalf("EOT NAK must request one more EOT: reply=%v handoff=%v", got, handoff)
	}
	can = 0
	if got, handoff := scanXmodemEOTReply([]byte("NOTICE: BL31 starting\r\n"), &can); got != xmodemReplyNone || !handoff {
		t.Fatalf("boot text after EOT must be treated as next-stage handoff: reply=%v handoff=%v", got, handoff)
	}
	can = 0
	if got, handoff := scanXmodemEOTReply([]byte{'C', 'C', 'C'}, &can); got != xmodemReplyNone || !handoff {
		t.Fatalf("next receiver C stream after EOT must be handoff evidence: reply=%v handoff=%v", got, handoff)
	}
	can = 0
	if got, handoff := scanXmodemEOTReply([]byte{0x06}, &can); got != xmodemReplyACK || handoff {
		t.Fatalf("EOT ACK must remain definitive: reply=%v handoff=%v", got, handoff)
	}
}
func TestPrompt(t *testing.T) {
	good := [][]byte{
		[]byte("\r\nU-Boot> \r\n"),
		[]byte("AN7583> "),
		[]byte("\x1b[2J\x1b[H    *** U-Boot Boot Menu ***\x1b[20;1HAN7583> \x1b[?25h"),
		[]byte("menu text without newline\x1b[24;1H=> "),
	}
	for _, in := range good {
		if !promptPresent(in) {
			t.Fatalf("prompt not detected in %q", in)
		}
	}
	bad := [][]byte{
		[]byte("foo > bar"),
		[]byte("AN7583> still printing"),
		[]byte("echo U-Boot> not-a-prompt"),
	}
	for _, in := range bad {
		if promptPresent(in) {
			t.Fatalf("false prompt in %q", in)
		}
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
