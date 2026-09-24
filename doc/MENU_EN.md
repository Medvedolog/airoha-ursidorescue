# UrsidoRescue menus: every item in detail

[Русская версия](MENU_RU.md) · [Contents](README.md) · version 0.2.0-test14

This page explains what every menu item does, in which order, which commands reach the router and
where the program stops by itself. Wiring and network setup are in [GUIDE_EN.md](GUIDE_EN.md).

Contents:

- [How every operation works](#how-every-operation-works)
  - [Choosing the device profile](#choosing-the-device-profile)
  - [Choosing the UART port](#choosing-the-uart-port)
  - [Getting a RAM U-Boot through the BootROM](#getting-a-ram-u-boot-through-the-bootrom)
  - [How U-Boot commands are sent](#how-u-boot-commands-are-sent)
  - [Loading a file into RAM over TFTP](#loading-a-file-into-ram-over-tftp)
  - [Readback after write](#readback-after-write)
  - [Confirmation phrases](#confirmation-phrases)
- [Main menu](#main-menu)
  - [1. Restore stock Nokia firmware from mtd16/all_flash](#1-restore-stock-nokia-firmware-from-an-mtd16all_flash-backup)
  - [2. Repair OpenWrt boot / replace the FIP](#2-repair-openwrt-boot--replace-the-fip)
  - [3. Restore a full physical NAND image](#3-restore-a-full-physical-nand-image-256-mib)
  - [4. Boot an OpenWrt recovery ITB from RAM](#4-boot-an-openwrt-recovery-itb-from-ram)
  - [5. NAND / UBI / U-Boot diagnostics](#5-nand--ubi--u-boot-diagnostics)
  - [6. Build a log bundle for a report](#6-build-a-log-bundle-for-a-report)
  - [7. Porting / hardware discovery](#7-porting--hardware-discovery)
  - [8. Expert mode](#8-expert-mode)
- [UART terminal: keys and menu](#uart-terminal-keys-and-menu)

---

## How every operation works

Started without arguments, the program asks for the language (`1. Русский  2. English`, Russian by
default), prints the 3.3 V TTL warning and shows the main menu. Any error inside an item is printed
as `[STOP] <reason>` with "No further write/erase commands were sent after this error" and returns
to the menu; the port and log are closed.

### Choosing the device profile

Almost every item that needs a RAM U-Boot first asks:

```
Device profile:
  1. Auto (detect from UART if possible)
  2. Nokia XG-040G-MD / AN7581
  3. Nokia XG-040G-MF / AN7583
```

- **Auto** identifies the SoC from BootROM/DRAM-init output (`AN7581DRAMC`, `Airoha AN7581`,
  etc.). Without such text the program asks again, and then you must pick MD or MF.
- If you picked MD but the UART shows AN7583 (or vice versa), it **stops**: "the UART shows …, but
  profile … was chosen". This prevents loading the wrong preloader.

The profile decides which two files from `payloads/` are sent. Their size and SHA256 are pinned in
the code and checked before every send.

### Choosing the UART port

The program lists the ports it finds:

- **Windows**: only `COMn` ports that actually exist (via `QueryDosDeviceW`);
- **Linux**: `/dev/ttyUSB*`, `/dev/ttyACM*`, `/dev/ttyAMA*`, `/dev/ttyS*`.

Enter a list number, a name (`COM6`, `/dev/ttyUSB0`), or on Windows just `6` → `COM6`. With one port,
Enter picks it. Settings are always 115200 8N1, no flow control.

### Getting a RAM U-Boot through the BootROM

The common procedure for main menu items 1–5, expert items 2–5 and the Porting Collector RAM U-Boot
mode. **Nothing is written to flash.**

1. The port is opened and the log `work/recovery-<date-time>.uart.log` is created.
2. Hint: if `Press x to load BL31 + U-Boot FIP` and `CCC` are already on screen, reboot nothing.
   Otherwise power the router off, **hold Reset and power it on** (BootROM mode).
3. The program waits up to 180 s for the BootROM. On `Press x` it sends `x` (at most once every
   2 s). Three `C` in a row mean the BootROM is ready for XMODEM.
4. The **receiver stage** is classified:
   - *preloader*: the normal start;
   - *FIP*: if the output has `Press x to load BL31`, `DRAM flow done` or
     `load BL31 + U-Boot FIP`. The preloader is then **not** sent again.
5. The profile (see above) and the pinned SHA256 of the files are checked.
6. Preloader stage: XMODEM-CRC sends `payloads/<md|mf>/…-preloader.bin`, then waits for `CCC` again
   (up to 180 s) and checks that the SoC did not change.
7. XMODEM-CRC sends the RAM FIP (BL31 + RECOVERY_SAFE U-Boot). XMODEM rules (details in
   [GUIDE_EN.md](GUIDE_EN.md#xmodem)):
   - 128-byte blocks, ACK wait up to 2 s, **at most 8 attempts** per block; NAK or `C` retries only
     that block at once;
   - a single `CAN` is line noise; a receiver abort needs **`CAN CAN`**;
   - after the last block is ACKed: EOT, at most 3 times with 1.5 s waits. **A missing EOT ACK after
     fully ACKed data is not fatal**: the next stage proves success, either the second `CCC`
     receiver after the preloader (step 6) or a stable U-Boot prompt after the FIP (step 8);
   - `CAN CAN CAN` is sent only when the data phase fails, never after the last block was ACKed.
8. Waiting up to 180 s for the U-Boot prompt (output that arrived during EOT counts). Once
   `U-Boot 20…` or `Hit any key to stop autoboot` shows up, the program sends Ctrl-C every 250 ms
   (at most 20 times); with a bootmenu it sends only Esc instead (at most 6 times). **Enter is
   never sent**, so no bootmenu entry gets selected. The prompt (`AN7581>`, `AN7583>`, `U-Boot>`,
   `=>`) is recognised at the end of the stream after ANSI sequences are stripped, so it is found
   after a screen-oriented bootmenu too. It stops if:
   - the output shows `mtd erase ubi` / `Erasing 0x…`: autoboot started erasing NAND;
   - `Starting kernel` / `Booting Linux`: autoboot escaped into Linux;
   - `Press x to load BL31` appears again after the FIP: the device went back to the BootROM.
9. `version` and `mtd list` are run. **Geometry check** (stop without it): `spi-nand0`, block
   `0x20000`, min I/O `0x800`, partitions `bl2` = `0x0–0x20000` and `ubi` = `0x20000–0x10000000`.
10. The NAND vendor (Fudan Micro / SkyHigh / unknown) is printed **for information only**; it
    changes nothing.

### How U-Boot commands are sent

Every command goes out in 16-byte pieces (3 ms apart) plus `\r`, then the program waits for the
prompt (`=>`, `AN7581>`, `AN7583>`, `U-Boot>`). It then sends `echo __URSIDO_<random>__RC_$?` to get
the return code. **Non-zero code → stop.** A Linux kernel start seen in a command's output also
stops it.

### Loading a file into RAM over TFTP

Used by items 1–4 and expert 3–4. The router's Ethernet (LAN) port is cabled to the PC. Resilience
details: [GUIDE_EN.md](GUIDE_EN.md#lan-tftp).

1. **Route-aware PC IP.** The program takes the address of the interface the OS uses to reach
   `192.168.1.1`; fallback: any active interface with `192.168.1.x`. If none, it asks you to set
   Ethernet statically (`192.168.1.254/24` recommended) and type the address, and checks that it can
   bind to it.
2. For every attempt (at most **3**):
   1. U-Boot gets `ethaddr=02:00:00:04:0d:10`, `eth1addr=02:00:00:04:0d:11` (temporary locally
      administered MACs, in-memory environment only), `ipaddr 192.168.1.1`, `serverip <PC IP>`,
      `netmask 255.255.255.0`, `tftpdstp 1069`, `autoload no` again;
   2. **the built-in TFTP server** starts on `<PC IP>:1069/UDP`: it serves exactly one file under
      one expected name, only to `192.168.1.1`, and every wait it makes is bounded;
   3. `tftpboot 0x90000000 <name>`; the byte count must equal the file size;
   4. **RAM check:** `hash sha256` if U-Boot has it, otherwise `crc32`.
3. On failure the server stops and releases the port, U-Boot is **resynchronised** (Ctrl-C until a
   stable prompt), there is a 1–2 s backoff, and **only this transfer** is repeated. After 3
   failures: stop, "TFTP failed after 3 attempts".

`ping` is no longer run: the transfer plus the RAM check is the proof that the network works. One
transfer is at most `0x08000000` (128 MiB). Large images (stock, physical) are split into 8 MiB
chunks; only the current chunk is retried, and chunks already written to NAND are never rewritten.

### Readback after write

After every `mtd write` the RAM area is zeroed (`mw.b`), `mtd read` reads the written data back and
its `crc32` is compared with the CRC32 of the source chunk on the PC. Mismatch → stop. For UBI
volumes: `ubi read` into `0x98000000` and `crc32`.

### Confirmation phrases

Before the first write the program shows exactly what will happen and requires a phrase typed
**exactly**, case-sensitive. The phrases are the same in both languages:

| phrase | item |
|---|---|
| `RESTORE STOCK BACKUP` | 1. Stock restore |
| `WRITE FIP` | 2. FIP replacement |
| `RESTORE PHYSICAL NAND` | 3. Physical NAND |
| `BOOT RAM ITB` | 4. Boot ITB from RAM |
| `WRITE UBI VOLUME <name>` | Expert 3 |
| `WRITE RAW BL2 0x<offset>` / `WRITE RAW UBI 0x<offset>` | Expert 4 |
| `UBI ATTACH` | Porting → A |

Anything else means "operation cancelled by the user".

---

## Main menu

```
Main menu
  1. Restore stock Nokia firmware from an mtd16/all_flash backup
  2. Repair OpenWrt boot / replace the FIP
  3. Restore a full physical NAND image (256 MiB)
  4. Boot an OpenWrt recovery ITB from RAM
  5. NAND / UBI / U-Boot diagnostics
  6. Build a log bundle for a report
  7. PORTING / HARDWARE DISCOVERY (read-only probe of new Airoha devices)
  8. Expert mode
  0. Exit
```

### 1. Restore stock Nokia firmware from an mtd16/all_flash backup

**Why:** return the router to stock Nokia firmware (after a failed OpenWrt install, or to hand it
back to the ISP) from a backup taken by MedveFlasher or by hand.

**Needs:** an `mtd16` backup (the "stock restore span", the first `0x0EBA0000` bytes of NAND: BL2 +
the whole stock area), UART, an Ethernet cable.

**Steps:**

1. **Source.** A path to a *MedveFlasher backup directory* or to a *file*. In a directory the
   program looks for `mtd16.bin.gz`, `mtd16_*.bin.gz`, `mtd16.gz`, `mtd16.bin`, `mtd16_*.bin` (first
   alphabetically) and prints how many of `mtd0…mtd16` are present. If `SHA256SUMS`/`SHA256SUMS.txt`
   has an entry for the chosen file, the hash is checked; mismatch → stop.
2. **Split, no writes.** The file (gzip is detected automatically) is read completely and split in
   `work/stock-<time>/`: `stock-bl2.bin` (first `0x20000`) and `stock-ibu-NN.bin` (8 MiB each).
   Checks:
   - the uncompressed size must be **exactly** `0x0EBA0000` bytes (more/less → stop);
   - **BL2 provenance:** if the backup BL2 is a known AN7581/AN7583 OpenWrt preloader (including one
     shifted by `0x800` inside an `FF` container), this is not a stock backup → stop.
   It prints the SHA256 of the whole `mtd16`, the BL2 SHA256 and the chunk count.
3. **Profile and RAM U-Boot** (see the common procedure).
4. **Bad blocks.** `mtd bad bl2`: any bad block in BL2 → stop. `mtd bad ubi`: bad blocks inside the
   stock area are allowed only in the "safe window" of physical addresses `0x052C0000–0x0EB60000`;
   outside it → stop ("automatic stock restore blocked").
5. **Network and a TFTP test before writing.** U-Boot network setup, transfer of the first chunk
   and its RAM CRC check. If this fails, nothing has been erased yet.
6. **Confirmation** `RESTORE STOCK BACKUP`. Warning: the OpenWrt UBI region will be erased
   completely; BL2 is written last.
7. `mtd erase ubi` (up to 20 min). A second `mtd bad ubi`: the map must stay in the safe window and
   must not lose any earlier bad block.
8. **Chunk writes.** For each 8 MiB chunk: TFTP → RAM (+CRC), then writing only the "good" spans
   between bad blocks (`mtd write ubi …`) with a readback per span. If U-Boot reports
   `skipping bad block` / `new bad block` → stop, BL2 untouched.
9. A third `mtd bad ubi`: the map must not change during the write, otherwise stop (BL2 untouched).
10. **BL2 last:** TFTP `stock-bl2.bin` → `mtd erase bl2` → `mtd write bl2` → readback.
11. Result `Stock restore PASS` with the source SHA256. Enter → `reset`, `N` → stay in U-Boot.

**Why BL2 last:** while the old BL2 is in place, a half-written stock area does not stop you from
entering the BootROM again and repeating. A broken BL2 on top of a half-written rest is the worst case.

### 2. Repair OpenWrt boot / replace the FIP

**Why:** OpenWrt in UBI is intact but does not boot because the loader in the `fip` UBI volume
(BL31 + U-Boot) is damaged or wrong.

**Steps:**

1. Profile → RAM U-Boot.
2. `ubi part ubi`: attach the existing UBI. Failure → stop ("FIP-only repair not applicable").
   `ubi info layout` must list a `fip` volume.
3. Source: `1` the built-in RAM FIP of the current profile (recommended for rescue), `2` your own
   `.fip`. File check: 200,000 bytes … 2 MiB and a TF-A FIP ToC header (`01 00 64 AA 78 56 34 12`).
   Path, size and SHA256 are printed.
4. TFTP to `0x90000000` + RAM check.
5. Confirmation `WRITE FIP`. **Only** the existing `fip` volume is overwritten.
6. `ubi write 0x90000000 fip <size>` → `ubi read 0x98000000 fip <size>` → `crc32`.
7. Enter → `reset`, `N` → stay in the RAM U-Boot.

> The built-in RAM FIP is a RECOVERY_SAFE U-Boot: after reset the router stops in U-Boot
> (`bootdelay=-1`) and boots nothing by itself. It is a "safe landing pad" for further repair, not
> a normal OpenWrt boot loader. For normal operation use a FIP from UrsusBoot/UrsusFlasher.

### 3. Restore a full physical NAND image (256 MiB)

**Why:** write a complete raw NAND image (e.g. read with a programmer or from `all_flash`).

**Steps:**

1. Path to the image; the size must be **exactly** `0x10000000` (256 MiB). The SHA256 is printed.
2. Profile → RAM U-Boot.
3. `mtd bad bl2` and `mtd bad ubi`: in 0.2.0-test14 **any** bad block → stop. A raw image carries
   another chip's bad-block layout; writing it over a NAND with bad blocks without understanding
   the format is unsafe.
4. Network; the image is split in `work/physical-<time>/` into `bl2.bin` and 8 MiB `ubi-NN.bin`.
   The first chunk is transferred as a TFTP test.
5. Confirmation `RESTORE PHYSICAL NAND`.
6. `mtd erase ubi`, then per chunk: TFTP → `mtd write ubi` → readback.
7. BL2 last: `mtd erase bl2` → `mtd write bl2` → readback.

### 4. Boot an OpenWrt recovery ITB from RAM

**Why:** run an OpenWrt initramfs (or another FIT recovery image) straight from memory without
touching flash, e.g. to take a backup or sysupgrade from there.

**Steps:**

1. Path to the `.itb`; the FIT magic `D0 0D FE ED` is checked.
2. Profile → RAM U-Boot → TFTP to `0x90000000` + RAM check.
3. `iminfo 0x90000000`: if missing or failing, only a warning (the magic was already checked).
4. Confirmation `BOOT RAM ITB`, then `bootm 0x90000000`.
5. For 60 seconds the UART output is shown and logged, then back to the menu. Ctrl+C on the PC is
   **not** forwarded to the router here. Continue through the terminal (Expert → 1) or the network.

### 5. NAND / UBI / U-Boot diagnostics

**Why:** see the NAND state before a repair or for a report.

1. Profile → RAM U-Boot. Profile and NAND vendor are printed.
2. `mtd bad bl2`, `mtd bad ubi`: bad-block lists.
3. `mtd list`: the MTD layout.
4. The "UBI attach skipped" note: normal diagnostics does **not** send `ubi part`, because an attach
   may change UBI metadata or the fastmap. An intentional attach has its own ADVANCED mode:
   Porting → A or `probe --ubi-attach`.
5. `printenv`: the RAM U-Boot environment.

Diagnostics is fully read-only: no NAND/UBI write, erase or attach (since test14; before that it
ran `ubi part ubi` + `ubi info layout`).

### 6. Build a log bundle for a report

Creates `UrsidoRescue-support-<date-time>.zip` next to the program. It holds every `*.log` and `*.txt`
from `work/` (UART logs of all sessions) plus `VERSION`, `STATUS.md`, `BUILDINFO.json`,
`MANIFEST.json` when present. No port is opened.

> UART logs contain U-Boot/Linux output and may include the MAC and serial number. Review the
> archive before publishing it.

### 7. Porting / hardware discovery

The Porting Collector is **read-only**. Its job is to gather everything about an unknown Airoha
device that a UrsusBoot port and a UrsusFlasher profile need. The full safety rules and collected
data are in [../PROBE.md](../PROBE.md). In short:

- every command passes an **allowlist** (`probe/guard.go`) and is re-checked right before it is
  written; `erase`, `write`, `saveenv`, `env save`, `ubi create/remove/write`, `sf write/update`,
  U-Boot command abbreviations, `;`, `&&`, `|`, `$` and quotes are never sent; `--unsafe` relaxes
  nothing;
- an unknown bootloader gets nothing until a U-Boot or Linux boot banner has been seen;
- MAC/serial/GPON never enter `profile.json`, the report or the draft: only *where* they live.

```
 PORTING / HARDWARE DISCOVERY (read-only)
  1. Probe new Airoha device (full automatic read-only probe)
  2. Collect BootROM profile
  3. Collect U-Boot profile
  4. Collect Linux profile
  5. Collect flash / MTD / UBI map
  6. Collect DTB / device-tree
  7. Collect network / PHY / switch data
  8. Export Ursus porting bundle
  9. View collected profile
  A. ADVANCED: U-Boot UBI attach (NOT read-only)
  N. Start a new probe session (current: …)
  0. Back
```

All collectors write into the **current probe session**, the directory `work/probe-<date-time>/`.
Several items in a row extend one session; `N` starts a new one.

Items 1 and 3–7 first ask: *"Is the device already on and sitting at a U-Boot/Linux prompt (allow
one Ctrl-C)? [y/N]"*. `y` is the `--wake` mode: one Ctrl-C to wake a device that is already running.
Then: "The probe only reads… After starting, power the device on." Timeout: 5 minutes.

| item | collects |
|---|---|
| **1. Full probe** | All layers: BootROM markers, U-Boot, flash/MTD/UBI, DTB, network, GPIO, Linux. Flow: power on → the probe interrupts U-Boot autoboot and collects → asks you to power-cycle the device (do **not** hold Reset) → waits for Linux without interrupting it and collects the Linux part. The bundle is exported automatically at the end. |
| **2. BootROM profile** | Submenu, see below. |
| **3. U-Boot profile** | U-Boot only: `version`, `help`, `bdinfo`, `printenv`, `mtd list`, `mtd bad`, `nand/mmc info`, `dm tree`, `mii`/`mdio`, `gpio status`, control DTB. |
| **4. Linux profile** | Linux only: `uname`, `cpuinfo`, `cmdline`, `meminfo`, `/proc/mtd`, `/sys/class/mtd`, `ubinfo`, `dmesg`, `fw_printenv`, `board.json`, `/sys/firmware/fdt`, `ip link/addr`, `ethtool`, debug gpio. If Linux asks for a login, the probe asks you for user and password (empty user = skip the Linux part). |
| **5. Flash / MTD / UBI** | Partition map and samples: the first 4 KiB of every partition and the last 4 KiB of partitions up to 8 MiB (`mtd dump`), UBI geometry from the EC/VID headers, FIP ToC parsing, SHA256 of boot partitions. Runs in the first reachable environment (U-Boot or Linux). |
| **6. DTB** | Control DTB from U-Boot (`md.b`) or `/sys/firmware/fdt` from Linux; the built-in parser decompiles it to `dt/fdt.dts` (no dtc needed). |
| **7. Network / PHY / switch** | `mii`/`mdio` in U-Boot, `ip`, `ethtool` in Linux. |
| **8. Export** | Analyses the current (or latest) session and packs `ursus-probe-<vendor>-<model>-<soc>-<time>.zip` next to the program. Asks whether to mask MACs/serials in text files (`--redact`: binary files such as DTB and flash samples are then left out and listed in `REDACTED.txt`). |
| **9. View profile** | An `ursus-profile-v1` summary: SoC, model, compatible, RAM, NAND, etc., with confidence; paths to `profile.json` and `ursusboot-porting-report.md`. |
| **A. UBI attach** | ADVANCED, **NOT read-only**. Warns that attaching UBI may change the volume table, write a fastmap or move blocks, and that booting Linux (`ubinfo -a`) is better for a write-free volume list. Requires `UBI ATTACH`, then collects U-Boot + flash with `ubi part`/`ubi read`. The profile will say `strict_read_only: false`. |
| **N. New session** | Creates a new `work/probe-…` directory and makes it current. |

**Submenu "2. BootROM profile":**

```
  1. Observe only (send nothing)
  2. Answer 'Press x' with x and check the XMODEM 'C'
  3. Load UrsidoRescue's RAM U-Boot (Nokia MD/MF only) and collect the U-Boot/flash profile
```

1. Passively records the UART for 3 minutes and marks markers (Press x, CCC, BL2/BL31/BL33, SoC).
2. Answers `x` and checks that the BootROM switched to XMODEM receive (`C`). Loads nothing.
3. For a bricked Nokia: pick MD or MF **explicitly** (Auto is not allowed) → the standard RAM U-Boot
   procedure → a full probe through it. The acquisition log goes to `bootrom/ram-uboot-acquire.log`.

**Bundle contents:** `profile.json` (every fact with `source` and `confidence`; high = two
independent sources agree, disagreements → `conflicts` with a `null` value),
`ursusboot-porting-report.md`, `ursusflasher-device-draft.json` (a draft, not for production),
`hashes.sha256`, `uart.log`, `transcript.jsonl` (every command: time, transport, response, result),
directories `bootrom/ uboot/ linux/ flash/ mtd/ ubi/ dt/ network/ gpio/`, `README.txt`.

A probe ends with a result code (also the command-line exit code):
`0` ok · `1` failure · `2` UART unavailable · `3` BootROM not detected · `4` U-Boot not detected ·
`5` Linux unavailable · `6` profile incomplete · `7` safety violation blocked.

### 8. Expert mode

```
Expert mode
  1. UART terminal (↑/↓ history, manual XMODEM, logging)
  2. Start RAM U-Boot and leave the prompt
  3. Write an existing UBI volume from a file
  4. Write a raw range into MTD bl2/ubi
  5. Diagnostics
  6. UART Shell (transparent passthrough, sends nothing by itself)
  0. Back
```

An error in any expert item returns to the **main** menu.

#### Expert 1. UART terminal

Port choice → log `work/term-<time>.uart.log` → the full terminal (see
[below](#uart-terminal-keys-and-menu)). It sends nothing by itself and holds no passwords.

#### Expert 2. Start RAM U-Boot and leave the prompt

Profile → the standard RAM U-Boot procedure (including the geometry check) → a UART Shell opens on
the same port. From there you work in U-Boot by hand; Ctrl+] or Ctrl+Q returns to the menu. Useful
when you need a command no wizard offers.

#### Expert 3. Write an existing UBI volume from a file

1. Profile → RAM U-Boot → `ubi part ubi` → prints `ubi info layout`.
2. Name of an **existing** volume (only `A–Z a–z 0–9 _ . -`); `ubi check <name>`: no volume → stop.
   No volumes are created and no sizes change.
3. A file up to 128 MiB (`0x08000000`).
   Size and SHA256 are printed.
4. TFTP + RAM check → confirmation `WRITE UBI VOLUME <name>` → `ubi write` → `ubi read 0x98000000` →
   `crc32`.

#### Expert 4. Write a raw range into MTD bl2/ubi

1. Profile → RAM U-Boot.
2. Target `bl2` (limit `0x20000`) or `ubi` (limit `0x0FFE0000`), a hex offset relative to the target
   start, a file up to 128 MiB. The file must not cross the target end.
3. For `bl2` with offset ≠ 0 or size ≠ `0x20000`: a warning that a partial BL2 write can remove the
   BootROM handoff safety.
4. TFTP + check → confirmation `WRITE RAW <BL2|UBI> 0x<offset>` → `mtd write` → readback through
   `0x98000000`.

`mtd write` does **not erase** first: NAND can only be written into erased blocks. If the range is
not erased, run `mtd erase` by hand via Expert 2, or the CRC readback will fail. Unlike item 1, this
item does not check the bad-block map.

#### Expert 5. Diagnostics

Same as [main menu item 5](#5-nand--ubi--u-boot-diagnostics).

#### Expert 6. UART Shell

Port choice → log `work/shell-<time>.uart.log` → a simplified terminal: the same low-latency raw
mode but without the Ctrl+] menu (Ctrl+] and Ctrl+Q exit). No automatic `x`/Enter/Ctrl-C is sent,
handy for simply "watching what the router says".

---

## UART terminal: keys and menu

The terminal (Expert 1) starts in **raw passthrough**: device output is printed verbatim (you can
select and copy it), keystrokes go to the router and arrows use the device's own history (BusyBox,
U-Boot). 115200 8N1. Everything received and sent is logged.

| key | action |
|---|---|
| **Ctrl+]** | local menu (not sent to the router) |
| **Ctrl+Q** | quick exit (not sent to the router) |
| **Ctrl+P** | toggle the local pager: output pauses per window height, Enter shows the next page. The UART keeps being read and logged meanwhile (up to 4 MiB queued). Fullscreen programs (`top`, `vi`, `less`) are detected from ANSI sequences; the pager switches itself off and hands them the screen |
| **Ctrl+C / Ctrl+Z** | forwarded to the router as `0x03` / `0x1A` (interrupt / suspend) |
| characters ≥ 0x80 | blocked locally with a keyboard-layout warning (Cyrillic in a command is almost always a mistake). XMODEM is unaffected |

**Ctrl+] menu:**

| key | action |
|---|---|
| `l` | toggle raw / **line** mode. In line mode the line is edited on the PC (←/→, Home/End, Backspace, ↑/↓ local history) and sent on Enter, for devices without their own line editor |
| `s` | XMODEM send: asks for a path, asks you to start the receiver on the device (`loadx`/`loady` in U-Boot) and press Enter |
| `r` | XMODEM receive to a file (XMODEM-CRC, 128/1K blocks): asks for a path (overwrite needs confirmation) and prints size and SHA256 at the end |
| `g` | show the log path |
| `q` | leave the terminal |
| Enter | back to the terminal |

On Windows the console is read with `ReadConsoleInputW`: Enter, arrows, Home/End/Delete and Ctrl
combinations are translated explicitly; QuickEdit stays on, so mouse selection and paste work.
