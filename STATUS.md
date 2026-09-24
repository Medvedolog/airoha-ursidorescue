# UrsidoRescue 0.2.0-test8-dev

Status: **HW terminal debugging / unreleased.** Real Windows/OpenWrt testing of v0.2.0-test7
showed that local Ctrl+P/Ctrl+Q were visible but ordinary keys and Enter could be dropped in the
new raw-console path. The COM transport itself remained open.

## 0.2.0-test8-dev

- Windows terminal input now uses ReadConsoleInputW + KEY_EVENT_RECORD instead of ReadFile/os.Stdin
  bytes under VIRTUAL_TERMINAL_INPUT.
- Enter, Backspace, printable ASCII and arrow/Home/End/Delete keys are translated explicitly;
  arrows become one complete ANSI sequence before the UART write path sees them.
- Ctrl+Q, Ctrl+P, Ctrl+C, Ctrl+D and Ctrl+] are translated locally from Windows key events.
- Non-Latin Unicode input is preserved long enough for the ASCII gate to reject it reliably before
  UART transmission, with the existing keyboard-layout warning.
- VIRTUAL_TERMINAL_INPUT is disabled on stdin; QuickEdit remains enabled for Windows clipboard use.
- Linux terminal input is unchanged.
- Pure-Go regression tests cover Windows Enter/arrows/control keys and Cyrillic rejection.

# UrsidoRescue 0.2.0-test7

Status: **simulation PASS / HW PARTIAL.** Real OpenWrt testing of v0.2.0-test6 showed that
the legacy Expert-mode UART Shell still used one-byte console reads and a 250 ms serial read loop,
so arrow escape sequences were split and interactive input could lag close to a second. Those fixes are included here but still need hardware re-test.

## 0.2.0-test7

- Expert item 6 now uses the same low-latency raw backend as item 1; no separate one-byte UART
  shell implementation remains.
- Interactive serial read polling reduced from 200/250 ms to 20 ms so synchronous Windows COM
  reads do not stall operator writes for a visible fraction of a second.
- Raw keyboard/paste data is written in chunks; ANSI arrow sequences remain contiguous.
- Ctrl+Q and Ctrl+] both exit the simple UART Shell locally.
- Interactive command input is ASCII-only: bytes >=0x80 are blocked locally with a layout warning.
  XMODEM/binary transfers are unaffected.
- Ctrl+P toggles a local pager sized to the console window. While paused, UART keeps draining and
  logging; Enter shows the next page. Pending display data is capped at 4 MiB.

# UrsidoRescue 0.2.0-test6

Status: **simulation PASS / HW PARTIAL.** The interactive UART terminal has now run on real
OpenWrt hardware. That first hardware session exposed Windows terminal usability issues fixed in
this build; the fixes themselves still need a hardware re-test. Probe/recovery write paths remain
HW HOLD.

UART testers only: 3.3V TTL, GND/TX/RX only, never connect VCC.

## 0.2.0-test6

- Windows now enumerates only COM ports that actually exist via QueryDosDeviceW and offers a
  numbered selection; manual COM<n> input remains available.
- Windows serial writes no longer call FlushFileBuffers after every write. Raw terminal input is
  forwarded in console chunks, removing the near-second typing latency seen on hardware and
  keeping ANSI arrow/history sequences together.
- Windows QuickEdit remains enabled in UART raw mode so native selection/copy/paste works again.
- Ctrl+Q is a local fast-exit from the UART terminal and is never sent to the router.
- Regression tests verify one-write delivery of an arrow escape sequence and local Ctrl+Q.
- Exact pre-release build is still subject to hardware re-test for typing latency, arrows and
  clipboard behavior.

## 0.2.0-test5

- Terminal fixed: raw passthrough is now the default, so device output is
  printed verbatim — clean, copyable, and the device's own shell history
  (busybox/U-Boot arrows) works. The garbled "[K" litter came from the old
  line-mode redraw injecting ANSI erase codes into the output stream.
- Line-input mode (menu: l) is still available for devices without their own
  editing; its redraw now uses only CR/space/backspace, no escape sequences.
- Main menu: Expert mode moved to the last position and shown in bold.
- Windows: ENABLE_VIRTUAL_TERMINAL_PROCESSING is turned on so bold and any
  ANSI render correctly.

## 0.2.0-test4

- New interactive UART terminal (Expert mode, item 1): local line editing with
  command history (↑/↓), cursor keys/Home/End, a raw passthrough toggle, manual
  XMODEM send and receive (XMODEM-CRC, 128/1K), and full session logging
  (device output plus every operator-sent line). The old transparent shell is
  still there as item 6. It has no device credentials and guesses none.

## 0.2.0-test3 (review fixes before the first hardware probe)

- `ubi part` removed from the strict probe: UBI attach can write (volume auto-resize, fastmap).
  UBI geometry is parsed offline from the raw EC/VID headers; U-Boot attach is a separate
  ADVANCED mode (`--ubi-attach`, menu item A) reported as not read-only.
- Identity leak fixed: `/etc/board.json` `macaddr` values reached profile.json. A recursive
  sanitizer now cleans profile.json, the report and the draft; regression tests cover it.
- Unknown devices are passive: no idle Ctrl-C and no commands without a U-Boot/Linux banner;
  `--wake` allows one Ctrl-C and only well-known U-Boot prompts get commands.
- Internal marker lines go through fixed, tested templates (`probe/internal.go`).
- `--redact` also handles JSON identity fields and leaves binary files out of the bundle.

## 0.2.0-test2

Language choice (Русский / English) at start; `--lang ru|en` or `URSIDO_LANG`.

## 0.2.0-test1

Read-only Porting Collector (menu item 8, `probe` / `export`), ursus-profile-v1 bundles.

## Recovery

Unchanged from 0.1.1-test2: RAM FIPs for both boards are the UrsusBoot 0.1.0-alpha5-t66
RECOVERY_SAFE U-Boots (Fudan FM25S01A + FM25G02B). Hardware status: PENDING.
