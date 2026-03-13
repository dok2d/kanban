#!/usr/bin/env bash
set -euo pipefail

IMAGE="localhost/kanban:latest"

# Настройки (переопределяются переменными окружения или флагами)
CONTAINER="${KANBAN_NAME:-kanban}"
HOST="${KANBAN_HOST:-kanban.local}"
PORT="${KANBAN_PORT:-}"
TLS="${KANBAN_TLS:-yes}"
SSL_CERT="${KANBAN_SSL_CERT:-/etc/nginx/ssl/kanban.crt}"
SSL_KEY="${KANBAN_SSL_KEY:-/etc/nginx/ssl/kanban.key}"

# Парсинг флагов
parse_flags() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --name)   CONTAINER="$2"; shift 2 ;;
            --host)   HOST="$2";      shift 2 ;;
            --port)   PORT="$2";      shift 2 ;;
            --tls)    TLS="yes";      shift   ;;
            --no-tls) TLS="no";       shift   ;;
            --cert)   SSL_CERT="$2";  shift 2 ;;
            --key)    SSL_KEY="$2";   shift 2 ;;
            *) break ;;
        esac
    done
    # Производные от CONTAINER
    VOLUME="${CONTAINER}-data"
    # Порт по умолчанию: 443 (TLS) или 80 (HTTP)
    if [[ -z "$PORT" ]]; then
        [[ "$TLS" == "yes" ]] && PORT=443 || PORT=80
    fi
}

usage() {
    cat <<'HELP'
Использование: ./kanban.sh <команда> [флаги]

Команды:
  build    Собрать образ контейнера
  run      Запустить контейнер (без nginx)
  stop     Остановить контейнер
  restart  Перезапустить контейнер
  logs     Логи контейнера
  backup   Бэкап БД в ./backups/
  restore  Восстановить БД из бэкапа (без аргумента — список бэкапов)
  status   Статус контейнера
  deploy   Установить systemd + nginx конфиг

Флаги (для run и deploy):
  --name <имя>       Имя инстанса        (по умолчанию kanban)
  --host <FQDN|IP>   Имя хоста / IP      (по умолчанию kanban.local)
  --port <порт>       Порт               (по умолчанию 443/TLS или 80/HTTP)
  --tls               Включить TLS       (по умолчанию)
  --no-tls            Без TLS — только HTTP
  --cert <путь>       Путь к сертификату (по умолчанию /etc/nginx/ssl/kanban.crt)
  --key  <путь>       Путь к ключу       (по умолчанию /etc/nginx/ssl/kanban.key)

Переменные окружения (альтернатива флагам):
  KANBAN_NAME, KANBAN_HOST, KANBAN_PORT, KANBAN_TLS, KANBAN_SSL_CERT, KANBAN_SSL_KEY

Примеры:
  ./kanban.sh run --port 9090
  ./kanban.sh deploy --host kanban.example.com --port 9090 --tls
  ./kanban.sh deploy --name kanban-work --host work.local --port 8080 --no-tls
  ./kanban.sh deploy --name kanban-home --host home.local --port 9090 --no-tls
HELP
    exit 1
}

cmd_build() {
    echo "==> Сборка образа..."
    podman build -t "$IMAGE" -f Containerfile .
    echo "==> Готово. Размер образа:"
    podman image ls "$IMAGE" --format "{{.Size}}"
}

cmd_run() {
    podman volume exists "$VOLUME" 2>/dev/null || podman volume create "$VOLUME"

    echo "==> Запуск ${CONTAINER} на порту ${PORT}..."
    podman run -d \
        --name "$CONTAINER" \
        --replace \
        --publish "127.0.0.1:${PORT}:8080" \
        --volume "${VOLUME}:/data:Z" \
        --read-only \
        --tmpfs /tmp:rw,noexec,nosuid,size=64m \
        --cap-drop ALL \
        --security-opt no-new-privileges:true \
        --memory 256m \
        --cpus 0.5 \
        --restart unless-stopped \
        --health-cmd "test -f /app/kanban || exit 1" \
        --health-interval 30s \
        --health-start-period 5s \
        "$IMAGE"

    echo "==> ${CONTAINER} запущен: http://127.0.0.1:${PORT}"
}

cmd_stop() {
    echo "==> Остановка ${CONTAINER}..."
    podman stop "$CONTAINER" 2>/dev/null || true
    podman rm "$CONTAINER" 2>/dev/null || true
}

cmd_restart() {
    cmd_stop
    cmd_run
}

cmd_logs() {
    podman logs -f "$CONTAINER"
}

cmd_backup() {
    BACKUP_DIR="${BACKUP_DIR:-./backups}"
    mkdir -p "$BACKUP_DIR"
    TIMESTAMP=$(date +%Y%m%d_%H%M%S)
    local dest="${BACKUP_DIR}/${CONTAINER}_${TIMESTAMP}.db"
    podman unshare cp "$(podman volume inspect "$VOLUME" --format '{{.Mountpoint}}')/kanban.db" "$dest"
    podman unshare chown "$(id -u):$(id -g)" "$dest"
    echo "==> Бэкап: ${dest}"
}

cmd_restore() {
    local backup_file="${1:-}"
    BACKUP_DIR="${BACKUP_DIR:-./backups}"

    if [[ -z "$backup_file" ]]; then
        echo "==> Доступные бэкапы:"
        local found=0
        for f in "${BACKUP_DIR}"/${CONTAINER}_*.db; do
            [[ -f "$f" ]] || continue
            found=1
            local size
            size=$(du -h "$f" | cut -f1)
            echo "    $(basename "$f")  ($size)"
        done
        if [[ $found -eq 0 ]]; then
            echo "    (нет бэкапов)"
        fi
        echo ""
        echo "Использование: ./kanban.sh restore <файл>"
        echo "Пример:        ./kanban.sh restore ${BACKUP_DIR}/${CONTAINER}_20250101_120000.db"
        return 1
    fi

    if [[ ! -f "$backup_file" ]]; then
        echo "==> Ошибка: файл не найден: $backup_file"
        return 1
    fi

    # Проверяем что файл — валидная SQLite БД
    if ! file "$backup_file" | grep -q "SQLite"; then
        echo "==> Ошибка: файл не является базой данных SQLite"
        return 1
    fi

    local mount
    mount=$(podman volume inspect "$VOLUME" --format '{{.Mountpoint}}')
    local db_path="${mount}/kanban.db"

    echo "==> Восстановление из: $backup_file"
    echo "    Том: ${VOLUME}"
    echo ""
    echo "    ВНИМАНИЕ: текущие данные будут перезаписаны!"
    read -rp "    Продолжить? (yes/no): " confirm
    if [[ "$confirm" != "yes" ]]; then
        echo "==> Отменено."
        return 1
    fi

    # Создаём бэкап текущей БД перед восстановлением
    local pre_restore_backup="${BACKUP_DIR}/${CONTAINER}_pre-restore_$(date +%Y%m%d_%H%M%S).db"
    mkdir -p "$BACKUP_DIR"
    if podman unshare test -f "$db_path"; then
        podman unshare cp "$db_path" "$pre_restore_backup"
        podman unshare chown "$(id -u):$(id -g)" "$pre_restore_backup"
        echo "==> Бэкап текущей БД: $pre_restore_backup"
    fi

    # Останавливаем контейнер
    echo "==> Остановка ${CONTAINER}..."
    podman stop "$CONTAINER" 2>/dev/null || true

    # Копируем бэкап в volume через podman unshare
    podman unshare cp "$backup_file" "$db_path"
    # Удаляем WAL/SHM файлы для чистого старта
    podman unshare rm -f "${db_path}-wal" "${db_path}-shm"

    echo "==> БД восстановлена."

    # Запускаем контейнер
    echo "==> Запуск ${CONTAINER}..."
    podman start "$CONTAINER" 2>/dev/null || cmd_run
    echo "==> Готово! Данные восстановлены из бэкапа."
}

cmd_status() {
    podman ps -a --filter "name=$CONTAINER" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
}

generate_systemd() {
    local target_dir="${HOME}/.config/containers/systemd"
    mkdir -p "$target_dir"

    cat > "${target_dir}/${CONTAINER}.container" <<UNIT
[Unit]
Description=Kanban Board (${CONTAINER})
After=network-online.target
Wants=network-online.target

[Container]
Image=${IMAGE}
ContainerName=${CONTAINER}
PublishPort=127.0.0.1:${PORT}:8080
Volume=${VOLUME}.volume:/data:Z

# Security hardening
NoNewPrivileges=true
ReadOnly=true
ReadOnlyTmpfs=true
Tmpfs=/tmp:rw,noexec,nosuid,size=64m
DropCapability=ALL

# Resource limits
PodmanArgs=--memory=256m --cpus=0.5

[Service]
Restart=always
RestartSec=5
TimeoutStartSec=30

[Install]
WantedBy=default.target
UNIT

    cat > "${target_dir}/${VOLUME}.volume" <<UNIT
[Volume]
UNIT

    echo "  ${target_dir}/${CONTAINER}.container (контейнер 127.0.0.1:${PORT})"
    echo "  ${target_dir}/${VOLUME}.volume"
}

generate_nginx() {
    if [[ "$TLS" == "yes" ]]; then
        # HTTP → HTTPS редирект (только для стандартных портов)
        if [[ "$PORT" == "443" ]]; then
            cat <<NGINX
server {
    listen ${HOST}:80;
    server_name ${HOST};
    return 301 https://\$host\$request_uri;
}

NGINX
        fi
        cat <<NGINX
server {
    listen ${HOST}:${PORT} ssl http2;
    server_name ${HOST};

    ssl_certificate     ${SSL_CERT};
    ssl_certificate_key ${SSL_KEY};
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers on;

    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-XSS-Protection "0" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains" always;

    location / {
        proxy_pass http://127.0.0.1:${PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
    }

    location ~ /\\. {
        deny all;
        return 404;
    }

    client_max_body_size 50m;
}
NGINX
    else
        cat <<NGINX
server {
    listen ${HOST}:${PORT};
    server_name ${HOST};

    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-XSS-Protection "0" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;

    location / {
        proxy_pass http://127.0.0.1:${PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
    }

    location ~ /\\. {
        deny all;
        return 404;
    }

    client_max_body_size 50m;
}
NGINX
    fi
}

cmd_deploy() {
    echo "==> Конфигурация:"
    echo "    Имя:       ${CONTAINER}"
    echo "    Хост:      ${HOST}"
    echo "    Порт:      ${PORT}"
    echo "    TLS:       ${TLS}"
    echo "    Контейнер: 127.0.0.1:${PORT}"
    echo "    Nginx:     ${HOST}:${PORT}"
    [[ "$TLS" == "yes" ]] && echo "    Серт:      ${SSL_CERT}" && echo "    Ключ:      ${SSL_KEY}"
    echo ""

    # --- systemd ---
    echo "==> Установка systemd unit-файлов..."
    generate_systemd

    # --- nginx ---
    local nginx_conf="deploy/${CONTAINER}-nginx.conf"
    generate_nginx > "$nginx_conf"
    echo ""
    echo "==> Nginx конфиг сгенерирован: ${nginx_conf}"

    # Пробуем установить в nginx (нужен sudo)
    local nginx_dir="/etc/nginx/sites-available"
    local nginx_enabled="/etc/nginx/sites-enabled"

    if [[ -d "$nginx_dir" ]]; then
        echo ""
        echo "==> Установка nginx конфига (требуется sudo)..."
        if sudo cp "$nginx_conf" "${nginx_dir}/${CONTAINER}.conf"; then
            sudo ln -sf "${nginx_dir}/${CONTAINER}.conf" "${nginx_enabled}/${CONTAINER}.conf" 2>/dev/null || true
            echo "  ${nginx_dir}/${CONTAINER}.conf"
            [[ -d "$nginx_enabled" ]] && echo "  ${nginx_enabled}/${CONTAINER}.conf -> symlink"

            if [[ "$TLS" == "yes" ]] && [[ ! -f "$SSL_CERT" ]]; then
                echo ""
                echo "==> Сертификат не найден. Создать self-signed:"
                echo "  sudo mkdir -p $(dirname "$SSL_CERT")"
                echo "  sudo openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \\"
                echo "    -keyout ${SSL_KEY} \\"
                echo "    -out ${SSL_CERT} \\"
                echo "    -subj \"/CN=${HOST}\""
            fi

            echo ""
            echo "==> Далее выполните:"
            echo "  sudo nginx -t && sudo nginx -s reload"
        else
            echo "  [!] Не удалось скопировать (нет sudo?). Конфиг в ${nginx_conf}"
        fi
    else
        echo "  [!] ${nginx_dir} не найден. Скопируйте вручную: ${nginx_conf}"
    fi

    echo ""
    echo "==> Запуск сервиса:"
    echo "  loginctl enable-linger \$(whoami)   # автостарт после ребута"
    echo "  systemctl --user daemon-reload"
    echo "  systemctl --user start ${CONTAINER}"
    echo ""
    if [[ "$TLS" == "yes" ]]; then
        echo "==> Канбан будет доступен: https://${HOST}$([ "$PORT" != "443" ] && echo ":${PORT}")"
    else
        echo "==> Канбан будет доступен: http://${HOST}$([ "$PORT" != "80" ] && echo ":${PORT}")"
    fi
}

# --- main ---
CMD="${1:-}"
shift || true

# Для restore первый аргумент — путь к файлу, а не флаг
RESTORE_FILE=""
if [[ "$CMD" == "restore" && $# -gt 0 && ! "$1" =~ ^-- ]]; then
    RESTORE_FILE="$1"
    shift
fi

parse_flags "$@"

case "$CMD" in
    build)   cmd_build   ;;
    run)     cmd_run     ;;
    stop)    cmd_stop    ;;
    restart) cmd_restart ;;
    logs)    cmd_logs    ;;
    backup)  cmd_backup  ;;
    restore) cmd_restore "$RESTORE_FILE" ;;
    status)  cmd_status  ;;
    deploy)  cmd_deploy  ;;
    *)       usage       ;;
esac
