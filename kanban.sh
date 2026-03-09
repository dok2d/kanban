#!/usr/bin/env bash
set -euo pipefail

IMAGE="localhost/kanban:latest"
CONTAINER="kanban"
VOLUME="kanban-data"

usage() {
    echo "Использование: $0 {build|run|stop|restart|logs|backup|status}"
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

    echo "==> Запуск контейнера..."
    podman run -d \
        --name "$CONTAINER" \
        --replace \
        --publish 127.0.0.1:8080:8080 \
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

    echo "==> Канбан запущен: http://127.0.0.1:8080"
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

case "${1:-}" in
    build)   cmd_build   ;;
    run)     cmd_run     ;;
    stop)    cmd_stop    ;;
    restart) cmd_restart ;;
    logs)    cmd_logs    ;;
    backup)  cmd_backup  ;;
    status)  cmd_status  ;;
    *)       usage       ;;
esac
