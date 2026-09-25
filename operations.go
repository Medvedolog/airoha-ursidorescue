package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ursidorescue/app"
)

// operation is one entry of the scenario catalogue (doc/UI_SPEC_RU.md §10).
// Front ends start operations by kind through RunOperation, so every front
// end gets the same session, operation ID, risk class and logs.
type operation struct {
	Risk app.Risk
	run  func(a *App) error
}

// unstoppable names operations whose code does not poll STOP yet; they report
// that honestly instead of promising an immediate stop.
var unstoppable = map[string]bool{"terminal": true, "shell": true}

var operationCatalog = map[string]operation{
	"stock-restore":    {app.Erase, (*App).stockRestoreWizard},
	"fip-repair":       {app.Write, (*App).fipRepairWizard},
	"physical-restore": {app.Erase, (*App).physicalRestoreWizard},
	"itb-boot":         {app.NonPersistent, (*App).bootRecoveryWizard},
	"diagnostics":      {app.ReadOnly, (*App).diagnosticsWizard},
	"support-bundle":   {app.ReadOnly, (*App).supportBundleOperation},
	"ram-uboot":        {app.NonPersistent, (*App).ramUBootShell},
	"ubi-volume":       {app.Write, (*App).expertUBIVolume},
	"raw-mtd":          {app.Write, (*App).expertRawMTD},
	"terminal":         {app.Manual, (*App).runTerminal},
	"shell":            {app.Manual, (*App).uartShell},
}

// cancelledError is the operator's refusal to confirm, in the UI language.
type cancelledError struct{ msg string }

func (e cancelledError) Error() string { return e.msg }
func (e cancelledError) Unwrap() error { return app.ErrCancelled }

// RunOperation runs a catalogue operation in a new session.
func (a *App) RunOperation(kind string) error {
	op, ok := operationCatalog[kind]
	if !ok {
		return fmt.Errorf("unknown operation %q", kind)
	}
	sess, err := app.NewSession(a.work, kind, appName, appVersion, a.frontEnd)
	if err != nil {
		return err
	}
	return a.inSession(sess, kind, op.Risk, func() error { return op.run(a) }, true)
}

// inSession runs fn as one operation of sess: it assigns the operation ID,
// routes the UI through the session and records start, end and failure.
func (a *App) inSession(sess *app.Session, kind string, risk app.Risk, fn func() error, closeAfter bool) error {
	opID := sess.NewOperation(kind)
	a.cancelMu.Lock()
	a.lastSess, a.lastOp = sess.Dir, opID
	prevUI, prevSess, prevOp, prevKind := a.ui, a.sess, a.op, a.opKind
	a.ui = &app.SessionUI{Inner: a.front, Session: sess, Op: opID}
	a.sess, a.op, a.opKind = sess, opID, kind
	a.stop.Reset()
	a.cancelMu.Unlock()
	sess.Record(map[string]any{"op": opID, "event": "start", "operation": kind, "risk": string(risk)})
	if unstoppable[kind] || strings.HasPrefix(kind, "probe") {
		a.cancelBlocked(noStopReason())
	} else {
		a.cancelNow()
	}
	err := fn()
	result := "success"
	switch {
	case errors.Is(err, app.ErrCancelled):
		result = "cancelled"
	case err != nil:
		result = "failed"
		sess.LogError(opID, err)
	}
	rec := map[string]any{"op": opID, "event": "end", "operation": kind, "result": result}
	if err != nil {
		rec["error"] = err.Error()
	}
	sess.Record(rec)
	a.cancelNow()
	a.cancelMu.Lock()
	a.ui, a.sess, a.op, a.opKind = prevUI, prevSess, prevOp, prevKind
	a.cancelMu.Unlock()
	if closeAfter {
		sess.Close(result)
	}
	return err
}

// probeSession returns the current Porting session, creating one if needed.
// Probe menu items continue one session until "N" starts a new one.
func (a *App) probeSession() (*app.Session, error) {
	if a.probeSess != nil {
		return a.probeSess, nil
	}
	s, err := app.NewSession(a.work, "probe", appName, appVersion, a.frontEnd)
	if err != nil {
		return nil, err
	}
	a.probeSess = s
	a.probeDir = s.Dir
	return s, nil
}

// newProbeSession closes the current Porting session and starts a new one.
func (a *App) newProbeSession() (string, error) {
	if a.probeSess != nil {
		a.probeSess.Close("closed")
		a.probeSess = nil
	}
	s, err := a.probeSession()
	if err != nil {
		return "", err
	}
	return s.Dir, nil
}

// runProbeOperation runs a Porting operation inside the current probe session.
func (a *App) runProbeOperation(kind string, risk app.Risk, fn func() error) error {
	s, err := a.probeSession()
	if err != nil {
		return err
	}
	return a.inSession(s, kind, risk, fn, false)
}

// closeSessions finishes a still-open Porting session on exit.
func (a *App) closeSessions() {
	if a.probeSess != nil {
		a.probeSess.Close("closed")
		a.probeSess = nil
	}
}

// supportBundleOperation builds the report bundle and reports it as an artifact.
func (a *App) supportBundleOperation() error {
	p, err := a.makeSupportBundle()
	if err != nil {
		return err
	}
	sha, _ := shaFile(p)
	a.ui.Artifact(app.Artifact{Kind: "support-bundle", Label: L("Пакет для отчёта:", "Report bundle:"), Path: p, SHA256: sha})
	return nil
}

// ramUBootShell loads the RAM U-Boot and leaves its prompt to the operator.
func (a *App) ramUBootShell() error {
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, _, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	a.note(L("RAM U-Boot готов. Открываю UART Shell; Ctrl+] вернёт в меню.", "RAM U-Boot is ready. Opening the UART Shell; Ctrl+] returns to the menu."))
	e = a.uartShellOn(s)
	s.Close()
	a.closeLog()
	return e
}

// sessionScratch returns a scratch directory for large temporary files: inside
// the current session when there is one, else under work/ as before.
func (a *App) sessionScratch(name string) string {
	if a.sess != nil {
		return a.sess.WorkDir(name)
	}
	d := filepath.Join(a.work, name)
	_ = os.MkdirAll(d, 0o755)
	return d
}

// portOwner returns the application layer's port owner.
func (a *App) portOwner() *app.PortOwner {
	if a.ports == nil {
		a.ports = app.NewPortOwner(openSerial)
	}
	return a.ports
}

// openPort leases the serial port to the running operation. When no port is
// connected (the console connects per operation), it asks for one, connects
// it for this operation only, and the lease's Close also disconnects it.
func (a *App) openPort() (Serial, error) {
	owner := a.portOwner()
	implicit := false
	if _, ok := owner.Connected(); !ok {
		if err := a.connectChosenPort(owner); err != nil {
			return nil, err
		}
		implicit = true
	}
	p, err := owner.Acquire(a.op)
	if err != nil {
		if implicit {
			_ = owner.Disconnect()
		}
		return nil, err
	}
	if implicit {
		return &oneShotPort{Port: p, owner: owner}, nil
	}
	return p, nil
}

// connectChosenPort asks for a port and connects it. A port held by another
// program is not a failure: the operator is told to close that program and
// may retry, pick another port or cancel.
func (a *App) connectChosenPort(owner *app.PortOwner) error {
	name, err := a.choosePort()
	for err == nil {
		err = owner.Connect(name)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errPortInUse) || a.portOverride != "" {
			return fmt.Errorf(L("не удалось открыть %s: %w", "open %s: %w"), name, err)
		}
		// The question says which port and what to do; the letters in the
		// prompt are for the console, the TUI shows buttons instead.
		v, _ := a.ui.Ask(app.AskRequest{Kind: app.AskText,
			Title: err.Error() + ".\n" + L("Закройте эту программу и нажмите «Повторить» — или выберите другой порт.",
				"Close that program and press Retry, or choose another port."),
			Prompt: L("Повторить (r), другой порт (p) или отмена (n)? [r]: ", "Retry (r), another port (p) or cancel (n)? [r]: "),
			Quick: []app.Choice{
				{Key: "r", Label: L("Повторить", "Retry")},
				{Key: "p", Label: L("Другой порт", "Another port")},
				{Key: "n", Label: L("Отмена", "Cancel")},
			},
			Default: "r"})
		v = strings.TrimSpace(v)
		switch strings.ToLower(v) {
		case "", "r", "к":
			err = nil // the same port again
		case "p", "з":
			name, err = a.choosePort()
		default:
			return cancelledError{L("выбор порта отменён", "port choice cancelled")}
		}
	}
	return err
}

// oneShotPort is a lease on a port connected just for one operation.
type oneShotPort struct {
	app.Port
	owner *app.PortOwner
	done  bool
}

func (p *oneShotPort) Close() error {
	if p.done {
		return nil
	}
	p.done = true
	_ = p.Port.Close()
	return p.owner.Disconnect()
}

// cancelNotes is the STOP policy per operation (doc/UI_SPEC_RU.md §17), as
// shown in the confirmation; the wizards emit the matching CancelState at
// each phase.
var cancelNotes = map[string]func() string{
	"stock-restore": func() string {
		return L("до «mtd erase ubi» — сразу; во время стирания и записи частей — после текущей части и её проверки; от стирания BL2 до его проверки — недоступна",
			"before \"mtd erase ubi\": at once; while erasing and writing chunks: after the current chunk and its readback; from erasing BL2 until it is verified: unavailable")
	},
	"physical-restore": func() string {
		return L("до «mtd erase ubi» — сразу; во время стирания и записи частей — после текущей части и её проверки; от стирания BL2 до его проверки — недоступна",
			"before \"mtd erase ubi\": at once; while erasing and writing chunks: after the current chunk and its readback; from erasing BL2 until it is verified: unavailable")
	},
	"fip-repair": func() string {
		return L("до записи — сразу; запись тома fip и её проверка не прерываются",
			"before writing: at once; writing the fip volume and its readback are not interrupted")
	},
	"ubi-volume": func() string {
		return L("до записи — сразу; запись тома и её проверка не прерываются",
			"before writing: at once; writing the volume and its readback are not interrupted")
	},
	"raw-mtd": func() string {
		return L("до записи — сразу; запись и её проверка не прерываются",
			"before writing: at once; the write and its readback are not interrupted")
	},
	"itb-boot": func() string {
		return L("до «bootm» — сразу; после «bootm» управление у ядра Linux, отменять нечего",
			"before \"bootm\": at once; after \"bootm\" the Linux kernel is in control and there is nothing to undo")
	},
}

// noStopReason is shown for operations whose code does not poll STOP yet.
func noStopReason() string {
	return L("эта операция пока не поддерживает СТОП: выход из неё — её собственными средствами",
		"this operation does not support STOP yet: leave it by its own means")
}

// confirmOp is the single confirmation with the operation's actions and its
// STOP policy (doc/UI_SPEC_RU.md §13).
func (a *App) confirmOp(risk app.Risk, phrase string, actions []string) error {
	note := ""
	if f, ok := cancelNotes[a.opKind]; ok {
		note = f()
	}
	err := a.ui.Confirm(app.ConfirmRequest{Risk: risk, Phrase: phrase, Actions: actions, CancelNote: note})
	if errors.Is(err, app.ErrCancelled) {
		return cancelledError{L("операция отменена пользователем", "operation cancelled by the user")}
	}
	return err
}

// setCancel reports what STOP does now; the front end only displays it.
func (a *App) setCancel(mode app.CancelMode, checkpoint, reason string) {
	a.cancelMu.Lock()
	a.cancel = app.CancelState{Mode: mode, Checkpoint: checkpoint, Reason: reason, Requested: a.stop.Requested()}
	c, ui := a.cancel, a.ui
	a.cancelMu.Unlock()
	ui.Cancel(c)
}

func (a *App) cancelNow() { a.setCancel(app.CancelNow, "", "") }
func (a *App) cancelAt(checkpoint string) {
	a.setCancel(app.CancelAtCheckpoint, checkpoint, "")
}
func (a *App) cancelBlocked(reason string) { a.setCancel(app.CancelUnavailable, "", reason) }

// cancelMode is the current STOP phase.
func (a *App) cancelMode() app.CancelMode {
	a.cancelMu.Lock()
	defer a.cancelMu.Unlock()
	return a.cancel.Mode
}

// LastOperation is the session directory and ID of the latest operation, so a
// front end can say which logs to attach. Safe from any goroutine.
func (a *App) LastOperation() (sessionDir, op string) {
	a.cancelMu.Lock()
	defer a.cancelMu.Unlock()
	return a.lastSess, a.lastOp
}

// Busy reports whether an operation is running. Safe from any goroutine.
func (a *App) Busy() bool {
	a.cancelMu.Lock()
	defer a.cancelMu.Unlock()
	return a.opKind != ""
}

// RequestStop is what a front end's STOP button calls; it may be called from
// any goroutine. The core alone decides: in an unavailable phase the request
// is refused (not queued) and the returned state says why; otherwise the core
// stops at once where nothing is being written, or at the next checkpoint.
func (a *App) RequestStop() app.CancelState {
	a.cancelMu.Lock()
	c, ui, sess, op := a.cancel, a.ui, a.sess, a.op
	if c.Mode != app.CancelUnavailable {
		a.stop.Request()
		c.Requested = true
		a.cancel = c
	}
	a.cancelMu.Unlock()
	if sess != nil {
		ev := "stop_requested"
		if !c.Requested {
			ev = "stop_refused"
		}
		sess.Record(map[string]any{"op": op, "event": ev, "reason": c.Reason})
	}
	ui.Cancel(c)
	return c
}

// stopError is the cancellation of an operation at a safe point.
func stopError(where string) error {
	return cancelledError{fmt.Sprintf(L("остановлено оператором в безопасной точке: %s", "stopped by the operator at a safe checkpoint: %s"), where)}
}

// checkpoint ends the operation here when STOP is pending. Call it only
// where stopping leaves the device recoverable.
func (a *App) checkpoint(where string) error {
	if a.stop.Requested() {
		return stopError(where)
	}
	return nil
}

// stopBeforeCommand lets STOP take effect before the next U-Boot command,
// but only while the operation is in a phase where stopping at once is safe.
func (a *App) stopBeforeCommand(command string) error {
	if a.stop.Requested() && a.cancelMode() == app.CancelNow {
		return stopError(L("до команды ", "before the command ") + command)
	}
	return nil
}
