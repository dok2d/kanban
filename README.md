# Kanban — Personal Task Board

[Русская версия](README.RUS.md)

Minimalist self-hosted Kanban board with epics, tags, comments, notifications, Telegram integration, and drag-and-drop.

## Stack

- **Go 1.22** — stdlib `net/http` for server, templates, and routing
- **SQLite** (single external dependency via `mattn/go-sqlite3`)
- **Vanilla JS** — no frontend frameworks
- **Podman** — rootless container with hardening

## Features

- Kanban board with drag-and-drop cards between columns
- Create / edit / delete tasks with inline editing
- Priorities (none, low, medium, high, critical)
- Optional deadline with visual indicator on cards
- Epics with color coding and progress tracking
- Tags (multiple per task)
- Nested comments with replies (Markdown, @mentions)
- Task dependencies with search/filter (depends on / blocks)
- Markdown descriptions with syntax highlighting
- Interactive TODO checklists on tasks
- File attachments and image paste (Ctrl+V)
- Search across tasks, comments, tags, and epics (plaintext / regex)
- Column management (create, reorder, delete)
- Three roles: Admin, User, Read-only
- Notification system (in-app + Telegram)
- Task subscriptions for update tracking
- Telegram bot integration for push notifications
- User activity feed with detailed change history
- 8 themes (dark, light, ocean, forest, nord, dracula, solarized, spacedust)
- 10 languages (Russian, English, Chinese, Spanish, French, German, Portuguese, Japanese, Korean, Arabic)
- Adjustable font size
- Timezone selector
- Export / import board as JSON
- All static assets served locally (no external CDN)
- Mobile responsive

## Quick Start

```bash
# Build
./kanban.sh build

# Run
./kanban.sh run

# Open http://127.0.0.1:8080
```

On first launch you will be prompted to create an admin account.

## Commands

| Command               | Description                          |
|-----------------------|--------------------------------------|
| `./kanban.sh build`   | Build container image                |
| `./kanban.sh run`     | Start container                      |
| `./kanban.sh stop`    | Stop container                       |
| `./kanban.sh restart` | Restart container                    |
| `./kanban.sh logs`    | View logs                            |
| `./kanban.sh backup`  | Backup DB to ./backups/              |
| `./kanban.sh status`  | Container status                     |
| `./kanban.sh systemd` | Install systemd quadlet unit files   |

Set `KANBAN_PORT` to change the listen port (default `8080`):
```bash
KANBAN_PORT=9090 ./kanban.sh run
```

## Authentication & Roles

The application requires authentication. On first launch, create an admin account via the setup page.

Three roles are available:
- **Admin** — full access: manage users, columns, epics, tags, Telegram bot settings
- **User** — create/edit/delete tasks, comment, subscribe to notifications
- **Read-only** — view board and tasks, receive notifications

Sessions are cookie-based (90-day expiry) with PBKDF2-HMAC-SHA256 password hashing.

Password recovery is available for users with linked Telegram — a 6-digit code is sent to the bot.

## Telegram Integration

Admins can configure a Telegram bot for push notifications:

1. Create a bot via [@BotFather](https://t.me/BotFather) and get the token
2. In Settings → Telegram, enter the bot token and bot username
3. Users link their accounts by sending a hash code to the bot

Notifications are sent for: task assignments, @mentions, comments on subscribed tasks, and task updates.

Bot commands:
- `/tasks` — list your assigned tasks
- `/task N` — view task #N details and recent comments
- `/comment N text` — add a comment to task #N
- `/help` — show available commands

## Container Security

- **Non-root**: runs as `kanban` user (not root)
- **Read-only filesystem**: root FS mounted read-only
- **CAP_DROP ALL**: all capabilities dropped
- **no-new-privileges**: privilege escalation blocked
- **Limits**: 256MB RAM, 0.5 CPU
- **Listens on 127.0.0.1 only**: exposed via nginx

## Nginx

Reverse proxy config with TLS in `deploy/nginx-kanban.conf`.
Copy to `/etc/nginx/sites-available/` and generate certificates.

Self-signed example:
```bash
openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
  -keyout /etc/nginx/ssl/kanban.key \
  -out /etc/nginx/ssl/kanban.crt \
  -subj "/CN=kanban.local"
```

## Systemd (Quadlet)

For auto-start via systemd quadlet:

```bash
# Install unit files (default port 8080)
./kanban.sh systemd

# Or with a custom port
KANBAN_PORT=9090 ./kanban.sh systemd

# Then enable and start
systemctl --user daemon-reload
systemctl --user start kanban
systemctl --user enable kanban
```

## Project Structure

```
kanban/
├── cmd/server/main.go         # entry point
├── internal/
│   ├── auth/auth.go           # PBKDF2 password hashing & tokens
│   ├── db/store.go            # SQLite storage + migrations
│   ├── handler/
│   │   ├── handler.go         # HTTP handlers (REST API)
│   │   └── telegram.go        # Telegram bot integration
│   └── model/model.go         # data models
├── web/
│   ├── static/                # JS, CSS, fonts (downloaded at build)
│   └── templates/
│       ├── index.html         # SPA frontend
│       └── login.html         # login / setup page
├── deploy/
│   ├── kanban.container       # Quadlet unit
│   ├── kanban-data.volume     # Quadlet volume
│   └── nginx-kanban.conf      # Nginx config
├── Containerfile              # multi-stage build (assets + Go + runtime)
├── kanban.sh                  # management script
└── README.md
```

## API

All endpoints return JSON. Authentication required (session cookie).

### Auth

| Method | Path              | Description              |
|--------|-------------------|--------------------------|
| POST   | /api/auth/setup   | Create first admin user  |
| POST   | /api/auth/login   | Login                    |
| POST   | /api/auth/logout  | Logout                   |
| GET    | /api/auth/me      | Current user info        |
| POST   | /api/auth/reset-request | Request password reset (via Telegram) |
| POST   | /api/auth/reset-confirm | Confirm reset with code |

### Users (admin only)

| Method | Path              | Description              |
|--------|-------------------|--------------------------|
| GET    | /api/users        | List users               |
| POST   | /api/users        | Create user              |
| PUT    | /api/users/:id    | Update user role/password|
| DELETE | /api/users/:id    | Delete user              |

### Board

| Method | Path                | Description                            |
|--------|---------------------|----------------------------------------|
| GET    | /api/board          | Full board (columns, tasks, epics, tags) |
| GET    | /api/tasks          | List tasks                             |
| POST   | /api/tasks          | Create task                            |
| GET    | /api/tasks/:id      | Task details                           |
| PUT    | /api/tasks/:id      | Update task                            |
| DELETE | /api/tasks/:id      | Delete task                            |
| POST   | /api/tasks/move     | Move task between columns              |

### Notifications & Subscriptions

| Method | Path                       | Description               |
|--------|----------------------------|---------------------------|
| GET    | /api/notifications         | User notifications        |
| POST   | /api/notifications/read    | Mark notification read    |
| POST   | /api/notifications/read-all| Mark all read             |
| POST   | /api/subscribe             | Subscribe to task         |
| POST   | /api/unsubscribe           | Unsubscribe from task     |

### Telegram

| Method | Path                          | Description                |
|--------|-------------------------------|----------------------------|
| GET    | /api/settings/telegram        | Get bot settings (admin)   |
| POST   | /api/settings/telegram        | Set bot token/username     |
| GET    | /api/settings/telegram/status | Check if bot configured    |
| POST   | /api/user/telegram/link       | Generate link hash         |
| POST   | /api/user/telegram/unlink     | Unlink Telegram            |

### Settings

| Method | Path                    | Description                |
|--------|-------------------------|----------------------------|
| GET    | /api/settings/timezone  | Get server timezone        |
| POST   | /api/settings/timezone  | Set server timezone        |

### Other

| Method | Path                | Description                            |
|--------|---------------------|----------------------------------------|
| GET    | /api/columns        | List columns                           |
| POST   | /api/columns        | Create column                          |
| PUT    | /api/columns/:id    | Update column                          |
| DELETE | /api/columns/:id    | Delete column                          |
| POST   | /api/columns/reorder| Reorder columns                        |
| GET    | /api/epics          | List epics                             |
| POST   | /api/epics          | Create epic                            |
| GET    | /api/epics/:id      | Epic with tasks                        |
| PUT    | /api/epics/:id      | Update epic                            |
| DELETE | /api/epics/:id      | Delete epic                            |
| GET    | /api/tags           | List tags                              |
| POST   | /api/tags           | Create tag                             |
| DELETE | /api/tags/:id       | Delete tag                             |
| POST   | /api/comments       | Add comment                            |
| PUT    | /api/comments/:id   | Edit comment                           |
| DELETE | /api/comments/:id   | Delete comment                         |
| GET    | /api/search?q=...   | Search tasks                           |
| POST   | /api/images         | Upload image (base64)                  |
| GET    | /api/images/:id     | Serve image                            |
| POST   | /api/files          | Upload file (base64)                   |
| GET    | /api/files/:id      | Download file                          |
| GET    | /api/export         | Export board as JSON                   |
| POST   | /api/import         | Import board from JSON                 |
| GET    | /api/user/activity/:id | User activity feed                  |
| POST   | /api/user/password  | Change own password                    |
