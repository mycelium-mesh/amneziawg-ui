# AmneziaWG Web UI

Самостоятельно размещаемая веб-панель управления **AmneziaWG 3.1** — обфусцированным,
устойчивым к DPI форком WireGuard.

Один Go-бинарник и три бинарника AmneziaWG в Alpine-образе: ни интерпретаторов,
ни nginx, ни supervisor. Создавайте несколько серверов, управляйте клиентами и
следите за трафиком из веб-интерфейса, где **AmneziaWG 3.1 (header protection +
random trailers) — встроенный и всегда включённый режим обфускации**.

<p align="center">
  <img src="screenshot2.png" alt="Список серверов: два сервера AmneziaWG со своими клиентами" width="92%"/>
</p>
<p align="center">
  <img src="screenshot.png" alt="Форма создания VPN-сервера с параметрами обфускации AmneziaWG 3.1" width="62%"/>
</p>

## Быстрый старт

Положите на VPS `docker-compose.yml`:

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

Веб-интерфейс — на `http://<ваш-сервер>:54845`, логин `admin`, пароль
`changeme`. Смените его до того, как откроете порт наружу.

> [!IMPORTANT]
> `WEB_UI_PASSWORD` хранит **base64 от SHA-256 пароля**, а не сам пароль:
> ```sh
> printf 'ваш-пароль' | openssl dgst -binary -sha256 | base64
> ```
> HTTPS контейнер не терминирует — поставьте перед ним свой reverse proxy
> (nginx, Caddy, Traefik), если панель смотрит в интернет.

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

## Документация

Полное описание — REST API, параметры обфускации AmneziaWG 3.1, архитектура,
сборка из исходников, профилирование, резервное копирование и отладка:

**[DOCS.md](DOCS.md)**

