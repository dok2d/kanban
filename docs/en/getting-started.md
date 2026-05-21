# Getting Started

## Requirements

- **Podman** or **Docker**
- For production deployment: **nginx**, **systemd**

## Build and Run

```bash
# Build the container
./kanban.sh build

# Run (defaults to http://127.0.0.1:8080)
./kanban.sh run
```

Open your browser at `http://127.0.0.1:8080`.

## First Launch

On the first visit, you will see an administrator creation form. Enter a username and password — this will be the main account with full access.

After creating the administrator, you will land on an empty kanban board. Get started:

1. **Create columns** — e.g., "To Do", "In Progress", "Done"
2. **Add tasks** — click "+" in the desired column
3. **Drag and drop cards** between columns to track progress

## Running on a Different Port

```bash
./kanban.sh run --port 9090
```

## Management Commands

| Command                | Description                     |
|------------------------|---------------------------------|
| `./kanban.sh build`    | Build the container             |
| `./kanban.sh run`      | Start the container             |
| `./kanban.sh stop`     | Stop the container              |
| `./kanban.sh restart`  | Restart the container           |
| `./kanban.sh logs`     | View logs                       |
| `./kanban.sh backup`   | Back up the database            |
| `./kanban.sh restore`  | Restore from backup             |
| `./kanban.sh status`   | Container status                |
| `./kanban.sh deploy`   | Install systemd + nginx         |

## Next Steps

- [User Guide](user-guide.md) — how to work with the board
- [Deployment](deployment.md) — production setup with TLS
- [Authentication](authentication.md) — adding users and configuring SSO
