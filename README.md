# Kanban — Personal Task Board

[Русская версия](README.RUS.md)

Self-hosted Kanban board that runs anywhere with zero external dependencies. One binary, one SQLite file, one container — full project management for small teams.

## Why Kanban

- **Zero dependencies** — Go + SQLite, no Redis, no Postgres, no message queue
- **One command to run** — `./kanban.sh build && ./kanban.sh run`, open browser
- **Batteries included** — sprints, epics, tags, file attachments, Markdown, comments with @mentions, search (text + regex), drag-and-drop, Telegram notifications
- **Hardened by default** — rootless Podman, read-only FS, all capabilities dropped, 127.0.0.1 only
- **Works offline** — all static assets bundled, no external CDN calls
- **8 themes, 10 languages** — dark/light/ocean/forest/nord/dracula/solarized/spacedust, from English to Arabic

## Quick Start

```bash
./kanban.sh build
./kanban.sh run
# → http://127.0.0.1:8080
```

First launch opens the admin setup page. Create an account and start using the board immediately.

To deploy on a server with TLS behind nginx:

```bash
./kanban.sh deploy --host kanban.example.com
```

## Features at a Glance

| Area | What you get |
|------|-------------|
| Board | Columns with drag-and-drop, inline editing, priorities, deadlines |
| Planning | Sprints (plan → active → done), epics with color coding and progress |
| Collaboration | Nested comments, @mentions, task subscriptions, activity feed |
| Organization | Tags, dependencies (blocks / depends on), TODO checklists |
| Search | Plaintext and regex across tasks, comments, tags, and epics |
| Files | Attachments, image paste (Ctrl+V), Markdown with syntax highlighting |
| Notifications | In-app + Telegram bot (assignments, mentions, updates) |
| Access control | Three roles: Admin, User, Read-only |
| Data | JSON export/import, scheduled backups via `./kanban.sh backup` |
| Customization | 8 themes, 10 languages, adjustable font size, timezone selector |

## Comparison

| | **Kanban** | Trello | Jira | Notion | WeKan | Planka |
|---|---|---|---|---|---|---|
| Free & open source | **Yes, MIT** | Freemium | Freemium | Freemium | Yes, MIT | No (Fair Use) |
| Self-hosted | **Yes** | No | Data Center ($) | No | Yes | Yes |
| External dependencies | **None** (SQLite) | — | Postgres, Elasticsearch, etc. | — | MongoDB | Postgres, Redis |
| Setup time | **~1 min** | — | Hours | — | ~15 min | ~10 min |
| Sprints & epics | **Yes** | No | Yes | Basic | No | No |
| Telegram notifications | **Yes** | No | No | No | No | Yes |
| Offline / air-gapped | **Yes** | No | Yes (DC) | Partial | Partial | Yes |
| Resource usage | **~30 MB RAM** | — | 4+ GB RAM | — | ~400 MB | ~150 MB |
| Themes | **8** | 2 | 1 | 2 | 3 | 1 |
| Languages | **10** | 20+ | 20+ | ~15 | 100+ | 20+ |

Kanban is not trying to replace Jira for a 500-person company. It is built for individuals and small teams who want a fast, private, self-hosted board that works out of the box without infrastructure overhead.

## Stack

- **Go 1.22** — stdlib `net/http`, no framework
- **SQLite** — single `mattn/go-sqlite3` dependency
- **Vanilla JS** — no React, no Vue, no build step
- **Podman** — rootless container with security hardening

## Documentation

Full guides: **[English](docs/en/README.md)** | **[Русский](docs/ru/README.md)**
