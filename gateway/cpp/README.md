# iMessage Gateway (C++)

A native C++ reimplementation of the lightweight gateway client. It mirrors the Python CLI commands while depending only on the macOS Messages database (`~/Library/Messages/chat.db`) and the existing `config/contacts.json` file for contact resolution.

## Build

```bash
cd gateway/cpp
cmake -B build -S .
cmake --build build
```

Dependencies:

- C++17 toolchain
- CMake 3.16+
- SQLite3 development headers/libraries (macOS ships these by default)

## Usage

All commands must be run from the build output directory (or provide the full path to the binary):

```bash
./imessage_gateway contacts --json
./imessage_gateway messages "John" --limit 10
./imessage_gateway search "Jane" --query "coffee"
./imessage_gateway send "Mom" "Hello from C++!"
./imessage_gateway analytics "Alex" --days 14 --json
./imessage_gateway followup --days 7 --stale 3
```

Flags:

- `--json` prints machine-readable output for commands that support it.
- `--limit` controls result limits for `messages`, `search`, `recent`, `unread`, and `followup`.
- `--days`/`--stale` tune the analytics and follow-up lookback windows.

The binary searches upward from its location to find the repository root (looking for `config/` and `src/`) and loads contacts from `config/contacts.json`. Messages are read directly from `~/Library/Messages/chat.db`.
