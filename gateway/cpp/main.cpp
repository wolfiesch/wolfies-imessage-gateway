#include <algorithm>
#include <chrono>
#include <cmath>
#include <cctype>
#include <cstdio>
#include <cstdlib>
#include <ctime>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <limits>
#include <map>
#include <optional>
#include <regex>
#include <sstream>
#include <string>
#include <utility>
#include <vector>

#include <sqlite3.h>

namespace fs = std::filesystem;

struct Contact {
    std::string name;
    std::string phone;
    std::optional<std::string> relationship;
    std::optional<std::string> notes;
};

struct MessageRecord {
    std::string text;
    std::string timestamp;
    bool isFromMe{false};
    std::string handle;
    bool isGroup{false};
    std::optional<std::string> groupId;
};

static std::string toLower(const std::string &input) {
    std::string out = input;
    std::transform(out.begin(), out.end(), out.begin(), [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
    return out;
}

static std::string trim(const std::string &input) {
    const auto start = input.find_first_not_of(" \t\n\r");
    if (start == std::string::npos) {
        return "";
    }
    const auto end = input.find_last_not_of(" \t\n\r");
    return input.substr(start, end - start + 1);
}

static std::string jsonEscape(const std::string &input) {
    std::ostringstream oss;
    for (char c : input) {
        switch (c) {
            case '\\': oss << "\\\\"; break;
            case '"': oss << "\\\""; break;
            case '\b': oss << "\\b"; break;
            case '\f': oss << "\\f"; break;
            case '\n': oss << "\\n"; break;
            case '\r': oss << "\\r"; break;
            case '\t': oss << "\\t"; break;
            default:
                if (static_cast<unsigned char>(c) < 0x20) {
                    oss << "\\u" << std::hex << std::uppercase << (static_cast<int>(c) & 0xFF);
                } else {
                    oss << c;
                }
        }
    }
    return oss.str();
}

static std::string join(const std::vector<std::string> &parts, size_t start = 0) {
    std::ostringstream oss;
    for (size_t i = start; i < parts.size(); ++i) {
        if (i > start) {
            oss << " ";
        }
        oss << parts[i];
    }
    return oss.str();
}

static std::time_t timegmCompat(std::tm *tm) {
#ifdef _WIN32
    return _mkgmtime(tm);
#else
    return timegm(tm);
#endif
}

static int levenshteinDistance(const std::string &a, const std::string &b) {
    const size_t m = a.size();
    const size_t n = b.size();
    std::vector<int> prev(n + 1), cur(n + 1);
    for (size_t j = 0; j <= n; ++j) {
        prev[j] = static_cast<int>(j);
    }
    for (size_t i = 1; i <= m; ++i) {
        cur[0] = static_cast<int>(i);
        for (size_t j = 1; j <= n; ++j) {
            int cost = a[i - 1] == b[j - 1] ? 0 : 1;
            cur[j] = std::min({prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost});
        }
        std::swap(prev, cur);
    }
    return prev[n];
}

static std::string getTimestampFromCocoa(sqlite3_int64 cocoa) {
    // Cocoa stores nanoseconds since 2001-01-01
    if (cocoa == 0) {
        return {};
    }
    auto seconds = static_cast<double>(cocoa) / 1'000'000'000.0;
    const std::tm epoch_tm = {0, 0, 0, 1, 0, 101, 0, 0, 0};  // 2001-01-01
    std::time_t epoch_time = std::mktime(const_cast<std::tm *>(&epoch_tm));
    std::time_t ts = epoch_time + static_cast<std::time_t>(seconds);
    std::tm *utc = std::gmtime(&ts);
    char buffer[64];
    if (std::strftime(buffer, sizeof(buffer), "%Y-%m-%dT%H:%M:%SZ", utc)) {
        return std::string(buffer);
    }
    return {};
}

static std::string extractTextFromBlob(const std::vector<unsigned char> &blob) {
    if (blob.empty()) {
        return {};
    }

    const std::string data(blob.begin(), blob.end());

    auto ns_pos = data.find("NSString");
    if (ns_pos != std::string::npos) {
        auto plus = data.find('+', ns_pos);
        if (plus != std::string::npos && plus + 1 < data.size()) {
            size_t start = plus + 2;  // skip + and length byte
            size_t end = start;
            while (end < data.size()) {
                unsigned char c = static_cast<unsigned char>(data[end]);
                if (c == 0x86 || c == 0x84 || c == 0x00) {
                    break;
                }
                ++end;
            }
            if (end > start) {
                std::string candidate = data.substr(start, end - start);
                candidate.erase(std::remove_if(candidate.begin(), candidate.end(), [](unsigned char ch) {
                    return static_cast<bool>(std::iscntrl(ch));
                }), candidate.end());
                if (!candidate.empty()) {
                    return candidate;
                }
            }
        }
    }

    std::string printable;
    printable.reserve(blob.size());
    for (unsigned char c : blob) {
        if (std::isprint(c)) {
            printable.push_back(static_cast<char>(c));
        } else {
            printable.push_back(' ');
        }
    }

    std::regex text_run(R"(([A-Za-z0-9][A-Za-z0-9\.\,\!\?\-\s]{3,}))");
    std::smatch match;
    if (std::regex_search(printable, match, text_run)) {
        auto candidate = trim(match.str());
        if (!candidate.empty()) {
            return candidate;
        }
    }

    return {};
}

class ContactManager {
public:
    explicit ContactManager(fs::path config_path) : config_path_(std::move(config_path)) {}

    bool load() {
        std::ifstream in(config_path_);
        if (!in) {
            std::cerr << "Could not open contacts config: " << config_path_ << "\n";
            return false;
        }
        std::stringstream buffer;
        buffer << in.rdbuf();
        const std::string contents = buffer.str();

        std::regex contact_block(R"(\{[^\}]*\})");
        auto begin = std::sregex_iterator(contents.begin(), contents.end(), contact_block);
        auto end = std::sregex_iterator();

        std::regex name_rx(R"REGEX("name"\s*:\s*"([^"]+)")REGEX");
        std::regex phone_rx(R"REGEX("phone"\s*:\s*"([^"]+)")REGEX");
        std::regex relationship_rx(R"REGEX("relationship_type"\s*:\s*"([^"]+)")REGEX");
        std::regex notes_rx(R"REGEX("notes"\s*:\s*"([^"]+)")REGEX");

        for (auto it = begin; it != end; ++it) {
            const std::string block = it->str();
            std::smatch name_match;
            std::smatch phone_match;
            if (!std::regex_search(block, name_match, name_rx) || !std::regex_search(block, phone_match, phone_rx)) {
                continue;
            }

            Contact c;
            c.name = name_match[1].str();
            c.phone = phone_match[1].str();

            std::smatch rel_match;
            if (std::regex_search(block, rel_match, relationship_rx)) {
                c.relationship = rel_match[1].str();
            }

            std::smatch notes_match;
            if (std::regex_search(block, notes_match, notes_rx)) {
                c.notes = notes_match[1].str();
            }

            contacts_.push_back(c);
        }

        return !contacts_.empty();
    }

    std::optional<Contact> resolve(const std::string &query) const {
        if (contacts_.empty()) {
            return std::nullopt;
        }

        const std::string query_lower = toLower(query);

        for (const auto &c : contacts_) {
            if (toLower(c.name) == query_lower) {
                return c;
            }
        }

        for (const auto &c : contacts_) {
            const auto name_lower = toLower(c.name);
            if (name_lower.find(query_lower) != std::string::npos) {
                return c;
            }
        }

        int best_distance = std::numeric_limits<int>::max();
        const Contact *best = nullptr;
        for (const auto &c : contacts_) {
            int dist = levenshteinDistance(query_lower, toLower(c.name));
            if (dist < best_distance) {
                best_distance = dist;
                best = &c;
            }
        }

        if (best && best_distance <= static_cast<int>(best->name.size() / 2 + 2)) {
            return *best;
        }

        return std::nullopt;
    }

    const std::vector<Contact> &all() const { return contacts_; }

private:
    fs::path config_path_;
    std::vector<Contact> contacts_;
};

class MessageGateway {
public:
    explicit MessageGateway(fs::path db_path) : db_path_(std::move(db_path)) {}

    bool canAccessDatabase() const {
        return fs::exists(db_path_);
    }

    std::optional<std::string> sendMessage(const std::string &phone, const std::string &message) const {
        const std::string escaped_message = escapeForAppleScript(message);
        const std::string escaped_phone = escapeForAppleScript(phone);
        std::ostringstream script;
        script << "tell application \"Messages\"\n"
               << "    set targetService to 1st account whose service type = iMessage\n"
               << "    set targetBuddy to participant \"" << escaped_phone << "\" of targetService\n"
               << "    send \"" << escaped_message << "\" to targetBuddy\n"
               << "end tell\n";

        std::string command = "osascript -e \"" + escapeShell(script.str()) + "\"";
        int rc = std::system(command.c_str());
        if (rc != 0) {
            return std::string("osascript returned non-zero status: ") + std::to_string(rc);
        }
        return std::nullopt;
    }

    std::vector<MessageRecord> getMessagesByPhone(const std::string &phone, int limit) const {
        const char *sql = R"SQL(
            SELECT message.text, message.attributedBody, message.date, message.is_from_me, handle.id, message.cache_roomnames
            FROM message
            JOIN handle ON message.handle_id = handle.ROWID
            WHERE handle.id LIKE ?
            ORDER BY message.date DESC
            LIMIT ?
        )SQL";
        return runMessageQuery(sql, {phone}, limit);
    }

    std::vector<MessageRecord> getRecentConversations(int limit) const {
        const char *sql = R"SQL(
            SELECT message.text, message.attributedBody, message.date, message.is_from_me, handle.id, message.cache_roomnames
            FROM message
            LEFT JOIN handle ON message.handle_id = handle.ROWID
            ORDER BY message.date DESC
            LIMIT ?
        )SQL";
        return runMessageQuery(sql, {}, limit);
    }

    std::vector<MessageRecord> getUnreadMessages(int limit) const {
        const char *sql = R"SQL(
            SELECT m.text, m.attributedBody, m.date, m.is_from_me, h.id, m.cache_roomnames
            FROM message m
            LEFT JOIN handle h ON m.handle_id = h.ROWID
            WHERE m.is_read = 0
                AND m.is_from_me = 0
                AND m.is_finished = 1
                AND m.is_system_message = 0
                AND m.item_type = 0
            ORDER BY m.date DESC
            LIMIT ?
        )SQL";
        return runMessageQuery(sql, {}, limit);
    }

    std::vector<MessageRecord> searchMessages(const std::string &query, const std::optional<std::string> &phone, int limit) const {
        const char *sql_contact = R"SQL(
            SELECT message.text, message.attributedBody, message.date, message.is_from_me, handle.id, message.cache_roomnames
            FROM message
            JOIN handle ON message.handle_id = handle.ROWID
            WHERE handle.id LIKE ?
            ORDER BY message.date DESC
            LIMIT ?
        )SQL";

        const char *sql_all = R"SQL(
            SELECT message.text, message.attributedBody, message.date, message.is_from_me, handle.id, message.cache_roomnames
            FROM message
            LEFT JOIN handle ON message.handle_id = handle.ROWID
            ORDER BY message.date DESC
            LIMIT ?
        )SQL";

        std::vector<MessageRecord> messages;
        if (phone.has_value()) {
            messages = runMessageQuery(sql_contact, {*phone}, limit);
        } else {
            messages = runMessageQuery(sql_all, {}, limit);
        }

        std::vector<MessageRecord> filtered;
        const std::string lower = toLower(query);
        for (auto &m : messages) {
            if (toLower(m.text).find(lower) != std::string::npos) {
                filtered.push_back(m);
            }
        }
        return filtered;
    }

    struct ConversationStats {
        int total_messages{0};
        int sent_count{0};
        int received_count{0};
        double avg_daily_messages{0.0};
        std::optional<int> busiest_hour;
        std::optional<std::string> busiest_day;
        int attachment_count{0};
        int reaction_count{0};
        std::vector<std::pair<std::string, int>> top_contacts;
    };

    ConversationStats getConversationAnalytics(const std::optional<std::string> &phone, int days) const {
        ConversationStats stats;
        sqlite3 *db = nullptr;
        if (!openDatabase(&db)) {
            return stats;
        }

        auto cutoff = static_cast<sqlite3_int64>(secondsSinceCocoa(days));

        const std::string base_filter = phone ? " AND h.id LIKE ?" : "";
        const std::string count_query =
            "SELECT COUNT(*), SUM(CASE WHEN m.is_from_me = 1 THEN 1 ELSE 0 END), "
            "SUM(CASE WHEN m.is_from_me = 0 THEN 1 ELSE 0 END) "
            "FROM message m LEFT JOIN handle h ON m.handle_id = h.ROWID "
            "WHERE m.date >= ?" + base_filter + " AND (m.associated_message_type IS NULL OR m.associated_message_type = 0)";

        sqlite3_stmt *stmt = nullptr;
        if (sqlite3_prepare_v2(db, count_query.c_str(), -1, &stmt, nullptr) == SQLITE_OK) {
            sqlite3_bind_int64(stmt, 1, cutoff);
            if (phone) {
                sqlite3_bind_text(stmt, 2, phone->c_str(), -1, SQLITE_TRANSIENT);
            }
            if (sqlite3_step(stmt) == SQLITE_ROW) {
                stats.total_messages = sqlite3_column_int(stmt, 0);
                stats.sent_count = sqlite3_column_int(stmt, 1);
                stats.received_count = sqlite3_column_int(stmt, 2);
                stats.avg_daily_messages = days > 0 ? std::round((static_cast<double>(stats.total_messages) / days) * 10.0) / 10.0 : 0.0;
            }
        }
        sqlite3_finalize(stmt);

        const std::string hour_query =
            "SELECT CAST((m.date / 1000000000 / 3600) % 24 AS INTEGER) as hour, COUNT(*) as count "
            "FROM message m LEFT JOIN handle h ON m.handle_id = h.ROWID "
            "WHERE m.date >= ?" + base_filter + " GROUP BY hour ORDER BY count DESC LIMIT 1";
        if (sqlite3_prepare_v2(db, hour_query.c_str(), -1, &stmt, nullptr) == SQLITE_OK) {
            sqlite3_bind_int64(stmt, 1, cutoff);
            if (phone) {
                sqlite3_bind_text(stmt, 2, phone->c_str(), -1, SQLITE_TRANSIENT);
            }
            if (sqlite3_step(stmt) == SQLITE_ROW) {
                stats.busiest_hour = sqlite3_column_int(stmt, 0);
            }
        }
        sqlite3_finalize(stmt);

        const std::string dow_query =
            "SELECT CAST((m.date / 1000000000 / 86400 + 1) % 7 AS INTEGER) as dow, COUNT(*) as count "
            "FROM message m LEFT JOIN handle h ON m.handle_id = h.ROWID "
            "WHERE m.date >= ?" + base_filter + " GROUP BY dow ORDER BY count DESC LIMIT 1";
        const std::vector<std::string> days_of_week = {"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"};
        if (sqlite3_prepare_v2(db, dow_query.c_str(), -1, &stmt, nullptr) == SQLITE_OK) {
            sqlite3_bind_int64(stmt, 1, cutoff);
            if (phone) {
                sqlite3_bind_text(stmt, 2, phone->c_str(), -1, SQLITE_TRANSIENT);
            }
            if (sqlite3_step(stmt) == SQLITE_ROW) {
                int dow = sqlite3_column_int(stmt, 0);
                if (dow >= 0 && dow < static_cast<int>(days_of_week.size())) {
                    stats.busiest_day = days_of_week[dow];
                }
            }
        }
        sqlite3_finalize(stmt);

        const std::string attachment_query =
            "SELECT COUNT(DISTINCT a.ROWID) FROM attachment a "
            "JOIN message_attachment_join maj ON a.ROWID = maj.attachment_id "
            "JOIN message m ON maj.message_id = m.ROWID "
            "LEFT JOIN handle h ON m.handle_id = h.ROWID "
            "WHERE m.date >= ?" + base_filter;
        if (sqlite3_prepare_v2(db, attachment_query.c_str(), -1, &stmt, nullptr) == SQLITE_OK) {
            sqlite3_bind_int64(stmt, 1, cutoff);
            if (phone) {
                sqlite3_bind_text(stmt, 2, phone->c_str(), -1, SQLITE_TRANSIENT);
            }
            if (sqlite3_step(stmt) == SQLITE_ROW) {
                stats.attachment_count = sqlite3_column_int(stmt, 0);
            }
        }
        sqlite3_finalize(stmt);

        const std::string reaction_query =
            "SELECT COUNT(*) FROM message m LEFT JOIN handle h ON m.handle_id = h.ROWID "
            "WHERE m.date >= ?" + base_filter + " AND m.associated_message_type BETWEEN 2000 AND 3005";
        if (sqlite3_prepare_v2(db, reaction_query.c_str(), -1, &stmt, nullptr) == SQLITE_OK) {
            sqlite3_bind_int64(stmt, 1, cutoff);
            if (phone) {
                sqlite3_bind_text(stmt, 2, phone->c_str(), -1, SQLITE_TRANSIENT);
            }
            if (sqlite3_step(stmt) == SQLITE_ROW) {
                stats.reaction_count = sqlite3_column_int(stmt, 0);
            }
        }
        sqlite3_finalize(stmt);

        if (!phone) {
            const std::string top_query =
                "SELECT h.id, COUNT(*) as msg_count FROM message m "
                "JOIN handle h ON m.handle_id = h.ROWID "
                "WHERE m.date >= ? AND (m.associated_message_type IS NULL OR m.associated_message_type = 0) "
                "GROUP BY h.id ORDER BY msg_count DESC LIMIT 10";
            if (sqlite3_prepare_v2(db, top_query.c_str(), -1, &stmt, nullptr) == SQLITE_OK) {
                sqlite3_bind_int64(stmt, 1, cutoff);
                while (sqlite3_step(stmt) == SQLITE_ROW) {
                    std::string handle = reinterpret_cast<const char *>(sqlite3_column_text(stmt, 0));
                    int count = sqlite3_column_int(stmt, 1);
                    stats.top_contacts.emplace_back(handle, count);
                }
            }
            sqlite3_finalize(stmt);
        }

        sqlite3_close(db);
        return stats;
    }

    struct FollowUpItem {
        std::string phone;
        std::string text;
        std::string date;
        std::string reason;
    };

    std::vector<FollowUpItem> detectFollowUps(int days, int stale_days, int limit) const {
        std::vector<FollowUpItem> results;
        sqlite3 *db = nullptr;
        if (!openDatabase(&db)) {
            return results;
        }

        sqlite3_int64 cutoff = secondsSinceCocoa(days);
        const std::time_t stale_seconds = static_cast<std::time_t>(stale_days * 24 * 3600);

        const char *sql = R"SQL(
            SELECT m.text, m.attributedBody, m.date, m.is_from_me, h.id
            FROM message m
            JOIN handle h ON m.handle_id = h.ROWID
            WHERE m.date >= ?
                AND (m.associated_message_type IS NULL OR m.associated_message_type = 0)
                AND m.item_type = 0
            ORDER BY h.id, m.date DESC
        )SQL";

        sqlite3_stmt *stmt = nullptr;
        if (sqlite3_prepare_v2(db, sql, -1, &stmt, nullptr) != SQLITE_OK) {
            sqlite3_close(db);
            return results;
        }

        sqlite3_bind_int64(stmt, 1, cutoff);

        std::map<std::string, std::vector<MessageRecord>> conversations;
        while (sqlite3_step(stmt) == SQLITE_ROW) {
            std::string text = columnText(stmt, 0);
            std::vector<unsigned char> blob = columnBlob(stmt, 1);
            if (text.empty() && !blob.empty()) {
                text = extractTextFromBlob(blob);
            }
            sqlite3_int64 cocoa = sqlite3_column_int64(stmt, 2);
            bool is_from_me = sqlite3_column_int(stmt, 3) != 0;
            std::string handle = columnText(stmt, 4);

            if (text.empty()) {
                continue;
            }

            MessageRecord record;
            record.text = text;
            record.timestamp = getTimestampFromCocoa(cocoa);
            record.isFromMe = is_from_me;
            record.handle = handle;
            conversations[handle].push_back(record);
        }
        sqlite3_finalize(stmt);
        sqlite3_close(db);

        for (auto &entry : conversations) {
            auto &msgs = entry.second;
            if (msgs.empty()) {
                continue;
            }
            const MessageRecord &latest = msgs.front();
            if (!latest.isFromMe) {
                auto date = latest.timestamp;
                if (!date.empty()) {
                    // convert timestamp string back to time_t
                    std::tm tm{};
                    std::istringstream iss(date);
                    iss >> std::get_time(&tm, "%Y-%m-%dT%H:%M:%SZ");
                    auto tt = timegmCompat(&tm);
                    if (tt != -1) {
                        std::time_t now = std::time(nullptr);
                        if (now - tt > stale_seconds && static_cast<int>(results.size()) < limit) {
                            results.push_back({entry.first, latest.text, latest.timestamp, "stale_conversation"});
                        }
                    }
                }
            }

            for (const auto &msg : msgs) {
                if (static_cast<int>(results.size()) >= limit) {
                    break;
                }
                if (!msg.isFromMe) {
                    if (msg.text.find('?') != std::string::npos) {
                        bool replied = false;
                        for (const auto &reply : msgs) {
                            if (reply.isFromMe && reply.timestamp > msg.timestamp) {
                                replied = true;
                                break;
                            }
                        }
                        if (!replied) {
                            results.push_back({entry.first, msg.text, msg.timestamp, "unanswered_question"});
                        }
                    }
                }
            }
        }

        return results;
    }

private:
    fs::path db_path_;

    static std::string escapeForAppleScript(const std::string &input) {
        std::string escaped = input;
        size_t pos = 0;
        while ((pos = escaped.find("\\", pos)) != std::string::npos) {
            escaped.replace(pos, 1, "\\\\");
            pos += 2;
        }
        pos = 0;
        while ((pos = escaped.find("\"", pos)) != std::string::npos) {
            escaped.replace(pos, 1, "\\\"");
            pos += 2;
        }
        return escaped;
    }

    static std::string escapeShell(const std::string &input) {
        std::string escaped;
        escaped.reserve(input.size());
        for (char c : input) {
            if (c == '"' || c == '\\' || c == '$' || c == '`') {
                escaped.push_back('\\');
            }
            escaped.push_back(c);
        }
        return escaped;
    }

    bool openDatabase(sqlite3 **db) const {
        if (sqlite3_open_v2(db_path_.c_str(), db, SQLITE_OPEN_READONLY, nullptr) != SQLITE_OK) {
            std::cerr << "Failed to open Messages database at " << db_path_ << "\n";
            return false;
        }
        return true;
    }

    static bool isGroupChat(const std::string &identifier) {
        if (identifier.rfind("chat", 0) == 0) {
            std::string rest = identifier.substr(4);
            return !rest.empty() && std::all_of(rest.begin(), rest.end(), ::isdigit);
        }
        return identifier.find(',') != std::string::npos;
    }

    static std::string columnText(sqlite3_stmt *stmt, int col) {
        const unsigned char *text = sqlite3_column_text(stmt, col);
        if (!text) {
            return {};
        }
        return reinterpret_cast<const char *>(text);
    }

    static std::vector<unsigned char> columnBlob(sqlite3_stmt *stmt, int col) {
        const auto *ptr = static_cast<const unsigned char *>(sqlite3_column_blob(stmt, col));
        int size = sqlite3_column_bytes(stmt, col);
        if (!ptr || size <= 0) {
            return {};
        }
        return std::vector<unsigned char>(ptr, ptr + size);
    }

    static sqlite3_int64 secondsSinceCocoa(int days_back) {
        using namespace std::chrono;
        auto now = system_clock::now();
        auto cutoff = now - hours(days_back * 24);
        auto cocoa_epoch = system_clock::from_time_t(978307200); // 2001-01-01
        auto diff = cutoff - cocoa_epoch;
        return duration_cast<nanoseconds>(diff).count();
    }

    std::vector<MessageRecord> runMessageQuery(const std::string &sql, const std::vector<std::string> &params, int limit) const {
        sqlite3 *db = nullptr;
        std::vector<MessageRecord> results;
        if (!openDatabase(&db)) {
            return results;
        }

        sqlite3_stmt *stmt = nullptr;
        if (sqlite3_prepare_v2(db, sql.c_str(), -1, &stmt, nullptr) != SQLITE_OK) {
            sqlite3_close(db);
            return results;
        }

        int bind_index = 1;
        for (const auto &p : params) {
            sqlite3_bind_text(stmt, bind_index++, p.c_str(), -1, SQLITE_TRANSIENT);
        }
        sqlite3_bind_int(stmt, bind_index, limit);

        while (sqlite3_step(stmt) == SQLITE_ROW) {
            std::string text = columnText(stmt, 0);
            std::vector<unsigned char> blob = columnBlob(stmt, 1);
            sqlite3_int64 cocoa = sqlite3_column_int64(stmt, 2);
            bool is_from_me = sqlite3_column_int(stmt, 3) != 0;
            std::string handle = columnText(stmt, 4);
            std::string cache_roomnames = columnText(stmt, 5);

            if (text.empty() && !blob.empty()) {
                text = extractTextFromBlob(blob);
            }
            if (text.empty()) {
                text = "[message content not available]";
            }

            MessageRecord record;
            record.text = text;
            record.timestamp = getTimestampFromCocoa(cocoa);
            record.isFromMe = is_from_me;
            record.handle = handle;
            record.isGroup = isGroupChat(cache_roomnames);
            if (record.isGroup) {
                record.groupId = cache_roomnames;
            }

            results.push_back(record);
        }

        sqlite3_finalize(stmt);
        sqlite3_close(db);
        return results;
    }
};

static fs::path findRepositoryRoot(const fs::path &start) {
    fs::path current = fs::absolute(start);
    for (int i = 0; i < 6; ++i) {
        if (fs::exists(current / "config") && fs::exists(current / "src")) {
            return current;
        }
        if (!current.has_parent_path()) {
            break;
        }
        current = current.parent_path();
    }
    return {};
}

static void printMessages(const std::vector<MessageRecord> &messages, bool as_json, const std::optional<std::string> &contact_name = std::nullopt) {
    if (as_json) {
        std::cout << "[";
        for (size_t i = 0; i < messages.size(); ++i) {
            const auto &m = messages[i];
            std::cout << "{"
                      << "\"text\":\"" << jsonEscape(m.text) << "\","
                      << "\"timestamp\":\"" << jsonEscape(m.timestamp) << "\","
                      << "\"is_from_me\":" << (m.isFromMe ? "true" : "false") << ","
                      << "\"handle\":\"" << jsonEscape(m.handle) << "\"";
            if (m.isGroup && m.groupId.has_value()) {
                std::cout << ",\"group_id\":\"" << jsonEscape(*m.groupId) << "\"";
            }
            std::cout << "}";
            if (i + 1 < messages.size()) {
                std::cout << ",";
            }
        }
        std::cout << "]\n";
    } else {
        for (const auto &m : messages) {
            std::string sender = m.isFromMe ? "Me" : (contact_name.value_or(m.handle));
            std::cout << sender << ": " << m.text << "\n";
        }
    }
}

static void printContacts(const std::vector<Contact> &contacts, bool as_json) {
    if (as_json) {
        std::cout << "[";
        for (size_t i = 0; i < contacts.size(); ++i) {
            const auto &c = contacts[i];
            std::cout << "{"
                      << "\"name\":\"" << jsonEscape(c.name) << "\","
                      << "\"phone\":\"" << jsonEscape(c.phone) << "\"";
            if (c.relationship) {
                std::cout << ",\"relationship\":\"" << jsonEscape(*c.relationship) << "\"";
            }
            if (c.notes) {
                std::cout << ",\"notes\":\"" << jsonEscape(*c.notes) << "\"";
            }
            std::cout << "}";
            if (i + 1 < contacts.size()) {
                std::cout << ",";
            }
        }
        std::cout << "]\n";
    } else {
        std::cout << "Contacts (" << contacts.size() << "):\n";
        for (const auto &c : contacts) {
            std::cout << "- " << c.name << ": " << c.phone << "\n";
        }
    }
}

static void printAnalytics(const MessageGateway::ConversationStats &stats, bool as_json, int days) {
    if (as_json) {
        std::cout << "{"
                  << "\"total_messages\":" << stats.total_messages << ","
                  << "\"sent_count\":" << stats.sent_count << ","
                  << "\"received_count\":" << stats.received_count << ","
                  << "\"avg_daily_messages\":" << stats.avg_daily_messages << ","
                  << "\"analysis_period_days\":" << days << ",";
        if (stats.busiest_hour.has_value()) {
            std::cout << "\"busiest_hour\":" << *stats.busiest_hour << ",";
        } else {
            std::cout << "\"busiest_hour\":null,";
        }
        if (stats.busiest_day.has_value()) {
            std::cout << "\"busiest_day\":\"" << jsonEscape(*stats.busiest_day) << "\",";
        } else {
            std::cout << "\"busiest_day\":null,";
        }
        std::cout << "\"attachment_count\":" << stats.attachment_count << ","
                  << "\"reaction_count\":" << stats.reaction_count;
        if (!stats.top_contacts.empty()) {
            std::cout << ",\"top_contacts\":[";
            for (size_t i = 0; i < stats.top_contacts.size(); ++i) {
                const auto &tc = stats.top_contacts[i];
                std::cout << "{\"phone\":\"" << jsonEscape(tc.first) << "\",\"message_count\":" << tc.second << "}";
                if (i + 1 < stats.top_contacts.size()) {
                    std::cout << ",";
                }
            }
            std::cout << "]";
        }
        std::cout << "}\n";
    } else {
        std::cout << "Conversation Analytics (last " << days << " days):\n";
        std::cout << "  Total messages: " << stats.total_messages << "\n";
        std::cout << "  Sent: " << stats.sent_count << ", Received: " << stats.received_count << "\n";
        std::cout << "  Avg per day: " << stats.avg_daily_messages << "\n";
        if (stats.busiest_hour) {
            std::cout << "  Busiest hour: " << *stats.busiest_hour << "\n";
        }
        if (stats.busiest_day) {
            std::cout << "  Busiest day: " << *stats.busiest_day << "\n";
        }
        std::cout << "  Attachments: " << stats.attachment_count << ", Reactions: " << stats.reaction_count << "\n";
        if (!stats.top_contacts.empty()) {
            std::cout << "  Top contacts:\n";
            for (const auto &tc : stats.top_contacts) {
                std::cout << "    - " << tc.first << ": " << tc.second << "\n";
            }
        }
    }
}

static void printFollowUps(const std::vector<MessageGateway::FollowUpItem> &items, bool as_json) {
    if (as_json) {
        std::cout << "[";
        for (size_t i = 0; i < items.size(); ++i) {
            const auto &f = items[i];
            std::cout << "{"
                      << "\"phone\":\"" << jsonEscape(f.phone) << "\","
                      << "\"text\":\"" << jsonEscape(f.text) << "\","
                      << "\"date\":\"" << jsonEscape(f.date) << "\","
                      << "\"reason\":\"" << jsonEscape(f.reason) << "\""
                      << "}";
            if (i + 1 < items.size()) {
                std::cout << ",";
            }
        }
        std::cout << "]\n";
    } else if (items.empty()) {
        std::cout << "No follow-ups needed.\n";
    } else {
        std::cout << "Follow-ups Needed:\n";
        for (const auto &f : items) {
            std::cout << "- " << f.phone << " (" << f.reason << "): " << f.text << "\n";
        }
    }
}

static void printUsage() {
    std::cout << "iMessage Gateway (C++)\n"
              << "Usage:\n"
              << "  imessage_gateway search <contact> [--query <text>] [--limit N] [--json]\n"
              << "  imessage_gateway messages <contact> [--limit N] [--json]\n"
              << "  imessage_gateway recent [--limit N] [--json]\n"
              << "  imessage_gateway unread [--limit N] [--json]\n"
              << "  imessage_gateway send <contact> <message...>\n"
              << "  imessage_gateway contacts [--json]\n"
              << "  imessage_gateway analytics [contact] [--days N] [--json]\n"
              << "  imessage_gateway followup [--days N] [--stale N] [--limit N] [--json]\n";
}

int main(int argc, char **argv) {
    if (argc < 2) {
        printUsage();
        return 1;
    }

    std::string command = argv[1];
    std::vector<std::string> args;
    for (int i = 2; i < argc; ++i) {
        args.emplace_back(argv[i]);
    }

    fs::path executable_path = fs::absolute(argv[0]);
    fs::path repo_root = findRepositoryRoot(executable_path);
    if (repo_root.empty()) {
        repo_root = findRepositoryRoot(fs::current_path());
    }

    if (repo_root.empty()) {
        std::cerr << "Could not locate repository root (config and src folders).\n";
        return 1;
    }

    fs::path contacts_path = repo_root / "config" / "contacts.json";
    const char *home_env = std::getenv("HOME");
    fs::path db_path;
    if (home_env) {
        db_path = fs::path(home_env) / "Library" / "Messages" / "chat.db";
    } else {
        std::cerr << "HOME environment variable not set. Cannot locate chat.db\n";
        return 1;
    }

    ContactManager contact_manager(contacts_path);
    if (!contact_manager.load()) {
        std::cerr << "Warning: No contacts loaded from " << contacts_path << ". Contact matching may fail.\n";
    }
    MessageGateway gateway(db_path);

    if (command == "contacts") {
        bool as_json = std::find(args.begin(), args.end(), "--json") != args.end();
        printContacts(contact_manager.all(), as_json);
        return 0;
    }

    if (command == "send") {
        if (args.size() < 2) {
            std::cerr << "Usage: send <contact> <message...>\n";
            return 1;
        }
        std::string contact_name = args[0];
        auto contact = contact_manager.resolve(contact_name);
        if (!contact) {
            std::cerr << "Contact not found: " << contact_name << "\n";
            return 1;
        }
        std::string message = join(args, 1);
        auto error = gateway.sendMessage(contact->phone, message);
        if (error.has_value()) {
            std::cerr << "Failed to send: " << *error << "\n";
            return 1;
        }
        std::cout << "Message sent to " << contact->name << " (" << contact->phone << ")\n";
        return 0;
    }

    if (!gateway.canAccessDatabase()) {
        std::cerr << "Messages database not accessible at " << db_path << "\n";
        return 1;
    }

    if (command == "messages") {
        if (args.empty()) {
            std::cerr << "Usage: messages <contact> [--limit N] [--json]\n";
            return 1;
        }
        std::string contact_name = args[0];
        int limit = 20;
        bool as_json = false;
        for (size_t i = 1; i < args.size(); ++i) {
            if (args[i] == "--limit" || args[i] == "-l") {
                if (i + 1 < args.size()) {
                    limit = std::stoi(args[++i]);
                }
            } else if (args[i] == "--json") {
                as_json = true;
            }
        }
        auto contact = contact_manager.resolve(contact_name);
        if (!contact) {
            std::cerr << "Contact not found: " << contact_name << "\n";
            return 1;
        }
        auto messages = gateway.getMessagesByPhone(contact->phone, limit);
        printMessages(messages, as_json, contact->name);
        return 0;
    }

    if (command == "search") {
        if (args.empty()) {
            std::cerr << "Usage: search <contact> [--query text] [--limit N] [--json]\n";
            return 1;
        }
        std::string contact_name = args[0];
        std::optional<std::string> query;
        int limit = 30;
        bool as_json = false;
        for (size_t i = 1; i < args.size(); ++i) {
            if (args[i] == "--query" || args[i] == "-q") {
                if (i + 1 < args.size()) {
                    query = args[++i];
                }
            } else if (args[i] == "--limit" || args[i] == "-l") {
                if (i + 1 < args.size()) {
                    limit = std::stoi(args[++i]);
                }
            } else if (args[i] == "--json") {
                as_json = true;
            }
        }
        auto contact = contact_manager.resolve(contact_name);
        if (!contact) {
            std::cerr << "Contact not found: " << contact_name << "\n";
            return 1;
        }
        auto messages = gateway.getMessagesByPhone(contact->phone, limit);
        if (query.has_value()) {
            messages.erase(std::remove_if(messages.begin(), messages.end(), [&](const MessageRecord &m) {
                return toLower(m.text).find(toLower(*query)) == std::string::npos;
            }), messages.end());
        }
        printMessages(messages, as_json, contact->name);
        return 0;
    }

    if (command == "recent") {
        int limit = 10;
        bool as_json = false;
        for (size_t i = 0; i < args.size(); ++i) {
            if (args[i] == "--limit" || args[i] == "-l") {
                if (i + 1 < args.size()) {
                    limit = std::stoi(args[++i]);
                }
            } else if (args[i] == "--json") {
                as_json = true;
            }
        }
        auto messages = gateway.getRecentConversations(limit);
        printMessages(messages, as_json);
        return 0;
    }

    if (command == "unread") {
        int limit = 20;
        bool as_json = false;
        for (size_t i = 0; i < args.size(); ++i) {
            if (args[i] == "--limit" || args[i] == "-l") {
                if (i + 1 < args.size()) {
                    limit = std::stoi(args[++i]);
                }
            } else if (args[i] == "--json") {
                as_json = true;
            }
        }
        auto messages = gateway.getUnreadMessages(limit);
        printMessages(messages, as_json);
        return 0;
    }

    if (command == "analytics") {
        std::optional<std::string> contact_name;
        int days = 30;
        bool as_json = false;

        if (!args.empty() && args[0][0] != '-') {
            contact_name = args[0];
        }
        for (size_t i = 0; i < args.size(); ++i) {
            if (args[i] == "--days" || args[i] == "-d") {
                if (i + 1 < args.size()) {
                    days = std::stoi(args[++i]);
                }
            } else if (args[i] == "--json") {
                as_json = true;
            }
        }
        std::optional<std::string> phone;
        if (contact_name) {
            auto contact = contact_manager.resolve(*contact_name);
            if (!contact) {
                std::cerr << "Contact not found: " << *contact_name << "\n";
                return 1;
            }
            phone = contact->phone;
        }
        auto stats = gateway.getConversationAnalytics(phone, days);
        printAnalytics(stats, as_json, days);
        return 0;
    }

    if (command == "followup") {
        int days = 7;
        int stale = 3;
        int limit = 50;
        bool as_json = false;
        for (size_t i = 0; i < args.size(); ++i) {
            if (args[i] == "--days" || args[i] == "-d") {
                if (i + 1 < args.size()) {
                    days = std::stoi(args[++i]);
                }
            } else if (args[i] == "--stale" || args[i] == "-s") {
                if (i + 1 < args.size()) {
                    stale = std::stoi(args[++i]);
                }
            } else if (args[i] == "--limit" || args[i] == "-l") {
                if (i + 1 < args.size()) {
                    limit = std::stoi(args[++i]);
                }
            } else if (args[i] == "--json") {
                as_json = true;
            }
        }
        auto items = gateway.detectFollowUps(days, stale, limit);
        printFollowUps(items, as_json);
        return 0;
    }

    printUsage();
    return 1;
}
