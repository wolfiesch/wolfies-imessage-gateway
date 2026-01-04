#!/usr/bin/env python3
"""
iMessage Gateway Client - Standalone CLI for iMessage operations.

No MCP server required. Queries Messages.db directly and uses
the imessage-mcp library for contact resolution and message parsing.

This is the primary interface for iMessage operations, offering 19x faster
execution than the deprecated MCP server approach.

Usage:
    python3 gateway/imessage_client.py find "Angus" --query "SF"
    python3 gateway/imessage_client.py messages "John" --limit 20
    python3 gateway/imessage_client.py recent --limit 10
    python3 gateway/imessage_client.py unread
    python3 gateway/imessage_client.py send "John" "Running late!"
    python3 gateway/imessage_client.py send-by-phone +14155551234 "Hi"
    python3 gateway/imessage_client.py contacts
    python3 gateway/imessage_client.py analytics "Sarah" --days 30
    python3 gateway/imessage_client.py search "dinner plans"    # Semantic search (RAG)
    python3 gateway/imessage_client.py index --source=imessage  # Index for RAG
"""

import sys
import argparse
import json
import io
from contextlib import redirect_stdout, redirect_stderr
from pathlib import Path

# Add parent directory to path for imports
SCRIPT_DIR = Path(__file__).parent
REPO_ROOT = SCRIPT_DIR.parent
sys.path.insert(0, str(REPO_ROOT))

try:
    from src.messages_interface import MessagesInterface
    from src.contacts_manager import ContactsManager
except ImportError as e:
    print(f"Error: Could not import modules: {e}")
    print(f"Make sure you're running from the imessage-mcp repository root")
    print(f"Expected path: {REPO_ROOT}")
    sys.exit(1)

# Default config path (relative to repo root)
CONTACTS_CONFIG = REPO_ROOT / "config" / "contacts.json"

# Valid RAG sources (single source of truth)
VALID_RAG_SOURCES = ['imessage', 'superwhisper', 'notes', 'local', 'gmail', 'slack', 'calendar']


def get_interfaces():
    """Initialize MessagesInterface and ContactsManager."""
    mi = MessagesInterface()
    cm = ContactsManager(str(CONTACTS_CONFIG))
    return mi, cm


def resolve_contact(cm: ContactsManager, name: str):
    """Resolve contact name to Contact object using fuzzy matching."""
    contact = cm.get_contact_by_name(name)
    # get_contact_by_name already does partial matching
    if contact and contact.name.lower() != name.lower():
        print(f"Matched '{name}' to '{contact.name}'", file=sys.stderr)
    return contact


def cmd_find(args):
    """Find messages with a contact (keyword search)."""
    mi, cm = get_interfaces()
    contact = resolve_contact(cm, args.contact)

    if not contact:
        print(f"Contact '{args.contact}' not found.", file=sys.stderr)
        print("Available contacts:", ", ".join(c.name for c in cm.contacts[:10]), file=sys.stderr)
        return 1

    # Use efficient database-level search when query provided
    if args.query:
        messages = mi.search_messages(query=args.query, phone=contact.phone, limit=args.limit)
    else:
        messages = mi.get_messages_by_phone(contact.phone, limit=args.limit)

    if args.json:
        print(json.dumps(messages, indent=2, default=str))
    else:
        print(f"Messages with {contact.name} ({contact.phone}):")
        print("-" * 60)

        for m in messages:
            sender = "Me" if m.get('is_from_me') else contact.name
            text = m.get('text', '[media/attachment]') or '[media/attachment]'
            timestamp = m.get('timestamp', '')
            print(f"{timestamp} | {sender}: {text[:200]}")

    return 0


def cmd_messages(args):
    """Get messages with a specific contact."""
    mi, cm = get_interfaces()
    contact = resolve_contact(cm, args.contact)

    if not contact:
        print(f"Contact '{args.contact}' not found.", file=sys.stderr)
        return 1

    messages = mi.get_messages_by_phone(contact.phone, limit=args.limit)

    if args.json:
        print(json.dumps(messages, indent=2, default=str))
    else:
        if not messages:
            print("No messages found.")
            return 0

        for m in messages:
            sender = "Me" if m.get('is_from_me') else contact.name
            text = m.get('text', '[media]') or '[media]'
            print(f"{sender}: {text[:200]}")

    return 0


def cmd_recent(args):
    """Get recent conversations across all contacts."""
    mi, _ = get_interfaces()

    conversations = mi.get_all_recent_conversations(limit=args.limit)

    if args.json:
        print(json.dumps(conversations, indent=2, default=str))
    else:
        if not conversations:
            print("No recent conversations found.")
            return 0

        print("Recent Conversations:")
        print("-" * 60)
        for conv in conversations:
            handle = conv.get('handle_id', 'Unknown')
            last_msg = conv.get('last_message', '')[:80]
            timestamp = conv.get('last_message_date', '')
            print(f"{handle}: {last_msg} ({timestamp})")

    return 0


def cmd_unread(args):
    """Get unread messages."""
    mi, _ = get_interfaces()

    messages = mi.get_unread_messages(limit=args.limit)

    if args.json:
        print(json.dumps(messages, indent=2, default=str))
    else:
        if not messages:
            print("No unread messages.")
            return 0

        print(f"Unread Messages ({len(messages)}):")
        print("-" * 60)
        for m in messages:
            sender = m.get('sender', 'Unknown')
            text = m.get('text', '[media]') or '[media]'
            print(f"{sender}: {text[:150]}")

    return 0


def cmd_send(args):
    """Send a message to a contact."""
    mi, cm = get_interfaces()
    contact = resolve_contact(cm, args.contact)

    if not contact:
        print(f"Contact '{args.contact}' not found.", file=sys.stderr)
        return 1

    message = " ".join(args.message)

    print(f"Sending to {contact.name} ({contact.phone}): {message[:50]}...", file=sys.stderr)
    result = mi.send_message(contact.phone, message)

    if result.get('success'):
        print("Message sent successfully.", file=sys.stderr)
        return 0
    else:
        print(f"Failed to send: {result.get('error', 'Unknown error')}", file=sys.stderr)
        return 1


def cmd_send_by_phone(args):
    """Send a message directly to a phone number (no contact lookup)."""
    mi, _ = get_interfaces()

    # Normalize phone number (strip formatting characters)
    phone = args.phone.strip().translate(str.maketrans('', '', ' ()-.'))


    message = " ".join(args.message)

    print(f"Sending to {phone}: {message[:50]}...", file=sys.stderr)
    result = mi.send_message(phone, message)

    if result.get('success'):
        if args.json:
            print(json.dumps({"success": True, "phone": phone, "message": message}))
        else:
            print("Message sent successfully.", file=sys.stderr)
        return 0
    else:
        error = result.get('error', 'Unknown error')
        if args.json:
            print(json.dumps({"success": False, "phone": phone, "error": error}))
        else:
            print(f"Failed to send: {error}", file=sys.stderr)
        return 1


def cmd_contacts(args):
    """List all contacts."""
    _, cm = get_interfaces()

    if args.json:
        print(json.dumps([c.to_dict() for c in cm.contacts], indent=2))
    else:
        print(f"Contacts ({len(cm.contacts)}):")
        print("-" * 40)
        for c in cm.contacts:
            print(f"{c.name}: {c.phone}")

    return 0


def cmd_analytics(args):
    """Get conversation analytics for a contact."""
    mi, cm = get_interfaces()

    if args.contact:
        contact = resolve_contact(cm, args.contact)
        if not contact:
            print(f"Contact '{args.contact}' not found.", file=sys.stderr)
            return 1
        analytics = mi.get_conversation_analytics(contact.phone, days=args.days)
    else:
        analytics = mi.get_conversation_analytics(days=args.days)

    if args.json:
        print(json.dumps(analytics, indent=2, default=str))
    else:
        print("Conversation Analytics:")
        print("-" * 40)
        for key, value in analytics.items():
            print(f"{key}: {value}")

    return 0


def cmd_followup(args):
    """Detect messages needing follow-up."""
    mi, cm = get_interfaces()

    followups = mi.detect_follow_up_needed(days=args.days, min_stale_days=args.stale)

    if args.json:
        print(json.dumps(followups, indent=2, default=str))
    else:
        # Check if there are any action items
        summary = followups.get("summary", {})
        total_items = summary.get("total_action_items", 0) if summary else 0

        # Also count items across categories if no summary
        if not total_items:
            for key, items in followups.items():
                if key not in ("summary", "analysis_period_days") and isinstance(items, list):
                    total_items += len(items)

        if not total_items:
            print("No follow-ups needed.")
            return 0

        print("Follow-ups Needed:")
        print("-" * 60)

        # Iterate through categories (skip metadata keys)
        for category, items in followups.items():
            if category in ("summary", "analysis_period_days"):
                continue
            if not items or not isinstance(items, list):
                continue

            print(f"\n--- {category.replace('_', ' ').title()} ---")
            for item in items:
                phone = item.get('phone')
                contact = cm.get_contact_by_phone(phone) if phone else None
                name = contact.name if contact else phone or "Unknown"
                text = item.get('text') or item.get('last_message', '')
                date = item.get('date', '')
                print(f"  {name}: {text[:100]} ({date})")

    return 0


# =============================================================================
# T0 COMMANDS - Core Features
# =============================================================================


def cmd_groups(args):
    """List all group chats."""
    mi, _ = get_interfaces()

    groups = mi.list_group_chats(limit=args.limit)

    if args.json:
        print(json.dumps(groups, indent=2, default=str))
    else:
        if not groups:
            print("No group chats found.")
            return 0

        print(f"Group Chats ({len(groups)}):")
        print("-" * 60)
        for g in groups:
            name = g.get('display_name') or g.get('group_id', 'Unknown')
            participants = g.get('participant_count', 0)
            msg_count = g.get('message_count', 0)
            print(f"{name} ({participants} members, {msg_count} messages)")
            print(f"  ID: {g.get('group_id', 'N/A')}")

    return 0


def cmd_group_messages(args):
    """Get messages from a group chat."""
    mi, _ = get_interfaces()

    if not args.group_id and not args.participant:
        print("Error: Must provide --group-id or --participant", file=sys.stderr)
        return 1

    messages = mi.get_group_messages(
        group_id=args.group_id,
        participant_filter=args.participant,
        limit=args.limit
    )

    if args.json:
        print(json.dumps(messages, indent=2, default=str))
    else:
        if not messages:
            print("No group messages found.")
            return 0

        print(f"Group Messages ({len(messages)}):")
        print("-" * 60)
        for m in messages:
            sender = "Me" if m.get('is_from_me') else m.get('sender_handle', 'Unknown')
            text = m.get('text', '[media]') or '[media]'
            date = m.get('date', '')
            print(f"[{date}] {sender}: {text[:150]}")

    return 0


def cmd_attachments(args):
    """Get attachments (photos, videos, files) from messages."""
    mi, cm = get_interfaces()

    phone = None
    if args.contact:
        contact = resolve_contact(cm, args.contact)
        if not contact:
            print(f"Contact '{args.contact}' not found.", file=sys.stderr)
            return 1
        phone = contact.phone

    attachments = mi.get_attachments(
        phone=phone,
        mime_type_filter=args.type,
        limit=args.limit
    )

    if args.json:
        print(json.dumps(attachments, indent=2, default=str))
    else:
        if not attachments:
            print("No attachments found.")
            return 0

        print(f"Attachments ({len(attachments)}):")
        print("-" * 60)
        for a in attachments:
            filename = a.get('filename') or a.get('transfer_name', 'Unknown')
            mime = a.get('mime_type', 'unknown')
            size = a.get('total_bytes', 0)
            size_str = f"{size / 1024:.1f}KB" if size else "N/A"
            date = a.get('message_date', '')
            print(f"{filename} ({mime}, {size_str}) - {date}")

    return 0


def cmd_add_contact(args):
    """Add a new contact."""
    _, cm = get_interfaces()

    try:
        cm.add_contact(
            name=args.name,
            phone=args.phone,
            relationship_type=args.relationship,
            notes=args.notes
        )
        print(f"Contact '{args.name}' added successfully.")
        return 0
    except Exception as e:
        print(f"Failed to add contact: {e}", file=sys.stderr)
        return 1


# =============================================================================
# T1 COMMANDS - Advanced Features
# =============================================================================


def cmd_reactions(args):
    """Get reactions (tapbacks) from messages."""
    mi, cm = get_interfaces()

    phone = None
    if args.contact:
        contact = resolve_contact(cm, args.contact)
        if not contact:
            print(f"Contact '{args.contact}' not found.", file=sys.stderr)
            return 1
        phone = contact.phone

    reactions = mi.get_reactions(phone=phone, limit=args.limit)

    if args.json:
        print(json.dumps(reactions, indent=2, default=str))
    else:
        if not reactions:
            print("No reactions found.")
            return 0

        print(f"Reactions ({len(reactions)}):")
        print("-" * 60)
        for r in reactions:
            emoji = r.get('reaction_emoji', '?')
            reactor = "Me" if r.get('is_from_me') else r.get('reactor_handle', 'Unknown')
            original = r.get('original_message_preview', '')[:50]
            date = r.get('date', '')
            print(f"{emoji} by {reactor} on \"{original}...\" ({date})")

    return 0


def cmd_links(args):
    """Extract URLs shared in conversations."""
    mi, cm = get_interfaces()

    phone = None
    if args.contact:
        contact = resolve_contact(cm, args.contact)
        if not contact:
            print(f"Contact '{args.contact}' not found.", file=sys.stderr)
            return 1
        phone = contact.phone

    days = None if getattr(args, "all_time", False) else (args.days if args.days is not None else 30)
    links = mi.extract_links(phone=phone, days=days, limit=args.limit)

    if args.json:
        print(json.dumps(links, indent=2, default=str))
    else:
        if not links:
            print("No links found.")
            return 0

        print(f"Shared Links ({len(links)}):")
        print("-" * 60)
        for link in links:
            url = link.get('url', 'N/A')
            sender = "Me" if link.get('is_from_me') else link.get('sender_handle', 'Unknown')
            date = link.get('date', '')
            print(f"{url}")
            print(f"  From: {sender} ({date})")

    return 0


def cmd_voice(args):
    """Get voice messages with file paths."""
    mi, cm = get_interfaces()

    phone = None
    if args.contact:
        contact = resolve_contact(cm, args.contact)
        if not contact:
            print(f"Contact '{args.contact}' not found.", file=sys.stderr)
            return 1
        phone = contact.phone

    voice_msgs = mi.get_voice_messages(phone=phone, limit=args.limit)

    if args.json:
        print(json.dumps(voice_msgs, indent=2, default=str))
    else:
        if not voice_msgs:
            print("No voice messages found.")
            return 0

        print(f"Voice Messages ({len(voice_msgs)}):")
        print("-" * 60)
        for v in voice_msgs:
            path = v.get('attachment_path', 'N/A')
            sender = "Me" if v.get('is_from_me') else v.get('sender_handle', 'Unknown')
            size = v.get('size_bytes', 0)
            size_str = f"{size / 1024:.1f}KB" if size else "N/A"
            date = v.get('date', '')
            print(f"{path}")
            print(f"  From: {sender}, Size: {size_str}, Date: {date}")

    return 0


def cmd_thread(args):
    """Get messages in a reply thread."""
    mi, _ = get_interfaces()

    if not args.guid:
        print("Error: Must provide --guid for message thread", file=sys.stderr)
        return 1

    thread = mi.get_message_thread(message_guid=args.guid, limit=args.limit)

    if args.json:
        print(json.dumps(thread, indent=2, default=str))
    else:
        if not thread:
            print("No thread messages found.")
            return 0

        print(f"Thread Messages ({len(thread)}):")
        print("-" * 60)
        for m in thread:
            sender = "Me" if m.get('is_from_me') else m.get('sender_handle', 'Unknown')
            text = m.get('text', '[media]') or '[media]'
            date = m.get('date', '')
            is_originator = " [THREAD START]" if m.get('is_thread_originator') else ""
            print(f"[{date}] {sender}: {text[:150]}{is_originator}")

    return 0


# =============================================================================
# T2 COMMANDS - Discovery Features
# =============================================================================


def cmd_handles(args):
    """List all unique phone/email handles from recent messages."""
    mi, _ = get_interfaces()

    handles = mi.list_recent_handles(days=args.days, limit=args.limit)

    if args.json:
        print(json.dumps(handles, indent=2, default=str))
    else:
        if not handles:
            print("No handles found.")
            return 0

        print(f"Recent Handles ({len(handles)}):")
        print("-" * 60)
        for h in handles:
            handle = h.get('handle', 'Unknown')
            msg_count = h.get('message_count', 0)
            last_date = h.get('last_message_date', '')
            print(f"{handle} ({msg_count} messages, last: {last_date})")

    return 0


def cmd_unknown(args):
    """Find messages from senders not in contacts."""
    mi, cm = get_interfaces()

    known_phones = [c.phone for c in cm.contacts]
    unknown = mi.search_unknown_senders(
        known_phones=known_phones,
        days=args.days,
        limit=args.limit
    )

    if args.json:
        print(json.dumps(unknown, indent=2, default=str))
    else:
        if not unknown:
            print("No unknown senders found.")
            return 0

        print(f"Unknown Senders ({len(unknown)}):")
        print("-" * 60)
        for u in unknown:
            handle = u.get('handle', 'Unknown')
            msg_count = u.get('message_count', 0)
            last_date = u.get('last_message_date', '')
            print(f"{handle} ({msg_count} messages, last: {last_date})")
            # Show sample messages if available
            messages = u.get('messages', [])
            for msg in messages[:2]:
                text = msg.get('text', '')[:80] if msg.get('text') else '[media]'
                print(f"  \"{text}\"")

    return 0


def cmd_scheduled(args):
    """Get scheduled messages (pending sends)."""
    mi, _ = get_interfaces()

    scheduled = mi.get_scheduled_messages()

    if args.json:
        print(json.dumps(scheduled, indent=2, default=str))
    else:
        if not scheduled:
            print("No scheduled messages.")
            return 0

        print(f"Scheduled Messages ({len(scheduled)}):")
        print("-" * 60)
        for s in scheduled:
            text = s.get('text', '[media]') or '[media]'
            recipient = s.get('recipient_handle', 'Unknown')
            sched_date = s.get('scheduled_date', 'N/A')
            print(f"To: {recipient}")
            print(f"  Message: {text[:100]}")
            print(f"  Scheduled for: {sched_date}")

    return 0


def cmd_summary(args):
    """Get conversation formatted for AI summarization."""
    mi, cm = get_interfaces()

    contact = resolve_contact(cm, args.contact)
    if not contact:
        print(f"Contact '{args.contact}' not found.", file=sys.stderr)
        return 1

    summary = mi.get_conversation_for_summary(
        phone=contact.phone,
        days=args.days,
        limit=args.limit
    )

    if args.json:
        print(json.dumps(summary, indent=2, default=str))
    else:
        print(f"Conversation Summary: {contact.name}")
        print("-" * 60)
        stats = summary.get('key_stats', {})
        print(f"Messages: {summary.get('message_count', 0)}")
        print(f"Date range: {summary.get('date_range', 'N/A')}")
        print(f"Last interaction: {summary.get('last_interaction', 'N/A')}")
        if stats:
            print(f"Sent: {stats.get('sent', 0)}, Received: {stats.get('received', 0)}")
        topics = summary.get('recent_topics', [])
        if topics:
            print(f"Recent topics: {', '.join(topics[:5])}")
        print("\n--- Conversation Text ---")
        print(summary.get('conversation_text', '')[:2000])
        if len(summary.get('conversation_text', '')) > 2000:
            print("... (truncated, use --json for full output)")

    return 0


# =============================================================================
# RAG COMMANDS - Semantic Search & Knowledge Base
# =============================================================================


def get_unified_retriever():
    """Get UnifiedRetriever instance (lazy import for faster startup)."""
    from src.rag.unified import UnifiedRetriever
    return UnifiedRetriever()


def cmd_index(args):
    """Index content from a source for semantic search."""
    import time
    start = time.time()

    source = args.source.lower()

    if source not in VALID_RAG_SOURCES:
        print(f"Error: Unknown source '{source}'", file=sys.stderr)
        print(f"Valid sources: {', '.join(VALID_RAG_SOURCES)}", file=sys.stderr)
        return 1

    try:
        if source == 'imessage':
            # iMessage needs MessagesInterface and ContactsManager
            mi, cm = get_interfaces()
            from src.rag.unified import UnifiedRetriever
            from src.rag.unified.imessage_indexer import ImessageIndexer

            retriever = UnifiedRetriever()
            indexer = ImessageIndexer(
                messages_interface=mi,
                contacts_manager=cm,
                store=retriever.store,
            )
            result = indexer.index(
                days=args.days,
                limit=args.limit,
                contact_name=args.contact,
                incremental=not args.full,
            )
        elif source == 'superwhisper':
            retriever = get_unified_retriever()
            result = retriever.index_superwhisper(days=args.days, limit=args.limit)
        elif source == 'notes':
            retriever = get_unified_retriever()
            result = retriever.index_notes(days=args.days, limit=args.limit)
        elif source == 'local':
            retriever = get_unified_retriever()
            result = retriever.index_local_sources(days=args.days)
        else:
            # Gmail, Slack, Calendar require pre-fetched data
            print(f"Error: Source '{source}' requires pre-fetched data.", file=sys.stderr)
            print("Use the appropriate MCP tools to fetch data first, then pass to the indexer.", file=sys.stderr)
            return 1

        elapsed = time.time() - start

        if args.json:
            result['elapsed_seconds'] = elapsed
            print(json.dumps(result, indent=2, default=str))
        else:
            chunks_indexed = result.get('chunks_indexed', 0)
            chunks_found = result.get('chunks_found', chunks_indexed)
            print(f"✓ Indexed {source}")
            print(f"  Chunks found: {chunks_found}")
            print(f"  Chunks indexed: {chunks_indexed}")
            print(f"  Duration: {elapsed:.1f}s")

            if source == 'local':
                by_source = result.get('by_source', {})
                for src, info in by_source.items():
                    print(f"  - {src}: {info.get('chunks_indexed', 0)} chunks")

        return 0

    except Exception as e:
        print(f"Error indexing {source}: {e}", file=sys.stderr)
        return 1


def cmd_search(args):
    """Semantic search across indexed knowledge base."""
    try:
        retriever = get_unified_retriever()

        # Parse sources if provided
        sources = None
        if args.sources:
            sources = [s.strip() for s in args.sources.split(',')]

        results = retriever.search(
            query=args.query,
            sources=sources,
            limit=args.limit,
            days=args.days,
        )

        if args.json:
            print(json.dumps(results, indent=2, default=str))
        else:
            if not results:
                print(f'No results found for: "{args.query}"')
                return 0

            print(f'Found {len(results)} result(s) for: "{args.query}"')
            print("-" * 60)

            for i, result in enumerate(results, 1):
                score = result.get('score', 0) * 100
                source = result.get('source', 'unknown')
                title = result.get('title') or result.get('context_id', '')[:30]
                timestamp = result.get('timestamp', '')[:10] if result.get('timestamp') else ''
                text = result.get('text', '')[:200]

                print(f"\n[{i}] [{source}] {title} | {timestamp} | {score:.0f}% match")
                print(f"    {text}...")

        return 0

    except Exception as e:
        print(f"Error searching: {e}", file=sys.stderr)
        return 1


def cmd_ask(args):
    """Get AI-formatted context from knowledge base."""
    try:
        retriever = get_unified_retriever()

        # Parse sources if provided
        sources = None
        if args.sources:
            sources = [s.strip() for s in args.sources.split(',')]

        context = retriever.ask(
            question=args.question,
            sources=sources,
            limit=args.limit,
            days=args.days,
        )

        if args.json:
            print(json.dumps({"question": args.question, "context": context}, indent=2))
        else:
            print(context)

        return 0

    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1


def cmd_stats(args):
    """Show statistics about the indexed knowledge base."""
    try:
        retriever = get_unified_retriever()
        stats = retriever.get_stats(source=args.source)

        if args.json:
            print(json.dumps(stats, indent=2, default=str))
        else:
            total = stats.get('total_chunks', 0)
            if total == 0:
                print("Knowledge base is empty.")
                print("\nRun 'index --source=<source>' to start indexing:")
                print("  index --source=imessage      Index iMessage conversations")
                print("  index --source=superwhisper  Index voice transcriptions")
                print("  index --source=notes         Index markdown notes")
                print("  index --source=local         Index all local sources")
                return 0

            print("Knowledge Base Statistics")
            print("=" * 40)
            print(f"Total chunks indexed: {total}")
            print(f"Unique participants: {stats.get('unique_participants', 0)}")
            print(f"Unique tags: {stats.get('unique_tags', 0)}")

            by_source = stats.get('by_source', {})
            if by_source:
                print("\nBy Source:")
                for src, info in sorted(by_source.items()):
                    count = info.get('chunk_count', 0)
                    if count > 0:
                        oldest = info.get('oldest', 'N/A')[:10] if info.get('oldest') else 'N/A'
                        newest = info.get('newest', 'N/A')[:10] if info.get('newest') else 'N/A'
                        print(f"  {src}: {count} chunks ({oldest} to {newest})")

        return 0

    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1


def cmd_clear(args):
    """Clear indexed data from the knowledge base."""
    try:
        retriever = get_unified_retriever()

        # Get current stats for confirmation
        stats = retriever.get_stats(source=args.source)
        total = stats.get('total_chunks', 0)

        if total == 0:
            print("Nothing to clear - knowledge base is empty.")
            return 0

        if not args.force:
            source_msg = f" for source '{args.source}'" if args.source else ""
            print(f"About to delete {total} chunks{source_msg}.")
            print("Use --force to confirm deletion.")
            return 1

        deleted = retriever.clear(source=args.source)

        if args.json:
            print(json.dumps({"deleted_chunks": deleted, "source": args.source or "all"}, indent=2))
        else:
            print(f"✓ Deleted {deleted} chunks")

        return 0

    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1


def cmd_sources(args):
    """List available and indexed sources."""
    try:
        retriever = get_unified_retriever()

        available = retriever.list_sources()
        indexed = retriever.get_indexed_sources()
        stats = retriever.get_stats()
        by_source = stats.get('by_source', {})

        if args.json:
            result = {
                "available": available,
                "indexed": indexed,
                "details": {
                    src: by_source.get(src, {}).get('chunk_count', 0)
                    for src in available
                }
            }
            print(json.dumps(result, indent=2))
        else:
            print("Available Sources:")
            print("-" * 40)
            for src in available:
                count = by_source.get(src, {}).get('chunk_count', 0)
                status = f"({count} chunks)" if count > 0 else "(not indexed)"
                marker = "✓" if src in indexed else " "
                print(f"  {marker} {src} {status}")

            print("\nTo index a source:")
            print("  index --source=<source> [--days=30]")

        return 0

    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1


def main():
    parser = argparse.ArgumentParser(
        description="iMessage Gateway - Standalone CLI for iMessage operations",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  %(prog)s find "Angus" --query "SF"       Find messages with Angus containing "SF"
  %(prog)s messages "John" --limit 10      Get last 10 messages with John
  %(prog)s recent                          Show recent conversations
  %(prog)s unread                          Show unread messages
  %(prog)s send "John" "Running late!"     Send message to John
  %(prog)s send-by-phone +14155551234 "Hi" Send directly to phone number
  %(prog)s contacts                        List all contacts
  %(prog)s followup --days 7               Find messages needing follow-up
  %(prog)s search "dinner plans"           Semantic search across indexed messages
  %(prog)s index --source=imessage         Index iMessages for semantic search
        """
    )

    subparsers = parser.add_subparsers(dest='command', help='Command to run')

    # find command (keyword search in messages)
    p_find = subparsers.add_parser('find', help='Find messages with a contact (keyword search)')
    p_find.add_argument('contact', help='Contact name (fuzzy matched)')
    p_find.add_argument('--query', '-q', help='Text to search for in messages')
    p_find.add_argument('--limit', '-l', type=int, default=30, choices=range(1, 501), metavar='N',
                        help='Max messages to return (1-500, default: 30)')
    p_find.add_argument('--json', action='store_true', help='Output as JSON')
    p_find.set_defaults(func=cmd_find)

    # messages command
    p_messages = subparsers.add_parser('messages', help='Get messages with a contact')
    p_messages.add_argument('contact', help='Contact name')
    p_messages.add_argument('--limit', '-l', type=int, default=20, choices=range(1, 501), metavar='N',
                            help='Max messages (1-500, default: 20)')
    p_messages.add_argument('--json', action='store_true', help='Output as JSON')
    p_messages.set_defaults(func=cmd_messages)

    # recent command
    p_recent = subparsers.add_parser('recent', help='Get recent conversations')
    p_recent.add_argument('--limit', '-l', type=int, default=10, choices=range(1, 501), metavar='N',
                          help='Max conversations (1-500, default: 10)')
    p_recent.add_argument('--json', action='store_true', help='Output as JSON')
    p_recent.set_defaults(func=cmd_recent)

    # unread command
    p_unread = subparsers.add_parser('unread', help='Get unread messages')
    p_unread.add_argument('--limit', '-l', type=int, default=20, choices=range(1, 501), metavar='N',
                          help='Max messages (1-500, default: 20)')
    p_unread.add_argument('--json', action='store_true', help='Output as JSON')
    p_unread.set_defaults(func=cmd_unread)

    # send command
    p_send = subparsers.add_parser('send', help='Send a message')
    p_send.add_argument('contact', help='Contact name')
    p_send.add_argument('message', nargs='+', help='Message to send')
    p_send.set_defaults(func=cmd_send)

    # send-by-phone command
    p_send_phone = subparsers.add_parser('send-by-phone', help='Send message directly to phone number')
    p_send_phone.add_argument('phone', help='Phone number (e.g., +14155551234)')
    p_send_phone.add_argument('message', nargs='+', help='Message to send')
    p_send_phone.add_argument('--json', action='store_true', help='Output as JSON')
    p_send_phone.set_defaults(func=cmd_send_by_phone)

    # contacts command
    p_contacts = subparsers.add_parser('contacts', help='List all contacts')
    p_contacts.add_argument('--json', action='store_true', help='Output as JSON')
    p_contacts.set_defaults(func=cmd_contacts)

    # analytics command
    p_analytics = subparsers.add_parser('analytics', help='Get conversation analytics')
    p_analytics.add_argument('contact', nargs='?', help='Contact name (optional)')
    p_analytics.add_argument('--days', '-d', type=int, default=30, choices=range(1, 366), metavar='N',
                             help='Days to analyze (1-365, default: 30)')
    p_analytics.add_argument('--json', action='store_true', help='Output as JSON')
    p_analytics.set_defaults(func=cmd_analytics)

    # followup command
    p_followup = subparsers.add_parser('followup', help='Detect messages needing follow-up')
    p_followup.add_argument('--days', '-d', type=int, default=7, choices=range(1, 366), metavar='N',
                            help='Days to look back (1-365, default: 7)')
    p_followup.add_argument('--stale', '-s', type=int, default=2, choices=range(1, 366), metavar='N',
                            help='Min stale days (1-365, default: 2)')
    p_followup.add_argument('--json', action='store_true', help='Output as JSON')
    p_followup.set_defaults(func=cmd_followup)

    # =========================================================================
    # T0 COMMANDS - Core Features
    # =========================================================================

    # groups command
    p_groups = subparsers.add_parser('groups', help='List all group chats')
    p_groups.add_argument('--limit', '-l', type=int, default=50, choices=range(1, 501), metavar='N',
                          help='Max groups to return (1-500, default: 50)')
    p_groups.add_argument('--json', action='store_true', help='Output as JSON')
    p_groups.set_defaults(func=cmd_groups)

    # group-messages command
    p_group_msg = subparsers.add_parser('group-messages', help='Get messages from a group chat')
    p_group_msg.add_argument('--group-id', '-g', dest='group_id', help='Group chat ID')
    p_group_msg.add_argument('--participant', '-p', help='Filter by participant phone/email')
    p_group_msg.add_argument('--limit', '-l', type=int, default=50, choices=range(1, 501), metavar='N',
                             help='Max messages (1-500, default: 50)')
    p_group_msg.add_argument('--json', action='store_true', help='Output as JSON')
    p_group_msg.set_defaults(func=cmd_group_messages)

    # attachments command
    p_attach = subparsers.add_parser('attachments', help='Get attachments (photos, videos, files)')
    p_attach.add_argument('contact', nargs='?', help='Contact name (optional)')
    p_attach.add_argument('--type', '-t', help='MIME type filter (e.g., "image/", "video/")')
    p_attach.add_argument('--limit', '-l', type=int, default=50, choices=range(1, 501), metavar='N',
                          help='Max attachments (1-500, default: 50)')
    p_attach.add_argument('--json', action='store_true', help='Output as JSON')
    p_attach.set_defaults(func=cmd_attachments)

    # add-contact command
    p_add = subparsers.add_parser('add-contact', help='Add a new contact')
    p_add.add_argument('name', help='Contact name')
    p_add.add_argument('phone', help='Phone number (e.g., +14155551234 or +1-415-555-1234)')
    p_add.add_argument('--relationship', '-r', default='other',
                       choices=['friend', 'family', 'colleague', 'professional', 'other'],
                       help='Relationship type (default: other)')
    p_add.add_argument('--notes', '-n', help='Notes about the contact')
    p_add.set_defaults(func=cmd_add_contact)

    # =========================================================================
    # T1 COMMANDS - Advanced Features
    # =========================================================================

    # reactions command
    p_react = subparsers.add_parser('reactions', help='Get reactions (tapbacks) from messages')
    p_react.add_argument('contact', nargs='?', help='Contact name (optional)')
    p_react.add_argument('--limit', '-l', type=int, default=100, choices=range(1, 501), metavar='N',
                         help='Max reactions (1-500, default: 100)')
    p_react.add_argument('--json', action='store_true', help='Output as JSON')
    p_react.set_defaults(func=cmd_reactions)

    # links command
    p_links = subparsers.add_parser('links', help='Extract URLs shared in conversations')
    p_links.add_argument('contact', nargs='?', help='Contact name (optional)')
    p_links.add_argument('--days', '-d', type=int, choices=range(1, 366), metavar='N',
                         help='Days to look back (1-365)')
    p_links.add_argument('--all-time', action='store_true',
                         help='Search without date cutoff (can be slow)')
    p_links.add_argument('--limit', '-l', type=int, default=100, choices=range(1, 501), metavar='N',
                         help='Max links (1-500, default: 100)')
    p_links.add_argument('--json', action='store_true', help='Output as JSON')
    p_links.set_defaults(func=cmd_links)

    # voice command
    p_voice = subparsers.add_parser('voice', help='Get voice messages with file paths')
    p_voice.add_argument('contact', nargs='?', help='Contact name (optional)')
    p_voice.add_argument('--limit', '-l', type=int, default=50, choices=range(1, 501), metavar='N',
                         help='Max voice messages (1-500, default: 50)')
    p_voice.add_argument('--json', action='store_true', help='Output as JSON')
    p_voice.set_defaults(func=cmd_voice)

    # thread command
    p_thread = subparsers.add_parser('thread', help='Get messages in a reply thread')
    p_thread.add_argument('--guid', '-g', required=True, help='Message GUID to get thread for')
    p_thread.add_argument('--limit', '-l', type=int, default=50, choices=range(1, 501), metavar='N',
                          help='Max messages (1-500, default: 50)')
    p_thread.add_argument('--json', action='store_true', help='Output as JSON')
    p_thread.set_defaults(func=cmd_thread)

    # =========================================================================
    # T2 COMMANDS - Discovery Features
    # =========================================================================

    # handles command
    p_handles = subparsers.add_parser('handles', help='List all phone/email handles from recent messages')
    p_handles.add_argument('--days', '-d', type=int, default=30, choices=range(1, 366), metavar='N',
                           help='Days to look back (1-365, default: 30)')
    p_handles.add_argument('--limit', '-l', type=int, default=100, choices=range(1, 501), metavar='N',
                           help='Max handles (1-500, default: 100)')
    p_handles.add_argument('--json', action='store_true', help='Output as JSON')
    p_handles.set_defaults(func=cmd_handles)

    # unknown command
    p_unknown = subparsers.add_parser('unknown', help='Find messages from senders not in contacts')
    p_unknown.add_argument('--days', '-d', type=int, default=30, choices=range(1, 366), metavar='N',
                           help='Days to look back (1-365, default: 30)')
    p_unknown.add_argument('--limit', '-l', type=int, default=100, choices=range(1, 501), metavar='N',
                           help='Max unknown senders (1-500, default: 100)')
    p_unknown.add_argument('--json', action='store_true', help='Output as JSON')
    p_unknown.set_defaults(func=cmd_unknown)

    # scheduled command
    p_sched = subparsers.add_parser('scheduled', help='Get scheduled messages (pending sends)')
    p_sched.add_argument('--json', action='store_true', help='Output as JSON')
    p_sched.set_defaults(func=cmd_scheduled)

    # summary command
    p_summary = subparsers.add_parser('summary', help='Get conversation formatted for AI summarization')
    p_summary.add_argument('contact', help='Contact name')
    p_summary.add_argument('--days', '-d', type=int, choices=range(1, 366), metavar='N',
                           help='Days to include (1-365)')
    p_summary.add_argument('--limit', '-l', type=int, default=200, choices=range(1, 501), metavar='N',
                           help='Max messages (1-500, default: 200)')
    p_summary.add_argument('--json', action='store_true', help='Output as JSON')
    p_summary.set_defaults(func=cmd_summary)

    # =========================================================================
    # RAG COMMANDS - Semantic Search & Knowledge Base
    # =========================================================================

    # index command
    p_index = subparsers.add_parser('index', help='Index content for semantic search')
    p_index.add_argument('--source', '-s', required=True,
                         choices=VALID_RAG_SOURCES,
                         help='Source to index')
    p_index.add_argument('--days', '-d', type=int, default=30,
                         help='Days of history to index (default: 30)')
    p_index.add_argument('--limit', '-l', type=int,
                         help='Maximum items to index')
    p_index.add_argument('--contact', '-c',
                         help='For iMessage: index only this contact')
    p_index.add_argument('--full', action='store_true',
                         help='Full reindex (ignore incremental state)')
    p_index.add_argument('--json', action='store_true', help='Output as JSON')
    p_index.set_defaults(func=cmd_index)

    # search command (semantic search)
    p_search = subparsers.add_parser('search', help='Semantic search across indexed content')
    p_search.add_argument('query', help='Search query')
    p_search.add_argument('--sources', help='Comma-separated sources to search (default: all)')
    p_search.add_argument('--days', '-d', type=int,
                          help='Only search content from last N days')
    p_search.add_argument('--limit', '-l', type=int, default=10,
                          help='Max results (default: 10)')
    p_search.add_argument('--json', action='store_true', help='Output as JSON')
    p_search.set_defaults(func=cmd_search)

    # ask command (formatted context for AI)
    p_ask = subparsers.add_parser('ask', help='Get AI-formatted context from knowledge base')
    p_ask.add_argument('question', help='Question to answer')
    p_ask.add_argument('--sources', help='Comma-separated sources to search (default: all)')
    p_ask.add_argument('--days', '-d', type=int,
                       help='Only search content from last N days')
    p_ask.add_argument('--limit', '-l', type=int, default=5,
                       help='Max results to include (default: 5)')
    p_ask.add_argument('--json', action='store_true', help='Output as JSON')
    p_ask.set_defaults(func=cmd_ask)

    # stats command
    p_stats = subparsers.add_parser('stats', help='Show knowledge base statistics')
    p_stats.add_argument('--source', '-s', help='Show stats for specific source')
    p_stats.add_argument('--json', action='store_true', help='Output as JSON')
    p_stats.set_defaults(func=cmd_stats)

    # clear command
    p_clear = subparsers.add_parser('clear', help='Clear indexed data')
    p_clear.add_argument('--source', '-s', help='Clear only this source (default: all)')
    p_clear.add_argument('--force', '-f', action='store_true',
                         help='Skip confirmation prompt')
    p_clear.add_argument('--json', action='store_true', help='Output as JSON')
    p_clear.set_defaults(func=cmd_clear)

    # sources command
    p_sources = subparsers.add_parser('sources', help='List available and indexed sources')
    p_sources.add_argument('--json', action='store_true', help='Output as JSON')
    p_sources.set_defaults(func=cmd_sources)

    return parser


def execute_cli(argv):
    """Execute the gateway CLI programmatically.

    Args:
        argv: List of CLI arguments (excluding the program name)

    Returns:
        Tuple of (return_code, stdout, stderr)
    """
    parser = create_parser()
    stdout_buffer = io.StringIO()
    stderr_buffer = io.StringIO()

    with redirect_stdout(stdout_buffer), redirect_stderr(stderr_buffer):
        try:
            args = parser.parse_args(argv)

            if not args.command:
                parser.print_help()
                return 1, stdout_buffer.getvalue(), stderr_buffer.getvalue()

            return_code = args.func(args)
        except SystemExit as exc:
            return_code = exc.code if isinstance(exc.code, int) else 1

    return return_code, stdout_buffer.getvalue(), stderr_buffer.getvalue()


def main():
    parser = create_parser()
    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        return 1

    return args.func(args)


if __name__ == '__main__':
    sys.exit(main())
