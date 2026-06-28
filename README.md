# iMessage Gateway CLI

![macOS](https://img.shields.io/badge/macOS-only-blue?logo=apple)
![Python](https://img.shields.io/badge/Python-3.9+-green?logo=python)
![License](https://img.shields.io/badge/License-MIT-yellow)
![Claude Code](https://img.shields.io/badge/Claude%20Code-compatible-purple)

**The fastest iMessage integration for Claude Code.** Direct CLI architecture delivers 19x faster performance than MCP-based alternatives.

## Features

- **Send Messages**: Send iMessages using natural language
- **Read Messages**: Retrieve message history with contacts or phone numbers
- **Smart Contact Lookup**: Fuzzy matching for contact names
- **Semantic Search (RAG)**: AI-powered search across iMessages, SuperWhisper, Notes
- **Follow-up Detection**: Find conversations needing response
- **Group Chats**: List and read group conversations
- **Analytics**: Conversation patterns and statistics

## Performance

| Operation | Gateway CLI | MCP-based | Speedup |
|-----------|-------------|-----------|---------|
| List contacts | 40ms | ~763ms | **19x** |
| Find messages | 43ms | ~763ms | **18x** |
| Unread messages | 44ms | ~763ms | **17x** |
| Groups | 61ms | ~763ms | **12x** |
| Semantic search | 150ms | ~900ms | **6x** |

## Requirements

- **macOS** (required - iMessage is macOS only)
- **Python 3.9+**
- **Full Disk Access** permission (for reading message history)

## Quick Start

### 1. Clone and Install

```bash
git clone https://github.com/yourusername/imessage-gateway.git
cd imessage-gateway
pip install -r requirements.txt
```

### 2. Set Up Contacts

```bash
# Sync from macOS Contacts (recommended)
python3 scripts/sync_contacts.py

# Or manual setup
cp config/contacts.example.json config/contacts.json
# Edit config/contacts.json with your contacts
```

### 3. Grant Permissions

1. **Full Disk Access** (for reading messages):
   - System Settings → Privacy & Security → Full Disk Access
   - Add Terminal.app or your Python interpreter

2. **Automation** (for sending messages):
   - Will be requested automatically on first send

### 4. Test It Out

```bash
# List your contacts
python3 gateway/imessage_client.py contacts --json

# Check unread messages
python3 gateway/imessage_client.py unread --json

# Get recent messages
python3 gateway/imessage_client.py recent --limit 20 --json

# Send a message
python3 gateway/imessage_client.py send "John" "Hey, are you free for coffee?"
```

## Command Reference (27 Commands)

### Messaging (3)

```bash
# Send to contact
python3 gateway/imessage_client.py send "John" "Hello!"

# Send to phone number directly
python3 gateway/imessage_client.py send-by-phone "+14155551234" "Hi there!"

# Add a new contact
python3 gateway/imessage_client.py add-contact "Jane Doe" "+14155559876"
```

### Reading (12)

```bash
# Messages with a contact
python3 gateway/imessage_client.py messages "John" --limit 20 --json

# Find messages (keyword search)
python3 gateway/imessage_client.py find "John" --query "meeting" --json

# Recent across all contacts
python3 gateway/imessage_client.py recent --limit 50 --json

# Unread messages
python3 gateway/imessage_client.py unread --json

# Recent phone handles
python3 gateway/imessage_client.py handles --days 30 --json

# Messages from unknown senders
python3 gateway/imessage_client.py unknown --days 7 --json

# Attachments
python3 gateway/imessage_client.py attachments "John" --json

# Voice messages
python3 gateway/imessage_client.py voice --json

# Links shared
python3 gateway/imessage_client.py links --days 30 --json

# Message thread
python3 gateway/imessage_client.py thread "<message-guid>" --json

# Scheduled messages
python3 gateway/imessage_client.py scheduled --json

# Conversation summary
python3 gateway/imessage_client.py summary "John" --days 7 --json
```

### Groups (2)

```bash
# List group chats
python3 gateway/imessage_client.py groups --json

# Messages from a group
python3 gateway/imessage_client.py group-messages --group-id "chat123456" --json
```

### Analytics (3)

```bash
# Conversation analytics
python3 gateway/imessage_client.py analytics "John" --days 30 --json

# Follow-ups needed
python3 gateway/imessage_client.py followup --days 7 --json

# Reactions
python3 gateway/imessage_client.py reactions "John" --json
```

### Contacts (1)

```bash
# List all contacts
python3 gateway/imessage_client.py contacts --json
```

### Semantic Search / RAG (6)

```bash
# Index iMessages for semantic search
python3 gateway/imessage_client.py index --source=imessage --days 30

# Index SuperWhisper transcriptions
python3 gateway/imessage_client.py index --source=superwhisper

# Index Notes
python3 gateway/imessage_client.py index --source=notes

# Index all local sources
python3 gateway/imessage_client.py index --source=local

# Semantic search
python3 gateway/imessage_client.py search "dinner plans with Sarah" --json

# AI-formatted context
python3 gateway/imessage_client.py ask "What restaurant did Sarah recommend?"

# Knowledge base stats
python3 gateway/imessage_client.py stats --json

# List available/indexed sources
python3 gateway/imessage_client.py sources --json

# Clear indexed data
python3 gateway/imessage_client.py clear --source=imessage --force
```

## Architecture

```
Messages.db (SQLite) ←─ ~/Library/Messages/chat.db
        ↓
MessagesInterface ────→ SQLite queries (read)
        │                AppleScript → Messages.app (send)
        ↓
ContactsManager ──────→ config/contacts.json (fuzzy matching)
        ↓
Gateway CLI ──────────→ gateway/imessage_client.py
        ↓
Claude Code ──────────→ Bash tool calls
```

### Why Gateway CLI?

The Gateway CLI architecture bypasses MCP framework overhead entirely:

- **Direct execution**: Python script runs immediately, no JSON-RPC initialization
- **No session startup**: MCP servers have ~700-800ms cold start per session
- **Smaller footprint**: No MCP SDK dependency, simpler codebase
- **Same reliability**: Uses identical `MessagesInterface` code for all operations

## Claude Code Integration

### Using the Skill

The `imessage-gateway` skill provides natural language access:

```
/imessage-gateway unread
/imessage-gateway send John "Running late!"
/imessage-gateway search "meeting next week"
```

### Bash Pre-approval

Ensure your settings include:
```
Bash(python3:*::*)
```

## Configuration

### Contact Format

Contacts are stored in `config/contacts.json`:

```json
{
  "contacts": [
    {
      "name": "John Doe",
      "phone": "14155551234",
      "relationship_type": "friend",
      "notes": "Optional notes"
    }
  ]
}
```

Phone numbers can be in any format - they're normalized automatically.

## Troubleshooting

### "Contact not found"
- Run `python3 scripts/sync_contacts.py` to sync contacts
- Check `config/contacts.json` exists and has contacts
- Try partial names (e.g., "John" instead of "John Doe")

### "Permission denied" reading messages
- Grant Full Disk Access to Terminal/Python
- Restart Terminal after granting permission
- Verify: `ls ~/Library/Messages/chat.db`

### Messages show "[message content not available]"
- Some older messages use a different format
- Attachment-only messages don't have text content
- This is normal for some message types

## Rust Gateway (Experimental)

A Rust rewrite of the gateway CLI lives in `gateway-rs/` with the same core commands and JSON output.

```bash
# Run from repo root
cargo run --manifest-path gateway-rs/Cargo.toml -- --help

# Examples
cargo run --manifest-path gateway-rs/Cargo.toml -- unread --json --limit 5
cargo run --manifest-path gateway-rs/Cargo.toml -- search "John" --limit 10
cargo run --manifest-path gateway-rs/Cargo.toml -- send "Mom" "On my way!"
```

Use `--contacts` to point at a custom `contacts.json` and `--database` to override the default `~/Library/Messages/chat.db` path.

## Development

```bash
# Run tests
pytest tests/ -v

# Run performance benchmarks
python3 -m Texting.benchmarks.run_benchmarks

# Sync contacts
python3 scripts/sync_contacts.py
```

## Privacy & Security

- All data stays local on your Mac
- No cloud services for core functionality
- Contacts file is gitignored by default
- Message history accessed read-only
- Optional OpenAI API for semantic search embeddings

## MCP Server (Archived)

The MCP server has been archived in `mcp_server_archive/`. See `mcp_server_archive/ARCHIVED.md` for restoration instructions if needed.

## License

MIT License - see [LICENSE](LICENSE) for details.

---

*Built for use with [Claude Code](https://claude.ai/code)*
