package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ursidorescue/app"
)

const (
	wsRAMLoadAddr = 0x81800000
	wsRAMMin      = 0x80000000
	wsRAMMax      = 0xC0000000
	wsRAMMaxFile  = 64 * 1024 * 1024
)

var wsSHA256RE = regexp.MustCompile("(?i)\\b[0-9a-f]{64}\\b")

func wsReadUntil(w *ursusWSPort, timeout time.Duration, ready func([]byte) bool) ([]byte, error) {
	end := time.Now().Add(timeout)
	var out []byte
	buf := make([]byte, 4096)
	for time.Now().Before(end) {
		n, err := w.Read(buf, 300*time.Millisecond)
		if err != nil {
			return out, err
		}
		if n == 0 {
			continue
		}
		out = append(out, buf[:n]...)
		if len(out) > 256*1024 {
			out = out[len(out)-256*1024:]
		}
		if ready(out) {
			return out, nil
		}
	}
	return out, errors.New("UrsusBoot command timeout")
}

func wsPromptSeen(b []byte) bool {
	b = bytes.TrimRight(b, " \t\r\n\x00")
	return bytes.HasSuffix(b, []byte("UrsusBoot>"))
}

func wsCommand(w *ursusWSPort, cmd string, timeout time.Duration) ([]byte, error) {
	if err := w.Write([]byte(cmd + "\r")); err != nil {
		return nil, err
	}
	out, err := wsReadUntil(w, timeout, wsPromptSeen)
	if err != nil {
		return out, err
	}
	low := strings.ToLower(string(out))
	if strings.Contains(low, "unknown command") || strings.Contains(low, "usage:") {
		return out, fmt.Errorf("UrsusBoot rejected command %q", cmd)
	}
	return out, nil
}

func (a *App) wsXmodemSend(host, path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() <= 0 || st.Size() > wsRAMMaxFile {
		return errors.New(L("XMODEM: размер должен быть 1..64 МиБ", "XMODEM size must be 1..64 MiB"))
	}
	expected, err := shaFile(path)
	if err != nil {
		return err
	}

	w, err := connectUrsusWS(host, 8*time.Second)
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.Write([]byte(fmt.Sprintf("loadx 0x%x\r", wsRAMLoadAddr))); err != nil {
		return err
	}
	_, err = wsReadUntil(w, 30*time.Second, func(b []byte) bool {
		low := bytes.ToLower(b)
		if bytes.Contains(low, []byte("unknown command")) || bytes.Contains(low, []byte("usage:")) {
			return true
		}
		pos := bytes.Index(low, []byte("ready for binary (xmodem)"))
		return pos >= 0 && bytes.Contains(b[pos:], []byte{'C'})
	})
	if err != nil {
		return fmt.Errorf("UrsusBoot loadx readiness: %w", err)
	}

	a.event(L("UrsusBoot loadx готов; XMODEM идёт поверх того же WebSocket", "UrsusBoot loadx ready; XMODEM uses the same WebSocket"))
	res, err := a.xmodemSend(w, path, filepath.Base(path))
	if err != nil {
		return err
	}
	if len(res.Trailing) > 0 {
		a.logBytes(res.Trailing, false)
	}

	_, _ = wsCommand(w, "", 8*time.Second)
	out, err := wsCommand(w, fmt.Sprintf("hash sha256 0x%x 0x%x", wsRAMLoadAddr, st.Size()), 30*time.Second)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(string(out)), strings.ToLower(expected)) {
		return errors.New(L("SHA256 файла в RAM не совпал с локальным", "RAM file SHA256 does not match the local file"))
	}
	a.status(L("ГОТОВО", "DONE"), fmt.Sprintf(L("%d байт в RAM через XMODEM/WebSocket, SHA256 совпал; flash не записывалась", "%d bytes in RAM over XMODEM/WebSocket, SHA256 matched; flash was not written"), st.Size()), app.LevelOK)
	return nil
}

type tftpPutResult struct {
	bytes int64
	err   error
}

func parseTFTPRequest(p []byte) (op uint16, name, mode string, opts map[string]string, err error) {
	if len(p) < 4 {
		return 0, "", "", nil, errors.New("short TFTP request")
	}
	op = binary.BigEndian.Uint16(p[:2])
	parts := bytes.Split(p[2:], []byte{0})
	if len(parts) < 3 {
		return 0, "", "", nil, errors.New("malformed TFTP request")
	}
	name = string(parts[0])
	mode = strings.ToLower(string(parts[1]))
	opts = map[string]string{}
	tail := parts[2:]
	if len(tail) > 0 && len(tail[len(tail)-1]) == 0 {
		tail = tail[:len(tail)-1]
	}
	for i := 0; i+1 < len(tail); i += 2 {
		opts[strings.ToLower(string(tail[i]))] = string(tail[i+1])
	}
	return
}

func runTFTPPutReceiver(bindIP string, port int, output, expectedName, allowedHost string, ready chan error, done chan tftpPutResult) {
	res := tftpPutResult{}
	finish := func(err error) {
		res.err = err
		done <- res
	}

	addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(bindIP, strconv.Itoa(port)))
	if err != nil {
		ready <- err
		finish(err)
		return
	}
	c, err := net.ListenUDP("udp4", addr)
	if err != nil {
		ready <- err
		finish(err)
		return
	}
	defer c.Close()
	ready <- nil

	buf := make([]byte, 65535)
	deadline := time.Now().Add(120 * time.Second)
	var peer *net.UDPAddr
	var opts map[string]string
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		n, a, er := c.ReadFromUDP(buf)
		if er != nil {
			if ne, ok := er.(net.Error); ok && ne.Timeout() {
				continue
			}
			if isExpectedUDPNoise(er) {
				continue
			}
			finish(er)
			return
		}
		if allowedHost != "" && a.IP.String() != allowedHost {
			continue
		}
		op, name, mode, o, er := parseTFTPRequest(buf[:n])
		if er != nil || op != 2 || name != expectedName || mode != "octet" {
			continue
		}
		peer, opts = a, o
		break
	}
	if peer == nil {
		finish(errors.New("TFTP PUT timed out waiting for WRQ"))
		return
	}

	blockSize := 512
	if x := opts["blksize"]; x != "" {
		if n, er := strconv.Atoi(x); er == nil {
			blockSize = max(512, min(n, 4096))
		}
	}
	var response []byte
	if _, ok := opts["blksize"]; ok {
		response = append([]byte{0, 6}, []byte("blksize\x00"+strconv.Itoa(blockSize)+"\x00")...)
	} else {
		response = []byte{0, 4, 0, 0}
	}
	_, _ = c.WriteToUDP(response, peer)
	lastResponse := response

	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		finish(err)
		return
	}
	defer f.Close()

	expectedBlock := uint16(1)
	retries := 0
	for {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		n, a, er := c.ReadFromUDP(buf)
		if er != nil {
			if ne, ok := er.(net.Error); ok && ne.Timeout() {
				retries++
				if retries >= 120 {
					finish(errors.New("TFTP PUT timeout"))
					return
				}
				_, _ = c.WriteToUDP(lastResponse, peer)
				continue
			}
			if isExpectedUDPNoise(er) {
				continue
			}
			finish(er)
			return
		}
		if a.String() != peer.String() || n < 4 {
			continue
		}
		op := binary.BigEndian.Uint16(buf[:2])
		block := binary.BigEndian.Uint16(buf[2:4])
		if op == 5 {
			finish(errors.New("TFTP client returned ERROR"))
			return
		}
		if op != 3 {
			continue
		}
		payload := buf[4:n]
		switch {
		case block == expectedBlock:
			if _, er := f.Write(payload); er != nil {
				finish(er)
				return
			}
			res.bytes += int64(len(payload))
			ack := []byte{0, 4, byte(block >> 8), byte(block)}
			_, _ = c.WriteToUDP(ack, peer)
			lastResponse = ack
			retries = 0
			expectedBlock++
			if len(payload) < blockSize {
				_ = f.Sync()
				finish(nil)
				return
			}
		case block == expectedBlock-1:
			ack := []byte{0, 4, byte(block >> 8), byte(block)}
			_, _ = c.WriteToUDP(ack, peer)
		}
	}
}

func localIPForHost(host string) (string, error) {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(host), Port: 9})
	if err != nil {
		return "", err
	}
	defer c.Close()
	a, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || a.IP == nil || a.IP.IsUnspecified() {
		return "", errors.New("cannot determine local IP for UrsusBoot")
	}
	return a.IP.String(), nil
}

func chooseTFTPPutPort(bindIP string) (int, error) {
	for _, p := range []int{defaultTFTPPort, 0} {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(bindIP), Port: p})
		if err != nil {
			continue
		}
		port := c.LocalAddr().(*net.UDPAddr).Port
		_ = c.Close()
		return port, nil
	}
	return 0, errors.New("no UDP port available for TFTP PUT")
}

func (a *App) wsReceiveRAM(host string) error {
	raw := strings.TrimSpace(a.ask(L("Адрес RAM [0x81800000]: ", "RAM address [0x81800000]: ")))
	if raw == "" {
		raw = "0x81800000"
	}
	addr, err := strconv.ParseUint(raw, 0, 64)
	if err != nil {
		return errors.New(L("неверный адрес RAM", "invalid RAM address"))
	}
	raw = strings.TrimSpace(a.ask(L("Длина, например 0x100000: ", "Length, e.g. 0x100000: ")))
	size, err := strconv.ParseUint(raw, 0, 64)
	if err != nil || size == 0 || size > wsRAMMaxFile || addr < wsRAMMin || addr+size > wsRAMMax {
		return errors.New(L("диапазон должен быть внутри 0x80000000..0xC0000000 и не больше 64 МиБ", "range must be within 0x80000000..0xC0000000 and at most 64 MiB"))
	}

	dir := a.sessionScratch("ws-ram")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	def := filepath.Join(dir, fmt.Sprintf("ursus-ram-%d.bin", time.Now().Unix()))
	out := strings.TrimSpace(strings.Trim(a.ask(fmt.Sprintf(L("Файл на ПК [%s]: ", "PC output file [%s]: "), def)), "\""))
	if out == "" {
		out = def
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return err
	}
	if fileExists(out) || fileExists(out+".partial") {
		return fmt.Errorf(L("файл уже существует: %s", "file already exists: %s"), out)
	}

	w, err := connectUrsusWS(host, 8*time.Second)
	if err != nil {
		return err
	}
	defer w.Close()

	before, err := wsCommand(w, fmt.Sprintf("hash sha256 0x%x 0x%x", addr, size), 60*time.Second)
	if err != nil {
		return err
	}
	m := wsSHA256RE.Find(before)
	if len(m) != 64 {
		return errors.New("UrsusBoot did not return SHA256 for the RAM range")
	}
	expected := strings.ToLower(string(m))

	local, err := localIPForHost(host)
	if err != nil {
		return err
	}
	port, err := chooseTFTPPutPort(local)
	if err != nil {
		return err
	}
	remote := fmt.Sprintf("ursus-ram-%d.bin", time.Now().UnixNano())
	partial := out + ".partial"
	ready := make(chan error, 1)
	done := make(chan tftpPutResult, 1)
	go runTFTPPutReceiver(local, port, partial, remote, host, ready, done)
	if err := <-ready; err != nil {
		return err
	}
	a.event(fmt.Sprintf(L("TFTP PUT: принимаю %d байт на %s:%d", "TFTP PUT: receiving %d bytes on %s:%d"), size, local, port))

	console, cmdErr := wsCommand(w, fmt.Sprintf("tftpput 0x%x 0x%x %s:%d:%s", addr, size, local, port, remote), max(90*time.Second, time.Duration(size/10000)*time.Second))
	res := <-done
	if cmdErr != nil {
		_ = os.Remove(partial)
		return cmdErr
	}
	if res.err != nil || uint64(res.bytes) != size {
		_ = os.Remove(partial)
		return fmt.Errorf("TFTP PUT incomplete: %d/%d: %v", res.bytes, size, res.err)
	}
	if bytes.Contains(console, []byte("TFTP upload incomplete")) || bytes.Contains(console, []byte("TFTP error")) {
		_ = os.Remove(partial)
		return errors.New("UrsusBoot reported TFTP upload error")
	}

	f, err := os.Open(partial)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	_ = f.Close()
	if err != nil {
		return err
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != expected {
		_ = os.Remove(partial)
		return fmt.Errorf("RAM SHA256 %s differs from saved file %s", expected, got)
	}
	after, err := wsCommand(w, fmt.Sprintf("hash sha256 0x%x 0x%x", addr, size), 60*time.Second)
	if err != nil || !strings.Contains(strings.ToLower(string(after)), expected) {
		_ = os.Remove(partial)
		return errors.New("RAM range changed during TFTP export")
	}
	if err := os.Rename(partial, out); err != nil {
		return err
	}
	a.ui.Artifact(app.Artifact{Kind: "ws-ram", Label: L("RAM сохранена:", "RAM saved:"), Path: out, SHA256: got})
	a.status(L("ГОТОВО", "DONE"), fmt.Sprintf(L("%d байт · SHA256 %s · flash не изменялась", "%d bytes · SHA256 %s · flash was not modified"), size, got), app.LevelOK)
	return nil
}

func (a *App) wsDownloadMenu(host string) error {
	v := strings.TrimSpace(a.ask(L("Получить: 1 диагностика · 2 диапазон RAM через TFTP PUT [Enter — назад]: ",
		"Receive: 1 diagnostics · 2 RAM range over TFTP PUT [Enter — back]: ")))
	switch v {
	case "1":
		return a.wsSaveDiagnostics(host)
	case "2":
		return a.wsReceiveRAM(host)
	default:
		return nil
	}
}
