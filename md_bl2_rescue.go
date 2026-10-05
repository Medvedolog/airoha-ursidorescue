package main

import (
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ursidorescue/app"
)

const rescueBL2PreloaderOffset = 0x800

func buildRescueBL2Image(preloader []byte) ([]byte, error) {
	if len(preloader) == 0 || len(preloader) > bl2Size-rescueBL2PreloaderOffset {
		return nil, fmt.Errorf("preloader size %d does not fit BL2", len(preloader))
	}
	img := make([]byte, bl2Size)
	for i := range img {
		img[i] = 0xff
	}
	copy(img[rescueBL2PreloaderOffset:], preloader)
	return img, nil
}

func (a *App) rescueBlindCommand(s Serial, command string, settle time.Duration) error {
	if e := a.stopBeforeCommand(command); e != nil {
		return e
	}
	a.event("U-Boot(rescue/noisy): " + command)
	_ = s.ResetInput()
	if e := sendLine(s, command); e != nil {
		return e
	}
	time.Sleep(settle)
	a.waitQuiet(s, 500*time.Millisecond, 2*time.Second)
	return nil
}

func (a *App) rescueBL2BadBlockGate(s Serial) error {
	var last error
	for attempt := 1; attempt <= 8; attempt++ {
		bad, e := a.mtdBad(s, "bl2", bl2Size)
		if e == nil {
			if len(bad) != 0 {
				return fmt.Errorf(L("bad-блок в bl2: %v; запись запрещена", "bad block in bl2: %v; write blocked"), bad)
			}
			a.status("BL2", fmt.Sprintf(L("mtd bad bl2 PASS (попытка %d)", "mtd bad bl2 PASS (attempt %d)"), attempt), app.LevelOK)
			return nil
		}
		last = e
		a.event(fmt.Sprintf(L("mtd bad bl2: шумный UART, повтор %d/8: %v", "mtd bad bl2: noisy UART, retry %d/8: %v"), attempt, e))
		a.waitQuiet(s, 350*time.Millisecond, 2*time.Second)
	}
	return fmt.Errorf(L("не удалось надёжно проверить bad-блоки bl2: %w", "could not reliably check bl2 bad blocks: %w"), last)
}

func (a *App) rescueFIPVolume(s Serial) (ubiVol, error) {
	if e := a.rescueBlindCommand(s, "ubi part ubi", 3*time.Second); e != nil {
		return ubiVol{}, e
	}
	for attempt := 1; attempt <= 12; attempt++ {
		a.event(fmt.Sprintf(L("Аварийное чтение UBI layout: попытка %d/12", "Rescue UBI layout read: attempt %d/12"), attempt))
		_ = s.ResetInput()
		if e := sendLine(s, "ubi info layout"); e != nil {
			return ubiVol{}, e
		}
		out := a.waitQuiet(s, 900*time.Millisecond, 6*time.Second)
		var fip []ubiVol
		for _, v := range parseUBIVolumes(out) {
			if v.Name == "fip" {
				fip = append(fip, v)
			}
		}
		if len(fip) == 1 && fip[0].Type == 4 && fip[0].ReservedPEBs != 0 && fip[0].UsableLEB != 0 {
			a.status("UART", fmt.Sprintf(L("UBI fip распознан через шум с попытки %d", "UBI fip parsed through UART noise on attempt %d"), attempt), app.LevelOK)
			return fip[0], nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return ubiVol{}, errors.New(L(
		"не удалось получить достаточно целый UBI layout за 12 попыток; flash не записывалась",
		"could not obtain a sufficiently intact UBI layout in 12 attempts; flash was not written"))
}

func (a *App) rescueConfirm(title string) error {
	v, e := a.ui.Ask(app.AskRequest{
		Kind:    app.AskText,
		Title:   title,
		Prompt:  L("Продолжить? [y/N]: ", "Continue? [y/N]: "),
		Quick:   []app.Choice{{Key: "y", Label: L("Да", "Yes")}, {Key: "n", Label: L("Нет", "No")}},
		Default: "n",
	})
	if e != nil {
		return e
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "y", "yes", "д", "да":
		return nil
	default:
		return cancelledError{L("аварийная запись отменена пользователем", "emergency write cancelled by the user")}
	}
}

func (a *App) rescueVerifyMTDCRC(s Serial, target string, off, size uint64, want uint32) error {
	if e := a.rescueBlindCommand(s, fmt.Sprintf("mw.b 0x%x 0x00 0x%x", verifyAddr, size), 2*time.Second); e != nil {
		return e
	}
	if e := a.rescueBlindCommand(s, fmt.Sprintf("mtd read %s 0x%x 0x%x 0x%x", target, verifyAddr, off, size), 8*time.Second); e != nil {
		return e
	}
	return a.rescueCRCAt(s, verifyAddr, size, want, "BL2")
}

func (a *App) rescueVerifyFIPCRC(s Serial, size uint64, want uint32) error {
	if e := a.rescueBlindCommand(s, fmt.Sprintf("mw.b 0x%x 0x00 0x%x", verifyAddr, size), 2*time.Second); e != nil {
		return e
	}
	if e := a.rescueBlindCommand(s, fmt.Sprintf("ubi read 0x%x fip 0x%x", verifyAddr, size), 12*time.Second); e != nil {
		return e
	}
	return a.rescueCRCAt(s, verifyAddr, size, want, "FIP")
}

func (a *App) rescueCRCAt(s Serial, addr, size uint64, want uint32, label string) error {
	wantRE := regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, want))
	cmd := fmt.Sprintf("crc32 0x%x 0x%x", addr, size)
	for attempt := 1; attempt <= 24; attempt++ {
		a.event(fmt.Sprintf(L("CRC32 %s через шумный UART: попытка %d/24", "CRC32 %s over noisy UART: attempt %d/24"), label, attempt))
		_ = s.ResetInput()
		if e := sendLine(s, cmd); e != nil {
			return e
		}
		out := a.waitQuiet(s, 700*time.Millisecond, 4*time.Second)
		if wantRE.Match(out) {
			a.status("OK", fmt.Sprintf("%s CRC32 PASS: %08x", label, want), app.LevelOK)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf(L(
		"не удалось увидеть ожидаемый CRC32 %s %08x за 24 попытки; запись не подтверждена",
		"expected %s CRC32 %08x was not observed in 24 attempts; the write is not verified"), label, want)
}

func (a *App) rescueProfileBL2(p Profile) ([]byte, string, uint32, error) {
	prePath := filepath.Join(a.root, filepath.FromSlash(p.PreloaderRel))
	pre, e := os.ReadFile(prePath)
	if e != nil {
		return nil, "", 0, e
	}
	if int64(len(pre)) != p.PreloaderSize {
		return nil, "", 0, fmt.Errorf("%s preloader size %d != pinned %d", p.ID, len(pre), p.PreloaderSize)
	}
	preSHA := shaHex(pre)
	if !strings.EqualFold(preSHA, p.PreloaderSHA) {
		return nil, "", 0, fmt.Errorf("%s preloader SHA256 mismatch: %s", p.ID, preSHA)
	}
	img, e := buildRescueBL2Image(pre)
	if e != nil {
		return nil, "", 0, e
	}
	return img, preSHA, crc32.ChecksumIEEE(img), nil
}

func (a *App) rescueProfileFIP(p Profile) ([]byte, installReport, error) {
	var rep installReport
	if p.ID == "md" {
		fip, e := os.ReadFile(filepath.Join(a.root, filepath.FromSlash(p.BootRel)))
		if e != nil {
			return nil, rep, e
		}
		if int64(len(fip)) != p.BootSize || !strings.EqualFold(shaHex(fip), p.BootSHA) {
			return nil, rep, errors.New("MD pinned UrsusBoot FIP size/SHA mismatch")
		}
		l, e := mdCheckFIP(fip)
		if e != nil {
			return nil, rep, e
		}
		nt := l.find(uuidNTFW)[0]
		rep = installReport{Method: "md-pinned-full-fip", TargetSHA: shaHex(fip), Entries: len(l.Entries), NTOff: nt.Off, TargetNTSize: nt.Size, TargetEnd: l.End}
		return fip, rep, nil
	}
	if p.ID != "mf" {
		return nil, rep, fmt.Errorf("unsupported profile %q", p.ID)
	}
	van, e := os.ReadFile(filepath.Join(a.root, filepath.FromSlash(p.VanillaRel)))
	if e != nil {
		return nil, rep, e
	}
	bl33, e := os.ReadFile(filepath.Join(a.root, filepath.FromSlash(p.BootRel)))
	if e != nil {
		return nil, rep, e
	}
	if int64(len(van)) != p.VanillaSize || !strings.EqualFold(shaHex(van), p.VanillaSHA) {
		return nil, rep, errors.New("MF pinned vanilla FIP size/SHA mismatch")
	}
	if int64(len(bl33)) != p.BootSize || !strings.EqualFold(shaHex(bl33), p.BootSHA) {
		return nil, rep, errors.New("MF pinned UrsusBoot BL33 size/SHA mismatch")
	}
	fip, rep, e := mfDerivePinnedFIP(van, bl33)
	if e != nil {
		return nil, rep, fmt.Errorf("MF rescue FIP build: %w", e)
	}
	rep.Method = "mf-pinned-vanilla-plus-ursusboot-bl33"
	return fip, rep, nil
}

func (a *App) bl2RescueWizard() error {
	a.showNetworkPrerequisites()
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, p, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()

	img, preSHA, imgCRC, e := a.rescueProfileBL2(p)
	if e != nil {
		return e
	}
	if e = a.rescueBL2BadBlockGate(s); e != nil {
		return e
	}
	target, e := a.saveInstallFile(p.ID+"-bl2-rescue.bin", L("Кандидат BL2:", "BL2 candidate:"), img)
	if e != nil {
		return e
	}
	imgSHA := shaHex(img)
	a.event(fmt.Sprintf(L(
		"%s BL2 candidate PASS: 0x%x байт; FF prefix=0x800; preloader=%d байт; CRC32=%08x; SHA256=%s",
		"%s BL2 candidate PASS: 0x%x bytes; FF prefix=0x800; preloader=%d bytes; CRC32=%08x; SHA256=%s"),
		strings.ToUpper(p.ID), len(img), p.PreloaderSize, imgCRC, imgSHA))
	if _, e = a.tftpLoad(s, target, "ursido-bl2.bin", loadAddr); e != nil {
		return e
	}

	title := fmt.Sprintf(L(
		"\nАВАРИЙНОЕ ВОССТАНОВЛЕНИЕ BL2: %s / %s. Будет стёрт и записан ТОЛЬКО раздел bl2 (128 КиБ). UBI/fip не трогаются.\nPreloader SHA256: %s\nBL2 image SHA256: %s",
		"\nEMERGENCY BL2 RECOVERY: %s / %s. ONLY the 128 KiB bl2 partition will be erased and written. UBI/fip are untouched.\nPreloader SHA256: %s\nBL2 image SHA256: %s"),
		p.Model, p.SoC, preSHA, imgSHA)
	if e = a.rescueConfirm(title); e != nil {
		return e
	}

	a.cancelBlocked(L("стирание, запись и проверка BL2", "erasing, writing and verifying BL2"))
	if e = a.rescueBlindCommand(s, "mtd erase bl2", 10*time.Second); e != nil {
		return e
	}
	if e = a.rescueBlindCommand(s, fmt.Sprintf("mtd write bl2 0x%x 0x0 0x%x", loadAddr, bl2Size), 12*time.Second); e != nil {
		return e
	}
	if e = a.rescueVerifyMTDCRC(s, "bl2", 0, bl2Size, imgCRC); e != nil {
		return e
	}
	a.cancelNow()
	a.event(fmt.Sprintf(L("BL2 %s/%s восстановлен и проверен; UBI/fip не изменялись.", "BL2 %s/%s restored and verified; UBI/fip were not modified."), p.ID, p.SoC))
	a.noteln(L("Можно выполнить reset. Нажмите Enter для reset или введите N чтобы оставить RAM U-Boot.", "Ready to reset. Press Enter to reset or type N to stay in RAM U-Boot."))
	if a.askResetOrStay() {
		_ = sendLine(s, "reset")
	}
	return nil
}

func (a *App) bootChainRescueWizard() error {
	a.showNetworkPrerequisites()
	pref, e := chooseProfileInteractive(a)
	if e != nil {
		return e
	}
	s, p, _, e := a.acquireRAMUBoot(pref)
	if e != nil {
		return e
	}
	defer func() { s.Close(); a.closeLog() }()

	bl2, preSHA, bl2CRC, e := a.rescueProfileBL2(p)
	if e != nil {
		return e
	}
	fip, rep, e := a.rescueProfileFIP(p)
	if e != nil {
		return e
	}
	if e = a.rescueBL2BadBlockGate(s); e != nil {
		return e
	}
	vol, e := a.rescueFIPVolume(s)
	if e != nil {
		return e
	}
	if cap := vol.capacity(); cap != 0 && uint64(len(fip)) > cap {
		return fmt.Errorf(L("новый FIP (%d) больше тома fip (%d)", "new FIP (%d) is larger than the fip volume (%d)"), len(fip), cap)
	}

	fipPath, e := a.saveInstallFile(p.ID+"-bootchain-fip.bin", L("Кандидат FIP:", "FIP candidate:"), fip)
	if e != nil {
		return e
	}
	bl2Path, e := a.saveInstallFile(p.ID+"-bootchain-bl2.bin", L("Кандидат BL2:", "BL2 candidate:"), bl2)
	if e != nil {
		return e
	}
	a.reportInstall(rep)
	a.event(fmt.Sprintf(L("Boot-chain FIP PASS: %d байт; CRC32=%08x; SHA256=%s", "Boot-chain FIP PASS: %d bytes; CRC32=%08x; SHA256=%s"), len(fip), crc32.ChecksumIEEE(fip), shaHex(fip)))
	a.event(fmt.Sprintf(L("Boot-chain BL2 PASS: 0x%x байт; CRC32=%08x; SHA256=%s", "Boot-chain BL2 PASS: 0x%x bytes; CRC32=%08x; SHA256=%s"), len(bl2), bl2CRC, shaHex(bl2)))

	if _, e = a.tftpLoad(s, fipPath, "ursido-bootchain-fip.bin", loadAddr); e != nil {
		return e
	}
	title := fmt.Sprintf(L(
		"\nПОЛНОЕ ВОССТАНОВЛЕНИЕ ЗАГРУЗОЧНОЙ ЦЕПОЧКИ: %s / %s.\n1) перезаписать и проверить UBI-том fip (BL31 + BL33/U-Boot);\n2) последним шагом стереть, записать и проверить BL2.\nДругие UBI-тома не трогаются. Старые BL2/FIP не резервируются в этом аварийном режиме.\nPreloader SHA256: %s\nFIP SHA256: %s",
		"\nFULL BOOT-CHAIN RECOVERY: %s / %s.\n1) overwrite and verify UBI volume fip (BL31 + BL33/U-Boot);\n2) erase, write and verify BL2 LAST.\nOther UBI volumes are untouched. Old BL2/FIP are not backed up in this emergency mode.\nPreloader SHA256: %s\nFIP SHA256: %s"),
		p.Model, p.SoC, preSHA, shaHex(fip))
	if e = a.rescueConfirm(title); e != nil {
		return e
	}

	a.cancelBlocked(L("запись FIP и BL2 с проверкой; BL2 записывается последним", "writing and verifying FIP and BL2; BL2 is written last"))
	fipCRC := crc32.ChecksumIEEE(fip)
	if e = a.rescueBlindCommand(s, fmt.Sprintf("ubi write 0x%x fip 0x%x", loadAddr, len(fip)), 18*time.Second); e != nil {
		return e
	}
	if e = a.rescueVerifyFIPCRC(s, uint64(len(fip)), fipCRC); e != nil {
		return e
	}
	a.status("OK", L("FIP записан и проверен; перехожу к BL2 (последний шаг)", "FIP written and verified; moving to BL2 (last step)"), app.LevelOK)

	if _, e = a.tftpLoad(s, bl2Path, "ursido-bootchain-bl2.bin", loadAddr); e != nil {
		return e
	}
	if e = a.rescueBlindCommand(s, "mtd erase bl2", 10*time.Second); e != nil {
		return e
	}
	if e = a.rescueBlindCommand(s, fmt.Sprintf("mtd write bl2 0x%x 0x0 0x%x", loadAddr, bl2Size), 12*time.Second); e != nil {
		return e
	}
	if e = a.rescueVerifyMTDCRC(s, "bl2", 0, bl2Size, bl2CRC); e != nil {
		return e
	}
	a.cancelNow()
	a.event(fmt.Sprintf(L(
		"Полная загрузочная цепочка %s/%s восстановлена: FIP + BL2 PASS; BL2 записан последним.",
		"Full %s/%s boot chain restored: FIP + BL2 PASS; BL2 was written last."),
		p.ID, p.SoC))
	a.noteln(L("Можно выполнить reset. Нажмите Enter для reset или введите N чтобы оставить RAM U-Boot.", "Ready to reset. Press Enter to reset or type N to stay in RAM U-Boot."))
	if a.askResetOrStay() {
		_ = sendLine(s, "reset")
	}
	return nil
}
