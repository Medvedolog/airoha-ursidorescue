# Porting Collector (probe mode) — UrsidoRescue 0.2.0 (current: test16)

Read-only discovery of an Airoha device over UART. The result is a porting bundle:
`ursus-probe-<vendor>-<model>-<soc>-<timestamp>.zip` with `profile.json` (ursus-profile-v1),
`ursusboot-porting-report.md`, `ursusflasher-device-draft.json`, raw logs and `hashes.sha256`.

## Safety

Default probe mode is strict: no command that can write flash is sent.

- **What reaches the device.** A line is written in exactly two ways: a command that passed the
  allowlist (`probe/guard.go`), re-checked right before it is written; or one of three fixed
  internal templates (`probe/internal.go`): the hush test `echo URSIDO_HUSH_$?`, the return-code
  marker `echo URSIDO_<n>_RC_$?`, and the Linux wrapper
  `echo URSIDO_B_<n>; <allowlisted command> 2>&1; echo URSIDO_E_<n>_$?`. A test fails the build
  if anything else writes a line.
- **Allowlist.** Full command names, case-sensitive; U-Boot abbreviations (`sa`, `mtd wr`),
  `;`, `&&`, `|`, `$`, quotes and newlines are refused. erase / write / saveenv / env save /
  ubi create|remove|write / sf write|update are never sent. `--unsafe` changes nothing.
- **No UBI attach.** `ubi part` is refused in strict mode: attaching UBI can write (volume
  auto-resize, fastmap auto-conversion, scrubbing). UBI geometry comes offline from the raw EC/VID
  headers of the `mtd dump` sample, volumes from `ubinfo -a` when Linux boots.
  `--ubi-attach` / menu item A is a separate ADVANCED mode that is **not** read-only; the profile
  then says `strict_read_only: false` and warns.
- **Unknown devices are passive.** Keys are sent to a bootloader only after its U-Boot banner was
  seen (or for UrsidoRescue's own RAM U-Boot). Commands go to a Linux shell only after a kernel
  boot / OpenWrt console was seen. Otherwise a prompt is recorded as unconfirmed and gets nothing.
  `--wake` allows one Ctrl-C for a device that is already running; even then only `=>`,
  `U-Boot>` and `AN75xx>`/`EN75xx>` prompts get U-Boot commands.
- **Keys** (never lines): Ctrl-C/Esc to stop U-Boot autoboot during its countdown, Enter to
  activate an OpenWrt console, `x` to BootROM only with `--bootrom`, `--stop-key`, login answers.
- **Stock Linux login (Nokia XG-040G-MD/MF).** At a stock `Login:` the interactive probe (and the CLI
  with `--stock-lan-assist`) waits up to 90 s for the stock Web UI on `192.168.1.1` while it keeps
  draining the UART, logs in to it, checks the model and reads the current Telnet/FTP credentials.
  It then logs in over UART as the UID-0 service account, or as the Telnet account followed by `su`,
  and claims UID 0 only when `id -u` returns `0` (the only `id` form the guard allows). Credentials
  stay in memory: they are never printed or written to the transcript, UART log or bundle. If UID 0
  is not reached and stock FTP is off, the interactive probe asks one explicit `y/N` to enable FTP
  through the stock Web UI: a **stock-settings change** (FTP stays on), no MTD/firmware write. The
  CLI never enables FTP. Result fields: `linux_uid0`, `stock_lan_assist`, `stock_service_provisioned`.
- **Reads into RAM** (`mtd read`, `hash`; `ubi read` only in the attach mode) only when
  `loadaddr` lies inside a DRAM bank and 32 MiB below U-Boot's relocation address (from `bdinfo`).
- **Identity.** A recursive sanitizer removes MAC, serial, GPON/PLOAM, credential values from
  `profile.json`, the report and the UrsusFlasher draft (keys stay with `<redacted>`), including
  `board.json` `macaddr` fields. Raw logs in the bundle are unredacted unless `--redact`.
- **`--redact`** masks text files (MACs, `key=value` and JSON `"key": "value"` identity fields)
  and leaves binary files out (DTB, raw flash samples, listed in `REDACTED.txt`), because they can
  hold MAC/serial/calibration data. Review a bundle before publishing it.

## Menu

Main menu → 7. PORTING / HARDWARE DISCOVERY:
1 full probe · 2 BootROM (observe / x-handshake / RAM U-Boot for MD/MF) · 3 U-Boot · 4 Linux ·
5 flash/MTD/UBI · 6 DTB · 7 network · 8 export · 9 view · A advanced UBI attach · N new session.

## Interactive terminal

Expert mode → item 1 is a UART terminal. It starts in raw passthrough (verbatim,
copyable output; the device's own shell history works). Ctrl+] opens a menu:
l toggles a local line-input mode with ↑/↓ history, s/r do manual XMODEM
send/receive, g shows the log path, q quits. Everything is logged. It only
drives the port you open; it holds no credentials and never guesses any.

## Command line

    UrsidoRescue probe --uart COM6                 # Windows
    ./UrsidoRescue-linux-amd64 probe --uart /dev/ttyUSB0 --output ./probe
    ... probe --uboot-only | --linux-only | --no-linux
    ... probe --bootrom                            # answer "Press x", verify XMODEM 'C'
    ... probe --ram-uboot md|mf                    # bricked Nokia: load our RAM U-Boot, then probe
    ... probe --linux-user root --linux-password X # when Linux asks for a login
    ... probe --stock-lan-assist                   # stock Nokia MD/MF: fetch credentials via Web, UART UID0 login
    ... probe --sample 64k --stop-key tpl --redact --timeout 10m
    ... probe --wake                               # device already running at a prompt
    ... probe --ubi-attach                         # ADVANCED, not read-only
    ... export [--input DIR] [--redact]

Flow of a full probe: power the device on after start. The probe interrupts U-Boot autoboot,
collects U-Boot data, then asks you to power-cycle the device (without holding Reset) and waits
for Linux without interrupting it.

Exit codes: 0 ok · 1 failure · 2 UART unavailable · 3 BootROM not detected · 4 U-Boot not detected ·
5 Linux unavailable · 6 profile incomplete · 7 safety violation blocked.

## What is collected

UART markers with timestamps (Press x, CCC, BL2/BL31/BL33, U-Boot, AN7581/AN7583, kernel);
U-Boot: version, help (capabilities), bdinfo, printenv, mtd list, mtd bad, nand/mmc info, dm tree,
mii/mdio, gpio status, control DTB (md.b); raw samples of the first 4 KiB (or 64 KiB) of each
partition and the last 4 KiB of partitions up to 8 MiB via `mtd dump`; UBI geometry from those
samples; FIP ToC parsing and SHA256 of boot partitions (UBI volumes only in the attach mode). Linux: uname, cpuinfo, cmdline, meminfo, /proc/mtd,
/sys/class/mtd, ubinfo, dmesg, fw_printenv, board.json, /sys/firmware/fdt, ip link/addr, ethtool,
debug gpio.

The DTB is decompiled to `dt/fdt.dts` by the built-in parser (no dtc needed). Every fact in
`profile.json` has `source` and `confidence` (high = two independent sources agree); disagreeing
sources become `conflicts` and the value stays null. Device identity (MAC, serial, GPON) never
enters `profile.json`; only its location is listed. `--redact` masks it in the bundle's text files.

## Language

Interactive start asks for the language. `--lang ru|en` (any position) or `URSIDO_LANG=ru|en`
sets it without asking; `probe`/`export` use the system locale otherwise.
