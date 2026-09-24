package probe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Layers selects what to collect.
type Layers struct {
	BootROM bool
	UBoot   bool
	Linux   bool
	Flash   bool
	DT      bool
	Network bool
	GPIO    bool
}

// AllLayers is the full automatic probe.
func AllLayers() Layers {
	return Layers{BootROM: true, UBoot: true, Linux: true, Flash: true, DT: true, Network: true, GPIO: true}
}

// LinuxLoginPlan contains credentials only in memory. Password fields must
// never be written to transcripts, bundles or operator logs.
type LinuxLoginPlan struct {
	LoginUser       string
	LoginPassword   string
	RootUser        string
	RootPassword    string
	Model           string
	Source          string
	FTPEnabled      bool
	SettingsChanged bool
}

// LinuxLoginAssist may use a trusted LAN-side stock-management path to obtain
// the current console credentials. provision=true explicitly allows it to
// enable the stock FTP service if needed for the UID-0 service account.
type LinuxLoginAssist func(provision bool) (LinuxLoginPlan, error)

// Options control one collection run.
type Options struct {
	Layers           Layers
	NoLinux          bool          // never wait for Linux
	UBootOnly        bool          // stop after U-Boot
	LinuxOnly        bool          // never interrupt U-Boot
	FirstReachable   bool          // stop after whichever environment is reached first
	BootROMHandshake bool          // answer "Press x" with x and wait for the XMODEM 'C'
	Timeout          time.Duration // whole run
	LinuxUser        string
	LinuxPassword    string
	LinuxLoginAssist LinuxLoginAssist
	StopKey          string // extra autoboot stop string
	SampleHead       int    // bytes read from the start of each partition (4 KiB default)
	Unsafe           bool   // accepted and recorded; never relaxes the guard
	Ask              func(prompt string) string
	RAMHint          uint64 // a load address known for this board; still checked against bdinfo
	UBootSource      string // "device" (default) or "ursido-ram-uboot"
	AtUBootPrompt    string // the session already sits at this U-Boot prompt
	// UBIAttach is the advanced, NOT read-only mode: U-Boot "ubi part" may
	// write to flash (volume auto-resize, fastmap, scrubbing). Off by default.
	UBIAttach bool
	// Wake lets the probe send one Ctrl-C when the line is silent, to find a
	// prompt on a device that is already running. Off by default: on an
	// unknown device the first pass is passive.
	Wake bool
}

// Outcome is what a run reached.
type Outcome struct {
	UARTData          bool     `json:"uart_data"`
	BootROM           bool     `json:"bootrom"`
	BootROMXmodem     bool     `json:"bootrom_xmodem"`
	UBoot             bool     `json:"uboot"`
	OtherBootloader   string   `json:"other_bootloader,omitempty"`
	Linux             bool     `json:"linux"`
	LinuxUID0         bool     `json:"linux_uid0,omitempty"`
	LinuxLoginBlock   bool     `json:"linux_login_required,omitempty"`
	StockLANAssist    bool     `json:"stock_lan_assist,omitempty"`
	StockProvisioned  bool     `json:"stock_service_provisioned,omitempty"`
	UBIAttached       bool     `json:"ubi_attached,omitempty"`
	UnconfirmedPrompt string   `json:"unconfirmed_prompt,omitempty"`
	Blocked           int      `json:"blocked"`
	Notes             []string `json:"notes,omitempty"`
}

type collector struct {
	s   *Session
	o   Options
	res Outcome

	wantUB, wantLX, wantBR bool
	ubDone, lxDone, brDone bool
	noInterrupt            bool
	handledActivate        int
	handledLogin           int
	handledPressX          int
	lastRx                 time.Time
	kicked                 bool
	linuxKicked            bool
	sampled                map[string]bool
	ubiAttached            bool
	uartParams             map[string]any
}

// Collect runs the probe state machine until the wanted layers are collected
// or the timeout expires. It never sends anything the guard refuses.
func Collect(s *Session, o Options) Outcome {
	if o.SampleHead <= 0 {
		o.SampleHead = 4096
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.UBootSource == "" {
		o.UBootSource = "device"
	}
	ly := o.Layers
	c := &collector{s: s, o: o, sampled: map[string]bool{}}
	needEnv := ly.UBoot || ly.Flash || ly.DT || ly.Network || ly.GPIO
	c.wantUB = needEnv && !o.LinuxOnly
	c.wantLX = (ly.Linux || ly.Flash || ly.DT || ly.Network || ly.GPIO) && !o.NoLinux && !o.UBootOnly
	if ly.UBoot && !ly.Linux && !ly.Flash && !ly.DT && !ly.Network && !ly.GPIO {
		c.wantLX = false
	}
	if ly.Linux && !ly.UBoot && !ly.Flash && !ly.DT && !ly.Network && !ly.GPIO {
		c.wantUB = false
	}
	c.wantBR = ly.BootROM && o.BootROMHandshake
	if o.Unsafe {
		s.Info("%s", L("--unsafe принят, но probe mode остаётся строго read-only (guard не отключается).", "--unsafe accepted, but probe mode stays strictly read-only (the guard is not disabled)."))
		c.res.Notes = append(c.res.Notes, "--unsafe was given; probe mode ignores it and stays read-only")
	}
	c.loadSampled()
	c.run()
	c.writeBootROM()
	c.writeParams()
	c.res.Blocked = len(s.Blocked)
	c.res.UARTData = s.RxBytes() > 0 || c.res.UBoot || c.res.Linux
	b, _ := json.MarshalIndent(c.res, "", "  ")
	_ = s.WriteFile(fmt.Sprintf("runs/outcome-%s.json", s.start.Format("20060102-150405")), b)
	return c.res
}

func (c *collector) done() bool {
	ub := !c.wantUB || c.ubDone
	lx := !c.wantLX || c.lxDone
	br := !c.wantBR || c.brDone
	if c.o.FirstReachable && (c.ubDone || c.lxDone) {
		return br
	}
	return ub && lx && br
}

func (c *collector) run() {
	s := c.s
	if c.o.AtUBootPrompt != "" {
		c.res.UBoot = true
		u := NewUBoot(s, c.o.AtUBootPrompt, c.o.UBootSource)
		u.AllowUBIAttach = c.o.UBIAttach
		c.collectUBoot(u)
		c.ubDone = true
		c.wantLX = false
		return
	}
	s.Info(L("UART открыт (%s, 115200 8N1). Включите устройство (или оставьте работающим). Probe только читает.", "UART open (%s, 115200 8N1). Power the device on (or leave it running). The probe only reads."), s.Port.Name())
	deadline := s.now().Add(c.o.Timeout)
	c.lastRx = s.now()
	for s.now().Before(deadline) && !c.done() {
		d, err := s.Read(s.T.Poll)
		if err != nil {
			c.note(L("ошибка чтения UART: ", "UART read error: ") + err.Error())
			return
		}
		if len(d) > 0 {
			c.lastRx = s.now()
			c.kicked = false
		}
		c.step()
	}
	if !c.done() {
		c.note(fmt.Sprintf(L("тайм-аут %s: не все запрошенные слои собраны", "timeout %s reached before all requested layers were collected"), c.o.Timeout))
	}
}

func (c *collector) note(n string) {
	c.res.Notes = append(c.res.Notes, n)
	c.s.Info("%s", n)
}

// step reacts to what the device printed so far.
func (c *collector) step() {
	s := c.s
	if s.Seen("press_x") > 0 {
		c.res.BootROM = true
	}
	if s.Seen("xmodem_c") > 0 {
		c.res.BootROM = true
		c.res.BootROMXmodem = true
	}
	if c.wantBR && !c.brDone && s.Seen("press_x") > c.handledPressX {
		c.handledPressX = s.Seen("press_x")
		c.bootromHandshake()
		return
	}
	if !c.brDone && s.Seen("xmodem_c") > 0 && s.Seen("press_x") == 0 && c.wantBR {
		c.brDone = true
		c.note(L("устройство уже ждёт XMODEM (C); BootROM handshake засчитан без отправки x", "device is already waiting for XMODEM (C); BootROM handshake counted without sending x"))
	}
	if s.Seen("destructive_autoboot") > 0 && !c.noInterrupt {
		c.noInterrupt = true
		c.note(L("[WARN] загрузчик выполняет erase/write при автозагрузке; probe не вмешивается", "[WARN] the bootloader runs erase/write during autoboot; the probe does not interfere"))
	}
	// Keys go to a bootloader only after its U-Boot banner was seen: an
	// "autoboot"-looking line from an unknown loader is not enough.
	if c.wantUB && !c.ubDone && !c.noInterrupt && s.Seen("linux") == 0 && c.ubootEvidence() {
		c.interruptAutoboot()
		return
	}
	if s.Seen("console_activate") > c.handledActivate && c.wantLX && !c.lxDone {
		c.handledActivate = s.Seen("console_activate")
		_, _ = s.Drain(s.T.Quiet, 2*time.Second)
		_ = s.sendKeys("Enter (activate console)", []byte{'\r'})
		_, _ = s.Drain(s.T.Quiet, 3*time.Second)
		c.tryPrompt(false)
		return
	}
	if ClassifyPrompt(lastLine(s.Tail())) == PromptLogin && s.Seen("login") > c.handledLogin && c.wantLX && !c.lxDone && (c.linuxEvidence() || c.o.Wake) {
		c.handledLogin = s.Seen("login")
		_, _ = s.Drain(s.T.Quiet, 2*time.Second)
		c.login()
		return
	}
	quiet := s.now().Sub(c.lastRx)
	if c.wantLX && !c.lxDone && !c.linuxKicked && s.Seen("linux") > 0 && quiet > s.T.LinuxSettle {
		c.linuxKicked = true
		_ = s.sendKeys("Enter (look for a shell)", []byte{'\r'})
		_, _ = s.Drain(s.T.Quiet, 3*time.Second)
		c.tryPrompt(false)
		return
	}
	// A countdown is quiet between ticks; a key there would stop autoboot.
	last := lastLine(s.Tail())
	countdown := markerByID("autoboot").re.MatchString(last) || markerByID("bootmenu").re.MatchString(last)
	if !c.kicked && quiet > s.T.IdleKick && !countdown {
		c.kicked = true
		if !c.o.Wake {
			// Passive by default: nothing is sent to a device whose firmware
			// has not identified itself.
			if s.RxBytes() == 0 {
				s.Info("%s", L("С UART ничего не приходит. Включите устройство; проверьте GND/TX/RX (TX↔RX) и 115200. Если устройство уже работает и стоит в prompt — запустите с --wake.", "Nothing arrives on the UART. Power the device on; check GND/TX/RX (TX↔RX) and 115200 baud. If it is already running at a prompt, run with --wake."))
			} else {
				c.tryPrompt(true)
			}
			return
		}
		// --wake: one Ctrl-C to redraw a prompt of an already running device.
		_ = s.sendKeys("Ctrl-C (--wake: look for a prompt)", []byte{0x03})
		_, _ = s.Drain(s.T.Quiet, 3*time.Second)
		if !c.tryPrompt(true) && s.RxBytes() == 0 {
			s.Info("%s", L("С UART ничего не приходит. Проверьте GND/TX/RX (TX↔RX), 115200, и питание устройства.", "Nothing arrives on the UART. Check GND/TX/RX (TX↔RX), 115200 baud and device power."))
		}
	}
}

// knownUBootPromptRE are prompts that identify U-Boot on their own (for --wake
// on a device whose banner was not captured). Any other "xxx>" stays unconfirmed.
var knownUBootPromptRE = regexp.MustCompile(`^(?:=>|U-Boot ?>|[AE]N75[0-9]{2} ?>)$`)

// ubootEvidence: the device printed a U-Boot banner (and not vendor tcboot),
// or the U-Boot is UrsidoRescue's own RAM U-Boot.
func (c *collector) ubootEvidence() bool {
	if c.o.UBootSource == "ursido-ram-uboot" {
		return true
	}
	return c.s.Seen("uboot") > 0 && c.s.Seen("tcboot") == 0
}

// linuxEvidence: a Linux kernel boot or an OpenWrt console was seen.
func (c *collector) linuxEvidence() bool {
	return c.s.Seen("linux") > 0 || c.s.Seen("console_activate") > 0
}

func (c *collector) unconfirmedPrompt(line string) {
	if c.res.UnconfirmedPrompt == line {
		return
	}
	c.res.UnconfirmedPrompt = line
	c.note(fmt.Sprintf(L("prompt %q без подтверждения (нет баннера U-Boot/Linux) — команды не отправляются; используйте --wake только если уверены, что это U-Boot или Linux", "prompt %q is unconfirmed (no U-Boot/Linux banner) — no commands sent; use --wake only if you know it is U-Boot or Linux"), line))
}

// tryPrompt classifies the current last line and collects what it offers.
func (c *collector) tryPrompt(idle bool) bool {
	s := c.s
	line := lastLine(s.Tail())
	switch ClassifyPrompt(line) {
	case PromptUBoot:
		if !c.wantUB || c.ubDone {
			return true
		}
		if !c.ubootEvidence() && !(c.o.Wake && knownUBootPromptRE.MatchString(line)) {
			c.unconfirmedPrompt(line)
			return true
		}
		c.enterUBoot(line)
		return true
	case PromptOtherBootloader:
		if c.res.OtherBootloader == "" {
			c.res.OtherBootloader = line
			c.note(L("найден не-U-Boot загрузчик (", "non-U-Boot bootloader found (") + line + L("); команды ему не отправляются", "); no commands are sent to it"))
		}
		return true
	case PromptLinuxShell, PromptLogin, PromptPassword:
		if !c.wantLX || c.lxDone {
			return true
		}
		if !c.linuxEvidence() && !c.o.Wake {
			c.unconfirmedPrompt(line)
			return true
		}
		switch ClassifyPrompt(line) {
		case PromptLinuxShell:
			c.enterLinux()
		case PromptLogin:
			c.login()
		default:
			_ = s.sendKeys("Ctrl-C (leave password prompt)", []byte{0x03})
		}
		return true
	}
	_ = idle
	return false
}

func (c *collector) interruptAutoboot() {
	s := c.s
	s.Info("%s", L("Обнаружен U-Boot autoboot — прерываю (Ctrl-C/Esc, без Enter).", "U-Boot autoboot detected — interrupting (Ctrl-C/Esc, no Enter)."))
	end := s.now().Add(10 * time.Second)
	for s.now().Before(end) {
		keys := []byte{0x03}
		if s.Seen("bootmenu") > 0 {
			keys = append(keys, 0x1b)
		}
		if c.o.StopKey != "" {
			keys = append(keys, []byte(c.o.StopKey)...)
		}
		_ = s.sendKeys("autoboot interrupt", keys)
		d, _ := s.Read(s.T.InterruptGap)
		if len(d) > 0 {
			c.lastRx = s.now()
		}
		if s.Seen("linux") > 0 || s.Seen("starting_kernel") > 0 {
			c.note(L("автозагрузку прервать не удалось — устройство ушло в Linux", "could not interrupt autoboot — the device went on to Linux"))
			c.noInterrupt = true
			return
		}
		if ClassifyPrompt(lastLine(s.Tail())) == PromptUBoot {
			_, _ = s.Drain(s.T.PromptSettle, 4*time.Second)
			// Make the prompt the last thing on the line again.
			_ = s.sendKeys("Ctrl-C (redraw prompt)", []byte{0x03})
			_, _ = s.Drain(s.T.Quiet, 3*time.Second)
			if line := lastLine(s.Tail()); ClassifyPrompt(line) == PromptUBoot {
				c.enterUBoot(line)
				return
			}
		}
	}
	c.note(L("U-Boot prompt не получен после прерывания; попробуйте --stop-key", "no U-Boot prompt after interrupting; try --stop-key"))
	c.noInterrupt = true
}

func (c *collector) enterUBoot(prompt string) {
	u := NewUBoot(c.s, prompt, c.o.UBootSource)
	u.AllowUBIAttach = c.o.UBIAttach
	out, _, err := u.Exec("version", 0)
	if err != nil || !strings.Contains(out, "U-Boot") {
		c.note(fmt.Sprintf(L("prompt %q не подтверждён как U-Boot; команды не отправляются", "prompt %q not confirmed as U-Boot; no commands are sent"), prompt))
		c.noInterrupt = true
		return
	}
	c.res.UBoot = true
	c.collectUBoot(u)
	c.ubDone = true
	if c.wantLX && !c.lxDone {
		c.noInterrupt = true
		c.lastRx = c.s.now()
		c.kicked = true
		c.s.Info("%s", L("U-Boot собран. Для Linux-части ВЫКЛЮЧИТЕ и ВКЛЮЧИТЕ питание (Reset не держать). Прерывать загрузку больше не буду.", "U-Boot collected. For the Linux part, POWER-CYCLE the device (do not hold Reset). Boot will not be interrupted again."))
	}
}

func (c *collector) enterLinux() {
	l := NewLinux(c.s)
	out, rc, err := l.Exec("uname -a", 0)
	if err != nil || rc != 0 || !strings.Contains(out, "Linux") {
		c.note(L("shell prompt не подтверждён как Linux (uname -a)", "shell prompt not confirmed as Linux (uname -a)"))
		c.lxDone = true
		return
	}
	c.res.Linux = true
	_ = c.s.WriteFile("linux/uname.txt", []byte(out))
	c.collectLinux(l)
	c.lxDone = true
}

var loginNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

const stockCredentialRefreshDelay = 12 * time.Second

type linuxAssistResult struct {
	plan LinuxLoginPlan
	err  error
}

func (c *collector) runLinuxAssist(provision bool) (LinuxLoginPlan, error) {
	if c.o.LinuxLoginAssist == nil {
		return LinuxLoginPlan{}, errors.New("stock LAN login assist is unavailable")
	}
	ch := make(chan linuxAssistResult, 1)
	go func() {
		p, err := c.o.LinuxLoginAssist(provision)
		ch <- linuxAssistResult{plan: p, err: err}
	}()
	for {
		select {
		case r := <-ch:
			return r.plan, r.err
		default:
			d, err := c.s.Read(c.s.T.Poll)
			if err != nil {
				return LinuxLoginPlan{}, err
			}
			if len(d) > 0 {
				c.lastRx = c.s.now()
			}
		}
	}
}

func (c *collector) waitWithUART(delay time.Duration) error {
	end := c.s.now().Add(delay)
	for c.s.now().Before(end) {
		d, err := c.s.Read(c.s.T.Poll)
		if err != nil {
			return err
		}
		if len(d) > 0 {
			c.lastRx = c.s.now()
		}
	}
	return nil
}

func (c *collector) waitAuthPrompt(timeout time.Duration) PromptKind {
	end := c.s.now().Add(timeout)
	for c.s.now().Before(end) {
		d, err := c.s.Read(c.s.T.Poll)
		if err != nil {
			return PromptNone
		}
		if len(d) == 0 {
			continue
		}
		switch k := ClassifyPrompt(lastLine(c.s.Tail())); k {
		case PromptLogin, PromptPassword, PromptLinuxShell:
			return k
		}
	}
	return PromptNone
}

func (c *collector) ensureLoginPrompt() bool {
	if ClassifyPrompt(lastLine(c.s.Tail())) == PromptLogin {
		return true
	}
	_ = c.s.sendKeys("Enter (redraw stock login)", []byte{'\r'})
	return c.waitAuthPrompt(8*time.Second) == PromptLogin
}

func (c *collector) tryUARTLogin(user, pass, label string) bool {
	if !loginNameRE.MatchString(user) || strings.ContainsAny(pass, "\r\n") {
		return false
	}
	if !c.ensureLoginPrompt() {
		return false
	}
	_ = c.s.sendKeys(label+" login name", append([]byte(user), '\r'))
	k := c.waitAuthPrompt(10 * time.Second)
	if k == PromptPassword {
		_ = c.s.sendKeys(label+" password (masked)", append([]byte(pass), '\r'))
		k = c.waitAuthPrompt(12 * time.Second)
	}
	c.s.transcript(TranscriptEntry{
		Transport: "linux",
		Command:   "login " + user + " (password masked)",
		Result:    "prompt-kind: " + k.String(),
	})
	return k == PromptLinuxShell
}

func (c *collector) currentUID() (int, bool) {
	l := NewLinux(c.s)
	out, rc, err := l.Exec("id -u", 10*time.Second)
	if err != nil || rc != 0 {
		return -1, false
	}
	for _, f := range strings.Fields(out) {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil {
			return n, true
		}
	}
	return -1, false
}

func (c *collector) tryUARTSU(plan LinuxLoginPlan) bool {
	if !loginNameRE.MatchString(plan.RootUser) || plan.RootPassword == "" ||
		strings.ContainsAny(plan.RootPassword, "\r\n") {
		return false
	}
	if err := c.s.sendAuthLine("su " + plan.RootUser); err != nil {
		return false
	}
	k := c.waitAuthPrompt(8 * time.Second)
	if k == PromptPassword {
		_ = c.s.sendKeys("su password (masked)", append([]byte(plan.RootPassword), '\r'))
		k = c.waitAuthPrompt(10 * time.Second)
	}
	c.s.transcript(TranscriptEntry{
		Transport: "linux",
		Command:   "su " + plan.RootUser + " (password masked)",
		Result:    "prompt-kind: " + k.String(),
	})
	if k != PromptLinuxShell {
		return false
	}
	uid, ok := c.currentUID()
	return ok && uid == 0
}

func (c *collector) leaveLinuxShell() {
	if ClassifyPrompt(lastLine(c.s.Tail())) != PromptLinuxShell {
		return
	}
	if err := c.s.sendAuthLine("exit"); err != nil {
		return
	}
	_ = c.waitAuthPrompt(8 * time.Second)
}

func (c *collector) tryStockUARTPlan(plan LinuxLoginPlan) (root bool, shell bool) {
	if plan.RootUser != "" && plan.RootPassword != "" {
		if c.tryUARTLogin(plan.RootUser, plan.RootPassword, "stock UID0") {
			if uid, ok := c.currentUID(); ok && uid == 0 {
				return true, true
			}
			c.leaveLinuxShell()
		}
	}
	if plan.LoginUser == "" || plan.LoginPassword == "" {
		return false, false
	}
	if !c.tryUARTLogin(plan.LoginUser, plan.LoginPassword, "stock") {
		return false, false
	}
	if uid, ok := c.currentUID(); ok && uid == 0 {
		return true, true
	}
	if c.tryUARTSU(plan) {
		return true, true
	}
	return false, true
}

// tryStockUARTWithRefresh performs one bounded second credential read after a
// failed login. Stock init on Nokia MD/MF can rotate service passwords after
// the Web UI is already reachable, so "Web is up" is not proof that the first
// credentials are final.
func (c *collector) tryStockUARTWithRefresh(plan LinuxLoginPlan) (LinuxLoginPlan, bool, bool) {
	root, shell := c.tryStockUARTPlan(plan)
	if root {
		return plan, true, shell
	}

	c.note(fmt.Sprintf(L(
		"[STOCK] UID 0 не подтверждён; продолжаю читать UART %s и один раз перечитаю stock-реквизиты (init может менять пароли поздно).",
		"[STOCK] UID 0 not confirmed; keeping UART drained for %s, then re-reading stock credentials once (init may rotate passwords late).",
	), stockCredentialRefreshDelay))
	if err := c.waitWithUART(stockCredentialRefreshDelay); err != nil {
		c.note(L("[WARN] ошибка UART во время ожидания обновления реквизитов: ", "[WARN] UART error while waiting to refresh credentials: ") + err.Error())
		return plan, false, shell
	}
	refreshed, err := c.runLinuxAssist(false)
	if err != nil {
		c.note(L("[WARN] повторное чтение stock-реквизитов не удалось: ", "[WARN] stock credential re-read failed: ") + err.Error())
		return plan, false, shell
	}
	plan = refreshed
	if shell {
		if c.tryUARTSU(plan) {
			return plan, true, true
		}
		c.leaveLinuxShell()
	}
	root, shell = c.tryStockUARTPlan(plan)
	return plan, root, shell
}

func (c *collector) automaticStockLogin() bool {
	if c.o.LinuxLoginAssist == nil {
		return false
	}
	c.note(L("[STOCK] Найден login stock Linux; получаю актуальные реквизиты через 192.168.1.1. Пароли остаются только в памяти.", "[STOCK] Stock Linux login detected; obtaining current credentials through 192.168.1.1. Passwords stay in memory only."))
	plan, err := c.runLinuxAssist(false)
	if err != nil {
		c.note(L("[WARN] stock LAN assist не сработал: ", "[WARN] stock LAN assist failed: ") + err.Error())
		return false
	}
	c.res.StockLANAssist = true
	c.note(fmt.Sprintf(L("[STOCK] Web-доступ подтверждён: %s; пробую UART login и UID 0.", "[STOCK] Web access confirmed: %s; trying UART login and UID 0."), plan.Model))
	plan, root, shell := c.tryStockUARTWithRefresh(plan)
	if root {
		c.res.LinuxUID0 = true
		c.note(L("[OK] UART stock shell: UID 0 подтверждён.", "[OK] UART stock shell: UID 0 confirmed."))
		c.enterLinux()
		return true
	}

	if !plan.FTPEnabled && c.o.Ask != nil {
		answer := strings.ToLower(strings.TrimSpace(c.o.Ask(L(
			"UID 0 через stock UART не подтверждён. Включить FTP через штатную Web UI, перечитать user_ftp и повторить? Это изменит stock-настройку, raw MTD/firmware не пишется. [y/N]: ",
			"UID 0 was not confirmed over stock UART. Enable FTP through the stock Web UI, refresh user_ftp and retry? This changes a stock setting; no raw MTD/firmware write is performed. [y/N]: ",
		))))
		if answer == "y" || answer == "yes" || answer == "д" || answer == "да" {
			refreshed, e := c.runLinuxAssist(true)
			if e != nil {
				c.note(L("[WARN] не удалось включить/перечитать stock FTP: ", "[WARN] could not enable/refresh stock FTP: ") + e.Error())
			} else {
				plan = refreshed
				c.res.StockProvisioned = refreshed.SettingsChanged
				if refreshed.SettingsChanged {
					c.note(L("[ВНИМАНИЕ] FTP включён штатной Web UI и останется включён после probe.", "[NOTICE] FTP was enabled through the stock Web UI and remains enabled after the probe."))
				}
				if shell && c.tryUARTSU(plan) {
					c.res.LinuxUID0 = true
					c.note(L("[OK] UART stock shell: UID 0 подтверждён после FTP provisioning.", "[OK] UART stock shell: UID 0 confirmed after FTP provisioning."))
					c.enterLinux()
					return true
				}
				if shell {
					c.leaveLinuxShell()
				}
				root, shell = c.tryStockUARTPlan(plan)
				if root {
					c.res.LinuxUID0 = true
					c.note(L("[OK] UART stock shell: UID 0 подтверждён после FTP provisioning.", "[OK] UART stock shell: UID 0 confirmed after FTP provisioning."))
					c.enterLinux()
					return true
				}
			}
		}
	}
	if shell {
		c.note(L("[WARN] UART login получен без UID 0; продолжаю доступную read-only Linux-диагностику с текущими правами.", "[WARN] UART login succeeded without UID 0; continuing the available read-only Linux diagnostics with current privileges."))
		c.enterLinux()
		return true
	}
	return false
}

func (c *collector) login() {
	if c.o.LinuxUser == "" && c.automaticStockLogin() {
		return
	}

	user, pass := c.o.LinuxUser, c.o.LinuxPassword
	if user == "" && c.o.Ask != nil {
		user = strings.TrimSpace(c.o.Ask(L("Linux требует вход. Логин (пусто = пропустить Linux-часть): ", "Linux asks for a login. User (empty = skip the Linux part): ")))
		if user != "" {
			pass = c.o.Ask(L("Пароль (пусто = без пароля): ", "Password (empty = none): "))
		}
	}
	if user == "" || !loginNameRE.MatchString(user) {
		c.res.LinuxLoginBlock = true
		c.note(L("Linux login prompt: автоматический stock LAN assist и ручные учётные данные недоступны — Linux-часть пропущена", "Linux login prompt: automatic stock LAN assist and manual credentials are unavailable — Linux part skipped"))
		c.lxDone = true
		return
	}
	if strings.ContainsAny(pass, "\r\n") {
		c.note(L("пароль содержит перевод строки — пропускаю вход", "password contains a newline — login skipped"))
		c.lxDone = true
		return
	}
	if c.tryUARTLogin(user, pass, "manual") {
		if uid, ok := c.currentUID(); ok && uid == 0 {
			c.res.LinuxUID0 = true
		}
		c.enterLinux()
		return
	}
	c.res.LinuxLoginBlock = true
	c.note(L("вход в Linux не удался — Linux-часть пропущена", "Linux login failed — Linux part skipped"))
	c.lxDone = true
}

func (c *collector) bootromHandshake() {
	s := c.s
	before := s.Seen("xmodem_c")
	s.Info("%s", L("BootROM 'Press x' — отправляю x и жду XMODEM 'C' (в flash ничего не пишется).", "BootROM 'Press x' — sending x and waiting for XMODEM 'C' (nothing is written to flash)."))
	_ = s.sendKeys("x (BootROM)", []byte("x"))
	end := s.now().Add(s.T.HandshakeWait)
	for s.now().Before(end) && s.Seen("xmodem_c") <= before {
		_, _ = s.Read(s.T.Poll)
	}
	ok := s.Seen("xmodem_c") > before
	c.brDone = true
	c.res.BootROM = true
	if ok {
		c.res.BootROMXmodem = true
		c.note(L("BootROM XMODEM receiver подтверждён ('C'). Устройство ждёт файл: ВЫКЛЮЧИТЕ и ВКЛЮЧИТЕ питание без Reset, чтобы продолжить.", "BootROM XMODEM receiver confirmed ('C'). The device waits for a file: POWER-CYCLE it without Reset to continue."))
		c.noInterrupt = false
	} else {
		c.note(L("после x не пришёл 'C' — XMODEM handshake не подтверждён", "no 'C' after x — XMODEM handshake not confirmed"))
	}
	c.lastRx = s.now()
}

func (c *collector) writeBootROM() {
	s := c.s
	path := filepath.Join(s.Dir, "bootrom", "bootrom.json")
	if !c.res.BootROM {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	info := map[string]any{
		"detected":        c.res.BootROM,
		"press_x":         s.Seen("press_x") > 0,
		"xmodem":          c.res.BootROMXmodem,
		"handshake_sent":  c.handledPressX > 0 && c.wantBR,
		"entry_sequence":  "",
		"receiver_char":   "",
		"c_gap_ms":        s.CGaps(),
		"preloader_ok":    nil,
		"fip_ok":          nil,
		"flash_written":   false,
		"collected_at":    s.now().Format(time.RFC3339),
		"marker_events":   filterEvents(s.Events(), "press_x", "xmodem_c", "dramc", "bl2", "bl31"),
		"handshake_mode":  map[bool]string{true: "active (x sent)", false: "passive (observed only)"}[c.wantBR],
		"reset_hint":      "on Nokia XG-040G boards BootROM prints 'Press x' only when Reset is held at power-on",
		"transport_speed": 115200,
	}
	if s.Seen("press_x") > 0 {
		info["entry_sequence"] = "press-x"
	}
	if c.res.BootROMXmodem {
		info["receiver_char"] = "C"
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	_ = s.WriteFile("bootrom/bootrom.json", b)
}

func filterEvents(ev []Event, ids ...string) []Event {
	var out []Event
	for _, e := range ev {
		for _, id := range ids {
			if e.Marker == id {
				out = append(out, e)
			}
		}
	}
	return out
}

func (c *collector) writeParams() {
	s := c.s
	p := map[string]any{
		"port": s.Port.Name(), "baud": 115200, "data_bits": 8, "parity": "none",
		"stop_bits": 1, "flow_control": "none", "rx_bytes": s.RxBytes(),
		"non_text_ratio": fmt.Sprintf("%.3f", s.BinaryRatio()),
	}
	if s.RxBytes() > 256 && s.BinaryRatio() > 0.3 {
		p["warning"] = "many non-text bytes: baud-rate mismatch is likely (only 115200 is supported)"
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	_ = s.WriteFile("uart/params.json", b)
}

// ---------------- U-Boot collection ----------------

type cmdStatus struct {
	Command string `json:"command"`
	File    string `json:"file"`
	RC      int    `json:"rc"`
	Error   string `json:"error,omitempty"`
}

func (c *collector) ubRun(u *UBoot, cmd, file string, timeout time.Duration) (string, bool) {
	out, rc, err := u.Exec(cmd, timeout)
	if errors.Is(err, ErrBlocked) {
		return "", false
	}
	st := cmdStatus{Command: cmd, File: file, RC: rc}
	if err != nil {
		st.Error = err.Error()
	}
	if file != "" {
		_ = c.s.WriteFile(file, []byte(out))
		c.appendStatus("uboot/_status.jsonl", st)
	}
	return out, err == nil && rc <= 0 && !looksFailed(out)
}

func (c *collector) appendStatus(rel string, st cmdStatus) {
	p := filepath.Join(c.s.Dir, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(st)
	_, _ = f.Write(append(b, '\n'))
}

func (c *collector) collectUBoot(u *UBoot) {
	s, ly := c.s, c.o.Layers
	s.Info(L("U-Boot probe (%s prompt %q) — только read-only команды.", "U-Boot probe (%s prompt %q) — read-only commands only."), u.Source, u.Prompt)
	u.DetectHush()
	meta, _ := json.MarshalIndent(map[string]any{"prompt": u.Prompt, "hush": u.Hush, "source": u.Source}, "", "  ")
	_ = s.WriteFile("uboot/session.json", meta)
	c.ubRun(u, "version", "uboot/version.txt", 0)
	help, _ := c.ubRun(u, "help", "uboot/help.txt", 0)
	caps := ParseHelp(help)
	has := func(name string) bool {
		if len(caps) == 0 {
			return true
		}
		_, ok := caps[name]
		return ok
	}
	bdText, _ := c.ubRun(u, "bdinfo", "uboot/bdinfo.txt", 0)
	envText, _ := c.ubRun(u, "printenv", "uboot/env.txt", 0)
	bd, env := ParseBDInfo(bdText), ParsePrintenv(envText)
	if has("dm") && (ly.UBoot || ly.Network || ly.GPIO) {
		c.ubRun(u, "dm tree", "uboot/dm-tree.txt", 0)
	}
	if has("coninfo") && ly.UBoot {
		c.ubRun(u, "coninfo", "uboot/coninfo.txt", 0)
	}

	var devs []MTDDevice
	if ly.Flash || ly.UBoot {
		if has("mtd") {
			mt, _ := c.ubRun(u, "mtd list", "uboot/mtd-list.txt", s.T.CommandDef*2)
			devs = ParseUBootMTDList(mt)
			for _, d := range devs {
				if strings.Contains(strings.ToLower(d.Type), "nand") && mtdNameArg.MatchString(d.Name) {
					c.ubRun(u, "mtd bad "+d.Name, "uboot/mtd-bad-"+safeName(d.Name)+".txt", s.T.CommandLong)
				}
			}
		}
		if has("nand") {
			c.ubRun(u, "nand info", "uboot/nand-info.txt", 0)
		}
		if has("spi-nand") {
			c.ubRun(u, "spi-nand info", "uboot/spi-nand-info.txt", 0)
		}
		if has("mmc") {
			c.ubRun(u, "mmc list", "uboot/mmc-list.txt", 0)
			c.ubRun(u, "mmc info", "uboot/mmc-info.txt", 0)
		}
		if has("sf") && len(devs) == 0 {
			c.ubRun(u, "sf probe", "uboot/sf-probe.txt", 0)
		}
	}
	addr, window := ramWindow(bd, env, c.o.RAMHint, 4<<20)
	winNote := map[string]any{"usable": window, "addr": fmt.Sprintf("0x%x", addr), "banks": bd.DRAMBanks, "relocaddr": fmt.Sprintf("0x%x", bd.RelocAddr)}
	wb, _ := json.MarshalIndent(winNote, "", "  ")
	_ = s.WriteFile("uboot/ram-window.json", wb)
	if !window {
		c.note(L("безопасное RAM-окно не подтверждено (loadaddr/bdinfo) — чтения через RAM (ubi read, mtd read, hash) пропущены", "safe RAM window not confirmed (loadaddr/bdinfo) — reads through RAM (ubi read, mtd read, hash) skipped"))
	}

	if ly.Flash && has("mtd") && len(devs) > 0 {
		mtdHelp, _ := c.ubRun(u, "help mtd", "uboot/help-mtd.txt", 0)
		if strings.Contains(mtdHelp, "mtd dump") || len(caps) == 0 {
			c.sampleUBootMTD(u, devs)
		} else {
			c.note(L("в этом U-Boot нет 'mtd dump' — сэмплы разделов через U-Boot не сняты", "this U-Boot has no 'mtd dump' — no partition samples taken through U-Boot"))
		}
		if c.o.UBIAttach && has("ubi") {
			if !window {
				addr = 0
			}
			c.ubootUBI(u, devs, addr, has)
		}
		if window {
			c.hashBootParts(u, devs, addr, has)
		}
	}
	if ly.DT {
		c.ubootFDT(u, bd, env, has)
	}
	if ly.Network {
		if has("mii") {
			c.ubRun(u, "mii info", "network/uboot-mii-info.txt", 0)
		}
		if has("mdio") {
			c.ubRun(u, "mdio list", "network/uboot-mdio-list.txt", 0)
		}
	}
	if ly.GPIO {
		if has("gpio") {
			c.ubRun(u, "gpio status -a", "gpio/uboot-gpio-status.txt", 0)
		}
		if has("pinmux") {
			c.ubRun(u, "pinmux status -a", "gpio/uboot-pinmux-status.txt", 0)
		}
	}
	if c.ubiAttached {
		s.Info("%s", L("U-Boot probe завершён. ВНИМАНИЕ: выполнялся ubi part — UBI мог записать во flash (advanced-режим).", "U-Boot probe finished. WARNING: ubi part was run — UBI may have written to flash (advanced mode)."))
	} else {
		s.Info("%s", L("U-Boot probe завершён; команд записи не отправлялось.", "U-Boot probe finished; no write commands were sent."))
	}
}

func safeName(s string) string {
	return regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(s, "_")
}

// ramWindow picks a RAM address for read-into-RAM commands only when it is
// inside a DRAM bank and at least 32 MiB below U-Boot's relocation address.
func ramWindow(bd BDInfo, env map[string]string, hint, need uint64) (uint64, bool) {
	var cands []uint64
	for _, k := range []string{"loadaddr", "kernel_addr_r"} {
		// U-Boot reads these as hex with or without 0x.
		if v, ok := parseHexPlain(strings.TrimPrefix(strings.ToLower(env[k]), "0x")); ok && env[k] != "" {
			cands = append(cands, v)
		}
	}
	if hint != 0 {
		cands = append(cands, hint)
	}
	for _, a := range cands {
		for _, bank := range bd.DRAMBanks {
			start, end := bank[0], bank[0]+bank[1]
			limit := end
			if bd.RelocAddr > start && bd.RelocAddr < end {
				if bd.RelocAddr < start+(32<<20) {
					continue
				}
				limit = bd.RelocAddr - (32 << 20)
			}
			if a >= start && a+need <= limit {
				return a, true
			}
		}
	}
	return 0, false
}

func parseHexPlain(s string) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 16, 64)
	return v, err == nil
}

// SampleRecord describes one raw read-only sample.
type SampleRecord struct {
	MTD       string  `json:"mtd"`
	Source    string  `json:"source"`
	Offset    uint64  `json:"offset"`
	AbsOffset *uint64 `json:"abs_offset,omitempty"`
	Length    int     `json:"length"`
	File      string  `json:"file"`
	SHA256    string  `json:"sha256"`
	Kind      string  `json:"kind"` // head|tail|volume-head
}

func (c *collector) loadSampled() {
	for _, r := range readSamples(c.s.Dir) {
		c.sampled[r.MTD+"|"+r.Kind] = true
	}
}

func readSamples(dir string) []SampleRecord {
	var out []SampleRecord
	b, err := os.ReadFile(filepath.Join(dir, "flash", "samples", "index.jsonl"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(b), "\n") {
		var r SampleRecord
		if json.Unmarshal([]byte(line), &r) == nil && r.File != "" {
			out = append(out, r)
		}
	}
	return out
}

func (c *collector) saveSample(r SampleRecord, data []byte) {
	sum := sha256.Sum256(data)
	r.SHA256 = hex.EncodeToString(sum[:])
	r.Length = len(data)
	r.File = fmt.Sprintf("flash/samples/%s-%s-%s-0x%x.bin", r.Source, safeName(r.MTD), r.Kind, r.Offset)
	_ = c.s.WriteFile(r.File, data)
	p := filepath.Join(c.s.Dir, "flash", "samples", "index.jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		b, _ := json.Marshal(r)
		_, _ = f.Write(append(b, '\n'))
		f.Close()
	}
	c.sampled[r.MTD+"|"+r.Kind] = true
}

type partRange struct {
	name       string
	start, end uint64
	minIO      uint64
	leaf       bool
}

func leafParts(devs []MTDDevice) []partRange {
	var out []partRange
	for _, d := range devs {
		if len(d.Parts) == 0 {
			out = append(out, partRange{name: d.Name, start: d.Start, end: d.End, minIO: d.MinIO, leaf: true})
			continue
		}
		for i, p := range d.Parts {
			leaf := i+1 >= len(d.Parts) || d.Parts[i+1].Level <= p.Level
			if leaf {
				out = append(out, partRange{name: p.Name, start: p.Start, end: p.End, minIO: d.MinIO, leaf: true})
			}
		}
	}
	return out
}

func (c *collector) sampleUBootMTD(u *UBoot, devs []MTDDevice) {
	parts := leafParts(devs)
	if len(parts) > 32 {
		parts = parts[:32]
	}
	for _, p := range parts {
		if !mtdNameArg.MatchString(p.name) {
			c.note(L("имя раздела ", "partition name ") + strconv.Quote(p.name) + L(" не проходит allowlist — сэмпл пропущен", " fails the allowlist — sample skipped"))
			continue
		}
		size := p.end - p.start
		head := uint64(c.o.SampleHead)
		if head > size {
			head = size
		}
		if head == 0 || c.sampled[p.name+"|head"] {
			continue
		}
		c.dumpMTD(u, p, 0, head, "head")
		if size <= 8<<20 && size > head && !c.sampled[p.name+"|tail"] {
			tail := uint64(4096)
			if p.minIO > tail {
				tail = p.minIO
			}
			if tail < size {
				c.dumpMTD(u, p, size-tail, tail, "tail")
			}
		}
	}
}

func (c *collector) dumpMTD(u *UBoot, p partRange, off, length uint64, kind string) {
	cmd := fmt.Sprintf("mtd dump %s 0x%x 0x%x", p.name, off, length)
	out, ok := c.ubRun(u, cmd, "", c.s.T.CommandLong)
	if !ok {
		c.note(L("mtd dump не удался: ", "mtd dump failed: ") + cmd)
		return
	}
	data, err := ParseHexDump(out, off, int(length))
	if err != nil {
		c.note(fmt.Sprintf(L("разбор mtd dump %s: %v", "parsing mtd dump %s: %v"), p.name, err))
		return
	}
	abs := p.start + off
	c.saveSample(SampleRecord{MTD: p.name, Source: "uboot", Offset: off, AbsOffset: &abs, Kind: kind}, data)
}

func (c *collector) ubootUBI(u *UBoot, devs []MTDDevice, addr uint64, has func(string) bool) {
	var ubiParts []string
	for _, r := range readSamples(c.s.Dir) {
		if r.Kind != "head" || r.Source != "uboot" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.s.Dir, filepath.FromSlash(r.File)))
		if err == nil && DetectSignature(b).Format == "ubi" {
			ubiParts = append(ubiParts, r.MTD)
		}
	}
	if len(ubiParts) == 0 {
		// Attaching a partition without a UBI header could make U-Boot write
		// a fresh layout, so no attach without evidence.
		c.note(L("разделы с UBI-заголовком не подтверждены сэмплом — ubi part не выполняется", "no partition with a UBI header confirmed by a sample — ubi part not run"))
		return
	}
	sort.Strings(ubiParts)
	for _, name := range ubiParts {
		if !mtdNameArg.MatchString(name) {
			continue
		}
		c.note(L("ADVANCED: ubi part "+name+" — UBI attach может записать во flash", "ADVANCED: ubi part "+name+" — UBI attach may write to flash"))
		c.ubiAttached = true
		c.res.UBIAttached = true
		if _, ok := c.ubRun(u, "ubi part "+name, "ubi/uboot-"+safeName(name)+"-attach.txt", c.s.T.CommandLong); !ok {
			continue
		}
		info, _ := c.ubRun(u, "ubi info", "ubi/uboot-"+safeName(name)+"-info.txt", 0)
		layout, _ := c.ubRun(u, "ubi info layout", "ubi/uboot-"+safeName(name)+"-layout.txt", c.s.T.CommandDef*2)
		dev := ParseUBootUBIInfo(info, layout)
		if addr == 0 || !has("md") {
			continue
		}
		for _, v := range dev.Volumes {
			if v.Name == "" || !volNameArg.MatchString(v.Name) {
				continue
			}
			key := "ubi:" + v.Name
			if !c.sampled[key+"|volume-head"] {
				n := uint64(4096)
				if v.UsedBytes > 0 && v.UsedBytes < n {
					n = v.UsedBytes
				}
				if _, ok := c.ubRun(u, fmt.Sprintf("ubi read 0x%x %s 0x%x", addr, v.Name, n), "", c.s.T.CommandLong); ok {
					if out, ok := c.ubRun(u, fmt.Sprintf("md.b 0x%x 0x%x", addr, n), "", c.s.T.CommandLong); ok {
						if data, err := ParseHexDump(out, addr, int(n)); err == nil {
							c.saveSample(SampleRecord{MTD: key, Source: "uboot", Offset: 0, Kind: "volume-head"}, data)
						}
					}
				}
			}
			if bootVolume(v.Name) && v.UsedBytes > 0 && v.UsedBytes <= 4<<20 {
				if _, ok := c.ubRun(u, fmt.Sprintf("ubi read 0x%x %s 0x%x", addr, v.Name, v.UsedBytes), "", c.s.T.CommandLong); ok {
					c.hashRAM(u, key, addr, v.UsedBytes, has)
				}
			}
		}
	}
}

var bootNameRE = regexp.MustCompile(`(?i)^(?:fip|bl2|bl31|preloader|u-?boot[0-9]?|bootloader|boot|atf|tfa)$`)

func bootVolume(name string) bool { return bootNameRE.MatchString(name) }

func (c *collector) hashBootParts(u *UBoot, devs []MTDDevice, addr uint64, has func(string) bool) {
	for _, p := range leafParts(devs) {
		size := p.end - p.start
		if !bootVolume(p.name) || !mtdNameArg.MatchString(p.name) || size == 0 || size > 4<<20 {
			continue
		}
		if _, ok := c.ubRun(u, fmt.Sprintf("mtd read %s 0x%x 0x0 0x%x", p.name, addr, size), "", c.s.T.CommandLong); ok {
			c.hashRAM(u, p.name, addr, size, has)
		}
	}
}

var hashOutRE = regexp.MustCompile(`==>\s*([0-9a-fA-F]{8,64})`)

func (c *collector) hashRAM(u *UBoot, what string, addr, size uint64, has func(string) bool) {
	rec := map[string]any{"object": what, "size": size, "source": "uboot"}
	if has("hash") {
		if out, ok := c.ubRun(u, fmt.Sprintf("hash sha256 0x%x 0x%x", addr, size), "", c.s.T.CommandLong); ok {
			if m := hashOutRE.FindStringSubmatch(out); m != nil && len(m[1]) == 64 {
				rec["sha256"] = strings.ToLower(m[1])
			}
		}
	}
	if rec["sha256"] == nil && has("crc32") {
		if out, ok := c.ubRun(u, fmt.Sprintf("crc32 0x%x 0x%x", addr, size), "", c.s.T.CommandLong); ok {
			if m := hashOutRE.FindStringSubmatch(out); m != nil {
				rec["crc32"] = strings.ToLower(m[1])
			}
		}
	}
	if rec["sha256"] == nil && rec["crc32"] == nil {
		return
	}
	p := filepath.Join(c.s.Dir, "flash", "hashes.jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		b, _ := json.Marshal(rec)
		_, _ = f.Write(append(b, '\n'))
		f.Close()
	}
}

func (c *collector) ubootFDT(u *UBoot, bd BDInfo, env map[string]string, has func(string) bool) {
	if has("fdt") {
		c.ubRun(u, "fdt addr", "uboot/fdt-addr.txt", 0)
	}
	if !has("md") && !has("md.b") {
		return
	}
	var addr uint64
	if v, ok := parseHexPlain(strings.TrimPrefix(strings.ToLower(env["fdtcontroladdr"]), "0x")); ok && env["fdtcontroladdr"] != "" {
		addr = v
	} else if bd.FDTBlob != 0 {
		addr = bd.FDTBlob
	}
	if addr == 0 {
		c.note(L("адрес control DTB U-Boot неизвестен (fdtcontroladdr/fdt_blob)", "address of U-Boot's control DTB unknown (fdtcontroladdr/fdt_blob)"))
		return
	}
	hdr, ok := c.ubRun(u, fmt.Sprintf("md.b 0x%x 0x40", addr), "", 0)
	if !ok {
		return
	}
	h, err := ParseHexDump(hdr, addr, 0x40)
	if err != nil {
		return
	}
	total := FDTTotalSize(h)
	if total < 0x40 || total > 1<<20 {
		c.note(fmt.Sprintf(L("по адресу 0x%x нет корректного DTB", "no valid DTB at 0x%x"), addr))
		return
	}
	out, ok := c.ubRun(u, fmt.Sprintf("md.b 0x%x 0x%x", addr, total), "", c.s.T.CommandLong)
	if !ok {
		return
	}
	blob, err := ParseHexDump(out, addr, int(total))
	if err != nil {
		c.note(L("разбор DTB dump: ", "parsing DTB dump: ") + err.Error())
		return
	}
	_ = c.s.WriteFile("dt/fdt-uboot.dtb", blob)
}

// ---------------- Linux collection ----------------

func (c *collector) lxRun(l *Linux, cmd, file string, timeout time.Duration) (string, bool) {
	out, rc, err := l.Exec(cmd, timeout)
	if errors.Is(err, ErrBlocked) {
		return "", false
	}
	st := cmdStatus{Command: cmd, File: file, RC: rc}
	if err != nil {
		st.Error = err.Error()
	}
	if file != "" {
		_ = c.s.WriteFile(file, []byte(out))
		c.appendStatus("linux/_status.jsonl", st)
	}
	return out, err == nil && rc == 0
}

// readHexFromLinux reads a file or a dd pipe through the first available hex tool.
func (c *collector) readHexFromLinux(l *Linux, src string, pipe bool) ([]byte, bool) {
	tools := []string{"od -An -v -tx1", "hexdump -C", "xxd -p"}
	for _, t := range tools {
		cmd := t + " " + src
		if pipe {
			cmd = src + " | " + t
		}
		out, ok := c.lxRun(l, cmd, "", c.s.T.CommandLong)
		if !ok {
			continue
		}
		if b := ParseLinuxHex(out); len(b) > 0 {
			return b, true
		}
	}
	return nil, false
}

func (c *collector) collectLinux(l *Linux) {
	s, ly := c.s, c.o.Layers
	s.Info("%s", L("Linux probe — только read-only команды.", "Linux probe — read-only commands only."))
	if ly.Linux || ly.UBoot {
		c.lxRun(l, "cat /proc/cpuinfo", "linux/cpuinfo.txt", 0)
		c.lxRun(l, "cat /proc/cmdline", "linux/cmdline.txt", 0)
		c.lxRun(l, "cat /proc/meminfo", "linux/meminfo.txt", 0)
		c.lxRun(l, "mount", "linux/mount.txt", 0)
		c.lxRun(l, "df", "linux/df.txt", 0)
		c.lxRun(l, "lsblk", "linux/lsblk.txt", 0)
		c.lxRun(l, "fw_printenv", "linux/fw_printenv.txt", 0)
		c.lxRun(l, "cat /etc/openwrt_release", "linux/openwrt_release.txt", 0)
		c.lxRun(l, "cat /etc/os-release", "linux/os-release.txt", 0)
		c.lxRun(l, "grep -H . /tmp/sysinfo/model /tmp/sysinfo/board_name", "linux/sysinfo.txt", 0)
		c.lxRun(l, "cat /etc/board.json", "linux/board.json", 0)
	}
	c.lxRun(l, "dmesg", "linux/dmesg.txt", s.T.CommandLong)
	if ly.Flash || ly.Linux {
		mtdText, _ := c.lxRun(l, "cat /proc/mtd", "linux/proc-mtd.txt", 0)
		c.lxRun(l, "grep -H . /sys/class/mtd/mtd*/name /sys/class/mtd/mtd*/type /sys/class/mtd/mtd*/size /sys/class/mtd/mtd*/erasesize /sys/class/mtd/mtd*/writesize /sys/class/mtd/mtd*/oobsize /sys/class/mtd/mtd*/subpagesize /sys/class/mtd/mtd*/offset", "flash/sys-class-mtd.txt", 0)
		c.lxRun(l, "ubinfo -a", "ubi/ubinfo.txt", 0)
		if ly.Flash {
			c.sampleLinuxMTD(l, ParseProcMTD(mtdText))
		}
	}
	if ly.DT {
		c.lxRun(l, "cat /sys/firmware/devicetree/base/model", "dt/linux-model.txt", 0)
		c.lxRun(l, "cat /sys/firmware/devicetree/base/compatible", "dt/linux-compatible.txt", 0)
		if b, ok := c.readHexFromLinux(l, "/sys/firmware/fdt", false); ok && FDTTotalSize(b) > 0 {
			_ = s.WriteFile("dt/fdt-linux.dtb", b)
		} else {
			c.note(L("/sys/firmware/fdt не прочитан (нет od/hexdump/xxd или нет доступа)", "/sys/firmware/fdt not read (no od/hexdump/xxd or no access)"))
		}
	}
	if ly.Network {
		link, _ := c.lxRun(l, "ip link", "network/ip-link.txt", 0)
		c.lxRun(l, "ip addr", "network/ip-addr.txt", 0)
		c.lxRun(l, "ls -l /sys/class/net", "network/sys-class-net.txt", 0)
		n := 0
		for _, i := range ParseIPLink(link) {
			if i.Name == "lo" || !ifaceArg.MatchString(i.Name) || n >= 16 {
				continue
			}
			n++
			c.lxRun(l, "ethtool -i "+i.Name, "network/ethtool-i-"+safeName(i.Name)+".txt", 0)
			c.lxRun(l, "ethtool "+i.Name, "network/ethtool-"+safeName(i.Name)+".txt", 0)
		}
	}
	if ly.GPIO {
		c.lxRun(l, "cat /sys/kernel/debug/gpio", "gpio/linux-debug-gpio.txt", 0)
		c.lxRun(l, "ls /sys/class/leds", "gpio/linux-leds.txt", 0)
	}
	s.Info("%s", L("Linux probe завершён; flash не изменялась.", "Linux probe finished; flash was not changed."))
}

func (c *collector) sampleLinuxMTD(l *Linux, parts []ProcMTD) {
	for i, p := range parts {
		if i >= 32 || c.sampled[p.Name+"|head"] {
			continue
		}
		blocks := c.o.SampleHead / 4096
		if blocks < 1 {
			blocks = 1
		}
		if uint64(blocks*4096) > p.Size {
			continue
		}
		src := fmt.Sprintf("dd if=/dev/mtd%d bs=4096 count=%d", p.Index, blocks)
		if b, ok := c.readHexFromLinux(l, src, true); ok && len(b) == blocks*4096 {
			c.saveSample(SampleRecord{MTD: p.Name, Source: "linux", Offset: 0, Kind: "head"}, b)
		}
		if p.Size <= 8<<20 && p.Size >= 8192 && !c.sampled[p.Name+"|tail"] {
			skip := p.Size/4096 - 1
			src := fmt.Sprintf("dd if=/dev/mtd%d bs=4096 count=1 skip=%d", p.Index, skip)
			if b, ok := c.readHexFromLinux(l, src, true); ok && len(b) == 4096 {
				c.saveSample(SampleRecord{MTD: p.Name, Source: "linux", Offset: skip * 4096, Kind: "tail"}, b)
			}
		}
	}
}
