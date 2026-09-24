# UrsidoRescue

[English version](README.md)

Нативная утилита для Windows и Linux для работы по UART с роутерами на Airoha (Nokia XG-040G-MD /
AN7581, XG-040G-MF / AN7583). Что умеет:

- восстановление через BootROM по XMODEM с RAM U-Boot;
- восстановление стока, FIP и полного образа NAND;
- загрузка recovery ITB из RAM;
- диагностика;
- UART-терминал;
- **Porting Collector** — исследует незнакомые Airoha-устройства и только читает (см.
  [PROBE.md](PROBE.md)).

Интерфейс на русском и английском. Статус: simulation candidate / HW PARTIAL (см. [STATUS.md](STATUS.md)).

Python, pyserial и TFTP-утилиты не нужны: на каждую платформу один статический бинарник.

**Документация (RU/EN):** [doc/](doc/README.md). В ней:
- семейство Ursus;
- руководство оператора;
- все пункты меню;
- архитектура;
- полная история изменений.

> ЛАБОРАТОРНЫЕ UART-сборки. 3.3 В TTL, только GND/TX/RX — VCC не подключать никогда.
> Что проверено на железе, а что нет — см. [STATUS.md](STATUS.md).

## Сборка

    ./build.sh          # vet, тесты, Windows x64 + Linux x86_64/aarch64 в dist/

Бинарник должен лежать рядом с `VERSION` и `payloads/`.

## Payloads

| файл | источник |
|---|---|
| `payloads/md/an7581-preloader.bin` | airoha-router-ursusflasher, OpenWrt AN7581 UBI preloader |
| `payloads/md/an7581-fudan-capable-ram.fip` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-md/recovery-safe-u-boot.fip` |
| `payloads/mf/an7583-preloader.bin` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-mf/ursusboot-uart-preloader.bin` |
| `payloads/mf/an7583-recovery-ram.fip` | airoha-ursusboot v0.1.0-alpha5-t66 `xg040-mf/recovery-safe-u-boot.fip` |

Размеры и SHA256 закреплены в `main.go`; `--selftest` их проверяет.

## Структура

- `main.go`, `main_probe.go`, `lang.go` — мастера восстановления, меню, командная строка (`probe`,
  `export`, `--lang`).
- `term.go`, `term_run.go` — UART-терминал: история, пейджер, ручной XMODEM.
- `serial_*.go`, `console_*.go`, `udp_*.go` — нативный COM-порт, raw-консоль и обработка сетевого
  шума для Windows и Linux.
- `probe/` — исследование в режиме только чтения:
  - allowlist команд (`guard.go`);
  - сборщик данных и парсеры, в том числе парсер DTB;
  - анализ `ursus-profile-v1`;
  - отчёты и экспорт bundle.
  Протестирован на симулированной плате.
- `tools/embedicon/` — встраивает иконку в `.exe` при сборке.
- `doc/` — документация на русском и английском.
