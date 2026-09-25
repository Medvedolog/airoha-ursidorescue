# UrsidoRescue — документация / documentation

Версия: **0.2.0-test18** · статус: simulation candidate / HW PARTIAL (см. [../STATUS.md](../STATUS.md))

## Русский

| документ | о чём |
|---|---|
| [ABOUT_RU.md](ABOUT_RU.md) | Что такое UrsidoRescue, семейство Ursus и его репозитории, почему Go |
| [GUIDE_RU.md](GUIDE_RU.md) | Руководство оператора: подключение UART, сеть, запуск, командная строка, логи, неполадки |
| [MENU_RU.md](MENU_RU.md) | Все пункты всех меню: что делает, какие шаги выполняет, где остановится |
| [ARCHITECTURE_RU.md](ARCHITECTURE_RU.md) | Структура исходников, модули, раскладка NAND, сборка, CI, релизы, тесты |
| [CHANGELOG_RU.md](CHANGELOG_RU.md) | История изменений |
| [UI_SPEC_RU.md](UI_SPEC_RU.md) | **ТЗ v3.2 на единый интерфейс** Web-GUI / TUI / CLI — утверждённая основа разработки |
| [HW_SMOKE_STAGE1_RU.md](HW_SMOKE_STAGE1_RU.md) | Проверка этапа 1 на железе: СТОП и сессии |
| [HW_SMOKE_STAGE2_RU.md](HW_SMOKE_STAGE2_RU.md) | Проверка на железе: TUI, рамка UART, выбор путей, установка UrsusBoot |
| [ui-mockup/](ui-mockup/index.html) | Кликабельный HTML-макет Web-GUI в стиле UrsusBoot — дизайн-референс к ТЗ |

## English

| document | contents |
|---|---|
| [ABOUT_EN.md](ABOUT_EN.md) | What UrsidoRescue is, the Ursus family and its repositories, why Go |
| [GUIDE_EN.md](GUIDE_EN.md) | Operator guide: UART wiring, network, launch, command line, logs, troubleshooting |
| [MENU_EN.md](MENU_EN.md) | Every item of every menu: what it does, which steps it runs, where it stops |
| [ARCHITECTURE_EN.md](ARCHITECTURE_EN.md) | Source layout, modules, NAND layout, build, CI, releases, tests |
| [CHANGELOG_EN.md](CHANGELOG_EN.md) | Change history |
| [UI_SPEC_RU.md](UI_SPEC_RU.md) | Unified UI specification v3.2 (Web-GUI / TUI / CLI), Russian — the approved development baseline |
| [HW_SMOKE_STAGE1_RU.md](HW_SMOKE_STAGE1_RU.md) | Stage 1 hardware smoke test: STOP and sessions (Russian) |
| [HW_SMOKE_STAGE2_RU.md](HW_SMOKE_STAGE2_RU.md) | Hardware check: TUI, UART frame, paths, UrsusBoot install (Russian) |
| [ui-mockup/](ui-mockup/index.html) | Clickable HTML mockup of the Web-GUI in the UrsusBoot style — design reference for the spec |

Главный README: [../README.ru.md](../README.ru.md) (RU) · [../README.md](../README.md) (EN). Porting Collector reference (EN): [../PROBE.md](../PROBE.md).

> LAB / UART test builds. 3.3 V TTL UART, GND/TX/RX only — never connect VCC.
> ЛАБОРАТОРНЫЕ UART-сборки. 3.3 В TTL, только GND/TX/RX — VCC не подключать никогда.
