package main

import "strings"

// The winking bear of the Ursus family, in plain ASCII so every console
// font draws it.
var (
	bearBig = []string{
		"  .--.     .--.",
		" (    '---'    )",
		"  '.  o   -  .'",
		"   |   (_)   |",
		"    \\  \\_/  /",
		"     '-...-'",
	}
	bearSmall = []string{
		" .-.   .-.",
		"( '-...-' )",
		" \\ o   - /",
		"  '.(_).'",
	}
)

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
	if len(art) == len(bearBig) {
		text = []string{"",
			tsBrand.Render("UrsidoRescue") + " " + tsFaint.Render(appVersion),
			tsMuted.Render(L("UART-спасатель роутеров Airoha", "UART rescue for Airoha routers")),
			tsFaint.Render(L("семейство Ursus · только то, что вы подтвердили", "Ursus family · only what you confirm"))}
	} else {
		text = []string{tsBrand.Render("UrsidoRescue"), tsFaint.Render(appVersion),
			tsMuted.Render(L("UART-спасатель Airoha", "Airoha UART rescue"))}
	}
	artW := 0
	for _, l := range art {
		artW = max(artW, len(l))
	}
	out := make([]string, len(art))
	for i, l := range art {
		// The winking eye is the only accent.
		drawn := tsBrand.Render(l)
		if j := strings.Index(l, "o   -"); j >= 0 {
			drawn = tsBrand.Render(l[:j+4]) + tsSand.Render("-") + tsBrand.Render(l[j+5:])
		}
		out[i] = drawn
		if i < len(text) {
			out[i] += strings.Repeat(" ", artW-len(l)+2) + text[i]
		}
	}
	return out
}
