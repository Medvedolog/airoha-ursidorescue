<div align="center">

<img src="assets/ursus-bear.svg" alt="Медвежонок UrsidoRescue" width="112" height="112">

# UrsidoRescue

### Медвежонок-спасатель из семейства Ursus

**Возвращает к жизни «окирпиченные» роутеры Airoha по UART — даже когда жив остался только BootROM**

[![Release](https://img.shields.io/github/v/release/Medvedolog/airoha-ursidorescue?label=release&color=c8873a)](https://github.com/Medvedolog/airoha-ursidorescue/releases/latest)
[![Build](https://img.shields.io/github/actions/workflow/status/Medvedolog/airoha-ursidorescue/build.yml?branch=main&label=build)](https://github.com/Medvedolog/airoha-ursidorescue/actions/workflows/build.yml)
![Go](https://img.shields.io/badge/Go-1.24%20·%20без%20зависимостей-00add8?logo=go&logoColor=white)
![Платформы](https://img.shields.io/badge/Windows%20·%20Linux%20x64%20·%20arm64-один%20бинарник-6f4b2f)
![SoC](https://img.shields.io/badge/Airoha-AN7581%20·%20AN7583-b36b32)
![UI](https://img.shields.io/badge/интерфейс-RU%20·%20EN-555)

**[📦 Релизы](https://github.com/Medvedolog/airoha-ursidorescue/releases)** · [📖 Документация](doc/README.md) · [🧭 Руководство](doc/GUIDE_RU.md) · [📝 История изменений](doc/CHANGELOG_RU.md)

🇬🇧 [English version](README.md)

</div>

---

## Зачем он нужен

Роутеры Nokia XG-040G-MD и XG-040G-MF построены на SoC Airoha. После неудачной прошивки, сломанного
загрузчика или не того образа в них может не остаться ничего работающего: ни веб-интерфейса, ни SSH,
ни U-Boot. Остаётся только **BootROM** — код, зашитый в сам процессор, который всё ещё умеет
принимать программу по последовательному порту.

UrsidoRescue сделан именно для этого момента. Он договаривается с BootROM по UART, загружает
проверенный U-Boot **только в оперативную память** и уже оттуда шаг за шагом чинит flash, проверяя
каждую запись, прежде чем идти дальше. Это последний рубеж семейства: когда UrsusBoot, UrsusFlasher
и MedveFlasher до роутера уже не достают, медвежонок всё ещё может.

Вторая его работа — **разведка**. На Airoha-устройстве, которое ещё никто не портировал, он строго
в режиме чтения собирает всё, что нужно для нового порта UrsusBoot, и упаковывает в один архив.

## Крайний релиз

**0.2.1-test.25-md-mf-total-rescue** делает TOTAL disaster recovery симметричным для Nokia XG-040G-MD / AN7581 и XG-040G-MF / AN7583. Оба профиля используют один fresh-UBI transaction engine с выбором UART-only или TFTP. CI проходит на Windows x64 и Linux x86_64/arm64; MD/MF TOTAL flash-paths пока имеют статус **HW PENDING**.

## Что умеет медвежонок-спасатель

| | |
|---|---|
| 🧸 **Оживить «кирпич»** | BootROM → preloader → RAM U-Boot по XMODEM, во flash при этом ничего не пишется |
| 🏭 **Вернуть сток** | Восстановить заводскую прошивку Nokia из бэкапа `mtd16` / MedveFlasher, BL2 — последним |
| 🔧 **Восстановить FIP** | Заменить UBI-том `fip` OpenWrt, когда UBI цел, но система не стартует |
| 🧯 **Восстановить boot chain** | Починить только BL2 или BL2 + FIP на MD/MF: FIP проверяется первым, BL2 пишется последним |
| 🚨 **Полностью поднять MD/MF с потерянной UBI** | Создать UBI с нуля, static `fip` ID 4, записать профильный UrsusBoot FIP и затем BL2; на выбор UART-only или TFTP |
| 🌐 **Консоль UrsusBoot по Ethernet** | Нативный WebSocket-терминал, загрузка/выгрузка RAM и диагностика без UART, если UrsusBoot уже запущен |
| 💾 **Залить весь NAND** | Записать полный сырой образ 256 МиБ с проверкой каждой части |
| 🚀 **Загрузить из RAM** | Запустить OpenWrt initramfs / recovery ITB, не трогая flash |
| 🩺 **Продиагностировать** | Bad-блоки, разметка MTD и окружение — полностью только чтение |
| ⌨️ **UART-терминал** | Быстрая консоль с историей, пейджером, ручным XMODEM и полным логом |
| 🔍 **Разведать новое железо** | Porting Collector: профиль, DTB, карта flash, отчёт для порта UrsusBoot — только чтение |
| 🔑 **Добраться до root в стоке** | На заводской прошивке Nokia взять актуальные реквизиты через Web и доказать UID 0 по UART |

Всё нужное — внутри одного статического бинарника на платформу: свой драйвер COM-порта, XMODEM,
TFTP-сервер и парсер DTB. Ни Python, ни pip, ни дополнительных утилит, ни интернета.

## TOTAL-восстановление после полной аварии

Если UBI ещё подключается и существующий том `fip` виден, используйте обычный **boot-chain rescue**.
**TOTAL rescue** нужен только когда UBI metadata или `fip` уже нельзя считать достоверными: он
намеренно стирает весь MTD `ubi` и создаёт минимальную загрузочную разметку заново.

| Профиль | Аварийный FIP | Что делать после PASS |
|---|---|---|
| MD / AN7581 | закреплённый полный MD UrsusBoot FIP | загрузиться в UrsusBoot Recovery и установить соответствующий **MD UBI sysupgrade** |
| MF / AN7583 | закреплённый vanilla MF FIP с проверенной заменой BL33 на UrsusBoot | загрузиться в UrsusBoot Recovery и установить соответствующий **MF UBI sysupgrade** |

Для обоих профилей есть одинаковые два транспорта:

- **UART-only:** BootROM, RAM U-Boot, FIP и BL2 передаются только через UART/XMODEM; Ethernet не нужен.
- **TFTP:** FIP и BL2 заранее загружаются в разные области RAM по Ethernet; после начала erase destructive-фаза от сети уже не зависит.

Flash-транзакция для MD и MF одинакова: стабильная BBT → проверка обоих payload в RAM → один
`y/N` → erase `ubi` → fresh UBI → static `fip` **ID 4**, размер `0x100000` →
запись/readback FIP → запись/readback **BL2 последним**. Flash-changing команды не повторяются
автоматически только потому, что ответ UART оказался повреждён или потерян.

> [!CAUTION]
> TOTAL rescue уничтожает прежние OpenWrt-тома и настройки внутри `ubi`. Заводские идентификаторы
> и калибровки он из воздуха не восстанавливает — при наличии заводского бэкапа его нужно хранить.

## Принципы

- **Fail-closed.** Любая неясность — остановка; после остановки больше ничего не пишется.
- **Каждая запись проверяется.** Записанное читается обратно и сверяется, передачи проверяются в RAM.
- **BL2 последним.** Загрузчик пишется только после того, как всё остальное прошло проверку.
- **Закреплённые payloads.** Встроенные бинарники сверяются по размеру и SHA256 перед каждым
  использованием.
- **Ваша идентичность остаётся вашей.** MAC, серийный номер и GPON-данные не генерируются и не
  экспортируются; пароли — только в памяти.
- **Честный статус.** Симуляция — не железо. Что доказано на реальных роутерах, записано в
  [STATUS.md](STATUS.md).

## Поддерживаемое железо

| Модель | SoC | Flash |
|---|---|---|
| Nokia XG-040G-MD | Airoha AN7581 | SPI-NAND 256 МиБ |
| Nokia XG-040G-MF | Airoha AN7583 | SPI-NAND 256 МиБ |

Porting Collector работает и с другими устройствами Airoha — в режиме только чтения.

## Быстрый старт

1. Скачайте ZIP со страницы [релизов](https://github.com/Medvedolog/airoha-ursidorescue/releases) и
   распакуйте. Бинарник должен лежать рядом с `VERSION` и `payloads/`.
2. Подключите USB-UART адаптер **3,3 В**: GND, TX, RX. **VCC не подключать никогда.**
3. Для работы по TFTP подключите ПК кабелем в LAN2/LAN3 и задайте ему `192.168.1.254/24`.
4. Запустите `UrsidoRescue.exe` (Windows) или `./UrsidoRescue-linux-amd64` и следуйте меню.

Всё остальное, пошагово, — в [руководстве оператора](doc/GUIDE_RU.md) и
[описании меню](doc/MENU_RU.md).

> [!WARNING]
> Это **лабораторные UART-сборки**. Часть путей записи ещё не прошла полную проверку на железе —
> см. [STATUS.md](STATUS.md). Всегда держите бэкап: заводские MAC, серийный номер и калибровки
> оптики без него не восстановить.

## Документация

| | |
|---|---|
| [О проекте](doc/ABOUT_RU.md) | Цель, семейство Ursus, почему Go |
| [Руководство оператора](doc/GUIDE_RU.md) | Подключение, сеть, поведение XMODEM и TFTP, командная строка, неполадки |
| [Описание меню](doc/MENU_RU.md) | Каждый пункт меню пошагово |
| [Архитектура](doc/ARCHITECTURE_RU.md) | Исходники, раскладка NAND, payloads, сборка, CI, тесты |
| [Porting Collector](PROBE.md) | Правила безопасности probe и собираемые данные (EN) |
| [История изменений](doc/CHANGELOG_RU.md) | Полная история с самого начала |

## Семейство Ursus

| Проект | Роль |
|---|---|
| [UrsusBoot](https://github.com/Medvedolog/airoha-ursusboot) | U-Boot с recovery-средой внутри роутера |
| [UrsusFlasher](https://github.com/Medvedolog/airoha-router-ursusflasher) | Установка, бэкапы и восстановление с ПК |
| [MedveFlasher](https://github.com/Medvedolog/nokia-router-medveflasher) | Сток Nokia → OpenWrt без UART |
| **UrsidoRescue** | Медвежонок-спасатель по UART и разведчик нового железа |

## Сборка

```sh
./build.sh    # vet, тесты, Windows x64 + Linux x86_64/arm64 в dist/, selftest
```

Нужен только Go ≥ 1.24. Подробности — в [архитектуре](doc/ARCHITECTURE_RU.md#сборка).
