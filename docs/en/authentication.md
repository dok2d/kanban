# Authentication & Roles

## Local Authentication

On first launch, the application prompts you to create an administrator. After that, login is done via the login form.

### Sessions

- Cookie-based sessions with a 90-day lifetime
- Session token — a random 64-character string
- HttpOnly cookies for XSS protection
- Automatic cleanup of expired sessions (hourly)

### Password Hashing

Passwords are hashed using PBKDF2-HMAC-SHA256:
- 100,000 iterations
- 32-byte random salt

## Roles

| Role        | Capabilities |
|-------------|-------------|
| **Admin**   | Full access: user management, columns, epics, tags, Telegram settings, export/import |
| **User**    | Create, edit, delete tasks; comments; subscriptions; change own password |
| **Read-only** | View board and tasks; receive notifications |

### User Management (Admin)

Administrators can:

- Create new users with a selected role
- Change an existing user's role
- Reset a user's password
- Delete users

Restrictions: administrators cannot change their own role and cannot delete themselves.

## Password Recovery

Available for users with a linked Telegram account:

1. On the login page, click "Forgot password?"
2. Enter your username
3. The bot will send an 8-digit confirmation code
4. Enter the code and set a new password

## LDAP / Active Directory

The application supports LDAP/AD authentication for enterprise environments.

Configuration via API (admin):

```
POST /api/auth/sso/config
```

Configuration parameters:
- LDAP server URL
- Base DN for user search
- Bind DN and password (for search)
- User search filter
- Attribute mapping

With LDAP authentication, users enter their corporate credentials, and the application verifies them via the LDAP server.

## OpenID Connect (OIDC)

SSO support via OIDC providers (Keycloak, Google, Azure AD, etc.).

Configuration via API (admin):

```
POST /api/auth/sso/config
```

Parameters:
- Client ID and Client Secret
- Provider URL (issuer)
- Redirect URI

Once configured, an SSO login button appears on the login page. On first OIDC login, a local account is automatically created.

Endpoints:
- `POST /api/auth/oidc/login` — initiate OIDC authorization
- `POST /api/auth/oidc/callback` — handle provider response
