# Backup

## Database Backup

### Creating a Backup

```bash
./kanban.sh backup
```

The backup is saved to the `./backups/` directory with a timestamp in the filename.

### Restoring from Backup

```bash
./kanban.sh restore
```

The command will prompt you to select a file from `./backups/`.

## Export & Import (JSON)

### Export

Administrators can export the entire board as JSON via the UI or API:

```
GET /api/export
```

The export includes:

- Columns and their order
- Tasks with descriptions, priorities, deadlines
- Sprints, epics, tags
- Comments
- Users (optionally with password hashes)
- Settings
- Files and images (optional)

### Import

```
POST /api/import
```

Import restores the board from a previously exported JSON file. All existing data will be replaced.

When restoring from a backup (via API or UI), the system automatically creates a safety copy of the current state — a `pre-restore-{timestamp}.json` file. This allows you to roll back if the restore goes wrong.

## Automatic Backups via Telegram

If a Telegram bot is configured, the server automatically sends a daily backup to all administrators with a linked Telegram account. The backup is sent at **18:00** in the administrator's timezone (`admin_timezone` setting, defaults to UTC).

Details:
- The backup is sent as a JSON file (`kanban-backup-YYYY-MM-DD.json`) via the Telegram bot
- If data hasn't changed since the last backup, the send is skipped
- Orphaned files and images are automatically cleaned up before each backup
- Requirements for auto-backup: a configured Telegram bot and at least one administrator with a linked Telegram account

## Orphaned File Cleanup

Before each backup (manual and automatic), the system automatically deletes uploaded files and images that are no longer linked to any task or comment.

## Recommendations

- Make regular backups, especially before updates
- Store backup copies off-server
- JSON export is convenient for migrating between servers
- SQLite uses WAL mode — backups are safe while the server is running
