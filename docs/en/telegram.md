# Telegram Bot

Telegram integration allows you to receive push notifications and manage tasks from the messenger.

## Bot Setup

### 1. Create the Bot

1. Open [@BotFather](https://t.me/BotFather) in Telegram
2. Send `/newbot` and follow the instructions
3. Copy the received token

### 2. Configure in the Application

1. Log in as administrator
2. Open **Settings → Telegram**
3. Enter the bot token and bot username (without @)
4. Save

### 3. Link Accounts

Each user links Telegram on their own:

1. In your profile, click "Link Telegram"
2. Copy the generated code
3. Send this code to the bot in Telegram
4. Account linked

To unlink, use the "Unlink Telegram" button in your profile.

## Bot Commands

| Command              | Description                                    |
|----------------------|------------------------------------------------|
| `/tasks`             | List of tasks assigned to you                  |
| `/tasks mine`        | Only tasks where you are the assignee          |
| `/task N`            | Details of task #N with recent comments        |
| `/comment N text`    | Add a comment to task #N                       |
| `/help`              | List of available commands                     |

## Notifications

The bot sends notifications when:

- A task is assigned to you
- You are @mentioned in a comment
- A new comment is added to a task you're subscribed to
- A subscribed task is updated

## Password Recovery

Users with a linked Telegram account can recover their password:

1. On the login page, click "Forgot password?"
2. Enter your username
3. The bot will send an 8-digit code
4. Enter the code and set a new password

## Image Processing

Images sent via the bot are automatically compressed:

- Adaptive JPEG compression (target size ~500 KB)
- Resized to a maximum of 1920px on the longest side
