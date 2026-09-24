package main

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"ursidorescue/app"
)

const (
	uiReset  = "0"
	uiBold   = "1"
	uiInk    = "38;2;242;232;218"
	uiMuted  = "38;2;168;145;121"
	uiAmber  = "1;38;2;217;154;77"
	uiAmber2 = "1;38;2;200;135;58"
	uiSand   = "1;38;2;239;192;121"
	uiOK     = "1;38;2;124;196;147"
	uiBad    = "1;38;2;232;131;122"
)

func consoleColorEnabled() bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func paint(s, style string) string {
	if s == "" || !consoleColorEnabled() {
		return s
	}
	return "\x1b[" + style + "m" + s + "\x1b[0m"
}

func uiRule(title, style string) {
	line := strings.Repeat("─", 84)
	if title != "" {
		label := " " + title + " "
		if len([]rune(label)) < 76 {
			line = "────" + label + strings.Repeat("─", 80-len([]rune(label)))
		}
	}
	fmt.Println(paint(line, style))
}

func uiStatus(label, msg, style string) {
	fmt.Printf("%s %s\n", paint("["+label+"]", style), paint(msg, uiInk))
}

func eventTone(s string) string {
	u := strings.ToUpper(s)
	switch {
	case strings.Contains(u, "PASS") || strings.Contains(u, "ГОТОВО") || strings.Contains(u, "ЗАВЕРШЕН"):
		return uiOK
	case strings.Contains(u, "STOP") || strings.Contains(u, "FAIL") || strings.Contains(u, "ERROR") ||
		strings.Contains(u, "ОШИБ") || strings.Contains(u, "ОТМЕН") || strings.Contains(u, "CANCEL"):
		return uiBad
	case strings.Contains(u, "WARN") || strings.Contains(u, "ВНИМАН"):
		return uiSand
	case strings.Contains(u, "TFTP") || strings.Contains(u, "XMODEM") ||
		strings.Contains(u, "ОЖИД") || strings.Contains(u, "WAIT"):
		return uiAmber2
	default:
		return uiInk
	}
}

func uiEvent(ts, msg string) {
	fmt.Printf("%s %s\n", paint("["+ts+"]", uiMuted), paint(msg, eventTone(msg)))
}

func activeIPv4Interfaces() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, iface := range ifs {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			out = append(out, fmt.Sprintf("%s=%s", iface.Name, ip.String()))
		}
	}
	sort.Strings(out)
	return out
}

// showNetworkPrerequisites reports the LAN/TFTP checklist as one section of
// labelled events; each front end draws it its own way.
func (a *App) showNetworkPrerequisites() {
	a.ui.Event(app.Event{Kind: app.KindSectionStart, Text: L("СЕТЕВЫЕ ПРЕРЕКВИЗИТЫ", "NETWORK PREREQUISITES")})
	a.status(L("КАБЕЛЬ", "CABLE"),
		L("ПК напрямую к Nokia; для recovery используйте LAN2 или LAN3. LAN1 не рекомендуется; LAN4 лучше не использовать для переходов/boot.", "Connect PC directly to Nokia; use LAN2 or LAN3 for recovery. LAN1 is not recommended; avoid LAN4 for transition/boot workflows."),
		app.LevelWarn)
	a.status("IP",
		L("Nokia RAM U-Boot: 192.168.1.1/24. ПК: статический 192.168.1.254/24 (или другой свободный 192.168.1.x); DHCP на recovery NIC выключить.", "Nokia RAM U-Boot: 192.168.1.1/24. PC: static 192.168.1.254/24 (or another free 192.168.1.x); disable DHCP on the recovery NIC."),
		app.LevelInfo)
	a.status("TFTP",
		fmt.Sprintf(L("Встроенный сервер UrsidoRescue слушает UDP/%d; отдельный TFTP-сервер не нужен.", "Built-in UrsidoRescue server listens on UDP/%d; no external TFTP server is required."), defaultTFTPPort),
		app.LevelInfo)
	a.status(L("АДАПТЕРЫ", "ADAPTERS"),
		L("На время recovery отключите Wi-Fi, VPN, прочие Ethernet, Hyper-V/виртуальные адаптеры и туннели. Оставьте только NIC, подключённый к Nokia.", "During recovery disable Wi-Fi, VPN, other Ethernet, Hyper-V/virtual adapters and tunnels. Leave only the NIC connected to Nokia."),
		app.LevelWarn)
	active := activeIPv4Interfaces()
	if len(active) > 0 {
		level := app.LevelNote
		if len(active) > 1 {
			level = app.LevelWarn
		}
		a.status(L("СЕЙЧАС", "ACTIVE"), strings.Join(active, "  "), level)
		if len(active) > 1 {
			a.status(L("ВНИМАНИЕ", "WARNING"),
				L("Видно несколько активных IPv4-интерфейсов. Это не блокирует запуск, но перед TFTP лишние интерфейсы лучше погасить.", "Multiple active IPv4 interfaces are visible. This does not block startup, but disable the extras before TFTP."),
				app.LevelWarn)
		}
	}
	a.ui.Event(app.Event{Kind: app.KindSectionEnd})
}
