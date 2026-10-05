package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"ursidorescue/app"
)

// consoleUI is the console front end of the application layer: it renders
// core events exactly as the interactive console did before app existed.
type consoleUI struct {
	mu     sync.Mutex
	reader *bufio.Reader
}

func newConsoleUI(r *bufio.Reader) *consoleUI { return &consoleUI{reader: r} }

func levelStyle(l app.Level) string {
	switch l {
	case app.LevelOK:
		return uiOK
	case app.LevelWarn:
		return uiSand
	case app.LevelError:
		return uiBad
	case app.LevelInfo:
		return uiAmber2
	default:
		return uiMuted
	}
}

func (c *consoleUI) Event(e app.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e.Kind {
	case app.KindSectionStart:
		fmt.Println()
		uiRule(e.Text, uiAmber)
		return
	case app.KindSectionEnd:
		uiRule("", uiAmber)
		fmt.Println()
		return
	}
	switch {
	case e.Label != "":
		uiStatus(e.Label, e.Text, levelStyle(e.Level))
	case e.Level == app.LevelNote:
		fmt.Println(e.Text)
	default:
		t := e.Time
		if t.IsZero() {
			t = time.Now()
		}
		uiEvent(t.Format("15:04:05"), e.Text)
	}
}

func (c *consoleUI) Progress(p app.Progress) {
	if p.Overall {
		return // the console reports every step as its own line already
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.Done {
		fmt.Println()
		return
	}
	fmt.Printf("\r%s %s", paint("["+p.Label+"]", uiSand), p.Detail)
}

func (c *consoleUI) readLine() string {
	s, _ := c.reader.ReadString('\n')
	return strings.TrimSpace(s)
}

func (c *consoleUI) Ask(q app.AskRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if q.Title != "" {
		fmt.Println(q.Title)
	}
	for _, ch := range q.Choices {
		fmt.Printf("  %s. %s\n", ch.Key, ch.Label)
	}
	fmt.Print(q.Prompt)
	return c.readLine(), nil
}

func (c *consoleUI) Confirm(r app.ConfirmRequest) error {
	if err := r.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.Title != "" {
		fmt.Println(r.Title)
	}
	for _, s := range r.Summary {
		fmt.Println(s)
	}
	if app.FormFor(r.Risk) != app.FormNone || len(r.Actions) > 0 {
		fmt.Println(paint(L("Риск: ", "Risk: ")+string(r.Risk), uiSand))
	}
	for i, act := range r.Actions {
		fmt.Printf("  %d. %s\n", i+1, act)
	}
	if r.CancelNote != "" {
		fmt.Println(paint(L("Остановка: ", "Stopping: ")+r.CancelNote, uiMuted))
	}
	if r.Phrase != "" {
		fmt.Printf(L("Введите точно %s: ", "Type exactly %s: "), r.Phrase)
	} else if app.FormFor(r.Risk) == app.FormButton {
		fmt.Print(L("Подтвердить? [y/N]: ", "Confirm? [y/N]: "))
	} else {
		return nil
	}
	return app.CheckAnswer(r, c.readLine())
}

func (c *consoleUI) Output(b []byte) {
	_, _ = os.Stdout.Write(b)
}

func (c *consoleUI) Artifact(a app.Artifact) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Println(a.Label, a.Path)
}

// Cancel is not drawn: the console has no STOP button (Ctrl+C ends the
// program). The session still records every state.
func (c *consoleUI) Cancel(app.CancelState) {}

// uiWriter lets code that writes to an io.Writer (the probe session) report
// through the UI.
type uiWriter struct{ ui app.UI }

func (w uiWriter) Write(p []byte) (int, error) {
	w.ui.Output(append([]byte(nil), p...))
	return len(p), nil
}

// watchInterrupt makes Ctrl+C the console's STOP button while an operation
// runs; the core decides what it means (RequestStop). Outside an operation
// Ctrl+C ends the program as before. A second Ctrl+C within 3 s ends the
// program anyway. The UART terminal reads Ctrl+C as a key in raw mode, so it
// still goes to the router and never reaches this handler.
func (a *App) watchInterrupt() (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		var last time.Time
		for {
			select {
			case <-done:
				return
			case <-ch:
			}
			if !a.Busy() {
				fmt.Fprintln(os.Stderr)
				os.Exit(130)
			}
			if !last.IsZero() && time.Since(last) < 3*time.Second {
				fmt.Fprintln(os.Stderr, paint(L("\n[ВЫХОД] принудительное завершение по второму Ctrl+C", "\n[EXIT] forced exit on the second Ctrl+C"), uiBad))
				os.Exit(130)
			}
			last = time.Now()
			fmt.Fprintln(os.Stderr, "\n"+stopMessage(a.RequestStop()))
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// stopMessage is the console's rendering of a STOP press.
func stopMessage(c app.CancelState) string {
	switch {
	case !c.Requested:
		return paint(L("[СТОП] остановка сейчас недоступна: ", "[STOP] stopping is unavailable now: ")+c.Reason, uiSand) + "\n" +
			paint(L("       Шаг будет доведён до проверки. Второй Ctrl+C в течение 3 с — принудительный выход, устройство может остаться нерабочим.",
				"       The step will run to its check. A second Ctrl+C within 3 s forces exit; the device may be left unbootable."), uiMuted)
	case c.Mode == app.CancelAtCheckpoint:
		return paint(L("[СТОП] остановка запрошена: остановлюсь ", "[STOP] stop requested: will stop ")+c.Checkpoint, uiSand)
	default:
		return paint(L("[СТОП] остановка запрошена: следующая команда не будет отправлена (если ждёт ввод — нажмите Enter)",
			"[STOP] stop requested: the next command will not be sent (if a prompt is waiting, press Enter)"), uiSand)
	}
}
