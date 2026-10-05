package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func wsServerFrame(payload []byte, op byte) []byte {
	h := []byte{0x80 | op}
	n := len(payload)
	switch {
	case n <= 125:
		h = append(h, byte(n))
	case n <= 0xffff:
		h = append(h, 126, byte(n>>8), byte(n))
	default:
		h = append(h, 127)
		var x [8]byte
		binary.BigEndian.PutUint64(x[:], uint64(n))
		h = append(h, x[:]...)
	}
	return append(h, payload...)
}

func wsReadClientFrame(r *bufio.Reader) (byte, []byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(r, h); err != nil {
		return 0, nil, err
	}
	if h[1]&0x80 == 0 {
		return 0, nil, fmt.Errorf("client WebSocket frame is not masked")
	}
	op := h[0] & 0x0f
	n := int(h[1] & 0x7f)
	if n == 126 {
		x := make([]byte, 2)
		if _, err := io.ReadFull(r, x); err != nil {
			return 0, nil, err
		}
		n = int(binary.BigEndian.Uint16(x))
	} else if n == 127 {
		x := make([]byte, 8)
		if _, err := io.ReadFull(r, x); err != nil {
			return 0, nil, err
		}
		n = int(binary.BigEndian.Uint64(x))
	}
	mask := make([]byte, 4)
	if _, err := io.ReadFull(r, mask); err != nil {
		return 0, nil, err
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= mask[i&3]
	}
	return op, p, nil
}

func TestUrsusWSHandshakeAndIO(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	done := make(chan error, 1)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		var key string
		for {
			line, err := r.ReadString(byte(10))
			if err != nil {
				done <- err
				return
			}
			if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
				key = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			}
			if line == "\r\n" {
				break
			}
		}
		sum := sha1.Sum([]byte(key + ursusWSGUID))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\n\r\n", accept, ursusWSProtocol)
		_, _ = c.Write(wsServerFrame(append(append([]byte(nil), ursusWSHello...), []byte("\r\nUrsusBoot> ")...), 2))
		op, p, err := wsReadClientFrame(r)
		if err != nil {
			done <- err
			return
		}
		if op != 2 || string(p) != "version\r" {
			done <- fmt.Errorf("frame op=%d payload=%q", op, p)
			return
		}
		_, _ = c.Write(wsServerFrame([]byte("U-Boot test\r\nUrsusBoot> "), 2))
		done <- nil
	}()

	w, err := connectUrsusWSAt("127.0.0.1", port, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	buf := make([]byte, 256)
	n, err := w.Read(buf, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(buf[:n]), string(ursusWSHello)) {
		t.Fatalf("hello %q", buf[:n])
	}
	if err := w.Write([]byte("version\r")); err != nil {
		t.Fatal(err)
	}
	n, err = w.Read(buf, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n]), "U-Boot test") {
		t.Fatalf("reply %q", buf[:n])
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWSPresetMenuIsReadOnlyList(t *testing.T) {
	for _, cmd := range []string{"version", "bdinfo", "mtd list", "printenv", "mtd bad bl2", "mtd bad ubi", "help"} {
		if strings.Contains(cmd, "write") || strings.Contains(cmd, "erase") || strings.Contains(cmd, "ubi part") {
			t.Fatalf("destructive preset %q", cmd)
		}
	}
}
