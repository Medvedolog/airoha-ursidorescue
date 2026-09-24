# UrsidoRescue operator guide

[Русская версия](GUIDE_RU.md) · [Contents](README.md) · version 0.2.0-test17

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
UrsidoRescue-0.2.0-test17/
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
- Turn DHCP off on that adapter.
- Cable the PC directly to the Nokia, into **LAN2 or LAN3**. LAN1 is not recommended; avoid LAN4 for
  transition and boot work.
- While working, **disable Wi-Fi, VPN, other Ethernet, Hyper-V and other virtual adapters, and
  tunnels**. Leave only the adapter connected to the Nokia.
- The program finds the PC's `192.168.1.x` address itself; if it cannot, it asks you to type it.
- The built-in TFTP server listens on `UDP 1069` and serves only `192.168.1.1`. No separate TFTP
  server is needed.

Before every TFTP wizard the program prints a **"Network prerequisites"** block with these
requirements and the PC's active IPv4 interfaces. With more than one, a warning appears. This does
not block anything, but disable the extra interfaces.

The same network is needed for the Porting Collector's [stock LAN assist](#stock-login): there the
router runs its factory firmware and answers on `192.168.1.1`.

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
- **EOT handoff.** After the last block, EOT is sent and a reply is awaited for 0.9 s:
  - ACK to EOT: transfer complete;
  - NAK: one more EOT (only on an explicit NAK, at most 3 times);
  - `C` or any text: this is already **next-stage output** (e.g. `NOTICE: BL31…`); no more EOT;
  - silence: EOT is **not** retried; control passes straight to the next-stage proof;
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
- **U-Boot network is configured once per session:** temporary MACs, `ipaddr`, `serverip`,
  `netmask`, `tftpdstp`, `autoload`. All later transfers and chunks reuse these settings.
- **3 attempts of the current transfer.** Only the current file or 8 MiB chunk is repeated; chunks
  already written to NAND are never rewritten.
- **U-Boot resync after a network failure.** If the `tftpboot` command itself failed:
  - the TFTP server stops and releases UDP/1069;
  - U-Boot gets Ctrl-C until a stable prompt (up to 4 cycles of 1.5 s);
  - the network variables are re-applied, then a 1–2 s backoff.
  If the prompt does not come back: stop, "could not resynchronize U-Boot prompt after network
  error".
- **Verification retry.** If `tftpboot` succeeded but the byte count or the RAM check did not match,
  only the transfer is repeated, with no resync and no network reconfiguration.
- **RAM verification after every successful transfer**, retries included: `hash sha256` if
  available, otherwise `crc32`.
- **No `ping`.** Lost ICMP during link/ARP bring-up no longer stops the job; the transfer plus the
  RAM check is the proof that the network works.
- **Bounded server waits:** RRQ 30 s, option negotiation 8 × 1 s, block ACK 10 × 1 s.
- **Windows:** `WSAECONNRESET`/`WSAECONNABORTED` UDP errors from a stale peer count as noise and do
  not break the transfer.

<a id="stock-login"></a>
### Stock LAN assist: logging in to stock Linux over UART (Porting Collector)

This applies only to the Porting Collector on a Nokia XG-040G-MD/MF running factory firmware. Stock
Linux asks `Login:` on the UART, and it changes the service-account passwords late, near the end of
boot. So the current credentials are taken from the router's own Web UI.

1. On a stock `Login:` the probe **does not ask for a login right away**. It prints "[STOCK] Stock
   Linux login detected; obtaining current credentials through 192.168.1.1…".
2. For up to **90 s** it waits for the stock Web UI on `192.168.1.1`, reading the UART all the time,
   so a long stock boot cannot overflow the port's receive buffer. Then it:
   - logs in to the Web UI with the stock administrator account and an encrypted form;
   - checks that the model is XG-040G-MD or XG-040G-MF;
   - reads the current Telnet and FTP credentials.
3. It tries the UART login **directly as the UID-0 service account** (FTP; `user_ftp` on MF). If the
   serial getty refuses it, it logs in with the Telnet account (`user-telnet`) and runs `su` to the
   UID-0 account.
4. It runs **`id -u`**. Only `0` counts as success: "[OK] UART stock shell: UID 0 confirmed".
5. If UID 0 was not obtained and stock FTP is off, **one `y/N` question** offers to enable FTP
   through the stock Web UI.
   - This is a **stock-settings change**, and the question says so. FTP stays enabled after the
     probe, which the result records.
   - No raw MTD or firmware is written.
   - Credentials are re-read, then the login and `su` are retried.
6. If a login succeeds without UID 0, the read-only Linux diagnostics continue with the current
   privileges. If the stock assist fails completely, the probe asks for a user and password by hand
   as before.

**Retry after late password rotation.** A reachable Web UI does not prove that stock init has
finished rotating service passwords. If the first attempt does not prove UID 0, the probe keeps
draining UART for another 12 seconds, re-reads Web credentials once, and retries the login/`su`.
If FTP is off and the page does not expose an FTP account yet, that does not block the Telnet login
or the later explicit FTP question. After confirmed FTP enablement, usable FTP credentials are
required.

**Credential safety.** Passwords live in memory only. They are never printed or written to the
bundle, `transcript.jsonl` or `uart.log`, which record only a "password (masked)" label. A password
is sent to the UART only after the device has shown `Password:`.

**Command line.** The stock assist is enabled with `--stock-lan-assist`. It only reads credentials;
the command line never enables FTP. If `--linux-user` / `--linux-password` are given, they are used
instead. If the Web administrator password is not the factory one, set it with the
`URSIDO_STOCK_WEB_PASSWORD` environment variable.

The probe result records `linux_uid0`, `stock_lan_assist` and `stock_service_provisioned` (FTP was
enabled).

## 6. Running

**Interactive:**

```
Windows:  double-click UrsidoRescue.exe   (or run it from cmd/PowerShell)
Linux:    ./UrsidoRescue-linux-amd64
```

The language is asked at start. Preset it with `--lang ru|en` (anywhere on the command line) or the
`URSIDO_LANG=ru|en` environment variable. All menu items are described in [MENU_EN.md](MENU_EN.md).

On an interactive terminal the program's messages are coloured (the UrsusBoot/UrsusFlasher
palette). `NO_COLOR=1`, `TERM=dumb` or redirecting output to a file gives plain text. The router's own
output and the UART logs are never coloured.

**Full-screen mode (TUI):** `--tui` opens a full-terminal interface: the same menus as the console
(Main, Porting, Expert), the UART and event log always at the bottom, confirmations in their own
window. Handy over SSH, on a Raspberry Pi and on other Linux systems without a desktop; the terminal
must be at least 80×24.

```
./UrsidoRescue-linux-arm64 --tui
UrsidoRescue.exe --tui --lang en
```

Next to the menu the TUI explains what the selected item does, what it needs and its risk. Yes/no
questions and short options are buttons (← → and Enter), lists (COM port, profile) use the arrows; typing
an answer still works. The hotkeys also work with the Russian layout and through F-keys.

| key | action |
|---|---|
| ↑ ↓, ← → / Tab | pick an item and a section; in a dialog, pick an option |
| Enter | run the item; answer a dialog |
| p, F4 | choose and connect the UART port (or disconnect) |
| s, Ctrl+C | STOP during an operation; the core decides (a second Ctrl+C within 3 s forces exit) |
| PgUp / PgDn, End | scroll the log, jump to new lines |
| f, F2 | log filter: all / UART / events |
| m, F3 | large log |
| q, F10 | quit (when no operation runs) |

The UART terminal, UART Shell and "RAM U-Boot and prompt" open full screen as in the console; the TUI
comes back when you leave them. Without `--tui` the console menu starts as before. With `TERM=dumb`
the TUI is unavailable: the program says so and opens the console menu. `NO_COLOR` turns colours off.

**Command line:**

| command | does |
|---|---|
| `--version` | prints the version |
| `--selftest` | self-check: pinned payload SHA256, FIP headers, probe allowlist, XMODEM CRC, parsers. `SELFTEST PASS` / `SELFTEST FAIL: …` |
| `probe [flags]` | Porting Collector without the menu |
| `export [--input DIR] [--output DIR] [--redact]` | repack a bundle from an existing probe directory (default: the latest probe session under `work/sessions/`; old `work/probe-*` are found too) |

`probe` flags:

| flag | meaning |
|---|---|
| `--uart PORT` | port (`COM6`, `/dev/ttyUSB0`). Optional: `probe` finds ports the same way as the menu (`QueryDosDeviceW` on Windows). No port → an error with exit code 2; exactly one → selected automatically; several → a numbered list is printed, exit code 2, re-run with `--uart PORT` |
| `--output DIR` | probe data directory (default: a new session `work/sessions/<date>-<time>-probe-<hex>/`) |
| `--uboot-only` / `--linux-only` / `--no-linux` | limit environments (the first two exclude each other) |
| `--bootrom` | answer `Press x` and check the XMODEM `C` |
| `--ram-uboot md\|mf` | bricked Nokia: load UrsidoRescue's RAM U-Boot and probe through it |
| `--timeout 5m` | overall timeout |
| `--sample 4k\|64k` | head sample size per partition |
| `--linux-user U --linux-password P` | credentials if Linux asks for a login |
| `--stock-lan-assist` | stock Nokia MD/MF: fetch the current credentials through the Web UI at `192.168.1.1` and log in over UART with a UID-0 check ([details](#stock-login)); never enables FTP |
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
| `work/sessions/<date>-<time>-<operation>-<hex>/` | a **session**: every menu operation runs in its own (probe items share one until "N") |
| `…/uart.log` | full raw UART log of the operation (formerly `work/recovery-*`, `term-*`, `shell-*.uart.log`) |
| `…/session.log` | timestamped program events and questions (answers are not written: they can be passwords) |
| `…/operations.jsonl` | structured log: operation start and result, every U-Boot command with return code and duration, confirmations, produced files |
| `…/errors.log` | failures |
| `…/session.json` | operation kind, version, front end, times, result (`success` / `cancelled` / `failed`), operation IDs |
| `…/stock-<time>/`, `…/physical-<time>/` | temporary image chunks (items 1 and 3) |
| probe session | plus the Porting Collector data: `transcript.jsonl`, `uboot/`, `linux/`, `flash/`, `dt/`… |
| `UrsidoRescue-support-<time>.zip` | report log bundle (item 6) |
| `ursus-probe-<vendor>-<model>-<soc>-<time>.zip` | porting bundle |

The `stock-*` and `physical-*` directories inside a session take as much space as the source image (up
to 256 MiB); after a successful restore they can be deleted. Files from older versions in `work/`
(`*.uart.log`, `probe-*`) still go into the log bundle and are found by `export`.

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
| `[WARN] stock LAN assist failed: stock LAN assist timed out` | The router's Web UI did not answer within 90 s. Cable in LAN2/LAN3, PC on `192.168.1.x`, extra adapters off? If the Web administrator password was changed, set `URSIDO_STOCK_WEB_PASSWORD`. The probe then asks for a login by hand |
| `stock Web model "…" is not a supported XG-040G-MD/MF` | The stock assist only works with the Nokia XG-040G-MD/MF. Type the login by hand or use `--linux-user` |
| `[WARN] UART login succeeded without UID 0…` | Logged in, but not as root. Diagnostics continue with the current privileges; for full results answer `y` to the FTP question if it was asked |
| `bad block in raw-critical stock region …` | Automatic stock restore is unsafe on this NAND. Needs manual analysis |
| `backup BL2 begins with … OpenWrt preloader` | This is not a stock backup; it was taken after reflashing |
| `readback CRC mismatch` | The write did not verify. BL2 is still untouched (it is always last), so you can start over |

For a report: main menu → **6**, attach `UrsidoRescue-support-*.zip` and describe what you did.
Review the logs for MACs/serials before publishing.
