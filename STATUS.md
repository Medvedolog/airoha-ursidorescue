# UrsidoRescue 0.2.0-test2

LAB / PUBLIC UART TEST build. UART testers only: 3.3V TTL, GND/TX/RX only, never connect VCC.

New in 0.2.0: read-only "Porting Collector" (menu item 8, `probe` / `export` commands) that
profiles unknown Airoha devices over UART and writes an ursus-profile-v1 porting bundle.
See PROBE.md. The probe has been tested against a simulated board and a pseudo-terminal U-Boot,
NOT yet on real hardware.

Recovery functions are unchanged from 0.1.1-test2: RAM FIPs for both boards are the UrsusBoot
0.1.0-alpha5-t66 RECOVERY_SAFE U-Boots (Fudan FM25S01A + FM25G02B). Hardware status: PENDING.

New in 0.2.0-test2: language choice (Русский / English) at start; all menus, prompts and
operator messages are translated. `--lang ru|en` or `URSIDO_LANG` skips the question; the
`probe`/`export` commands default to the system locale (LANG), English if it is not Russian.
Confirmation phrases (WRITE FIP, RESTORE STOCK BACKUP, ...) are identical in both languages,
and profile.json / reports stay English.
