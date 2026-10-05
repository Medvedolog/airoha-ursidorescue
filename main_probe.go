package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ursidorescue/app"
	"ursidorescue/probe"
)

// Probe exit codes (spec §32).
const (
	exitProbeOK         = 0
	exitProbeFail       = 1
	exitUARTUnavailable = 2
	exitNoBootROM       = 3
	exitNoUBoot         = 4
	exitNoLinux         = 5
	exitIncomplete      = 6
	exitSafetyBlocked   = 7
)

// newProbeDir starts a new Porting session (work/sessions/<id>/) and returns
// its directory; the probe writes its data there.
func (a *App) newProbeDir() string {
	if d, err := a.newProbeSession(); err == nil {
		return d
	}
	d := filepath.Join(a.work, "probe-"+time.Now().Format("20060102-150405"))
	a.probeDir = d
	return d
}

func (a *App) currentProbeDir() string {
	if a.probeDir == "" {
		return a.newProbeDir()
	}
	return a.probeDir
}

// runProbeIn runs one probe inside the current Porting session.
func (a *App) runProbeIn(kind string, risk app.Risk, r probeRequest) (probeResult, error) {
	var res probeResult
	err := a.runProbeOperation(kind, risk, func() error {
		var e error
		res, e = a.runProbe(r)
		if e == nil && res.code != exitProbeOK {
			e = probeCodeError{code: res.code, effects: probeEffects(res, r)}
		}
		return e
	})
	return res, err
}

// probeCodeError is a probe that finished without everything that was asked
// (no UART, no BootROM/U-Boot/Linux, an incomplete profile). Nothing was
// written; the session records it as failed and front ends must not show it
// as success.
type probeCodeError struct {
	code int
	// effects says what the run changed, from its mode and outcome.
	effects string
}

// probeEffects states what a finished probe changed on the device. It is
// derived from what actually happened, not from the menu item: UBI attach
// and stock FTP provisioning are the only things a probe may change.
func probeEffects(res probeResult, r probeRequest) string {
	var parts []string
	switch {
	case res.outcome.UBIAttached:
		parts = append(parts, L("выполнялся ubi part — UBI мог изменить метаданные во flash (advanced)", "ubi part was run: UBI may have changed its metadata in flash (advanced)"))
	case r.opts.UBIAttach:
		parts = append(parts, L("ubi part разрешён, но не выполнялся — во flash ничего не писалось", "ubi part was allowed but not run: nothing was written to flash"))
	default:
		parts = append(parts, L("команд записи во flash не отправлялось", "no flash write commands were sent"))
	}
	if res.outcome.StockProvisioned {
		parts = append(parts, L("на stock-прошивке включён FTP (изменена настройка по вашему согласию)", "FTP was enabled on the stock firmware (a setting changed with your consent)"))
	}
	if r.ramUBoot != "" {
		parts = append(parts, L("RAM U-Boot загружен только в память", "the RAM U-Boot was loaded into RAM only"))
	}
	return strings.Join(parts, "; ") + "."
}

func (e probeCodeError) Error() string {
	return fmt.Sprintf(L("probe завершён с кодом %d: %s", "probe finished with code %d: %s"), e.code, exitText(e.code))
}

func (a *App) analyzeProbe(dir string) (*probe.Profile, error) {
	return probe.Analyze(dir, probe.AnalyzeOptions{Tool: appName, Version: appVersion})
}

type probeRequest struct {
	port        string
	dir         string
	opts        probe.Options
	ramUBoot    string // "", "md", "mf"
	export      bool
	redact      bool
	interactive bool
}

type probeResult struct {
	outcome probe.Outcome
	profile *probe.Profile
	bundle  string
	code    int
}

// runProbe opens the UART, collects, analyses and exports. Everything it
// sends goes through the probe package's allowlist; the optional RAM U-Boot
// path loads UrsidoRescue's pinned stages into RAM only.
func (a *App) runProbe(r probeRequest) (probeResult, error) {
	var res probeResult
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return res, err
	}
	var s Serial
	if r.ramUBoot != "" {
		p, ok := profiles[r.ramUBoot]
		if !ok {
			return res, fmt.Errorf(L("--ram-uboot: неизвестный профиль %q (md|mf)", "--ram-uboot: unknown profile %q (md|mf)"), r.ramUBoot)
		}
		a.portOverride = r.port
		ser, got, trans, err := a.acquireRAMUBoot(p)
		a.portOverride = ""
		a.closeLog()
		_ = os.MkdirAll(filepath.Join(r.dir, "bootrom"), 0o755)
		_ = os.WriteFile(filepath.Join(r.dir, "bootrom", "ram-uboot-acquire.log"), trans, 0o644)
		if err != nil {
			res.code = exitNoBootROM
			if ser != nil {
				ser.Close()
			}
			return res, fmt.Errorf("RAM U-Boot: %w", err)
		}
		s = ser
		a.cancelBlocked(noStopReason()) // the probe itself does not poll STOP yet
		r.opts.AtUBootPrompt = got.SoC + ">"
		r.opts.UBootSource = "ursido-ram-uboot"
		r.opts.RAMHint = loadAddr
		br := map[string]any{"detected": true, "press_x": true, "xmodem": true, "entry_sequence": "press-x", "receiver_char": "C",
			"preloader_ok": true, "fip_ok": true, "flash_written": false, "via": "UrsidoRescue RAM U-Boot " + got.RAMFIPRel,
			"collected_at": time.Now().Format(time.RFC3339)}
		b, _ := json.MarshalIndent(br, "", "  ")
		_ = os.WriteFile(filepath.Join(r.dir, "bootrom", "bootrom.json"), b, 0o644)
	} else {
		a.portOverride = r.port
		ser, err := a.openPort()
		a.portOverride = ""
		if err != nil {
			res.code = exitUARTUnavailable
			return res, err
		}
		s = ser
	}
	defer s.Close()
	sess, err := probe.NewSession(r.dir, s, uiWriter{a.ui}, probe.DefaultTiming)
	if err != nil {
		return res, err
	}
	res.outcome = probe.Collect(sess, r.opts)
	sess.Close()
	res.profile, err = a.analyzeProbe(r.dir)
	if err != nil {
		return res, err
	}
	if r.export {
		res.bundle, err = probe.Export(r.dir, res.profile, probe.ExportOptions{OutDir: a.root, Redact: r.redact})
		if err != nil {
			return res, err
		}
	}
	res.code = probeExitCode(res.outcome, res.profile, r)
	return res, nil
}

func probeExitCode(o probe.Outcome, p *probe.Profile, r probeRequest) int {
	switch {
	case o.Blocked > 0:
		return exitSafetyBlocked
	case !o.UARTData:
		return exitUARTUnavailable
	case r.opts.BootROMHandshake && !o.BootROM:
		return exitNoBootROM
	case r.opts.UBootOnly && !o.UBoot:
		return exitNoUBoot
	case r.opts.LinuxOnly && !o.Linux:
		return exitNoLinux
	case !o.UBoot && !o.Linux:
		if r.opts.LinuxOnly {
			return exitNoLinux
		}
		return exitNoUBoot
	}
	if p != nil && p.Completeness["complete"] != true {
		return exitIncomplete
	}
	return exitProbeOK
}

// ---------------- CLI (spec §31) ----------------

func probeUsage() string {
	return L(`использование:
  ursidorescue probe [--uart ПОРТ] [--output КАТАЛОГ] [--no-linux | --uboot-only | --linux-only]
                     [--bootrom] [--ram-uboot md|mf] [--timeout 5m] [--sample 4k|64k]
                     [--linux-user U] [--linux-password P] [--stock-lan-assist] [--stop-key S] [--redact] [--no-export] [--unsafe]
                     [--wake] [--ubi-attach]
  ursidorescue export [--input КАТАЛОГ] [--output КАТАЛОГ] [--redact]
  ursidorescue [--console] без флагов — полноэкранный интерфейс; --console — текстовое меню
  общий флаг: --lang ru|en (или переменная URSIDO_LANG)

probe строго только читает: erase/write/saveenv/ubi part не отправляются. Неизвестному загрузчику
не отправляется ничего; --wake разрешает один Ctrl-C для уже работающего устройства.
--ubi-attach (ADVANCED, НЕ read-only) разрешает U-Boot ubi part. --unsafe ничего не ослабляет.
коды выхода: 0 ok, 1 ошибка, 2 UART недоступен, 3 BootROM не найден, 4 U-Boot не найден,
             5 Linux недоступен, 6 профиль неполный, 7 заблокировано нарушение безопасности`, `usage:
  ursidorescue probe [--uart PORT] [--output DIR] [--no-linux | --uboot-only | --linux-only]
                     [--bootrom] [--ram-uboot md|mf] [--timeout 5m] [--sample 4k|64k]
                     [--linux-user U] [--linux-password P] [--stock-lan-assist] [--stop-key S] [--redact] [--no-export] [--unsafe]
                     [--wake] [--ubi-attach]
  ursidorescue export [--input DIR] [--output DIR] [--redact]
  ursidorescue [--console] no flags: full-screen interface; --console: text menu
  common flag: --lang ru|en (or the URSIDO_LANG variable)

probe is strictly read-only: no erase/write/saveenv/ubi part is sent. An unknown bootloader gets
nothing; --wake allows one Ctrl-C for a device that is already running.
--ubi-attach (ADVANCED, NOT read-only) allows U-Boot ubi part. --unsafe relaxes nothing.
exit codes: 0 ok, 1 failure, 2 UART unavailable, 3 BootROM not detected, 4 U-Boot not detected,
            5 Linux unavailable, 6 profile incomplete, 7 safety violation blocked`)
}

func (a *App) cliProbe(args []string) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.String("uart", "", "")
	out := fs.String("output", "", "")
	noLinux := fs.Bool("no-linux", false, "")
	ubootOnly := fs.Bool("uboot-only", false, "")
	linuxOnly := fs.Bool("linux-only", false, "")
	bootrom := fs.Bool("bootrom", false, "")
	ram := fs.String("ram-uboot", "", "")
	timeout := fs.Duration("timeout", 5*time.Minute, "")
	sample := fs.String("sample", "4k", "")
	user := fs.String("linux-user", "", "")
	pass := fs.String("linux-password", "", "")
	stockAssist := fs.Bool("stock-lan-assist", false, "")
	stop := fs.String("stop-key", "", "")
	redact := fs.Bool("redact", false, "")
	noExport := fs.Bool("no-export", false, "")
	unsafe := fs.Bool("unsafe", false, "")
	wake := fs.Bool("wake", false, "")
	ubiAttach := fs.Bool("ubi-attach", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, probeUsage())
		return exitProbeFail
	}
	if *ubootOnly && *linuxOnly {
		fmt.Fprintln(os.Stderr, L("--uboot-only и --linux-only взаимоисключающие", "--uboot-only and --linux-only exclude each other"))
		return exitProbeFail
	}
	head := 4096
	switch strings.ToLower(*sample) {
	case "4k", "4096":
	case "64k", "65536":
		head = 65536
	default:
		fmt.Fprintln(os.Stderr, L("--sample должен быть 4k или 64k", "--sample must be 4k or 64k"))
		return exitProbeFail
	}
	p := strings.TrimSpace(*port)
	if p == "" {
		ports := existingPorts()
		switch len(ports) {
		case 0:
			fmt.Fprintln(os.Stderr, L("UART-порты не найдены. Подключите USB-UART или укажите --uart PORT.", "No UART ports found. Connect a USB-UART adapter or specify --uart PORT."))
			return exitUARTUnavailable
		case 1:
			p = ports[0]
			fmt.Println(L("[UART] Автовыбор единственного порта:", "[UART] Auto-selected the only port:"), p)
		default:
			fmt.Fprintln(os.Stderr, L("Найденные UART:", "UART ports found:"))
			for i, name := range ports {
				fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, name)
			}
			fmt.Fprintln(os.Stderr, L("Найдено несколько UART. Повторите команду с --uart PORT.", "Multiple UART ports found. Re-run with --uart PORT."))
			return exitUARTUnavailable
		}
	}
	dir := *out
	var sess *app.Session
	if dir == "" {
		dir = a.newProbeDir()
		sess = a.probeSess
	}
	if *unsafe {
		fmt.Println(L("[SAFETY] --unsafe игнорируется: probe mode остаётся read-only.", "[SAFETY] --unsafe is ignored: probe mode stays read-only."))
	}
	opts := probe.Options{
		Layers: probe.AllLayers(), NoLinux: *noLinux, UBootOnly: *ubootOnly, LinuxOnly: *linuxOnly, BootROMHandshake: *bootrom,
		Timeout: *timeout, LinuxUser: *user, LinuxPassword: *pass, StopKey: *stop, SampleHead: head, Unsafe: *unsafe, RAMHint: 0,
		Wake: *wake, UBIAttach: *ubiAttach,
	}
	if *stockAssist {
		opts.LinuxLoginAssist = a.stockLANLoginAssist
	}
	req := probeRequest{port: p, dir: dir, ramUBoot: strings.ToLower(*ram), export: !*noExport, redact: *redact, opts: opts}
	if *ubiAttach {
		fmt.Println(L("[ADVANCED] --ubi-attach: U-Boot выполнит ubi part. Это НЕ read-only: UBI может изменить volume table (auto-resize), записать fastmap или перенести блоки.", "[ADVANCED] --ubi-attach: U-Boot will run ubi part. This is NOT read-only: UBI may change the volume table (auto-resize), write a fastmap or move blocks."))
	}
	risk := app.ReadOnly
	if *ubiAttach {
		risk = app.UBIMetadata
	} else if req.ramUBoot != "" {
		risk = app.NonPersistent
	}
	var res probeResult
	run := func() error {
		var e error
		res, e = a.runProbe(req)
		return e
	}
	var err error
	if sess != nil {
		err = a.inSession(sess, "probe", risk, run, true)
		a.probeSess = nil
	} else {
		err = run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "[PROBE]", err)
		if res.code == 0 {
			res.code = exitProbeFail
		}
		return res.code
	}
	printProbeSummary(res.profile)
	fmt.Println(L("каталог probe:", "probe dir:"), dir)
	if res.bundle != "" {
		fmt.Println(L("бандл:", "bundle:"), res.bundle)
	}
	fmt.Println(L("код выхода:", "exit code:"), res.code)
	return res.code
}

func (a *App) cliExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	in := fs.String("input", "", "")
	out := fs.String("output", "", "")
	redact := fs.Bool("redact", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, probeUsage())
		return exitProbeFail
	}
	dir := *in
	if dir == "" {
		dir = latestProbeDir(a.work)
		if dir == "" {
			fmt.Fprintln(os.Stderr, L("нет каталога probe в", "no probe directory under"), a.work, L("(укажите --input)", "(use --input)"))
			return exitProbeFail
		}
	}
	p, err := a.analyzeProbe(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[EXPORT]", err)
		return exitProbeFail
	}
	od := *out
	if od == "" {
		od = a.root
	}
	z, err := probe.Export(dir, p, probe.ExportOptions{OutDir: od, Redact: *redact})
	if err != nil {
		fmt.Fprintln(os.Stderr, "[EXPORT]", err)
		return exitProbeFail
	}
	printProbeSummary(p)
	fmt.Println(L("бандл:", "bundle:"), z)
	return exitProbeOK
}

// latestProbeDir finds the newest Porting data: sessions
// (work/sessions/<date>-<time>-probe-*, ordered by their exact start time)
// and legacy work/probe-<date>-<time>.
func latestProbeDir(work string) string {
	type cand struct {
		at  time.Time
		dir string
	}
	var cs []cand
	legacy, _ := filepath.Glob(filepath.Join(work, "probe-*"))
	for _, d := range legacy {
		if !dirExists(d) {
			continue
		}
		at, err := time.ParseInLocation("20060102-150405", strings.TrimPrefix(filepath.Base(d), "probe-"), time.Local)
		if err != nil {
			continue
		}
		cs = append(cs, cand{at, d})
	}
	sessions, _ := filepath.Glob(filepath.Join(app.SessionsDir(work), "*-probe-*"))
	for _, d := range sessions {
		if !dirExists(d) {
			continue
		}
		var meta struct {
			Started time.Time `json:"started"`
		}
		b, err := os.ReadFile(filepath.Join(d, "session.json"))
		if err != nil || json.Unmarshal(b, &meta) != nil || meta.Started.IsZero() {
			continue
		}
		cs = append(cs, cand{meta.Started, d})
	}
	if len(cs) == 0 {
		return ""
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].at.Before(cs[j].at) })
	return cs[len(cs)-1].dir
}

// existingPorts uses the same platform enumerator as the interactive menu.
// On Windows listSerialPorts uses QueryDosDeviceW, so CLI probe sees real COM
// devices without opening or disturbing them.
func existingPorts() []string {
	return listSerialPorts()
}

// ---------------- menu (spec §3) ----------------

func (a *App) portingMenu() error {
	for {
		fmt.Println("\n────────────────────────────────")
		fmt.Println(L(" ПОРТИРОВАНИЕ / ИССЛЕДОВАНИЕ ОБОРУДОВАНИЯ", " PORTING / HARDWARE DISCOVERY"))
		fmt.Println("────────────────────────────────")
		fmt.Println(L("  1. Исследование нового устройства Airoha (полный автоматический probe)", "  1. Probe new Airoha device (full automatic probe)"))
		fmt.Println(L("  2. Сбор профиля BootROM", "  2. Collect BootROM profile"))
		fmt.Println(L("  3. Сбор профиля U-Boot", "  3. Collect U-Boot profile"))
		fmt.Println(L("  4. Сбор профиля Linux", "  4. Collect Linux profile"))
		fmt.Println(L("  5. Сбор карты flash / MTD / UBI", "  5. Collect flash / MTD / UBI map"))
		fmt.Println(L("  6. Сбор DTB / device tree", "  6. Collect DTB / device-tree"))
		fmt.Println(L("  7. Сбор данных сети / PHY / коммутатора", "  7. Collect network / PHY / switch data"))
		fmt.Println(L("  8. Экспорт porting bundle Ursus", "  8. Export Ursus porting bundle"))
		fmt.Println(L("  9. Просмотр собранного профиля", "  9. View collected profile"))
		fmt.Println(L("  A. ADVANCED: U-Boot UBI attach (НЕ read-only)", "  A. ADVANCED: U-Boot UBI attach (NOT read-only)"))
		fmt.Println(L("  N. Новая probe-сессия (текущая: ", "  N. Start a new probe session (current: ") + displayDir(a.probeDir) + ")")
		fmt.Println(L("  0. Назад", "  0. Back"))
		v := strings.ToUpper(a.ask(L("Выбор: ", "Choice: ")))
		if v == "0" {
			return nil
		}
		// A finished probe with a non-zero code already printed its result.
		if err := a.portingItem(v); err != nil && !errors.As(err, new(probeCodeError)) {
			a.showProbeErr(err)
		}
	}
}

func displayDir(d string) string {
	if d == "" {
		return L("ещё не создана", "not created yet")
	}
	return d
}

func (a *App) showProbeErr(err error) {
	fmt.Println("\n[PROBE STOP]", err)
	fmt.Println(L("Команд записи во flash не отправлялось.", "No flash write commands were sent."))
}

// portingItem runs one item of the Porting menu (1-9, A, N). It reports only
// through a.ui, so the console and the TUI share it.
func (a *App) portingItem(v string) error {
	switch strings.ToUpper(v) {
	case "1":
		return a.menuProbe(probe.Options{Layers: probe.AllLayers()}, true)
	case "2":
		return a.menuBootROM()
	case "3":
		return a.menuProbe(probe.Options{Layers: probe.Layers{UBoot: true}, UBootOnly: true}, false)
	case "4":
		return a.menuProbe(probe.Options{Layers: probe.Layers{Linux: true}, LinuxOnly: true}, false)
	case "5":
		return a.menuProbe(probe.Options{Layers: probe.Layers{Flash: true}, FirstReachable: true}, false)
	case "6":
		return a.menuProbe(probe.Options{Layers: probe.Layers{DT: true}, FirstReachable: true}, false)
	case "7":
		return a.menuProbe(probe.Options{Layers: probe.Layers{Network: true}, FirstReachable: true}, false)
	case "8":
		return a.menuExport()
	case "9":
		return a.menuView()
	case "A":
		return a.menuUBIAttach()
	case "N":
		a.noteln(L("Новая сессия:", "New session:"), a.newProbeDir())
		return nil
	}
	a.note(L("Неверный выбор.", "Invalid choice."))
	return nil
}

func (a *App) probeAsk(prompt string) string { return a.ask(prompt) }

func (a *App) menuProbe(o probe.Options, export bool) error {
	o.Ask = a.probeAsk
	o.LinuxLoginAssist = a.stockLANLoginAssist
	if strings.ToLower(a.ask(L("Устройство уже включено и стоит в U-Boot/Linux prompt (разрешить один Ctrl-C)? [y/N]: ", "Is the device already on and sitting at a U-Boot/Linux prompt (allow one Ctrl-C)? [y/N]: "))) == "y" {
		o.Wake = true
	}
	o.Timeout = 5 * time.Minute
	a.note(L("\nProbe flash-команды остаются read-only. Stock LAN assist сначала только читает Web-реквизиты; если понадобится включить FTP, будет отдельное y/N с предупреждением об изменении stock-настройки.", "\nProbe flash commands remain read-only. Stock LAN assist first only reads Web credentials; if FTP must be enabled, a separate y/N warns that a stock setting will change."))
	a.note(L("Подключите UART (GND/TX/RX, 3.3V; VCC не подключать).", "Connect the UART (GND/TX/RX, 3.3V; never connect VCC)."))
	a.note(L("После запуска включите устройство. Если нужна Linux-часть, probe попросит перезагрузить его после U-Boot.", "After starting, power the device on. For the Linux part the probe will ask you to power-cycle it after U-Boot."))
	risk := app.ReadOnly
	if o.UBIAttach {
		risk = app.UBIMetadata
	}
	res, err := a.runProbeIn("probe", risk, probeRequest{dir: a.currentProbeDir(), opts: o, export: export, interactive: true})
	if err != nil && !errors.As(err, new(probeCodeError)) {
		return err
	}
	a.noteProbeSummary(res.profile)
	if res.bundle != "" {
		a.noteln("Porting bundle:", res.bundle)
	}
	a.notef(L("Результат probe: код %d (%s)\n", "Probe result: code %d (%s)\n"), res.code, exitText(res.code))
	return err
}

func (a *App) menuBootROM() error {
	v, _ := a.ui.Ask(app.AskRequest{Kind: app.AskChoice, Title: L("\nПрофиль BootROM:", "\nBootROM profile:"),
		Choices: []app.Choice{
			{Key: "1", Label: L("Только наблюдать (ничего не отправлять)", "Observe only (send nothing)")},
			{Key: "2", Label: L("Ответить x на 'Press x' и проверить XMODEM 'C'", "Answer 'Press x' with x and check the XMODEM 'C'")},
			{Key: "3", Label: L("Загрузить RAM U-Boot UrsidoRescue (только Nokia MD/MF) и собрать U-Boot/flash профиль", "Load UrsidoRescue's RAM U-Boot (Nokia MD/MF only) and collect the U-Boot/flash profile")},
		},
		Prompt: L("Выбор [1]: ", "Choice [1]: "), Default: "1"})
	switch strings.TrimSpace(v) {
	case "", "1":
		return a.menuProbe(probe.Options{Layers: probe.Layers{BootROM: true}, LinuxOnly: true, NoLinux: true, Timeout: 3 * time.Minute}, false)
	case "2":
		return a.menuProbe(probe.Options{Layers: probe.Layers{BootROM: true}, BootROMHandshake: true, LinuxOnly: true, NoLinux: true}, false)
	case "3":
		pref, err := chooseProfileInteractive(a)
		if err != nil {
			return err
		}
		if pref.ID == "auto" {
			return errors.New(L("для RAM U-Boot выберите MD или MF явно", "for the RAM U-Boot choose MD or MF explicitly"))
		}
		o := probe.Options{Layers: probe.AllLayers(), Ask: a.probeAsk, Timeout: 5 * time.Minute}
		res, err := a.runProbeIn("probe-ram-uboot", app.NonPersistent, probeRequest{dir: a.currentProbeDir(), opts: o, ramUBoot: pref.ID, interactive: true})
		if err != nil && !errors.As(err, new(probeCodeError)) {
			return err
		}
		a.noteProbeSummary(res.profile)
		a.notef(L("Результат probe: код %d (%s)\n", "Probe result: code %d (%s)\n"), res.code, exitText(res.code))
		return err
	}
	return errors.New(L("неверный выбор", "invalid choice"))
}

// menuUBIAttach is the only way to let U-Boot attach UBI from the menu.
func (a *App) menuUBIAttach() error {
	a.note(L("\nADVANCED: U-Boot выполнит ubi part для раздела с UBI-заголовком, чтобы прочитать список томов и хеш FIP.", "\nADVANCED: U-Boot will run ubi part on the partition with a UBI header to read the volume list and the FIP hash."))
	a.note(L("Это НЕ read-only: при attach UBI может изменить volume table (auto-resize), записать fastmap или перенести блоки.", "This is NOT read-only: attaching UBI may change the volume table (auto-resize), write a fastmap or move blocks."))
	a.note(L("Для списка томов без записи лучше загрузить Linux и взять ubinfo -a (обычный probe делает это сам).", "For the volume list without writes, boot Linux and use ubinfo -a (the normal probe does that)."))
	if err := a.confirm(app.UBIMetadata, "UBI ATTACH"); err != nil {
		return err
	}
	return a.menuProbe(probe.Options{Layers: probe.Layers{UBoot: true, Flash: true}, UBootOnly: true, UBIAttach: true}, false)
}

func (a *App) menuExport() error {
	dir := a.probeDir
	if dir == "" {
		dir = latestProbeDir(a.work)
	}
	if dir == "" {
		return errors.New(L("нет собранных данных — сначала выполните probe", "nothing collected yet — run a probe first"))
	}
	p, err := a.analyzeProbe(dir)
	if err != nil {
		return err
	}
	redact := strings.ToLower(a.ask(L("Маскировать MAC/серийные номера в текстовых файлах? [y/N]: ", "Mask MAC addresses/serial numbers in text files? [y/N]: "))) == "y"
	z, err := probe.Export(dir, p, probe.ExportOptions{OutDir: a.root, Redact: redact})
	if err != nil {
		return err
	}
	a.noteln("Porting bundle:", z)
	return nil
}

func (a *App) menuView() error {
	dir := a.probeDir
	if dir == "" {
		dir = latestProbeDir(a.work)
	}
	if dir == "" {
		return errors.New(L("нет собранных данных", "nothing collected yet"))
	}
	p, err := a.analyzeProbe(dir)
	if err != nil {
		return err
	}
	a.noteProbeSummary(p)
	a.noteln(L("Полный профиль:", "Full profile:"), filepath.Join(dir, "profile.json"))
	a.noteln(L("Отчёт UrsusBoot:", "UrsusBoot report:"), filepath.Join(dir, "ursusboot-porting-report.md"))
	return nil
}

func exitText(c int) string {
	if uiLang == "ru" {
		return map[int]string{0: "probe завершён", 1: "ошибка", 2: "UART недоступен", 3: "BootROM не найден", 4: "U-Boot не найден",
			5: "Linux недоступен", 6: "профиль неполный", 7: "заблокировано нарушение безопасности"}[c]
	}
	return map[int]string{0: "probe completed", 1: "generic failure", 2: "UART unavailable", 3: "BootROM not detected", 4: "U-Boot not detected",
		5: "Linux unavailable", 6: "profile incomplete", 7: "safety violation blocked"}[c]
}

func fv(f *probe.Fact) string {
	if f == nil || f.Value == nil {
		if f != nil && f.Note == "conflict" {
			return L("КОНФЛИКТ", "CONFLICT")
		}
		return L("неизвестно", "unknown")
	}
	switch v := f.Value.(type) {
	case uint64:
		return fmt.Sprintf("0x%x [%s]", v, f.Confidence)
	}
	return fmt.Sprintf("%v [%s]", f.Value, f.Confidence)
}

// printProbeSummary is the CLI's rendering of the profile summary.
func printProbeSummary(p *probe.Profile) {
	for _, l := range probeSummaryLines(p) {
		fmt.Println(l)
	}
}

// noteProbeSummary reports the profile summary through the UI.
func (a *App) noteProbeSummary(p *probe.Profile) {
	for _, l := range probeSummaryLines(p) {
		a.note(l)
	}
}

func probeSummaryLines(p *probe.Profile) []string {
	if p == nil {
		return nil
	}
	var out []string
	ln := func(args ...any) { out = append(out, strings.TrimSuffix(fmt.Sprintln(args...), "\n")) }
	ln("\n=== ursus-profile-v1 ===")
	ln("SoC:         ", fv(&p.SoC.Fact))
	ln(L("Модель:      ", "Model:       "), fv(p.Device["model"]))
	ln("Compatible:  ", fv(p.Device["compatible"]))
	ram := L("неизвестно", "unknown")
	if f := p.Device["ram_mib"]; f != nil && f.Value != nil {
		ram = fmt.Sprintf("%v MiB [%s]", f.Value, f.Confidence)
	} else if f != nil && f.Note == "conflict" {
		ram = L("КОНФЛИКТ", "CONFLICT")
	}
	ln("RAM:         ", ram)
	ln("Flash:       ", fv(p.Flash.Type), "/", fv(p.Flash.Manufacturer), L("/ размер", "/ size"), fv(p.Flash.TotalSize), L("/ блок", "/ erase"), fv(p.Flash.EraseSize), L("/ страница", "/ page"), fv(p.Flash.PageSize))
	ln("U-Boot:      ", p.UBoot["detected"], p.UBoot["version"], p.UBoot["source"])
	ln("Linux:       ", p.Linux["detected"], p.Linux["os"])
	ln("BootROM:     ", p.BootROM["detected"], "xmodem:", p.BootROM["xmodem"])
	if len(p.MTD) > 0 {
		ln("MTD:")
		for _, e := range p.MTD {
			off, sz := "?", "?"
			if e.Offset != nil {
				off = fmt.Sprintf("0x%08x", *e.Offset)
			}
			if e.Size != nil {
				sz = fmt.Sprintf("0x%08x", *e.Size)
			}
			flag := ""
			if e.Conflict {
				flag = " CONFLICT"
			}
			if e.DeviceSpecific {
				flag += " device-specific"
			}
			out = append(out, fmt.Sprintf("  %-16s off=%s size=%s [%s]%s", e.Name, off, sz, e.Confidence, flag))
		}
	}
	for _, c := range p.Conflicts {
		ln(L("КОНФЛИКТ:", "CONFLICT:"), c.Field)
	}
	if m, ok := p.Completeness["missing"].([]string); ok && len(m) > 0 {
		ln(L("Не хватает:", "Missing:"), strings.Join(m, ", "))
	}
	for _, w := range p.Warnings {
		ln(L("ВНИМАНИЕ:", "WARN:"), w)
	}
	return out
}
