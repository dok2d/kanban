# Kanban — Personal Task Board

[Русская версия](README.RUS.md)

Minimalist self-hosted Kanban board with epics, tags, comments, authentication, and drag-and-drop.

## Stack

- **Go 1.22** — stdlib `net/http` for server, templates, and routing
- **SQLite** (single external dependency via `mattn/go-sqlite3`)
- **Vanilla JS** — no frontend frameworks
- **Podman** — rootless container with hardening

## Features

- Kanban board with drag-and-drop cards between columns
- Create / edit / delete tasks
- Priorities (none, low, medium, high, critical)
- Epics with color coding and progress tracking
- Tags (multiple per task)
- Nested comments with replies
- Task dependencies (depends on / blocks)
- Markdown descriptions with syntax highlighting
- Interactive TODO lists on tasks
- File attachments and image paste (Ctrl+V)
- Search across tasks, comments, tags, and epics (plaintext / regex)
- Column management (create, reorder, delete)
- 8 themes (dark, light, ocean, forest, nord, dracula, solarized, spacedust)
- Adjustable font size
- Timezone selector
- Export / import board as JSON
- Authentication with user management (admin panel)
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

| Command               | Description                 |
|-----------------------|-----------------------------|
| `./kanban.sh build`   | Build container image       |
| `./kanban.sh run`     | Start container             |
| `./kanban.sh stop`    | Stop container              |
| `./kanban.sh restart` | Restart container           |
| `./kanban.sh logs`    | View logs                   |
| `./kanban.sh backup`  | Backup DB to ./backups/     |
| `./kanban.sh status`  | Container status            |

## Authentication

The application requires authentication. On first launch, create an admin account via the setup page. Admins can:

- Create and delete users
- Reset user passwords
- All users have full board access

Sessions are cookie-based (30-day expiry) with PBKDF2-HMAC-SHA256 password hashing.

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

For auto-start via systemd quadlet, copy `deploy/kanban.container`
to `~/.config/containers/systemd/` and run:

```bash
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
│   ├── handler/handler.go     # HTTP handlers (REST API)
│   └── model/model.go         # data models
├── web/
│   └── templates/
│       ├── index.html         # SPA frontend
│       └── login.html         # login / setup page
├── deploy/
│   ├── kanban.container       # Quadlet unit
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

### Users (admin only)

| Method | Path              | Description              |
|--------|-------------------|--------------------------|
| GET    | /api/users        | List users               |
| POST   | /api/users        | Create user              |
| PUT    | /api/users/:id    | Update user password     |
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
