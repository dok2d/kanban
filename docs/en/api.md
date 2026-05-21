# REST API

All endpoints return JSON. Authentication (session cookie) is required, except for `/api/auth/setup` on first launch and the login page.

## Authentication

| Method | Path                      | Description                             |
|--------|---------------------------|-----------------------------------------|
| POST   | `/api/auth/setup`         | Create the first administrator          |
| POST   | `/api/auth/login`         | Log in                                  |
| POST   | `/api/auth/logout`        | Log out                                 |
| GET    | `/api/auth/me`            | Current user info                       |
| POST   | `/api/auth/reset-request` | Password reset request (via Telegram)   |
| POST   | `/api/auth/reset-confirm` | Confirm reset with code                 |
| POST   | `/api/auth/sso/config`    | Configure OIDC/LDAP (admin)             |
| POST   | `/api/auth/oidc/login`    | Initiate OIDC authorization             |
| POST   | `/api/auth/oidc/callback` | Handle OIDC provider response           |

## Users (admin)

| Method | Path             | Description                 |
|--------|------------------|-----------------------------|
| GET    | `/api/users`     | List users                  |
| POST   | `/api/users`     | Create user                 |
| PUT    | `/api/users/:id` | Update role/password        |
| DELETE | `/api/users/:id` | Delete user                 |

## Board & Tasks

| Method | Path               | Description                                           |
|--------|--------------------|-------------------------------------------------------|
| GET    | `/api/board`       | Full board (columns, tasks, epics, sprints, tags)     |
| GET    | `/api/tasks`       | List tasks                                            |
| POST   | `/api/tasks`       | Create task                                           |
| GET    | `/api/tasks/:id`   | Task details                                          |
| PUT    | `/api/tasks/:id`   | Update task                                           |
| DELETE | `/api/tasks/:id`   | Delete task                                           |
| POST   | `/api/tasks/move`  | Move task between columns                             |

## Columns

| Method | Path                   | Description           |
|--------|------------------------|-----------------------|
| GET    | `/api/columns`         | List columns          |
| POST   | `/api/columns`         | Create column         |
| PUT    | `/api/columns/:id`     | Update column         |
| DELETE | `/api/columns/:id`     | Delete column         |
| POST   | `/api/columns/reorder` | Reorder columns       |

## Sprints

| Method | Path                        | Description                           |
|--------|-----------------------------|---------------------------------------|
| GET    | `/api/sprints`              | List sprints                          |
| POST   | `/api/sprints`              | Create sprint                         |
| GET    | `/api/sprints/:id`          | Sprint with tasks                     |
| PUT    | `/api/sprints/:id`          | Update sprint                         |
| DELETE | `/api/sprints/:id`          | Delete sprint                         |
| POST   | `/api/sprints/:id/complete` | Complete sprint (carry over tasks)    |

## Epics

| Method | Path              | Description         |
|--------|-------------------|---------------------|
| GET    | `/api/epics`      | List epics          |
| POST   | `/api/epics`      | Create epic         |
| GET    | `/api/epics/:id`  | Epic with tasks     |
| PUT    | `/api/epics/:id`  | Update epic         |
| DELETE | `/api/epics/:id`  | Delete epic         |

## Tags

| Method | Path            | Description     |
|--------|-----------------|-----------------|
| GET    | `/api/tags`     | List tags       |
| POST   | `/api/tags`     | Create tag      |
| DELETE | `/api/tags/:id` | Delete tag      |

## Comments

| Method | Path                 | Description         |
|--------|----------------------|---------------------|
| POST   | `/api/comments`      | Add comment         |
| PUT    | `/api/comments/:id`  | Edit comment        |
| DELETE | `/api/comments/:id`  | Delete comment      |

## Notifications & Subscriptions

| Method | Path                          | Description                  |
|--------|-------------------------------|------------------------------|
| GET    | `/api/notifications`          | User notifications           |
| POST   | `/api/notifications/read`     | Mark as read                 |
| POST   | `/api/notifications/read-all` | Mark all as read             |
| POST   | `/api/subscribe`              | Subscribe to a task          |
| POST   | `/api/unsubscribe`            | Unsubscribe from a task      |

## Files & Images

| Method | Path              | Description                     |
|--------|-------------------|---------------------------------|
| POST   | `/api/images`     | Upload image (base64)           |
| GET    | `/api/images/:id` | Get image                       |
| POST   | `/api/files`      | Upload file (base64)            |
| GET    | `/api/files/:id`  | Download file                   |

## Telegram

| Method | Path                            | Description                  |
|--------|---------------------------------|------------------------------|
| GET    | `/api/settings/telegram`        | Bot settings (admin)         |
| POST   | `/api/settings/telegram`        | Set bot token/username       |
| GET    | `/api/settings/telegram/status` | Bot setup status             |
| POST   | `/api/user/telegram/link`       | Generate linking hash        |
| POST   | `/api/user/telegram/unlink`     | Unlink Telegram              |

## Settings

| Method | Path                     | Description            |
|--------|--------------------------|------------------------|
| GET    | `/api/settings/timezone` | Current timezone       |
| POST   | `/api/settings/timezone` | Set timezone           |

## Backups (admin)

| Method | Path                   | Description              |
|--------|------------------------|--------------------------|
| GET    | `/api/backups`         | List backups             |
| POST   | `/api/backups`         | Create backup            |
| GET    | `/api/backups/{name}`  | Download backup          |
| POST   | `/api/backups/{name}`  | Restore from backup      |
| DELETE | `/api/backups/{name}`  | Delete backup            |

## Search

| Method | Path                 | Description                           |
|--------|----------------------|---------------------------------------|
| GET    | `/api/search?q=...`  | Search tasks (text / regex)           |

## Other

| Method | Path                      | Description              |
|--------|---------------------------|--------------------------|
| GET    | `/api/export`             | Export board as JSON      |
| POST   | `/api/import`             | Import board from JSON    |
| GET    | `/api/user/activity/:id`  | Activity feed             |
| POST   | `/api/user/password`      | Change own password       |

## WAP

A WML interface is available for compatibility with legacy mobile devices:

| Path              | Description        |
|-------------------|--------------------|
| `/wap/`           | Board overview     |
| `/wap/column/:id` | View column        |
| `/wap/task/:id`   | Task details       |
| `/wap/backlog`    | Backlog            |
| `/wap/login`      | Login (WAP)        |
