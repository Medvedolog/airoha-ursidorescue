package main

// tuiItem is one menu entry of the TUI.
type tuiItem struct {
	label, hint string
	// desc says what the item does, what it needs and its risk; the TUI
	// shows it next to the menu (doc/MENU_RU.md has the full story).
	desc    string
	kind    string // catalogue operation, "" for Porting items
	porting string // Porting item key
}

type tuiGroup struct {
	title string
	items []tuiItem
}

// tuiMenu mirrors the console menus (doc/MENU_RU.md), so both front ends
// offer the same operations under the same names.
func tuiMenu() []tuiGroup {
	return []tuiGroup{
		{L("Главное", "Main"), []tuiItem{
			{label: L("Восстановить заводскую Nokia", "Restore stock Nokia"), hint: L("mtd16 / all_flash, .bin или .gz", "mtd16 / all_flash, .bin or .gz"), kind: "stock-restore",
				desc: L("Возвращает заводскую прошивку Nokia XG-040G-MD/MF из бэкапа: файл mtd16 или all_flash (.bin / .bin.gz) или каталог с mtd16.bin(.gz).\n\nКак: BootROM → RAM U-Boot → образ по LAN/TFTP частями со сверкой; BL2 последним.\n\nНужно: UART, LAN2/LAN3, ПК 192.168.1.254/24.\n\nРиск: ERASE — подтверждение фразой.",
					"Returns the stock Nokia XG-040G-MD/MF firmware from a backup: an mtd16 or all_flash file (.bin / .bin.gz) or a directory with mtd16.bin(.gz).\n\nHow: BootROM → RAM U-Boot → the image in verified chunks over LAN/TFTP; BL2 last.\n\nNeeds: UART, LAN2/LAN3, PC at 192.168.1.254/24.\n\nRisk: ERASE; typed phrase.")},
			{label: L("Восстановить FIP (UBI цел)", "Restore the FIP (UBI intact)"), hint: L("только UBI-том fip", "UBI volume fip only"), kind: "fip-repair",
				desc: L("Только когда UBI цел, а испорчен том fip (BL31 + U-Boot): перезаписывает этот том встроенным RAM FIP или вашим .fip и сверяет CRC. Если UBI не подключается, мастер остановится — тогда нужен «Вернуть сток» или «Весь NAND».\n\nНужно: UART и LAN.\n\nРиск: WRITE — подтверждение фразой WRITE FIP.",
					"Only when UBI is intact and the fip volume (BL31 + U-Boot) is damaged: overwrites that volume with the built-in RAM FIP or your .fip and checks the CRC. If UBI does not attach the wizard stops; then use Restore stock or Full NAND.\n\nNeeds: UART and LAN.\n\nRisk: WRITE; typed phrase WRITE FIP.")},
			{label: L("Восстановить весь NAND", "Restore the full NAND"), hint: "physical 256 MiB", kind: "physical-restore",
				desc: L("Пишет полный сырой образ NAND 256 МиБ (снятый программатором или dump): стирает ubi, пишет частями со сверкой, BL2 последним.\n\nНужно: UART, LAN, файл ровно 256 МиБ.\n\nРиск: ERASE — подтверждение фразой.",
					"Writes a full raw 256 MiB NAND image (from a programmer or a dump): erases ubi, writes verified chunks, BL2 last.\n\nNeeds: UART, LAN, a file of exactly 256 MiB.\n\nRisk: ERASE; typed phrase.")},
			{label: L("Загрузить ITB в RAM", "Boot an ITB from RAM"), hint: L("без записи", "no write"), kind: "itb-boot",
				desc: L("Загружает OpenWrt initramfs/recovery .itb в память и запускает его (bootm). Во flash ничего не пишется — после перезагрузки всё как было.\n\nНужно: UART, LAN, файл .itb.",
					"Loads an OpenWrt initramfs/recovery .itb into RAM and starts it (bootm). Nothing is written to flash; a reboot brings everything back.\n\nNeeds: UART, LAN, the .itb file.")},
			{label: L("Диагностика NAND / MTD / U-Boot", "NAND / MTD / U-Boot diagnostics"), hint: L("только чтение", "read-only"), kind: "diagnostics",
				desc: L("Только чтение: через RAM U-Boot показывает разметку MTD (mtd list), bad-блоки bl2 и ubi и окружение U-Boot (printenv). Ничего не пишет.\n\nUBI не подключает (ubi part не отправляется): тома UBI — через «Портирование → ADVANCED: UBI attach».\n\nНужно: UART.",
					"Read-only: through the RAM U-Boot shows the MTD layout (mtd list), the bl2 and ubi bad blocks and the U-Boot environment (printenv). Writes nothing.\n\nDoes not attach UBI (no ubi part): for UBI volumes use Porting → ADVANCED: UBI attach.\n\nNeeds: UART.")},
			{label: L("Собрать пакет логов", "Build a log bundle"), hint: L("для отчёта", "for a report"), kind: "support-bundle",
				desc: L("Собирает zip с логами сессий, UART-логами и сведениями о системе — приложите его к отчёту разработчику. Устройство не нужно.",
					"Builds a zip with session logs, UART logs and system details to attach to a report. No device needed.")},
		}},
		{L("Портирование", "Porting"), []tuiItem{
			{label: L("Полный probe нового устройства", "Full probe of a new device"), hint: "+ porting bundle", porting: "1",
				desc: L("Автоматически: BootROM → U-Boot → Linux. Flash не записывает; включение FTP на stock-прошивке, если понадобится, спросит отдельно. Собирает SoC, NAND, разметку, env, DTB, сеть; создаёт ursus-profile-v1 и porting bundle для UrsusBoot.\n\nНужно: UART; включить устройство после старта, для Linux-части — перезагрузить по просьбе программы.\n\nНезнакомому загрузчику или приглашению probe ничего не отправляет.",
					"Automatic: BootROM → U-Boot → Linux. Writes no flash; enabling FTP on stock firmware, if needed, is asked separately. Collects SoC, NAND, layout, env, DTB, network; builds ursus-profile-v1 and a porting bundle for UrsusBoot.\n\nNeeds: UART; power the device on after starting, power-cycle it when asked for the Linux part.\n\nThe probe sends nothing to an unknown loader or prompt.")},
			{label: L("Профиль BootROM", "BootROM profile"), hint: L("наблюдать / Press x / RAM U-Boot", "observe / Press x / RAM U-Boot"), porting: "2",
				desc: L("Только BootROM: наблюдать, ответить x на «Press x» и проверить XMODEM, или (только Nokia MD/MF) загрузить RAM U-Boot и собрать профиль через него.",
					"BootROM only: observe, answer 'Press x' and check XMODEM, or (Nokia MD/MF only) load the RAM U-Boot and collect through it.")},
			{label: L("Профиль U-Boot", "U-Boot profile"), hint: L("env, mtd, bdinfo", "env, mtd, bdinfo"), porting: "3",
				desc: L("Только U-Boot: прервать автозагрузку (если это U-Boot) и прочитать версию, env, mtd, bdinfo — только команды чтения.",
					"U-Boot only: interrupt autoboot (when it is U-Boot) and read the version, env, mtd, bdinfo; read commands only.")},
			{label: L("Профиль Linux", "Linux profile"), hint: L("/proc, dmesg, ubinfo", "/proc, dmesg, ubinfo"), porting: "4",
				desc: L("Только Linux: войти в консоль (логин спросит при необходимости) и прочитать /proc, dmesg, mtd, ubinfo.",
					"Linux only: log in to the console (asks for a login if needed) and read /proc, dmesg, mtd, ubinfo.")},
			{label: L("Карта flash / MTD / UBI", "Flash / MTD / UBI map"), porting: "5",
				desc: L("Тип и размеры flash, MTD-разделы и UBI; сэмплы начала разделов — из первой доступной среды.",
					"Flash type and sizes, MTD partitions and UBI, samples of partition heads, from the first reachable environment.")},
			{label: L("DTB / device tree", "DTB / device tree"), porting: "6",
				desc: L("Device tree из Linux (/sys/firmware/fdt) или U-Boot.", "The device tree from Linux (/sys/firmware/fdt) or U-Boot.")},
			{label: L("Сеть / PHY / коммутатор", "Network / PHY / switch"), porting: "7",
				desc: L("PHY, коммутатор, интерфейсы и MAC — из DT, U-Boot mii и Linux ip link.", "PHY, switch, interfaces and MACs from the DT, U-Boot mii and Linux ip link.")},
			{label: L("Экспорт porting bundle", "Export the porting bundle"), hint: "zip", porting: "8",
				desc: L("Упаковывает собранную сессию в porting bundle (zip); по желанию маскирует MAC и серийные номера.",
					"Packs the collected session into a porting bundle (zip), optionally masking MACs and serial numbers.")},
			{label: L("Показать собранный профиль", "View the collected profile"), porting: "9",
				desc: L("Сводка собранного профиля и пути к profile.json и отчёту для UrsusBoot.", "The collected profile summary and the paths to profile.json and the UrsusBoot report.")},
			{label: L("ADVANCED: UBI attach", "ADVANCED: UBI attach"), hint: L("НЕ только чтение", "NOT read-only"), porting: "A",
				desc: L("Разрешает U-Boot выполнить ubi part, чтобы прочитать тома UBI и хеш FIP.\n\nРиск: НЕ только чтение — UBI может обновить метаданные. Подтверждение фразой UBI ATTACH.",
					"Lets U-Boot run ubi part to read the UBI volumes and the FIP hash.\n\nRisk: NOT read-only; UBI may update its metadata. Typed phrase UBI ATTACH.")},
			{label: L("Новая probe-сессия", "New probe session"), porting: "N",
				desc: L("Следующие пункты «Портирования» будут писать в новый каталог work/sessions/….", "The next Porting items write into a new work/sessions/… directory.")},
		}},
		{L("Эксперт", "Expert"), []tuiItem{
			{label: L("UART-терминал", "UART terminal"), hint: L("на весь экран", "full screen"), kind: "terminal",
				desc: L("Прозрачный терминал на весь экран: клавиши уходят в роутер, история ↑/↓, построчный режим, ручной XMODEM, лог.\n\nВыход: Ctrl+] → q (или Ctrl+Q). Потом TUI вернётся.",
					"A full-screen transparent terminal: keys go to the router, ↑/↓ history, line mode, manual XMODEM, logging.\n\nLeave with Ctrl+] → q (or Ctrl+Q); the TUI comes back.")},
			{label: L("RAM U-Boot и prompt", "RAM U-Boot and prompt"), hint: L("на весь экран", "full screen"), kind: "ram-uboot",
				desc: L("Через BootROM загружает RAM U-Boot (во flash ничего не пишется) и открывает UART Shell на его prompt для ручной работы.\n\nВыход: Ctrl+].",
					"Loads the RAM U-Boot through the BootROM (nothing written to flash) and opens the UART Shell at its prompt for manual work.\n\nLeave with Ctrl+].")},
			{label: L("Записать UBI-том из файла", "Write a UBI volume from a file"), kind: "ubi-volume",
				desc: L("Записывает файл в существующий UBI-том (имя тома и файл спросит) со сверкой.\n\nРиск: WRITE — подтверждение фразой.",
					"Writes a file into an existing UBI volume (asks for the name and the file) and verifies it.\n\nRisk: WRITE; typed phrase.")},
			{label: L("Raw-запись в MTD bl2/ubi", "Raw write into MTD bl2/ubi"), kind: "raw-mtd",
				desc: L("Записывает файл в диапазон MTD bl2 или ubi по смещению, со сверкой. Неверный диапазон ломает загрузчик.\n\nРиск: WRITE — подтверждение фразой.",
					"Writes a file into an MTD bl2 or ubi range at an offset and verifies it. A wrong range breaks the bootloader.\n\nRisk: WRITE; typed phrase.")},
			{label: L("UART Shell", "UART Shell"), hint: L("на весь экран", "full screen"), kind: "shell",
				desc: L("Прозрачный UART Shell: сам ничего не отправляет, только ваши клавиши. Для уже работающего U-Boot или Linux.\n\nВыход: Ctrl+].",
					"A transparent UART Shell: sends nothing by itself, only your keys. For a U-Boot or Linux that is already running.\n\nLeave with Ctrl+].")},
		}},
	}
}
