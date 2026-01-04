use std::collections::HashMap;
use std::path::PathBuf;

use anyhow::{bail, Context, Result};
use chrono::{DateTime, Duration, NaiveDateTime, Utc};
use clap::{Parser, Subcommand};
use fuzzy_matcher::skim::SkimMatcherV2;
use fuzzy_matcher::FuzzyMatcher;
use regex::Regex;
use rusqlite::{Connection, OpenFlags, Row};
use serde::Serialize;
use thiserror::Error;

#[derive(Debug, Parser)]
#[command(name = "imessage-gateway", about = "Rust iMessage gateway CLI")]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Debug, Subcommand)]
enum Commands {
    /// Search messages with a contact (fuzzy matched)
    Search {
        contact: String,
        #[arg(short, long, default_value_t = 30)]
        limit: i64,
        #[arg(short, long)]
        query: Option<String>,
    },
    /// Get recent messages with a contact
    Messages {
        contact: String,
        #[arg(short, long, default_value_t = 20)]
        limit: i64,
    },
    /// Get unread incoming messages
    Unread {
        #[arg(short, long, default_value_t = 20)]
        limit: i64,
    },
    /// Send a message (uses AppleScript)
    Send {
        contact: String,
        message: Vec<String>,
    },
    /// List contacts
    Contacts,
}

#[derive(Debug, Serialize)]
struct Contact {
    name: String,
    phone: String,
}

#[derive(Debug, Serialize)]
struct Message {
    text: String,
    date: Option<DateTime<Utc>>,
    is_from_me: bool,
}

#[derive(Debug, Error)]
enum GatewayError {
    #[error("contacts.json not found at {0}")]
    MissingContacts(String),
    #[error("contact '{0}' not found")]
    ContactNotFound(String),
}

struct ContactsManager {
    contacts: Vec<Contact>,
    matcher: SkimMatcherV2,
}

impl ContactsManager {
    fn load(path: PathBuf) -> Result<Self> {
        if !path.exists() {
            return Err(GatewayError::MissingContacts(path.display().to_string()).into());
        }

        let data = std::fs::read_to_string(&path)
            .with_context(|| format!("reading contacts at {}", path.display()))?;
        let json: serde_json::Value = serde_json::from_str(&data)
            .with_context(|| format!("parsing contacts at {}", path.display()))?;
        let contacts_json = json
            .get("contacts")
            .and_then(|c| c.as_array())
            .cloned()
            .unwrap_or_default();

        let contacts = contacts_json
            .into_iter()
            .filter_map(|c| {
                Some(Contact {
                    name: c.get("name")?.as_str()?.to_string(),
                    phone: c.get("phone")?.as_str()?.to_string(),
                })
            })
            .collect();

        Ok(Self {
            contacts,
            matcher: SkimMatcherV2::default(),
        })
    }

    fn resolve(&self, input: &str) -> Option<&Contact> {
        // exact
        if let Some(contact) = self
            .contacts
            .iter()
            .find(|c| c.name.eq_ignore_ascii_case(input))
        {
            return Some(contact);
        }

        // fuzzy
        let mut best: Option<(&Contact, i64)> = None;
        for contact in &self.contacts {
            if let Some(score) = self.matcher.fuzzy_match(&contact.name, input) {
                if best.map_or(true, |(_, s)| score > s) {
                    best = Some((contact, score));
                }
            }
        }
        best.map(|(c, _)| c)
    }
}

struct MessagesDb {
    conn: Connection,
}

impl MessagesDb {
    fn open() -> Result<Self> {
        let mut path = home::home_dir().context("home directory")?;
        path.push("Library/Messages/chat.db");
        let conn = Connection::open_with_flags(
            path,
            OpenFlags::SQLITE_OPEN_READ_ONLY
                | OpenFlags::SQLITE_OPEN_SHARED_CACHE
                | OpenFlags::SQLITE_OPEN_URI,
        )?;
        Ok(Self { conn })
    }

    fn query_messages(&self, phone: &str, limit: i64) -> Result<Vec<Message>> {
        let mut stmt = self.conn.prepare(
            "SELECT text, date, is_from_me FROM message \
             JOIN handle ON message.handle_id = handle.ROWID \
             WHERE handle.id LIKE ?1 \
             ORDER BY message.date DESC LIMIT ?2",
        )?;
        let mut rows = stmt.query((format!("%{}%", phone), limit))?;
        let mut messages = Vec::new();
        while let Some(row) = rows.next()? {
            messages.push(parse_message_row(row));
        }
        Ok(messages)
    }

    fn query_unread(&self, limit: i64) -> Result<Vec<MessageWithSender>> {
        let mut stmt = self.conn.prepare(
            "SELECT m.text, m.date, m.is_from_me, h.id FROM message m \
             LEFT JOIN handle h ON m.handle_id = h.ROWID \
             WHERE m.is_read = 0 AND m.is_from_me = 0 \
             ORDER BY m.date DESC LIMIT ?1",
        )?;
        let mut rows = stmt.query([limit])?;
        let mut messages = Vec::new();
        while let Some(row) = rows.next()? {
            messages.push(parse_message_with_sender_row(row));
        }
        Ok(messages)
    }
}

#[derive(Debug, Serialize)]
struct MessageWithSender {
    text: String,
    date: Option<DateTime<Utc>>,
    is_from_me: bool,
    sender: Option<String>,
}

fn parse_message_row(row: &Row<'_>) -> Message {
    let text: Option<String> = row.get(0).unwrap_or(None);
    let date: Option<i64> = row.get(1).unwrap_or(None);
    let is_from_me: i64 = row.get(2).unwrap_or(0);

    Message {
        text: text.unwrap_or_else(|| "[message content not available]".to_string()),
        date: date.map(cocoa_to_utc),
        is_from_me: is_from_me != 0,
    }
}

fn parse_message_with_sender_row(row: &Row<'_>) -> MessageWithSender {
    let text: Option<String> = row.get(0).unwrap_or(None);
    let date: Option<i64> = row.get(1).unwrap_or(None);
    let is_from_me: i64 = row.get(2).unwrap_or(0);
    let sender: Option<String> = row.get(3).unwrap_or(None);

    MessageWithSender {
        text: text.unwrap_or_else(|| "[message content not available]".to_string()),
        date: date.map(cocoa_to_utc),
        is_from_me: is_from_me != 0,
        sender,
    }
}

fn cocoa_to_utc(ts: i64) -> DateTime<Utc> {
    // Cocoa epoch is 2001-01-01, stored in nanoseconds
    let seconds = ts as f64 / 1_000_000_000.0;
    let naive = NaiveDateTime::from_timestamp(seconds as i64, (seconds.fract() * 1e9) as u32);
    DateTime::<Utc>::from_naive_utc_and_offset(naive, Utc)
}

fn format_messages(messages: &[Message]) -> String {
    messages
        .iter()
        .map(|m| {
            let date = m
                .date
                .map(|d| d.format("%Y-%m-%d %H:%M").to_string())
                .unwrap_or_else(|| "".into());
            let sender = if m.is_from_me { "Me" } else { "Them" };
            format!("{date} | {sender}: {}", truncate(&m.text, 160))
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn truncate(s: &str, max: usize) -> String {
    if s.len() > max {
        format!("{}…", &s[..max])
    } else {
        s.to_string()
    }
}

fn send_message(contact: &Contact, message: &str) -> Result<()> {
    // Use osascript for AppleScript send
    let script = format!(
        "tell application \"Messages\" to send \"{}\" to buddy \"{}\"",
        escape_applescript_string(message),
        escape_applescript_string(&contact.phone),
    );
    let status = std::process::Command::new("osascript")
        .arg("-e")
        .arg(script)
        .status()
        .context("running osascript")?;
    if status.success() {
        Ok(())
    } else {
        bail!("osascript exited with status {status}");
    }
}

fn escape_applescript_string(input: &str) -> String {
    input.replace('\\', "\\\\").replace('"', "\\\"")
}

fn load_contacts() -> Result<ContactsManager> {
    let mut root = std::env::current_dir().context("current dir")?;
    root.push("config/contacts.json");
    ContactsManager::load(root)
}

fn main() -> Result<()> {
    let cli = Cli::parse();
    let contacts = load_contacts()?;
    let db = MessagesDb::open()?;

    match cli.command {
        Commands::Search {
            contact,
            limit,
            query,
        } => {
            let contact = contacts
                .resolve(&contact)
                .ok_or_else(|| GatewayError::ContactNotFound(contact.clone()))?;
            let mut messages = db.query_messages(&contact.phone, limit)?;
            if let Some(q) = query {
                let re = Regex::new(&q).unwrap_or_else(|_| Regex::new(".*").unwrap());
                messages.retain(|m| re.is_match(&m.text));
            }
            println!("Messages with {} ({}):", contact.name, contact.phone);
            println!("{}", format_messages(&messages));
        }
        Commands::Messages { contact, limit } => {
            let contact = contacts
                .resolve(&contact)
                .ok_or_else(|| GatewayError::ContactNotFound(contact.clone()))?;
            let messages = db.query_messages(&contact.phone, limit)?;
            println!("{}", format_messages(&messages));
        }
        Commands::Unread { limit } => {
            let messages = db.query_unread(limit)?;
            for m in messages {
                let sender = m.sender.as_deref().unwrap_or("unknown");
                let when = m
                    .date
                    .map(|d| d.format("%Y-%m-%d %H:%M").to_string())
                    .unwrap_or_else(|| "".into());
                println!("{when} | {sender}: {}", truncate(&m.text, 160));
            }
        }
        Commands::Send { contact, message } => {
            let contact = contacts
                .resolve(&contact)
                .ok_or_else(|| GatewayError::ContactNotFound(contact.clone()))?;
            let body = message.join(" ");
            send_message(contact, &body)?;
            println!("Message sent to {}", contact.name);
        }
        Commands::Contacts => {
            for c in contacts.contacts {
                println!("{}: {}", c.name, c.phone);
            }
        }
    }

    Ok(())
}
