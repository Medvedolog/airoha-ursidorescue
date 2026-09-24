# Porting Collector (probe mode) — UrsidoRescue 0.2.0 (test2)

Read-only discovery of an Airoha device over UART. The result is a porting bundle:
`ursus-probe-<vendor>-<model>-<soc>-<timestamp>.zip` with `profile.json` (ursus-profile-v1),
`ursusboot-porting-report.md`, `ursusflasher-device-draft.json`, raw logs and `hashes.sha256`.

## Safety

- Every command goes through a whitelist (`probe/guard.go`). Commands are matched by their full
  name, case-sensitively; U-Boot abbreviations (`sa`, `mtd wr`), `;`, `&&`, `|`, `$`, quotes and
  newlines are refused. erase / write / saveenv / env save / ubi create|remove|write / sf write|update
  are never sent. `--unsafe` does not change this.
- Keys the probe may send without a command: Ctrl-C/Esc to stop U-Boot autoboot (only during
  the countdown), Enter to activate a Linux console, `x` to BootROM only with `--bootrom`.
- Reads into RAM (`ubi read`, `mtd read`, `hash`) only when `loadaddr` lies inside a DRAM bank
  and at least 32 MiB below U-Boot's relocation address (from `bdinfo`); otherwise they are skipped.
- `ubi part` is issued only for partitions whose first page carries a UBI header. UBI attach can
  still scrub blocks internally; the profile records this as a warning.
- Unknown bootloaders (e.g. vendor `bldr>`) get no commands at all.

## Menu

Main menu → 8. PORTING / HARDWARE DISCOVERY:
1 full probe · 2 BootROM (observe / x-handshake / RAM U-Boot for MD/MF) · 3 U-Boot · 4 Linux ·
5 flash/MTD/UBI · 6 DTB · 7 network · 8 export · 9 view · N new session.

## Command line

    UrsidoRescue probe --uart COM6                 # Windows
    ./UrsidoRescue-linux-amd64 probe --uart /dev/ttyUSB0 --output ./probe
    ... probe --uboot-only | --linux-only | --no-linux
    ... probe --bootrom                            # answer "Press x", verify XMODEM 'C'
    ... probe --ram-uboot md|mf                    # bricked Nokia: load our RAM U-Boot, then probe
    ... probe --linux-user root --linux-password X # when Linux asks for a login
    ... probe --sample 64k --stop-key tpl --redact --timeout 10m
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
partition and the last 4 KiB of partitions up to 8 MiB via `mtd dump`; UBI geometry and volumes;
FIP ToC parsing and SHA256 of boot volumes. Linux: uname, cpuinfo, cmdline, meminfo, /proc/mtd,
/sys/class/mtd, ubinfo, dmesg, fw_printenv, board.json, /sys/firmware/fdt, ip link/addr, ethtool,
debug gpio.

The DTB is decompiled to `dt/fdt.dts` by the built-in parser (no dtc needed). Every fact in
`profile.json` has `source` and `confidence` (high = two independent sources agree); disagreeing
sources become `conflicts` and the value stays null. Device identity (MAC, serial, GPON) never
enters `profile.json`; only its location is listed. `--redact` masks it in the bundle's text files.

## Language

Interactive start asks for the language. `--lang ru|en` (any position) or `URSIDO_LANG=ru|en`
sets it without asking; `probe`/`export` use the system locale otherwise.
