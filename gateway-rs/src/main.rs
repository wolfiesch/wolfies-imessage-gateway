mod contacts;
mod messages;
mod util;

use crate::contacts::ContactsManager;
use crate::messages::{
    Analytics, ConversationSummary, FollowupItem, MessageRecord, MessagesClient,
};
use anyhow::{anyhow, Result};
use clap::{Parser, Subcommand};
use std::env;
use std::path::{Path, PathBuf};

#[derive(Parser)]
#[command(
    name = "imessage-gateway",
    version,
    about = "Fast, standalone iMessage gateway CLI implemented in Rust"
)]
struct Cli {
    #[arg(
        long,
        value_name = "PATH",
        help = "Path to contacts.json (defaults to repository config/contacts.json)"
    )]
    contacts: Option<PathBuf>,

    #[arg(
        long,
        value_name = "PATH",
        help = "Path to chat.db (defaults to ~/Library/Messages/chat.db)"
    )]
    database: Option<PathBuf>,

    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    Search {
        contact: String,
        #[arg(short, long)]
        query: Option<String>,
        #[arg(short, long, default_value_t = 30, value_parser = clap::value_parser!(usize))]
        limit: usize,
        #[arg(long)]
        json: bool,
    },
    Messages {
        contact: String,
        #[arg(short, long, default_value_t = 20, value_parser = clap::value_parser!(usize))]
        limit: usize,
        #[arg(long)]
        json: bool,
    },
    Recent {
        #[arg(short, long, default_value_t = 10, value_parser = clap::value_parser!(usize))]
        limit: usize,
        #[arg(long)]
        json: bool,
    },
    Unread {
        #[arg(short, long, default_value_t = 20, value_parser = clap::value_parser!(usize))]
        limit: usize,
        #[arg(long)]
        json: bool,
    },
    Send {
        contact: String,
        message: Vec<String>,
    },
    Contacts {
        #[arg(long)]
        json: bool,
    },
    Analytics {
        #[arg()]
        contact: Option<String>,
        #[arg(short, long, default_value_t = 30, value_parser = clap::value_parser!(u32))]
        days: u32,
        #[arg(long)]
        json: bool,
    },
    Followup {
        #[arg(short, long, default_value_t = 7, value_parser = clap::value_parser!(u32))]
        days: u32,
        #[arg(short, long, default_value_t = 2, value_parser = clap::value_parser!(u32))]
        stale: u32,
        #[arg(long)]
        json: bool,
    },
}

fn main() -> Result<()> {
    let cli = Cli::parse();
    let repo_root = resolve_repo_root();
    let contacts_path = cli
        .contacts
        .clone()
        .unwrap_or_else(|| repo_root.join("config").join("contacts.json"));
    let contacts = ContactsManager::load(&contacts_path)?;

    match cli.command {
        Command::Contacts { json } => {
            if json {
                print_json(&contacts.all())?;
            } else {
                println!("Contacts ({}):", contacts.all().len());
                for contact in contacts.all() {
                    println!("- {} ({})", contact.name, contact.phone);
                }
            }
        }
        Command::Send { contact, message } => {
            let resolved = contacts.resolve(&contact).ok_or_else(|| {
                anyhow!(
                    "Contact '{}' not found in {}",
                    contact,
                    contacts_path.display()
                )
            })?;
            let text = message.join(" ");

            let client = MessagesClient::open(cli.database)?;
            println!("Sending to {} ({})…", resolved.name, resolved.phone);
            client.send_message(&resolved.phone, &text)?;
            println!("Message sent.");
        }
        Command::Search {
            contact,
            query,
            limit,
            json,
        } => {
            let resolved = require_contact(&contacts, &contacts_path, &contact)?;
            let client = MessagesClient::open(cli.database)?;
            let records = if let Some(query) = query {
                client.search_messages(&resolved.phone, &query, limit)?
            } else {
                client.messages_for_phone(&resolved.phone, limit)?
            };
            render_messages(records, json, Some(&resolved.name));
        }
        Command::Messages {
            contact,
            limit,
            json,
        } => {
            let resolved = require_contact(&contacts, &contacts_path, &contact)?;
            let client = MessagesClient::open(cli.database)?;
            let records = client.messages_for_phone(&resolved.phone, limit)?;
            render_messages(records, json, Some(&resolved.name));
        }
        Command::Recent { limit, json } => {
            let client = MessagesClient::open(cli.database)?;
            let conversations = client.recent_conversations(limit)?;
            render_recent(conversations, json);
        }
        Command::Unread { limit, json } => {
            let client = MessagesClient::open(cli.database)?;
            let unread = client.unread_messages(limit)?;
            render_messages(unread, json, None);
        }
        Command::Analytics {
            contact,
            days,
            json,
        } => {
            let client = MessagesClient::open(cli.database.clone())?;
            let (target_name, phone) = if let Some(name) = contact {
                let resolved = require_contact(&contacts, &contacts_path, &name)?;
                (Some(resolved.name), Some(resolved.phone))
            } else {
                (None, None)
            };

            let stats = client.analytics(phone.as_deref(), Some(days))?;
            render_analytics(stats, target_name.as_deref(), json);
        }
        Command::Followup { days, stale, json } => {
            let client = MessagesClient::open(cli.database)?;
            let items = client.followups(days, stale)?;
            render_followups(items, &contacts, json);
        }
    }

    Ok(())
}

fn require_contact(
    contacts: &ContactsManager,
    path: &Path,
    query: &str,
) -> Result<crate::contacts::Contact> {
    contacts
        .resolve(query)
        .ok_or_else(|| anyhow!("Contact '{}' not found in {}", query, path.display()))
}

fn render_messages(records: Vec<MessageRecord>, json: bool, contact_name: Option<&str>) {
    if json {
        if let Err(err) = print_json(&records) {
            eprintln!("Failed to render JSON: {err}");
        }
        return;
    }

    if records.is_empty() {
        println!("No messages found.");
        return;
    }

    for record in records {
        let sender = if record.is_from_me {
            "Me".to_string()
        } else if let Some(name) = contact_name {
            name.to_string()
        } else {
            record.sender.unwrap_or_else(|| "Unknown".to_string())
        };
        let text = record
            .text
            .as_deref()
            .unwrap_or("[media/attachment]")
            .chars()
            .take(200)
            .collect::<String>();
        let ts = record
            .timestamp
            .unwrap_or_else(|| "unknown time".to_string());
        println!("{ts} | {sender}: {text}");
    }
}

fn render_recent(conversations: Vec<ConversationSummary>, json: bool) {
    if json {
        if let Err(err) = print_json(&conversations) {
            eprintln!("Failed to render JSON: {err}");
        }
        return;
    }

    if conversations.is_empty() {
        println!("No recent conversations found.");
        return;
    }

    println!("Recent Conversations:");
    for conv in conversations {
        let handle = conv.handle.unwrap_or_else(|| "Unknown".to_string());
        let last = conv.last_message.unwrap_or_else(|| "[media]".to_string());
        let date = conv
            .last_message_date
            .unwrap_or_else(|| "unknown time".to_string());
        println!("- {handle}: {last} ({date})");
    }
}

fn render_analytics(stats: Analytics, contact: Option<&str>, json: bool) {
    if json {
        if let Err(err) = print_json(&stats) {
            eprintln!("Failed to render JSON: {err}");
        }
        return;
    }

    if let Some(name) = contact {
        println!("Analytics for {name}:");
    } else {
        println!("Analytics (all conversations):");
    }
    println!("  Total messages: {}", stats.total_messages);
    println!("  Sent: {}", stats.sent);
    println!("  Received: {}", stats.received);
    if let Some(first) = stats.first_message {
        println!("  First message: {first}");
    }
    if let Some(last) = stats.last_message {
        println!("  Last message: {last}");
    }
}

fn render_followups(items: Vec<FollowupItem>, contacts: &ContactsManager, json: bool) {
    if json {
        if let Err(err) = print_json(&items) {
            eprintln!("Failed to render JSON: {err}");
        }
        return;
    }

    if items.is_empty() {
        println!("No follow-ups needed.");
        return;
    }

    println!("Follow-ups needed:");
    for item in items {
        let name = contacts
            .get_by_phone(&item.handle)
            .map(|c| c.name)
            .unwrap_or_else(|| item.handle.clone());

        let preview = item
            .text_preview
            .as_deref()
            .unwrap_or("[no preview]")
            .chars()
            .take(120)
            .collect::<String>();
        let last = item.last_inbound.as_deref().unwrap_or("unknown time");
        let stale = item.days_stale.unwrap_or(0);
        println!("- {name}: {preview} (last inbound {last}, {stale}d stale)");
    }
}

fn print_json<T: serde::Serialize>(value: &T) -> Result<()> {
    let rendered = serde_json::to_string_pretty(value)?;
    println!("{rendered}");
    Ok(())
}

fn resolve_repo_root() -> PathBuf {
    if let Ok(mut dir) = env::current_dir() {
        for _ in 0..4 {
            if dir.join("config").exists() {
                return dir;
            }
            if !dir.pop() {
                break;
            }
        }
    }

    if let Ok(mut exe_dir) = env::current_exe().map(|p| p.parent().map(|p| p.to_path_buf())) {
        if let Some(mut dir) = exe_dir.take() {
            for _ in 0..4 {
                if dir.join("config").exists() {
                    return dir;
                }
                if !dir.pop() {
                    break;
                }
            }
        }
    }

    PathBuf::from(".")
}
