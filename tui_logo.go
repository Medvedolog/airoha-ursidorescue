package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The winking, smiling teddy-bear head of the Ursus family, drawn with
// half-block characters (▀▄█), which every console font has. The big one is
// generated from ellipses (head, ears) with the inner ears, the eyes, the
// nose and the smile cut out; the small one is drawn by hand for short
// terminals. The right eye winks: its "▀▀▀" is the only accent.
var (
	bearBig = []string{
		" ████        ████",
		"▀█  ██████████  █▀",
		" ████████████████",
		"▄████  ████▀▀▀███▄",
		"████████▀▀████████",
		" ████▀██▄▄██▀████",
		"  ▀███▄▄▄▄▄▄███▀",
		"      ▀▀▀▀▀▀",
	}
	bearSmall = []string{
		"▄███▄    ▄███▄",
		"▀█▄▄██████▄▄█▀",
		" ██  ███▀▀▀██",
		" ███▀█▄▄█▀███",
		"  ▀▀█▄▄▄▄█▀▀",
	}
)

const bearWink = "▀▀▀"

// tuiLogo is the bear with the name and version beside it, for a screen
// corner, within rows × width. It returns nil when not even the small bear
// fits.
func tuiLogo(rows, width int) []string {
	art := bearBig
	if rows < len(bearBig) || width < 60 {
		art = bearSmall
	}
	if rows < len(art) || width < 30 {
		return nil
	}
	var text []string
	first := 2 // the text starts beside the eyes
	if len(art) == len(bearBig) {
		text = []string{
			tsBrand.Render("UrsidoRescue") + " " + tsFaint.Render(appVersion),
			tsMuted.Render(L("UART-спасатель роутеров Airoha", "UART rescue for Airoha routers")),
			tsFaint.Render(L("семейство Ursus · Bearborn utility", "Ursus family · Bearborn utility")),
		}
	} else {
		first = 1
		text = []string{tsBrand.Render("UrsidoRescue"), tsFaint.Render(appVersion),
			tsMuted.Render(L("UART-спасатель", "UART rescue"))}
	}
	artW := 0
	for _, l := range art {
		artW = max(artW, lipgloss.Width(l))
	}
	out := make([]string, len(art))
	for i, l := range art {
		drawn := tsBrand.Render(l)
		if j := strings.Index(l, bearWink); j >= 0 {
			drawn = tsBrand.Render(l[:j]) + tsSand.Render(bearWink) + tsBrand.Render(l[j+len(bearWink):])
		}
		out[i] = " " + drawn // a margin from the screen edge
		if t := i - first; t >= 0 && t < len(text) {
			if line := out[i] + strings.Repeat(" ", artW-lipgloss.Width(l)+3) + text[t]; lipgloss.Width(line) <= width {
				out[i] = line // a caption that does not fit is left out
			}
		}
	}
	return out
}
