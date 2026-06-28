use crate::util::{
    cocoa_to_datetime, datetime_to_cocoa, escape_applescript_string, format_timestamp,
    normalize_phone,
};
use anyhow::{anyhow, Context, Result};
use chrono::{Duration, Local};
use rusqlite::{params, types::Value, Connection, OpenFlags, Row};
use serde::Serialize;
use std::path::PathBuf;
use std::process::Command;

const NORMALIZED_HANDLE_EXPR: &str =
    "replace(replace(replace(replace(replace(h.id, '+',''), '-',''), ' ', ''), '(', ''), ')','')";

#[derive(Debug, Serialize)]
pub struct MessageRecord {
    pub guid: Option<String>,
    pub handle: Option<String>,
    pub sender: Option<String>,
    pub text: Option<String>,
    pub is_from_me: bool,
    pub timestamp: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct ConversationSummary {
    pub handle: Option<String>,
    pub last_message: Option<String>,
    pub last_message_date: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct Analytics {
    pub total_messages: u64,
    pub sent: u64,
    pub received: u64,
    pub first_message: Option<String>,
    pub last_message: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct FollowupItem {
    pub handle: String,
    pub last_inbound: Option<String>,
    pub last_outbound: Option<String>,
    pub text_preview: Option<String>,
    pub days_stale: Option<i64>,
}

pub struct MessagesClient {
    conn: Connection,
}

impl MessagesClient {
    pub fn open(db_path: Option<PathBuf>) -> Result<Self> {
        let path = db_path.unwrap_or_else(default_db_path);
        if !path.exists() {
            return Err(anyhow!("Messages database not found at {}", path.display()));
        }

        let conn = Connection::open_with_flags(&path, OpenFlags::SQLITE_OPEN_READ_ONLY)
            .with_context(|| format!("failed to open Messages.db at {}", path.display()))?;

        Ok(Self { conn })
    }

    pub fn messages_for_phone(&self, phone: &str, limit: usize) -> Result<Vec<MessageRecord>> {
        let pattern = normalized_pattern(phone);
        let mut stmt = self.conn.prepare(&format!(
            "SELECT m.guid, {expr} as handle, m.text, m.is_from_me, m.date
                 FROM message m
                 LEFT JOIN handle h ON m.handle_id = h.ROWID
                 WHERE {expr} LIKE ?
                 ORDER BY m.date DESC
                 LIMIT ?",
            expr = NORMALIZED_HANDLE_EXPR
        ))?;

        let rows = stmt
            .query_map(params![pattern, limit as i64], |row| {
                self.map_message_row(row)
            })?
            .collect::<Result<Vec<_>, _>>()?;

        Ok(rows)
    }

    pub fn search_messages(
        &self,
        phone: &str,
        query: &str,
        limit: usize,
    ) -> Result<Vec<MessageRecord>> {
        let pattern = normalized_pattern(phone);
        let mut stmt = self.conn.prepare(&format!(
            "SELECT m.guid, {expr} as handle, m.text, m.is_from_me, m.date
             FROM message m
             LEFT JOIN handle h ON m.handle_id = h.ROWID
             WHERE {expr} LIKE ? AND m.text LIKE ?
             ORDER BY m.date DESC
             LIMIT ?",
            expr = NORMALIZED_HANDLE_EXPR
        ))?;

        let rows = stmt
            .query_map(
                params![pattern, format!("%{}%", query), limit as i64],
                |row| self.map_message_row(row),
            )?
            .collect::<Result<Vec<_>, _>>()?;

        Ok(rows)
    }

    pub fn recent_conversations(&self, limit: usize) -> Result<Vec<ConversationSummary>> {
        let mut stmt = self.conn.prepare(&format!(
            "SELECT {expr} as handle,
                    MAX(m.date) as last_date,
                    (SELECT text FROM message m2
                     WHERE m2.handle_id = m.handle_id
                     ORDER BY m2.date DESC
                     LIMIT 1) as last_message
             FROM message m
             LEFT JOIN handle h ON m.handle_id = h.ROWID
             WHERE {expr} IS NOT NULL
             GROUP BY m.handle_id
             ORDER BY last_date DESC
             LIMIT ?",
            expr = NORMALIZED_HANDLE_EXPR
        ))?;

        let results = stmt
            .query_map([limit as i64], |row| {
                let handle: Option<String> = row.get(0)?;
                let last_date: Option<i64> = row.get(1)?;
                let last_msg: Option<String> = row.get(2)?;

                Ok(ConversationSummary {
                    handle,
                    last_message: last_msg,
                    last_message_date: format_timestamp(last_date),
                })
            })?
            .collect::<Result<Vec<_>, _>>()?;

        Ok(results)
    }

    pub fn unread_messages(&self, limit: usize) -> Result<Vec<MessageRecord>> {
        let mut stmt = self.conn.prepare(&format!(
            "SELECT m.guid, {expr} as handle, m.text, m.is_from_me, m.date
             FROM message m
             LEFT JOIN handle h ON m.handle_id = h.ROWID
             WHERE m.is_from_me = 0 AND COALESCE(m.is_read, 0) = 0
             ORDER BY m.date DESC
             LIMIT ?",
            expr = NORMALIZED_HANDLE_EXPR
        ))?;

        let results = stmt
            .query_map([limit as i64], |row| self.map_message_row(row))?
            .collect::<Result<Vec<_>, _>>()?;

        Ok(results)
    }

    pub fn analytics(&self, phone: Option<&str>, days: Option<u32>) -> Result<Analytics> {
        let mut sql = format!(
            "SELECT COUNT(*) as total,
                    SUM(CASE WHEN m.is_from_me = 1 THEN 1 ELSE 0 END) as sent,
                    SUM(CASE WHEN m.is_from_me = 0 THEN 1 ELSE 0 END) as received,
                    MIN(m.date) as first_date,
                    MAX(m.date) as last_date
             FROM message m
             LEFT JOIN handle h ON m.handle_id = h.ROWID
             WHERE 1=1"
        );

        let mut params: Vec<Value> = Vec::new();

        if let Some(phone) = phone {
            sql.push_str(&format!(
                " AND {expr} LIKE ?",
                expr = NORMALIZED_HANDLE_EXPR
            ));
            params.push(Value::from(normalized_pattern(phone)));
        }

        if let Some(days) = days {
            let cutoff = Local::now() - Duration::days(days.into());
            sql.push_str(" AND m.date >= ?");
            params.push(Value::from(datetime_to_cocoa(cutoff)));
        }

        let mut stmt = self.conn.prepare(&sql)?;
        let mut rows = stmt.query(rusqlite::params_from_iter(params))?;
        let row = rows.next()?.unwrap();

        let total: u64 = row.get::<_, Option<i64>>(0)?.unwrap_or(0) as u64;
        let sent: u64 = row.get::<_, Option<i64>>(1)?.unwrap_or(0) as u64;
        let received: u64 = row.get::<_, Option<i64>>(2)?.unwrap_or(0) as u64;
        let first_message = format_timestamp(row.get(3)?);
        let last_message = format_timestamp(row.get(4)?);

        Ok(Analytics {
            total_messages: total,
            sent,
            received,
            first_message,
            last_message,
        })
    }

    pub fn followups(&self, days: u32, stale_days: u32) -> Result<Vec<FollowupItem>> {
        let mut sql = format!(
            "SELECT {expr} as handle,
                    MAX(CASE WHEN m.is_from_me = 0 THEN m.date END) as last_inbound,
                    MAX(CASE WHEN m.is_from_me = 1 THEN m.date END) as last_outbound,
                    (SELECT text FROM message mi
                     WHERE mi.handle_id = m.handle_id AND mi.is_from_me = 0
                     ORDER BY mi.date DESC LIMIT 1) as last_text
             FROM message m
             LEFT JOIN handle h ON m.handle_id = h.ROWID
             WHERE {expr} IS NOT NULL",
            expr = NORMALIZED_HANDLE_EXPR
        );

        let mut params: Vec<Value> = Vec::new();

        if days > 0 {
            let cutoff = Local::now() - Duration::days(days.into());
            sql.push_str(" AND m.date >= ?");
            params.push(Value::from(datetime_to_cocoa(cutoff)));
        }

        sql.push_str(" GROUP BY m.handle_id ORDER BY last_inbound DESC");

        let mut stmt = self.conn.prepare(&sql)?;
        let mut rows = stmt.query(rusqlite::params_from_iter(params))?;

        let mut results = Vec::new();
        let now = Local::now();

        while let Some(row) = rows.next()? {
            let handle: Option<String> = row.get(0)?;
            let inbound: Option<i64> = row.get(1)?;
            let outbound: Option<i64> = row.get(2)?;
            let preview: Option<String> = row.get(3)?;

            if handle.is_none() || inbound.is_none() {
                continue;
            }

            let inbound_dt = inbound.and_then(cocoa_to_datetime);
            let outbound_dt = outbound.and_then(cocoa_to_datetime);

            if let Some(inbound_dt) = inbound_dt {
                let needs_reply = outbound_dt.map(|o| o < inbound_dt).unwrap_or(true);
                let age_days = (now - inbound_dt).num_days();

                if needs_reply && age_days >= stale_days as i64 {
                    results.push(FollowupItem {
                        handle: handle.clone().unwrap_or_default(),
                        last_inbound: Some(inbound_dt.format("%Y-%m-%d %H:%M:%S").to_string()),
                        last_outbound: outbound_dt
                            .map(|d| d.format("%Y-%m-%d %H:%M:%S").to_string()),
                        text_preview: preview,
                        days_stale: Some(age_days),
                    });
                }
            }
        }

        Ok(results)
    }

    pub fn send_message(&self, phone: &str, message: &str) -> Result<()> {
        let escaped_phone = escape_applescript_string(phone);
        let escaped_message = escape_applescript_string(message);

        let script = format!(
            r#"tell application "Messages"
    set targetService to 1st service whose service type = iMessage
    set targetBuddy to buddy "{phone}" of targetService
    send "{message}" to targetBuddy
end tell"#,
            phone = escaped_phone,
            message = escaped_message
        );

        let status = Command::new("osascript")
            .arg("-e")
            .arg(script)
            .status()
            .context("failed to invoke osascript")?;

        if status.success() {
            Ok(())
        } else {
            Err(anyhow!("osascript exited with status {}", status))
        }
    }

    fn map_message_row(&self, row: &Row<'_>) -> rusqlite::Result<MessageRecord> {
        let guid: Option<String> = row.get(0)?;
        let handle: Option<String> = row.get(1)?;
        let text: Option<String> = row.get(2)?;
        let is_from_me: bool = row.get::<_, i64>(3)? == 1;
        let ts: Option<i64> = row.get(4)?;

        Ok(MessageRecord {
            guid,
            handle: handle.clone(),
            sender: if is_from_me {
                Some("Me".to_string())
            } else {
                handle.clone()
            },
            text,
            is_from_me,
            timestamp: format_timestamp(ts),
        })
    }
}

fn default_db_path() -> PathBuf {
    home::home_dir()
        .unwrap_or_else(|| PathBuf::from("/"))
        .join("Library")
        .join("Messages")
        .join("chat.db")
}

fn normalized_pattern(phone: &str) -> String {
    format!("%{}%", normalize_phone(phone))
}
