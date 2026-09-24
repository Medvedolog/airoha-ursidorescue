<div align="center">

<img src="assets/ursus-bear.svg" alt="UrsidoRescue bear" width="112" height="112">

# UrsidoRescue

### The rescue bear cub of the Ursus family

**Brings bricked Airoha routers back over UART, even when only the BootROM is left alive**

[![Pre-release](https://img.shields.io/github/v/release/Medvedolog/airoha-ursidorescue?include_prereleases&label=pre-release&color=c8873a)](https://github.com/Medvedolog/airoha-ursidorescue/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/Medvedolog/airoha-ursidorescue/build.yml?branch=main&label=build)](https://github.com/Medvedolog/airoha-ursidorescue/actions/workflows/build.yml)
![Go](https://img.shields.io/badge/Go-1.23%20·%20zero%20deps-00add8?logo=go&logoColor=white)
![Platforms](https://img.shields.io/badge/Windows%20·%20Linux%20x64%20·%20arm64-single%20binary-6f4b2f)
![SoC](https://img.shields.io/badge/Airoha-AN7581%20·%20AN7583-b36b32)
![UI](https://img.shields.io/badge/UI-RU%20·%20EN-555)

**[📦 Releases](https://github.com/Medvedolog/airoha-ursidorescue/releases)** · [📖 Documentation](doc/README.md) · [🧭 Operator guide](doc/GUIDE_EN.md) · [📝 Changelog](doc/CHANGELOG_EN.md)

🇷🇺 [Русская версия](README.ru.md)

</div>

---

## Why it exists

Nokia XG-040G-MD and XG-040G-MF routers run Airoha SoCs, and a failed flash, a broken bootloader or
a wrong image can leave them with nothing working: no web UI, no SSH, no U-Boot. What is left is
the **BootROM** in the SoC itself, which can still receive code over a serial line.

UrsidoRescue is built for that moment. It talks to the BootROM over UART, loads a verified U-Boot
**into RAM only**, and from there repairs flash step by step, checking every write before it moves
on. It is the family's last line of defence: when UrsusBoot, UrsusFlasher and MedveFlasher can no
longer reach the router, the bear cub still can.

Its second job is **exploration**. On an Airoha device nobody has ported yet, it collects everything
a new UrsusBoot port needs, strictly read-only, and packs it into one bundle.

## What the rescue bear can do

| | |
|---|---|
| 🧸 **Revive a brick** | BootROM → preloader → RAM U-Boot over XMODEM, with nothing written to flash |
| 🏭 **Return to stock** | Restore factory Nokia firmware from an `mtd16` / MedveFlasher backup, BL2 written last |
| 🔧 **Repair the boot** | Replace the OpenWrt `fip` UBI volume when the system is intact but will not start |
| 💾 **Restore all of NAND** | Write a full 256 MiB raw image with readback of every chunk |
| 🚀 **Boot from RAM** | Start an OpenWrt initramfs / recovery ITB without touching flash |
| 🩺 **Diagnose** | Bad blocks, MTD layout and environment, fully read-only |
| ⌨️ **UART terminal** | Low-latency console with history, pager, manual XMODEM and full logging |
| 🔍 **Explore new hardware** | Porting Collector: read-only profile, DTB, flash map, UrsusBoot port report |
| 🔑 **Reach stock root** | On stock Nokia firmware, fetch current credentials via the Web UI and prove UID 0 over UART |

Everything needed is inside one static binary per platform: its own serial driver, XMODEM, TFTP
server and DTB parser. No Python, no pip, no extra tools, no internet required.

## Principles

- **Fail-closed.** Anything unclear stops the operation; nothing more is written after a stop.
- **Verify every write.** Each written range is read back and compared; transfers are checked in RAM.
- **BL2 last.** The bootloader stage is written only after everything else has verified.
- **Pinned payloads.** Bundled binaries are checked by size and SHA256 before every use.
- **Your identity stays yours.** MAC, serial and GPON data are never generated or exported;
  passwords stay in memory only.
- **Honest status.** Simulation is not hardware. What has been proven on real routers is written
  in [STATUS.md](STATUS.md).

## Supported hardware

| Model | SoC | Flash |
|---|---|---|
| Nokia XG-040G-MD | Airoha AN7581 | 256 MiB SPI-NAND |
| Nokia XG-040G-MF | Airoha AN7583 | 256 MiB SPI-NAND |

The Porting Collector works with other Airoha devices too, read-only.

## Quick start

1. Download the ZIP from [Releases](https://github.com/Medvedolog/airoha-ursidorescue/releases)
   and unpack it. Keep the binary next to `VERSION` and `payloads/`.
2. Connect a **3.3 V** USB-UART adapter: GND, TX, RX. **Never connect VCC.**
3. For TFTP work, cable the PC to LAN2/LAN3 and give it `192.168.1.254/24`.
4. Run `UrsidoRescue.exe` (Windows) or `./UrsidoRescue-linux-amd64` and follow the menu.

Everything else, step by step, is in the [operator guide](doc/GUIDE_EN.md) and the
[menu reference](doc/MENU_EN.md).

> [!WARNING]
> These are **lab UART test builds**. Some write paths have not yet completed full hardware
> validation; see [STATUS.md](STATUS.md). Always keep a backup: factory MAC, serial and optical
> calibration cannot be recreated without one.

## Documentation

| | |
|---|---|
| [About the project](doc/ABOUT_EN.md) | Purpose, the Ursus family, why Go |
| [Operator guide](doc/GUIDE_EN.md) | Wiring, network, XMODEM and TFTP behaviour, CLI, troubleshooting |
| [Menu reference](doc/MENU_EN.md) | Every menu item, step by step |
| [Architecture](doc/ARCHITECTURE_EN.md) | Source layout, NAND layout, payloads, build, CI, tests |
| [Porting Collector](PROBE.md) | Probe safety rules and collected data |
| [Changelog](doc/CHANGELOG_EN.md) | Complete history since the beginning |

## The Ursus family

| Project | Role |
|---|---|
| [UrsusBoot](https://github.com/Medvedolog/airoha-ursusboot) | Recovery-aware U-Boot inside the router |
| [UrsusFlasher](https://github.com/Medvedolog/airoha-router-ursusflasher) | Installer, backup and recovery workstation |
| [MedveFlasher](https://github.com/Medvedolog/nokia-router-medveflasher) | Stock Nokia → OpenWrt without UART |
| **UrsidoRescue** | The UART rescue bear cub and hardware explorer |

## Building

```sh
./build.sh    # vet, tests, Windows x64 + Linux x86_64/arm64 into dist/, selftest
```

Only Go ≥ 1.23 is required. Details: [architecture](doc/ARCHITECTURE_EN.md#build).
