# UrsidoRescue changelog

[Русская версия](CHANGELOG_RU.md) · [Contents](README.md)

Status labels: **simulation PASS**: tests and simulation passed; **HW PARTIAL**: partly verified
on hardware; **HW HOLD/PENDING**: not verified on hardware. Builds and CI are not hardware
validation. The detailed status of every version is in [../STATUS.md](../STATUS.md).

## Unreleased

- Added `doc/` with Russian and English documentation: about the project and the Ursus family,
  operator guide, every menu in detail, architecture, changelog.
- `PROBE.md`: fixed the main menu item number of the Porting Collector (7, not 8).

## 0.2.0-test10 (2026-09-24)

Status: simulation PASS / HW PARTIAL. UART/recovery hardware status unchanged from test9.

- `UrsidoRescue.exe` carries the Ursus bear icon as Windows `RT_ICON` / `RT_GROUP_ICON` resources in
  7 sizes (16, 24, 32, 48, 64, 128, 256 px) instead of the generic Go icon.
- Icon sources (`assets/ursus-bear.svg`, 256 px `assets/ursus-bear.png`) are kept for the planned GUI.
- Embedding is done by the pure-Go tool `tools/embedicon`; the build needs no MinGW, windres, Python
  or ImageMagick.
- Fix: `build.sh` is executable again.

## 0.2.0-test9 (2026-09-24)

Status: simulation PASS / HW PARTIAL. Testing test8 on Windows/OpenWrt confirmed normal input and
live UART output, but the pager split fullscreen programs such as BusyBox `top`.

- Ctrl+P paging applies only to ordinary line-oriented output.
- Fullscreen ANSI/TUI streams are detected from cursor-home, clear-screen and alternate-screen
  sequences; the pager flushes its queue and turns itself off.
- Detection works even when a CSI sequence is split across two reads.
- Colour (SGR) output and simple erase-line sequences do not trigger TUI mode.
- `top`/`vi`/`less` keep their own screen model; Ctrl+P can be re-enabled for `ps`, `dmesg`, logs.
- Ctrl+C and Ctrl+Z are forwarded to the router as `0x03` and `0x1A` in both input modes, including
  Windows `KEY_EVENT_RECORD`.
- Regression tests for all of the above.

## 0.2.0-test8 (2026-09-24)

Status: simulation PASS / HW PARTIAL. In test7 on Windows, Ctrl+P/Ctrl+Q worked but ordinary keys
and Enter could be dropped in the new raw console path.

- Windows terminal input uses `ReadConsoleInputW` + `KEY_EVENT_RECORD` instead of reading
  `os.Stdin` bytes under `VIRTUAL_TERMINAL_INPUT`.
- Enter, Backspace, printable ASCII, arrows and Home/End/Delete are translated explicitly; an arrow
  becomes one complete ANSI sequence before it reaches the UART.
- Ctrl+Q, Ctrl+P, Ctrl+C, Ctrl+D and Ctrl+] are translated locally.
- Non-Latin Unicode reaches the ASCII gate and is reliably rejected with a layout warning.
- `VIRTUAL_TERMINAL_INPUT` is off on stdin; QuickEdit stays on for the clipboard.
- Linux unchanged. Tests for Windows Enter/arrows/Ctrl keys and Cyrillic rejection.

## 0.2.0-test7 (2026-09-24)

Status: simulation PASS / HW PARTIAL. OpenWrt testing showed that the old UART Shell (Expert 6) read
the console byte by byte and polled the port every 250 ms: arrows were split and input lagged by
almost a second.

- Expert 6 uses the same low-latency raw engine as Expert 1; there is no separate byte-by-byte
  implementation any more.
- Interactive port polling is 20 ms instead of 200/250 ms.
- Keyboard and paste input is written in chunks; ANSI arrows stay intact.
- Ctrl+Q and Ctrl+] leave the UART Shell.
- Commands are ASCII-only: bytes ≥ 0x80 are blocked locally with a warning. XMODEM is unaffected.
- Ctrl+P is a local pager sized to the window; while paused the UART keeps being read and logged,
  Enter shows the next page; the queue is capped at 4 MiB.

## 0.2.0-test6 (2026-09-24)

Status: simulation PASS / HW PARTIAL. The interactive UART terminal ran on real OpenWrt hardware for
the first time; the Windows issues it exposed are fixed. Probe and writes: HW HOLD.

- Windows lists only COM ports that actually exist (`QueryDosDeviceW`), selectable by number;
  manual `COM<n>` input remains.
- Windows serial writes no longer call `FlushFileBuffers` after every write, removing the
  near-second typing latency.
- QuickEdit stays enabled in raw mode, so select/copy/paste work again.
- Ctrl+Q is a local fast exit and never reaches the router.
- Tests: an arrow goes out in one write, Ctrl+Q stays local.

## 0.2.0-test5 (2026-09-24)

- The terminal defaults to raw passthrough: device output verbatim, clean and copyable, and the
  device's own history (BusyBox/U-Boot) works. The `[K` litter came from the old line-mode redraw
  injecting ANSI erase codes.
- Line-input mode (menu `l`) remains for devices without their own editing; its redraw now uses
  only CR/space/backspace.
- Main menu: Expert mode moved to the last position and shown in bold.
- Windows: `ENABLE_VIRTUAL_TERMINAL_PROCESSING` is enabled, so bold and ANSI render.

## 0.2.0-test4 (2026-09-24)

- New UART terminal (Expert 1): local line editing, ↑/↓ history, cursor keys, Home/End, a raw
  passthrough toggle, manual XMODEM send and receive (XMODEM-CRC, 128/1K) and full session logging.
  The old transparent shell stays as item 6. It holds no credentials and guesses none.
- CI: a pre-release can be published from a manual workflow run on `main`.

## 0.2.0-test3 (2026-09-24)

Review fixes before the first hardware probe.

- `ubi part` removed from the strict probe: attaching UBI can write (auto-resize, fastmap). UBI
  geometry is parsed offline from the EC/VID headers; U-Boot attach is a separate ADVANCED mode
  (`--ubi-attach`, item A) reported as not read-only.
- Identity leak: `/etc/board.json` `macaddr` values reached `profile.json`. A recursive sanitizer now
  cleans the profile, report and draft; regression tests added.
- Unknown devices are passive: no Ctrl-C and no commands until a U-Boot/Linux banner is seen;
  `--wake` allows one Ctrl-C, and only well-known prompts get U-Boot commands.
- Internal marker lines use fixed, tested templates (`probe/internal.go`).
- `--redact` also handles JSON identity fields and leaves binary files out of the bundle.
- CI: build and test on every push, pre-releases for `v*` tags.

## 0.2.0-test2 (2026-09-24)

First version in this repository.

- Language choice at start (Русский / English); `--lang ru|en` or `URSIDO_LANG`.
- Includes everything from 0.2.0-test1 and the recovery from 0.1.1-test2.

## 0.2.0-test1

- Read-only Porting Collector (the "Porting" menu item, `probe` / `export` commands),
  `ursus-profile-v1` bundles.

## 0.1.x

Early UrsidoRescue builds before this repository: UART recovery of the Nokia XG-040G-MD/MF through
the BootROM (stock mtd16, FIP, physical NAND, recovery ITB, diagnostics, expert mode). Since
0.1.1-test2 the RAM FIPs of both boards are UrsusBoot 0.1.0-alpha5-t66 RECOVERY_SAFE (Fudan FM25S01A
+ FM25G02B). Hardware status: PENDING.
