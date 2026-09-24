# UrsidoRescue operator guide

[Русская версия](GUIDE_RU.md) · [Contents](README.md) · version 0.2.0-test10

## 0. Before you start

- This is a **lab UART build**. Flash writes have not completed a full hardware validation cycle
  yet; see [../STATUS.md](../STATUS.md). Use it only if you know what you are doing and have a backup.
- **The backup is what matters.** Factory MACs, serial number, GPON data and optical calibration
  cannot be recreated without a backup (for example one taken by MedveFlasher).
- Reading and diagnostics (items 5, 7, expert 1/6) write nothing to flash. Items 1–3 and expert 3/4
  write, and always require a confirmation phrase first.

## 1. What you need

| item | why |
|---|---|
| USB-UART adapter, **3.3 V TTL** (CH340, CP2102, FT232 …) | router console, BootROM, XMODEM |
| 3 wires: **GND, TX, RX** | **never connect VCC**; the router runs from its own power supply |
| Ethernet cable PC ↔ router LAN port | TFTP for large files (items 1–4, expert 3–4) |
| Windows x64 or Linux x86_64 / arm64 | the program itself |
| A backup or image | depending on the item: `mtd16`, `.fip`, `.itb`, 256 MiB NAND image |

**UART wiring:** adapter TX → router RX, adapter RX → router TX, GND → GND.
Port settings (the program sets them): 115200, 8N1, no flow control.

## 2. Installation

1. Download `UrsidoRescue-<version>.zip` from the repository Releases page and unpack it.
2. Check integrity: `SHA256SUMS` inside the archive, `.zip.sha256` next to it.
3. Contents:

```
UrsidoRescue-0.2.0-test10/
  UrsidoRescue.exe            Windows x64
  UrsidoRescue-linux-amd64    Linux x86_64
  UrsidoRescue-linux-arm64    Linux aarch64 (Raspberry Pi 4/5, ARM laptops)
  VERSION
  payloads/md/…               preloader and RAM FIP for XG-040G-MD
  payloads/mf/…               preloader and RAM FIP for XG-040G-MF
  STATUS.md  PROBE.md  SHA256SUMS
```

The program looks for `VERSION` and `payloads/` next to itself (up to three levels up) or in the
current directory. Do not move the binary away from them. Working files and logs go to `work/`.

**Linux:** serial access usually needs the `dialout` group (`sudo usermod -aG dialout $USER`, then
log in again) or `sudo`.

**Windows:** Defender/SmartScreen may warn about an unsigned file on first start. For TFTP, allow
inbound UDP port **1069** in the firewall (or allow private-network access when Windows asks).

## 3. Network for TFTP

Needed for items 1–4 and expert 3–4.

- Give the PC's Ethernet adapter the static address **`192.168.1.254`**, mask `255.255.255.0`
  (any `192.168.1.x` except `.1` also works).
- The router in the RAM U-Boot gets **`192.168.1.1`**.
- The program finds the PC's `192.168.1.x` address itself; if it cannot, it asks you to type it.
- The built-in TFTP server listens on `UDP 1069` and serves only `192.168.1.1`.
- Turn Wi-Fi off if it is also on a `192.168.1.0/24` network.

## 4. Putting the router into BootROM mode

For every RAM U-Boot operation:

1. Power the router off.
2. Connect the UART, start the item in the program and wait for
   "Waiting for BootROM Press x / CCC".
3. **Hold Reset and apply power.** Keep holding until `Press x` appears (the program answers `x`
   itself) followed by a `CCC…` line.
4. The program does the rest: preloader → FIP → U-Boot prompt.

If `Press x to load BL31 + U-Boot FIP` and running `C`s are **already** on screen, reboot nothing:
the program sees that the preloader is loaded and sends only the FIP.

> Do not confuse this with UrsusBoot recovery: Reset held **before power-on** is the Airoha BootROM;
> Reset held **after** start (2 short + 3 long blinks) is UrsusBoot recovery mode.

## 5. Running

**Interactive:**

```
Windows:  double-click UrsidoRescue.exe   (or run it from cmd/PowerShell)
Linux:    ./UrsidoRescue-linux-amd64
```

The language is asked at start. Preset it with `--lang ru|en` (anywhere on the command line) or the
`URSIDO_LANG=ru|en` environment variable. All menu items are described in [MENU_EN.md](MENU_EN.md).

**Command line:**

| command | does |
|---|---|
| `--version` | prints the version |
| `--selftest` | self-check: pinned payload SHA256, FIP headers, probe allowlist, XMODEM CRC, parsers. `SELFTEST PASS` / `SELFTEST FAIL: …` |
| `probe [flags]` | Porting Collector without the menu |
| `export [--input DIR] [--output DIR] [--redact]` | repack a bundle from an existing probe directory (default: the latest `work/probe-*`) |

`probe` flags:

| flag | meaning |
|---|---|
| `--uart PORT` | port (`COM6`, `/dev/ttyUSB0`). Required on Windows; on Linux optional when there is exactly one port |
| `--output DIR` | session directory (default `work/probe-<time>`) |
| `--uboot-only` / `--linux-only` / `--no-linux` | limit environments (the first two exclude each other) |
| `--bootrom` | answer `Press x` and check the XMODEM `C` |
| `--ram-uboot md\|mf` | bricked Nokia: load UrsidoRescue's RAM U-Boot and probe through it |
| `--timeout 5m` | overall timeout |
| `--sample 4k\|64k` | head sample size per partition |
| `--linux-user U --linux-password P` | credentials if Linux asks for a login |
| `--stop-key S` | a special autoboot stop key (e.g. `tpl`) |
| `--wake` | device already running: allow one Ctrl-C |
| `--redact` | mask MACs/serials in text files, leave binary files out |
| `--no-export` | do not create the zip |
| `--ubi-attach` | ADVANCED, **not read-only**: allow `ubi part` |
| `--unsafe` | relaxes nothing: the probe stays read-only and a warning is printed |

`probe` exit codes: `0` ok, `1` failure, `2` UART unavailable, `3` BootROM not detected, `4` U-Boot
not detected, `5` Linux unavailable, `6` profile incomplete, `7` safety violation blocked.

Examples:

```
UrsidoRescue.exe probe --uart COM6
./UrsidoRescue-linux-amd64 probe --uart /dev/ttyUSB0 --output ./probe
./UrsidoRescue-linux-amd64 probe --uart /dev/ttyUSB0 --ram-uboot md
./UrsidoRescue-linux-amd64 export --redact
```

## 6. Files and logs

| path | contents |
|---|---|
| `work/recovery-<time>.uart.log` | full UART log of RAM U-Boot operations (items 1–5, expert 2–5) |
| `work/term-<time>.uart.log` | UART terminal log (expert 1) |
| `work/shell-<time>.uart.log` | UART Shell log (expert 6) |
| `work/stock-<time>/` | `mtd16` split into BL2 + 8 MiB chunks (item 1) |
| `work/physical-<time>/` | NAND image split into chunks (item 3) |
| `work/probe-<time>/` | a Porting Collector session |
| `UrsidoRescue-support-<time>.zip` | report log bundle (item 6) |
| `ursus-probe-<vendor>-<model>-<soc>-<time>.zip` | porting bundle |

The `stock-*` and `physical-*` directories take as much space as the source image (up to 256 MiB);
after a successful restore they can be deleted.

## 7. When something goes wrong

The program is **fail-closed**: on anything unclear it stops and prints `[STOP] <reason>`. After a
stop no new write commands are sent.

| message | what to do |
|---|---|
| `timeout waiting for BootROM XMODEM` | Check TX/RX (swap them), GND, that Reset is held **before** power-on, and the port |
| `the UART shows AN758x, but profile … was chosen` | Pick the right profile or Auto |
| `… SHA256 mismatch` / `size … != pinned` | Files in `payloads/` are damaged or replaced; unpack the release again |
| `XMODEM block N not ACKed` | Line noise, poor contact or a long wire. Restart from the BootROM step |
| `autoboot escaped into Linux before prompt` | The RAM U-Boot did not stop in time. Retry; make sure the built-in RAM FIP is used |
| `destructive autoboot observed before prompt` | Safety stop. Keep the log and report it |
| `MTD geometry missing marker …` | The RAM U-Boot did not recognise the NAND, or this is not MD/MF. Run diagnostics (item 5) and send the log |
| `No 192.168.1.x IPv4 address on this PC` | Set a static `192.168.1.254/24` on Ethernet |
| `U-Boot ping PC failed` | Cable in a LAN port? Firewall? Is the PC address really `192.168.1.x`? |
| `TFTP RRQ timeout` | The firewall blocks UDP 1069, or U-Boot has no network |
| `bad block in raw-critical stock region …` | Automatic stock restore is unsafe on this NAND. Needs manual analysis |
| `backup BL2 begins with … OpenWrt preloader` | This is not a stock backup; it was taken after reflashing |
| `readback CRC mismatch` | The write did not verify. BL2 is still untouched (it is always last), so you can start over |

For a report: main menu → **6**, attach `UrsidoRescue-support-*.zip` and describe what you did.
Review the logs for MACs/serials before publishing.
