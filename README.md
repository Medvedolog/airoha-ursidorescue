# UrsidoRescue

Native Windows/Linux UART tool for Airoha routers (Nokia XG-040G-MD / AN7581, XG-040G-MF / AN7583):
BootROM XMODEM recovery with a RAM U-Boot, stock / FIP / physical-NAND restore, recovery ITB boot,
diagnostics, a transparent UART shell, and a read-only **Porting Collector** that profiles unknown
Airoha devices (see [PROBE.md](PROBE.md)). Russian / English UI. Status: simulation PASS / HW HOLD (see STATUS.md).

No Python, pyserial or TFTP utilities are needed; one static binary per platform.

> LAB / UART test builds. 3.3 V TTL UART, GND/TX/RX only — never connect VCC.
> See [STATUS.md](STATUS.md) for what has and has not been verified on hardware.

## Build

    ./build.sh          # vet, tests, Windows x64 + Linux x86_64/aarch64 into dist/

The binary must sit next to `VERSION` and `payloads/`.

## Payloads

| file | source |
|---|---|
| `payloads/md/an7581-preloader.bin` | airoha-router-ursusflasher, OpenWrt AN7581 UBI preloader |
| `payloads/md/an7581-fudan-capable-ram.fip` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-md/recovery-safe-u-boot.fip` |
| `payloads/mf/an7583-preloader.bin` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-mf/ursusboot-uart-preloader.bin` |
| `payloads/mf/an7583-recovery-ram.fip` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-mf/recovery-safe-u-boot.fip` |

Sizes and SHA256 are pinned in `main.go`; `--selftest` checks them.

## Layout

- `main.go`, `main_probe.go`, `lang.go` — recovery workflows, menus, CLI (`probe`, `export`, `--lang`)
- `serial_*.go`, `console_*.go` — native serial port and raw console for Windows and Linux
- `probe/` — read-only discovery: command allowlist (`guard.go`), collector, parsers, DTB parser,
  ursus-profile-v1 analysis, reports and bundle export; tested against a simulated board
