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

	"ursidorescue/probe"
)

const (
	appName               = "UrsidoRescue"
	appVersion            = "0.2.0-test12"
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
			if err := a.stockRestoreWizard(); err != nil {
				a.showErr(err)
			}
		case "2":
			if err := a.fipRepairWizard(); err != nil {
				a.showErr(err)
			}
		case "3":
			if err := a.physicalRestoreWizard(); err != nil {
				a.showErr(err)
			}
		case "4":
			if err := a.bootRecoveryWizard(); err != nil {
				a.showErr(err)
			}
		case "5":
			if err := a.diagnosticsWizard(); err != nil {
				a.showErr(err)
			}
		case "6":
			p, err := a.makeSupportBundle()
			if err != nil {
				a.showErr(err)
			} else {
				fmt.Println(L("Пакет для отчёта:", "Report bundle:"), p)
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
func bold(s string) string { return "\x1b[1m" + s + "\x1b[22m" }

func (a *App) showErr(err error) {
	fmt.Println("\n[STOP]", err)
	fmt.Println(L("Никаких дополнительных write/erase команд после этой ошибки не отправлено.", "No further write/erase commands were sent after this error."))
	fmt.Println()
}
func (a *App) ask(prompt string) string {
	fmt.Print(prompt)
	s, _ := a.reader.ReadString('\n')
	return strings.TrimSpace(s)
}
func (a *App) askPath(prompt string) (string, error) {
	p := strings.Trim(strings.TrimSpace(a.ask(prompt)), "\"")
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
func (a *App) confirm(exact string) error {
	fmt.Printf(L("Введите точно %s: ", "Type exactly %s: "), exact)
	if a.ask("") != exact {
		return errors.New(L("операция отменена пользователем", "operation cancelled by the user"))
	}
	return nil
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
	stamp := time.Now().Format("20060102-150405")
	p := filepath.Join(a.work, fmt.Sprintf("%s-%s.uart.log", prefix, stamp))
	f, e := os.Create(p)
	if e != nil {
		return nil, e
	}
	a.logFile = f
	fmt.Println(L("UART-лог:", "UART log:"), p)
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
		_, _ = os.Stdout.Write(b)
	}
}
func (a *App) event(s string) { fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), s) }

func chooseProfileInteractive(a *App) (Profile, error) {
	fmt.Println(L("Профиль устройства:", "Device profile:"))
	fmt.Println(L("  1. Auto (определить по UART, если возможно)", "  1. Auto (detect from UART if possible)"))
	fmt.Println("  2. Nokia XG-040G-MD / AN7581")
	fmt.Println("  3. Nokia XG-040G-MF / AN7583")
	v := a.ask(L("Выбор [1]: ", "Choice [1]: "))
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
	fmt.Println(L("Найденные UART:", "UART ports found:"))
	for i, p := range ports {
		fmt.Printf("  %d. %s\n", i+1, p)
	}
	if len(ports) == 0 {
		fmt.Println(L("  (автообнаружение пусто; можно ввести порт вручную)", "  (none detected; you can type a port manually)"))
	}
	prompt := L("UART порт (номер пункта или имя): ", "UART port (list number or name): ")
	if len(ports) == 1 {
		prompt = L("UART порт [1]: ", "UART port [1]: ")
	}
	v := a.ask(prompt)
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

func (a *App) waitReceiver(s Serial, timeout time.Duration, keepExisting bool) (ReceiverPhase, Profile, []byte, error) {
	if !keepExisting {
		_ = s.ResetInput()
	}
	a.event(L("Ожидание BootROM Press x / CCC. Если устройство уже печатает C, НЕ перезагружайте его.", "Waiting for BootROM Press x / CCC. If the device already prints C, do NOT reboot it."))
	deadline := time.Now().Add(timeout)
	tail := make([]byte, 0, 65536)
	cCount := 0
	lastX := time.Time{}
	press := false
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		n, e := s.Read(buf, 500*time.Millisecond)
		if e != nil {
			return phaseUnknown, Profile{}, tail, e
		}
		if n > 0 {
			d := append([]byte(nil), buf[:n]...)
			a.logBytes(d, true)
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
						return ph, p, tail, nil
					}
				} else if b == 9 || b == 10 || b == 13 || b == 32 || b < 0x20 {
				} else {
					cCount = 0
				}
			}
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

func scanXmodemReply(data []byte, consecutiveCAN *int) xmodemReply {
	for _, b := range data {
		switch b {
		case 0x06: // ACK
			*consecutiveCAN = 0
			return xmodemReplyACK
		case 0x18: // CAN: require a real CAN CAN cancellation, not one noisy byte.
			(*consecutiveCAN)++
			if *consecutiveCAN >= 2 {
				return xmodemReplyCancel
			}
		case 0x15, 'C': // NAK or CRC request: retry the current block immediately.
			*consecutiveCAN = 0
			return xmodemReplyRetry
		default:
			*consecutiveCAN = 0
		}
	}
	return xmodemReplyNone
}

func (a *App) xmodemSend(s Serial, path, label string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
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

	completed := false
	defer func() {
		if !completed {
			// Leave BootROM in a deterministic state after any host-side failure.
			_ = s.Write([]byte{0x18, 0x18, 0x18})
		}
	}()

	for idx := int64(0); idx < blocks; idx++ {
		for i := range payload {
			payload[i] = 0x1a
		}
		n, er := f.Read(payload)
		if er != nil && er != io.EOF {
			return er
		}
		c := crc16Xmodem(payload)
		pkt := make([]byte, 0, 133)
		pkt = append(pkt, 0x01, seq, 0xff-seq)
		pkt = append(pkt, payload...)
		pkt = append(pkt, byte(c>>8), byte(c))

		accepted := false
		for attempt := 1; attempt <= 8 && !accepted; attempt++ {
			if e = s.Write(pkt); e != nil {
				return e
			}
			deadline := time.Now().Add(2 * time.Second)
			retryNow := false
			consecutiveCAN := 0
			for time.Now().Before(deadline) {
				nr, er := s.Read(resp, 150*time.Millisecond)
				if er != nil {
					return er
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
					return fmt.Errorf(L("BootROM подтвердил отмену XMODEM %s (CAN CAN)", "BootROM confirmed XMODEM cancellation %s (CAN CAN)"), label)
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
			return fmt.Errorf(L("XMODEM блок %d не подтверждён после 8 попыток", "XMODEM block %d not ACKed after 8 attempts"), idx+1)
		}
		sent += int64(n)
		seq++
		if idx == 0 || (idx+1)%32 == 0 || idx+1 == blocks {
			fmt.Printf("\r[XMODEM] %s: %d/%d bytes (%d/%d)", label, sent, size, idx+1, blocks)
		}
	}

	for attempt := 1; attempt <= 6; attempt++ {
		if e = s.Write([]byte{0x04}); e != nil {
			return e
		}
		deadline := time.Now().Add(2 * time.Second)
		retryNow := false
		consecutiveCAN := 0
		for time.Now().Before(deadline) {
			n, er := s.Read(resp, 150*time.Millisecond)
			if er != nil {
				return er
			}
			if n == 0 {
				continue
			}
			a.logBytes(resp[:n], false)
			switch scanXmodemReply(resp[:n], &consecutiveCAN) {
			case xmodemReplyACK:
				fmt.Println()
				a.event(L("XMODEM завершён: ", "XMODEM complete: ") + label)
				completed = true
				return nil
			case xmodemReplyRetry:
				retryNow = true
			case xmodemReplyCancel:
				return fmt.Errorf(L("BootROM подтвердил отмену XMODEM %s на EOT (CAN CAN)", "BootROM confirmed XMODEM cancellation %s at EOT (CAN CAN)"), label)
			}
			if retryNow {
				break
			}
		}
		a.event(fmt.Sprintf(L("XMODEM повтор EOT %s, попытка %d/6", "XMODEM retry EOT %s attempt %d/6"), label, attempt))
		time.Sleep(80 * time.Millisecond)
	}
	return fmt.Errorf(L("XMODEM EOT не подтверждён: %s", "XMODEM EOT not ACKed: %s"), label)
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
func (a *App) waitUBootPrompt(s Serial, timeout time.Duration) ([]byte, error) {
	a.event(L("Ожидание RAM U-Boot; Enter не отправляется, чтобы не выбрать bootmenu", "Waiting for RAM U-Boot; Enter is not sent so no bootmenu entry gets selected"))
	end := time.Now().Add(timeout)
	var out, tail []byte
	buf := make([]byte, 4096)
	seen := false
	menu := false
	lastBreak := time.Time{}
	breaks := 0
	menuEscapes := 0
	for time.Now().Before(end) {
		n, e := s.Read(buf, 120*time.Millisecond)
		if e != nil {
			return out, e
		}
		if n == 0 {
			continue
		}
		d := append([]byte(nil), buf[:n]...)
		a.logBytes(d, true)
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
			return out, nil
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
				// Once bootmenu is visible, ESC is the correct non-selecting exit.
				// Do not keep mixing Ctrl-C into the menu/prompt stream.
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
			return out, errors.New(L("до prompt замечена разрушительная автозагрузка", "destructive autoboot observed before prompt"))
		}
		if strings.Contains(low, "starting kernel") || strings.Contains(low, "booting linux on physical cpu") {
			return out, errors.New(L("автозагрузка ушла в Linux до prompt", "autoboot escaped into Linux before prompt"))
		}
		if strings.Contains(low, "press x to load bl31") && !seen {
			return out, errors.New(L("после FIP устройство вернулось в BootROM", "device returned to BootROM after FIP"))
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
func (a *App) ubootCommandRaw(s Serial, command string, timeout time.Duration) ([]byte, int, error) {
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
	fmt.Println(L("\nЕсли сейчас уже видите 'Press x to load BL31 + U-Boot FIP' и CCC — ничего не перезагружайте.", "\nIf you already see 'Press x to load BL31 + U-Boot FIP' and CCC, do not reboot anything."))
	fmt.Println(L("Иначе выключите Nokia, подключите UART, удерживайте Reset и подайте питание для BootROM recovery.", "Otherwise power the Nokia off, connect the UART, hold Reset and power it on for BootROM recovery."))
	phase, det, trans, e := a.waitReceiver(s, 180*time.Second, true)
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
		fmt.Println(L("SoC не удалось определить из текущего UART окна.", "The SoC could not be identified from the current UART output."))
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
	fmt.Println("[INFO]", L(p.StatusRU, p.Status))
	pre := filepath.Join(a.root, filepath.FromSlash(p.PreloaderRel))
	fip := filepath.Join(a.root, filepath.FromSlash(p.RAMFIPRel))
	if phase == phasePreloader {
		a.event(L("Текущий receiver классифицирован как PRELOADER stage", "Current receiver classified as the PRELOADER stage"))
		if e = a.xmodemSend(s, pre, p.SoC+" preloader"); e != nil {
			s.Close()
			a.closeLog()
			return nil, Profile{}, trans, e
		}
		ph2, det2, t2, e2 := a.waitReceiver(s, 180*time.Second, false)
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
	if e = a.xmodemSend(s, fip, p.SoC+" RAM BL31+U-Boot FIP"); e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
	}
	boot, e := a.waitUBootPrompt(s, 180*time.Second)
	trans = append(trans, boot...)
	if e != nil {
		s.Close()
		a.closeLog()
		return nil, Profile{}, trans, e
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

func (a *App) networkIP() (string, error) {
	ip := detectLocalIP()
	if ip != "" {
		fmt.Println(L("[NET] найден адрес ПК:", "[NET] PC address found:"), ip)
		return ip, nil
	}
	fmt.Println(L("На ПК не найден IPv4 192.168.1.x. Настройте Ethernet статически, рекомендуется 192.168.1.254/24.", "No 192.168.1.x IPv4 address on this PC. Configure Ethernet statically, 192.168.1.254/24 recommended."))
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
		// Re-apply the complete network tuple for every attempt. A failed U-Boot
		// network command may leave ARP/net state stale even though the prompt is alive.
		if e = a.configureUBootNet(s, local); e != nil {
			return e
		}

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
			if re := a.resyncUBootAfterNetError(s); re != nil {
				return fmt.Errorf("%w; resync: %v", last, re)
			}
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
	// Actual transfer + RAM hash/CRC is the network preflight. Do not fail the
	// operation merely because an ICMP ping was lost during link/ARP convergence.
	e = a.tftpLoadKnownLocal(s, path, remote, addr, local, true)
	return local, e
}

func (a *App) loadChunkWithKnownLocal(s Serial, path, remote, local string) error {
	return a.tftpLoadKnownLocal(s, path, remote, loadAddr, local, true)
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
func verifyManifestEntry(dir, selected string) error {
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
			fmt.Println(L("[OK] SHA256SUMS проверен:", "[OK] SHA256SUMS verified:"), base)
		} else {
			fmt.Println(L("[INFO] SHA256SUMS есть, но для выбранного mtd16 нет записи; проверки размера/потока/BL2 остаются", "[INFO] SHA256SUMS exists but selected mtd16 has no direct entry; size/stream/BL2 checks remain active"))
		}
		return nil
	}
	fmt.Println(L("[INFO] SHA256SUMS не найден; mtd16 будет проверен полным чтением, точным размером и происхождением BL2", "[INFO] SHA256SUMS not found; mtd16 will be checked by full stream, exact size and BL2 provenance"))
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
	fmt.Printf(L("[INFO] Найдены файлы бэкапа MedveFlasher: mtd0..mtd16, есть %d/17; источник восстановления=%s\n", "[INFO] MedveFlasher backup files found: mtd0..mtd16 present count=%d/17; restore source=%s\n"), present, filepath.Base(m))
	if e = verifyManifestEntry(abs, m); e != nil {
		return "", e
	}
	return m, nil
}

func (a *App) stockRestoreWizard() error {
	path, e := a.askStockSource()
	if e != nil {
		return e
	}
	fmt.Println(L("Проверяю и раскладываю mtd16 на BL2 + IBU chunks без записи NAND...", "Checking mtd16 and splitting it into BL2 + IBU chunks without writing NAND..."))
	prep, e := a.prepareStock(path)
	if e != nil {
		return e
	}
	fmt.Printf("mtd16 SHA256: %s\nBL2 SHA256: %s\nchunks: %d\n", prep.allSHA, prep.bl2SHA, len(prep.chunks))
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
	fmt.Printf(L("[INFO] bad-блоков в ubi: %d\n", "[INFO] bad blocks in ubi: %d\n"), len(bad))
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
	fmt.Println(L("\nВНИМАНИЕ: будет полностью очищен OpenWrt UBI region, затем восстановлен stock mtd16; BL2 пишется ПОСЛЕДНИМ.", "\nWARNING: the OpenWrt UBI region will be erased completely, then stock mtd16 restored; BL2 is written LAST."))
	if e = a.confirm("RESTORE STOCK BACKUP"); e != nil {
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
	fmt.Println(L("Нажмите Enter для reset или N чтобы оставить U-Boot.", "Press Enter to reset or N to stay in U-Boot."))
	if strings.ToLower(a.ask("> ")) != "n" {
		_ = sendLine(s, "reset")
	}
	return nil
}

func (a *App) physicalRestoreWizard() error {
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
	fmt.Println("SHA256:", sha)
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
		return fmt.Errorf(L("восстановление physical image в 0.2.0-test12 требует отсутствия bad-блоков (bl2=%d ubi=%d); используйте восстановление с учётом формата", "physical-image restore 0.2.0-test12 requires zero bad blocks (bl2=%d ubi=%d); use a format-aware restore instead"), len(blbad), len(bad))
	}
	local, e := a.networkIP()
	if e != nil {
		return e
	}
	if e = a.configureUBootNet(s, local); e != nil {
		return e
	}
	dir := filepath.Join(a.work, "physical-"+time.Now().Format("20060102-150405"))
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
	fmt.Println(L("Будет восстановлен raw physical image; UBI region erase/write/readback, BL2 LAST.", "The raw physical image will be restored; UBI region erase/write/readback, BL2 LAST."))
	if e = a.confirm("RESTORE PHYSICAL NAND"); e != nil {
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
		fmt.Println(L("[WARN] iminfo недоступен/ошибка; FIT magic проверен на ПК. Вывод:", "[WARN] iminfo unavailable/failed; FIT magic was checked on PC. Output:"), string(out))
	}
	fmt.Println(L("Следующая команда bootm запускает ITB только из RAM и не пишет NAND.", "The next bootm command runs the ITB from RAM only and does not write NAND."))
	if e = a.confirm("BOOT RAM ITB"); e != nil {
		return e
	}
	if e = sendLine(s, fmt.Sprintf("bootm 0x%x", loadAddr)); e != nil {
		return e
	}
	fmt.Println(L("bootm отправлен. UART вывод продолжается 60 секунд; Ctrl+C здесь не отправляется в роутер.", "bootm sent. UART output continues for 60 seconds; Ctrl+C here is not sent to the router."))
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
	fmt.Printf("\n[PROFILE] %s / %s\n", p.Model, p.SoC)
	fmt.Println("[NAND vendor INFO]", identifyVendor(trans))
	if out, e := a.ubootCommand(s, "mtd bad bl2", 2*time.Minute); e == nil {
		fmt.Println("--- mtd bad bl2 ---\n", string(out))
	}
	if out, e := a.ubootCommand(s, "mtd bad ubi", 2*time.Minute); e == nil {
		fmt.Println("--- mtd bad ubi ---\n", string(out))
	}
	if _, e := a.ubootCommand(s, "ubi part ubi", 3*time.Minute); e == nil {
		if out, e := a.ubootCommand(s, "ubi info layout", 60*time.Second); e == nil {
			fmt.Println("--- UBI layout ---\n", string(out))
		}
	} else {
		fmt.Println(L("[INFO] UBI не подключился:", "[INFO] UBI attach failed:"), e)
	}
	if out, e := a.ubootCommand(s, "printenv", 60*time.Second); e == nil {
		fmt.Println("--- environment ---\n", string(out))
	}
	fmt.Println(L("Диагностика завершена. NAND write/erase не выполнялись.", "Diagnostics finished. No NAND write/erase was done."))
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
		switch v {
		case "1":
			if e := a.runTerminal(); e != nil {
				return e
			}
		case "6":
			if e := a.uartShell(); e != nil {
				return e
			}
		case "2":
			pref, e := chooseProfileInteractive(a)
			if e != nil {
				return e
			}
			s, _, _, e := a.acquireRAMUBoot(pref)
			if e != nil {
				return e
			}
			fmt.Println(L("RAM U-Boot готов. Открываю UART Shell; Ctrl+] вернёт в меню.", "RAM U-Boot is ready. Opening the UART Shell; Ctrl+] returns to the menu."))
			e = a.uartShellOn(s)
			s.Close()
			a.closeLog()
			if e != nil {
				return e
			}
		case "3":
			if e := a.expertUBIVolume(); e != nil {
				return e
			}
		case "4":
			if e := a.expertRawMTD(); e != nil {
				return e
			}
		case "5":
			if e := a.diagnosticsWizard(); e != nil {
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
	fmt.Println(string(layout))
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
		return errors.New(L("файл volume >64 MiB за один раз не поддерживается в 0.2.0-test12", "expert one-shot volume file >64 MiB is not supported in 0.2.0-test12"))
	}
	sha, _ := shaFile(path)
	fmt.Printf("WRITE EXISTING UBI VOLUME %s size=%d SHA256=%s\n", name, st.Size(), sha)
	if _, e = a.tftpLoad(s, path, "ursido-volume.bin", loadAddr); e != nil {
		return e
	}
	if e = a.confirm("WRITE UBI VOLUME " + name); e != nil {
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
		return errors.New(L("файл >64 MiB; используйте пошаговый режим", "file >64 MiB; use chunked workflow"))
	}
	if off+uint64(st.Size()) > cap {
		return errors.New(L("файл выходит за границы target", "file exceeds target range"))
	}
	if target == "bl2" && (off != 0 || st.Size() != bl2Size) {
		fmt.Println(L("[WARN] частичная запись BL2 — только для экспертов, может лишить безопасной передачи управления из BootROM.", "[WARN] BL2 partial/odd write is expert-only and can remove BootROM handoff safety."))
	}
	sha, _ := shaFile(path)
	fmt.Printf("Target=%s offset=0x%x size=0x%x SHA256=%s\n", target, off, st.Size(), sha)
	if _, e = a.tftpLoad(s, path, "ursido-raw.bin", loadAddr); e != nil {
		return e
	}
	exact := fmt.Sprintf("WRITE RAW %s 0x%x", strings.ToUpper(target), off)
	if e = a.confirm(exact); e != nil {
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
	if appVersion != "0.2.0-test12" {
		return errors.New("version")
	}
	if _, e := probe.CheckUBoot("saveenv"); e == nil {
		return errors.New("probe guard allowed saveenv")
	}
	if _, e := probe.CheckLinux("dd if=/dev/zero of=/dev/mtd0"); e == nil {
		return errors.New("probe guard allowed a flash write")
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
