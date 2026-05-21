# User Guide

## Board

The main screen is a kanban board with columns. Each column represents a work stage (e.g., "To Do", "In Progress", "Done"). Tasks are displayed as cards that can be dragged between columns.

### Column Management

- Administrators can create, rename, reorder, and delete columns
- The first (Backlog) and last (Done) columns cannot be deleted
- Column order is configured via drag and drop

### Filtering

Filters are available in the board header:

- **⬡ kanban** — show all tasks (no filtering)
- **Sprint** — filter by sprint (defaults to the oldest active sprint)
- **Epic** — filter by epic

## Tasks

### Creating a Task

Click "+" in the desired column. Specify:

- **Title** — brief task description
- **Description** — details in Markdown format (syntax highlighting supported)
- **Priority** — none, low, medium, high, critical
- **Deadline** — optional date, displayed on the card
- **Sprint** — assign to a sprint (or backlog)
- **Epic** — assign to an epic
- **Tags** — arbitrary labels
- **Assignee** — assigned user

### Priorities

Priority is displayed as a colored bar on the left side of the card:

| Priority | Color  |
|----------|--------|
| Low      | blue   |
| Medium   | yellow |
| High     | orange |
| Critical | red    |

### Markdown in Descriptions

Task descriptions support Markdown:

- Headings, lists, links, images
- Code blocks with syntax highlighting (highlight.js)
- **TODO lists** — interactive checkboxes (`- [ ]` / `- [x]`), can be toggled directly on the card

### Dependencies

Tasks can have dependencies:

- **Depends on** — the task cannot be completed until another is closed
- **Blocks** — this task blocks another from being completed

### Attachments

- **Images** — paste via Ctrl+V (automatically compressed to ~500 KB)
- **Files** — PDF, text files, archives, and other formats

## Sprints

Sprints help organize work into iterations.

### Sprint Statuses

1. **Planning** — sprint created, tasks being added
2. **Active** — work in progress
3. **Completed** — sprint closed

### Completing a Sprint

When a sprint is completed, unclosed tasks are automatically carried over to the next active sprint.

### Backlog

Tasks without a sprint go to the backlog — a general list of tasks awaiting planning.

## Epics

Epics are large initiatives that group multiple tasks. Each epic has:

- A title and description
- Color coding (displayed on cards)
- A progress indicator (proportion of completed tasks)

## Tags

Tags are arbitrary labels for categorizing tasks. A task can have multiple tags. Tags are searchable.

## Comments

- Threaded comments with replies
- Markdown and syntax highlighting support
- **@mentions** — mentioned users receive a notification

## Search

Search is available in the board header. Two modes are supported:

- **Plain text** — substring search
- **Regex** — regular expression search

Search works across: task titles, descriptions, comments, tags, epics.

## Notifications

Notifications are sent when:

- A task is assigned to you
- You are @mentioned in a comment
- A task you're subscribed to is updated
- A new comment is added to a subscribed task

You can subscribe to a task via the subscribe button on the card. Notifications are available in the app and via the [Telegram bot](telegram.md).

## Activity Feed

Each user has an activity feed with a detailed history of changes: task creation, status changes, comments, and other actions. The most recent 100 entries are displayed.
