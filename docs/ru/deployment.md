# Развёртывание

## Локальный запуск

Простейший способ — запуск контейнера через `kanban.sh`:

```bash
./kanban.sh build
./kanban.sh run
```

Приложение будет доступно на `http://127.0.0.1:8080`.

Для изменения порта:

```bash
./kanban.sh run --port 9090
```

## Production-развёртывание (systemd + nginx)

Команда `deploy` генерирует и устанавливает файлы systemd (quadlet) и конфигурацию nginx за один шаг.

### С TLS (по умолчанию)

```bash
./kanban.sh deploy --host kanban.example.com --port 9090
```

### Без TLS (HTTP)

```bash
./kanban.sh deploy --host 10.0.0.5 --port 8080 --no-tls
```

### Запуск после deploy

```bash
# Автозапуск после перезагрузки
loginctl enable-linger $(whoami)

# Перезагрузка конфигураций
systemctl --user daemon-reload
systemctl --user start kanban

# Применение nginx
sudo nginx -t && sudo nginx -s reload
```

## TLS-сертификаты

### Самоподписанный сертификат

Для тестирования или внутреннего использования:

```bash
sudo mkdir -p /etc/nginx/ssl
sudo openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
  -keyout /etc/nginx/ssl/kanban.key \
  -out /etc/nginx/ssl/kanban.crt \
  -subj "/CN=kanban.example.com"
```

### Указание путей к сертификатам

```bash
./kanban.sh deploy --host kanban.example.com \
  --cert /path/to/cert.crt \
  --key /path/to/key.key
```

## nginx

Deploy генерирует конфигурацию nginx (`/etc/nginx/sites-available/kanban`) с:

- TLS 1.2 / 1.3 с современными шифрами
- Заголовки безопасности (HSTS, CSP, X-Frame-Options и др.)
- Зона rate limiting
- Проксирование на контейнер (127.0.0.1)
- Лимит тела запроса: 2 МБ

## systemd (Quadlet)

Используются Quadlet-файлы для управления контейнером через systemd:

- `kanban.container` — юнит контейнера
- `kanban-data.volume` — том для данных (SQLite)

Файлы устанавливаются в `~/.config/containers/systemd/`.

### Управление через systemd

```bash
systemctl --user start kanban
systemctl --user stop kanban
systemctl --user restart kanban
systemctl --user status kanban
journalctl --user -u kanban -f   # логи
```

## Структура контейнера

Контейнер собирается в несколько этапов (multi-stage build):

1. **Assets** — загрузка marked.js, highlight.js, шрифтов Google Fonts
2. **Builder** — компиляция Go-приложения (статический бинарник с CGO)
3. **Runtime** — минимальный Debian-образ с непривилегированным пользователем

Все статические ресурсы обслуживаются из контейнера — внешние CDN не используются.
