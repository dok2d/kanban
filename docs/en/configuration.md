# Configuration

## Server Startup Flags

The server (`cmd/server/main.go`) accepts the following flags:

| Flag       | Description              | Default           |
|------------|--------------------------|-------------------|
| `-addr`    | Server address and port  | `:8080`           |
| `-db`      | Path to SQLite DB file   | `/data/kanban.db` |
| `-verbose` | Verbose logging          | `false`           |

## kanban.sh Flags

For `run` and `deploy` commands:

| Flag              | Description                     | Default                         |
|-------------------|---------------------------------|---------------------------------|
| `--host <value>`  | FQDN or IP address              | `kanban.local`                  |
| `--port <port>`   | Port (nginx + container)        | `443` (TLS) / `80` (HTTP)      |
| `--tls`           | Enable TLS (HTTPS)              | enabled                         |
| `--no-tls`        | HTTP only, no TLS               | —                               |
| `--cert <path>`   | Path to TLS certificate         | `/etc/nginx/ssl/kanban.crt`    |
| `--key <path>`    | Path to TLS key                 | `/etc/nginx/ssl/kanban.key`    |

## Environment Variables

As an alternative to flags, environment variables can be used:

| Variable           | Corresponds to    |
|--------------------|-------------------|
| `KANBAN_HOST`      | `--host`          |
| `KANBAN_PORT`      | `--port`          |
| `KANBAN_TLS`       | `--tls` / `--no-tls` |
| `KANBAN_SSL_CERT`  | `--cert`          |
| `KANBAN_SSL_KEY`   | `--key`           |

## UI Settings

### Theme

8 themes to choose from: dark, light, ocean, forest, nord, dracula, solarized, spacedust. The selection is saved in the browser's localStorage.

### Language

10 languages: Russian, English, Chinese, Spanish, French, German, Portuguese, Japanese, Korean, Arabic.

### Font Size

Configurable font size (default 18px).

### Timezone

The administrator can set the server timezone via settings:

```
POST /api/settings/timezone
{"timezone": "Europe/Moscow"}
```

### Telegram Bot

Configuration in "Settings → Telegram" (administrators only). See [Telegram Bot](telegram.md) for details.

## Database

- SQLite with WAL mode (Write-Ahead Logging) for concurrent access
- Foreign key support
- Automatic migrations on startup
- DB path is set via the `-db` flag
