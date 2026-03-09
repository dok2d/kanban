# Kanban — персональная доска задач

Минималистичный self-hosted канбан с эпиками, тегами, комментариями и drag-and-drop.

## Стек

- **Go 1.22** — stdlib `net/http` для сервера, шаблонов и роутинга
- **SQLite** (единственная внешняя зависимость через `mattn/go-sqlite3`)
- **Vanilla JS** — никаких фреймворков на фронте
- **Podman** — rootless контейнер с hardening

## Возможности

- Канбан-доска с перетаскиванием карточек между колонками
- Создание / редактирование / удаление задач
- Приоритеты (без, низкий, средний, высокий, критический)
- Эпики с цветовой маркировкой
- Теги (множественные на задачу)
- Комментарии к задачам
- Поиск по названию и описанию
- Управление колонками

## Быстрый старт

```bash
# Сборка
./kanban.sh build

# Запуск
./kanban.sh run

# Открыть http://127.0.0.1:8080
```

## Команды

| Команда              | Описание                    |
|---------------------|-----------------------------|
| `./kanban.sh build`   | Собрать образ контейнера    |
| `./kanban.sh run`     | Запустить контейнер         |
| `./kanban.sh stop`    | Остановить                  |
| `./kanban.sh restart` | Перезапустить               |
| `./kanban.sh logs`    | Логи                        |
| `./kanban.sh backup`  | Бэкап БД в ./backups/      |
| `./kanban.sh status`  | Статус контейнера           |

## Безопасность контейнера

- **Non-root**: процесс запускается от пользователя `kanban` (не root)
- **Read-only filesystem**: корневая ФС монтируется только на чтение
- **CAP_DROP ALL**: все capabilities сброшены
- **no-new-privileges**: запрет эскалации привилегий
- **Лимиты**: 256MB RAM, 0.5 CPU
- **Слушает только 127.0.0.1**: наружу только через nginx

## Nginx

Конфиг для reverse proxy с TLS в `deploy/nginx-kanban.conf`.  
Скопировать в `/etc/nginx/sites-available/` и сгенерировать сертификаты.

Для self-signed:
```bash
openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
  -keyout /etc/nginx/ssl/kanban.key \
  -out /etc/nginx/ssl/kanban.crt \
  -subj "/CN=kanban.local"
```

## Systemd (Quadlet)

Для автозапуска через systemd quadlet — скопировать `deploy/kanban.container`
в `~/.config/containers/systemd/` и выполнить:

```bash
systemctl --user daemon-reload
systemctl --user start kanban
systemctl --user enable kanban
```

## Структура проекта

```
kanban/
├── cmd/server/main.go      # точка входа
├── internal/
│   ├── db/store.go          # SQLite хранилище + миграции
│   ├── handler/handler.go   # HTTP обработчики (REST API)
│   └── model/model.go       # модели данных
├── web/templates/index.html  # SPA фронтенд
├── deploy/
│   ├── kanban.container      # Quadlet unit
│   └── nginx-kanban.conf     # Nginx config
├── Containerfile             # multi-stage build
├── kanban.sh                 # управляющий скрипт
└── README.md
```

## API

Все эндпоинты возвращают JSON.

| Метод  | Путь                | Описание                |
|--------|---------------------|-------------------------|
| GET    | /api/board          | Вся доска (колонки, задачи, эпики, теги) |
| GET    | /api/tasks          | Список задач            |
| POST   | /api/tasks          | Создать задачу          |
| GET    | /api/tasks/:id      | Детали задачи           |
| PUT    | /api/tasks/:id      | Обновить задачу         |
| DELETE | /api/tasks/:id      | Удалить задачу          |
| POST   | /api/tasks/move     | Переместить задачу      |
| GET    | /api/columns        | Список колонок          |
| POST   | /api/columns        | Создать колонку         |
| DELETE | /api/columns/:id    | Удалить колонку         |
| GET    | /api/epics          | Список эпиков           |
| POST   | /api/epics          | Создать эпик            |
| DELETE | /api/epics/:id      | Удалить эпик            |
| GET    | /api/tags           | Список тегов            |
| POST   | /api/tags           | Создать тег             |
| DELETE | /api/tags/:id       | Удалить тег             |
| POST   | /api/comments       | Добавить комментарий    |
| DELETE | /api/comments/:id   | Удалить комментарий     |
