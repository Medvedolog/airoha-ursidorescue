package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ursidorescue/probe"
)

const (
	stockWebUser            = "CMCCAdmin"
	stockWebDefaultPassword = "aDm8H%MdA"
)

type stockWebClient struct {
	host   string
	base   string
	client *http.Client
	pub    *rsa.PublicKey
}

type stockCredentials struct {
	TelnetUser     string
	TelnetPassword string
	FTPEnabled     bool
	FTPUser        string
	FTPPassword    string
	FTPPort        int
}

func newStockWebClient(host string) (*stockWebClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		DisableKeepAlives: true,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: -1,
		}).DialContext,
	}
	return &stockWebClient{
		host: host,
		base: "http://" + host,
		client: &http.Client{
			Timeout:   8 * time.Second,
			Jar:       jar,
			Transport: tr,
		},
	}, nil
}

func (w *stockWebClient) request(path string, data []byte, ajax bool) (int, []byte, error) {
	method := http.MethodGet
	if data != nil {
		method = http.MethodPost
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		var body io.Reader
		if data != nil {
			body = bytes.NewReader(data)
		}
		req, err := http.NewRequest(method, w.base+path, body)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Connection", "close")
		if data != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", w.base)
		}
		if ajax {
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
		}
		resp, err := w.client.Do(req)
		if err != nil {
			last = err
			time.Sleep(time.Duration(attempt+1) * 350 * time.Millisecond)
			continue
		}
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if rerr != nil {
			last = rerr
			time.Sleep(time.Duration(attempt+1) * 350 * time.Millisecond)
			continue
		}
		return resp.StatusCode, b, nil
	}
	return 0, nil, fmt.Errorf("stock HTTP %s failed after retries: %w", method, last)
}

func stockEncodeURL(value string) (string, error) {
	const unsafe = "\"<>%\\^[]`+$,'#&"
	var b strings.Builder
	for _, r := range value {
		if r >= 255 {
			return "", errors.New("stock Web credential contains non-Latin-1 characters")
		}
		if strings.ContainsRune(unsafe, r) || r <= 32 || r >= 123 {
			fmt.Fprintf(&b, "%%%X", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}

func stockPKCS7(data []byte, block int) []byte {
	pad := block - len(data)%block
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func stockParsePublicKey(raw string) (*rsa.PublicKey, error) {
	raw = strings.ReplaceAll(raw, "\\r", "")
	raw = strings.ReplaceAll(raw, "\\n", "\n")
	if !strings.Contains(raw, "\n") && strings.Contains(raw, "\\") {
		raw = strings.ReplaceAll(raw, "\\", "\n")
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("stock Web RSA public key PEM not found")
	}
	if any, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if pub, ok := any.(*rsa.PublicKey); ok {
			return pub, nil
		}
	}
	if pub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return pub, nil
	}
	return nil, errors.New("stock Web RSA public key is not an RSA key")
}

func (w *stockWebClient) fetchPublicKey() error {
	status, body, err := w.request("/", nil, false)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("stock Web GET / returned HTTP %d", status)
	}
	re := regexp.MustCompile(`(?s)var\s+pubkey\s*=\s*'(.*?)'\s*;`)
	m := re.FindSubmatch(body)
	if len(m) != 2 {
		return errors.New("stock Web login page has no pubkey")
	}
	w.pub, err = stockParsePublicKey(string(m[1]))
	return err
}

func (w *stockWebClient) encryptForm(form string) ([]byte, error) {
	if w.pub == nil {
		if err := w.fetchPublicKey(); err != nil {
			return nil, err
		}
	}
	key := make([]byte, 16)
	iv := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := stockPKCS7([]byte(form), block.BlockSize())
	ct := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plain)
	aesInfo := base64.StdEncoding.EncodeToString(key) + " " + base64.StdEncoding.EncodeToString(iv)
	ck, err := rsa.EncryptPKCS1v15(rand.Reader, w.pub, []byte(aesInfo))
	if err != nil {
		return nil, err
	}
	ckText := base64.StdEncoding.EncodeToString(ck)
	ckText = strings.NewReplacer("+", "-", "/", "_", "=", ".").Replace(ckText)
	return []byte("encrypted=1&ct=" + base64.RawURLEncoding.EncodeToString(ct) + "&ck=" + ckText), nil
}

func (w *stockWebClient) hasSID() bool {
	u, err := url.Parse(w.base)
	if err != nil {
		return false
	}
	for _, c := range w.client.Jar.Cookies(u) {
		if c.Name == "sid" && c.Value != "" {
			return true
		}
	}
	return false
}

func (w *stockWebClient) login(user, password string) error {
	name, err := stockEncodeURL(user)
	if err != nil {
		return err
	}
	pass, err := stockEncodeURL(password)
	if err != nil {
		return err
	}
	form := "newMethodLogin=1&name=" + name + "&pswd=" + pass
	for attempt := 0; attempt < 2; attempt++ {
		w.pub = nil
		data, err := w.encryptForm(form)
		if err != nil {
			return err
		}
		status, _, err := w.request("/login.cgi", data, true)
		if err == nil && status == 299 && w.hasSID() {
			return nil
		}
		_, _, _ = w.request("/login.cgi?out", nil, false)
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("stock Web login was not accepted")
}

func (w *stockWebClient) logout() {
	_, _, _ = w.request("/login.cgi?out", nil, false)
}

func stockDecodeJSString(raw string) string {
	if len(raw) < 2 {
		return raw
	}
	body := raw[1 : len(raw)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case '\\', '\'', '"':
			out.WriteByte(body[i])
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'x':
			if i+2 < len(body) {
				if v, err := strconv.ParseUint(body[i+1:i+3], 16, 8); err == nil {
					out.WriteByte(byte(v))
					i += 2
				}
			}
		default:
			out.WriteByte(body[i])
		}
	}
	return out.String()
}

func stockJSField(text, key string) (string, bool) {
	re := regexp.MustCompile(`(?s)(?:["']?` + regexp.QuoteMeta(key) + `["']?)\s*:\s*('(?:\\.|[^'])*'|"(?:\\.|[^"])*"|[^,}\r\n]+)`)
	m := re.FindStringSubmatch(text)
	if len(m) != 2 {
		return "", false
	}
	v := strings.TrimSpace(m[1])
	if len(v) >= 2 && ((v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"')) {
		return stockDecodeJSString(v), true
	}
	return v, true
}

func stockBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes", "enable", "enabled":
		return true
	}
	return false
}

func (w *stockWebClient) getText(path string) (string, error) {
	status, body, err := w.request(path, nil, false)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("stock Web GET %s returned HTTP %d", path, status)
	}
	return string(body), nil
}

func (w *stockWebClient) deviceModel() (string, error) {
	text, err := w.getText("/device_status.cgi")
	if err != nil {
		return "", err
	}
	model, ok := stockJSField(text, "ModelName")
	if !ok || strings.TrimSpace(model) == "" {
		model, ok = stockJSField(text, "ProductClass")
	}
	if !ok || strings.TrimSpace(model) == "" {
		return "", errors.New("stock Web did not expose ModelName")
	}
	return strings.TrimSpace(model), nil
}

func (w *stockWebClient) credentials() (stockCredentials, error) {
	text, err := w.getText("/storage.cgi?ftp_config")
	if err != nil {
		return stockCredentials{}, err
	}
	get := func(k string) (string, error) {
		v, ok := stockJSField(text, k)
		if !ok {
			return "", fmt.Errorf("stock ftp_cfg has no %s", k)
		}
		return v, nil
	}
	tu, err := get("TelnetUserName")
	if err != nil {
		return stockCredentials{}, err
	}
	tp, err := get("TelnetPassword")
	if err != nil {
		return stockCredentials{}, err
	}
	fu, err := get("FtpUserName")
	if err != nil {
		return stockCredentials{}, err
	}
	fp, err := get("FtpPassword")
	if err != nil {
		return stockCredentials{}, err
	}
	fe, err := get("FtpEnable")
	if err != nil {
		return stockCredentials{}, err
	}
	fportText, err := get("FtpPort")
	if err != nil {
		return stockCredentials{}, err
	}
	fport, err := strconv.Atoi(strings.TrimSpace(fportText))
	if err != nil || fport < 1 || fport > 65535 {
		fport = 21
	}
	if strings.TrimSpace(tu) == "" || tp == "" {
		return stockCredentials{}, errors.New("stock Web returned empty Telnet credentials")
	}
	return stockCredentials{
		TelnetUser:     strings.TrimSpace(tu),
		TelnetPassword: tp,
		FTPEnabled:     stockBool(fe),
		FTPUser:        strings.TrimSpace(fu),
		FTPPassword:    fp,
		FTPPort:        fport,
	}, nil
}

func stockCSRF(text string) (string, bool) {
	for _, pattern := range []string{
		`csrf_token=([A-Za-z0-9_]+)`,
		`(?i)name=["']csrf_token["'][^>]*value=["']([A-Za-z0-9_]+)["']`,
	} {
		if m := regexp.MustCompile(pattern).FindStringSubmatch(text); len(m) == 2 {
			return m[1], true
		}
	}
	return "", false
}

func stockPortOpen(host string, port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func (w *stockWebClient) enableFTP() (bool, error) {
	text, err := w.getText("/storage.cgi?ftp_config")
	if err != nil {
		return false, err
	}
	enabledText, ok := stockJSField(text, "FtpEnable")
	if !ok {
		return false, errors.New("stock FTP page has no FtpEnable")
	}
	portText, _ := stockJSField(text, "FtpPort")
	port, _ := strconv.Atoi(strings.TrimSpace(portText))
	if port < 1 || port > 65535 {
		port = 21
	}
	if stockBool(enabledText) {
		for i := 0; i < 10; i++ {
			if stockPortOpen(w.host, port) {
				return false, nil
			}
			time.Sleep(time.Second)
		}
		return false, fmt.Errorf("stock FTP is enabled but TCP/%d did not open", port)
	}
	csrf, ok := stockCSRF(text)
	if !ok {
		return false, errors.New("stock FTP page has no csrf_token")
	}
	body := []byte("ftp_en=true&csrf_token=" + csrf)
	status, _, err := w.request("/storage.cgi?ftp_config", body, true)
	if err != nil {
		return false, err
	}
	if status != 200 && status != 299 {
		enc, e := w.encryptForm(string(body))
		if e != nil {
			return false, e
		}
		status, _, err = w.request("/storage.cgi?ftp_config", enc, true)
		if err != nil {
			return false, err
		}
		if status != 200 && status != 299 {
			return false, fmt.Errorf("stock FTP enable returned HTTP %d", status)
		}
	}
	for i := 0; i < 20; i++ {
		time.Sleep(time.Second)
		page, e := w.getText("/storage.cgi?ftp_config")
		if e != nil {
			continue
		}
		v, _ := stockJSField(page, "FtpEnable")
		pv, _ := stockJSField(page, "FtpPort")
		if p, e := strconv.Atoi(strings.TrimSpace(pv)); e == nil && p > 0 && p <= 65535 {
			port = p
		}
		if stockBool(v) && stockPortOpen(w.host, port) {
			return true, nil
		}
	}
	return true, errors.New("stock FTP setting was saved but the service did not become ready")
}

func stockWebPassword() string {
	if v := os.Getenv("URSIDO_STOCK_WEB_PASSWORD"); v != "" {
		return v
	}
	return stockWebDefaultPassword
}

func (a *App) stockLANLoginAssist(provision bool) (probe.LinuxLoginPlan, error) {
	deadline := time.Now().Add(90 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		w, err := newStockWebClient(defaultRouterIP)
		if err != nil {
			return probe.LinuxLoginPlan{}, err
		}
		if err = w.login(stockWebUser, stockWebPassword()); err != nil {
			last = err
			time.Sleep(2 * time.Second)
			continue
		}
		model, err := w.deviceModel()
		if err != nil {
			w.logout()
			last = err
			time.Sleep(2 * time.Second)
			continue
		}
		up := strings.ToUpper(model)
		if up != "XG-040G-MF" && up != "XG-040G-MD" {
			w.logout()
			return probe.LinuxLoginPlan{}, fmt.Errorf("stock Web model %q is not a supported XG-040G-MD/MF", model)
		}
		changed := false
		if provision {
			changed, err = w.enableFTP()
			if err != nil {
				w.logout()
				return probe.LinuxLoginPlan{}, err
			}
		}
		creds, err := w.credentials()
		w.logout()
		if err != nil {
			last = err
			time.Sleep(2 * time.Second)
			continue
		}
		if creds.FTPUser == "" || creds.FTPPassword == "" {
			last = errors.New("stock Web returned no usable FTP service credentials yet")
			time.Sleep(2 * time.Second)
			continue
		}
		return probe.LinuxLoginPlan{
			LoginUser:       creds.TelnetUser,
			LoginPassword:   creds.TelnetPassword,
			RootUser:        creds.FTPUser,
			RootPassword:    creds.FTPPassword,
			Model:           model,
			Source:          "stock-web " + defaultRouterIP,
			FTPEnabled:      creds.FTPEnabled || provision,
			SettingsChanged: changed,
		}, nil
	}
	if last == nil {
		last = errors.New("stock Web did not become ready")
	}
	return probe.LinuxLoginPlan{}, fmt.Errorf("stock LAN assist timed out: %w", last)
}
