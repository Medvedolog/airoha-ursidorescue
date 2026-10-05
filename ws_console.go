package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ursidorescue/app"
)

// UrsusBoot live console transport, borrowed from the current UrsusFlasher
// implementation. It deliberately uses only the Go standard library: the
// WebSocket handshake/framing is small and pinned to one protocol.
const (
	ursusWSProtocol = "ursusboot-console-v1"
	ursusWSGUID     = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	ursusWSMaxFrame = 1 << 20
	ursusWSChunk    = 0x10000
)

var ursusWSHello = []byte("URSUS_WS_HELLO protocol=1 product=UrsusBoot transport=live-stdio")

type ursusWSPort struct {
	conn    net.Conn
	br      *bufio.Reader
	host    string
	pending []byte

	readMu  sync.Mutex
	writeMu sync.Mutex
	stateMu sync.Mutex
	closed  bool
	action  byte
}

func (w *ursusWSPort) Name() string { return "ws://" + w.host }
func (w *ursusWSPort) Host() string { return w.host }

func (w *ursusWSPort) setAction(a byte) {
	w.stateMu.Lock()
	w.action = a
	w.stateMu.Unlock()
}

func (w *ursusWSPort) takeAction() byte {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	a := w.action
	w.action = 0
	return a
}

func isUrsusWS(s Serial) (*ursusWSPort, bool) {
	w, ok := s.(*ursusWSPort)
	return w, ok
}

func connectUrsusWS(host string, timeout time.Duration) (*ursusWSPort, error) {
	return connectUrsusWSAt(host, 80, timeout)
}

func connectUrsusWSAt(host string, port int, timeout time.Duration) (*ursusWSPort, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = defaultRouterIP
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("UrsusBoot WebSocket %s: %w", host, err)
	}
	fail := func(e error) (*ursusWSPort, error) {
		_ = conn.Close()
		return nil, e
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return fail(err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	hostHeader := host
	if port != 80 {
		hostHeader = net.JoinHostPort(host, strconv.Itoa(port))
	}
	req := fmt.Sprintf("GET /ws/console HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: %s\r\n\r\n",
		hostHeader, key, ursusWSProtocol)
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := io.WriteString(conn, req); err != nil {
		return fail(err)
	}

	br := bufio.NewReader(conn)
	tp := textproto.NewReader(br)
	status, err := tp.ReadLine()
	if err != nil {
		return fail(fmt.Errorf("live console closed during WebSocket handshake: %w", err))
	}
	if !strings.Contains(status, " 101 ") {
		if strings.Contains(status, " 409 ") {
			return fail(fmt.Errorf("live console WebSocket upgrade failed: %s; another console slot is still active (UrsusBoot t79+ normally releases stale sessions after about a minute)", status))
		}
		return fail(fmt.Errorf("live console WebSocket upgrade failed: %s", status))
	}
	h, err := tp.ReadMIMEHeader()
	if err != nil {
		return fail(fmt.Errorf("WebSocket headers: %w", err))
	}
	sum := sha1.Sum([]byte(key + ursusWSGUID))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if h.Get("Sec-WebSocket-Accept") != want {
		return fail(errors.New("live console WebSocket accept hash mismatch"))
	}
	if h.Get("Sec-WebSocket-Protocol") != ursusWSProtocol {
		return fail(fmt.Errorf("live console protocol mismatch: %q", h.Get("Sec-WebSocket-Protocol")))
	}

	w := &ursusWSPort{conn: conn, br: br, host: host}
	_ = conn.SetDeadline(time.Time{})
	hello, err := w.recvMessage(timeout)
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if !bytes.HasPrefix(hello, ursusWSHello) {
		_ = w.Close()
		return nil, fmt.Errorf("live console UrsusBoot hello mismatch: %q", hello[:min(len(hello), 160)])
	}
	// Let the ordinary terminal reader display the machine-readable hello too.
	w.pending = append(w.pending, hello...)
	return w, nil
}

func (w *ursusWSPort) recvExact(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(w.br, b)
	return b, err
}

func (w *ursusWSPort) sendFrame(op byte, payload []byte) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	w.stateMu.Lock()
	closed := w.closed
	w.stateMu.Unlock()
	if closed {
		return io.EOF
	}

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	n := len(payload)
	head := []byte{0x80 | op}
	switch {
	case n <= 125:
		head = append(head, 0x80|byte(n))
	case n <= 0xffff:
		head = append(head, 0x80|126, byte(n>>8), byte(n))
	default:
		head = append(head, 0x80|127)
		var x [8]byte
		binary.BigEndian.PutUint64(x[:], uint64(n))
		head = append(head, x[:]...)
	}
	head = append(head, mask...)
	out := make([]byte, len(payload))
	for i := range payload {
		out[i] = payload[i] ^ mask[i&3]
	}
	if _, err := w.conn.Write(head); err != nil {
		return err
	}
	_, err := w.conn.Write(out)
	return err
}

func (w *ursusWSPort) recvMessage(timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		_ = w.conn.SetReadDeadline(time.Now().Add(timeout))
	} else {
		_ = w.conn.SetReadDeadline(time.Time{})
	}
	defer w.conn.SetReadDeadline(time.Time{})

	for {
		h, err := w.recvExact(2)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil, ne
			}
			return nil, err
		}
		fin := h[0]&0x80 != 0
		op := h[0] & 0x0f
		if h[0]&0x70 != 0 || !fin {
			return nil, errors.New("unsupported fragmented/reserved WebSocket frame")
		}
		if h[1]&0x80 != 0 {
			return nil, errors.New("server sent an invalid masked WebSocket frame")
		}
		n := uint64(h[1] & 0x7f)
		if n == 126 {
			x, err := w.recvExact(2)
			if err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(x))
		} else if n == 127 {
			x, err := w.recvExact(8)
			if err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(x)
		}
		if n > ursusWSMaxFrame {
			return nil, fmt.Errorf("live console frame too large: %d", n)
		}
		payload, err := w.recvExact(int(n))
		if err != nil {
			return nil, err
		}
		switch op {
		case 1, 2:
			return payload, nil
		case 8:
			return nil, io.EOF
		case 9:
			if err := w.sendFrame(10, payload); err != nil {
				return nil, err
			}
		case 10:
			// pong
		default:
			return nil, fmt.Errorf("unsupported WebSocket opcode %d", op)
		}
	}
}

func (w *ursusWSPort) Read(p []byte, timeout time.Duration) (int, error) {
	w.readMu.Lock()
	defer w.readMu.Unlock()
	if len(w.pending) > 0 {
		n := copy(p, w.pending)
		w.pending = w.pending[n:]
		return n, nil
	}
	msg, err := w.recvMessage(timeout)
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return 0, nil
		}
		return 0, err
	}
	n := copy(p, msg)
	if n < len(msg) {
		w.pending = append(w.pending[:0], msg[n:]...)
	}
	return n, nil
}

func (w *ursusWSPort) Write(p []byte) error {
	// UrsusBoot's live transport intentionally keeps frames small.
	for len(p) > 0 {
		n := min(len(p), 1024)
		if err := w.sendFrame(2, p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

func (w *ursusWSPort) ResetInput() error {
	// WebSocket frames are already ordered and the XMODEM readiness byte may
	// be the last byte emitted by loadx. Never discard it here.
	return nil
}

func (w *ursusWSPort) Close() error {
	w.stateMu.Lock()
	if w.closed {
		w.stateMu.Unlock()
		return nil
	}
	w.stateMu.Unlock()

	_ = w.sendFrame(8, []byte{0x03, 0xe8})
	w.stateMu.Lock()
	w.closed = true
	w.stateMu.Unlock()
	_ = w.conn.SetDeadline(time.Now())
	return w.conn.Close()
}

func ursusWSHost() string {
	for _, k := range []string{"URSUSBOOT_IP", "NOKIA_ROUTER_IP"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return defaultRouterIP
}

// runUrsusWSConsole is the Ethernet sibling of the UART terminal. It owns no
// serial port, so it can be used when UrsusBoot/WebFailsafe is already running.
func (a *App) runUrsusWSConsole() error {
	host := ursusWSHost()
	if _, err := a.startLog("ws-console"); err != nil {
		return err
	}
	defer a.closeLog()

	for {
		a.note(fmt.Sprintf(L("UrsusBoot Ethernet-консоль: %s · WebSocket /ws/console. Прямой кабель рекомендуется; F10 — выход.",
			"UrsusBoot Ethernet console: %s · WebSocket /ws/console. A direct cable is recommended; F10 quits."), host))
		ws, err := connectUrsusWS(host, 8*time.Second)
		if err != nil {
			return err
		}
		err = a.runTerminalOnMode(ws, false)
		action := ws.takeAction()
		_ = ws.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		switch action {
		case 'u':
			if err := a.wsUploadRAM(host); err != nil {
				a.status("!", err.Error(), app.LevelWarn)
			}
		case 'd':
			if err := a.wsDownloadMenu(host); err != nil {
				a.status("!", err.Error(), app.LevelWarn)
			}
		default:
			return nil
		}
		v := strings.ToLower(a.askQuick(L("Вернуться к Ethernet-консоли? [Y/n]: ", "Return to the Ethernet console? [Y/n]: "), "y",
			app.Choice{Key: "y", Label: L("Да", "Yes")},
			app.Choice{Key: "n", Label: L("Нет", "No")}))
		if v == "n" || v == "no" || v == "н" || v == "нет" {
			return nil
		}
	}
}

func wsJSON(host, method, path string, headers map[string]string, body []byte, timeout time.Duration) (map[string]any, error) {
	req, err := http.NewRequest(method, "http://"+host+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if len(data) != 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("%s %s: HTTP %d, invalid JSON: %q", method, path, resp.StatusCode, data[:min(len(data), 300)])
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return out, nil
}

func (a *App) wsUploadRAM(host string) error {
	a.ui.Event(app.Event{Kind: app.KindSectionStart, Text: L("ФАЙЛ В RAM URSUSBOOT", "FILE TO URSUSBOOT RAM")})
	defer a.ui.Event(app.Event{Kind: app.KindSectionEnd})

	v, _ := a.ui.Ask(app.AskRequest{
		Kind:  app.AskChoice,
		Title: L("Тип файла — только приём в RAM, flash не записывается:", "File type — RAM receive only; flash is not written:"),
		Choices: []app.Choice{
			{Key: "1", Label: "initramfs"},
			{Key: "2", Label: L("прошивка OpenWrt", "OpenWrt firmware")},
			{Key: "3", Label: "UrsusBoot FIP"},
			{Key: "4", Label: "Vanilla FIP"},
			{Key: "5", Label: "UBI preloader"},
			{Key: "6", Label: L("произвольный файл через XMODEM", "arbitrary file over XMODEM")},
		},
		Prompt: L("Тип [Enter — назад]: ", "Type [Enter — back]: "),
	})
	endpoints := map[string][2]string{
		"1": {"/api/initramfs-begin", "/api/initramfs-chunk"},
		"2": {"/api/firmware-begin", "/api/firmware-chunk"},
		"3": {"/api/ursus-fip-begin", "/api/ursus-fip-chunk"},
		"4": {"/api/vanilla-fip-begin", "/api/vanilla-fip-chunk"},
		"5": {"/api/ubi-preloader-begin", "/api/ubi-preloader-chunk"},
	}
	choice := strings.TrimSpace(v)
	if choice != "6" {
		if _, ok := endpoints[choice]; !ok {
			return nil
		}
	}

	path, err := a.askPath(L("Файл: ", "File: "))
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() <= 0 {
		return errors.New(L("файл пуст", "file is empty"))
	}
	if choice == "6" {
		return a.wsXmodemSend(host, path)
	}
	ep := endpoints[choice]

	gen := fmt.Sprintf("ursido-%x", time.Now().UnixMilli())
	total := st.Size()
	base := map[string]string{
		"X-Ursus-Generation": gen,
		"X-Ursus-Total":      strconv.FormatInt(total, 10),
		"X-Ursus-Filename":   url.QueryEscape(filepath.Base(path)),
	}
	ack, err := wsJSON(host, "POST", ep[0], base, nil, 30*time.Second)
	if err != nil {
		return fmt.Errorf("upload begin: %w", err)
	}
	g, _ := ack["generation"].(string)
	declared, dok := jsonNumberInt64(ack["declared_size"])
	received, rok := jsonNumberInt64(ack["received"])
	if g != gen || !dok || declared != total || !rok || received != 0 {
		return fmt.Errorf("upload begin ACK mismatch: generation=%q declared=%v received=%v", g, ack["declared_size"], ack["received"])
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, ursusWSChunk)
	var off int64
	for off < total {
		n, er := io.ReadFull(f, buf)
		if er == io.ErrUnexpectedEOF {
			er = nil
		}
		if er == io.EOF && n == 0 {
			break
		}
		if er != nil {
			return er
		}
		h := map[string]string{
			"Content-Type":       "application/octet-stream",
			"X-Ursus-Generation": gen,
			"X-Ursus-Offset":     strconv.FormatInt(off, 10),
			"X-Ursus-Total":      strconv.FormatInt(total, 10),
		}
		chunkAck, err := wsJSON(host, "POST", ep[1], h, buf[:n], 90*time.Second)
		if err != nil {
			return fmt.Errorf(L("передача оборвалась на %d/%d; flash не затронута: %w", "transfer stopped at %d/%d; flash was not touched: %w"), off, total, err)
		}
		off += int64(n)
		cg, _ := chunkAck["generation"].(string)
		cd, cdok := jsonNumberInt64(chunkAck["declared_size"])
		got, gok := jsonNumberInt64(chunkAck["received"])
		if cg != gen || !cdok || cd != total || !gok || got != off {
			return fmt.Errorf("upload chunk ACK mismatch: generation=%q declared=%v received=%v want=%d", cg, chunkAck["declared_size"], chunkAck["received"], off)
		}
		a.ui.Progress(app.Progress{Label: "HTTP", Current: off, Total: total, Unit: "bytes", Detail: fmt.Sprintf("%d / %d", off, total)})
	}
	if off != total {
		return fmt.Errorf("upload ended at %d/%d", off, total)
	}
	a.ui.Progress(app.Progress{Label: "HTTP", Current: total, Total: total, Unit: "bytes", Done: true})
	a.status(L("ГОТОВО", "DONE"), fmt.Sprintf(L("%d байт приняты UrsusBoot в RAM; запуск/запись не выполнялись", "%d bytes accepted into UrsusBoot RAM; no boot/write was started"), total), app.LevelOK)
	return nil
}

func jsonNumberInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), x == float64(int64(x))
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

func (a *App) wsSaveDiagnostics(host string) error {
	dir := a.sessionScratch("ws-diagnostics")
	_ = os.MkdirAll(dir, 0o755)
	st, err := wsJSON(host, "GET", "/api/status", nil, nil, 8*time.Second)
	if err != nil {
		return err
	}
	if p, _ := st["product"].(string); p != "UrsusBoot" {
		return fmt.Errorf("unexpected product %q", p)
	}
	data, _ := json.MarshalIndent(st, "", "  ")
	statusPath := filepath.Join(dir, "status.json")
	if err := os.WriteFile(statusPath, append(data, '\n'), 0o644); err != nil {
		return err
	}

	req, _ := http.NewRequest("GET", "http://"+host+"/api/operation-log", nil)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			_ = os.WriteFile(filepath.Join(dir, "operation-log.txt"), b, 0o644)
		}
	}
	sha, _ := shaFile(statusPath)
	a.ui.Artifact(app.Artifact{
		Kind:   "ws-diagnostics",
		Label:  L("Диагностика UrsusBoot:", "UrsusBoot diagnostics:"),
		Path:   statusPath,
		SHA256: sha,
	})
	return nil
}

// wsPresetMenu intentionally contains only read-only commands. UBI attach is
// excluded because it may update UBI metadata/fastmap.
func (t *uartTerm) wsPresetMenu() {
	if _, ok := isUrsusWS(t.s); !ok {
		return
	}
	fmt.Print(L("\r\n[ПРЕСЕТЫ: 1 version · 2 bdinfo · 3 mtd list · 4 printenv · 5 mtd bad bl2 · 6 mtd bad ubi · 7 help · Enter назад] ",
		"\r\n[PRESETS: 1 version · 2 bdinfo · 3 mtd list · 4 printenv · 5 mtd bad bl2 · 6 mtd bad ubi · 7 help · Enter back] "))
	c := t.readByte()
	fmt.Print("\r\n")
	cmd := map[byte]string{
		'1': "version",
		'2': "bdinfo",
		'3': "mtd list",
		'4': "printenv",
		'5': "mtd bad bl2",
		'6': "mtd bad ubi",
		'7': "help",
	}[c]
	if cmd == "" {
		return
	}
	t.logSent("<PRESET " + cmd + ">")
	_ = t.s.Write([]byte(cmd + "\r"))
}

func (t *uartTerm) sendCtrlC() {
	t.logSent("<Ctrl-C>")
	if _, ok := isUrsusWS(t.s); ok && strings.HasPrefix(t.devLine, "UrsusBoot>") {
		now := time.Now()
		if t.ctrlCAt.IsZero() || now.Sub(t.ctrlCAt) > 2*time.Second {
			t.ctrlCAt = now
			fmt.Print(L("\r\n[Ctrl-C на пустом UrsusBoot> остановил бы WebFailsafe. НЕ отправлено. F10 — выход; ещё Ctrl-C за 2 с — отправить всё равно.]\r\n",
				"\r\n[Ctrl-C at idle UrsusBoot> would stop WebFailsafe. NOT sent. F10 quits; press Ctrl-C again within 2 s to send anyway.]\r\n"))
			return
		}
	}
	t.ctrlCAt = time.Time{}
	_ = t.s.Write([]byte{0x03})
}
