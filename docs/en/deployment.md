# Deployment

## Local Run

The simplest way is to run the container via `kanban.sh`:

```bash
./kanban.sh build
./kanban.sh run
```

The application will be available at `http://127.0.0.1:8080`.

To change the port:

```bash
./kanban.sh run --port 9090
```

## Production Deployment (systemd + nginx)

The `deploy` command generates and installs systemd (quadlet) files and nginx configuration in one step.

### With TLS (default)

```bash
./kanban.sh deploy --host kanban.example.com --port 9090
```

### Without TLS (HTTP)

```bash
./kanban.sh deploy --host 10.0.0.5 --port 8080 --no-tls
```

### Starting After Deploy

```bash
# Enable auto-start after reboot
loginctl enable-linger $(whoami)

# Reload configurations
systemctl --user daemon-reload
systemctl --user start kanban

# Apply nginx config
sudo nginx -t && sudo nginx -s reload
```

## TLS Certificates

### Self-Signed Certificate

For testing or internal use:

```bash
sudo mkdir -p /etc/nginx/ssl
sudo openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
  -keyout /etc/nginx/ssl/kanban.key \
  -out /etc/nginx/ssl/kanban.crt \
  -subj "/CN=kanban.example.com"
```

### Custom Certificate Paths

```bash
./kanban.sh deploy --host kanban.example.com \
  --cert /path/to/cert.crt \
  --key /path/to/key.key
```

## nginx

Deploy generates an nginx configuration (`/etc/nginx/sites-available/kanban`) with:

- TLS 1.2 / 1.3 with modern ciphers
- Security headers (HSTS, CSP, X-Frame-Options, etc.)
- Rate limiting zone
- Reverse proxy to the container (127.0.0.1)
- Request body limit: 2 MB

## systemd (Quadlet)

Quadlet files are used to manage the container via systemd:

- `kanban.container` — container unit
- `kanban-data.volume` — data volume (SQLite)

Files are installed to `~/.config/containers/systemd/`.

### Managing via systemd

```bash
systemctl --user start kanban
systemctl --user stop kanban
systemctl --user restart kanban
systemctl --user status kanban
journalctl --user -u kanban -f   # logs
```

## Container Structure

The container is built in multiple stages (multi-stage build):

1. **Assets** — downloading marked.js, highlight.js, Google Fonts
2. **Builder** — compiling the Go application (static binary with CGO)
3. **Runtime** — minimal Debian image with a non-privileged user

All static assets are served from the container — no external CDNs are used.
