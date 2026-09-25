package main

// chromeParser follows the device's own screen control, for the chrome only
// (the pager keeps hasFullscreenANSI). It is a small streaming CSI reader:
// an escape sequence cut by a serial read is held until the next read, so a
// split "ESC[?104" + "9l" is seen as one sequence, at its real position.
//
// Two ways a device program takes the screen:
//   - alternate screen: ?1049h / ?1047h / ?47h … the matching "l";
//   - soft fullscreen (a U-Boot bootmenu draws on the main screen): the
//     cursor is hidden (?25l) and then the screen cleared or the cursor sent
//     home, until the cursor is shown again (?25h).
//
// A bare clear/home (the shell's "clear") or a bare ?25h/?25l does not take
// the screen: home is kept inside the scrolling area and the bars are
// redrawn after an erase.

type chromeAct int

const (
	actNone        chromeAct = iota
	actSuspend               // a program takes the screen: before its first byte
	actResume                // it left the alternate screen: the main screen, bars included, is back
	actResumeClear           // it showed the cursor after a soft fullscreen: redraw from clean
	actRefresh               // an erase may have wiped the bars: redraw them
)

const (
	modeNone = iota
	modeAlt
	modeSoft
)

// chromePiece is device bytes to write, then an action to apply.
type chromePiece struct {
	data []byte
	act  chromeAct
}

type chromeParser struct {
	pend   []byte // an unfinished escape sequence from the previous read
	hidden bool   // the device hid the cursor
	mode   int
}

const chromeCSIMax = 32

// feed splits d into pieces. keepHome rewrites a bare cursor-home to the top
// of the scrolling area (row 3); it is off when the chrome is not drawn.
func (p *chromeParser) feed(d []byte, keepHome bool) []chromePiece {
	buf := append(p.pend, d...)
	p.pend = nil
	var pieces []chromePiece
	var out []byte
	flush := func(act chromeAct) {
		pieces = append(pieces, chromePiece{data: out, act: act})
		out = nil
	}
	for i := 0; i < len(buf); i++ {
		c := buf[i]
		if c != 0x1b {
			out = append(out, c)
			continue
		}
		if i+1 >= len(buf) {
			p.pend = append([]byte(nil), buf[i:]...)
			break
		}
		if buf[i+1] != '[' {
			out = append(out, c)
			continue
		}
		j := i + 2
		for j < len(buf) && j-i < chromeCSIMax && (buf[j] < 0x40 || buf[j] > 0x7e) {
			j++
		}
		if j >= len(buf) {
			if j-i < chromeCSIMax {
				p.pend = append([]byte(nil), buf[i:]...)
				break
			}
			out = append(out, buf[i:]...)
			break
		}
		if j-i >= chromeCSIMax { // not a real CSI: pass it through
			out = append(out, c)
			continue
		}
		seq := buf[i : j+1]
		params, final := string(buf[i+2:j]), buf[j]
		i = j
		switch {
		case (final == 'h' || final == 'l') && (params == "?1049" || params == "?1047" || params == "?47"):
			if final == 'h' {
				if p.mode == modeNone {
					flush(actSuspend)
				}
				p.mode = modeAlt
				out = append(out, seq...)
			} else {
				out = append(out, seq...)
				if p.mode == modeAlt {
					p.mode = modeNone
					flush(actResume)
				}
			}
		case params == "?25" && final == 'l':
			p.hidden = true
			out = append(out, seq...)
		case params == "?25" && final == 'h':
			p.hidden = false
			out = append(out, seq...)
			if p.mode == modeSoft {
				p.mode = modeNone
				flush(actResumeClear)
			}
		case final == 'J' || final == 'H' || final == 'f':
			if p.mode == modeNone && p.hidden {
				flush(actSuspend)
				p.mode = modeSoft
				out = append(out, seq...)
				break
			}
			if p.mode != modeNone {
				out = append(out, seq...)
				break
			}
			if final != 'J' && keepHome && isCursorHome(params) {
				out = append(out, "\x1b[3;1H"...)
				break
			}
			out = append(out, seq...)
			if final == 'J' {
				flush(actRefresh)
			}
		default:
			out = append(out, seq...)
		}
	}
	if len(out) > 0 || len(pieces) == 0 {
		flush(actNone)
	}
	return pieces
}

func isCursorHome(params string) bool {
	switch params {
	case "", "1", "1;1", ";", "1;", ";1":
		return true
	}
	return false
}
