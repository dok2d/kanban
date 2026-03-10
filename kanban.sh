#!/usr/bin/env bash
set -euo pipefail

IMAGE="localhost/kanban:latest"
CONTAINER="kanban"
VOLUME="kanban-data"
PORT="${KANBAN_PORT:-8080}"

usage() {
    echo "Использование: $0 {build|run|stop|restart|logs|backup|status|systemd}"
    echo ""
    echo "  build    Собрать образ контейнера"
    echo "  run      Запустить контейнер"
    echo "  stop     Остановить контейнер"
    echo "  restart  Перезапустить контейнер"
    echo "  logs     Логи контейнера"
    echo "  backup   Бэкап БД в ./backups/"
    echo "  status   Статус контейнера"
    echo "  systemd  Установить systemd quadlet unit-файлы"
    echo ""
    echo "Переменные окружения:"
    echo "  KANBAN_PORT  Порт (по умолчанию 8080)"
    echo ""
    echo "Примеры:"
    echo "  KANBAN_PORT=9090 $0 run"
    echo "  KANBAN_PORT=9090 $0 systemd"
    exit 1
}

cmd_build() {
    echo "==> Сборка образа..."
    podman build -t "$IMAGE" -f Containerfile .
    echo "==> Готово. Размер образа:"
    podman image ls "$IMAGE" --format "{{.Size}}"
}

cmd_run() {
    # Создать volume если нет
    podman volume exists "$VOLUME" 2>/dev/null || podman volume create "$VOLUME"

    echo "==> Запуск контейнера на порту ${PORT}..."
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

    echo "==> Канбан запущен: http://127.0.0.1:${PORT}"
}

cmd_stop() {
    echo "==> Остановка..."
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
    MOUNT=$(podman volume inspect "$VOLUME" --format '{{.Mountpoint}}')
    cp "${MOUNT}/kanban.db" "${BACKUP_DIR}/kanban_${TIMESTAMP}.db"
    echo "==> Бэкап: ${BACKUP_DIR}/kanban_${TIMESTAMP}.db"
}

cmd_status() {
    podman ps -a --filter "name=$CONTAINER" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
}

cmd_systemd() {
    local target_dir="${HOME}/.config/containers/systemd"
    mkdir -p "$target_dir"

    # Генерируем kanban.container с нужным портом
    cat > "${target_dir}/kanban.container" <<UNIT
[Unit]
Description=Kanban Board
After=network-online.target
Wants=network-online.target

[Container]
Image=${IMAGE}
ContainerName=${CONTAINER}
PublishPort=127.0.0.1:${PORT}:8080
Volume=kanban-data.volume:/data:Z

# Security hardening
NoNewPrivileges=true
ReadOnly=true
ReadOnlyTmpfs=true
# /data writable for SQLite, /tmp for runtime
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

    # Копируем volume unit
    cp deploy/kanban-data.volume "${target_dir}/kanban-data.volume"

    echo "==> Установлено в ${target_dir}/"
    echo "    kanban.container (порт ${PORT})"
    echo "    kanban-data.volume"
    echo ""
    echo "Далее выполните:"
    echo "  systemctl --user daemon-reload"
    echo "  systemctl --user start kanban"
    echo "  systemctl --user enable kanban"
}

case "${1:-}" in
    build)   cmd_build   ;;
    run)     cmd_run     ;;
    stop)    cmd_stop    ;;
    restart) cmd_restart ;;
    logs)    cmd_logs    ;;
    backup)  cmd_backup  ;;
    status)  cmd_status  ;;
    systemd) cmd_systemd ;;
    *)       usage       ;;
esac
