package main

import (
	"os"
	"strings"

	"ursidorescue/probe"
)

// uiLang is the language of menus, prompts and messages: "ru" or "en".
// Confirmation phrases (WRITE FIP, RESTORE STOCK BACKUP, ...) are the same in
// both languages on purpose, and so are the files meant for developers.
var uiLang = "ru"

// L returns the text for the current UI language.
func L(ru, en string) string {
	if uiLang == "en" {
		return en
	}
	return ru
}

func setLang(l string) bool {
	l = strings.ToLower(strings.TrimSpace(l))
	if l != "ru" && l != "en" {
		return false
	}
	uiLang = l
	probe.SetLang(l)
	return true
}

// langFromArgs removes "--lang xx" / "--lang=xx" from args and applies it.
// Without the flag URSIDO_LANG is used. It reports whether a language was set.
func langFromArgs(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	set := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--lang" && i+1 < len(args):
			set = setLang(args[i+1]) || set
			i++
		case strings.HasPrefix(a, "--lang="):
			set = setLang(strings.TrimPrefix(a, "--lang=")) || set
		default:
			out = append(out, a)
		}
	}
	if !set {
		set = setLang(os.Getenv("URSIDO_LANG"))
	}
	return out, set
}

// langFromLocale is the default for non-interactive commands.
func langFromLocale() {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.ToLower(os.Getenv(k)); v != "" {
			if strings.HasPrefix(v, "ru") {
				setLang("ru")
			} else {
				setLang("en")
			}
			return
		}
	}
	setLang("en")
}

func (a *App) chooseLanguage() {
	for {
		v := strings.TrimSpace(a.ask("Язык / Language:  1. Русский   2. English  [1]: "))
		switch strings.ToLower(v) {
		case "", "1", "ru", "р":
			setLang("ru")
			return
		case "2", "en", "e":
			setLang("en")
			return
		}
	}
}
