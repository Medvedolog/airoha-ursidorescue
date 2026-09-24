package probe

// lang is the operator-message language: "ru" or "en". Files written for
// developers (profile.json, reports) are always English.
var lang = "ru"

// SetLang selects the operator-message language ("ru" or "en").
func SetLang(l string) {
	if l == "en" || l == "ru" {
		lang = l
	}
}

// L returns the operator text for the current language.
func L(ru, en string) string {
	if lang == "en" {
		return en
	}
	return ru
}
