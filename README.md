# AmneziaWG Web UI — веб-панель (админка) для VPN на AmneziaWG 3.1 в Docker

[![Docker Image Size](https://img.shields.io/docker/image-size/myceliummesh/amneziawg-ui/latest?label=docker%20image)](https://hub.docker.com/r/myceliummesh/amneziawg-ui)
[![Docker Pulls](https://img.shields.io/docker/pulls/myceliummesh/amneziawg-ui)](https://hub.docker.com/r/myceliummesh/amneziawg-ui)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![AmneziaWG](https://img.shields.io/badge/AmneziaWG-3.1-blue)](DOCS.md#-amneziawg-31--главное)


**AmneziaWG Web UI** — самостоятельно размещаемая (self-hosted) админ-панель
для **AmneziaWG** — обфусцированного форка **WireGuard**, который обходит
DPI-блокировки VPN. Одна команда `docker compose up -d` — и у вас
собственный VPN-сервер с админкой в браузере: создавайте несколько серверов
AmneziaWG, добавляйте клиентов, выдавайте конфиги как файл `.conf`, QR-код
или ссылку `vpn://` для приложения **AmneziaVPN**, следите за трафиком и
подключениями в браузере. Amnezia Panel.

Это лёгкая альтернатива `amnezia-wg-easy` и `wg-easy`: один Go-бинарник и
три бинарника AmneziaWG в Alpine-образе размером всего **~23 МБ** (у
[amnezia-wg-easy](https://github.com/w0rng/amnezia-wg-easy) — ~80 МБ) — без
Node.js, nginx и supervisor. **AmneziaWG 3.1 (header protection + random
trailers) — встроенный и всегда включённый режим обфускации**, ничего
настраивать руками не нужно.

<p align="center">
  <img src="screenshot2.png" alt="Админ-панель AmneziaWG Web UI: графики трафика VPN, CPU, RAM и диска за 5 минут, список VPN-серверов AmneziaWG с клиентами, трафиком и статусом подключения"/>
</p>
<p align="center">
  <img src="screenshot.png" alt="Конфигурация клиента AmneziaWG: QR-код для приложения AmneziaWG / AmneziaVPN, текст .conf с параметрами обфускации Jc, Jmin, Jmax, S1-S4, H1-H4, HeaderProtectionKey, ссылка vpn://"/>
</p>

> **AmneziaWG Web UI** is a self-hosted admin panel (web UI) for AmneziaWG —
> the DPI-resistant, obfuscated WireGuard fork. A single ~23 MB Docker image
> (Go + Alpine) to create VPN servers, manage clients, export `.conf`, QR
> codes and AmneziaVPN `vpn://` links, and watch traffic — with AmneziaWG 3.1
> obfuscation (header protection, random trailers) always on. The UI is in
> English and Russian; the documentation is in Russian.

## Содержание

- [Возможности](#возможности)
- [Быстрый старт: AmneziaWG в Docker за одну минуту](#быстрый-старт-amneziawg-в-docker-за-одну-минуту)
- [Обновление](#обновление)
- [Переменные окружения](#переменные-окружения)
- [Частые вопросы (FAQ)](#частые-вопросы-faq)
- [Документация](#документация)
- [Список изменений](#список-изменений)

## Возможности

- **Несколько VPN-серверов AmneziaWG** на одном хосте, у каждого свой порт,
  подсеть, MTU, DNS, endpoint и параметры обфускации
- **AmneziaWG 3.1 по умолчанию**: защита заголовков (header protection),
  random trailers, отключённые cookies, мусорные пакеты (junk packets) и
  паддинг — `HeaderProtectionKey` и все параметры генерируются автоматически
  и синхронизируются с каждым клиентским конфигом
- **Управление клиентами**: добавление, редактирование, удаление,
  приостановка и повторная активация клиента без перезапуска сервера,
  автоматическая приостановка по расписанию
- **Экспорт конфига клиента** тремя способами: файл `.conf`, **QR-код** для
  мобильных приложений и нативная ссылка **`vpn://`** для AmneziaVPN
- **Мониторинг в реальном времени**: трафик, последнее рукопожатие и endpoint
  каждого клиента, статус сервера — обновляются автоматически
- **REST API** для автоматизации: всё, что делает веб-интерфейс, доступно
  через HTTP-эндпоинты за basic auth
- **Автозапуск серверов** после перезапуска контейнера, **автоматическая
  настройка iptables**, поддержка IPv4 и IPv6
- **Интерфейс на русском и английском** — язык берётся из настроек браузера
- **Минимальный образ**: Go + Alpine, ~23 МБ, без интерпретаторов и лишних
  сервисов

## Быстрый старт: AmneziaWG в Docker за одну минуту

Понадобится VPS с Linux и установленным Docker. Положите на сервер
`docker-compose.yml`:

```yaml
services:
  app:
    image: myceliummesh/amneziawg-ui:latest
    restart: unless-stopped
    ports:
      - "54845:54845/tcp"   # веб-интерфейс
      - "54844:54844/udp"   # VPN (Можно задать диапазон)
    environment:
      - WEB_UI_USER=admin
      # base64(sha256(пароль)); значение ниже — это "changeme", замените:
      # printf 'ваш-пароль' | openssl dgst -binary -sha256 | base64
      - WEB_UI_PASSWORD=BXugPWxEEEhj3HNh/kV4ll0YhzYPkKCJWILlimJI/IY=
    volumes:
      - data:/etc/amnezia
    cap_add:
      - NET_ADMIN
      - SYS_MODULE
    devices:
      - /dev/net/tun
    sysctls:
      - net.ipv4.ip_forward=1
      - net.ipv4.conf.all.src_valid_mark=1
      - net.ipv6.conf.all.disable_ipv6=0
      - net.ipv6.conf.all.forwarding=1
      - net.ipv6.conf.default.forwarding=1
    logging:
      options:
        max-size: 10m
        max-file: 3

volumes:
  data:
```

И поднимите одной командой:

```sh
docker compose up -d
```

Админ-панель — на `http://<ваш-сервер>:54845`, логин `admin`, пароль
`changeme`. Смените его до того, как откроете порт наружу.

> [!IMPORTANT]
> `WEB_UI_PASSWORD` хранит **base64 от SHA-256 пароля**, а не сам пароль:
> ```sh
> printf 'ваш-пароль' | openssl dgst -binary -sha256 | base64
> ```
> HTTPS контейнер не создаёт — если нужен HTTPS, поставьте перед ним свой
> reverse proxy (nginx, Caddy, Traefik).

Дальше — в браузере: **Create server** → сервер AmneziaWG поднят →
**Add client** → скачайте `.conf`, отсканируйте QR-код или скопируйте
ссылку `vpn://` в приложение AmneziaVPN.

## Обновление

Скачайте свежий образ и пересоздайте контейнер — из той же директории, где
лежит `docker-compose.yml`:

```sh
docker compose pull
docker compose up -d
```

Серверы, клиенты и `web_config.json` лежат в томе `/etc/amnezia` и
обновление не затрагивают; после старта контейнер сам поднимет созданные
ранее серверы. VPN-клиенты переподключатся
автоматически. Старый образ можно удалить: `docker image prune (если понимаешь, что это за команда)`.

> [!TIP]
> Перед обновлением загляните в [CHANGELOG.md](CHANGELOG.md) — там отмечены
> изменения, которые требуют действий с вашей стороны.

## Переменные окружения

Все они задают лишь **значения по умолчанию** — почти всё переопределяется для
каждого сервера через UI или API. Исключение — `WEB_UI_*`, они читаются только
при старте.

| Переменная | По умолчанию | Описание |
|---|---|---|
| `WEB_UI_PORT` | `54845` | Порт веб-интерфейса. То же значение зашито в образ и публикуется в compose |
| `WEB_UI_USER` | `admin` | Имя пользователя для basic auth |
| `WEB_UI_PASSWORD` | `changeme` | Пароль для basic auth в виде base64 от SHA-256 (см. врезку выше) |
| `WEB_UI_PPROF` | `false` | Профилировщик на `/debug/pprof` за той же basic auth. Разбирается как булево (`1`, `true`, `t`), всё остальное — выключено |
| `AUTO_START_SERVERS` | `true` | Поднимать созданные ранее серверы при старте контейнера |
| `DEFAULT_MTU` | `1280` | MTU по умолчанию для новых серверов |
| `DEFAULT_SUBNET` | `10.0.0.0/24` | Подсеть по умолчанию для новых серверов |
| `DEFAULT_PORT` | `54844` | UDP-порт по умолчанию для новых серверов. Именно он публикуется в compose |
| `DEFAULT_DNS` | `8.8.8.8,1.1.1.1` | DNS-серверы, попадающие в клиентские конфиги |

Всё состояние — конфиги серверов и `web_config.json` — лежит в томе
`/etc/amnezia`. Забэкапьте этот том, и панель переедет на другую машину как есть.

## Частые вопросы (FAQ)

### Чем AmneziaWG отличается от WireGuard?

AmneziaWG — форк WireGuard с обфускацией трафика: мусорные пакеты, паддинг и
изменённые заголовки не дают DPI опознать протокол. AmneziaWG 3.0 добавил
**header protection** — шифрование самого заголовка пакета WireGuard, а 3.1 —
**random trailers** и отключение cookie-обмена. Скорость и криптография те же,
что у WireGuard. Подробнее — в [DOCS.md](DOCS.md#-amneziawg-31--главное) и в
статье на Хабре «[AmneziaWG: от 2.0 к 3.1, и что творится у вас под капотом](https://habr.com/ru/articles/1080342/)».

### Какие клиенты подключатся к серверу AmneziaWG 3.1?

Официальное приложение **AmneziaVPN 5.0.1.5 и новее** для Windows, macOS,
Linux, Android и iOS — скопируйте в него ссылку `vpn://` или отсканируйте
QR-код. Для более старых клиентов снимите в форме сервера галочки
`RandomTrailers` и `DisableCookies` — остальные параметры AmneziaWG 3.x с ними
совместимы.

### Как поменять пароль от админ-панели?

Пароль хранится в `WEB_UI_PASSWORD` как base64 от SHA-256. Посчитайте новое
значение командой `printf 'ваш-пароль' | openssl dgst -binary -sha256 | base64`,
подставьте в `docker-compose.yml` и перезапустите контейнер.

### Как сделать бэкап и перенести VPN на другой сервер?

Всё состояние лежит в томе `/etc/amnezia`: `web_config.json` и `.conf`
каждого сервера. Скопируйте его (`docker cp <контейнер>:/etc/amnezia ./backup/`),
разверните том на новой машине и запустите контейнер — серверы и клиенты
поднимутся как были.

### Нужен ли HTTPS и как его включить?

Панель отдаёт HTTP с basic auth. Для доступа из интернета поставьте перед
ней reverse proxy с TLS (Caddy, nginx, Traefik) или ограничьте порт
`54845` файрволом.

## Документация

Полное описание — REST API, справочник параметров обфускации AmneziaWG 3.1
(`Jc`, `Jmin`, `Jmax`, `S1`–`S4`, `H1`–`H4`, `HeaderProtectionKey`,
`RandomTrailers`, `DisableCookies`, `I1`–`I5`), архитектура, сборка из
исходников, профилирование, резервное копирование и отладка:

**[DOCS.md](DOCS.md)**

Статья на Хабре о протоколе и о том, какие параметры обязаны совпадать у
сервера и клиента:
**[AmneziaWG: от 2.0 к 3.1, и что творится у вас под капотом](https://habr.com/ru/articles/1080342/)**

## Список изменений

**[CHANGELOG.md](CHANGELOG.md)**

---

*Ключевые слова: AmneziaWG, AmneziaWG 3.1, Amnezia VPN, WireGuard, обход DPI,
обход блокировок VPN, self-hosted VPN, веб-панель AmneziaWG, админка AmneziaWG,
админ-панель WireGuard, AmneziaWG admin panel, AmneziaWG panel, WireGuard web
UI, VPN в Docker, docker compose VPN, amnezia-wg-easy, wg-easy, Go, Fiber,
Fyne, WebAssembly.*
