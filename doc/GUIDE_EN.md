# UrsidoRescue operator guide

[Русская версия](GUIDE_RU.md) · [Contents](README.md) · version 0.2.0-test14

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
UrsidoRescue-0.2.0-test14/
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

## 5. How data moves: XMODEM and LAN/TFTP

<a id="xmodem"></a>
### XMODEM (BootROM and the terminal's manual send)

Router UART lines can be noisy, and the BootROM and the next boot stage do not behave like
"textbook" XMODEM. So the rules are:

- **Bounded retries.** Each 128-byte block waits up to 2 s for ACK, with **at most 8 attempts**.
  NAK or `C` (CRC request) retries only that block at once. 80 ms between retries. After 8 failures:
  stop, "XMODEM block N not ACKed after 8 attempts".
- **ACK beats noise.** If one UART read holds `C`/NAK litter and an ACK, the ACK counts.
- **`CAN CAN`, not `CAN`.** A single `CAN` byte (`0x18`) is line noise and is **no longer a
  cancellation**. The receiver has aborted only when **two `CAN`s in a row** arrive: stop,
  "BootROM confirmed XMODEM cancellation (CAN CAN)".
- **`CAN CAN CAN` from the PC only in the data phase.** If the block transfer fails, the program
  sends three `CAN`s so the receiver is not left in an ambiguous state. After the last block has
  been ACKed, `CAN` is **never** sent: the receiver may already be running the transferred image.
- **EOT handoff.** After the last block, EOT is sent (at most 3 times, 1.5 s waits):
  - ACK to EOT: transfer complete;
  - NAK: one more EOT;
  - `C` or any text: this is already **next-stage output** (e.g. `NOTICE: BL31…`); no more EOT;
  - **no EOT ACK although every data block was ACKed is not an error by itself.** The next stage
    proves success: the second BootROM receiver (`CCC`) after the preloader, a stable RAM U-Boot
    prompt after the FIP. If the stage does not appear, the normal timeouts apply (up to 180 s) and
    the operation stops. Bytes that arrived during EOT are not lost; they go straight to the
    next-stage parser.
- The terminal's manual send (Ctrl+] → `s`) reports "All XMODEM data blocks were ACKed; EOT ACK was
  not received. Verify the device state in the terminal" when there is no EOT ACK.

<a id="lan-tftp"></a>
### LAN/TFTP

- **Route-aware PC IP.** The program asks the OS which interface routes to `192.168.1.1` and takes
  its address if it is `192.168.1.x`; fallback: any active interface with such an address. With
  several network adapters, the one that really leads to the router is chosen.
- **3 attempts of the current transfer.** Each re-applies the U-Boot network variables. Only the
  current file or 8 MiB chunk is repeated; chunks already written to NAND are never rewritten.
- **U-Boot resync.** After a failure the TFTP server stops and releases UDP/1069, U-Boot gets Ctrl-C
  until a stable prompt (up to 4 cycles of 1.5 s), then a 1–2 s backoff. If the prompt does not
  come back: stop, "could not resynchronize U-Boot prompt after network error".
- **RAM verification after every successful transfer**, retries included: `hash sha256` if
  available, otherwise `crc32`.
- **No `ping`.** Lost ICMP during link/ARP bring-up no longer stops the job; the transfer plus the
  RAM check is the proof that the network works.
- **Bounded server waits:** RRQ 30 s, option negotiation 8 × 1 s, block ACK 10 × 1 s.
- **Windows:** `WSAECONNRESET`/`WSAECONNABORTED` UDP errors from a stale peer count as noise and do
  not break the transfer.

## 6. Running

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
| `--uart PORT` | port (`COM6`, `/dev/ttyUSB0`). Optional: `probe` finds ports the same way as the menu (`QueryDosDeviceW` on Windows). No port → an error with exit code 2; exactly one → selected automatically; several → a numbered list is printed, exit code 2, re-run with `--uart PORT` |
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

## 7. Files and logs

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

## 8. When something goes wrong

The program is **fail-closed**: on anything unclear it stops and prints `[STOP] <reason>`. After a
stop no new write commands are sent.

| message | what to do |
|---|---|
| `timeout waiting for BootROM XMODEM` | Check TX/RX (swap them), GND, that Reset is held **before** power-on, and the port |
| `the UART shows AN758x, but profile … was chosen` | Pick the right profile or Auto |
| `… SHA256 mismatch` / `size … != pinned` | Files in `payloads/` are damaged or replaced; unpack the release again |
| `XMODEM block N not ACKed after 8 attempts` | Line noise, poor contact or a long wire. Restart from the BootROM step |
| `BootROM confirmed XMODEM cancellation … (CAN CAN)` | The receiver aborted the transfer. Restart from the BootROM step |
| "All XMODEM blocks were ACKed but EOT ACK was not received…" | Not an error: the program waits for the next stage. It stops only if that stage never appears |
| `autoboot escaped into Linux before prompt` | The RAM U-Boot did not stop in time. Retry; make sure the built-in RAM FIP is used |
| `destructive autoboot observed before prompt` | Safety stop. Keep the log and report it |
| `MTD geometry missing marker …` | The RAM U-Boot did not recognise the NAND, or this is not MD/MF. Run diagnostics (item 5) and send the log |
| `No 192.168.1.x IPv4 address on this PC` | Set a static `192.168.1.254/24` on Ethernet |
| `TFTP failed after 3 attempts: …` | Cable in a LAN port? Does the firewall allow UDP 1069? Is the PC address really `192.168.1.x`? The last attempt's reason follows (`TFTP RRQ timeout`: the request never reached the PC) |
| `could not resynchronize U-Boot prompt after network error` | U-Boot stopped answering. Keep the log; start over from the BootROM |
| `bad block in raw-critical stock region …` | Automatic stock restore is unsafe on this NAND. Needs manual analysis |
| `backup BL2 begins with … OpenWrt preloader` | This is not a stock backup; it was taken after reflashing |
| `readback CRC mismatch` | The write did not verify. BL2 is still untouched (it is always last), so you can start over |

For a report: main menu → **6**, attach `UrsidoRescue-support-*.zip` and describe what you did.
Review the logs for MACs/serials before publishing.
