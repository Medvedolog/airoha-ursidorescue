# About UrsidoRescue

[Русская версия](ABOUT_RU.md) · [Contents](README.md)

## What it is

**UrsidoRescue** is a PC tool that rescues Nokia routers built on Airoha SoCs over UART, when the
web UI, SSH and the router's own bootloader no longer help. Supported boards:

| model | SoC | NAND |
|---|---|---|
| Nokia XG-040G-MD | Airoha AN7581 | 256 MiB SPI-NAND |
| Nokia XG-040G-MF | Airoha AN7583 | 256 MiB SPI-NAND |

It does four things:

1. **Revives a brick through the BootROM.** The Airoha loader in the SoC mask ROM (the BootROM)
   accepts, with Reset held, a preloader (BL2) and then a FIP (BL31 + U-Boot) over XMODEM.
   UrsidoRescue feeds it verified files and gets a working U-Boot in RAM **without writing flash**.
2. **Restores flash from that RAM U-Boot:** stock Nokia firmware from an `mtd16` backup, the
   OpenWrt `fip` UBI volume, a full 256 MiB NAND image. Files travel over a built-in TFTP server,
   every written range is read back and compared by CRC32, and BL2 is always written last.
3. **Provides tools:** booting an OpenWrt initramfs/recovery ITB straight from RAM, NAND/UBI
   diagnostics, a UART terminal with history, a pager and manual XMODEM, writing one UBI volume or
   a raw MTD range.
4. **Explores unknown Airoha devices** (Porting Collector, read-only) and produces a porting
   bundle: an `ursus-profile-v1` profile, a draft UrsusBoot port report and a draft UrsusFlasher
   device description.

The UI is bilingual (Russian / English). All it needs is the binary with `VERSION` and
`payloads/` next to it: no Python, pip, pyserial or separate TFTP programs.

### What UrsidoRescue is *not*

- **Not** an OpenWrt installer. The normal install paths are MedveFlasher (no UART) and UrsusFlasher.
- **Not** a factory-data generator. MAC, serial number, GPON identity and calibration live only in
  your backup. No backup, nothing to restore.
- A **lab** build: flash writes have not yet completed a full hardware validation cycle (see
  [../STATUS.md](../STATUS.md)).

## The Ursus family

*Ursus* is Latin for "bear", and *Ursidae* is the bear family. All projects of the family target the
same Nokia/Airoha routers and share formats, files and safety conventions. The name UrsidoRescue
refers to that "bear family": it is the rescue tool for all the others.

```text
                ┌────────────────────────────┐
                │ UrsusBoot (in the router)  │  U-Boot + recovery layer
                │ airoha-ursusboot           │  WebFailsafe, UBI, FIP
                └───────────▲───────▲────────┘
     installs, updates,     │       │  RECOVERY_SAFE RAM FIPs
     backs up               │       │  (payloads/*/…ram.fip)
┌───────────────────────────┴──┐  ┌─┴───────────────────────────┐
│ UrsusFlasher (PC)            │  │ UrsidoRescue (PC)           │
│ airoha-router-ursusflasher   │  │ airoha-ursidorescue         │
│ install/backup orchestrator  │  │ UART rescue + Porting       │
└──────────────▲───────────────┘  │ Collector                   │
               │ AN7581 preloader └─▲───────────────────────────┘
               │                    │ mtd16 backup, RC18 contract
┌──────────────┴────────────────────┴─┐
│ MedveFlasher (PC)                   │
│ nokia-router-medveflasher           │
│ stock Nokia → OpenWrt without UART  │
└─────────────────────────────────────┘
```

### UrsusBoot: `airoha-ursusboot`

https://github.com/Medvedolog/airoha-ursusboot · current version `0.1.0-alpha5-t66`

A compact recovery U-Boot for Airoha: the ordinary U-Boot 2026.07 with the OpenWrt and Airoha
patches, trimmed to what a router needs (a ~512 KiB boot area), plus a WebFailsafe with a real
U-Boot console in the browser, OpenWrt sysupgrade/FIT awareness, UBI creation and migration, FIP
self-update and BootROM/UART recovery paths.

**Link to UrsidoRescue:** the RAM FIPs of both boards (`payloads/md/an7581-fudan-capable-ram.fip`,
`payloads/mf/an7583-recovery-ram.fip`) and the MF preloader (`payloads/mf/an7583-preloader.bin`)
come from the UrsusBoot `v0.1.0-alpha5-t66` release. They are **RECOVERY_SAFE** builds:
`bootdelay=-1`, `bootcmd` only prints text, and the saved environment goes to UBI volumes that never
exist. Such a U-Boot neither boots nor writes anything by itself. It knows the Fudan FM25S01A and
FM25G02B NAND chips (the older MedveFlasher RC18 RAM U-Boots did not know FM25G02B).

Note: the RAM FIP is **not UrsusBoot code**. It holds a vanilla OpenWrt U-Boot (`3d1645ee` + PR 24025 with
FM25G01B/FM25G02B), built by the UrsusBoot pipeline under the RC18 contract and packed into the pinned
RC18 FIP (BL31 byte-exact). The UrsusBoot build itself checks that it contains neither `UrsusBoot-` nor
`ursusdispatch`. UrsusBoot gives the rescue path a board-specific build, provenance and QA, not its own
logic, so the `ursus*` commands are absent from the RAM path on purpose: the rescue path does not depend on
the bootloader it may have to repair. Using a live persistent UrsusBoot is a separate planned layer, see
`UI_SPEC_RU.md` §9a.

The UrsidoRescue
porting report (`ursusboot-porting-report.md`) is input for a new UrsusBoot port.

### UrsusFlasher: `airoha-router-ursusflasher`

https://github.com/Medvedolog/airoha-router-ursusflasher · current version `0.2.66`

The PC orchestrator (Python 3.12+, Windows/Linux): detects the router state, makes backups,
installs and updates UrsusBoot from stock Nokia, OpenWrt or UrsusBoot recovery, runs the ONECLICK
and EXPERT flows and handles emergency BootROM/UART recovery.

**Link to UrsidoRescue:** the MD preloader (`payloads/md/an7581-preloader.bin`) comes from the
UrsusFlasher kit (OpenWrt AN7581 UBI preloader). `ursusflasher-device-draft.json` in a porting bundle
is a draft device profile for UrsusFlasher. UrsidoRescue is a narrow native UART rescuer with no
Python; UrsusFlasher is the full installer.

### MedveFlasher: `nokia-router-medveflasher`

https://github.com/Medvedolog/nokia-router-medveflasher · current version `1.0.0-rc35`

The oldest project of the family: moves the Nokia XG-040G-MD/MF from stock to OpenWrt **with no UART
and no disassembly**: backup → transitional OpenWrt → permanent system in UBI. Python 3 without pip,
with its own COM port, TFTP, XMODEM and AES/RSA implementations.

**Link to UrsidoRescue:**
- main menu item 1 accepts a **MedveFlasher backup directory** (`mtd0…mtd16`, `SHA256SUMS`) and
  restores `mtd16` from it, the `0x0EBA0000`-byte "stock restore span";
- when checking a backup, UrsidoRescue rejects a BL2 that is actually a "MedveFlasher lineage"
  OpenWrt preloader: such a backup was taken after reflashing;
- the RECOVERY_SAFE RAM U-Boot contract ("RC18") started in MedveFlasher.

### UrsidoRescue: `airoha-ursidorescue`

https://github.com/Medvedolog/airoha-ursidorescue · this repository · version `0.2.0-test17`

The native UART rescuer and porting data collector. Its place in the family is the "last line":
when nothing in the router works except the BootROM.

### Family rules

- **Fail-closed.** Anything unclear stops the operation instead of "let's try on".
- **Readback after write.** Every flash write is read back and compared.
- **Pinned hashes.** Bundled binaries are checked by size and SHA256 before use.
- **Device identity is untouchable.** MAC/serial/GPON are never generated or published;
  `profile.json` records only *where* they live.
- **BUILD PASS ≠ HW PASS.** Builds and simulations do not replace hardware tests; the status of
  every feature is stated honestly in `STATUS.md`.

## Why UrsidoRescue is written in Go

The other PC tools of the family are Python. UrsidoRescue uses Go (1.24, `CGO_ENABLED=0`; the core uses the standard library only). Reasons:

1. **One static file per platform.** `UrsidoRescue.exe` for Windows x64 and
   `UrsidoRescue-linux-amd64` / `-arm64` for Linux, with no installer, runtime or DLLs. Someone
   whose router just became a brick should not have to install Python and debug pip offline (the
   cable is already plugged into the router).
2. **Cross-compiling from one machine.** `build.sh` builds all three targets on Linux in CI with
   one command. Linux arm64 lets you rescue a router from a Raspberry Pi or an ARM laptop.
3. **The standard library has everything needed.** UDP for the TFTP server (`net`), `archive/zip`,
   `compress/gzip` (compressed backups), `crypto/sha256`, `hash/crc32`, `encoding/binary`, `flag`.
   The serial port and console are implemented directly with system calls: termios on Linux,
   `kernel32.dll` (`CreateFileW`, `SetCommState`, `ReadConsoleInputW`, `QueryDosDeviceW` …) on
   Windows, through `syscall`, without cgo or third-party packages. The only dependencies are the pure-Go Charm libraries
   (Bubble Tea, Lip Gloss, Bubbles) for the full-screen `--tui`, pinned in `go.mod` / `go.sum`.
4. **Concurrency without pain.** The UART terminal reads the port and the keyboard and writes the
   log at the same time; the TFTP server runs next to the U-Boot dialogue. Goroutines and channels
   make that simple and reliable.
5. **Strictness and testability.** Static typing, `go vet`, `gofmt` in CI and fast tests, including
   a full simulated board for the Porting Collector. For a tool that writes flash this matters more
   than development speed.
6. **Reproducibility.** `-trimpath -ldflags "-s -w"` and `SHA256SUMS` for the whole release.

The cost: Windows console input had to be written by hand (`ReadConsoleInputW`, see
[CHANGELOG_EN.md](CHANGELOG_EN.md), test8), and the `.exe` icon is embedded by a small in-house
tool, `tools/embedicon`, since there is no `windres` without MinGW.
