package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ursidorescue/app"
)

// operation is one entry of the scenario catalogue (doc/UI_SPEC_RU.md §10).
// Front ends start operations by kind through RunOperation, so every front
// end gets the same session, operation ID, risk class and logs.
type operation struct {
	Risk app.Risk
	run  func(a *App) error
}

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
	prevUI, prevSess, prevOp := a.ui, a.sess, a.op
	a.ui = &app.SessionUI{Inner: a.front, Session: sess, Op: opID}
	a.sess, a.op = sess, opID
	sess.Record(map[string]any{"op": opID, "event": "start", "operation": kind, "risk": string(risk)})
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
	a.ui, a.sess, a.op = prevUI, prevSess, prevOp
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
		name, err := a.choosePort()
		if err != nil {
			return nil, err
		}
		if err := owner.Connect(name); err != nil {
			return nil, fmt.Errorf(L("не удалось открыть %s: %w", "open %s: %w"), name, err)
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
