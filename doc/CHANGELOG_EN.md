# UrsidoRescue changelog (complete, reconstructed)

[Русская версия](CHANGELOG_RU.md) · [Contents](README.md)

This file is reconstructed from the very beginning of the project from these sources:

- this repository's git history (every commit, its message and diff);
- the `v0.2.0-test*` tags and the GitHub pre-releases;
- the `STATUS.md` section of every version;
- the source code at every version (menu numbers, limits, behaviour);
- sibling Ursus repositories (MedveFlasher, UrsusBoot), for the prehistory.

What happened before this repository's first commit is known only from `STATUS.md` and the
0.2.0-test2 code; such sections are marked **(reconstructed)**.

**Status labels:**

- **simulation PASS**: tests and simulation passed;
- **simulation candidate**: a candidate that has not completed a full run yet;
- **HW PARTIAL**: partly verified on hardware;
- **HW HOLD / PENDING**: not verified on hardware.

Builds and CI are not hardware validation.

Times are UTC on 2026-09-24 unless stated otherwise. "Pre-release" means published to GitHub
Releases by CI.

| version | commit | pre-release | headline |
|---|---|---|---|
| 0.1.x | — (before the repo) | — | UART recovery for Nokia MD/MF |
| 0.2.0-test1 | — (before the repo) | — | Porting Collector |
| 0.2.0-test2 | `0a30362` | no | first commit, language choice |
| 0.2.0-test3 | `0722824` | 08:07 | probe review, CI |
| 0.2.0-test4 | `f96b654` | 08:20 | UART terminal |
| 0.2.0-test5 | `911c101` | 08:33 | raw passthrough, new menu order |
| 0.2.0-test6 | `ce5a30c` | 08:55 | Windows COM: latency, port list |
| 0.2.0-test7 | `7228235` | 09:24 | UART Shell on the new engine, pager |
| 0.2.0-test8 | `5c90240` | 09:52 | Windows input via ReadConsoleInputW |
| 0.2.0-test9 | `9909608` | 10:11 | pager and TUI, Ctrl+C/Ctrl+Z |
| 0.2.0-test10 | `1865cb1` | 10:27 | .exe icon |
| 0.2.0-test11 | `260419f` | 10:36 | U-Boot prompt after the ANSI bootmenu |
| 0.2.0-test12 | `0b07944` | 11:03 | XMODEM and LAN/TFTP hardening |
| 0.2.0-test13 | `03336da`, `d799a8f` | no (no tag) | AN7583 EOT handoff |
| 0.2.0-test14 | `28f14c4` | yes | read-only diagnostics, 128 MiB limit, COM in the CLI |
| 0.2.0-test15 | `115c87c` | yes | network once per session, LAN prerequisites, colour, EOT |
| 0.2.0-test16 | `8f72817` | yes | stock LAN assist: UART login and UID 0 |
| 0.2.0-test17 | `6008769` | yes | Telnet-only passive plan, late credential refresh, auth hardening |

---

## Unreleased

- Added `doc/` with Russian and English documentation: about the project and the Ursus family,
  operator guide, every menu in detail, architecture, and this complete changelog.
- Added `README.ru.md`, the Russian version of the main README, linked from the English `README.md`.
- Redesigned the `README.md` / `README.ru.md` front pages: a header with the bear and badges,
  purpose and capabilities, principles, quick start, documentation links. The payload provenance
  table moved to ARCHITECTURE.
- `PROBE.md`: the Porting Collector main-menu number is now 7. Number 8 has been stale since test5.
- Approved the unified UI specification v3 for Web-GUI / TUI / CLI (`doc/UI_SPEC_RU.md`, Russian)
  and added an HTML mockup of the Web-GUI (`doc/ui-mockup/`).
- **Spec stage 1: application layer** (the console behaves as before):
  - package `app`: the Event / Progress / Ask / Confirm / Output / Artifact contract, risk classes,
    confirmation by class (a phrase is mandatory for WRITE / ERASE / UBI_METADATA);
  - the core reports and asks only through the UI; the console is one implementation; a `go/ast`
    guard test;
  - operation catalogue and `RunOperation`: every operation runs in its own session
    `work/sessions/<date>-<time>-<kind>-<hex>/` (`session.json`, `session.log`, `uart.log`,
    `operations.jsonl` with every U-Boot command, `errors.log`), operation IDs on every event;
  - the port belongs to `PortOwner` and is leased to one operation at a time;
  - UART logs and image chunks moved into the session directory; the log bundle and `export` still
    find older files in `work/`.
- **STOP in the core:** "now / at a checkpoint / unavailable" modes (`CancelState`) for every
  operation phase; the XMODEM data phase is aborted with `CAN CAN CAN`, and after the last ACK
  stopping is unavailable until the next stage is proven; in an unavailable phase a request is
  refused, not queued. The confirmation shows the risk class, the ordered actions and the stop boundary.
- In the console Ctrl+C during an operation is STOP (a second Ctrl+C within 3 s forces exit);
  outside an operation and in the UART terminal nothing changed.
- **Spec stage 2: TUI** (`--tui`, Bubble Tea + Lip Gloss): the console's menus; the UART and event log
  always on screen (≈35 %, filter, scroll, wrapping); §13 confirmation dialogs with the risk class,
  actions and stop boundary; port choice and connection; STOP with `s` / Ctrl+C labelled by the core;
  the UART terminal and Shell full screen with a return to the TUI; the 80×24 layout is covered by tests.
  Without `--tui` the old console menu starts.
- TUI after first feedback: screen zones are separated (top and bottom bars, titled rules, a log bar, a
  gutter on log lines); every menu item has a description (what it does, what it needs, its risk); yes/no
  questions and short options (`bl2/ubi`, reset / stay) are buttons, lists use highlighted arrows with the
  `[1]` default preselected; hotkeys work with the Russian layout and through F2/F3/F4/F10; long lines wrap
  at words. `AskRequest` gained `Quick` and `Default` for this; the console does not show them.
- TUI, after the branch review:
  - the port chooser (`p`) with no detected ports is no longer empty: it says no ports were found and
    accepts a typed name, like the console;
  - a probe with a non-zero code (no UART, no BootROM/U-Boot/Linux, an incomplete profile) is no longer a
    green "Done": the result says INCOMPLETE with the code and reason, and the session is recorded as
    failed; the console output is unchanged;
  - a long confirmation is never cut: the answer line (phrase and input, or buttons) is pinned at the
    bottom and the text above scrolls with ↑/↓ and a "lines N–M of K" indicator.
- **The TUI is the default.** Without flags the TUI opens; the old text menu is `--console` and the
  automatic fallback when the TUI cannot run (not a terminal, `TERM=dumb`, an error). The language follows
  the Windows display language (the locale on Linux); `l` switches it in the TUI.
- TUI, after feedback from Windows: log events are coloured like the console and UrsusFlasher (stage tags
  amber, PASS green, errors bordeaux), router output in dark lime; the running log shows no times (they
  stay in the log files), the operation panel shows them as a faint column so text does not jump; the top
  bar no longer shows "op …xxxx", which looked like a stray checksum.
- PC address for TFTP: any 192.168.1.2–.254 fits, not only .254. Without one the program waits up to 20 s
  for a link (Windows hides a static address on a NIC without link until the router brings its port up),
  then says what to set and offers Search again / Type an address / Cancel; a typed address is checked
  (subnet, not .1, on a NIC of this PC) and a wrong one asks again instead of ending the operation.
- TUI: the overall progress of stock and full-NAND writes ("chunk N of M, then BL2") as a wide line above
  the current transfer; the console skips it, as it prints every chunk as its own line.
- TUI: statuses stand out in the log: success bold bright green with ✓, warning with !, error bold bordeaux
  with ✗; program steps light, notes and router output dark lime. The top bar is filled up to STOP.
- Busy COM port: the dialog says which port is busy, what may hold it and what to do, with Retry / Another
  port / Cancel buttons (Cancel was wrongly labelled No); the console letters `(r)` and `[1]` are not shown in
  the TUI, which has buttons and a list instead.
- A COM port held by another program is not an error: "held by another program, close it" with retry /
  another port / cancel (console and TUI). On Linux a busy port is detected through `flock`.
- TUI and menu texts, after the second review:
  - after an incomplete probe the result states what actually happened ("no flash write commands were
    sent", "ubi part was run: UBI may have changed its metadata", "FTP was enabled on the stock firmware",
    "the RAM U-Boot was loaded into RAM only"), from the real outcome rather than one sentence for all;
  - "NAND / UBI / U-Boot diagnostics" is now "NAND / MTD / U-Boot diagnostics" (console, TUI, MENU): it does
    not attach UBI or list volumes, and the description says where to go for volumes;
  - main menu item 2 is "Restore the FIP if UBI is intact" (was "Repair OpenWrt boot / replace the FIP");
  - the full probe description: writes no flash, enabling FTP is asked separately;
  - the TUI has no duplicate Diagnostics under Expert (console item numbers are unchanged);
  - the result screen shows the session and operation IDs, the full log path is in the log;
  - at 80×24 the item description always fits: while idle the log shrinks to 3 rows and the bear only
    takes free space.
- TUI: a blank row sets the tabs apart from the list; the navigation hints moved under the menu list (when
  they fit; the bottom help bar always has the same keys); the help bar is dark lime like the log frame.
- TUI: the logo is a teddy-bear head with a wink and a smile (big 8 rows, small 5), captioned "Ursus family ·
  Bearborn utility"; on a big screen menu items are spaced, bold, with their hint below; the log bar and
  plain log text are dark lime, only statuses are coloured.
- Restore stock: instead of "MedveFlasher backup" it says which backups are accepted: an mtd16 or all_flash
  file (.bin or .bin.gz) or a directory with mtd16.bin(.gz); the path question and the found-files message
  say the same.
- TUI: the logo is a sitting, winking teddy bear in half-blocks `▀▄█` (after an ASCII-art picture): ears,
  an open and a winking eye, nose, arms, feet with pads; big (12 rows) and small (6 rows) for 80×24.
- TUI: the logo is a winking ASCII bear with the UrsidoRescue name and version: big in the top-left
  corner when the height allows, otherwise small in a free corner of the menu; it never pushes the menu or
  the description off the screen.
- Porting Collector: a `Username:` / `User:` prompt is recognised as a login (previously only `login:`);
  when the last line is not a known prompt the probe says so instead of waiting silently for the timeout.
- The Porting menu items report through the application layer; the console prints the same as before,
  and the literal `\n` in the UBI attach text is now a line break.
- Go 1.24 (required by the Charm modules); `probe`: `Info` calls no longer use a non-constant format
  (a `go vet` requirement).
- Stage 1 (application layer, STOP, Ctrl+C) passed the hardware check in `doc/HW_SMOKE_STAGE1_RU.md`:
  **HW smoke PASS**.
- UI spec v3.1 and mockup: STOP states, port chooser, IDs on screen, Porting banners, BootROM wait
  and result screens, compact layout, no external fonts.
- UI spec v3.2: a backend layer and the degradation ladder (persistent UrsusBoot Web/UART → RAM
  RECOVERY_SAFE → BootROM), with a read-only "what is on the other end" detector as its own stage. ABOUT and
  ARCHITECTURE now state that the RAM FIP is a vanilla OpenWrt U-Boot under the RC18 contract, built by the
  UrsusBoot pipeline, with no UrsusBoot code.
- Documentation and `PROBE.md` updated to test17: network and LAN prerequisites, coloured output,
  EOT, stock LAN assist, `--stock-lan-assist`, the Telnet-only passive plan and late credential refresh.
- **Expert 7 / TUI Expert → Install UrsusBoot (UART)**: a persistent UrsusBoot 0.1.0-alpha5-t67
  install on MD and MF over the UART and the RAM U-Boot only, for the stock and the UBI layout.
  The candidate is built from the device's current content: it is read over the UART (`md.l` in
  64 KiB pieces, each checked against the device's `crc32`; the RAM U-Boot has no `tftpput`) and
  kept in the session as a backup. MD: the pinned t67 update.fip at `0x800` of the live boot area;
  MF: only BL33 replaced in the live FIP (a port of UrsusFlasher 0.2.67). The BootROM prefix and
  the stock env stay byte-exact; only changed blocks are written, BL2 last; on UBI only the
  (static) `fip` volume. Typed phrase `INSTALL URSUSBOOT`. The new pinned payloads and their
  provenance are in `payloads/`; `--selftest` checks them too. The fip-volume write of Restore FIP
  moved into a shared helper (same commands).

- TUI, after a reviewer's analysis: the UART consoles no longer drop into a black screen. The raw
  backend (`tea.Exec`, `consoleRaw`, XMODEM, the fullscreen-ANSI detector) is unchanged; the new
  `term_chrome.go` draws an UrsidoRescue header and key bar around it through an ANSI scroll region,
  shows an entry plate, gives the screen to `top`/`vi`/a bootmenu and comes back after them. The
  consoles' port is chosen in the TUI dialog first. `--console` prints as before.
- Names: "UART Shell" → "Transparent UART console", "UART terminal" → "UART terminal + XMODEM" (both
  front ends). The TUI Expert tab is ordered by risk, and the risk (`MANUAL` / `RAM only` / `WRITE` /
  `RAW WRITE` / `ERASE`) shows under each item. The help bar shows the F-keys; during an operation
  the top bar shows its name and phase (`READ 37%`, `2/6`).
- UrsusBoot install: overall progress in 6 steps (RAM U-Boot and layout → read → checks and build →
  TFTP → write → final readback); before the read it says what is read and that nothing was written
  yet; on stock, after the block writes the whole 0x80000 area is read back and CRC32-checked.
- The small bear's caption no longer shifts the divider at 80 columns.
---

## 0.2.0-test17 (2026-09-24 12:39–12:49)

Commits included in the release:
- `3514fc7`: stock UART assist fixes;
- `ec97bbe`: README front-page redesign;
- `98edd7c`: test17 documentation;
- `6008769`: release commit with the same verified tree.

Tag `v0.2.0-test17` → `6008769`, pre-release. CI run #40 checked the code candidate, run #42
checked the final tree with documentation, and run #43 on the exact release SHA fully PASSed:
format, vet and tests, Windows/Linux builds, selftest, packaging and publishing.

Status: **simulation candidate / HW PARTIAL**. The test17 stock UART login still needs the next
real XG-040G-MF run; CI is not hardware validation.

**Fixes after the test16 review:**
- Passive stock LAN assist no longer **requires FTP credentials** when FTP is off. Current Telnet
  credentials are enough, so the flow does not wait 90 seconds only to fail and the "enable FTP?"
  question remains reachable.
- After the first failed UID-0 attempt the probe keeps **draining UART for another 12 seconds**,
  re-reads Web credentials once, and retries the login/`su`. This covers the window where the Web
  UI is already up but stock init is still rotating service passwords.
- If a non-root Telnet shell is already open at refresh time, the probe first retries `su` with
  the refreshed UID-0 credentials; after failure it returns to `Login:` and retries the full plan.
- UID 0 still counts **only after `id -u` returns `0`**.
- Passwords remain memory-only. Auth transcript entries now store the prompt kind rather than a raw
  trailing UART line.
- Stock-auth shell transitions no longer use the generic raw-key path for command lines: the narrow
  validator permits only `exit` and `su <validated-account>`.
- Tests cover the Telnet-only plan, the FTP-credential requirement after provisioning, `id -u`,
  and the auth-line grammar.
- The Porting Collector wording no longer promises absolute "read-only": flash/MTD commands remain
  read-only, while enabling stock FTP after a separate `y/N` is explicitly a stock-settings change.

---

## 0.2.0-test16 (2026-09-24 12:07–12:10)

Commits:
- `4caad37`: release;
- `f9b953f`: CI gofmt diff output;
- `3311d81`: finalisation;
- `8f72817`: probe build fix.

Tag `v0.2.0-test16` → `8f72817`, pre-release. CI run #38 on this commit fully PASSed: format, vet
and tests, Windows/Linux builds, selftest, packaging, publishing.

Status: **simulation candidate / HW PARTIAL**.

**Why:** a real MF stock boot under test15 reached the serial `Login:` prompt, but the probe could
only ask for `--linux-user`/`--linux-password`, and an empty answer skipped the Linux part. The same
log shows:
- `user_ftp` on MF is UID 0, while `user-telnet` is an ordinary user (UID 1002);
- stock init changes the `user_ftp`, `user-telnet` and `root` passwords late in boot.

**Stock LAN assist for the UART login (Nokia XG-040G-MD/MF):**
- At a stock `Login:` the interactive probe does not ask for a login right away. For up to 90 s it
  waits for the stock Web UI on `192.168.1.1`, reading the UART all the time.
- The mechanism comes from the UrsusFlasher family:
  - encrypted stock Web login;
  - model check, XG-040G-MD/MF only;
  - reading the current `TelnetUserName/TelnetPassword` and `FtpUserName/FtpPassword`
    (`storage.cgi?ftp_config`).
- **Credentials live in memory only.** They are never printed or written to the bundle,
  transcript, UART log or command-line arguments.
- **UART login order:**
  1. directly as the UID-0 service account (FTP, `user_ftp` on MF);
  2. if the serial getty refuses it: log in as the Telnet account and `su` to the UID-0 account.
- **UID 0 is proven only by `id -u`** returning `0`. The probe's Linux allowlist permits exactly
  `id -u`.
- **If UID 0 is not obtained and stock FTP is off**, one separate `y/N` question offers to enable
  FTP through the stock Web UI.
  - It is labelled plainly as a stock-settings change, not "read-only".
  - No raw MTD or firmware is written.
  - Credentials are re-read, then the login and `su` are retried.
  - FTP stays enabled after the probe.
- If a login succeeds without UID 0, the read-only Linux diagnostics continue with the current
  privileges.
- **CLI:** the new `--stock-lan-assist` flag only fetches credentials passively. The command line
  never enables FTP, since it has no interactive confirmation.
- The probe result gained `linux_uid0`, `stock_lan_assist` and `stock_service_provisioned`.
- `--selftest` checks that the guard admits `id -u`.
- **Tests:** `TestStockEncodeURL`, `TestStockJSField`, `TestStockPKCS7`.

## 0.2.0-test15 (2026-09-24 11:43)

Commit `115c87c`, tag `v0.2.0-test15`, pre-release.

Status: **simulation candidate / HW PARTIAL**.

**Why:** test14 on a real MF reached a stable RAM U-Boot, passed the geometry and bad-block checks,
and transferred and verified several 8 MiB stock chunks. The log also showed that the U-Boot network
variables were redundantly re-applied before every chunk.

- **U-Boot network is configured once per session**, as in UrsusFlasher/MedveFlasher. Later chunks
  reuse the same `ethaddr/ipaddr/serverip/netmask/tftpdstp`.
  - The network is re-applied only after a real TFTP/network failure and prompt resync.
  - A RAM-verification retry does not touch the network.
- **A "Network prerequisites" block** before every LAN/TFTP wizard (items 1–4, expert 3–4):
  - a direct cable; LAN2 or LAN3 (LAN1 not recommended, avoid LAN4);
  - Nokia and PC IPs, DHCP off on the recovery adapter;
  - the built-in server on UDP/1069;
  - a request to disable Wi-Fi, VPN, other Ethernet, virtual adapters and tunnels;
  - the PC's active IPv4 interfaces, listed for information only, non-blocking.
- **Coloured console output** in the UrsusBoot/UrsusFlasher palette (brown, amber, sand, green,
  red), only on an interactive terminal. `NO_COLOR`, `TERM=dumb` and redirected output stay plain.
  UART bytes and UART logs are never coloured.
- **XMODEM EOT no longer retries on silence** after fully ACKed data:
  - the EOT reply wait is 0.9 s;
  - EOT is retried only on an explicit NAK, at most 3 times;
  - otherwise control passes straight to the next-stage proof.
- **Tests:** `TestEventTone`.

## 0.2.0-test14 (2026-09-24 11:31)

Commit `28f14c4`, tag `v0.2.0-test14`, pre-release. CI run #32 on this commit fully PASSed,
Windows build and publishing included.

Status: **simulation candidate / HW PARTIAL**. A code-only release: it closes three mismatches
between the code and the documentation found while writing it. No files under `doc/` changed in
this release.

- **Diagnostics is now fully read-only** (main menu 5, expert 5):
  - `ubi part ubi` and `ubi info layout` are no longer run;
  - `mtd bad bl2`, `mtd bad ubi`, `mtd list` (new) and `printenv` remain;
  - the output says plainly that the UBI attach was skipped and points to the separate ADVANCED
    mode: Porting → A or `--ubi-attach`;
  - it ends with "Read-only diagnostics finished. No NAND/UBI write, erase, or attach was
    performed".
- **One-transfer limit in messages** of expert 3 and 4 is derived from `maxGenericRAMFile`:
  `0x08000000` is always shown as 128 MiB (previously a wrong "64 MiB"). The version in the message
  comes from `appVersion`.
- **CLI `probe` finds ports the same way as the menu** on every platform; on Windows via
  `QueryDosDeviceW`:
  - 0 ports: a clear error;
  - 1 port: selected automatically;
  - several: a numbered list of the real COM ports and a request for `--uart PORT`, exit code 2.
- **Tests:** `TestGenericRAMFileLimitIs128MiB`.

## 0.2.0-test13 (2026-09-24 11:18–11:19)

Commits: `03336da` (release), `d799a8f` (manual XMODEM in the terminal). No `v0.2.0-test13` tag and
no `[publish prerelease]` marker, so it is not published on GitHub.

Status: **simulation candidate / HW PARTIAL**.

**Why:** two consecutive test12 runs on a real Nokia XG-040G-MF (AN7583) went like this:
- all 2539 FIP data blocks were ACKed;
- no EOT ACK ever arrived;
- test12 retried EOT six times and aborted, although the next stage had already started.

**Fixed: XMODEM EOT handoff**

- Once **every data block is ACKed**, a missing EOT ACK is no longer fatal by itself. The next stage
  proves success:
  - after the preloader: the second BootROM XMODEM receiver (`CCC`) appears;
  - after the FIP: a stable RAM U-Boot prompt appears.
  If the stage does not appear, the normal wait timeouts apply and the operation stops. On success
  the log says "EOT ACK was absent, but a stable RAM U-Boot prompt proved the handoff".
- EOT replies are read according to the stage:
  - ACK is definitive;
  - NAK requests another EOT;
  - ASCII `C` or any boot text is next-stage output, and no more EOT is sent. The `C` in words
    like `NOTICE` is no longer taken for a CRC request.
- EOT is retried at most **3 times** with 1.5 s waits, and only while there is no next-stage output.
- `CAN CAN CAN` is sent only when the **data phase** failed. It is never sent after the final block
  was ACKed, because the peer may already be running the transferred image.
- In block replies, ACK wins over `C`/NAK noise in the same UART read.
- UART bytes received during EOT go straight to the next-stage parser. A U-Boot banner and prompt
  from the same read are not lost, and autoboot can still be interrupted.

**Terminal (`d799a8f`):**
- The manual XMODEM send (Ctrl+] → `s`) prints the output received after the transfer.
- Without an EOT ACK it says "All data blocks were ACKed; EOT ACK was not received. Verify the
  device state in the terminal" and does not treat this as a failure.

**Tests:** `TestXmodemEOTHandoffClassifier`.

## 0.2.0-test12 (2026-09-24 10:55–11:02)

Commits:
- `5e0ec4a`: release;
- `6355c79`: gofmt;
- `74ef552`: CI gofmt diff output for diagnosis, reverted afterwards;
- `2d72182`: finalisation;
- `e3a4df8`: restores wizard helpers accidentally removed while replacing the TFTP block;
- `0b07944`: restores route-aware IP selection.

Tag `v0.2.0-test12` → `0b07944`, pre-release 11:03.

Status: **simulation PASS / HW PARTIAL**.

**Why:** with test11 on a real XG-040G-MF, the second BootROM XMODEM stage had transferred 1152 of
2539 FIP blocks when a single received `CAN` byte made the host abort the whole session. No NAND
erase or write had started.

**XMODEM:**
- A single `CAN` is **no longer** a cancellation; it is line noise. A receiver abort requires
  **`CAN CAN`** (two in a row).
- NAK or `C` (CRC request) retries only the current block immediately, without waiting out the old
  12-second deadline.
- The block ACK wait is 2 s with at most **8 attempts** (was 12 s and 10 attempts).
- EOT: 6 attempts of 2 s each (replaced by the handoff logic in test13).
- A final host-side failure sends `CAN CAN CAN`, so the peer is not left in an ambiguous session
  (test13 narrows this to the data phase).

**LAN/TFTP (UrsusFlasher-style):**
- **Route-aware PC IP.** The program takes the address of the interface the OS routes to
  `192.168.1.1`. Fallback: any active non-loopback interface with a `192.168.1.x` address.
- **3 attempts** of only the current transfer. Every attempt re-applies all U-Boot network
  variables.
- **U-Boot resync** after a network error: Ctrl-C and wait for the prompt, up to 4 cycles of 1.5 s.
  Backoff between attempts: 1 s, then 2 s.
- **Mandatory RAM verification** (`hash sha256` or `crc32`) after every successful transfer,
  retries included.
- ICMP `ping` is no longer a hard preflight. The TFTP transfer plus the RAM check is the proof that
  the Ethernet path works.
- **TFTP server:**
  - it can be cancelled; a failed attempt releases UDP/1069 before the next one;
  - every wait is bounded: RRQ 30 s, option negotiation 8 × 1 s, block ACK 10 × 1 s.
- **Windows:** `WSAECONNRESET`/`WSAECONNABORTED` UDP errors from a stale peer are treated as noise
  (`udp_windows.go`, `udp_other.go`).
- **Stock restore:** only the current 8 MiB RAM upload is retried. Chunks already written to NAND
  are never replayed by the transport retry. The BL2-last rule is unchanged.
- The temporary MACs `02:00:00:04:0d:10/11` are now set before every TFTP transfer in all wizards,
  not only in stock and physical restore.

**Tests:** `TestXmodemReplyNoiseAndCancel`.

## 0.2.0-test11 (2026-09-24 10:35)

Commit `260419f`, tag `v0.2.0-test11`, pre-release 10:36.

Status: **simulation PASS / HW PARTIAL**.

**Why:** on a real XG-040G-MF (AN7583), test10 completed the BootROM stage for the first time:
preloader XMODEM and the RAM FIP (BL31 + U-Boot) load. The wizard then reached a live `AN7583>`
prompt but did not recognise it after the ANSI bootmenu. No NAND erase or write happened.

- **U-Boot prompt detection:**
  - ANSI CSI sequences are stripped from the output;
  - the prompt is recognised by the stable end of the stream: `AN7581>`, `AN7583>`, `U-Boot>` or
    `=>`;
  - a newline before the prompt is no longer required, since the bootmenu positions the cursor with
    ANSI instead of CR/LF.
- **Leaving the bootmenu:** once a bootmenu is seen, only Esc is sent, at most 6 times, 250 ms apart.
  No more Ctrl-C into the menu (previously Ctrl-C and Esc together, up to 40 times). Without a menu:
  Ctrl-C at most 20 times.
- **Regression tests:** AN7583 bootmenu cursor addressing before and after the prompt; prompt-like
  text that is not at the end of the stream is rejected.
- **First hardware observation:** the RAM U-Boot load works on MF.

## 0.2.0-test10 (2026-09-24 10:25)

Commits `f4dfd56` and `1865cb1` (restores the `build.sh` executable bit). Tag `v0.2.0-test10` →
`1865cb1`, pre-release 10:27.

Status: **simulation PASS / HW PARTIAL**. UART and recovery hardware status as in test9.

- `UrsidoRescue.exe` carries the Ursus bear icon as Windows `RT_ICON` and `RT_GROUP_ICON` resources
  in 7 sizes (16, 24, 32, 48, 64, 128, 256 px).
- Icon sources: `assets/ursus-bear.svg` and `assets/ursus-bear.png` (256 px), kept for the planned
  GUI.
- The icon is embedded by the in-house pure-Go tool `tools/embedicon`. The build needs no MinGW,
  windres, Python or ImageMagick.

## 0.2.0-test9 (2026-09-24 10:03–10:10)

Commits `a76836d`, `fd3a01f` (gofmt), `9909608` (Ctrl+C/Ctrl+Z). Tag `v0.2.0-test9` → `9909608`,
pre-release 10:11.

Status: **simulation PASS / HW PARTIAL**.

**Why:** on Windows/OpenWrt, test8 confirmed normal input and live output, but the pager split
fullscreen programs such as BusyBox `top`.

- **Ctrl+P pager** applies only to ordinary line-oriented output.
- **Fullscreen output** is detected from cursor-home, clear-screen and alternate-screen ANSI
  sequences:
  - the pager flushes its queue at once and switches itself off;
  - detection works even when a CSI sequence is split across two reads;
  - colour (SGR) and erase-line sequences do not trigger TUI mode.
- **Ctrl+C and Ctrl+Z** are forwarded to the router as `0x03` and `0x1A` in both input modes,
  Windows included.

## 0.2.0-test8 (2026-09-24 09:29–09:51)

Commits `e95e2b8`, `8e49aa6` (gofmt), `5c90240`. Tag `v0.2.0-test8`, pre-release 09:52.

Status: **simulation PASS / HW PARTIAL**.

**Why:** in test7 on Windows, Ctrl+P and Ctrl+Q worked but ordinary keys and Enter could be lost.

- **Windows input** uses `ReadConsoleInputW` + `KEY_EVENT_RECORD` instead of byte reads under
  `VIRTUAL_TERMINAL_INPUT`:
  - Enter, Backspace, ASCII, arrows and Home/End/Delete are translated explicitly;
  - an arrow goes out as one complete ANSI sequence;
  - Ctrl+Q, Ctrl+P, Ctrl+C, Ctrl+D and Ctrl+] are translated locally.
- Non-Latin input is reliably rejected by the ASCII gate.
- `VIRTUAL_TERMINAL_INPUT` is off; QuickEdit is kept.

## 0.2.0-test7 (2026-09-24 09:09–09:23)

Commits `d54ec3f`, `23810f0` (gofmt), `7228235`. Tag `v0.2.0-test7`, pre-release 09:24.

Status: **simulation PASS / HW PARTIAL**.

**Why:** the old UART Shell (Expert 6) read the console byte by byte and polled the port every
250 ms. Arrows were split and input lagged by almost a second.

- **Expert 6** runs on the same low-latency engine as Expert 1.
- Port polling is 20 ms (was 200/250 ms).
- Input is sent in chunks.
- Ctrl+Q and Ctrl+] exit.
- **ASCII gate:** bytes ≥ 0x80 are blocked with a keyboard-layout warning.
- **Local Ctrl+P pager** (queue up to 4 MiB).

## 0.2.0-test6 (2026-09-24 08:46–08:54)

Commits `b083d61`, `dac94ac` (gofmt), `ce5a30c`, the first commits by medvedolog. Tag
`v0.2.0-test6`, pre-release 08:55.

Status: **simulation PASS / HW PARTIAL**: the terminal ran on real OpenWrt for the first time.

- **Windows COM port list** shows only ports that exist (`QueryDosDeviceW`), selectable by number.
- No `FlushFileBuffers` after every write, which removed the near-second input lag.
- QuickEdit is kept in raw mode, so copy and paste work.
- Ctrl+Q is a local fast exit.

## 0.2.0-test5 (2026-09-24 08:30)

Commit `911c101`, tag `v0.2.0-test5`, pre-release 08:33.

- **Terminal** defaults to raw passthrough:
  - device output verbatim;
  - the device's own history works;
  - no more `[K` litter from ANSI redraws.
- Line mode (`l`) redraws the line only with CR, space and backspace.
- **Main menu reordered** (the current numbering):

  | item | before test5 | since test5 |
  |---|---|---|
  | Expert mode | 6 | **8, bold** |
  | Log bundle | 7 | 6 |
  | Porting | 8 | 7 |

- Windows: `ENABLE_VIRTUAL_TERMINAL_PROCESSING` is enabled.

## 0.2.0-test4 (2026-09-24 08:18)

Commit `f96b654`, tag `v0.2.0-test4`, pre-release 08:20.

- **New UART terminal, Expert 1:**
  - line input, ↑/↓ history, cursor, Home/End;
  - a raw passthrough toggle;
  - manual XMODEM send and receive: XMODEM-CRC, 128 and 1K blocks;
  - full session log.
- **Expert menu reordered:**

  | item | before test4 | since test4 |
  |---|---|---|
  | UART terminal | — | **1** |
  | UART Shell | 1 | 6 |

## 0.2.0-test3 (2026-09-24 07:55–08:05)

Commits:
- `84c00e3`: CI builds and tests on every push, pre-releases for `v*` tags;
- `0722824`: probe review;
- `6692754`: CI publishing from a manual run on `main`.

Tag `v0.2.0-test3` → `6692754`, pre-release 08:07, the first published one.

Review fixes before the first hardware probe:

- **UBI attach:**
  - `ubi part` removed from the strict probe;
  - UBI geometry is parsed offline from the EC/VID headers;
  - attach is a separate ADVANCED mode (`--ubi-attach`, item A).
- **Identity leak:** `board.json` `macaddr` reached `profile.json`. A recursive sanitizer was added.
- **Unknown devices** stay passive until a bootloader banner is seen. `--wake` allows one Ctrl-C.
- **Internal lines** use the fixed templates in `probe/internal.go`.
- **`--redact`** masks JSON fields and leaves binary files out of the archive.

## 0.2.0-test2 (2026-09-24 07:49)

Commit `0a30362`, the repository's first commit. No pre-release: CI did not exist yet.

- **Language choice at start**: Русский / English.
  - Preset it with `--lang ru|en` or `URSIDO_LANG`.
  - `probe` and `export` follow the system locale.
  - Confirmation phrases, `profile.json` and reports are always English.
- **Contents at the first commit:**
  - main menu: 1 stock, 2 FIP, 3 physical, 4 ITB, 5 diagnostics, 6 expert, 7 log bundle, 8 porting;
  - expert: 1 UART Shell, 2 RAM U-Boot, 3 UBI volume, 4 raw MTD, 5 diagnostics;
  - `payloads/` with pinned SHA256, `--selftest`, `build.sh` for 3 targets.
- **XMODEM at the time:**
  - 10 attempts per block, 12 s wait;
  - a single `CAN` meant cancellation;
  - EOT without ACK was an error.
- **Status:** the probe was tested on a simulated board and a pseudo-terminal, not on hardware.
  Recovery: HW PENDING.

## 0.2.0-test1 (reconstructed)

Before the repository; known from the test2 `STATUS.md`.

- Read-only **Porting Collector**:
  - the "Porting" menu item;
  - the `probe` and `export` commands;
  - `ursus-profile-v1` bundles.

## 0.1.x (reconstructed)

Before the repository; per-version details were not preserved. The 0.2.0-test2 code and `STATUS.md`
show that by 0.1.1-test2 the tool already had:

- UART recovery of the Nokia XG-040G-MD (AN7581) and XG-040G-MF (AN7583) through the BootROM:
  preloader + RAM FIP over XMODEM;
- stock restore from `mtd16`, FIP replacement, physical NAND, ITB boot from RAM, diagnostics,
  expert mode, log bundle;
- a built-in TFTP server, readback after write, "BL2 last".

**0.1.1-test2:** the RAM FIPs of both boards are UrsusBoot 0.1.0-alpha5-t66 RECOVERY_SAFE (Fudan
FM25S01A + FM25G02B). Hardware status: PENDING.

## Prehistory in the Ursus family (reconstructed)

- **MedveFlasher RC18** (no later than 2026-08-14): the RECOVERY_SAFE RAM U-Boot contract:
  - `bootdelay=-1`;
  - `bootcmd` only prints text;
  - the environment is saved only to UBI volumes that never exist.
  These RAM U-Boots knew only the Fudan FM25S01A.
- **UrsusBoot 0.1.0-alpha5-t66** (2026-09-23): a Fudan-capable RECOVERY_SAFE RAM U-Boot on the same
  contract with FM25G01B/FM25G02B support. Its FIPs are what `payloads/` ships.
