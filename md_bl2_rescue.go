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

const mdBL2PreloaderOffset = 0x800

func buildMDBL2Image(preloader []byte) ([]byte, error) {
	if len(preloader) == 0 || len(preloader) > bl2Size-mdBL2PreloaderOffset {
		return nil, fmt.Errorf("MD preloader size %d does not fit BL2", len(preloader))
	}
	img := make([]byte, bl2Size)
	for i := range img {
		img[i] = 0xff
	}
	copy(img[mdBL2PreloaderOffset:], preloader)
	return img, nil
}

func (a *App) mdBL2BlindCommand(s Serial, command string, settle time.Duration) error {
	if e := a.stopBeforeCommand(command); e != nil {
		return e
	}
	a.event("U-Boot(MD BL2 rescue): " + command)
	_ = s.ResetInput()
	if e := sendLine(s, command); e != nil {
		return e
	}
	time.Sleep(settle)
	a.waitQuiet(s, 500*time.Millisecond, 2*time.Second)
	return nil
}

func (a *App) mdBL2BadBlockGate(s Serial) error {
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

func (a *App) mdBL2Confirm(p Profile, imageSHA string) error {
	title := fmt.Sprintf(L(
		"\nАВАРИЙНОЕ ВОССТАНОВЛЕНИЕ BL2: Nokia XG-040G-MD / AN7581. Будет стёрт и записан ТОЛЬКО раздел bl2 (128 КиБ). UBI/fip не трогаются.\nUrsusBoot preloader: %s\nBL2 image SHA256: %s",
		"\nEMERGENCY BL2 RECOVERY: Nokia XG-040G-MD / AN7581. ONLY the 128 KiB bl2 partition will be erased and written. UBI/fip are untouched.\nUrsusBoot preloader: %s\nBL2 image SHA256: %s"),
		p.PreloaderRel, imageSHA)
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
		return cancelledError{L("восстановление BL2 отменено пользователем", "BL2 recovery cancelled by the user")}
	}
}

func (a *App) mdBL2VerifyNoisy(s Serial, want uint32) error {
	if e := a.mdBL2BlindCommand(s, fmt.Sprintf("mw.b 0x%x 0x00 0x%x", verifyAddr, bl2Size), 2*time.Second); e != nil {
		return e
	}
	if e := a.mdBL2BlindCommand(s, fmt.Sprintf("mtd read bl2 0x%x 0x0 0x%x", verifyAddr, bl2Size), 8*time.Second); e != nil {
		return e
	}

	wantRE := regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, want))
	cmd := fmt.Sprintf("crc32 0x%x 0x%x", verifyAddr, bl2Size)
	for attempt := 1; attempt <= 24; attempt++ {
		a.event(fmt.Sprintf(L("CRC32 BL2 через шумный UART: попытка %d/24", "BL2 CRC32 over noisy UART: attempt %d/24"), attempt))
		_ = s.ResetInput()
		if e := sendLine(s, cmd); e != nil {
			return e
		}
		out := a.waitQuiet(s, 700*time.Millisecond, 4*time.Second)
		if wantRE.Match(out) {
			a.status("OK", fmt.Sprintf(L("BL2 CRC32 PASS: %08x", "BL2 CRC32 PASS: %08x"), want), app.LevelOK)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf(L(
		"не удалось увидеть ожидаемый CRC32 BL2 %08x за 24 попытки; запись не подтверждена",
		"expected BL2 CRC32 %08x was not observed in 24 attempts; the write is not verified"), want)
}

func (a *App) installMDBL2Rescue(s Serial, p Profile) error {
	if p.ID != "md" || p.SoC != "AN7581" {
		return errors.New(L("этот аварийный режим разрешён только для Nokia XG-040G-MD / AN7581", "this rescue mode is allowed only for Nokia XG-040G-MD / AN7581"))
	}

	prePath := filepath.Join(a.root, filepath.FromSlash(p.PreloaderRel))
	pre, e := os.ReadFile(prePath)
	if e != nil {
		return e
	}
	if int64(len(pre)) != p.PreloaderSize {
		return fmt.Errorf("MD preloader size %d != pinned %d", len(pre), p.PreloaderSize)
	}
	preSHA := shaHex(pre)
	if !strings.EqualFold(preSHA, p.PreloaderSHA) {
		return fmt.Errorf("MD preloader SHA256 mismatch: %s", preSHA)
	}

	img, e := buildMDBL2Image(pre)
	if e != nil {
		return e
	}
	imgSHA := shaHex(img)
	imgCRC := crc32.ChecksumIEEE(img)

	if e = a.mdBL2BadBlockGate(s); e != nil {
		return e
	}

	target, e := a.saveInstallFile("md-an7581-bl2-rescue.bin", L("Кандидат BL2:", "BL2 candidate:"), img)
	if e != nil {
		return e
	}
	a.event(fmt.Sprintf(L(
		"MD BL2 candidate PASS: 0x%x байт; FF prefix=0x800; preloader=%d байт; CRC32=%08x; SHA256=%s",
		"MD BL2 candidate PASS: 0x%x bytes; FF prefix=0x800; preloader=%d bytes; CRC32=%08x; SHA256=%s"),
		len(img), len(pre), imgCRC, imgSHA))

	if _, e = a.tftpLoad(s, target, "ursido-md-bl2.bin", loadAddr); e != nil {
		return e
	}
	if e = a.mdBL2Confirm(p, imgSHA); e != nil {
		return e
	}

	a.cancelBlocked(L("стирание, запись и проверка BL2", "erasing, writing and verifying BL2"))
	if e = a.mdBL2BlindCommand(s, "mtd erase bl2", 8*time.Second); e != nil {
		return e
	}
	if e = a.mdBL2BlindCommand(s, fmt.Sprintf("mtd write bl2 0x%x 0x0 0x%x", loadAddr, bl2Size), 10*time.Second); e != nil {
		return e
	}
	if e = a.mdBL2VerifyNoisy(s, imgCRC); e != nil {
		return e
	}
	a.cancelNow()

	a.event(fmt.Sprintf(L(
		"MD BL2 восстановлен и проверен. AN7581 preloader SHA256=%s; UBI/fip не изменялись.",
		"MD BL2 restored and verified. AN7581 preloader SHA256=%s; UBI/fip were not modified."),
		preSHA))
	a.noteln(L("Можно выполнить reset. Нажмите Enter для reset или введите N чтобы оставить RAM U-Boot.", "Ready to reset. Press Enter to reset or type N to stay in RAM U-Boot."))
	if a.askResetOrStay() {
		_ = sendLine(s, "reset")
	}
	return nil
}
