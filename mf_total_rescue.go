package main

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ursidorescue/app"
)

const (
	mfRescueFIPVolumeSize = 0x00100000
	mfRescueBL2Addr       = 0x91000000
)

type mfRescueTransport string

const (
	mfRescueUART mfRescueTransport = "uart-xmodem"
	mfRescueTFTP mfRescueTransport = "tftp"
)

func (a *App) mfTotalRescueUART() error {
	return a.mfTotalRescue(mfRescueUART)
}

func (a *App) mfTotalRescueTFTP() error {
	return a.mfTotalRescue(mfRescueTFTP)
}

func (a *App) stableBadBlocks(s Serial, part string, size uint64) ([]uint64, error) {
	var prev []uint64
	stable := 0
	var last error
	for attempt := 1; attempt <= 6; attempt++ {
		cur, err := a.mtdBad(s, part, size)
		if err != nil {
			last = err
		} else {
			last = nil
			if fmt.Sprint(cur) == fmt.Sprint(prev) {
				stable++
			} else {
				stable = 1
				prev = append(prev[:0], cur...)
			}
			if stable >= 2 {
				return append([]uint64(nil), cur...), nil
			}
		}
		a.event(fmt.Sprintf(L(
			"BBT %s: повтор чтения %d/6 — нужна одинаковая карта два раза подряд (%v)",
			"BBT %s: read retry %d/6 — need the same map twice in a row (%v)"),
			part, attempt, last))
		a.waitQuiet(s, 250*time.Millisecond, 1200*time.Millisecond)
		_ = s.ResetInput()
	}
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf(L("BBT %s не стабилизировалась за 6 чтений", "BBT %s did not stabilize in 6 reads"), part)
}

func missingBadBlocks(before, after []uint64) []uint64 {
	have := make(map[uint64]struct{}, len(after))
	for _, x := range after {
		have[x] = struct{}{}
	}
	var missing []uint64
	for _, x := range before {
		if _, ok := have[x]; !ok {
			missing = append(missing, x)
		}
	}
	return missing
}

func (a *App) waitLoadXReady(s Serial, timeout time.Duration) error {
	end := time.Now().Add(timeout)
	var out []byte
	buf := make([]byte, 512)
	for time.Now().Before(end) {
		n, err := s.Read(buf, 150*time.Millisecond)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		d := append([]byte(nil), buf[:n]...)
		a.logBytes(d, true)
		out = append(out, d...)
		if len(out) > 16384 {
			out = out[len(out)-8192:]
		}
		low := bytes.ToLower(out)
		if bytes.Contains(low, []byte("unknown command")) || bytes.Contains(low, []byte("usage:")) {
			return errors.New(L("RAM U-Boot отклонил loadx", "RAM U-Boot rejected loadx"))
		}
		if pos := bytes.Index(low, []byte("ready for binary (xmodem)")); pos >= 0 && bytes.Contains(out[pos:], []byte{'C'}) {
			return nil
		}
	}
	return errors.New(L("RAM U-Boot не запросил XMODEM-CRC после loadx", "RAM U-Boot did not request XMODEM-CRC after loadx"))
}

func (a *App) xmodemLoadRAM(s Serial, path, label string, addr uint64) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() <= 0 || st.Size() > maxGenericRAMFile {
		return fmt.Errorf(L("XMODEM: недопустимый размер %d", "XMODEM: invalid size %d"), st.Size())
	}

	a.event(fmt.Sprintf("U-Boot: loadx 0x%x (%s)", addr, label))
	a.waitQuiet(s, 180*time.Millisecond, time.Second)
	_ = s.ResetInput()
	if err = sendLine(s, fmt.Sprintf("loadx 0x%x", addr)); err != nil {
		return err
	}
	if err = a.waitLoadXReady(s, 30*time.Second); err != nil {
		return err
	}

	res, err := a.xmodemSend(s, path, label)
	if err != nil {
		return err
	}
	if len(res.Trailing) > 0 {
		a.ui.Output(append([]byte(nil), res.Trailing...))
	}
	if !promptPresent(res.Trailing) {
		if _, err = a.readUntilPrompt(s, 45*time.Second, "loadx"); err != nil {
			return err
		}
	}
	a.cancelNow()
	if err = a.verifyRAM(s, path, addr); err != nil {
		return err
	}
	a.status("XMODEM", fmt.Sprintf(L("%s: %d байт в RAM, проверка PASS", "%s: %d bytes in RAM, verification PASS"), label, st.Size()), app.LevelOK)
	return nil
}

func (a *App) mfLoadRescuePayload(s Serial, transport mfRescueTransport, path, remote, label string, addr uint64) error {
	switch transport {
	case mfRescueUART:
		return a.xmodemLoadRAM(s, path, label, addr)
	case mfRescueTFTP:
		_, err := a.tftpLoad(s, path, remote, addr)
		return err
	default:
		return fmt.Errorf("unknown rescue transport %q", transport)
	}
}

func validateFreshFIPVolume(vols []ubiVol, payloadSize uint64) error {
	var fip []ubiVol
	for _, v := range vols {
		if v.Name == "fip" {
			fip = append(fip, v)
		}
	}
	if len(fip) != 1 {
		return fmt.Errorf("fresh UBI: expected exactly one fip volume, found %d", len(fip))
	}
	v := fip[0]
	if v.ID != 4 {
		return fmt.Errorf("fresh UBI: fip volume id=%d, expected 4", v.ID)
	}
	if v.Type != 4 {
		return fmt.Errorf("fresh UBI: fip volume type=%d, expected static", v.Type)
	}
	if v.ReservedPEBs == 0 || v.UsableLEB == 0 || v.capacity() < payloadSize {
		return fmt.Errorf("fresh UBI: invalid fip capacity %d for payload %d", v.capacity(), payloadSize)
	}
	return nil
}

func (a *App) mfTotalRescue(transport mfRescueTransport) error {
	if transport == mfRescueTFTP {
		a.showNetworkPrerequisites()
	} else {
		a.status("UART", L("Аварийный режим без сети: BootROM, RAM U-Boot, FIP и BL2 передаются только по UART/XMODEM", "Network-free rescue: BootROM, RAM U-Boot, FIP and BL2 use UART/XMODEM only"), app.LevelInfo)
	}

	expected := profiles["mf"]
	s, p, _, err := a.acquireRAMUBoot(expected)
	if err != nil {
		return err
	}
	defer func() { s.Close(); a.closeLog() }()
	if p.ID != "mf" || p.SoC != "AN7583" {
		return fmt.Errorf(L("полное восстановление пустой UBI разрешено только для MF/AN7583, обнаружен %s/%s", "empty-UBI total rescue is MF/AN7583-only; detected %s/%s"), p.ID, p.SoC)
	}

	bl2, preSHA, bl2CRC, err := a.rescueProfileBL2(p)
	if err != nil {
		return err
	}
	fip, rep, err := a.rescueProfileFIP(p)
	if err != nil {
		return err
	}
	if uint64(len(fip)) > mfRescueFIPVolumeSize {
		return fmt.Errorf("MF rescue FIP 0x%x exceeds canonical fip volume 0x%x", len(fip), mfRescueFIPVolumeSize)
	}
	a.reportInstall(rep)

	bl2Path, err := a.saveInstallFile("mf-total-rescue-bl2.bin", L("Аварийный BL2:", "Rescue BL2:"), bl2)
	if err != nil {
		return err
	}
	fipPath, err := a.saveInstallFile("mf-total-rescue-fip.bin", L("Аварийный FIP:", "Rescue FIP:"), fip)
	if err != nil {
		return err
	}

	blBad, err := a.stableBadBlocks(s, "bl2", bl2Size)
	if err != nil {
		return err
	}
	if len(blBad) != 0 {
		return errors.New(L("BL2 содержит bad eraseblock; автоматическое полное восстановление заблокировано", "BL2 contains a bad eraseblock; automatic total rescue is blocked"))
	}
	ubiBadBefore, err := a.stableBadBlocks(s, "ubi", ubiSize)
	if err != nil {
		return err
	}
	a.status("BBT", fmt.Sprintf(L("ubi до erase: bad=%d; UBI будет учитывать их при создании заново", "ubi before erase: bad=%d; fresh UBI will account for them"), len(ubiBadBefore)), app.LevelInfo)

	if err = a.mfLoadRescuePayload(s, transport, fipPath, "ursido-mf-total-fip.bin", "MF UrsusBoot FIP", loadAddr); err != nil {
		return err
	}
	if err = a.mfLoadRescuePayload(s, transport, bl2Path, "ursido-mf-total-bl2.bin", "MF BL2", mfRescueBL2Addr); err != nil {
		return err
	}
	// Both payloads are in distinct RAM ranges before the destructive phase,
	// so no network or XMODEM dependency remains after mtd erase ubi.
	if err = a.verifyRAM(s, fipPath, loadAddr); err != nil {
		return err
	}
	if err = a.verifyRAM(s, bl2Path, mfRescueBL2Addr); err != nil {
		return err
	}

	title := fmt.Sprintf(L(
		"\nПОЛНОЕ АВАРИЙНОЕ ВОССТАНОВЛЕНИЕ MF / AN7583 — UBI МОЖЕТ БЫТЬ ПОЛНОСТЬЮ УТЕРЯНА.\nТранспорт payload: %s.\n1) СТЕРЕТЬ ВЕСЬ MTD ubi (все существующие тома/настройки/OpenWrt будут уничтожены);\n2) создать чистую UBI и static volume fip ID 4 размером 0x100000;\n3) записать и проверить UrsusBoot FIP;\n4) ПОСЛЕДНИМ записать и проверить BL2.\nПосле reset: UrsusBoot Recovery -> MF UBI sysupgrade.\nPreloader SHA256: %s\nFIP SHA256: %s",
		"\nFULL MF / AN7583 DISASTER RECOVERY — UBI MAY BE COMPLETELY LOST.\nPayload transport: %s.\n1) ERASE the entire ubi MTD (all existing volumes/settings/OpenWrt are destroyed);\n2) create a fresh UBI and static fip volume ID 4, size 0x100000;\n3) write and verify the UrsusBoot FIP;\n4) write and verify BL2 LAST.\nAfter reset: UrsusBoot Recovery -> MF UBI sysupgrade.\nPreloader SHA256: %s\nFIP SHA256: %s"),
		transport, preSHA, shaHex(fip))
	if err = a.rescueConfirm(title); err != nil {
		return err
	}

	a.cancelBlocked(L("полное восстановление UBI/FIP/BL2 уже началось; остановка до проверки BL2 недоступна", "total UBI/FIP/BL2 recovery has started; stopping is unavailable until BL2 verification completes"))

	// Do not depend on the old UBI at all. Detach is best-effort housekeeping,
	// then erase the whole target exactly once. Lost UART completion text is
	// never a reason to replay a destructive command.
	_ = a.rescueBlindCommand(s, "ubi detach", 2*time.Second)
	if err = a.ubootPersistentOnce(s, "mtd erase ubi", 10*time.Minute); err != nil {
		return err
	}

	ubiBadAfter, err := a.stableBadBlocks(s, "ubi", ubiSize)
	if err != nil {
		return err
	}
	if missing := missingBadBlocks(ubiBadBefore, ubiBadAfter); len(missing) != 0 {
		return fmt.Errorf(L("после erase исчезли ранее отмеченные bad-блоки %v; дальнейшая запись остановлена", "previously marked bad blocks disappeared after erase %v; stopping before further writes"), missing)
	}
	a.status("BBT", fmt.Sprintf(L("ubi после erase: bad=%d · новых=%d", "ubi after erase: bad=%d · new=%d"), len(ubiBadAfter), countNewBadBlocks(ubiBadBefore, ubiBadAfter)), app.LevelOK)

	if err = a.ubootPersistentOnce(s, "ubi part ubi", 5*time.Minute); err != nil {
		return err
	}
	if _, err = a.ubootCommand(s, "ubi info", 60*time.Second); err != nil {
		return fmt.Errorf(L("свежая UBI не подключилась после erase: %w", "fresh UBI did not attach after erase: %w"), err)
	}

	if err = a.ubootPersistentOnce(s, fmt.Sprintf("ubi create fip 0x%x static 4", mfRescueFIPVolumeSize), 2*time.Minute); err != nil {
		return err
	}
	layout, err := a.ubootCommand(s, "ubi info layout", 60*time.Second)
	if err != nil {
		return err
	}
	if err = validateFreshFIPVolume(parseUBIVolumes(layout), uint64(len(fip))); err != nil {
		return err
	}
	a.status("UBI", L("fresh UBI PASS: static fip ID 4 создан", "fresh UBI PASS: static fip ID 4 created"), app.LevelOK)

	if err = a.ubiWriteVerified(s, "fip", uint64(len(fip)), crc32.ChecksumIEEE(fip)); err != nil {
		return err
	}
	a.status("FIP", L("UrsusBoot FIP записан и проверен; перехожу к BL2 последним", "UrsusBoot FIP written and verified; moving to BL2 last"), app.LevelOK)

	if err = a.ubootPersistentOnce(s, "mtd erase bl2", 2*time.Minute); err != nil {
		return err
	}
	if err = a.ubootPersistentOnce(s, fmt.Sprintf("mtd write bl2 0x%x 0x0 0x%x", mfRescueBL2Addr, bl2Size), 3*time.Minute); err != nil {
		return err
	}
	if err = a.readbackCRC(s, "bl2", 0, bl2Size, verifyAddr, bl2CRC); err != nil {
		return err
	}

	a.cancelNow()
	a.event(fmt.Sprintf(L(
		"MF total rescue PASS: fresh UBI + fip ID4 + BL2; FIP SHA256=%s; transport=%s",
		"MF total rescue PASS: fresh UBI + fip ID4 + BL2; FIP SHA256=%s; transport=%s"),
		shaHex(fip), transport))
	a.status("NEXT", L("Перезагрузите в UrsusBoot Recovery и установите MF UBI sysupgrade; старые OpenWrt volumes намеренно удалены", "Reboot into UrsusBoot Recovery and install the MF UBI sysupgrade; old OpenWrt volumes were intentionally removed"), app.LevelOK)
	a.noteln(L("Нажмите Enter для reset или N, чтобы остаться в RAM U-Boot.", "Press Enter to reset or N to stay in the RAM U-Boot."))
	if a.askResetOrStay() {
		_ = sendLine(s, "reset")
	}
	return nil
}

// Keep filepath imported here intentionally: the rescue payloads are pinned
// profile files and saveInstallFile materializes copies in the current session.
var _ = filepath.Separator
var _ = strings.Builder{}
