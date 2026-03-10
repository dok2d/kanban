# Kanban — персональная доска задач

[English version](README.md)

Минималистичный self-hosted канбан с эпиками, тегами, комментариями, авторизацией и drag-and-drop.

## Стек

- **Go 1.22** — stdlib `net/http` для сервера, шаблонов и роутинга
- **SQLite** (единственная внешняя зависимость через `mattn/go-sqlite3`)
- **Vanilla JS** — никаких фреймворков на фронте
- **Podman** — rootless контейнер с hardening

## Возможности

- Канбан-доска с перетаскиванием карточек между колонками
- Создание / редактирование / удаление задач
- Приоритеты (без, низкий, средний, высокий, критический)
- Эпики с цветовой маркировкой и прогрессом
- Теги (множественные на задачу)
- Вложенные комментарии с ответами
- Зависимости между задачами (зависит от / блокирует)
- Markdown-описания с подсветкой синтаксиса
- Интерактивные TODO-списки
- Вложения файлов и вставка изображений (Ctrl+V)
- Поиск по задачам, комментариям, тегам и эпикам (текст / regex)
- Управление колонками (создание, сортировка, удаление)
- 8 тем оформления (тёмная, светлая, океан, лес, nord, dracula, solarized, spacedust)
- Настраиваемый размер шрифта
- Выбор часового пояса
- Экспорт / импорт доски в JSON
- Авторизация с управлением пользователями (админ-панель)
- Все статические ресурсы локальные (без внешних CDN)
- Адаптивный дизайн для мобильных устройств

## Быстрый старт

```bash
# Сборка
./kanban.sh build

# Запуск
./kanban.sh run

# Открыть http://127.0.0.1:8080
```

При первом запуске будет предложено создать учётную запись администратора.

## Команды

| Команда               | Описание                    |
|-----------------------|-----------------------------|
| `./kanban.sh build`   | Собрать образ контейнера    |
| `./kanban.sh run`     | Запустить контейнер         |
| `./kanban.sh stop`    | Остановить                  |
| `./kanban.sh restart` | Перезапустить               |
| `./kanban.sh logs`    | Логи                        |
| `./kanban.sh backup`  | Бэкап БД в ./backups/       |
| `./kanban.sh status`  | Статус контейнера           |

## Авторизация

Приложение требует авторизации. При первом запуске необходимо создать учётную запись администратора через страницу настройки. Администраторы могут:

- Создавать и удалять пользователей
- Сбрасывать пароли пользователей
- Все пользователи имеют полный доступ к доске

Сессии основаны на cookie (30 дней), пароли хешируются PBKDF2-HMAC-SHA256.

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
├── cmd/server/main.go         # точка входа
├── internal/
│   ├── auth/auth.go           # хеширование паролей PBKDF2 и токены
│   ├── db/store.go            # SQLite хранилище + миграции
│   ├── handler/handler.go     # HTTP обработчики (REST API)
│   └── model/model.go         # модели данных
├── web/
│   └── templates/
│       ├── index.html         # SPA фронтенд
│       └── login.html         # страница входа / первичная настройка
├── deploy/
│   ├── kanban.container       # Quadlet unit
│   └── nginx-kanban.conf      # Nginx config
├── Containerfile              # multi-stage build (ассеты + Go + рантайм)
├── kanban.sh                  # управляющий скрипт
└── README.md
```

## API

Все эндпоинты возвращают JSON. Требуется авторизация (session cookie).

### Авторизация

| Метод  | Путь              | Описание                         |
|--------|-------------------|----------------------------------|
| POST   | /api/auth/setup   | Создание первого администратора  |
| POST   | /api/auth/login   | Вход                             |
| POST   | /api/auth/logout  | Выход                            |
| GET    | /api/auth/me      | Информация о текущем пользователе|

### Пользователи (только для админа)

| Метод  | Путь              | Описание                |
|--------|-------------------|-------------------------|
| GET    | /api/users        | Список пользователей    |
| POST   | /api/users        | Создать пользователя    |
| PUT    | /api/users/:id    | Сменить пароль          |
| DELETE | /api/users/:id    | Удалить пользователя    |

### Доска

| Метод  | Путь                | Описание                                      |
|--------|---------------------|-----------------------------------------------|
| GET    | /api/board          | Вся доска (колонки, задачи, эпики, теги)      |
| GET    | /api/tasks          | Список задач                                  |
| POST   | /api/tasks          | Создать задачу                                |
| GET    | /api/tasks/:id      | Детали задачи                                 |
| PUT    | /api/tasks/:id      | Обновить задачу                               |
| DELETE | /api/tasks/:id      | Удалить задачу                                |
| POST   | /api/tasks/move     | Переместить задачу                            |
| GET    | /api/columns        | Список колонок                                |
| POST   | /api/columns        | Создать колонку                               |
| PUT    | /api/columns/:id    | Обновить колонку                              |
| DELETE | /api/columns/:id    | Удалить колонку                               |
| POST   | /api/columns/reorder| Изменить порядок колонок                      |
| GET    | /api/epics          | Список эпиков                                 |
| POST   | /api/epics          | Создать эпик                                  |
| GET    | /api/epics/:id      | Эпик с задачами                               |
| PUT    | /api/epics/:id      | Обновить эпик                                 |
| DELETE | /api/epics/:id      | Удалить эпик                                  |
| GET    | /api/tags           | Список тегов                                  |
| POST   | /api/tags           | Создать тег                                   |
| DELETE | /api/tags/:id       | Удалить тег                                   |
| POST   | /api/comments       | Добавить комментарий                          |
| PUT    | /api/comments/:id   | Редактировать комментарий                     |
| DELETE | /api/comments/:id   | Удалить комментарий                           |
| GET    | /api/search?q=...   | Поиск по задачам                              |
| POST   | /api/images         | Загрузить изображение (base64)                |
| GET    | /api/images/:id     | Отдать изображение                            |
| POST   | /api/files          | Загрузить файл (base64)                       |
| GET    | /api/files/:id      | Скачать файл                                  |
| GET    | /api/export         | Экспорт доски в JSON                          |
| POST   | /api/import         | Импорт доски из JSON                          |
