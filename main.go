package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ursidorescue/app"
	"ursidorescue/probe"
)

const (
	appName               = "UrsidoRescue"
	appVersion            = "0.2.0-test17"
	defaultRouterIP       = "192.168.1.1"
	defaultLocalIP        = "192.168.1.254"
	defaultTFTPPort       = 1069
	loadAddr              = 0x90000000
	verifyAddr            = 0x98000000
	physicalNANDSize      = 0x10000000
	bl2Size               = 0x00020000
	ubiSize               = physicalNANDSize - bl2Size
	stockRestoreSpan      = 0x0EBA0000
	stockIBUSize          = stockRestoreSpan - bl2Size
	eraseSize             = 0x00020000
	minIOSize             = 0x00000800
	chunkSize             = 0x00800000
	stockBadSafePhysStart = 0x052C0000
	stockBadSafePhysEnd   = 0x0EB60000
	stockBadSafeUBIStart  = stockBadSafePhysStart - bl2Size
	stockBadSafeUBIEnd    = stockBadSafePhysEnd - bl2Size
	maxGenericRAMFile     = 0x08000000
)

var fipHeader = []byte{0x01, 0x00, 0x64, 0xaa, 0x78, 0x56, 0x34, 0x12}

type Profile struct {
	ID            string
	Model         string
	SoC           string
	PreloaderRel  string
	PreloaderSize int64
	PreloaderSHA  string
	RAMFIPRel     string
	RAMFIPSize    int64
	RAMFIPSHA     string
	Status        string
	StatusRU      string
}

var profiles = map[string]Profile{
	"md": {
		ID: "md", Model: "Nokia XG-040G-MD", SoC: "AN7581",
		PreloaderRel: "payloads/md/an7581-preloader.bin", PreloaderSize: 113447,
		PreloaderSHA: "6c3b2339d036340396730a13adfe35c0d2a4dddedeffb6f9965a24e0c7908808",
		RAMFIPRel:    "payloads/md/an7581-fudan-capable-ram.fip", RAMFIPSize: 314064,
		RAMFIPSHA: "f0323b30b7eaaa142fabc31769ce0be7f5f0d4306b40635ef3a4e3c0356dac14",
		Status:    "MD RAM FIP: UrsusBoot 0.1.0-alpha5-t66 RECOVERY_SAFE (Fudan FM25S01A + FM25G02B), RC18 contract; HW PENDING. UrsidoRescue implementation itself is LAB until first hardware cycle.",
		StatusRU:  "MD RAM FIP: UrsusBoot 0.1.0-alpha5-t66 RECOVERY_SAFE (Fudan FM25S01A + FM25G02B), контракт RC18; на железе не проверен. Сама реализация UrsidoRescue — LAB до первого цикла на железе.",
	},
	"mf": {
		ID: "mf", Model: "Nokia XG-040G-MF", SoC: "AN7583",
		PreloaderRel: "payloads/mf/an7583-preloader.bin", PreloaderSize: 118322,
		PreloaderSHA: "c2ac1c183b18bc34632c958dfe0bd1dfdfb607f090e39c41126956641893362f",
		RAMFIPRel:    "payloads/mf/an7583-recovery-ram.fip", RAMFIPSize: 324908,
		RAMFIPSHA: "7b564e83ac3b7f8d15dc42b3980197f2176ca2dfd5bc1703eff2b66938a1d782",
		Status:    "MF RAM FIP: UrsusBoot 0.1.0-alpha5-t66 RECOVERY_SAFE (Fudan FM25S01A + FM25G02B), RC18 contract; HW PENDING. UrsidoRescue integration is LAB until hardware validation.",
		StatusRU:  "MF RAM FIP: UrsusBoot 0.1.0-alpha5-t66 RECOVERY_SAFE (Fudan FM25S01A + FM25G02B), контракт RC18; на железе не проверен. Интеграция в UrsidoRescue — LAB до проверки на железе.",
	},
}

type App struct {
	root    string
	work    string
	lang    string
	reader  *bufio.Reader
	logMu   sync.Mutex
	logFile *os.File

	probeDir     string // current porting-probe session
	portOverride string // UART chosen on the command line

	// ui is what the core reports to and asks through (application layer,
	// doc/UI_SPEC_RU.md §6): the front end, wrapped by the current session.
	ui       app.UI
	front    app.UI
	frontEnd string

	sess      *app.Session // session of the running operation
	op        string       // its operation ID
	probeSess *app.Session // current Porting session (spans probe items)
}

func main() { os.Exit(realMain()) }

func realMain() int {
	args, langSet := langFromArgs(os.Args[1:])
	interactive := len(args) == 0
	if !langSet && !interactive {
		langFromLocale()
	}
	root, err := locateRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[FATAL]", err)
		return 2
	}
	enableVTOutput()
	a := &App{root: root, work: filepath.Join(root, "work"), reader: bufio.NewReader(os.Stdin), lang: uiLang}
	a.front = newConsoleUI(a.reader)
	a.ui = a.front
	a.frontEnd = "console"
	defer a.closeSessions()
	_ = os.MkdirAll(a.work, 0755)
	if !interactive {
		switch args[0] {
		case "--selftest":
			if err := a.selftest(); err != nil {
				fmt.Fprintln(os.Stderr, "SELFTEST FAIL:", err)
				return 1
			}
			fmt.Println("SELFTEST PASS")
			return 0
		case "--version":
			fmt.Printf("%s %s\n", appName, appVersion)
			return 0
		case "probe":
			return a.cliProbe(args[1:])
		case "export":
			return a.cliExport(args[1:])
		}
		fmt.Fprintln(os.Stderr, probeUsage())
		return 1
	}
	if !langSet {
		a.chooseLanguage()
	}
	a.lang = uiLang
	if err := a.run(); err != nil {
		fmt.Fprintln(os.Stderr, "\n[FAIL-CLOSED]", err)
		return 1
	}
	return 0
}

func locateRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	d := filepath.Dir(exe)
	cands := []string{d, filepath.Dir(d), filepath.Dir(filepath.Dir(d)), filepath.Dir(filepath.Dir(filepath.Dir(d)))}
	if wd, e := os.Getwd(); e == nil {
		cands = append(cands, wd)
	}
	for _, c := range cands {
		if fileExists(filepath.Join(c, "VERSION")) && dirExists(filepath.Join(c, "payloads")) {
			return filepath.Clean(c), nil
		}
	}
	return "", fmt.Errorf("cannot locate release root (VERSION + payloads) from %s", exe)
}
func fileExists(p string) bool { st, e := os.Stat(p); return e == nil && !st.IsDir() }
func dirExists(p string) bool  { st, e := os.Stat(p); return e == nil && st.IsDir() }

func (a *App) run() error {
	fmt.Printf("%s %s — Nokia Airoha UART Recovery\n", appName, appVersion)
	fmt.Println(L("ПУБЛИЧНАЯ ТЕСТОВАЯ UART-сборка. Нативный бинарник Windows/Linux, Python/pip/pyserial/TFTP-утилиты не нужны.", "PUBLIC UART TEST build. Windows/Linux native binary, no Python/pip/pyserial/TFTP utilities required."))
	fmt.Println(L("ТОЛЬКО ДЛЯ UART-ТЕСТЕРОВ: 3.3V TTL, только GND/TX/RX. VCC НИКОГДА не подключать.", "UART TESTERS ONLY: 3.3V TTL, GND/TX/RX only. NEVER connect VCC."))
	fmt.Println()
	for {
		fmt.Println(L("Главное меню", "Main menu"))
		fmt.Println(L("  1. Восстановить заводскую Nokia из mtd16/all_flash backup", "  1. Restore stock Nokia firmware from an mtd16/all_flash backup"))
		fmt.Println(L("  2. Починить загрузку OpenWrt / заменить FIP", "  2. Repair OpenWrt boot / replace the FIP"))
		fmt.Println(L("  3. Восстановить полный physical NAND image (256 MiB)", "  3. Restore a full physical NAND image (256 MiB)"))
		fmt.Println(L("  4. Загрузить OpenWrt recovery ITB в RAM", "  4. Boot an OpenWrt recovery ITB from RAM"))
		fmt.Println(L("  5. Диагностика NAND / UBI / U-Boot", "  5. NAND / UBI / U-Boot diagnostics"))
		fmt.Println(L("  6. Собрать пакет логов для отчёта", "  6. Build a log bundle for a report"))
		fmt.Println(L("  7. Портирование / исследование оборудования (read-only probe новых Airoha)", "  7. PORTING / HARDWARE DISCOVERY (read-only probe of new Airoha devices)"))
		fmt.Println(bold(L("  8. Экспертный режим", "  8. Expert mode")))
		fmt.Println(L("  0. Выход", "  0. Exit"))
		v := a.ask(L("Выбор: ", "Choice: "))
		switch v {
		case "1":
			if err := a.RunOperation("stock-restore"); err != nil {
				a.showErr(err)
			}
		case "2":
			if err := a.RunOperation("fip-repair"); err != nil {
				a.showErr(err)
			}
		case "3":
			if err := a.RunOperation("physical-restore"); err != nil {
				a.showErr(err)
			}
		case "4":
			if err := a.RunOperation("itb-boot"); err != nil {
				a.showErr(err)
			}
		case "5":
			if err := a.RunOperation("diagnostics"); err != nil {
				a.showErr(err)
			}
		case "6":
			if err := a.RunOperation("support-bundle"); err != nil {
				a.showErr(err)
			}
		case "7":
			if err := a.portingMenu(); err != nil {
				a.showErr(err)
			}
		case "8":
			if err := a.expertMenu(); err != nil {
				a.showErr(err)
			}
		case "0":
			return nil
		default:
			fmt.Println(L("Неверный выбор.", "Invalid choice."))
			fmt.Println()
		}
	}
}

// bold wraps text in the ANSI bold SGR; enableVTOutput makes it render on
// Windows too. Terminals that ignore SGR simply show the text unstyled.
func bold(s string) string { return paint(s, uiBold) }

func (a *App) showErr(err error) {
	fmt.Println()
	uiStatus(L("СТОП", "STOP"), err.Error(), uiBad)
	fmt.Println(paint(L("Никаких дополнительных write/erase команд после этой ошибки не отправлено.", "No further write/erase commands were sent after this error."), uiMuted))
	fmt.Println()
}
func (a *App) ask(prompt string) string {
	v, _ := a.ui.Ask(app.AskRequest{Kind: app.AskText, Prompt: prompt})
	return strings.TrimSpace(v)
}
func (a *App) askPath(prompt string) (string, error) {
	v, _ := a.ui.Ask(app.AskRequest{Kind: app.AskPath, Prompt: prompt})
	p := strings.Trim(strings.TrimSpace(v), "\"")
	if p == "" {
		return "", errors.New(L("пустой путь", "empty path"))
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	if !fileExists(abs) {
		return "", fmt.Errorf(L("файл не найден: %s", "file not found: %s"), abs)
	}
	return abs, nil
}

// confirm is the single confirmation of an operation with risk; its form
// follows the risk class (doc/UI_SPEC_RU.md §13).
func (a *App) confirm(risk app.Risk, phrase string) error {
	err := a.ui.Confirm(app.ConfirmRequest{Risk: risk, Phrase: phrase})
	if errors.Is(err, app.ErrCancelled) {
		return cancelledError{L("операция отменена пользователем", "operation cancelled by the user")}
	}
	return err
}

// note is plain operator guidance; status is a labelled line; event is a
// timestamped event. All go through the UI.
func (a *App) note(s string)      { a.ui.Event(app.Event{Level: app.LevelNote, Text: s}) }
func (a *App) noteln(args ...any) { a.note(strings.TrimSuffix(fmt.Sprintln(args...), "\n")) }
func (a *App) notef(format string, args ...any) {
	a.note(strings.TrimSuffix(fmt.Sprintf(format, args...), "\n"))
}
func (a *App) status(label, text string, l app.Level) {
	a.ui.Event(app.Event{Level: l, Label: label, Text: text})
}

func shaFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func crcFile(path string) (uint32, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, e
	}
	defer f.Close()
	h := crc32.NewIEEE()
	_, e = io.Copy(h, f)
	return h.Sum32(), e
}
func validatePinned(root string, p Profile) error {
	for _, x := range []struct {
		rel   string
		size  int64
		sha   string
		label string
	}{{p.PreloaderRel, p.PreloaderSize, p.PreloaderSHA, "preloader"}, {p.RAMFIPRel, p.RAMFIPSize, p.RAMFIPSHA, "RAM FIP"}} {
		path := filepath.Join(root, filepath.FromSlash(x.rel))
		st, e := os.Stat(path)
		if e != nil {
			return fmt.Errorf("%s missing: %w", x.label, e)
		}
		if st.Size() != x.size {
			return fmt.Errorf("%s size %d != pinned %d", x.label, st.Size(), x.size)
		}
		d, e := shaFile(path)
		if e != nil {
			return e
		}
		if !strings.EqualFold(d, x.sha) {
			return fmt.Errorf("%s SHA256 mismatch: %s", x.label, d)
		}
	}
	return nil
}
func validateFIP(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if st.Size() < 200000 || st.Size() > 2*1024*1024 {
		return fmt.Errorf("FIP size outside bounds: %d", st.Size())
	}
	b := make([]byte, len(fipHeader))
	if _, e = io.ReadFull(f, b); e != nil {
		return e
	}
	if !bytes.Equal(b, fipHeader) {
		return errors.New("not a TF-A FIP ToC header")
	}
	return nil
}
func validateFIT(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	var m uint32
	if e = binary.Read(f, binary.BigEndian, &m); e != nil {
		return e
	}
	if m != 0xd00dfeed {
		return fmt.Errorf("FIT magic mismatch: 0x%08x", m)
	}
	return nil
}

func (a *App) startLog(prefix string) (*os.File, error) {
	var p string
	if a.sess != nil {
		p = a.sess.UARTPath()
	} else {
		stamp := time.Now().Format("20060102-150405")
		p = filepath.Join(a.work, fmt.Sprintf("%s-%s.uart.log", prefix, stamp))
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if e != nil {
		return nil, e
	}
	a.logFile = f
	a.note(L("UART-лог: ", "UART log: ") + p)
	return f, nil
}
func (a *App) logBytes(b []byte, echo bool) {
	if len(b) == 0 {
		return
	}
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if a.logFile != nil {
		_, _ = a.logFile.Write(b)
		_ = a.logFile.Sync()
	}
	if echo {
		a.ui.Output(append([]byte(nil), b...))
	}
}
func (a *App) event(s string) {
	a.ui.Event(app.Event{Time: time.Now(), Level: app.LevelInfo, Text: s})
}

func chooseProfileInteractive(a *App) (Profile, error) {
	v, _ := a.ui.Ask(app.AskRequest{Kind: app.AskChoice, Title: L("Профиль устройства:", "Device profile:"),
		Choices: []app.Choice{
			{Key: "1", Label: L("Auto (определить по UART, если возможно)", "Auto (detect from UART if possible)")},
			{Key: "2", Label: "Nokia XG-040G-MD / AN7581"},
			{Key: "3", Label: "Nokia XG-040G-MF / AN7583"},
		},
		Prompt: L("Выбор [1]: ", "Choice [1]: ")})
	v = strings.TrimSpace(v)
	if v == "" || v == "1" {
		return Profile{ID: "auto"}, nil
	}
	if v == "2" {
		return profiles["md"], nil
	}
	if v == "3" {
		return profiles["mf"], nil
	}
	return Profile{}, errors.New(L("неверный профиль", "invalid profile"))
}

func (a *App) choosePort() (string, error) {
	if a.portOverride != "" {
		return a.portOverride, nil
	}
	ports := listSerialPorts()
	title := L("Найденные UART:", "UART ports found:")
	if len(ports) == 0 {
		title += "\n" + L("  (автообнаружение пусто; можно ввести порт вручную)", "  (none detected; you can type a port manually)")
	}
	var choices []app.Choice
	for i, p := range ports {
		choices = append(choices, app.Choice{Key: strconv.Itoa(i + 1), Label: p})
	}
	prompt := L("UART порт (номер пункта или имя): ", "UART port (list number or name): ")
	if len(ports) == 1 {
		prompt = L("UART порт [1]: ", "UART port [1]: ")
	}
	v, _ := a.ui.Ask(app.AskRequest{Kind: app.AskChoice, Title: title, Choices: choices, Prompt: prompt})
	v = strings.TrimSpace(v)
	if v == "" && len(ports) == 1 {
		return ports[0], nil
	}
	if n, e := strconv.Atoi(v); e == nil {
		if n >= 1 && n <= len(ports) {
			return ports[n-1], nil
		}
		if runtime.GOOS == "windows" && n >= 1 && n <= 256 {
			return fmt.Sprintf("COM%d", n), nil
		}
	}
	if runtime.GOOS == "linux" && strings.HasPrefix(v, "/dev/") {
		return v, nil
	}
	if runtime.GOOS == "windows" {
		u := strings.ToUpper(v)
		if matched, _ := regexp.MatchString(`^COM[0-9]+$`, u); matched {
			return u, nil
		}
	}
	return "", fmt.Errorf(L("неверный UART порт: %q", "invalid UART port: %q"), v)
}

func inferProfile(transcript []byte) (Profile, bool) {
	s := strings.ToLower(string(transcript))
	if strings.Contains(s, "an7581dramc") || strings.Contains(s, "airoha an7581") || strings.Contains(s, "cpu:   airoha an7581") {
		return profiles["md"], true
	}
	if strings.Contains(s, "an7583dramc") || strings.Contains(s, "airoha an7583") || strings.Contains(s, "cpu:   airoha an7583") {
		return profiles["mf"], true
	}
	return Profile{}, false
}

type ReceiverPhase int

const (
	phaseUnknown ReceiverPhase = iota
	phasePreloader
	phaseFIP
)

func (a *App) waitReceiver(s Serial, timeout time.Duration, keepExisting bool, initial []byte) (ReceiverPhase, Profile, []byte, error) {
	if !keepExisting && len(initial) == 0 {
		_ = s.ResetInput()
	}
	a.event(L("Ожидание BootROM Press x / CCC. Если устройство уже печатает C, НЕ перезагружайте его.", "Waiting for BootROM Press x / CCC. If the device already prints C, do NOT reboot it."))
	deadline := time.Now().Add(timeout)
	tail := make([]byte, 0, 65536)
	cCount := 0
	lastX := time.Time{}
	press := false
	buf := make([]byte, 4096)

	consume := func(d []byte, alreadyLogged bool) (ReceiverPhase, Profile, bool) {
		if len(d) == 0 {
			return phaseUnknown, Profile{}, false
		}
		if alreadyLogged {
			a.ui.Output(append([]byte(nil), d...))
		} else {
			a.logBytes(d, true)
		}
		tail = append(tail, d...)
		if len(tail) > 65536 {
			tail = tail[len(tail)-65536:]
		}
		low := strings.ToLower(string(tail))
		if strings.Contains(low, "press x") {
			press = true
		}
		if press && time.Since(lastX) > 2*time.Second && cCount == 0 {
			a.event(L("Press x обнаружен; отправляю x", "Press x seen; sending x"))
			_ = s.Write([]byte("x"))
			lastX = time.Now()
		}
		for _, b := range d {
			if b == 'C' {
				cCount++
				if cCount >= 3 {
					ph := phasePreloader
					if strings.Contains(low, "press x to load bl31") || strings.Contains(low, "dram flow done") || strings.Contains(low, "load bl31 + u-boot fip") {
						ph = phaseFIP
					}
					p, ok := inferProfile(tail)
					if !ok {
						p = Profile{ID: "auto"}
					}
					return ph, p, true
				}
			} else if b == 9 || b == 10 || b == 13 || b == 32 || b < 0x20 {
			} else {
				cCount = 0
			}
		}
		return phaseUnknown, Profile{}, false
	}

	if ph, p, ok := consume(initial, true); ok {
		return ph, p, tail, nil
	}
	for time.Now().Before(deadline) {
		n, e := s.Read(buf, 500*time.Millisecond)
		if e != nil {
			return phaseUnknown, Profile{}, tail, e
		}
		if n == 0 {
			continue
		}
		d := append([]byte(nil), buf[:n]...)
		if ph, p, ok := consume(d, false); ok {
			return ph, p, tail, nil
		}
	}
	return phaseUnknown, Profile{}, tail, errors.New(L("тайм-аут ожидания BootROM XMODEM", "timeout waiting for BootROM XMODEM"))
}

func crc16Xmodem(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

type xmodemReply int

const (
	xmodemReplyNone xmodemReply = iota
	xmodemReplyACK
	xmodemReplyRetry
	xmodemReplyCancel
)

type xmodemResult struct {
	EOTAck   bool
	Trailing []byte
}

func scanXmodemReply(data []byte, consecutiveCAN *int) xmodemReply {
	retry := false
	for _, b := range data {
		switch b {
		case 0x06: // ACK wins even if C/NAK noise preceded it in the same UART read.
			*consecutiveCAN = 0
			return xmodemReplyACK
		case 0x18:
			(*consecutiveCAN)++
			if *consecutiveCAN >= 2 {
				return xmodemReplyCancel
			}
		case 0x15, 'C':
			*consecutiveCAN = 0
			retry = true
		default:
			*consecutiveCAN = 0
		}
	}
	if retry {
		return xmodemReplyRetry
	}
	return xmodemReplyNone
}

func scanXmodemEOTReply(data []byte, consecutiveCAN *int) (xmodemReply, bool) {
	retry := false
	handoff := false
	for _, b := range data {
		switch b {
		case 0x06:
			*consecutiveCAN = 0
			return xmodemReplyACK, false
		case 0x18:
			(*consecutiveCAN)++
			if *consecutiveCAN >= 2 {
				return xmodemReplyCancel, false
			}
		case 0x15:
			*consecutiveCAN = 0
			retry = true
		default:
			*consecutiveCAN = 0
			// At EOT, ASCII 'C' is not a valid EOT response. It can be the
			// next XMODEM receiver or ordinary boot text ("NOTICE", etc.).
			// Any non-whitespace/non-NUL output means stop injecting EOT and
			// let the caller prove the next stage from UART.
			if b != 0 && b != '\r' && b != '\n' && b != '\t' && b != ' ' {
				handoff = true
			}
		}
	}
	if handoff {
		return xmodemReplyNone, true
	}
	if retry {
		return xmodemReplyRetry, false
	}
	return xmodemReplyNone, false
}

func (a *App) xmodemSend(s Serial, path, label string) (xmodemResult, error) {
	var result xmodemResult
	f, e := os.Open(path)
	if e != nil {
		return result, e
	}
	defer f.Close()
	st, _ := f.Stat()
	size := st.Size()
	blocks := (size + 127) / 128
	seq := byte(1)
	sent := int64(0)
	payload := make([]byte, 128)
	resp := make([]byte, 256)
	_ = s.ResetInput()

	dataComplete := false
	defer func() {
		if !dataComplete {
			// Only abort while still inside the data phase. Once the final
			// block was ACKed, the peer may already have jumped to the next
			// stage and CAN bytes would then be injected into that new stage.
			_ = s.Write([]byte{0x18, 0x18, 0x18})
		}
	}()

	for idx := int64(0); idx < blocks; idx++ {
		for i := range payload {
			payload[i] = 0x1a
		}
		n, er := f.Read(payload)
		if er != nil && er != io.EOF {
			return result, er
		}
		c := crc16Xmodem(payload)
		pkt := make([]byte, 0, 133)
		pkt = append(pkt, 0x01, seq, 0xff-seq)
		pkt = append(pkt, payload...)
		pkt = append(pkt, byte(c>>8), byte(c))

		accepted := false
		for attempt := 1; attempt <= 8 && !accepted; attempt++ {
			if e = s.Write(pkt); e != nil {
				return result, e
			}
			deadline := time.Now().Add(2 * time.Second)
			retryNow := false
			consecutiveCAN := 0
			for time.Now().Before(deadline) {
				nr, er := s.Read(resp, 150*time.Millisecond)
				if er != nil {
					return result, er
				}
				if nr == 0 {
					continue
				}
				a.logBytes(resp[:nr], false)
				switch scanXmodemReply(resp[:nr], &consecutiveCAN) {
				case xmodemReplyACK:
					accepted = true
				case xmodemReplyRetry:
					retryNow = true
				case xmodemReplyCancel:
					return result, fmt.Errorf(L("BootROM подтвердил отмену XMODEM %s (CAN CAN)", "BootROM confirmed XMODEM cancellation %s (CAN CAN)"), label)
				}
				if accepted || retryNow {
					break
				}
			}
			if !accepted {
				reason := L("тайм-аут ACK", "ACK timeout")
				if retryNow {
					reason = L("NAK/C — повтор немедленно", "NAK/C — immediate retry")
				}
				a.event(fmt.Sprintf(L("XMODEM повтор блока %d/%d, попытка %d/8 (%s)", "XMODEM retry block %d/%d attempt %d/8 (%s)"), idx+1, blocks, attempt, reason))
				time.Sleep(80 * time.Millisecond)
			}
		}
		if !accepted {
			return result, fmt.Errorf(L("XMODEM блок %d не подтверждён после 8 попыток", "XMODEM block %d not ACKed after 8 attempts"), idx+1)
		}
		sent += int64(n)
		seq++
		if idx == 0 || (idx+1)%32 == 0 || idx+1 == blocks {
			a.ui.Progress(app.Progress{Label: "XMODEM", Current: sent, Total: size, Unit: "bytes",
				Detail: fmt.Sprintf("%s: %d/%d bytes (%d/%d)", label, sent, size, idx+1, blocks)})
		}
	}
	dataComplete = true

	// EOT is a transport close, not stronger evidence than the next-stage
	// receiver/prompt. Retry EOT only when the receiver explicitly NAKs it.
	// Silence after a fully ACKed payload immediately hands control to the
	// next-stage parser, which must prove the second receiver or U-Boot prompt.
	for attempt := 1; attempt <= 3; attempt++ {
		if e = s.Write([]byte{0x04}); e != nil {
			return result, e
		}
		deadline := time.Now().Add(900 * time.Millisecond)
		retryNow := false
		consecutiveCAN := 0
		for time.Now().Before(deadline) {
			n, er := s.Read(resp, 120*time.Millisecond)
			if er != nil {
				return result, er
			}
			if n == 0 {
				continue
			}
			d := append([]byte(nil), resp[:n]...)
			a.logBytes(d, false)
			result.Trailing = append(result.Trailing, d...)
			if len(result.Trailing) > 65536 {
				result.Trailing = result.Trailing[len(result.Trailing)-65536:]
			}
			reply, handoff := scanXmodemEOTReply(d, &consecutiveCAN)
			switch reply {
			case xmodemReplyACK:
				a.ui.Progress(app.Progress{Label: "XMODEM", Done: true})
				a.event(L("XMODEM завершён: ", "XMODEM complete: ") + label)
				result.EOTAck = true
				return result, nil
			case xmodemReplyRetry:
				retryNow = true
			case xmodemReplyCancel:
				return result, fmt.Errorf(L("Receiver подтвердил отмену XMODEM %s на EOT (CAN CAN)", "Receiver confirmed XMODEM cancellation %s at EOT (CAN CAN)"), label)
			}
			if handoff {
				a.ui.Progress(app.Progress{Label: "XMODEM", Done: true})
				a.event(L("Payload XMODEM подтверждён; уже виден вывод следующего этапа — проверяю handoff", "XMODEM payload is ACKed; next-stage output is already visible — verifying handoff"))
				return result, nil
			}
			if retryNow {
				break
			}
		}
		if !retryNow {
			a.ui.Progress(app.Progress{Label: "XMODEM", Done: true})
			a.event(L("Payload XMODEM подтверждён; EOT ACK не пришёл — без лишних EOT перехожу к проверке следующего этапа", "XMODEM payload is ACKed; no EOT ACK — moving to next-stage proof without extra EOT retries"))
			return result, nil
		}
		a.event(fmt.Sprintf(L("XMODEM EOT получил NAK, повтор %d/3", "XMODEM EOT got NAK, retry %d/3"), attempt))
		time.Sleep(80 * time.Millisecond)
	}
	a.ui.Progress(app.Progress{Label: "XMODEM", Done: true})
	a.event(L("Payload XMODEM подтверждён; EOT трижды получил NAK — проверяю следующий этап по UART", "XMODEM payload is ACKed; EOT was NAKed three times — verifying the next UART stage"))
	return result, nil
}

var ansiCSIForPromptRE = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")

func promptPresent(b []byte) bool {
	// U-Boot bootmenu is screen-oriented: it may position the cursor with ANSI
	// instead of emitting a CR/LF before the command prompt. Looking only for a
	// line-start prompt therefore misses a real "AN7583> " after menu exit.
	if len(b) > 8192 {
		b = b[len(b)-8192:]
	}
	clean := ansiCSIForPromptRE.ReplaceAll(b, nil)
	clean = bytes.TrimRight(clean, " \t\r\n\x00")
	for _, suffix := range [][]byte{
		[]byte("AN7581>"),
		[]byte("AN7583>"),
		[]byte("U-Boot>"),
		[]byte("=>"),
	} {
		if bytes.HasSuffix(clean, suffix) {
			return true
		}
	}
	return false
}
func (a *App) waitQuiet(s Serial, quiet, timeout time.Duration) []byte {
	end := time.Now().Add(timeout)
	qd := time.Now().Add(quiet)
	var out []byte
	buf := make([]byte, 4096)
	for time.Now().Before(end) {
		n, e := s.Read(buf, 100*time.Millisecond)
		if e != nil {
			break
		}
		if n > 0 {
			d := append([]byte(nil), buf[:n]...)
			a.logBytes(d, true)
			out = append(out, d...)
			qd = time.Now().Add(quiet)
		} else if time.Now().After(qd) {
			break
		}
	}
	return out
}
func (a *App) waitUBootPrompt(s Serial, timeout time.Duration, initial []byte) ([]byte, error) {
	a.event(L("Ожидание RAM U-Boot; Enter не отправляется, чтобы не выбрать bootmenu", "Waiting for RAM U-Boot; Enter is not sent so no bootmenu entry gets selected"))
	end := time.Now().Add(timeout)
	var out, tail []byte
	buf := make([]byte, 4096)
	seen := false
	menu := false
	lastBreak := time.Time{}
	breaks := 0
	menuEscapes := 0

	consume := func(d []byte, alreadyLogged bool) (bool, error) {
		if len(d) == 0 {
			return false, nil
		}
		if alreadyLogged {
			a.ui.Output(append([]byte(nil), d...))
		} else {
			a.logBytes(d, true)
		}
		out = append(out, d...)
		tail = append(tail, d...)
		if len(tail) > 65536 {
			tail = tail[len(tail)-65536:]
		}
		low := strings.ToLower(string(tail))
		if promptPresent(tail) {
			a.waitQuiet(s, 450*time.Millisecond, 4*time.Second)
			_ = s.ResetInput()
			a.event(L("Устойчивый U-Boot prompt получен", "Stable U-Boot prompt reached"))
			return true, nil
		}
		if strings.Contains(low, "u-boot 20") || strings.Contains(low, "hit any key to stop autoboot") {
			seen = true
		}
		if strings.Contains(low, "press up/down to move") || strings.Contains(low, "run default boot command") {
			menu = true
			seen = true
		}
		if seen && time.Since(lastBreak) >= 250*time.Millisecond {
			if menu {
				if menuEscapes < 6 {
					_ = s.Write([]byte{0x1b})
					menuEscapes++
				}
			} else if breaks < 20 {
				_ = s.Write([]byte{0x03})
				breaks++
			}
			lastBreak = time.Now()
		}
		if strings.Contains(low, "mtd erase ubi") || strings.Contains(low, "erasing 0x") {
			return false, errors.New(L("до prompt замечена разрушительная автозагрузка", "destructive autoboot observed before prompt"))
		}
		if strings.Contains(low, "starting kernel") || strings.Contains(low, "booting linux on physical cpu") {
			return false, errors.New(L("автозагрузка ушла в Linux до prompt", "autoboot escaped into Linux before prompt"))
		}
		if strings.Contains(low, "press x to load bl31") && !seen {
			return false, errors.New(L("после FIP устройство вернулось в BootROM", "device returned to BootROM after FIP"))
		}
		return false, nil
	}

	if done, e := consume(initial, true); done || e != nil {
		return out, e
	}
	for time.Now().Before(end) {
		n, e := s.Read(buf, 120*time.Millisecond)
		if e != nil {
			return out, e
		}
		if n == 0 {
			continue
		}
		d := append([]byte(nil), buf[:n]...)
		if done, er := consume(d, false); done || er != nil {
			return out, er
		}
	}
	return out, errors.New(L("тайм-аут ожидания U-Boot prompt", "U-Boot prompt timeout"))
}

func sendLine(s Serial, line string) error {
	if strings.ContainsAny(line, "\r\n") || line == "" {
		return errors.New("invalid U-Boot command")
	}
	b := []byte(line)
	for len(b) > 0 {
		n := 16
		if len(b) < n {
			n = len(b)
		}
		if e := s.Write(b[:n]); e != nil {
			return e
		}
		b = b[n:]
		time.Sleep(3 * time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	return s.Write([]byte{'\r'})
}
func (a *App) readUntilPrompt(s Serial, timeout time.Duration, command string) ([]byte, error) {
	end := time.Now().Add(timeout)
	var out []byte
	buf := make([]byte, 4096)
	for time.Now().Before(end) {
		n, e := s.Read(buf, 200*time.Millisecond)
		if e != nil {
			return out, e
		}
		if n == 0 {
			continue
		}
		d := append([]byte(nil), buf[:n]...)
		a.logBytes(d, true)
		out = append(out, d...)
		if len(out) > 1024*1024 {
			out = out[len(out)-512*1024:]
		}
		low := strings.ToLower(string(out))
		if strings.Contains(low, "starting kernel") || strings.Contains(low, "booting linux on physical cpu") {
			return out, fmt.Errorf(L("команда неожиданно загрузила Linux: %s", "command booted Linux unexpectedly: %s"), command)
		}
		if promptPresent(out) {
			return out, nil
		}
	}
	return out, fmt.Errorf(L("тайм-аут команды U-Boot: %s", "U-Boot command timeout: %s"), command)
}
func (a *App) ubootCommandRaw(s Serial, command string, timeout time.Duration) (out []byte, rc int, err error) {
	start := time.Now()
	defer func() { a.recordCommand(command, start, rc, err) }()
	return a.ubootCommandExec(s, command, timeout)
}

// recordCommand writes one U-Boot command into the session's
// operations.jsonl (the source for a Command Inspector, UI_SPEC §32).
func (a *App) recordCommand(command string, start time.Time, rc int, err error) {
	if a.sess == nil {
		return
	}
	rec := map[string]any{"op": a.op, "event": "command", "transport": "uart", "stage": "uboot",
		"command": command, "rc": rc, "duration_ms": time.Since(start).Milliseconds()}
	if err != nil {
		rec["error"] = err.Error()
	}
	a.sess.Record(rec)
}

func (a *App) ubootCommandExec(s Serial, command string, timeout time.Duration) ([]byte, int, error) {
	marker := fmt.Sprintf("__URSIDO_%x__", time.Now().UnixNano())
	a.event("U-Boot: " + command)
	a.waitQuiet(s, 180*time.Millisecond, time.Second)
	_ = s.ResetInput()
	if e := sendLine(s, command); e != nil {
		return nil, -1, e
	}
	out, e := a.readUntilPrompt(s, timeout, command)
	if e != nil {
		return out, -1, e
	}
	a.waitQuiet(s, 100*time.Millisecond, 500*time.Millisecond)
	_ = s.ResetInput()
	if e = sendLine(s, "echo "+marker+"_RC_$?"); e != nil {
		return out, -1, e
	}
	status, e := a.readUntilPrompt(s, 10*time.Second, "status")
	if e != nil {
		return append(out, status...), -1, e
	}
	re := regexp.MustCompile(regexp.QuoteMeta(marker) + `_RC_([0-9]+)`)
	m := re.FindSubmatch(status)
	if len(m) != 2 {
		return append(out, status...), -1, fmt.Errorf("no return code marker for %s", command)
	}
	rc, _ := strconv.Atoi(string(m[1]))
	return append(out, status...), rc, nil
}
func (a *App) ubootCommand(s Serial, command string, timeout time.Duration) ([]byte, error) {
	out, rc, e := a.ubootCommandRaw(s, command, timeout)
	if e != nil {
		return out, e
	}
	if rc != 0 {
		return out, fmt.Errorf("U-Boot rc=%d: %s", rc, command)
	}
	return out, nil
}

func requireGeometry(data []byte) error {
	s := strings.ToLower(string(data))
	checks := []string{"spi-nand0", "block size: 0x20000 bytes", "min i/o: 0x800 bytes", "0x000000000000-0x000000020000 : \"bl2\"", "0x000000020000-0x000010000000 : \"ubi\""}
	for _, x := range checks {
		if !strings.Contains(s, x) {
			return fmt.Errorf(L("в геометрии MTD нет маркера %q", "MTD geometry missing marker %q"), x)
		}
	}
	return nil
}
func identifyVendor(data []byte) string {
	s := strings.ToLower(string(data))
	switch {
	case strings.Contains(s, "fudan micro") || strings.Contains(s, "fm25g01") || strings.Contains(s, "fm25g02"):
		return "Fudan Micro"
	case strings.Contains(s, "skyhigh") || strings.Contains(s, "ml02g300"):
		return "SkyHigh"
	default:
		return "unknown/inconclusive"
	}
}
func parseBadBlocks(data []byte, partSize uint64) ([]uint64, error) {
	var out []uint64
	seen := map[uint64]bool{}
	re := regexp.MustCompile(`(?m)^\s*0x([0-9a-fA-F]+)\s*$`)
	for _, m := range re.FindAllSubmatch(data, -1) {
		v, e := strconv.ParseUint(string(m[1]), 16, 64)
		if e != nil {
			return nil, e
		}
		if v >= partSize || v%eraseSize != 0 {
			return nil, fmt.Errorf("invalid bad-block offset 0x%x", v)
		}
		if seen[v] {
			return nil, fmt.Errorf("duplicate bad-block 0x%x", v)
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
func validateStockBadBlocks(xs []uint64) error {
	for _, x := range xs {
		if x >= stockIBUSize {
			continue
		}
		if x >= stockBadSafeUBIStart && x < stockBadSafeUBIEnd {
			continue
		}
		return fmt.Errorf(L("bad-блок в критичной области стока ubi+0x%08x; автоматическое восстановление заблокировано", "bad block in raw-critical stock region ubi+0x%08x; automatic stock restore blocked"), x)
	}
	return nil
}
func goodSpans(off, size uint64, bad []uint64) [][2]uint64 {
	end := off + size
	cur := off
	var spans [][2]uint64
	for _, b := range bad {
		if b < off || b >= end {
			continue
		}
		if b > cur {
			spans = append(spans, [2]uint64{cur, b - cur})
		}
		if b+eraseSize > cur {
			cur = b + eraseSize
		}
	}
	if cur < end {
		spans = append(spans, [2]uint64{cur, end - cur})
	}
	return spans
}

func (a *App) acquireRAMUBoot(preferred Profile) (Serial, Profile, []byte, error) {
	port, e := a.choosePort()
	if e != nil {
		return nil, Profile{}, nil, e
	}
	s, e := openSerial(port)
	if e != nil {
		return nil, Profile{}, nil, fmt.Errorf("open %s: %w", port, e)
	}
	log, e := a.startLog("recovery")
	if e != nil {
		s.Close()
		return nil, Profile{}, nil, e
	}
	_ = log
	a.noteln(L("\nЕсли сейчас уже видите 'Press x to load BL31 + U-Boot FIP' и CCC — ничего не перезагружайте.", "\nIf you already see 'Press x to load BL31 + U-Boot FIP' and CCC, do not reboot anything."))
	a.noteln(L("Иначе выключите Nokia, подключите UART, удерживайте Reset и подайте питание для BootROM recovery.", "Otherwise power the Nokia off, connect the UART, hold Reset and power it on for BootROM recovery."))
	phase, det, trans, e := a.waitReceiver(s, 180*time.Second, true, nil)
	if e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
	}
	p := preferred
	if det.ID != "auto" && det.ID != "" {
		if preferred.ID != "auto" && preferred.ID != "" && preferred.ID != det.ID {
			s.Close()
			a.closeLog()
			return nil, Profile{}, trans, fmt.Errorf(L("UART определил %s, но выбран профиль %s", "the UART shows %s, but profile %s was chosen"), det.SoC, preferred.SoC)
		}
		p = det
	}
	if p.ID == "auto" || p.ID == "" {
		a.noteln(L("SoC не удалось определить из текущего UART окна.", "The SoC could not be identified from the current UART output."))
		var er error
		p, er = chooseProfileInteractive(a)
		if er != nil {
			s.Close()
			a.closeLog()
			return nil, Profile{}, trans, er
		}
		if p.ID == "auto" {
			s.Close()
			a.closeLog()
			return nil, Profile{}, trans, errors.New(L("нужно выбрать MD или MF, если UART не содержит SoC marker", "choose MD or MF when the UART output has no SoC marker"))
		}
	}
	if e = validatePinned(a.root, p); e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
	}
	a.event(fmt.Sprintf(L("Профиль: %s / %s", "Profile: %s / %s"), p.Model, p.SoC))
	a.noteln("[INFO]", L(p.StatusRU, p.Status))
	pre := filepath.Join(a.root, filepath.FromSlash(p.PreloaderRel))
	fip := filepath.Join(a.root, filepath.FromSlash(p.RAMFIPRel))
	if phase == phasePreloader {
		a.event(L("Текущий receiver классифицирован как PRELOADER stage", "Current receiver classified as the PRELOADER stage"))
		preResult, xe := a.xmodemSend(s, pre, p.SoC+" preloader")
		if xe != nil {
			s.Close()
			a.closeLog()
			return nil, Profile{}, trans, xe
		}
		ph2, det2, t2, e2 := a.waitReceiver(s, 180*time.Second, true, preResult.Trailing)
		trans = append(trans, t2...)
		if e2 != nil {
			s.Close()
			a.closeLog()
			return nil, Profile{}, trans, e2
		}
		if ph2 != phaseFIP {
			a.event(L("Второй receiver не дал явного FIP marker; продолжаю только потому, что preloader stage только что завершён", "The second receiver gave no explicit FIP marker; continuing only because the preloader stage just completed"))
		}
		if det2.ID != "auto" && det2.ID != "" && det2.ID != p.ID {
			s.Close()
			a.closeLog()
			return nil, Profile{}, trans, errors.New(L("SoC сменился между этапами XMODEM", "SoC changed between XMODEM stages"))
		}
	}
	if phase == phaseFIP {
		a.event(L("Устройство уже находится на BL31+U-Boot FIP XMODEM stage; preloader повторно НЕ отправляется", "The device is already at the BL31+U-Boot FIP XMODEM stage; the preloader is NOT sent again"))
	}
	fipResult, xe := a.xmodemSend(s, fip, p.SoC+" RAM BL31+U-Boot FIP")
	if xe != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, xe
	}
	boot, e := a.waitUBootPrompt(s, 180*time.Second, fipResult.Trailing)
	trans = append(trans, boot...)
	if e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
	}
	if !fipResult.EOTAck {
		a.event(L("EOT ACK отсутствовал, но устойчивый RAM U-Boot prompt доказал успешный handoff", "EOT ACK was absent, but a stable RAM U-Boot prompt proved the handoff"))
	}
	ver, e := a.ubootCommand(s, "version", 30*time.Second)
	if e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
	}
	trans = append(trans, ver...)
	mtd, e := a.ubootCommand(s, "mtd list", 60*time.Second)
	if e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
	}
	trans = append(trans, mtd...)
	if e = requireGeometry(mtd); e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
	}
	a.event(L("Геометрия MTD PASS: NAND 256 MiB, erase 0x20000, min I/O 0x800, разметка bl2+ubi", "MTD geometry PASS: NAND 256 MiB, erase 0x20000, min I/O 0x800, bl2+ubi layout"))
	a.event(L("Производитель NAND (только информация): ", "NAND vendor (INFO only): ") + identifyVendor(trans))
	return s, p, trans, nil
}
func (a *App) closeLog() {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if a.logFile != nil {
		_ = a.logFile.Close()
		a.logFile = nil
	}
}

// ---------------- TFTP ----------------
type tftpResult struct {
	bytes     int64
	blockSize int
	err       error
}

func parseRRQ(p []byte) (op uint16, name, mode string, opts map[string]string, err error) {
	if len(p) < 4 {
		err = errors.New("short TFTP")
		return
	}
	op = binary.BigEndian.Uint16(p[:2])
	parts := bytes.Split(p[2:], []byte{0})
	if len(parts) < 3 {
		err = errors.New("malformed TFTP")
		return
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
func tftpCancelled(cancel <-chan struct{}) bool {
	if cancel == nil {
		return false
	}
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

func runTFTPServer(bindIP string, port int, source, expectedName, allowedHost string, cancel <-chan struct{}, ready chan error, done chan tftpResult) {
	res := tftpResult{}
	finish := func(err error) {
		res.err = err
		done <- res
	}
	addr, er := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", bindIP, port))
	if er != nil {
		ready <- er
		finish(er)
		return
	}
	c, er := net.ListenUDP("udp4", addr)
	if er != nil {
		ready <- er
		finish(er)
		return
	}
	defer c.Close()
	ready <- nil

	buf := make([]byte, 65535)
	requestDeadline := time.Now().Add(30 * time.Second)
	var peer *net.UDPAddr
	opts := map[string]string{}
	for time.Now().Before(requestDeadline) {
		if tftpCancelled(cancel) {
			finish(errors.New("TFTP cancelled"))
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		n, a, e := c.ReadFromUDP(buf)
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Timeout() {
				continue
			}
			if isExpectedUDPNoise(e) {
				continue
			}
			finish(e)
			return
		}
		if allowedHost != "" && a.IP.String() != allowedHost {
			continue
		}
		op, name, mode, o, e := parseRRQ(buf[:n])
		if e != nil || op != 1 || name != expectedName || mode != "octet" {
			continue
		}
		peer = a
		opts = o
		break
	}
	if peer == nil {
		finish(errors.New("TFTP RRQ timeout"))
		return
	}

	bs := 512
	if x := opts["blksize"]; x != "" {
		if n, e := strconv.Atoi(x); e == nil {
			bs = n
		}
	}
	if bs < 512 {
		bs = 512
	}
	if bs > 1468 {
		bs = 1468
	}
	res.blockSize = bs

	if _, ok := opts["blksize"]; ok {
		oack := append([]byte{0, 6}, []byte("blksize\x00"+strconv.Itoa(bs)+"\x00")...)
		negotiated := false
		for retry := 1; retry <= 8 && !negotiated; retry++ {
			if tftpCancelled(cancel) {
				finish(errors.New("TFTP cancelled"))
				return
			}
			_, _ = c.WriteToUDP(oack, peer)
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			n, a, e := c.ReadFromUDP(buf)
			if e != nil {
				if ne, ok := e.(net.Error); ok && ne.Timeout() {
					continue
				}
				if isExpectedUDPNoise(e) {
					continue
				}
				finish(e)
				return
			}
			if a.String() == peer.String() && n >= 4 && binary.BigEndian.Uint16(buf[:2]) == 4 && binary.BigEndian.Uint16(buf[2:4]) == 0 {
				negotiated = true
			}
		}
		if !negotiated {
			finish(errors.New("TFTP option negotiation timeout"))
			return
		}
	}

	f, e := os.Open(source)
	if e != nil {
		finish(e)
		return
	}
	defer f.Close()

	block := uint16(1)
	data := make([]byte, bs)
	for {
		n, e := f.Read(data)
		if e != nil && e != io.EOF {
			finish(e)
			return
		}
		pkt := make([]byte, 4+n)
		binary.BigEndian.PutUint16(pkt[:2], 3)
		binary.BigEndian.PutUint16(pkt[2:4], block)
		copy(pkt[4:], data[:n])

		acked := false
		for retry := 1; retry <= 10 && !acked; retry++ {
			if tftpCancelled(cancel) {
				finish(errors.New("TFTP cancelled"))
				return
			}
			_, _ = c.WriteToUDP(pkt, peer)
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			rn, ra, re := c.ReadFromUDP(buf)
			if re != nil {
				if ne, ok := re.(net.Error); ok && ne.Timeout() {
					continue
				}
				if isExpectedUDPNoise(re) {
					continue
				}
				finish(re)
				return
			}
			if ra.String() != peer.String() || rn < 4 {
				continue
			}
			op := binary.BigEndian.Uint16(buf[:2])
			ack := binary.BigEndian.Uint16(buf[2:4])
			if op == 5 {
				finish(errors.New("TFTP client ERROR"))
				return
			}
			if op == 4 && ack == block {
				acked = true
			}
		}
		if !acked {
			finish(fmt.Errorf("TFTP timeout block %d", block))
			return
		}
		res.bytes += int64(n)
		if n < bs {
			finish(nil)
			return
		}
		block++
	}
}

func detectLocalIP() string {
	// Prefer the interface selected by the OS route to the recovery router,
	// matching UrsusFlasher's local_ip_for() behavior.
	c, e := net.DialUDP("udp4", nil, mustUDPAddr(defaultRouterIP, 9))
	if e == nil {
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			ip := a.IP.To4()
			_ = c.Close()
			if ip != nil && ip[0] == 192 && ip[1] == 168 && ip[2] == 1 && ip[3] != 1 {
				return ip.String()
			}
		} else {
			_ = c.Close()
		}
	}

	// Fallback for hosts whose routing table is not yet settled during cable
	// bring-up: consider only active non-loopback 192.168.1.x interfaces.
	ifs, _ := net.Interfaces()
	for _, iface := range ifs {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip != nil && ip[0] == 192 && ip[1] == 168 && ip[2] == 1 && ip[3] != 1 {
				return ip.String()
			}
		}
	}
	return ""
}

func (a *App) networkIP() (string, error) {
	ip := detectLocalIP()
	if ip != "" {
		a.status("NET", L("адрес ПК: ", "PC address: ")+ip, app.LevelOK)
		return ip, nil
	}
	a.noteln(L("На ПК не найден IPv4 192.168.1.x. Настройте Ethernet статически, рекомендуется 192.168.1.254/24.", "No 192.168.1.x IPv4 address on this PC. Configure Ethernet statically, 192.168.1.254/24 recommended."))
	v := a.ask(L("Введите локальный IP после настройки [192.168.1.254]: ", "Enter the local IP after configuring it [192.168.1.254]: "))
	if v == "" {
		v = defaultLocalIP
	}
	ln, e := net.ListenUDP("udp4", mustUDPAddr(v, 0))
	if e != nil {
		return "", fmt.Errorf(L("адрес %s не принадлежит ПК/нельзя bind: %w", "address %s does not belong to this PC / cannot bind: %w"), v, e)
	}
	ln.Close()
	return v, nil
}
func mustUDPAddr(ip string, port int) *net.UDPAddr {
	a, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", ip, port))
	return a
}
func (a *App) resyncUBootAfterNetError(s Serial) error {
	transcript := make([]byte, 0, 8192)
	lastBreak := time.Time{}
	for attempt := 0; attempt < 4; attempt++ {
		if attempt == 0 || time.Since(lastBreak) >= 500*time.Millisecond {
			_ = s.Write([]byte{0x03})
			lastBreak = time.Now()
		}
		deadline := time.Now().Add(1500 * time.Millisecond)
		buf := make([]byte, 2048)
		for time.Now().Before(deadline) {
			n, e := s.Read(buf, 150*time.Millisecond)
			if e != nil {
				return e
			}
			if n == 0 {
				continue
			}
			a.logBytes(buf[:n], true)
			transcript = append(transcript, buf[:n]...)
			if len(transcript) > 8192 {
				transcript = transcript[len(transcript)-8192:]
			}
			if promptPresent(transcript) {
				a.waitQuiet(s, 120*time.Millisecond, 500*time.Millisecond)
				_ = s.ResetInput()
				return nil
			}
		}
	}
	return errors.New(L("не удалось восстановить U-Boot prompt после сетевой ошибки", "could not resynchronize U-Boot prompt after network error"))
}

func (a *App) tftpLoadKnownLocal(s Serial, path, remote string, addr uint64, local string, verify bool) error {
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	if st.Size() > maxGenericRAMFile {
		return fmt.Errorf(L("файл слишком велик для одной передачи в RAM: %d", "file too large for one-shot RAM transfer: %d"), st.Size())
	}

	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		ready := make(chan error, 1)
		done := make(chan tftpResult, 1)
		cancel := make(chan struct{})
		go runTFTPServer(local, defaultTFTPPort, path, remote, defaultRouterIP, cancel, ready, done)
		if e = <-ready; e != nil {
			last = e
			a.event(fmt.Sprintf(L("TFTP server не стартовал, попытка %d/3: %v", "TFTP server failed to start, attempt %d/3: %v"), attempt, e))
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		a.event(fmt.Sprintf(L("TFTP %s: попытка %d/3, %s -> 0x%x", "TFTP %s: attempt %d/3, %s -> 0x%x"), remote, attempt, local, addr))
		out, cmdErr := a.ubootCommand(s, fmt.Sprintf("tftpboot 0x%x %s", addr, remote), 2*time.Minute)
		if cmdErr != nil {
			close(cancel)
			res := <-done
			if res.err != nil && !strings.Contains(res.err.Error(), "cancelled") {
				last = fmt.Errorf("%v; TFTP server: %w", cmdErr, res.err)
			} else {
				last = cmdErr
			}
			a.event(fmt.Sprintf(L("Сетевая коллизия/сбой TFTP, повторяю только текущую передачу (%d/3): %v", "Network collision/TFTP failure; retrying only the current transfer (%d/3): %v"), attempt, last))
			if attempt < 3 {
				if re := a.resyncUBootAfterNetError(s); re != nil {
					return fmt.Errorf("%w; resync: %v", last, re)
				}
				if re := a.configureUBootNet(s, local); re != nil {
					return fmt.Errorf("%w; network reconfigure: %v", last, re)
				}
				a.event(L("TFTP retry: сетевой env U-Boot восстановлен после сбоя", "TFTP retry: U-Boot network environment restored after failure"))
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			break
		}

		res := <-done
		if res.err != nil {
			last = res.err
		} else if res.bytes != st.Size() {
			last = fmt.Errorf("TFTP bytes %d != %d", res.bytes, st.Size())
		} else {
			low := strings.ToLower(string(out))
			if !strings.Contains(low, "bytes transferred") && !strings.Contains(low, "done") {
				last = errors.New(L("U-Boot не подтвердил TFTP", "U-Boot did not confirm TFTP"))
			} else if verify {
				last = a.verifyRAM(s, path, addr)
			} else {
				last = nil
			}
		}
		if last == nil {
			a.event(fmt.Sprintf(L("TFTP PASS: %s, %d bytes, block=%d", "TFTP PASS: %s, %d bytes, block=%d"), remote, res.bytes, res.blockSize))
			return nil
		}

		a.event(fmt.Sprintf(L("TFTP проверка не прошла; повторяю только текущую передачу (%d/3): %v", "TFTP verification failed; retrying only the current transfer (%d/3): %v"), attempt, last))
		if attempt < 3 {
			// The TFTP command already returned to a working prompt. Do not
			// churn ethaddr/ipaddr/serverip for a RAM verification retry.
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	return fmt.Errorf(L("TFTP не прошёл после 3 попыток: %w", "TFTP failed after 3 attempts: %w"), last)
}

func (a *App) tftpLoad(s Serial, path, remote string, addr uint64) (string, error) {
	local, e := a.networkIP()
	if e != nil {
		return "", e
	}
	if e = a.configureUBootNet(s, local); e != nil {
		return "", e
	}
	// Actual transfer + RAM hash/CRC is the network preflight. Do not fail the
	// operation merely because an ICMP ping was lost during link/ARP convergence.
	e = a.tftpLoadKnownLocal(s, path, remote, addr, local, true)
	return local, e
}

func (a *App) loadChunkWithKnownLocal(s Serial, path, remote, local string) error {
	return a.tftpLoadKnownLocal(s, path, remote, loadAddr, local, true)
}

func (a *App) verifyRAM(s Serial, path string, addr uint64) error {
	st, _ := os.Stat(path)
	sha, _ := shaFile(path)
	if out, rc, e := a.ubootCommandRaw(s, fmt.Sprintf("hash sha256 0x%x 0x%x", addr, st.Size()), 60*time.Second); e == nil && rc == 0 && strings.Contains(strings.ToLower(string(out)), strings.ToLower(sha)) {
		a.event(L("SHA256 в RAM PASS: ", "RAM SHA256 PASS: ") + sha)
		return nil
	}
	crc, e := crcFile(path)
	if e != nil {
		return e
	}
	out, e := a.ubootCommand(s, fmt.Sprintf("crc32 0x%x 0x%x", addr, st.Size()), 60*time.Second)
	if e != nil {
		return e
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, crc)).Match(out) {
		return fmt.Errorf(L("CRC32 в RAM не совпал; ожидался %08x", "RAM CRC32 mismatch; expected %08x"), crc)
	}
	a.event(fmt.Sprintf(L("CRC32 в RAM PASS: %08x (SHA256 источника %s)", "RAM CRC32 PASS: %08x (SHA256 source %s)"), crc, sha))
	return nil
}

func (a *App) readbackCRC(s Serial, target string, off, size, ram uint64, expected uint32) error {
	if _, e := a.ubootCommand(s, fmt.Sprintf("mw.b 0x%x 0x00 0x%x", ram, size), 2*time.Minute); e != nil {
		return e
	}
	if _, e := a.ubootCommand(s, fmt.Sprintf("mtd read %s 0x%x 0x%x 0x%x", target, ram, off, size), 10*time.Minute); e != nil {
		return e
	}
	out, e := a.ubootCommand(s, fmt.Sprintf("crc32 0x%x 0x%x", ram, size), 2*time.Minute)
	if e != nil {
		return e
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, expected)).Match(out) {
		return fmt.Errorf(L("CRC после записи не совпал target=%s off=0x%x ожидался=%08x", "readback CRC mismatch target=%s off=0x%x expected=%08x"), target, off, expected)
	}
	return nil
}

// ---------------- Workflows ----------------
func (a *App) fipRepairWizard() error {
	a.showNetworkPrerequisites()
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, p, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()
	if _, e = a.ubootCommand(s, "ubi part ubi", 3*time.Minute); e != nil {
		return fmt.Errorf(L("не удалось подключить существующий UBI; ремонт только FIP неприменим: %w", "existing UBI attach failed; FIP-only repair not applicable: %w"), e)
	}
	layout, e := a.ubootCommand(s, "ubi info layout", 60*time.Second)
	if e != nil {
		return e
	}
	if !strings.Contains(strings.ToLower(string(layout)), "fip") {
		return errors.New(L("UBI volume fip не найден", "UBI volume fip not found"))
	}
	a.noteln(L("\nFIP источник:", "\nFIP source:"))
	a.noteln(L("  1. Встроенный RAM FIP текущего профиля (рекомендуется для rescue)", "  1. Built-in RAM FIP of the current profile (recommended for rescue)"))
	a.noteln(L("  2. Выбрать другой .fip", "  2. Choose another .fip"))
	v := a.ask(L("Выбор [1]: ", "Choice [1]: "))
	path := filepath.Join(a.root, filepath.FromSlash(p.RAMFIPRel))
	if v == "2" {
		path, e = a.askPath(L("Путь к .fip: ", "Path to the .fip: "))
		if e != nil {
			return e
		}
	}
	if e = validateFIP(path); e != nil {
		return e
	}
	sha, e := shaFile(path)
	if e != nil {
		return e
	}
	st, _ := os.Stat(path)
	a.notef("FIP: %s\nsize=%d\nSHA256=%s\n", path, st.Size(), sha)
	if _, e = a.tftpLoad(s, path, "ursido-fip.bin", loadAddr); e != nil {
		return e
	}
	a.noteln(L("\nВсе read-only gates пройдены. Будет перезаписан ТОЛЬКО существующий UBI volume fip.", "\nAll read-only gates passed. ONLY the existing UBI volume fip will be overwritten."))
	if e = a.confirm(app.Write, "WRITE FIP"); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("ubi write 0x%x fip 0x%x", loadAddr, st.Size()), 5*time.Minute); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("ubi read 0x%x fip 0x%x", verifyAddr, st.Size()), 5*time.Minute); e != nil {
		return e
	}
	crc, _ := crcFile(path)
	out, e := a.ubootCommand(s, fmt.Sprintf("crc32 0x%x 0x%x", verifyAddr, st.Size()), 60*time.Second)
	if e != nil {
		return e
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, crc)).Match(out) {
		return errors.New(L("CRC32 FIP после записи не совпал", "FIP readback CRC32 mismatch"))
	}
	a.event(L("Запись FIP + проверка PASS; SHA256 источника=", "FIP write + readback PASS; source SHA256=") + sha)
	a.noteln(L("Можно выполнить reset. Нажмите Enter для reset или введите N чтобы оставить RAM U-Boot.", "Ready to reset. Press Enter to reset or type N to stay in RAM U-Boot."))
	if strings.ToLower(a.ask("> ")) != "n" {
		_ = sendLine(s, "reset")
	}
	return nil
}

func openMaybeGzip(path string) (io.ReadCloser, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	head := make([]byte, 2)
	n, _ := f.Read(head)
	_, _ = f.Seek(0, 0)
	if n == 2 && head[0] == 0x1f && head[1] == 0x8b {
		g, e := gzip.NewReader(f)
		if e != nil {
			f.Close()
			return nil, e
		}
		return &multiCloser{Reader: g, closers: []io.Closer{g, f}}, nil
	}
	return f, nil
}

type multiCloser struct {
	io.Reader
	closers []io.Closer
}

func (m *multiCloser) Close() error {
	for _, c := range m.closers {
		_ = c.Close()
	}
	return nil
}

type stockPrepared struct {
	dir    string
	bl2    string
	chunks []string
	allSHA string
	bl2SHA string
}

func rejectKnownOpenWrtBL2(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) != bl2Size {
		return fmt.Errorf("BL2 size %d != 0x%x", len(data), bl2Size)
	}
	type sig struct {
		size       int
		sha, label string
	}
	sigs := []sig{
		{113447, "6c3b2339d036340396730a13adfe35c0d2a4dddedeffb6f9965a24e0c7908808", "AN7581 OpenWrt preloader (MedveFlasher lineage)"},
		{112162, "b912be8138e260979eb12768907f7b9a53819ffa392ed8f3ccebe5bd19c291c9", "AN7581 OpenWrt preloader (later snapshot candidate)"},
		{118322, "c2ac1c183b18bc34632c958dfe0bd1dfdfb607f090e39c41126956641893362f", "AN7583 OpenWrt preloader"},
	}
	allFF := true
	for _, b := range data[:0x800] {
		if b != 0xff {
			allFF = false
			break
		}
	}
	for _, sg := range sigs {
		if sg.size <= len(data) {
			h := sha256.Sum256(data[:sg.size])
			if hex.EncodeToString(h[:]) == sg.sha {
				return fmt.Errorf(L("BL2 в бэкапе начинается с %s; это не похоже на оригинальный стоковый mtd16", "backup BL2 begins with %s; this does not look like original stock mtd16"), sg.label)
			}
		}
		if allFF && 0x800+sg.size <= len(data) {
			h := sha256.Sum256(data[0x800 : 0x800+sg.size])
			if hex.EncodeToString(h[:]) == sg.sha {
				return fmt.Errorf(L("BL2 в бэкапе содержит %s со сдвигом FF+0x800; это контейнер OpenWrt BL2, а не оригинальный стоковый BL2", "backup BL2 contains FF+0x800 shifted %s; this is an OpenWrt BL2 container, not original stock BL2"), sg.label)
			}
		}
	}
	return nil
}

func (a *App) prepareStock(path string) (stockPrepared, error) {
	r, e := openMaybeGzip(path)
	if e != nil {
		return stockPrepared{}, e
	}
	defer r.Close()
	dir := a.sessionScratch("stock-" + time.Now().Format("20060102-150405"))
	if e = os.MkdirAll(dir, 0755); e != nil {
		return stockPrepared{}, e
	}
	bl2p := filepath.Join(dir, "stock-bl2.bin")
	bl2f, e := os.Create(bl2p)
	if e != nil {
		return stockPrepared{}, e
	}
	hAll := sha256.New()
	hBL := sha256.New()
	total := int64(0)
	chunkIndex := 0
	var chunks []string
	var cf *os.File
	var chunkRemain int64
	buf := make([]byte, 1024*1024)
	for {
		n, er := r.Read(buf)
		if n > 0 {
			d := buf[:n]
			_, _ = hAll.Write(d)
			pos := 0
			for pos < n {
				if total < bl2Size {
					take := n - pos
					if int64(take) > bl2Size-total {
						take = int(bl2Size - total)
					}
					part := d[pos : pos+take]
					_, _ = bl2f.Write(part)
					_, _ = hBL.Write(part)
					pos += take
					total += int64(take)
					continue
				}
				if total >= stockRestoreSpan {
					return stockPrepared{}, errors.New(L("бэкап больше канонического размера mtd16", "backup larger than canonical mtd16 span"))
				}
				if cf == nil || chunkRemain == 0 {
					if cf != nil {
						cf.Close()
					}
					name := filepath.Join(dir, fmt.Sprintf("stock-ibu-%02d.bin", chunkIndex))
					cf, e = os.Create(name)
					if e != nil {
						return stockPrepared{}, e
					}
					chunks = append(chunks, name)
					chunkIndex++
					remain := stockRestoreSpan - total
					chunkRemain = chunkSize
					if remain < chunkRemain {
						chunkRemain = remain
					}
				}
				take := n - pos
				if int64(take) > chunkRemain {
					take = int(chunkRemain)
				}
				part := d[pos : pos+take]
				_, _ = cf.Write(part)
				pos += take
				total += int64(take)
				chunkRemain -= int64(take)
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return stockPrepared{}, er
		}
	}
	bl2f.Close()
	if cf != nil {
		cf.Close()
	}
	if total != stockRestoreSpan {
		return stockPrepared{}, fmt.Errorf(L("размер mtd16 без сжатия %d, ожидался %d", "mtd16 uncompressed size %d, expected %d"), total, stockRestoreSpan)
	}
	if err := rejectKnownOpenWrtBL2(bl2p); err != nil {
		return stockPrepared{}, err
	}
	return stockPrepared{dir: dir, bl2: bl2p, chunks: chunks, allSHA: hex.EncodeToString(hAll.Sum(nil)), bl2SHA: hex.EncodeToString(hBL.Sum(nil))}, nil
}

func (a *App) configureUBootNet(s Serial, local string) error {
	for _, cmd := range []string{"setenv ethaddr 02:00:00:04:0d:10", "setenv eth1addr 02:00:00:04:0d:11", "setenv ipaddr " + defaultRouterIP, "setenv serverip " + local, "setenv netmask 255.255.255.0", fmt.Sprintf("setenv tftpdstp %d", defaultTFTPPort), "setenv autoload no"} {
		if _, e := a.ubootCommand(s, cmd, 20*time.Second); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) mtdBad(s Serial, part string, size uint64) ([]uint64, error) {
	out, e := a.ubootCommand(s, "mtd bad "+part, 3*time.Minute)
	if e != nil {
		return nil, e
	}
	return parseBadBlocks(out, size)
}
func crcRange(path string, off, size int64) (uint32, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, e
	}
	defer f.Close()
	if _, e = f.Seek(off, 0); e != nil {
		return 0, e
	}
	h := crc32.NewIEEE()
	_, e = io.CopyN(h, f, size)
	return h.Sum32(), e
}
func findMTD16InDirectory(dir string) (string, error) {
	patterns := []string{"mtd16.bin.gz", "mtd16_*.bin.gz", "mtd16.gz", "mtd16.bin", "mtd16_*.bin"}
	var hits []string
	for _, pat := range patterns {
		xs, _ := filepath.Glob(filepath.Join(dir, pat))
		hits = append(hits, xs...)
	}
	if len(hits) == 0 {
		return "", errors.New(L("в каталоге не найден mtd16.bin(.gz) / mtd16_*.bin(.gz)", "no mtd16.bin(.gz) / mtd16_*.bin(.gz) in the directory"))
	}
	sort.Strings(hits)
	return hits[0], nil
}
func (a *App) verifyManifestEntry(dir, selected string) error {
	for _, name := range []string{"SHA256SUMS", "SHA256SUMS.txt"} {
		mp := filepath.Join(dir, name)
		if !fileExists(mp) {
			continue
		}
		f, err := os.Open(mp)
		if err != nil {
			return err
		}
		defer f.Close()
		scan := bufio.NewScanner(f)
		base := filepath.Base(selected)
		found := false
		for scan.Scan() {
			fields := strings.Fields(scan.Text())
			if len(fields) < 2 || len(fields[0]) != 64 {
				continue
			}
			n := strings.TrimPrefix(fields[1], "*")
			if filepath.Clean(n) == filepath.Clean(base) || filepath.Base(n) == base {
				found = true
				d, err := shaFile(selected)
				if err != nil {
					return err
				}
				if !strings.EqualFold(d, fields[0]) {
					return fmt.Errorf(L("SHA256SUMS не совпал для %s", "SHA256SUMS mismatch for %s"), base)
				}
				break
			}
		}
		if err := scan.Err(); err != nil {
			return err
		}
		if found {
			a.noteln(L("[OK] SHA256SUMS проверен:", "[OK] SHA256SUMS verified:"), base)
		} else {
			a.noteln(L("[INFO] SHA256SUMS есть, но для выбранного mtd16 нет записи; проверки размера/потока/BL2 остаются", "[INFO] SHA256SUMS exists but selected mtd16 has no direct entry; size/stream/BL2 checks remain active"))
		}
		return nil
	}
	a.noteln(L("[INFO] SHA256SUMS не найден; mtd16 будет проверен полным чтением, точным размером и происхождением BL2", "[INFO] SHA256SUMS not found; mtd16 will be checked by full stream, exact size and BL2 provenance"))
	return nil
}
func (a *App) askStockSource() (string, error) {
	p := strings.Trim(strings.TrimSpace(a.ask(L("Путь к MedveFlasher backup-каталогу ИЛИ canonical mtd16/all_flash файлу: ", "Path to a MedveFlasher backup directory OR a canonical mtd16/all_flash file: "))), "\"")
	if p == "" {
		return "", errors.New(L("пустой путь", "empty path"))
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	st, e := os.Stat(abs)
	if e != nil {
		return "", e
	}
	if !st.IsDir() {
		return abs, nil
	}
	m, e := findMTD16InDirectory(abs)
	if e != nil {
		return "", e
	}
	present := 0
	for i := 0; i <= 16; i++ {
		patterns := []string{fmt.Sprintf("mtd%d.bin.gz", i), fmt.Sprintf("mtd%d_*.bin.gz", i), fmt.Sprintf("mtd%d.gz", i), fmt.Sprintf("mtd%d.bin", i), fmt.Sprintf("mtd%d_*.bin", i)}
		ok := false
		for _, pat := range patterns {
			xs, _ := filepath.Glob(filepath.Join(abs, pat))
			if len(xs) > 0 {
				ok = true
				break
			}
		}
		if ok {
			present++
		}
	}
	a.notef(L("[INFO] Найдены файлы бэкапа MedveFlasher: mtd0..mtd16, есть %d/17; источник восстановления=%s\n", "[INFO] MedveFlasher backup files found: mtd0..mtd16 present count=%d/17; restore source=%s\n"), present, filepath.Base(m))
	if e = a.verifyManifestEntry(abs, m); e != nil {
		return "", e
	}
	return m, nil
}

func (a *App) stockRestoreWizard() error {
	a.showNetworkPrerequisites()
	path, e := a.askStockSource()
	if e != nil {
		return e
	}
	a.noteln(L("Проверяю и раскладываю mtd16 на BL2 + IBU chunks без записи NAND...", "Checking mtd16 and splitting it into BL2 + IBU chunks without writing NAND..."))
	prep, e := a.prepareStock(path)
	if e != nil {
		return e
	}
	a.notef("mtd16 SHA256: %s\nBL2 SHA256: %s\nchunks: %d\n", prep.allSHA, prep.bl2SHA, len(prep.chunks))
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, _, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()
	blbad, e := a.mtdBad(s, "bl2", bl2Size)
	if e != nil {
		return e
	}
	if len(blbad) > 0 {
		return errors.New(L("eraseblock BL2 помечен bad; автоматическое восстановление стока заблокировано", "BL2 eraseblock is marked bad; automatic stock restore blocked"))
	}
	bad, e := a.mtdBad(s, "ubi", ubiSize)
	if e != nil {
		return e
	}
	if e = validateStockBadBlocks(bad); e != nil {
		return e
	}
	a.notef(L("[INFO] bad-блоков в ubi: %d\n", "[INFO] bad blocks in ubi: %d\n"), len(bad))
	local, e := a.networkIP()
	if e != nil {
		return e
	}
	if e = a.configureUBootNet(s, local); e != nil {
		return e
	}
	if e = a.loadChunkWithKnownLocal(s, prep.chunks[0], "stock-00.bin", local); e != nil {
		return fmt.Errorf(L("тест TFTP перед записью не прошёл: %w", "pre-write TFTP test failed: %w"), e)
	}
	a.noteln(L("\nВНИМАНИЕ: будет полностью очищен OpenWrt UBI region, затем восстановлен stock mtd16; BL2 пишется ПОСЛЕДНИМ.", "\nWARNING: the OpenWrt UBI region will be erased completely, then stock mtd16 restored; BL2 is written LAST."))
	if e = a.confirm(app.Erase, "RESTORE STOCK BACKUP"); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, "mtd erase ubi", 20*time.Minute); e != nil {
		return e
	}
	bad2, e := a.mtdBad(s, "ubi", ubiSize)
	if e != nil {
		return e
	}
	if e = validateStockBadBlocks(bad2); e != nil {
		return e
	}
	for _, old := range bad {
		found := false
		for _, n := range bad2 {
			if n == old {
				found = true
				break
			}
		}
		if !found {
			return errors.New(L("после erase из карты bad-блоков пропал существующий bad-блок", "bad-block map lost an existing bad block after erase"))
		}
	}
	for i, ch := range prep.chunks {
		if i > 0 {
			if e = a.loadChunkWithKnownLocal(s, ch, fmt.Sprintf("stock-%02d.bin", i), local); e != nil {
				return e
			}
		}
		st, _ := os.Stat(ch)
		chunkOff := uint64(i) * chunkSize
		spans := goodSpans(chunkOff, uint64(st.Size()), bad2)
		if len(spans) == 0 {
			return fmt.Errorf(L("часть %d полностью в bad-блоках", "chunk %d entirely bad"), i)
		}
		for si, sp := range spans {
			localOff := int64(sp[0] - chunkOff)
			ram := uint64(loadAddr) + uint64(localOff)
			expected, e := crcRange(ch, localOff, int64(sp[1]))
			if e != nil {
				return e
			}
			cmd := fmt.Sprintf("mtd write ubi 0x%x 0x%x 0x%x", ram, sp[0], sp[1])
			out, e := a.ubootCommand(s, cmd, 10*time.Minute)
			if e != nil {
				return e
			}
			low := strings.ToLower(string(out))
			if strings.Contains(low, "skipping bad block") || strings.Contains(low, "new bad block") {
				return errors.New(L("во время записи появился новый bad-блок; BL2 не тронут", "new bad block appeared during write; BL2 remains untouched"))
			}
			if e = a.readbackCRC(s, "ubi", sp[0], sp[1], ram, expected); e != nil {
				return fmt.Errorf(L("IBU-часть %d, участок %d: %w", "IBU chunk %d span %d: %w"), i, si, e)
			}
		}
		a.event(fmt.Sprintf(L("IBU-часть %d/%d проверка PASS", "IBU chunk %d/%d readback PASS"), i+1, len(prep.chunks)))
	}
	bad3, e := a.mtdBad(s, "ubi", ubiSize)
	if e != nil {
		return e
	}
	if fmt.Sprint(bad3) != fmt.Sprint(bad2) {
		return errors.New(L("карта bad-блоков изменилась во время записи IBU; BL2 не тронут", "bad-block map changed during IBU write; BL2 remains untouched"))
	}
	if e = a.loadChunkWithKnownLocal(s, prep.bl2, "stock-bl2.bin", local); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, "mtd erase bl2", 3*time.Minute); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("mtd write bl2 0x%x 0x0 0x%x", loadAddr, bl2Size), 3*time.Minute); e != nil {
		return e
	}
	crc, e := crcFile(prep.bl2)
	if e != nil {
		return e
	}
	if e = a.readbackCRC(s, "bl2", 0, bl2Size, loadAddr, crc); e != nil {
		return e
	}
	a.event(L("Восстановление стока PASS: IBU проверен, BL2 проверен последним. SHA256 источника mtd16=", "Stock restore PASS: IBU verified, BL2 verified last. Source mtd16 SHA256=") + prep.allSHA)
	a.noteln(L("Нажмите Enter для reset или N чтобы оставить U-Boot.", "Press Enter to reset or N to stay in U-Boot."))
	if strings.ToLower(a.ask("> ")) != "n" {
		_ = sendLine(s, "reset")
	}
	return nil
}

func (a *App) physicalRestoreWizard() error {
	a.showNetworkPrerequisites()
	path, e := a.askPath(L("Путь к raw physical NAND image 256 MiB: ", "Path to the 256 MiB raw physical NAND image: "))
	if e != nil {
		return e
	}
	st, _ := os.Stat(path)
	if st.Size() != physicalNANDSize {
		return fmt.Errorf(L("размер physical image %d != %d", "physical image size %d != %d"), st.Size(), physicalNANDSize)
	}
	sha, e := shaFile(path)
	if e != nil {
		return e
	}
	a.noteln("SHA256:", sha)
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, _, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()
	blbad, e := a.mtdBad(s, "bl2", bl2Size)
	if e != nil {
		return e
	}
	bad, e := a.mtdBad(s, "ubi", ubiSize)
	if e != nil {
		return e
	}
	if len(blbad) > 0 || len(bad) > 0 {
		return fmt.Errorf(L("восстановление physical image в 0.2.0-test17 требует отсутствия bad-блоков (bl2=%d ubi=%d); используйте восстановление с учётом формата", "physical-image restore 0.2.0-test17 requires zero bad blocks (bl2=%d ubi=%d); use a format-aware restore instead"), len(blbad), len(bad))
	}
	local, e := a.networkIP()
	if e != nil {
		return e
	}
	if e = a.configureUBootNet(s, local); e != nil {
		return e
	}
	dir := a.sessionScratch("physical-" + time.Now().Format("20060102-150405"))
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	src, e := os.Open(path)
	if e != nil {
		return e
	}
	defer src.Close()
	bl := filepath.Join(dir, "bl2.bin")
	bf, _ := os.Create(bl)
	_, e = io.CopyN(bf, src, bl2Size)
	bf.Close()
	if e != nil {
		return e
	}
	var chunks []string
	remain := int64(ubiSize)
	for i := 0; remain > 0; i++ {
		n := int64(chunkSize)
		if remain < n {
			n = remain
		}
		cp := filepath.Join(dir, fmt.Sprintf("ubi-%02d.bin", i))
		f, _ := os.Create(cp)
		_, e = io.CopyN(f, src, n)
		f.Close()
		if e != nil {
			return e
		}
		chunks = append(chunks, cp)
		remain -= n
	}
	if e = a.loadChunkWithKnownLocal(s, chunks[0], "physical-00.bin", local); e != nil {
		return e
	}
	a.noteln(L("Будет восстановлен raw physical image; UBI region erase/write/readback, BL2 LAST.", "The raw physical image will be restored; UBI region erase/write/readback, BL2 LAST."))
	if e = a.confirm(app.Erase, "RESTORE PHYSICAL NAND"); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, "mtd erase ubi", 20*time.Minute); e != nil {
		return e
	}
	for i, ch := range chunks {
		if i > 0 {
			if e = a.loadChunkWithKnownLocal(s, ch, fmt.Sprintf("physical-%02d.bin", i), local); e != nil {
				return e
			}
		}
		st, _ := os.Stat(ch)
		off := uint64(i) * chunkSize
		crc, _ := crcFile(ch)
		if _, e = a.ubootCommand(s, fmt.Sprintf("mtd write ubi 0x%x 0x%x 0x%x", loadAddr, off, st.Size()), 10*time.Minute); e != nil {
			return e
		}
		if e = a.readbackCRC(s, "ubi", off, uint64(st.Size()), loadAddr, crc); e != nil {
			return e
		}
		a.event(fmt.Sprintf(L("physical UBI часть %d/%d PASS", "physical UBI chunk %d/%d PASS"), i+1, len(chunks)))
	}
	if e = a.loadChunkWithKnownLocal(s, bl, "physical-bl2.bin", local); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, "mtd erase bl2", 3*time.Minute); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("mtd write bl2 0x%x 0 0x%x", loadAddr, bl2Size), 3*time.Minute); e != nil {
		return e
	}
	crc, _ := crcFile(bl)
	if e = a.readbackCRC(s, "bl2", 0, bl2Size, loadAddr, crc); e != nil {
		return e
	}
	a.event(L("Восстановление physical NAND PASS; SHA256 источника=", "Physical NAND restore PASS; source SHA256=") + sha)
	return nil
}

func (a *App) bootRecoveryWizard() error {
	a.showNetworkPrerequisites()
	path, e := a.askPath(L("Путь к OpenWrt initramfs/recovery .itb: ", "Path to the OpenWrt initramfs/recovery .itb: "))
	if e != nil {
		return e
	}
	if e = validateFIT(path); e != nil {
		return e
	}
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, _, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()
	if _, e = a.tftpLoad(s, path, "ursido-recovery.itb", loadAddr); e != nil {
		return e
	}
	if out, rc, _ := a.ubootCommandRaw(s, fmt.Sprintf("iminfo 0x%x", loadAddr), 60*time.Second); rc != 0 {
		a.noteln(L("[WARN] iminfo недоступен/ошибка; FIT magic проверен на ПК. Вывод:", "[WARN] iminfo unavailable/failed; FIT magic was checked on PC. Output:"), string(out))
	}
	a.noteln(L("Следующая команда bootm запускает ITB только из RAM и не пишет NAND.", "The next bootm command runs the ITB from RAM only and does not write NAND."))
	if e = a.confirm(app.NonPersistent, "BOOT RAM ITB"); e != nil {
		return e
	}
	if e = sendLine(s, fmt.Sprintf("bootm 0x%x", loadAddr)); e != nil {
		return e
	}
	a.noteln(L("bootm отправлен. UART вывод продолжается 60 секунд; Ctrl+C здесь не отправляется в роутер.", "bootm sent. UART output continues for 60 seconds; Ctrl+C here is not sent to the router."))
	end := time.Now().Add(60 * time.Second)
	buf := make([]byte, 4096)
	for time.Now().Before(end) {
		n, _ := s.Read(buf, 500*time.Millisecond)
		if n > 0 {
			a.logBytes(buf[:n], true)
		}
	}
	return nil
}

func (a *App) diagnosticsWizard() error {
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, p, trans, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()
	a.notef("\n[PROFILE] %s / %s\n", p.Model, p.SoC)
	a.noteln("[NAND vendor INFO]", identifyVendor(trans))
	if out, e := a.ubootCommand(s, "mtd bad bl2", 2*time.Minute); e == nil {
		a.noteln("--- mtd bad bl2 ---\n", string(out))
	}
	if out, e := a.ubootCommand(s, "mtd bad ubi", 2*time.Minute); e == nil {
		a.noteln("--- mtd bad ubi ---\n", string(out))
	}
	if out, e := a.ubootCommand(s, "mtd list", 60*time.Second); e == nil {
		a.noteln("--- MTD layout (read-only) ---\n", string(out))
	}
	a.noteln(L("[INFO] UBI attach пропущен: обычная диагностика не отправляет 'ubi part', потому что attach может изменить UBI metadata/fastmap. Для явного advanced attach используйте режим портирования A / --ubi-attach.",
		"[INFO] UBI attach skipped: normal diagnostics never sends 'ubi part' because attach may change UBI metadata/fastmap. Use Porting mode A / --ubi-attach for an explicit advanced attach."))
	if out, e := a.ubootCommand(s, "printenv", 60*time.Second); e == nil {
		a.noteln("--- environment ---\n", string(out))
	}
	a.noteln(L("Read-only диагностика завершена. NAND/UBI write, erase и attach не выполнялись.", "Read-only diagnostics finished. No NAND/UBI write, erase, or attach was performed."))
	return nil
}

func (a *App) expertMenu() error {
	for {
		fmt.Println(L("\nЭкспертный режим", "\nExpert mode"))
		fmt.Println(L("  1. UART-терминал (история ↑/↓, ручной XMODEM, лог)", "  1. UART terminal (↑/↓ history, manual XMODEM, logging)"))
		fmt.Println(L("  2. Запустить RAM U-Boot и оставить prompt", "  2. Start RAM U-Boot and leave the prompt"))
		fmt.Println(L("  3. Записать существующий UBI volume из файла", "  3. Write an existing UBI volume from a file"))
		fmt.Println(L("  4. Записать raw range в MTD bl2/ubi", "  4. Write a raw range into MTD bl2/ubi"))
		fmt.Println(L("  5. Диагностика", "  5. Diagnostics"))
		fmt.Println(L("  6. UART Shell (прозрачный терминал, ничего не отправляет сам)", "  6. UART Shell (transparent passthrough, sends nothing by itself)"))
		fmt.Println(L("  0. Назад", "  0. Back"))
		v := a.ask(L("Выбор: ", "Choice: "))
		ops := map[string]string{"1": "terminal", "2": "ram-uboot", "3": "ubi-volume", "4": "raw-mtd", "5": "diagnostics", "6": "shell"}
		switch v {
		case "1", "2", "3", "4", "5", "6":
			if e := a.RunOperation(ops[v]); e != nil {
				return e
			}
		case "0":
			return nil
		default:
			fmt.Println(L("Неверный выбор", "Invalid choice"))
		}
	}
}
func (a *App) uartShell() error {
	port, e := a.choosePort()
	if e != nil {
		return e
	}
	s, e := openSerial(port)
	if e != nil {
		return e
	}
	defer s.Close()
	log, e := a.startLog("shell")
	if e != nil {
		return e
	}
	_ = log
	defer a.closeLog()
	return a.uartShellOn(s)
}
func (a *App) uartShellOn(s Serial) error {
	// The simple shell now shares the same low-latency raw backend as the full
	// terminal. It still sends nothing automatically.
	return a.runTerminalOnMode(s, true)
}
func (a *App) expertUBIVolume() error {
	a.showNetworkPrerequisites()
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, _, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()
	if _, e = a.ubootCommand(s, "ubi part ubi", 3*time.Minute); e != nil {
		return e
	}
	layout, e := a.ubootCommand(s, "ubi info layout", 60*time.Second)
	if e != nil {
		return e
	}
	a.noteln(string(layout))
	name := a.ask(L("Существующий volume name: ", "Existing volume name: "))
	if name == "" || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(name) {
		return errors.New(L("недопустимое имя volume", "invalid volume name"))
	}
	if _, e = a.ubootCommand(s, "ubi check "+name, 30*time.Second); e != nil {
		return fmt.Errorf(L("volume не существует: %w", "volume does not exist: %w"), e)
	}
	path, e := a.askPath(L("Файл для volume: ", "File for the volume: "))
	if e != nil {
		return e
	}
	st, _ := os.Stat(path)
	if st.Size() > maxGenericRAMFile {
		return fmt.Errorf(L("файл volume >%d MiB за один раз не поддерживается в %s", "expert one-shot volume file >%d MiB is not supported in %s"),
			maxGenericRAMFile/(1024*1024), appVersion)
	}
	sha, _ := shaFile(path)
	a.notef("WRITE EXISTING UBI VOLUME %s size=%d SHA256=%s\n", name, st.Size(), sha)
	if _, e = a.tftpLoad(s, path, "ursido-volume.bin", loadAddr); e != nil {
		return e
	}
	if e = a.confirm(app.Write, "WRITE UBI VOLUME "+name); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("ubi write 0x%x %s 0x%x", loadAddr, name, st.Size()), 10*time.Minute); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("ubi read 0x%x %s 0x%x", verifyAddr, name, st.Size()), 10*time.Minute); e != nil {
		return e
	}
	crc, _ := crcFile(path)
	out, e := a.ubootCommand(s, fmt.Sprintf("crc32 0x%x 0x%x", verifyAddr, st.Size()), 2*time.Minute)
	if e != nil {
		return e
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, crc)).Match(out) {
		return errors.New(L("CRC UBI volume после записи не совпал", "UBI volume readback CRC mismatch"))
	}
	a.event(L("Запись/проверка UBI volume PASS", "UBI volume write/readback PASS"))
	return nil
}
func (a *App) expertRawMTD() error {
	a.showNetworkPrerequisites()
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, _, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()
	target := strings.ToLower(a.ask(L("Target [bl2/ubi]: ", "Target [bl2/ubi]: ")))
	var cap uint64
	if target == "bl2" {
		cap = bl2Size
	} else if target == "ubi" {
		cap = ubiSize
	} else {
		return errors.New(L("target должен быть bl2 или ubi", "target must be bl2 or ubi"))
	}
	offS := a.ask(L("Offset relative to target, hex (например 0x0): ", "Offset relative to the target, hex (e.g. 0x0): "))
	off, e := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(offS), "0x"), 16, 64)
	if e != nil {
		return e
	}
	path, e := a.askPath(L("Файл: ", "File: "))
	if e != nil {
		return e
	}
	st, _ := os.Stat(path)
	if st.Size() > maxGenericRAMFile {
		return fmt.Errorf(L("файл >%d MiB; используйте пошаговый режим", "file >%d MiB; use chunked workflow"),
			maxGenericRAMFile/(1024*1024))
	}
	if off+uint64(st.Size()) > cap {
		return errors.New(L("файл выходит за границы target", "file exceeds target range"))
	}
	if target == "bl2" && (off != 0 || st.Size() != bl2Size) {
		a.noteln(L("[WARN] частичная запись BL2 — только для экспертов, может лишить безопасной передачи управления из BootROM.", "[WARN] BL2 partial/odd write is expert-only and can remove BootROM handoff safety."))
	}
	sha, _ := shaFile(path)
	a.notef("Target=%s offset=0x%x size=0x%x SHA256=%s\n", target, off, st.Size(), sha)
	if _, e = a.tftpLoad(s, path, "ursido-raw.bin", loadAddr); e != nil {
		return e
	}
	exact := fmt.Sprintf("WRITE RAW %s 0x%x", strings.ToUpper(target), off)
	if e = a.confirm(app.Write, exact); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("mtd write %s 0x%x 0x%x 0x%x", target, loadAddr, off, st.Size()), 10*time.Minute); e != nil {
		return e
	}
	crc, _ := crcFile(path)
	if e = a.readbackCRC(s, target, off, uint64(st.Size()), verifyAddr, crc); e != nil {
		return e
	}
	a.event(L("Запись/проверка RAW MTD PASS", "RAW MTD write/readback PASS"))
	return nil
}

func (a *App) makeSupportBundle() (string, error) {
	stamp := time.Now().Format("20060102-150405")
	out := filepath.Join(a.root, fmt.Sprintf("UrsidoRescue-support-%s.zip", stamp))
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	add := func(path, name string) error {
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		info, e := in.Stat()
		if e != nil {
			return e
		}
		h, e := zip.FileInfoHeader(info)
		if e != nil {
			return e
		}
		h.Name = filepath.ToSlash(name)
		h.Method = zip.Deflate
		w, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		_, e = io.Copy(w, in)
		return e
	}
	entries, _ := os.ReadDir(a.work)
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		low := strings.ToLower(name)
		if strings.HasSuffix(low, ".log") || strings.HasSuffix(low, ".txt") {
			if e := add(filepath.Join(a.work, name), filepath.ToSlash(filepath.Join("work", name))); e != nil {
				_ = zw.Close()
				_ = f.Close()
				return "", e
			}
		}
	}
	// Sessions (work/sessions/<id>/): logs and metadata only. Probe binaries
	// (flash samples, DTB) and scratch chunks stay out: they can hold device
	// identity and are large; a porting bundle is exported separately.
	sessions, _ := os.ReadDir(app.SessionsDir(a.work))
	for _, sd := range sessions {
		if !sd.IsDir() {
			continue
		}
		for _, name := range []string{"session.json", "session.log", "uart.log", "operations.jsonl", "errors.log", "transcript.jsonl"} {
			p := filepath.Join(app.SessionsDir(a.work), sd.Name(), name)
			if !fileExists(p) {
				continue
			}
			if e := add(p, filepath.ToSlash(filepath.Join("work", "sessions", sd.Name(), name))); e != nil {
				_ = zw.Close()
				_ = f.Close()
				return "", e
			}
		}
	}
	for _, name := range []string{"VERSION", "STATUS.md", "BUILDINFO.json", "MANIFEST.json"} {
		p := filepath.Join(a.root, name)
		if fileExists(p) {
			if e := add(p, name); e != nil {
				_ = zw.Close()
				_ = f.Close()
				return "", e
			}
		}
	}
	if e := zw.Close(); e != nil {
		_ = f.Close()
		return "", e
	}
	if e := f.Close(); e != nil {
		return "", e
	}
	return out, nil
}

func (a *App) selftest() error {
	if appVersion != "0.2.0-test17" {
		return errors.New("version")
	}
	if _, e := probe.CheckUBoot("saveenv"); e == nil {
		return errors.New("probe guard allowed saveenv")
	}
	if _, e := probe.CheckLinux("dd if=/dev/zero of=/dev/mtd0"); e == nil {
		return errors.New("probe guard allowed a flash write")
	}
	if c, e := probe.CheckLinux("id -u"); e != nil || c != "id -u" {
		return errors.New("probe guard rejected UID proof")
	}
	if c, e := probe.CheckUBoot("mtd  list"); e != nil || c != "mtd list" {
		return errors.New("probe guard canonicalisation")
	}
	if crc16Xmodem([]byte("123456789")) != 0x31c3 {
		return errors.New("xmodem CRC")
	}
	sample := []byte("List of MTD devices:\n* spi-nand0\n  - block size: 0x20000 bytes\n  - min I/O: 0x800 bytes\n  - 0x000000000000-0x000010000000 : \"spi-nand0\"\n          - 0x000000000000-0x000000020000 : \"bl2\"\n          - 0x000000020000-0x000010000000 : \"ubi\"\n")
	if e := requireGeometry(sample); e != nil {
		return e
	}
	if !promptPresent([]byte("\r\nAN7581> \r\n")) {
		return errors.New("prompt parser")
	}
	if identifyVendor([]byte("Fudan Micro FM25G02B")) != "Fudan Micro" {
		return errors.New("vendor parser")
	}
	for _, p := range profiles {
		if e := validatePinned(a.root, p); e != nil {
			return e
		}
		if e := validateFIP(filepath.Join(a.root, filepath.FromSlash(p.RAMFIPRel))); e != nil {
			return fmt.Errorf("%s FIP: %w", p.ID, e)
		}
	}
	xs, e := parseBadBlocks([]byte("0x00020000\n0x00040000\n"), ubiSize)
	if e != nil || len(xs) != 2 {
		return errors.New("bad block parser")
	}
	sp := goodSpans(0, 0x80000, []uint64{0x20000})
	if len(sp) != 2 || sp[0][1] != 0x20000 || sp[1][0] != 0x40000 {
		return errors.New("good spans")
	}
	return nil
}
