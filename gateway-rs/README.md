# Rust iMessage Gateway CLI

Rust rewrite of the iMessage gateway for low-latency access to `Messages.db` without running the MCP server. Mirrors the Python CLI commands with safer defaults and JSON output.

## Quick Start

```bash
# From repo root
cd gateway-rs
cargo run -- search "John" --limit 20
cargo run -- unread --json
cargo run -- send "Mom" "Happy birthday!"
```

### Configuration

- **Contacts**: Defaults to `../config/contacts.json` (relative to the repo). Override with `--contacts /path/to/contacts.json`.
- **Database**: Defaults to `~/Library/Messages/chat.db`. Override with `--database /path/to/chat.db`.

## Available Commands

| Command | Description |
|---------|-------------|
| `search <contact> [--query <text>]` | Search messages for a contact with optional text filter |
| `messages <contact>` | Get recent messages for a contact |
| `recent` | Show recent conversations across handles |
| `unread` | List unread inbound messages |
| `send <contact> <message>` | Send an iMessage via AppleScript |
| `contacts` | List contacts from `contacts.json` |
| `analytics [contact]` | Message counts and date range (optionally per contact) |
| `followup` | Identify stale inbound messages needing replies |

Add `--json` to most commands for structured output.

## Notes

- Requires macOS with `Messages.app` and access to `~/Library/Messages/chat.db`.
- Sending uses `osascript`; ensure Terminal has Automation permissions.
- The CLI performs simple fuzzy matching on contact names using Jaro-Winkler.
