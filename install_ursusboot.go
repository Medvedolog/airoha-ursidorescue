package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ursidorescue/app"
)

// Expert → Install UrsusBoot (UART). The RAM U-Boot has no tftpput, so what
// the device carries is read back to the PC as an md.l hex dump over the UART
// and proved with the device's own crc32; the candidate is built on the PC
// (bootfip.go) and goes back over TFTP like every other write.

const uartDumpChunk = 0x10000 // one md.l command; its output stays well under the 1 MiB read cap

var mdlLine = regexp.MustCompile(`(?m)^([0-9a-fA-F]{8,16}):((?: [0-9a-fA-F]{8}){1,4})`)

// parseMDL turns U-Boot "md.l" output into bytes (little-endian words). The
// lines must run contiguously from addr and cover size bytes.
func parseMDL(out []byte, addr, size uint64) ([]byte, error) {
	buf := make([]byte, 0, size)
	for _, m := range mdlLine.FindAllSubmatch(out, -1) {
		if uint64(len(buf)) >= size {
			break
		}
		at, err := strconv.ParseUint(string(m[1]), 16, 64)
		if err != nil {
			return nil, err
		}
		if at != addr+uint64(len(buf)) {
			return nil, fmt.Errorf("md.l: line at 0x%x, expected 0x%x", at, addr+uint64(len(buf)))
		}
		for _, w := range strings.Fields(string(m[2])) {
			v, err := strconv.ParseUint(w, 16, 32)
			if err != nil {
				return nil, err
			}
			buf = binary.LittleEndian.AppendUint32(buf, uint32(v))
		}
	}
	if uint64(len(buf)) < size {
		return nil, fmt.Errorf("md.l: got %d of %d bytes", len(buf), size)
	}
	return buf[:size], nil
}

// uartDump reads size bytes of device RAM at addr over the UART. Each piece is
// checked against the device's crc32 and read again on a mismatch.
func (a *App) uartDump(s Serial, addr, size uint64, label string) ([]byte, error) {
	if size%4 != 0 {
		return nil, fmt.Errorf("dump size 0x%x is not a multiple of 4", size)
	}
	a.quietUART = true
	defer func() { a.quietUART = false }()
	out := make([]byte, 0, size)
	for off := uint64(0); off < size; off += uartDumpChunk {
		n := min(uint64(uartDumpChunk), size-off)
		var last error
		for attempt := 1; attempt <= 3; attempt++ {
			raw, err := a.ubootCommand(s, fmt.Sprintf("md.l 0x%x 0x%x", addr+off, n/4), 5*time.Minute)
			if err != nil {
				return nil, err
			}
			piece, err := parseMDL(raw, addr+off, n)
			if err == nil {
				err = a.deviceCRC(s, addr+off, n, crc32.ChecksumIEEE(piece))
			}
			if err == nil {
				out = append(out, piece...)
				last = nil
				break
			}
			last = err
			a.event(fmt.Sprintf(L("Чтение по UART: повтор 0x%x (%d/3): %v", "UART read: retrying 0x%x (%d/3): %v"), addr+off, attempt, err))
		}
		if last != nil {
			return nil, last
		}
		a.ui.Progress(app.Progress{Label: L("ЧТЕНИЕ", "READ"), Current: int64(off + n), Total: int64(size), Unit: "bytes",
			Detail: fmt.Sprintf("%s: %d/%d bytes", label, off+n, size)})
	}
	a.ui.Progress(app.Progress{Label: L("ЧТЕНИЕ", "READ"), Done: true})
	return out, nil
}

// deviceCRC runs crc32 on the device and requires the expected value.
func (a *App) deviceCRC(s Serial, addr, size uint64, want uint32) error {
	out, err := a.ubootCommand(s, fmt.Sprintf("crc32 0x%x 0x%x", addr, size), 2*time.Minute)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, want)).Match(out) {
		return fmt.Errorf(L("CRC32 на устройстве не совпал с прочитанным (ожидался %08x)", "device CRC32 does not match the data read (expected %08x)"), want)
	}
	return nil
}

// ubiVol is one volume of "ubi info layout" (U-Boot ubi_dump_vol_info).
type ubiVol struct {
	Name                    string
	Type                    int // 3 dynamic, 4 static
	UsedBytes               uint64
	ReservedPEBs, UsableLEB uint64
}

func (v ubiVol) capacity() uint64 { return v.ReservedPEBs * v.UsableLEB }

var ubiField = regexp.MustCompile(`(?m)^\s*(?:ubi\d*:\s*)?\s*(reserved_pebs|vol_type|usable_leb_size|used_bytes|name)\s+(\S+)`)

func parseUBIVolumes(out []byte) []ubiVol {
	var vols []ubiVol
	for _, block := range strings.Split(string(out), "Volume information dump:")[1:] {
		var v ubiVol
		seen := 0
		for _, m := range ubiField.FindAllStringSubmatch(block, -1) {
			n, _ := strconv.ParseUint(m[2], 10, 64)
			switch m[1] {
			case "reserved_pebs":
				v.ReservedPEBs = n
			case "vol_type":
				v.Type = int(n)
			case "usable_leb_size":
				v.UsableLEB = n
			case "used_bytes":
				v.UsedBytes = n
			case "name":
				v.Name = m[2]
			}
			seen++
		}
		if seen > 0 {
			vols = append(vols, v)
		}
	}
	return vols
}

// bootLayout says what the first 0x80000 of the NAND carry.
func (a *App) bootLayout(s Serial) (string, error) {
	if _, err := a.ubootCommand(s, fmt.Sprintf("mtd read ubi 0x%x 0x0 0x800", loadAddr), 60*time.Second); err != nil {
		return "", err
	}
	head, err := a.uartDump(s, loadAddr, 16, "ubi+0x0")
	if err != nil {
		return "", err
	}
	if bytes.HasPrefix(head, []byte("UBI#")) {
		return "ubi", nil
	}
	if _, err = a.ubootCommand(s, fmt.Sprintf("mtd read bl2 0x%x 0x0 0x1000", loadAddr), 60*time.Second); err != nil {
		return "", err
	}
	fip, err := a.uartDump(s, loadAddr+bootFIPOff, 16, "bl2+0x800")
	if err != nil {
		return "", err
	}
	if binary.LittleEndian.Uint32(fip) == fipMagic {
		return "stock", nil
	}
	return "", errors.New(L("разметка не распознана: нет ни UBI с 0x20000, ни FIP на 0x800 — установка не применима",
		"layout not recognised: neither UBI at 0x20000 nor a FIP at 0x800; the install does not apply"))
}

func (a *App) ursusBootInstallWizard() error {
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
	payload, e := os.ReadFile(filepath.Join(a.root, filepath.FromSlash(p.BootRel)))
	if e != nil {
		return e
	}
	if p.ID == "md" {
		if _, e = mdCheckFIP(payload); e != nil {
			return fmt.Errorf("UrsusBoot MD FIP: %w", e)
		}
	}
	a.event(fmt.Sprintf(L("UrsusBoot %s для %s: %s", "UrsusBoot %s for %s: %s"), p.BootVersion, p.Model, p.BootRel))
	layout, e := a.bootLayout(s)
	if e != nil {
		return e
	}
	if layout == "ubi" {
		a.event(L("Разметка: UBI (OpenWrt/UrsusBoot) — обновляется только UBI-том fip, BL2 не трогается",
			"Layout: UBI (OpenWrt/UrsusBoot); only the UBI volume fip is updated, BL2 is not touched"))
		e = a.installUBIFIP(s, p, payload)
	} else {
		a.event(L("Разметка: стоковая Nokia — FIP на 0x800 загрузочной области",
			"Layout: stock Nokia; FIP at 0x800 of the boot area"))
		e = a.installStockBootArea(s, p, payload)
	}
	if e != nil && !errors.Is(e, errAlreadyInstalled) {
		return e
	}
	a.noteln(L("Можно выполнить reset. Нажмите Enter для reset или введите N чтобы оставить RAM U-Boot.", "Ready to reset. Press Enter to reset or type N to stay in RAM U-Boot."))
	if a.askResetOrStay() {
		_ = sendLine(s, "reset")
	}
	return nil
}

var errAlreadyInstalled = errors.New("already installed")

// saveInstallFile keeps a before/target image in the session and reports it.
func (a *App) saveInstallFile(name, label string, data []byte) (string, error) {
	path := filepath.Join(a.sessionScratch("ursusboot"), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	a.ui.Artifact(app.Artifact{Kind: "ursusboot", Label: label, Path: path, SHA256: shaHex(data)})
	return path, nil
}

func (a *App) reportInstall(r installReport) {
	for _, l := range r.lines() {
		a.noteln("  " + l)
	}
}

func (a *App) installStockBootArea(s Serial, p Profile, payload []byte) error {
	bad, e := a.mtdBad(s, "bl2", bl2Size)
	if e != nil {
		return e
	}
	if len(bad) > 0 {
		return errors.New(L("bad-блок в bl2: установка заблокирована", "bad block in bl2: install blocked"))
	}
	if bad, e = a.mtdBad(s, "ubi", ubiSize); e != nil {
		return e
	}
	for _, b := range bad {
		if b < bootAreaSize-bl2Size {
			return fmt.Errorf(L("bad-блок в загрузочной области ubi+0x%x: установка заблокирована", "bad block in the boot area ubi+0x%x: install blocked"), b)
		}
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("mtd read bl2 0x%x 0x0 0x%x", loadAddr, bl2Size), 2*time.Minute); e != nil {
		return e
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("mtd read ubi 0x%x 0x0 0x%x", loadAddr+bl2Size, bootAreaSize-bl2Size), 2*time.Minute); e != nil {
		return e
	}
	a.note(L("Читаю загрузочную область 512 КиБ по UART (около 3–4 минут)…", "Reading the 512 KiB boot area over the UART (about 3–4 minutes)…"))
	live, e := a.uartDump(s, loadAddr, bootAreaSize, "boot area")
	if e != nil {
		return e
	}
	before, e := a.saveInstallFile("bootarea-before.bin", L("Бэкап загрузочной области:", "Boot area backup:"), live)
	if e != nil {
		return e
	}
	var cand []byte
	var rep installReport
	if p.ID == "md" {
		cand, rep, e = mdPlaceFIP(live, payload)
	} else {
		cand, rep, e = mfDeriveBootArea(live, payload)
	}
	if e != nil {
		return fmt.Errorf(L("кандидат не собран: %w", "candidate not built: %w"), e)
	}
	a.reportInstall(rep)
	if bytes.Equal(cand, live) {
		a.status("OK", fmt.Sprintf(L("UrsusBoot %s уже установлен — запись не нужна", "UrsusBoot %s is already installed; nothing to write"), p.BootVersion), app.LevelOK)
		return errAlreadyInstalled
	}
	var blocks []int
	for i := 0; i < bootAreaSize/eraseSize; i++ {
		if !bytes.Equal(cand[i*eraseSize:(i+1)*eraseSize], live[i*eraseSize:(i+1)*eraseSize]) {
			blocks = append(blocks, i)
		}
	}
	target, e := a.saveInstallFile("bootarea-target.bin", L("Новая загрузочная область:", "New boot area:"), cand)
	if e != nil {
		return e
	}
	if _, e = a.tftpLoad(s, target, "ursido-bootarea.bin", loadAddr); e != nil {
		return e
	}
	var spans []string
	for _, i := range blocks {
		spans = append(spans, fmt.Sprintf("0x%05x", i*eraseSize))
	}
	a.noteln(L("\nВсе проверки чтения пройдены. Будут перезаписаны только изменённые блоки загрузочной области.", "\nAll read-only gates passed. Only the changed blocks of the boot area will be rewritten."))
	if e = a.confirmOp(app.Write, "INSTALL URSUSBOOT", []string{
		fmt.Sprintf(L("записать UrsusBoot %s в загрузочную область NAND: блоки %s (по 128 КиБ)", "write UrsusBoot %s into the NAND boot area: blocks %s (128 KiB each)"), p.BootVersion, strings.Join(spans, ", ")),
		L("BootROM-префикс 0x0–0x7FF и stock env 0x7C000 остаются байт-в-байт", "the BootROM prefix 0x0–0x7FF and the stock env 0x7C000 stay byte-exact"),
		L("сверить каждый блок CRC32; блок BL2 (0x00000) — последним", "check every block's CRC32; the BL2 block (0x00000) last"),
		L("бэкап: ", "backup: ") + before,
	}); e != nil {
		return e
	}
	a.cancelBlocked(L("запись загрузочной области и её проверка", "writing the boot area and its readback"))
	// ubi blocks first, the bl2 block (the first stage) last.
	order := append([]int(nil), blocks...)
	if len(order) > 0 && order[0] == 0 {
		order = append(order[1:], 0)
	}
	for _, i := range order {
		block := cand[i*eraseSize : (i+1)*eraseSize]
		part, off := "ubi", uint64(i*eraseSize-bl2Size)
		if i == 0 {
			part, off = "bl2", 0
		}
		if _, e = a.ubootCommand(s, fmt.Sprintf("mtd erase %s 0x%x 0x%x", part, off, eraseSize), 3*time.Minute); e != nil {
			return e
		}
		if _, e = a.ubootCommand(s, fmt.Sprintf("mtd write %s 0x%x 0x%x 0x%x", part, loadAddr+uint64(i*eraseSize), off, eraseSize), 3*time.Minute); e != nil {
			return e
		}
		if e = a.readbackCRC(s, part, off, eraseSize, verifyAddr, crc32.ChecksumIEEE(block)); e != nil {
			return e
		}
		a.status("OK", fmt.Sprintf(L("блок 0x%05x записан и сверен", "block 0x%05x written and verified"), i*eraseSize), app.LevelOK)
	}
	a.cancelNow()
	a.event(L("UrsusBoot записан, проверка PASS; SHA256 области=", "UrsusBoot written, readback PASS; boot area SHA256=") + rep.TargetSHA)
	return nil
}

func (a *App) installUBIFIP(s Serial, p Profile, payload []byte) error {
	if _, e := a.ubootCommand(s, "ubi part ubi", 3*time.Minute); e != nil {
		return fmt.Errorf(L("не удалось подключить UBI: %w", "UBI attach failed: %w"), e)
	}
	out, e := a.ubootCommand(s, "ubi info layout", 60*time.Second)
	if e != nil {
		return e
	}
	var fip []ubiVol
	for _, v := range parseUBIVolumes(out) {
		if v.Name == "fip" {
			fip = append(fip, v)
		}
	}
	if len(fip) != 1 {
		return fmt.Errorf(L("ожидался один UBI-том fip, найдено %d", "expected one UBI volume fip, found %d"), len(fip))
	}
	vol := fip[0]
	if vol.Type != 4 {
		return fmt.Errorf(L("UBI-том fip не static (vol_type=%d) — установка заблокирована", "UBI volume fip is not static (vol_type=%d); install blocked"), vol.Type)
	}
	if vol.UsedBytes == 0 || vol.UsedBytes > bootFIPMax {
		return fmt.Errorf(L("UBI-том fip: недопустимый размер данных %d", "UBI volume fip: invalid data size %d"), vol.UsedBytes)
	}
	if _, e = a.ubootCommand(s, fmt.Sprintf("ubi read 0x%x fip 0x%x", loadAddr, vol.UsedBytes), 5*time.Minute); e != nil {
		return e
	}
	a.note(L("Читаю текущий том fip по UART (несколько минут)…", "Reading the current fip volume over the UART (a few minutes)…"))
	cur, e := a.uartDump(s, loadAddr, (vol.UsedBytes+3)&^3, "fip")
	if e != nil {
		return e
	}
	cur = cur[:vol.UsedBytes]
	before, e := a.saveInstallFile("fip-before.bin", L("Бэкап тома fip:", "fip volume backup:"), cur)
	if e != nil {
		return e
	}
	var cand []byte
	var rep installReport
	if p.ID == "md" {
		cand, rep, e = mdUBIFIP(cur, payload)
	} else {
		cand, rep, e = mfDeriveFIP(cur, payload)
	}
	if e != nil {
		return fmt.Errorf(L("кандидат не собран: %w", "candidate not built: %w"), e)
	}
	a.reportInstall(rep)
	if bytes.Equal(cand, cur) {
		a.status("OK", fmt.Sprintf(L("UrsusBoot %s уже установлен — запись не нужна", "UrsusBoot %s is already installed; nothing to write"), p.BootVersion), app.LevelOK)
		return errAlreadyInstalled
	}
	if c := vol.capacity(); c != 0 && uint64(len(cand)) > c {
		return fmt.Errorf(L("новый FIP (%d) больше тома fip (%d)", "the new FIP (%d) is larger than the fip volume (%d)"), len(cand), c)
	}
	target, e := a.saveInstallFile("fip-target.bin", L("Новый FIP:", "New FIP:"), cand)
	if e != nil {
		return e
	}
	if _, e = a.tftpLoad(s, target, "ursido-fip.bin", loadAddr); e != nil {
		return e
	}
	a.noteln(L("\nВсе проверки чтения пройдены. Будет перезаписан ТОЛЬКО UBI-том fip.", "\nAll read-only gates passed. ONLY the UBI volume fip will be overwritten."))
	if e = a.confirmOp(app.Write, "INSTALL URSUSBOOT", []string{
		fmt.Sprintf(L("перезаписать UBI-том fip: UrsusBoot %s, %d байт", "overwrite the UBI volume fip: UrsusBoot %s, %d bytes"), p.BootVersion, len(cand)),
		L("прочитать том обратно и сверить CRC32; BL2 не трогается", "read the volume back and compare CRC32; BL2 is not touched"),
		L("бэкап: ", "backup: ") + before,
	}); e != nil {
		return e
	}
	a.cancelBlocked(L("запись тома fip и её проверка", "writing the fip volume and its readback"))
	if e = a.ubiWriteVerified(s, "fip", uint64(len(cand)), crc32.ChecksumIEEE(cand)); e != nil {
		return e
	}
	a.cancelNow()
	a.event(L("UrsusBoot записан в том fip, проверка PASS; SHA256=", "UrsusBoot written to the fip volume, readback PASS; SHA256=") + rep.TargetSHA)
	return nil
}

// ubiWriteVerified writes size bytes at loadAddr into a UBI volume, reads it
// back to verifyAddr and requires the CRC32.
func (a *App) ubiWriteVerified(s Serial, vol string, size uint64, crc uint32) error {
	if _, e := a.ubootCommand(s, fmt.Sprintf("ubi write 0x%x %s 0x%x", loadAddr, vol, size), 5*time.Minute); e != nil {
		return e
	}
	if _, e := a.ubootCommand(s, fmt.Sprintf("ubi read 0x%x %s 0x%x", verifyAddr, vol, size), 5*time.Minute); e != nil {
		return e
	}
	out, e := a.ubootCommand(s, fmt.Sprintf("crc32 0x%x 0x%x", verifyAddr, size), 60*time.Second)
	if e != nil {
		return e
	}
	if !regexp.MustCompile(fmt.Sprintf(`(?i)(?:0x)?%08x`, crc)).Match(out) {
		return errors.New(L("CRC32 FIP после записи не совпал", "FIP readback CRC32 mismatch"))
	}
	return nil
}
