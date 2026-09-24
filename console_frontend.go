package main

import (
	"bufio"
	"fmt"
	"os"
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

// uiWriter lets code that writes to an io.Writer (the probe session) report
// through the UI.
type uiWriter struct{ ui app.UI }

func (w uiWriter) Write(p []byte) (int, error) {
	w.ui.Output(append([]byte(nil), p...))
	return len(p), nil
}
