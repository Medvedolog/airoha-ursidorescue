# UrsidoRescue architecture

[Русская версия](ARCHITECTURE_RU.md) · [Contents](README.md) · version 0.2.0-test17

## Overview

```text
                         ┌──────────── main.go ────────────┐
 operator ── menus ─────▶│ recovery wizards                │
 (lang.go: RU/EN)        │ acquireRAMUBoot → BootROM/XMODEM│
                         │ ubootCommand (+RC marker)       │
                         │ TFTP server, checks, selftest   │
                         └──┬──────────────┬───────────────┘
                            │              │
          term.go/term_run.go            main_probe.go ──▶ probe/  (read-only)
          UART terminal                  menu/CLI          guard → collect → analyze
                            │              │                → report → bundle
                            ▼              ▼
                  serial_{linux,windows}.go   console_{linux,windows}.go
                  (serial port, 115200 8N1)   (raw console, input, window size)
```

One Go module, `ursidorescue` (Go 1.23), with **no external dependencies**: standard library only.
Platform differences live in `_linux.go` / `_windows.go` files (file-name build constraints);
everything else is shared.

## Top-level files

| file | lines | purpose |
|---|---|---|
| `main.go` | ~2640 | NAND layout constants, MD/MF profiles with pinned SHA256, `realMain` (argument parsing, release-root lookup), main and expert menus, profile and port choice, BootROM wait and XMODEM send, U-Boot prompt wait, U-Boot commands with return codes, TFTP server, RAM checks and readback, all wizards (stock, FIP, physical, ITB, diagnostics, expert 3/4), log bundle, `--selftest` |
| `main_probe.go` | ~590 | Glue to the `probe` package: the Porting menu, BootROM submenu, `probe`/`export` CLI (including `--stock-lan-assist`), exit codes, profile summary |
| `stock_access.go` | ~540 | Stock LAN assist for the Nokia XG-040G-MD/MF: stock Web HTTP client (RSA + AES login-form encryption, as in UrsusFlasher), model check, Telnet/FTP credentials (`storage.cgi?ftp_config`), enabling FTP via `storage.cgi` (only on confirmation), `stockLANLoginAssist` with a 90 s wait for the Web UI. The Web administrator password can be overridden with `URSIDO_STOCK_WEB_PASSWORD` |
| `console_ui.go` | ~130 | Coloured operator output (UrsusBoot/UrsusFlasher palette): `paint`, `uiStatus`, `uiEvent` with a content-based tone, the "Network prerequisites" block and the active IPv4 interface list. Colour is off with `NO_COLOR`, `TERM=dumb` and non-terminal output |
| `lang.go` | 83 | UI language: `L(ru, en)`, `--lang`, `URSIDO_LANG`, locale, selection dialogue |
| `term.go` | ~400 | Terminal logic without I/O: ANSI key decoder, Windows `KEY_EVENT_RECORD` translation, line editor with history, XMODEM receive (CRC, 128/1K) |
| `term_run.go` | ~640 | The running terminal: UART read loop, raw/line mode, ASCII gate, pager with fullscreen-TUI bypass, Ctrl+] menu, XMODEM send/receive |
| `serial.go` | 11 | The `Serial` interface: `Name`, `Read(buf, timeout)`, `Write`, `ResetInput`, `Close` |
| `serial_linux.go` | ~110 | termios via `ioctl(TCGETS/TCSETS)`: raw 115200 8N1, `CLOCAL`, no flow control; lists `/dev/ttyUSB*`, `ttyACM*`, `ttyAMA*`, `ttyS*` |
| `serial_windows.go` | ~170 | `kernel32.dll` via `syscall`: `CreateFileW`, `SetCommState`, `SetCommTimeouts`, `PurgeComm`, `ReadFile`/`WriteFile`; lists existing `COMn` via `QueryDosDeviceW` |
| `udp_windows.go`, `udp_other.go` | 18 / 5 | `isExpectedUDPNoise`: on Windows, UDP `WSAECONNRESET` (10054) / `WSAECONNABORTED` (10053) errors from a stale peer are noise for the TFTP server; always `false` elsewhere |
| `console_linux.go` | 60 | Raw console mode, window size (`TIOCGWINSZ`) |
| `console_windows.go` | ~150 | `ReadConsoleInputW`, console modes, VT output (ANSI), QuickEdit kept |
| `*_test.go` | | Main package tests (see below) |
| `tools/embedicon/` | ~320 | Build tool: embeds the icon (7 sizes, 16–256 px) as `RT_ICON`/`RT_GROUP_ICON` resources into a built PE32+ `.exe`, pure Go |
| `assets/` | | `ursus-bear.svg` (source) and `ursus-bear.png` (256 px): the brand bear |
| `payloads/` | | BootROM binaries: preloader and RAM FIP for MD and MF |
| `build.sh` | | Release build |
| `.github/workflows/build.yml` | | CI |

## The `probe/` package (Porting Collector)

It deliberately does **not import** any recovery code. Everything it writes to the port goes
through two entry points, and a test proves there are no others.

| file | purpose |
|---|---|
| `guard.go` | U-Boot and Linux command allowlist (`CheckUBoot`, `CheckUBootAttach`, `CheckLinux`): full names, no abbreviations, `;`, `&&`, `\|`, `$`, quotes or newlines; destructive commands never pass |
| `internal.go` | The three fixed internal line templates (hush test, return-code marker, Linux wrapper `echo URSIDO_B_n; cmd 2>&1; echo URSIDO_E_n_$?`) |
| `session.go` | Session: port, `uart.log`, `transcript.jsonl`, timeouts (`Timing`, so tests run fast) |
| `console.go` | Classifies the last line: U-Boot prompt, Linux shell, login, unknown |
| `collect.go` | Collection by layer (`BootROM`, `UBoot`, `Linux`, `Flash`, `DT`, `Network`, `GPIO`), autoboot stop, waiting for Linux, login: stock LAN assist via `LinuxLoginAssist` (credentials in `LinuxLoginPlan`, memory only), UART login, `su`, UID-0 proof via `id -u`, the FTP question |
| `parse.go` | Parsers: `md.b`/`mtd dump` hex dumps, `mtd list`, `/proc/mtd`, sysfs, `ubinfo`, `printenv`, `bdinfo`, `help`, `mii`, boot log and SoC |
| `fdt.go` | Own FDT/DTB parser and `.dts` decompiler (no `dtc`) |
| `dtmeta.go` | Device-tree digest: flash partitions, Ethernet/PHY, GPIO buttons and LEDs |
| `signature.go` | What a partition sample contains: UBI EC/VID, TF-A FIP ToC, FIT, empty, etc. |
| `analyze.go` | Merges all sources into `ursus-profile-v1`: every fact has `source` and `confidence`; two agreeing sources = high, disagreement → `conflicts` |
| `profile.go` | The profile schema (`SchemaV1`) |
| `sanitize.go` | Recursive removal of identity (MAC, serials, GPON/PLOAM, credentials) from the profile and reports |
| `report.go` | `ursusboot-porting-report.md` and `ursusflasher-device-draft.json` |
| `bundle.go` | `README.txt`, `hashes.sha256`, zip; `--redact` mode |
| `lang.go` | Operator message language (developer files are always English) |
| `probe_test.go`, `sim_test.go` | Safety and parser tests and a full run against a **simulated AN7581 board** (generated DTB, U-Boot and Linux answers) |

## NAND layout and addresses (from `main.go`)

```text
256 MiB SPI-NAND (0x10000000), eraseblock 0x20000, page (min I/O) 0x800

0x00000000 ┌──────────────┐
           │ bl2          │ 0x20000   BL2 / preloader
0x00020000 ├──────────────┤
           │ ubi          │ 0x0FFE0000  everything else (OpenWrt UBI or the stock layout)
           │  …           │
0x0EBA0000 │ ← end of the "stock restore span" (mtd16): BL2 + stock IBU area
           │  …           │
0x10000000 └──────────────┘

Window where bad blocks are tolerated during stock restore (physical addresses):
  0x052C0000 … 0x0EB60000
```

| constant | value | meaning |
|---|---|---|
| `loadAddr` | `0x90000000` | where TFTP puts a file in RAM |
| `verifyAddr` | `0x98000000` | where readback data goes |
| `chunkSize` | `0x00800000` | 8 MiB chunks for large images |
| `maxGenericRAMFile` | `0x08000000` | 128 MiB, the limit for one transfer |
| `defaultRouterIP` / `defaultLocalIP` | `192.168.1.1` / `192.168.1.254` | U-Boot / PC addresses |
| `defaultTFTPPort` | `1069` | UDP port of the PC TFTP server |

## Payloads and pinning

The `md`/`mf` profiles in `main.go` hold the path, size and SHA256 of every file in `payloads/`.
`validatePinned` checks them before every BootROM send and in `--selftest`. A replaced file without
a profile update means the program refuses to work. File provenance:

| file | source |
|---|---|
| `payloads/md/an7581-preloader.bin` | airoha-router-ursusflasher, OpenWrt AN7581 UBI preloader |
| `payloads/md/an7581-fudan-capable-ram.fip` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-md/recovery-safe-u-boot.fip` |
| `payloads/mf/an7583-preloader.bin` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-mf/ursusboot-uart-preloader.bin` |
| `payloads/mf/an7583-recovery-ram.fip` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-mf/recovery-safe-u-boot.fip` |

## Key mechanisms

- **Stock LAN assist (`stockLANLoginAssist` → `collector.automaticStockLogin`).** On a stock
  `Login:` the probe runs the assist in a goroutine and keeps reading the UART meanwhile
  (`runLinuxAssist`). For up to 90 s the assist tries to log in to the Web UI at `192.168.1.1`,
  checks the XG-040G-MD/MF model and returns a `LinuxLoginPlan`: the Telnet account (`LoginUser`),
  the UID-0 FTP service account (`RootUser`), `FTPEnabled`. Then `tryStockUARTPlan`:
  - a direct login as `RootUser`;
  - otherwise a login as `LoginUser` and `tryUARTSU`;
  - after each, `currentUID` (`id -u`); only `0` means UID 0.
  With no UID 0, FTP off and an `Ask` available, a `y/N` question follows; `runLinuxAssist(true)`
  enables FTP and re-reads the credentials. Passwords reach the port through `sendKeys`, which
  writes only a label to `uart.log`, and only after `Password:` was recognised. In the CLI the
  assist is enabled with `--stock-lan-assist` and no `Ask` is set, so FTP is never enabled.
- **Colour.** All operator messages go through `console_ui.go`. Raw UART bytes (`logBytes`, the
  terminal) are never coloured.

- **U-Boot return code.** After every command, `echo __URSIDO_<nanoseconds>__RC_$?`; the answer is
  parsed with a regular expression. No marker or `rc≠0` stops the operation.
- **Slow line sending.** 16-byte pieces 3 ms apart: U-Boot's UART buffer is small.
- **XMODEM send (`xmodemSend`).** Returns `xmodemResult{EOTAck, Trailing}`:
  - 128-byte blocks with CRC16; ACK wait 2 s, at most 8 attempts; NAK or `C` retries the block at
    once (`scanXmodemReply`: ACK beats noise in the same read);
  - a receiver abort is only `CAN CAN` in a row; a single `CAN` is ignored;
  - `CAN CAN CAN` from the PC is sent in a `defer`, only while `dataComplete == false`;
  - EOT with a 0.9 s wait; silence hands off at once without EOT retries. `scanXmodemEOTReply`: ACK
    is success; NAK retries EOT (at most 3 times); any
    other non-whitespace byte is a handoff, i.e. next-stage output;
  - on handoff or with no EOT ACK it returns `nil` and keeps the bytes received after EOT in
    `Trailing`.
  The caller must prove the transition: `acquireRAMUBoot` feeds `Trailing` into `waitReceiver` (the
  second receiver after the preloader) or `waitUBootPrompt` (the prompt after the FIP). Without
  proof: the normal timeout and a stop. The terminal's manual send prints `Trailing` and warns
  when there was no EOT ACK.
- **U-Boot prompt detection (`promptPresent`).** ANSI CSI sequences are stripped from the last 8 KiB
  of output, then the end of the stream is checked for `AN7581>`, `AN7583>`, `U-Boot>`, `=>`.
  Autoboot stop: Ctrl-C every 250 ms (up to 20), with a bootmenu only Esc (up to 6).
- **LAN/TFTP (`tftpLoadKnownLocal`).**
  - `detectLocalIP`: the address of the interface routed to `192.168.1.1` (a UDP "dial" that sends
    no packets); fallback: scan active `192.168.1.x` interfaces.
  - `configureUBootNet` (temporary MACs, IP, `serverip`, `tftpdstp`, `autoload`) runs **once per
    session**: in `tftpLoad` and at the start of the stock/physical wizards.
  - Up to **3 attempts** per transfer. Each: a fresh `runTFTPServer` with a cancel channel →
    `tftpboot` → byte count → `verifyRAM` (`hash sha256` or `crc32`).
  - `tftpboot` failure: `close(cancel)` releases UDP/1069, `resyncUBootAfterNetError` (Ctrl-C until
    the prompt, 4 × 1.5 s), `configureUBootNet` again, a backoff of `attempt` seconds.
  - Verification failure (bytes, RAM): only a backoff and a repeated transfer; the network is left
    alone.
  - `ping` is not used.
- **TFTP server (`runTFTPServer`).** A goroutine on UDP:
  - answers only an RRQ for the expected name from `192.168.1.1`;
  - supports options (block size) and checks the transferred byte count;
  - every wait is bounded: RRQ 30 s, OACK 8 × 1 s, block ACK 10 × 1 s;
  - Windows UDP noise is ignored.
- **Fail-closed wizards.** Any error returns an `error` before the next write command; the
  `[STOP]` message says explicitly that no further write/erase was sent.
- **BL2 last** in stock and physical restore; the bad-block map is compared before, between and
  after the writes. A transport retry repeats only the current chunk's RAM upload; chunks already
  written are never rewritten.
- **Terminal.** A separate goroutine reads the UART with a 20 ms timeout; keyboard input is
  forwarded in whole chunks (arrow ANSI sequences stay intact); the pager queues up to 4 MiB;
  fullscreen-ANSI detection survives a sequence split across reads.

## Build

```sh
./build.sh
```

1. `go vet ./...` and `go test ./...`;
2. `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`: `UrsidoRescue.exe` (windows/amd64),
   `UrsidoRescue-linux-amd64`, `UrsidoRescue-linux-arm64`;
3. `go run ./tools/embedicon` embeds the icon into the `.exe`;
4. copies `payloads/`, `VERSION`, `STATUS.md`, `PROBE.md` into `dist/UrsidoRescue-<VERSION>/`.
   The `doc/` folder is currently **not** in the release ZIP, because `build.sh` does not copy it;
   the documentation lives in the repository;
5. writes `SHA256SUMS` for all files;
6. on x86_64 runs the built binary with `--selftest`.

Only Go ≥ 1.23 is needed. No MinGW, windres, Python or ImageMagick.

## CI and releases

`.github/workflows/build.yml` (ubuntu-latest):

1. `gofmt -l .`: any unformatted file fails the build;
2. `./build.sh`;
3. zip `UrsidoRescue-<V>.zip` + `.sha256`, build artifact upload;
4. **pre-release publishing** of `v<VERSION>` in one of three cases:
   - a `v*` tag push (the tag must match `VERSION`);
   - a manual workflow run on `main` with `publish` ticked;
   - a push to `main` whose commit message contains `[publish prerelease]`.
   An existing release with that tag is an error (no overwrite). Release notes come from `STATUS.md`.

**Releasing a version** means changing these together: `VERSION`, `appVersion` in `main.go` (and the
check in `selftest()`), a section in `STATUS.md`, and the "0.2.0-testN" strings in limitation
messages (physical restore, expert 3/4).

## Tests

`go test ./...`: about 50 tests, ~15 s, no hardware needed.

- **Main package:** XMODEM CRC16, prompt detection, bad blocks and good spans, language choice, ANSI
  decoder, line editor and history, Windows key translation, ASCII gate (including Cyrillic from
  Windows), fullscreen-ANSI detection (including across reads), pager, batched escape sequences,
  local Ctrl+Q, XMODEM receive and bad-CRC rejection, the one-transfer limit being exactly 128 MiB,
  XMODEM reply parsing (noise, single `CAN`, `CAN CAN`), the EOT handoff classifier, the prompt after
  the AN7583 ANSI bootmenu, coloured-message tone (`TestEventTone`), stock Web encoding and parsing
  (`TestStockEncodeURL`, `TestStockJSField`, `TestStockPKCS7`).
- **probe:** allowlist (destructive commands blocked, read-only allowed, UBI attach not read-only),
  internal templates and "nothing else writes lines", no destructive literals in probe code, all
  parsers, FDT, FIP, conflict resolution, full probe on a simulated board (with and without UBI
  attach), passivity towards unknown loaders, `--wake` only for known prompts, identity sanitizer,
  offline UBI parsing.
- **tools/embedicon:** icon embedding into a PE.
- **`--selftest`** checks a packed release in place.
