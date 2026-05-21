# Security

## Container Hardening

The container runs with maximum restrictions:

| Measure                 | Description                                       |
|-------------------------|---------------------------------------------------|
| **Non-root**            | Process runs as the `kanban` user                 |
| **Read-only FS**        | Root filesystem is mounted read-only              |
| **CAP_DROP ALL**        | All Linux capabilities are dropped                |
| **no-new-privileges**   | Privilege escalation is blocked                   |
| **Resource limits**     | 256 MB RAM, 0.5 CPU                              |
| **Network**             | Listens only on 127.0.0.1, exposed via nginx     |
| **Health check**        | Built-in container health check                   |

## Authentication

- **PBKDF2-HMAC-SHA256** — 100,000 iterations, 32-byte salt
- Sessions: random 64-character tokens, 90-day expiry
- HttpOnly cookies — protection against XSS theft
- Automatic cleanup of expired sessions

## Attack Protection

### SQL Injection

All database queries use prepared statements.

### XSS

- Content-Security-Policy (CSP) header
- HTML escaping of user input
- HttpOnly cookies

### File Uploads

- Size limit: 50 MB
- MIME type validation
- Dangerous extensions blocked: `.exe`, `.bat`, `.js`, `.html`, `.svg`, etc.

## nginx

The nginx configuration includes:

- TLS 1.2 / 1.3 with modern ciphers
- HSTS (HTTP Strict Transport Security)
- X-Frame-Options: DENY
- X-Content-Type-Options: nosniff
- Referrer-Policy: strict-origin-when-cross-origin
- Rate limiting zone for brute-force protection
- Request body limit: 2 MB

## Recommendations

- Use TLS in production (self-signed or Let's Encrypt)
- Make regular [backups](backup.md)
- Restrict server access via firewall
- Use strong passwords for the administrator
- Consider LDAP/OIDC for enterprise use
