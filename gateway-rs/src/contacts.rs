use crate::util::normalize_phone;
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::Path;

#[derive(Debug, Clone, Serialize)]
pub struct Contact {
    pub name: String,
    pub phone: String,
    pub relationship_type: Option<String>,
    pub notes: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ContactsFile {
    contacts: Option<Vec<ContactRecord>>,
}

#[derive(Debug, Deserialize)]
struct ContactRecord {
    name: String,
    phone: String,
    #[serde(default)]
    relationship_type: Option<String>,
    #[serde(default)]
    notes: Option<String>,
}

pub struct ContactsManager {
    contacts: Vec<Contact>,
}

impl ContactsManager {
    pub fn load(path: &Path) -> Result<Self> {
        if !path.exists() {
            eprintln!(
                "Contacts file not found at {}. Proceeding with empty contact list.",
                path.display()
            );
            return Ok(Self { contacts: vec![] });
        }

        let raw = fs::read_to_string(path)
            .with_context(|| format!("failed to read contacts file at {}", path.display()))?;
        let parsed: ContactsFile = serde_json::from_str(&raw)
            .with_context(|| format!("failed to parse contacts JSON at {}", path.display()))?;

        let contacts = parsed
            .contacts
            .unwrap_or_default()
            .into_iter()
            .map(|c| Contact {
                name: c.name,
                phone: c.phone,
                relationship_type: c.relationship_type,
                notes: c.notes,
            })
            .collect();

        Ok(Self { contacts })
    }

    pub fn resolve(&self, query: &str) -> Option<Contact> {
        if query.trim().is_empty() {
            return None;
        }

        let lower = query.to_lowercase();

        for contact in &self.contacts {
            if contact.name.to_lowercase() == lower {
                return Some(contact.clone());
            }
        }

        for contact in &self.contacts {
            if contact.name.to_lowercase().contains(&lower) {
                return Some(contact.clone());
            }
        }

        let mut best: Option<(f64, Contact)> = None;
        for contact in &self.contacts {
            let score = strsim::jaro_winkler(&contact.name.to_lowercase(), &lower);
            if score > 0.82 {
                if let Some((best_score, _)) = &best {
                    if score > *best_score {
                        best = Some((score, contact.clone()));
                    }
                } else {
                    best = Some((score, contact.clone()));
                }
            }
        }

        best.map(|(_, c)| c)
    }

    pub fn get_by_phone(&self, phone: &str) -> Option<Contact> {
        let needle = normalize_phone(phone);
        self.contacts.iter().find_map(|c| {
            let normalized = normalize_phone(&c.phone);
            if normalized.ends_with(&needle) || needle.ends_with(&normalized) {
                Some(c.clone())
            } else {
                None
            }
        })
    }

    pub fn all(&self) -> &[Contact] {
        &self.contacts
    }
}
