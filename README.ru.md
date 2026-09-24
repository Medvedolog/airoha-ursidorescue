<div align="center">

<img src="assets/ursus-bear.svg" alt="Медвежонок UrsidoRescue" width="112" height="112">

# UrsidoRescue

### Медвежонок-спасатель из семейства Ursus

**Возвращает к жизни «окирпиченные» роутеры Airoha по UART — даже когда жив остался только BootROM**

[![Pre-release](https://img.shields.io/github/v/release/Medvedolog/airoha-ursidorescue?include_prereleases&label=pre-release&color=c8873a)](https://github.com/Medvedolog/airoha-ursidorescue/releases)
[![Build](https://img.shields.io/github/actions/workflow/status/Medvedolog/airoha-ursidorescue/build.yml?branch=main&label=build)](https://github.com/Medvedolog/airoha-ursidorescue/actions/workflows/build.yml)
![Go](https://img.shields.io/badge/Go-1.23%20·%20без%20зависимостей-00add8?logo=go&logoColor=white)
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

## Что умеет медвежонок-спасатель

| | |
|---|---|
| 🧸 **Оживить «кирпич»** | BootROM → preloader → RAM U-Boot по XMODEM, во flash при этом ничего не пишется |
| 🏭 **Вернуть сток** | Восстановить заводскую прошивку Nokia из бэкапа `mtd16` / MedveFlasher, BL2 — последним |
| 🔧 **Починить загрузку** | Заменить UBI-том `fip` OpenWrt, когда система цела, но не стартует |
| 💾 **Залить весь NAND** | Записать полный сырой образ 256 МиБ с проверкой каждой части |
| 🚀 **Загрузить из RAM** | Запустить OpenWrt initramfs / recovery ITB, не трогая flash |
| 🩺 **Продиагностировать** | Bad-блоки, разметка MTD и окружение — полностью только чтение |
| ⌨️ **UART-терминал** | Быстрая консоль с историей, пейджером, ручным XMODEM и полным логом |
| 🔍 **Разведать новое железо** | Porting Collector: профиль, DTB, карта flash, отчёт для порта UrsusBoot — только чтение |
| 🔑 **Добраться до root в стоке** | На заводской прошивке Nokia взять актуальные реквизиты через Web и доказать UID 0 по UART |

Всё нужное — внутри одного статического бинарника на платформу: свой драйвер COM-порта, XMODEM,
TFTP-сервер и парсер DTB. Ни Python, ни pip, ни дополнительных утилит, ни интернета.

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

Нужен только Go ≥ 1.23. Подробности — в [архитектуре](doc/ARCHITECTURE_RU.md#сборка).
