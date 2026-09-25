package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"ursidorescue/app"
)

// Paths the operator gave during this run, per operation, newest first, so
// a retry after an error does not mean typing the backup folder again. They
// live only in memory: nothing about the operator's files is written out.

const recentPathsMax = 3

type recentPaths struct {
	mu sync.Mutex
	m  map[string][]string
}

func (r *recentPaths) list(kind string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.m[kind]...)
}

func (r *recentPaths) remember(kind, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string][]string{}
	}
	xs := []string{path}
	for _, p := range r.m[kind] {
		if p != path && len(xs) < recentPathsMax {
			xs = append(xs, p)
		}
	}
	r.m[kind] = xs
}

// askPathAnswer asks for a path offering this operation's recent paths and,
// where the system has one, a file dialog ("*"). An answer that is a number
// of the list picks that path; anything else is the typed path.
func (a *App) askPathAnswer(prompt string) string {
	failed := false
	for {
		recent := a.recent.list(a.opKind)
		var ch []app.Choice
		for i, p := range recent {
			ch = append(ch, app.Choice{Key: strconv.Itoa(i + 1), Label: p})
		}
		browse := !failed && filePickerAvailable()
		if browse {
			ch = append(ch, app.Choice{Key: "*", Label: L("Обзор… (окно выбора файла)", "Browse… (file dialog)")})
		}
		title := ""
		switch {
		case len(recent) > 0 && browse:
			title = L("Недавние пути (номер), обзор (*) или свой путь:", "Recent paths (number), browse (*) or your own path:")
		case len(recent) > 0:
			title = L("Недавние пути (номер) или свой путь:", "Recent paths (number) or your own path:")
		}
		v, _ := a.ui.Ask(app.AskRequest{Kind: app.AskPath, Title: title, Choices: ch, Prompt: prompt})
		v = strings.Trim(strings.TrimSpace(v), "\"")
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(recent) {
			return recent[n-1]
		}
		if v != "*" || !browse {
			return v
		}
		dir := ""
		if len(recent) > 0 {
			dir = recent[0]
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				dir = filepath.Dir(dir)
			}
		}
		p, err := pickFile(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(prompt), ":")), dir)
		switch {
		case err != nil:
			failed = true
			a.status("!", L("Окно выбора файла недоступно: ", "The file dialog is unavailable: ")+err.Error()+L(". Введите путь вручную.", ". Type the path."), app.LevelWarn)
		case p != "":
			return p
		}
		// Cancelled in the dialog: ask again.
	}
}
