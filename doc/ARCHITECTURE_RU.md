# Архитектура UrsidoRescue

[English version](ARCHITECTURE_EN.md) · [Оглавление](README.md) · версия 0.2.0-test10

## Общая схема

```text
                         ┌──────────── main.go ────────────┐
 оператор ── меню ──────▶│ мастера восстановления          │
 (lang.go: RU/EN)        │ acquireRAMUBoot → BootROM/XMODEM│
                         │ ubootCommand (+RC-маркер)       │
                         │ TFTP-сервер, проверки, selftest │
                         └──┬──────────────┬───────────────┘
                            │              │
          term.go/term_run.go            main_probe.go ──▶ probe/  (read-only)
          UART-терминал                  меню/CLI          guard → collect → analyze
                            │              │                → report → bundle
                            ▼              ▼
                  serial_{linux,windows}.go   console_{linux,windows}.go
                  (COM-порт, 115200 8N1)      (raw-консоль, ввод, размер окна)
```

Один модуль Go `ursidorescue` (Go 1.23), **без внешних зависимостей** — только стандартная
библиотека. Платформенные различия вынесены в файлы с суффиксами `_linux.go` / `_windows.go`
(build-теги по имени файла), всё остальное общее.

## Файлы верхнего уровня

| файл | строк | назначение |
|---|---|---|
| `main.go` | ~2300 | Константы раскладки NAND, профили MD/MF с закреплёнными SHA256, `realMain` (разбор аргументов, поиск корня релиза), главное и экспертное меню, выбор профиля и порта, ожидание BootROM и XMODEM-отправка, ожидание приглашения U-Boot, выполнение команд U-Boot с получением кода возврата, TFTP-сервер, проверки RAM и проверка после записи, все мастера (stock, FIP, physical, ITB, диагностика, эксперт 3/4), пакет логов, `--selftest` |
| `main_probe.go` | ~570 | Связка с пакетом `probe`: меню «Портирование», подменю BootROM, CLI `probe`/`export`, коды выхода, печать сводки профиля |
| `lang.go` | 83 | Язык интерфейса: `L(ru, en)`, `--lang`, `URSIDO_LANG`, локаль, диалог выбора |
| `term.go` | ~400 | Чистая логика терминала без ввода-вывода: ANSI-декодер клавиш, перевод Windows `KEY_EVENT_RECORD` в байты, построчный редактор с историей, XMODEM-приём (CRC, 128/1K) |
| `term_run.go` | ~640 | Работающий терминал: цикл чтения UART, прозрачный/построчный режим, ASCII-фильтр, пейджер с обходом полноэкранных TUI, меню Ctrl+], XMODEM-отправка/приём |
| `serial.go` | 11 | Интерфейс `Serial`: `Name`, `Read(buf, timeout)`, `Write`, `ResetInput`, `Close` |
| `serial_linux.go` | ~110 | termios через `ioctl(TCGETS/TCSETS)`: raw 115200 8N1, `CLOCAL`, без управления потоком; поиск `/dev/ttyUSB*`, `ttyACM*`, `ttyAMA*`, `ttyS*` |
| `serial_windows.go` | ~170 | `kernel32.dll` через `syscall`: `CreateFileW`, `SetCommState`, `SetCommTimeouts`, `PurgeComm`, `ReadFile`/`WriteFile`; поиск существующих `COMn` через `QueryDosDeviceW` |
| `console_linux.go` | 60 | Raw-режим консоли, размер окна (`TIOCGWINSZ`) |
| `console_windows.go` | ~150 | `ReadConsoleInputW`, режимы консоли, включение VT-вывода (ANSI), сохранение QuickEdit |
| `*_test.go` | | Тесты главного пакета (см. ниже) |
| `tools/embedicon/` | ~320 | Сборочная утилита: встраивает иконку (7 размеров, 16–256 px) как ресурсы `RT_ICON`/`RT_GROUP_ICON` в готовый PE32+ `.exe`, чистый Go |
| `assets/` | | `ursus-bear.svg` (исходник) и `ursus-bear.png` (256 px) — фирменный медведь |
| `payloads/` | | Бинарники для BootROM: preloader и RAM FIP для MD и MF |
| `build.sh` | | Сборка релиза |
| `.github/workflows/build.yml` | | CI |

## Пакет `probe/` (Porting Collector)

Намеренно **не импортирует** код восстановления. Всё, что он пишет в порт, проходит через два
входа, и тест проверяет, что других нет.

| файл | назначение |
|---|---|
| `guard.go` | Allowlist команд U-Boot и Linux (`CheckUBoot`, `CheckUBootAttach`, `CheckLinux`): полные имена, запрет сокращений, `;`, `&&`, `\|`, `$`, кавычек и переводов строки; разрушительные команды не проходят никогда |
| `internal.go` | Три фиксированных внутренних шаблона строк (hush-тест, маркер кода возврата, Linux-обёртка `echo URSIDO_B_n; cmd 2>&1; echo URSIDO_E_n_$?`) |
| `session.go` | Сессия: порт, лог `uart.log`, `transcript.jsonl`, таймауты (`Timing` — чтобы тесты шли быстро) |
| `console.go` | Классификация последней строки: приглашение U-Boot, shell Linux, логин, неизвестное |
| `collect.go` | Сценарий сбора по слоям (`BootROM`, `UBoot`, `Linux`, `Flash`, `DT`, `Network`, `GPIO`), остановка автозагрузки, ожидание Linux, логин |
| `parse.go` | Парсеры: hex-дампы `md.b`/`mtd dump`, `mtd list`, `/proc/mtd`, sysfs, `ubinfo`, `printenv`, `bdinfo`, `help`, `mii`, журнал загрузки и SoC |
| `fdt.go` | Собственный парсер FDT/DTB и декомпиляция в `.dts` (без `dtc`) |
| `dtmeta.go` | Выжимка из дерева устройств: разделы flash, Ethernet/PHY, GPIO-кнопки и светодиоды |
| `signature.go` | Что лежит в сэмпле раздела: UBI EC/VID, TF-A FIP ToC, FIT, пусто и т. д. |
| `analyze.go` | Сведение всех источников в `ursus-profile-v1`: у каждого факта `source` и `confidence`, согласие двух источников = high, противоречие → `conflicts` |
| `profile.go` | Схема профиля (`SchemaV1`) |
| `sanitize.go` | Рекурсивное удаление идентичности (MAC, серийники, GPON/PLOAM, учётные данные) из профиля и отчётов |
| `report.go` | `ursusboot-porting-report.md` и `ursusflasher-device-draft.json` |
| `bundle.go` | `README.txt`, `hashes.sha256`, zip; режим `--redact` |
| `lang.go` | Язык сообщений оператору (файлы для разработчиков всегда на английском) |
| `probe_test.go`, `sim_test.go` | Тесты безопасности, парсеров и полный прогон на **симулированной плате** AN7581 (сгенерированный DTB, ответы U-Boot и Linux) |

## Раскладка NAND и адреса (из `main.go`)

```text
SPI-NAND 256 МиБ (0x10000000), eraseblock 0x20000, страница (min I/O) 0x800

0x00000000 ┌──────────────┐
           │ bl2          │ 0x20000   BL2 / preloader
0x00020000 ├──────────────┤
           │ ubi          │ 0x0FFE0000  всё остальное (OpenWrt UBI или заводская раскладка)
           │  …           │
0x0EBA0000 │ ← конец «stock restore span» (mtd16): BL2 + заводская область IBU
           │  …           │
0x10000000 └──────────────┘

Окно, где bad-блоки допустимы при восстановлении стока (физ. адреса):
  0x052C0000 … 0x0EB60000
```

| константа | значение | смысл |
|---|---|---|
| `loadAddr` | `0x90000000` | куда TFTP кладёт файл в RAM |
| `verifyAddr` | `0x98000000` | куда читаются данные для проверки |
| `chunkSize` | `0x00800000` | части по 8 МиБ для больших образов |
| `maxGenericRAMFile` | `0x08000000` | 128 МиБ — предел одной передачи |
| `defaultRouterIP` / `defaultLocalIP` | `192.168.1.1` / `192.168.1.254` | адреса U-Boot / ПК |
| `defaultTFTPPort` | `1069` | UDP-порт TFTP-сервера на ПК |

## Payloads и закрепление

Профили `md`/`mf` в `main.go` содержат путь, размер и SHA256 каждого файла из `payloads/`.
`validatePinned` проверяет их перед каждой отправкой в BootROM и в `--selftest`. Замена файла
без правки профиля = отказ работать. Происхождение файлов — в [../README.md](../README.md#payloads).

## Ключевые механизмы

- **Код возврата U-Boot.** После каждой команды — `echo __URSIDO_<наносекунды>__RC_$?`; ответ
  разбирается регулярным выражением. Без маркера или с `rc≠0` операция останавливается.
- **Медленная отправка строки.** Кусками по 16 байт с паузой 3 мс: UART-буфер U-Boot маленький.
- **TFTP-сервер.** Горутина на UDP; отвечает только на RRQ с ожидаемым именем от `192.168.1.1`,
  поддерживает опции (размер блока), проверяет число переданных байт.
- **Fail-closed в мастерах.** Любая ошибка возвращает `error` до следующей команды записи;
  сообщение `[STOP]` прямо говорит, что дальнейших write/erase не было.
- **BL2 последним** в stock- и physical-восстановлении; карта bad-блоков сверяется до, между и
  после записи.
- **Терминал.** Отдельная горутина читает UART с таймаутом 20 мс; ввод с клавиатуры уходит целыми
  кусками (ANSI-последовательности стрелок не рвутся); пейджер копит до 4 МиБ; обнаружение
  полноэкранного ANSI переживает разрыв последовательности между чтениями.

## Сборка

```sh
./build.sh
```

1. `go vet ./...` и `go test ./...`;
2. `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`: `UrsidoRescue.exe` (windows/amd64),
   `UrsidoRescue-linux-amd64`, `UrsidoRescue-linux-arm64`;
3. `go run ./tools/embedicon` встраивает иконку в `.exe`;
4. копирует `payloads/`, `VERSION`, `STATUS.md`, `PROBE.md` в `dist/UrsidoRescue-<VERSION>/`;
5. пишет `SHA256SUMS` на все файлы;
6. на x86_64 запускает собранный бинарник с `--selftest`.

Нужен только Go ≥ 1.23. Никаких MinGW, windres, Python или ImageMagick.

## CI и релизы

`.github/workflows/build.yml` (ubuntu-latest):

1. проверка `gofmt -l .` — любой неотформатированный файл валит сборку;
2. `./build.sh`;
3. zip `UrsidoRescue-<V>.zip` + `.sha256`, загрузка артефакта сборки;
4. **публикация pre-release** `v<VERSION>` в одном из трёх случаев:
   - пуш тега `v*` (тег обязан совпадать с `VERSION`);
   - ручной запуск workflow на `main` с галочкой `publish`;
   - пуш в `main`, в сообщении коммита есть `[publish prerelease]`.
   Если релиз с таким тегом уже есть — ошибка (перезаписи нет). Текст релиза — `STATUS.md`.

**Выпуск версии** — согласованно поменять три места: `VERSION`, `appVersion` в `main.go` (и проверку
в `selftest()`), раздел в `STATUS.md`; а также строки «0.2.0-testN» в сообщениях об ограничениях
(physical restore, эксперт 3/4).

## Тесты

`go test ./...` — около 50 тестов, выполняются за ~15 с, железо не нужно.

- **Главный пакет:** CRC16 XMODEM, распознавание приглашения, bad-блоки и «хорошие» участки,
  выбор языка, ANSI-декодер, построчный редактор и история, перевод клавиш Windows, ASCII-фильтр
  (в т. ч. кириллица из Windows), обнаружение полноэкранного ANSI (включая разрыв между чтениями),
  пейджер, пакетная отправка escape-последовательностей, локальный Ctrl+Q, XMODEM-приём и отказ
  при неверной CRC.
- **probe:** allowlist (разрушительные команды блокируются, read-only разрешены, UBI attach не
  считается read-only), внутренние шаблоны и «никто, кроме них, не пишет строки», отсутствие
  разрушительных литералов в коде probe, все парсеры, FDT, FIP, разрешение конфликтов, полный
  probe на симулированной плате (с UBI attach и без), пассивность к неизвестному загрузчику,
  `--wake` только для известных приглашений, санитайзер идентичности, офлайн-разбор UBI.
- **tools/embedicon:** встраивание иконки в PE.
- **`--selftest`** проверяет упакованный релиз на месте.
