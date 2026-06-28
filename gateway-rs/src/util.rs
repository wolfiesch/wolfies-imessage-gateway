use chrono::{DateTime, Duration, Local, NaiveDate};

pub fn normalize_phone(value: &str) -> String {
    value
        .chars()
        .filter(|c| c.is_ascii_digit())
        .collect::<String>()
}

pub fn cocoa_to_datetime(timestamp: i64) -> Option<DateTime<Local>> {
    let cocoa_epoch = NaiveDate::from_ymd_opt(2001, 1, 1)?
        .and_hms_opt(0, 0, 0)?
        .and_local_timezone(Local)
        .latest()?;

    let (seconds, nanos) = if timestamp.abs() > 1_000_000_000_000 {
        (timestamp / 1_000_000_000, timestamp % 1_000_000_000)
    } else {
        (timestamp, 0)
    };

    let dt = cocoa_epoch
        .checked_add_signed(Duration::seconds(seconds))
        .and_then(|t| t.checked_add_signed(Duration::nanoseconds(nanos)))?;

    Some(dt)
}

pub fn datetime_to_cocoa(dt: DateTime<Local>) -> i64 {
    if let Some(epoch) = NaiveDate::from_ymd_opt(2001, 1, 1)
        .and_then(|d| d.and_hms_opt(0, 0, 0))
        .and_then(|t| t.and_local_timezone(Local).latest())
    {
        (dt - epoch).num_nanoseconds().unwrap_or(0)
    } else {
        0
    }
}

pub fn format_timestamp(ts: Option<i64>) -> Option<String> {
    ts.and_then(cocoa_to_datetime)
        .map(|dt| dt.format("%Y-%m-%d %H:%M:%S").to_string())
}

pub fn escape_applescript_string(input: &str) -> String {
    input.replace('\\', "\\\\").replace('"', "\\\"")
}
