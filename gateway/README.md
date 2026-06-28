# iMessage Gateway CLI

Standalone CLI for iMessage operations without running the MCP server. **19x faster** than MCP tools.

> Looking for a Rust version? See `../gateway-rs/` for a Rust rewrite of the gateway with the same core commands and JSON output.

## Performance

| Metric | MCP Server | Gateway CLI | Speedup |
|--------|------------|-------------|---------|
| **Startup** | ~723ms | ~40ms | **18x faster** |
| **Most operations** | 723ms + op | 40-85ms | **10-18x faster** |
| **Complex ops** | 850ms + op | 130ms | **6x faster** |

All 20 commands execute in under 130ms with 100% success rate.

## Quick Start

```bash
# From repo root
python3 gateway/imessage_client.py search "John" --limit 10
python3 gateway/imessage_client.py unread
python3 gateway/imessage_client.py send "Mom" "Happy birthday!"
```

## All Commands (20 total)

### Core Commands (8)

| Command | Description | Example |
|---------|-------------|---------|
| `search` | Search messages with contact | `search "John" --query "meeting"` |
| `messages` | Get conversation with contact | `messages "John" --limit 20` |
| `recent` | Recent conversations | `recent --limit 10` |
| `unread` | Unread messages | `unread` |
| `send` | Send a message | `send "John" "On my way!"` |
| `contacts` | List all contacts | `contacts --json` |
| `analytics` | Conversation stats | `analytics "Sarah" --days 30` |
| `followup` | Find messages needing reply | `followup --days 7` |

### Group Chat Commands (2)

| Command | Description | Example |
|---------|-------------|---------|
| `groups` | List all group chats | `groups --json` |
| `group-messages` | Read group messages | `group-messages --group-id "chat123"` |

### Media & Attachments (3)

| Command | Description | Example |
|---------|-------------|---------|
| `attachments` | Get photos/videos/files | `attachments --type "image/"` |
| `voice` | Get voice messages | `voice "Sarah" --limit 10` |
| `links` | Extract shared URLs | `links --days 30` |

### Reactions & Threads (2)

| Command | Description | Example |
|---------|-------------|---------|
| `reactions` | Get tapbacks/emoji reactions | `reactions --limit 50` |
| `thread` | Get reply thread | `thread --guid "msg-guid"` |

### Discovery & Management (5)

| Command | Description | Example |
|---------|-------------|---------|
| `handles` | List all phone/email handles | `handles --days 30` |
| `unknown` | Find messages from non-contacts | `unknown --days 7` |
| `scheduled` | View scheduled messages | `scheduled` |
| `summary` | AI-ready conversation summary | `summary "John" --days 7` |
| `add-contact` | Add a new contact | `add-contact "John" "+14155551234"` |

## Usage Examples

### Search Messages

```bash
# All messages with Angus
python3 gateway/imessage_client.py search "Angus"

# Messages containing "SF"
python3 gateway/imessage_client.py search "Angus" --query "SF"

# Last 50 messages
python3 gateway/imessage_client.py search "Angus" --limit 50
```

### Group Chats

```bash
# List all groups
python3 gateway/imessage_client.py groups --json

# Read group messages
python3 gateway/imessage_client.py group-messages --group-id "chat123" --limit 20
```

### Attachments & Media

```bash
# Get all images
python3 gateway/imessage_client.py attachments --type "image/"

# Voice messages from Sarah
python3 gateway/imessage_client.py voice "Sarah"

# Extract all shared links
python3 gateway/imessage_client.py links --json
```

### Discovery

```bash
# Find unknown senders (not in contacts)
python3 gateway/imessage_client.py unknown --days 7

# List all handles you've messaged
python3 gateway/imessage_client.py handles --days 30
```

### AI Integration

```bash
# Get conversation formatted for summarization
python3 gateway/imessage_client.py summary "John" --days 7 --json
```

## Contact Resolution

Contact names are fuzzy-matched from `config/contacts.json`:

- "John" → "John Doe" (first match)
- "ang" → "Angus Smith" (partial match)
- Case insensitive

## JSON Output

All commands support `--json` for structured output:

```bash
python3 gateway/imessage_client.py messages "John" --json | jq '.[] | .text'
python3 gateway/imessage_client.py groups --json | jq '.[].display_name'
```

## Claude Code Integration

The Gateway CLI is designed for fast integration with Claude Code via the Bash tool:

```bash
# In Claude Code sessions (pre-approved, zero prompts)
python3 ~/path/to/imessage-mcp/gateway/imessage_client.py unread --json
python3 ~/path/to/imessage-mcp/gateway/imessage_client.py search "Sarah" --limit 10 --json
```

**Why use Gateway CLI instead of MCP tools?**
- 19x faster execution (40ms vs 763ms)
- 80% fewer tokens (300 vs 1500 per call)
- Same reliability (100% success rate)
- Direct JSON output for easy parsing

## Benchmarks

Run the benchmark suite:

```bash
python3 gateway/benchmarks.py           # Full suite
python3 gateway/benchmarks.py --quick   # Quick check
python3 gateway/benchmarks.py --json    # JSON output
```

Latest results (20 benchmarks, 100% success):
- Startup: ~44ms
- Most operations: 40-65ms
- Complex ops (analytics, attachments): 85-130ms

## Requirements

- macOS (Messages.app integration)
- Python 3.9+
- Full Disk Access for Terminal (System Settings → Privacy → Full Disk Access)
- Contacts synced to `config/contacts.json`
