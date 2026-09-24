# О проекте UrsidoRescue

[English version](ABOUT_EN.md) · [Оглавление](README.md)

## Что это такое

**UrsidoRescue** — утилита для ПК, которая спасает роутеры Nokia на SoC Airoha через UART, когда
ни Web, ни SSH, ни собственный загрузчик уже не помогают. Поддерживаются:

| модель | SoC | NAND |
|---|---|---|
| Nokia XG-040G-MD | Airoha AN7581 | SPI-NAND 256 МиБ |
| Nokia XG-040G-MF | Airoha AN7583 | SPI-NAND 256 МиБ |

Утилита делает четыре вещи:

1. **Оживляет «кирпич» через BootROM.** Зашитый в SoC загрузчик Airoha (BootROM) по кнопке Reset
   принимает по XMODEM сначала preloader (BL2), затем FIP (BL31 + U-Boot). UrsidoRescue отдаёт ему
   проверенные файлы и получает в RAM рабочий U-Boot, **ничего не записывая во flash**.
2. **Восстанавливает flash из этого RAM U-Boot:** заводскую прошивку Nokia из бэкапа `mtd16`,
   FIP-том OpenWrt, полный образ NAND на 256 МиБ. Передача по встроенному TFTP-серверу, каждое
   записанное место читается обратно и сверяется по CRC32; BL2 всегда пишется последним.
3. **Даёт инструменты:** запуск OpenWrt initramfs/recovery ITB прямо из RAM, диагностику NAND/UBI,
   UART-терминал с историей, пейджером и ручным XMODEM, запись отдельного UBI-тома или сырого
   диапазона MTD.
4. **Исследует незнакомые Airoha-устройства** (Porting Collector, только чтение) и собирает
   «porting bundle» — профиль `ursus-profile-v1`, черновик отчёта для порта UrsusBoot и черновик
   описания устройства для UrsusFlasher.

Интерфейс двуязычный (русский / английский). Для работы нужны только сам бинарник, файл `VERSION`
и папка `payloads/` рядом с ним — ни Python, ни pip, ни pyserial, ни отдельных TFTP-программ.

### Чем UrsidoRescue *не* является

- Это **не** установщик OpenWrt. Штатная установка — это MedveFlasher (без UART) и UrsusFlasher.
- Это **не** генератор заводских данных. MAC, серийный номер, GPON-идентичность и калибровки живут
  только в вашем бэкапе. Нет бэкапа — нечего восстанавливать.
- Это **лабораторная** сборка: запись во flash на реальном железе ещё не прошла полный цикл
  проверки (см. [../STATUS.md](../STATUS.md)).

## Семейство Ursus

Ursus по-латыни — «медведь», Ursidae — семейство медвежьих. Все проекты семейства — про одни и те
же роутеры Nokia на Airoha и делят друг с другом форматы, файлы и договорённости о безопасности.
Название UrsidoRescue отсылает как раз к «медвежьему семейству»: это спасательный инструмент для всех
остальных.

```text
                ┌────────────────────────────┐
                │ UrsusBoot (в роутере)      │  U-Boot + recovery-слой
                │ airoha-ursusboot           │  WebFailsafe, UBI, FIP
                └───────────▲───────▲────────┘
       ставит, обновляет,   │       │  RAM FIP RECOVERY_SAFE
       делает бэкап         │       │  (payloads/*/…ram.fip)
┌───────────────────────────┴──┐  ┌─┴───────────────────────────┐
│ UrsusFlasher (ПК)            │  │ UrsidoRescue (ПК)           │
│ airoha-router-ursusflasher   │  │ airoha-ursidorescue         │
│ оркестратор установки/бэкапа │  │ UART-спасатель + Porting    │
└──────────────▲───────────────┘  │ Collector                   │
               │ preloader AN7581 └─▲───────────────────────────┘
               │                    │ бэкап mtd16, контракт RC18
┌──────────────┴────────────────────┴─┐
│ MedveFlasher (ПК)                   │
│ nokia-router-medveflasher           │
│ сток Nokia → OpenWrt без UART       │
└─────────────────────────────────────┘
```

### UrsusBoot — `airoha-ursusboot`

https://github.com/Medvedolog/airoha-ursusboot · текущая версия `0.1.0-alpha5-t66`

Компактный U-Boot для Airoha с recovery-средой: обычный U-Boot 2026.07 с патчами OpenWrt и Airoha,
урезанный до того, что нужно роутеру (~512 КиБ загрузочной области), плюс WebFailsafe с настоящей
консолью U-Boot в браузере, понимание образов OpenWrt sysupgrade/FIT, создание и миграция UBI,
самообновление FIP и пути восстановления через BootROM/UART.

**Связь с UrsidoRescue:** RAM FIP обеих плат (`payloads/md/an7581-fudan-capable-ram.fip`,
`payloads/mf/an7583-recovery-ram.fip`) и preloader MF (`payloads/mf/an7583-preloader.bin`) взяты из
релиза UrsusBoot `v0.1.0-alpha5-t66`. Это сборки **RECOVERY_SAFE**: `bootdelay=-1`, `bootcmd` только
печатает текст, сохранённое окружение уходит в заведомо несуществующие UBI-тома — такой U-Boot сам
ничего не запускает и не пишет. Они знают NAND Fudan FM25S01A и FM25G02B (последний не умели старые
RAM U-Boot из MedveFlasher RC18). Porting-отчёт UrsidoRescue (`ursusboot-porting-report.md`) — это
входные данные для нового порта UrsusBoot.

### UrsusFlasher — `airoha-router-ursusflasher`

https://github.com/Medvedolog/airoha-router-ursusflasher · текущая версия `0.2.66`

Оркестратор на ПК (Python 3.12+, Windows/Linux): определяет состояние роутера, делает бэкапы,
ставит и обновляет UrsusBoot из стока Nokia, OpenWrt или recovery UrsusBoot, ведёт ONECLICK и EXPERT
сценарии, умеет аварийное BootROM/UART-восстановление.

**Связь с UrsidoRescue:** preloader MD (`payloads/md/an7581-preloader.bin`) взят из комплекта
UrsusFlasher (OpenWrt AN7581 UBI preloader). Файл `ursusflasher-device-draft.json` в porting bundle —
черновик профиля устройства для UrsusFlasher. UrsidoRescue — узкий нативный UART-спасатель без
Python; UrsusFlasher — полный установщик.

### MedveFlasher — `nokia-router-medveflasher`

https://github.com/Medvedolog/nokia-router-medveflasher · текущая версия `1.0.0-rc35`

Самый старый проект семейства: переводит Nokia XG-040G-MD/MF со стока на OpenWrt **без UART и без
разборки** — бэкап → переходная OpenWrt → постоянная система в UBI. Python 3 без pip, свои
реализации COM-порта, TFTP, XMODEM, AES/RSA.

**Связь с UrsidoRescue:**
- пункт 1 главного меню принимает **бэкап-каталог MedveFlasher** (`mtd0…mtd16`, `SHA256SUMS`) и
  восстанавливает из него `mtd16` — «stock restore span» размером `0x0EBA0000`;
- при проверке бэкапа UrsidoRescue отклоняет BL2, который на самом деле является OpenWrt-preloader
  «линии MedveFlasher» — это значит, что бэкап сделан уже после перепрошивки;
- контракт RECOVERY_SAFE RAM U-Boot («RC18») появился в MedveFlasher.

### UrsidoRescue — `airoha-ursidorescue`

https://github.com/Medvedolog/airoha-ursidorescue · этот репозиторий · версия `0.2.0-test13`

Нативный UART-спасатель и сборщик данных для портирования. Место в семействе — «последний рубеж»:
когда в роутере не осталось ничего работающего, кроме BootROM.

### Общие правила семейства

- **Fail-closed.** Любая непонятная ситуация — стоп, а не «попробуем дальше».
- **Проверка после записи.** Каждая запись во flash читается обратно и сверяется.
- **Закреплённые хеши.** Встроенные бинарники проверяются по размеру и SHA256 перед использованием.
- **Идентичность устройства неприкосновенна.** MAC/серийник/GPON не генерируются и не
  публикуются; в `profile.json` попадает только *место*, где они лежат.
- **BUILD PASS ≠ HW PASS.** Сборка и симуляция не заменяют проверку на железе; статус каждой
  возможности честно пишется в `STATUS.md`.

## Почему UrsidoRescue написан на Go

Остальные ПК-инструменты семейства написаны на Python. Для UrsidoRescue выбран Go (1.23, только
стандартная библиотека, `CGO_ENABLED=0`). Причины:

1. **Один статический файл на платформу.** `UrsidoRescue.exe` для Windows x64 и
   `UrsidoRescue-linux-amd64` / `-arm64` для Linux — без установщика, без runtime, без DLL. Тот,
   у кого роутер превратился в кирпич, скорее всего не будет ставить Python и отлаживать pip без
   интернета (кабель-то уже воткнут в роутер).
2. **Кросс-компиляция с одной машины.** `build.sh` собирает все три цели на Linux в CI одной
   командой. Linux arm64 позволяет спасать роутер с Raspberry Pi или ARM-ноутбука.
3. **Всё нужное есть в стандартной библиотеке.** UDP для TFTP-сервера (`net`), `archive/zip`,
   `compress/gzip` (сжатые бэкапы), `crypto/sha256`, `hash/crc32`, `encoding/binary`, `flag`.
   COM-порт и консоль реализованы прямо через системные вызовы: termios на Linux, `kernel32.dll`
   (`CreateFileW`, `SetCommState`, `ReadConsoleInputW`, `QueryDosDeviceW` …) на Windows — через
   `syscall`, без cgo и сторонних пакетов. Поэтому в `go.mod` нет ни одной зависимости.
4. **Параллельность без боли.** UART-терминал одновременно читает порт, клавиатуру и пишет лог;
   TFTP-сервер крутится рядом с диалогом U-Boot. Горутины и каналы делают это простым и надёжным.
5. **Строгость и проверяемость.** Статическая типизация, `go vet`, `gofmt` в CI и быстрые тесты,
   включая полную симуляцию платы для Porting Collector. Для инструмента, который пишет во flash,
   это важнее скорости разработки.
6. **Воспроизводимость.** `-trimpath -ldflags "-s -w"`, `SHA256SUMS` на весь релиз.

Цена выбора: консольный ввод Windows пришлось написать вручную (`ReadConsoleInputW`, см.
[CHANGELOG_RU.md](CHANGELOG_RU.md), test8), а иконку в `.exe` встраивает свой небольшой инструмент
`tools/embedicon`, потому что стандартного `windres` без MinGW нет.
