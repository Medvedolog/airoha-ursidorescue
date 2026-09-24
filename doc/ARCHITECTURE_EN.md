# UrsidoRescue architecture

[Русская версия](ARCHITECTURE_RU.md) · [Contents](README.md) · version 0.2.0-test10

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
| `main.go` | ~2300 | NAND layout constants, MD/MF profiles with pinned SHA256, `realMain` (argument parsing, release-root lookup), main and expert menus, profile and port choice, BootROM wait and XMODEM send, U-Boot prompt wait, U-Boot commands with return codes, TFTP server, RAM checks and readback, all wizards (stock, FIP, physical, ITB, diagnostics, expert 3/4), log bundle, `--selftest` |
| `main_probe.go` | ~570 | Glue to the `probe` package: the Porting menu, BootROM submenu, `probe`/`export` CLI, exit codes, profile summary |
| `lang.go` | 83 | UI language: `L(ru, en)`, `--lang`, `URSIDO_LANG`, locale, selection dialogue |
| `term.go` | ~400 | Terminal logic without I/O: ANSI key decoder, Windows `KEY_EVENT_RECORD` translation, line editor with history, XMODEM receive (CRC, 128/1K) |
| `term_run.go` | ~640 | The running terminal: UART read loop, raw/line mode, ASCII gate, pager with fullscreen-TUI bypass, Ctrl+] menu, XMODEM send/receive |
| `serial.go` | 11 | The `Serial` interface: `Name`, `Read(buf, timeout)`, `Write`, `ResetInput`, `Close` |
| `serial_linux.go` | ~110 | termios via `ioctl(TCGETS/TCSETS)`: raw 115200 8N1, `CLOCAL`, no flow control; lists `/dev/ttyUSB*`, `ttyACM*`, `ttyAMA*`, `ttyS*` |
| `serial_windows.go` | ~170 | `kernel32.dll` via `syscall`: `CreateFileW`, `SetCommState`, `SetCommTimeouts`, `PurgeComm`, `ReadFile`/`WriteFile`; lists existing `COMn` via `QueryDosDeviceW` |
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
| `collect.go` | Collection by layer (`BootROM`, `UBoot`, `Linux`, `Flash`, `DT`, `Network`, `GPIO`), autoboot stop, waiting for Linux, login |
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
a profile update means the program refuses to work. File provenance: [../README.md](../README.md#payloads).

## Key mechanisms

- **U-Boot return code.** After every command, `echo __URSIDO_<nanoseconds>__RC_$?`; the answer is
  parsed with a regular expression. No marker or `rc≠0` stops the operation.
- **Slow line sending.** 16-byte pieces 3 ms apart: U-Boot's UART buffer is small.
- **TFTP server.** A goroutine on UDP; answers only an RRQ for the expected name from
  `192.168.1.1`, supports options (block size) and checks the transferred byte count.
- **Fail-closed wizards.** Any error returns an `error` before the next write command; the
  `[STOP]` message says explicitly that no further write/erase was sent.
- **BL2 last** in stock and physical restore; the bad-block map is compared before, between and
  after the writes.
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
4. copies `payloads/`, `VERSION`, `STATUS.md`, `PROBE.md` into `dist/UrsidoRescue-<VERSION>/`;
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
  local Ctrl+Q, XMODEM receive and bad-CRC rejection.
- **probe:** allowlist (destructive commands blocked, read-only allowed, UBI attach not read-only),
  internal templates and "nothing else writes lines", no destructive literals in probe code, all
  parsers, FDT, FIP, conflict resolution, full probe on a simulated board (with and without UBI
  attach), passivity towards unknown loaders, `--wake` only for known prompts, identity sanitizer,
  offline UBI parsing.
- **tools/embedicon:** icon embedding into a PE.
- **`--selftest`** checks a packed release in place.
