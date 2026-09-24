# UrsidoRescue 0.2.0-test14

Status: **simulation candidate / HW PARTIAL.** This code-only follow-up does not touch the separate
doc/ work. It closes three code/documentation mismatches found during documentation review.

## 0.2.0-test14

- Normal NAND/UBI/U-Boot diagnostics are now genuinely read-only: they no longer execute
  `ubi part ubi`. MTD layout/bad-block/environment inspection remains, and the UI explicitly
  points users to Porting mode A / `--ubi-attach` for an intentional advanced UBI attach.
- Expert one-shot RAM-file limit messages are derived from `maxGenericRAMFile` instead of
  hard-coding 64 MiB. The current 0x08000000 limit therefore reports 128 MiB consistently.
- CLI `probe` now uses the same serial-port enumerator as the menu on every platform. On Windows
  that is the existing QueryDosDeviceW implementation; one detected COM port is auto-selected,
  multiple COM ports are shown as a numbered list and require explicit `--uart PORT`.
- No files under doc/ are changed in this release.

# UrsidoRescue 0.2.0-test13

Status: **simulation candidate / HW PARTIAL.** Two consecutive AN7583/MF hardware runs of test12
ACKed all 2539 FIP data blocks but emitted no EOT ACK. test12 then retried EOT six times and aborted,
so the transport close was incorrectly treated as stronger evidence than the already-starting next stage.

## 0.2.0-test13

- Fixes the AN7583 EOT regression seen twice on real Nokia XG-040G-MF hardware.
- Once every XMODEM data block is ACKed, missing EOT ACK is no longer an immediate fatal error.
  UrsidoRescue preserves any bytes already emitted by the next stage and requires the caller to prove
  the transition: the preloader path must reach the second XMODEM receiver, and the FIP path must reach
  a stable RAM U-Boot prompt before recovery continues.
- EOT handling is stage-safe: ACK remains definitive, NAK requests another EOT, but ASCII C or boot
  text is treated as next-stage output instead of an EOT retry. This avoids interpreting the letter C
  inside strings such as NOTICE as an XMODEM CRC request.
- CAN CAN CAN is sent only when the data phase itself failed. It is never injected after the final
  payload block was ACKed, because the peer may already be executing the newly transferred image.
- XMODEM block response parsing now gives ACK priority over C/NAK noise in the same UART read.
- EOT retries are bounded to three cautious 1.5 s waits only while there is no next-stage output.
- EOT-consumed UART bytes are fed into the next-stage parser immediately, so U-Boot autoboot can be
  interrupted without losing the banner/prompt that arrived in the same serial read.

# UrsidoRescue 0.2.0-test12

Status: **simulation PASS / HW PARTIAL.** Real Nokia XG-040G-MF testing of test11 reached the
second BootROM XMODEM stage and transferred 1152/2539 FIP blocks before one received CAN byte made
the host abort the entire session. No NAND erase/write had started.

## 0.2.0-test12

- XMODEM no longer treats one noisy CAN byte as cancellation. A receiver abort requires CAN CAN;
  a single CAN followed by ACK is accepted as a noisy line event.
- NAK or CRC-request 'C' causes an immediate retry of only the current block instead of waiting out
  the old 12-second deadline.
- Per-block ACK wait is reduced to 2 seconds with 8 bounded attempts; EOT uses 6 x 2-second attempts.
  Any final host-side XMODEM failure sends CAN CAN CAN so the peer is not left in an ambiguous session.
- LAN recovery adopts UrsusFlasher-style resilience: route-aware PC IPv4 selection, three bounded
  attempts of only the current TFTP transfer, backoff/resync of the same RAM U-Boot session, and
  mandatory RAM SHA256/CRC verification after every successful retry.
- ICMP ping is no longer a hard preflight gate. The actual TFTP transfer plus RAM verification is
  the proof that the selected Ethernet path works.
- The TFTP server is cancellable and bounded: stale failed attempts release UDP/1069 before retry;
  RRQ, option negotiation and block ACK waits have finite deadlines.
- Windows stale-peer UDP reset/abort errors are treated as recoverable network noise, matching the
  collision policy used by UrsusFlasher.
- Stock restore retries only the current 8 MiB RAM upload; already completed NAND chunks are never
  replayed by the transport retry layer. BL2-last policy is unchanged.

# UrsidoRescue 0.2.0-test11

Status: **simulation PASS / HW PARTIAL.** Real Nokia XG-040G-MF / AN7583 testing of test10
successfully completed BootROM preloader XMODEM and RAM BL31+U-Boot FIP loading. The recovery
wizard then reached a live AN7583> prompt but failed to recognize it after the ANSI boot menu, so
no NAND erase/write had started.

## 0.2.0-test11

- U-Boot prompt detection now strips ANSI CSI control sequences and recognizes a prompt by its
  stable trailing token (AN7581>, AN7583>, U-Boot> or =>), rather than requiring a physical CR/LF
  before it. This handles screen-oriented bootmenu output where cursor positioning replaces newline.
- Once bootmenu is detected, the recovery handshake sends only bounded ESC exits instead of mixing
  repeated Ctrl-C + ESC into the menu/prompt stream.
- Regression tests reproduce AN7583 bootmenu cursor-addressing before/after the prompt and reject
  prompt-like text that is not at the end of the stream.
- test10's branded Windows EXE icon and test9's terminal/TUI/Ctrl+C/Ctrl+Z fixes are retained.
- Hardware status: RAM U-Boot load on MF is now observed on real hardware; stock NAND restore is
  still not HW PASS until version/mtd geometry, TFTP preflight, erase/write/readback and BL2-last
  stages complete successfully.

# UrsidoRescue 0.2.0-test10

Status: **simulation PASS / HW PARTIAL.** UART/recovery hardware status is unchanged from test9.
This release adds Windows branding to the executable itself.

## 0.2.0-test10

- UrsidoRescue.exe now embeds the canonical Ursus bear as native Windows RT_ICON/RT_GROUP_ICON
  resources instead of shipping with the generic Go executable icon.
- Seven icon sizes are generated at build time: 16, 24, 32, 48, 64, 128 and 256 px.
- The canonical SVG and a 256 px raster master live under assets/ for reuse by the planned GUI.
- Resource injection is a small pure-Go build tool under tools/embedicon, so release builds do not
  require MinGW, windres, Python, ImageMagick or an external resource compiler.
- The test9 UART/TUI/Ctrl+C/Ctrl+Z fixes are unchanged.

# UrsidoRescue 0.2.0-test9

Status: **simulation PASS / HW PARTIAL.** Windows/OpenWrt hardware testing of v0.2.0-test8
confirmed ordinary keyboard input and live UART output, and exposed a pager/TUI interaction:
screen-oriented programs such as BusyBox top were being split by the local line pager.

## 0.2.0-test9

- Local Ctrl+P paging is now limited to ordinary line-oriented output.
- Fullscreen ANSI/TUI streams are detected from cursor-home/display-clear/alternate-screen control
  sequences; the pager immediately flushes pending data and auto-disables before forwarding them.
- Detection spans serial read boundaries so a split CSI sequence is still recognized.
- Color-only SGR output and simple erase-line sequences do not trigger TUI bypass.
- top/vi/less-style applications therefore retain their own terminal screen model; Ctrl+P can
  re-enable paging later for ps, dmesg, logs and other static output.
- Ctrl+C and Ctrl+Z are explicitly forwarded to the remote console as 0x03 and 0x1A in both raw
  and line-input modes; Windows KEY_EVENT_RECORD translation covers both shortcuts.
- Regression tests cover fullscreen ANSI detection, normal colored output, split CSI sequences and
  Ctrl+C/Ctrl+Z translation.

# UrsidoRescue 0.2.0-test8

Status: **simulation PASS / HW PARTIAL.** Real Windows/OpenWrt testing of v0.2.0-test7
showed that local Ctrl+P/Ctrl+Q were visible but ordinary keys and Enter could be dropped in the
new raw-console path. The COM transport itself remained open. These Windows input fixes are included here and still need hardware re-test.

## 0.2.0-test8

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
