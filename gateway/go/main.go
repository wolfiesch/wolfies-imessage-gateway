package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Contact represents a person with messaging info.
type Contact struct {
	Name             string `json:"name"`
	Phone            string `json:"phone"`
	RelationshipType string `json:"relationship_type"`
	Notes            string `json:"notes"`
}

// ContactsManager handles contact loading and resolution.
type ContactsManager struct {
	contacts []Contact
	path     string
}

// LoadContacts reads contacts from the provided JSON file.
func LoadContacts(path string) (*ContactsManager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read contacts: %w", err)
	}

	var payload struct {
		Contacts []Contact `json:"contacts"`
	}

	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse contacts: %w", err)
	}

	return &ContactsManager{contacts: payload.Contacts, path: path}, nil
}

// ResolveContact finds the best matching contact by name.
func (c *ContactsManager) ResolveContact(name string) (Contact, error) {
	if len(c.contacts) == 0 {
		return Contact{}, errors.New("no contacts configured")
	}

	normalized := strings.TrimSpace(name)
	lowered := strings.ToLower(normalized)

	// Exact match
	for _, contact := range c.contacts {
		if strings.EqualFold(contact.Name, normalized) {
			return contact, nil
		}
	}

	// Partial match
	for _, contact := range c.contacts {
		if strings.Contains(strings.ToLower(contact.Name), lowered) {
			return contact, nil
		}
	}

	// Simple fuzzy: choose contact with lowest Levenshtein distance
	bestScore := -1
	var best Contact
	for _, contact := range c.contacts {
		score := levenshteinDistance(lowered, strings.ToLower(contact.Name))
		if bestScore == -1 || score < bestScore {
			bestScore = score
			best = contact
		}
	}

	if bestScore >= 0 && bestScore <= max(len(lowered), len(best.Name))/2 {
		return best, nil
	}

	return Contact{}, fmt.Errorf("contact '%s' not found", name)
}

// FindByPhone searches for a contact by phone suffix to handle country codes.
func (c *ContactsManager) FindByPhone(phone string) (Contact, bool) {
	target := normalizeDigits(phone)
	for _, contact := range c.contacts {
		normalized := normalizeDigits(contact.Phone)
		if strings.HasSuffix(normalized, target) || strings.HasSuffix(target, normalized) {
			return contact, true
		}
	}
	return Contact{}, false
}

// List returns all contacts.
func (c *ContactsManager) List() []Contact {
	return c.contacts
}

// MessagesInterface interacts with chat.db through sqlite3 CLI and AppleScript.
type MessagesInterface struct {
	dbPath string
}

// NewMessagesInterface constructs an interface for the given database path.
func NewMessagesInterface(dbPath string) *MessagesInterface {
	return &MessagesInterface{dbPath: dbPath}
}

// DefaultMessagesDB returns the default chat.db path.
func DefaultMessagesDB() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/Library/Messages/chat.db"
	}
	return filepath.Join(home, "Library", "Messages", "chat.db")
}

// Message represents a single record.
type Message struct {
	Text         string    `json:"text"`
	Timestamp    time.Time `json:"timestamp"`
	IsFromMe     bool      `json:"is_from_me"`
	IsGroupChat  bool      `json:"is_group_chat"`
	GroupID      string    `json:"group_id"`
	Phone        string    `json:"phone"`
	SenderHandle string    `json:"sender_handle"`
}

// UnreadMessage represents an unread inbound message.
type UnreadMessage struct {
	Text        string    `json:"text"`
	Timestamp   time.Time `json:"timestamp"`
	Phone       string    `json:"phone"`
	GroupID     string    `json:"group_id"`
	GroupName   string    `json:"group_name"`
	IsGroupChat bool      `json:"is_group_chat"`
	DaysOld     int       `json:"days_old"`
}

// ConversationAnalytics summarizes messaging volume.
type ConversationAnalytics struct {
	TotalMessages     int           `json:"total_messages"`
	SentCount         int           `json:"sent_count"`
	ReceivedCount     int           `json:"received_count"`
	AvgDailyMessages  float64       `json:"avg_daily_messages"`
	BusiestHour       *int          `json:"busiest_hour"`
	BusiestDay        string        `json:"busiest_day"`
	TopContacts       []ContactStat `json:"top_contacts"`
	AttachmentCount   int           `json:"attachment_count"`
	ReactionCount     int           `json:"reaction_count"`
	AnalysisPeriodDay int           `json:"analysis_period_days"`
}

// ContactStat pairs a contact handle with counts.
type ContactStat struct {
	Phone        string `json:"phone"`
	MessageCount int    `json:"message_count"`
}

// FollowUpResult contains conversations needing attention.
type FollowUpResult struct {
	UnansweredQuestions []FollowUpItem `json:"unanswered_questions"`
	PendingPromises     []FollowUpItem `json:"pending_promises"`
	WaitingOnThem       []FollowUpItem `json:"waiting_on_them"`
	StaleConversations  []FollowUpItem `json:"stale_conversations"`
	TimeSensitive       []FollowUpItem `json:"time_sensitive"`
	AnalysisPeriodDays  int            `json:"analysis_period_days"`
}

// FollowUpItem represents a single follow-up candidate.
type FollowUpItem struct {
	Phone       string `json:"phone"`
	Text        string `json:"text"`
	Date        string `json:"date"`
	DaysAgo     int    `json:"days_ago"`
	DaysWaiting int    `json:"days_waiting"`
}

// SendMessage triggers AppleScript to send an iMessage.
func (m *MessagesInterface) SendMessage(phone, message string) error {
	script := fmt.Sprintf(`tell application "Messages"
set targetService to 1st account whose service type = iMessage
set targetBuddy to participant "%s" of targetService
send "%s" to targetBuddy
end tell`, escapeAppleScriptString(phone), escapeAppleScriptString(message))

	cmd := exec.Command("osascript", "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send via osascript: %w (%s)", err, string(output))
	}
	return nil
}

// GetMessagesByPhone retrieves recent messages for a handle using sqlite3 CLI.
func (m *MessagesInterface) GetMessagesByPhone(phone string, limit int) ([]Message, error) {
	filter := escapeLike(phone)
	query := fmt.Sprintf(`
SELECT message.text, message.attributedBody, message.date, message.is_from_me, message.cache_roomnames, handle.id
FROM message
JOIN handle ON message.handle_id = handle.ROWID
WHERE handle.id LIKE '%%%s%%'
ORDER BY message.date DESC
LIMIT %d;
`, filter, limit)
	rows, err := m.runSQLiteJSON(query)
	if err != nil {
		return nil, err
	}
	return m.rowsToMessages(rows)
}

// GetAllRecentConversations fetches messages across all conversations.
func (m *MessagesInterface) GetAllRecentConversations(limit int) ([]Message, error) {
	query := fmt.Sprintf(`
SELECT message.text, message.attributedBody, message.date, message.is_from_me, handle.id, message.cache_roomnames
FROM message
LEFT JOIN handle ON message.handle_id = handle.ROWID
ORDER BY message.date DESC
LIMIT %d;
`, limit)
	rows, err := m.runSQLiteJSON(query)
	if err != nil {
		return nil, err
	}
	return m.rowsToMessages(rows)
}

// GetUnreadMessages lists unread inbound messages.
func (m *MessagesInterface) GetUnreadMessages(limit int) ([]UnreadMessage, error) {
	query := fmt.Sprintf(`
SELECT m.text, m.attributedBody, m.date, h.id, m.cache_roomnames, c.display_name
FROM message m
LEFT JOIN handle h ON m.handle_id = h.ROWID
LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
LEFT JOIN chat c ON cmj.chat_id = c.ROWID
WHERE m.is_read = 0 AND m.is_from_me = 0 AND m.is_finished = 1 AND m.is_system_message = 0 AND m.item_type = 0
ORDER BY m.date DESC
LIMIT %d;
`, limit)
	rows, err := m.runSQLiteJSON(query)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	results := make([]UnreadMessage, 0, len(rows))
	for _, row := range rows {
		ts := cocoaToTime(row["date"])
		text := extractText(row)
		sender := stringValue(row["id"])
		cacheRoom := stringValue(row["cache_roomnames"])
		results = append(results, UnreadMessage{
			Text:        text,
			Timestamp:   ts,
			Phone:       sender,
			GroupID:     cacheRoom,
			GroupName:   stringValue(row["display_name"]),
			IsGroupChat: isGroupChatIdentifier(cacheRoom),
			DaysOld:     int(now.Sub(ts).Hours() / 24),
		})
	}
	return results, nil
}

// GetConversationAnalytics aggregates conversation metrics.
func (m *MessagesInterface) GetConversationAnalytics(phone string, days int) (ConversationAnalytics, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	cutoffCocoa := int64(cutoff.Sub(cocoaEpoch()).Nanoseconds())

	baseFilter := fmt.Sprintf("WHERE m.date >= %d", cutoffCocoa)
	if phone != "" {
		baseFilter += fmt.Sprintf(" AND h.id LIKE '%%%s%%'", escapeLike(phone))
	}

	countQuery := fmt.Sprintf(`
SELECT COUNT(*) as total,
       SUM(CASE WHEN m.is_from_me = 1 THEN 1 ELSE 0 END) as sent,
       SUM(CASE WHEN m.is_from_me = 0 THEN 1 ELSE 0 END) as received
FROM message m
LEFT JOIN handle h ON m.handle_id = h.ROWID
%s
AND (m.associated_message_type IS NULL OR m.associated_message_type = 0);
`, baseFilter)

	countRows, err := m.runSQLiteJSON(countQuery)
	if err != nil {
		return ConversationAnalytics{}, err
	}
	var total, sent, received int
	if len(countRows) > 0 {
		total = int(numberValue(countRows[0]["total"]))
		sent = int(numberValue(countRows[0]["sent"]))
		received = int(numberValue(countRows[0]["received"]))
	}

	busiestHourQuery := fmt.Sprintf(`
SELECT CAST((m.date / 1000000000 / 3600) %% 24 AS INTEGER) AS hour, COUNT(*) as count
FROM message m
LEFT JOIN handle h ON m.handle_id = h.ROWID
%s
GROUP BY hour
ORDER BY count DESC
LIMIT 1;
`, baseFilter)
	var busiestHourPtr *int
	if rows, err := m.runSQLiteJSON(busiestHourQuery); err == nil && len(rows) > 0 {
		h := int(numberValue(rows[0]["hour"]))
		busiestHourPtr = &h
	}

	busiestDayQuery := fmt.Sprintf(`
SELECT CAST((m.date / 1000000000 / 86400 + 1) %% 7 AS INTEGER) AS dow, COUNT(*) as count
FROM message m
LEFT JOIN handle h ON m.handle_id = h.ROWID
%s
GROUP BY dow
ORDER BY count DESC
LIMIT 1;
`, baseFilter)
	busiestDay := ""
	if rows, err := m.runSQLiteJSON(busiestDayQuery); err == nil && len(rows) > 0 {
		dow := int(numberValue(rows[0]["dow"]))
		daysOfWeek := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		if dow >= 0 && dow < len(daysOfWeek) {
			busiestDay = daysOfWeek[dow]
		}
	}

	topContacts := []ContactStat{}
	if phone == "" {
		topQuery := fmt.Sprintf(`
SELECT h.id, COUNT(*) as count
FROM message m
JOIN handle h ON m.handle_id = h.ROWID
WHERE m.date >= %d AND (m.associated_message_type IS NULL OR m.associated_message_type = 0)
GROUP BY h.id
ORDER BY count DESC
LIMIT 10;
`, cutoffCocoa)
		if rows, err := m.runSQLiteJSON(topQuery); err == nil {
			for _, row := range rows {
				topContacts = append(topContacts, ContactStat{
					Phone:        stringValue(row["id"]),
					MessageCount: int(numberValue(row["count"])),
				})
			}
		}
	}

	attachments := 0
	attachmentQuery := fmt.Sprintf(`
SELECT COUNT(DISTINCT a.ROWID) as attachments
FROM attachment a
JOIN message_attachment_join maj ON a.ROWID = maj.attachment_id
JOIN message m ON maj.message_id = m.ROWID
LEFT JOIN handle h ON m.handle_id = h.ROWID
%s;
`, baseFilter)
	if rows, err := m.runSQLiteJSON(attachmentQuery); err == nil && len(rows) > 0 {
		attachments = int(numberValue(rows[0]["attachments"]))
	}

	reactions := 0
	reactionQuery := fmt.Sprintf(`
SELECT COUNT(*) as reactions
FROM message m
LEFT JOIN handle h ON m.handle_id = h.ROWID
%s
AND m.associated_message_type BETWEEN 2000 AND 3005;
`, baseFilter)
	if rows, err := m.runSQLiteJSON(reactionQuery); err == nil && len(rows) > 0 {
		reactions = int(numberValue(rows[0]["reactions"]))
	}

	avgDaily := float64(total) / float64(max(1, days))

	return ConversationAnalytics{
		TotalMessages:     total,
		SentCount:         sent,
		ReceivedCount:     received,
		AvgDailyMessages:  avgDaily,
		BusiestHour:       busiestHourPtr,
		BusiestDay:        busiestDay,
		TopContacts:       topContacts,
		AttachmentCount:   attachments,
		ReactionCount:     reactions,
		AnalysisPeriodDay: days,
	}, nil
}

// DetectFollowUpNeeded surfaces conversations that may need replies.
func (m *MessagesInterface) DetectFollowUpNeeded(days, staleDays, limit int) (FollowUpResult, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	cutoffCocoa := int64(cutoff.Sub(cocoaEpoch()).Nanoseconds())

	query := fmt.Sprintf(`
SELECT m.text, m.attributedBody, m.date, m.is_from_me, h.id
FROM message m
JOIN handle h ON m.handle_id = h.ROWID
WHERE m.date >= %d AND (m.associated_message_type IS NULL OR m.associated_message_type = 0) AND m.item_type = 0
ORDER BY h.id, m.date DESC;
`, cutoffCocoa)

	rows, err := m.runSQLiteJSON(query)
	if err != nil {
		return FollowUpResult{}, err
	}

	conversations := map[string][]Message{}
	for _, row := range rows {
		text := extractText(row)
		if text == "" {
			continue
		}
		phone := stringValue(row["id"])
		ts := cocoaToTime(row["date"])
		isFromMe := numberValue(row["is_from_me"]) == 1
		conversations[phone] = append(conversations[phone], Message{
			Text:      text,
			Timestamp: ts,
			IsFromMe:  isFromMe,
			Phone:     phone,
		})
	}

	result := FollowUpResult{AnalysisPeriodDays: days}
	staleCutoff := time.Now().AddDate(0, 0, -staleDays)

	for phone, msgs := range conversations {
		if len(msgs) == 0 {
			continue
		}

		// Messages are ordered DESC
		last := msgs[0]
		if !last.IsFromMe && last.Timestamp.Before(staleCutoff) {
			result.StaleConversations = append(result.StaleConversations, FollowUpItem{
				Phone:   phone,
				Text:    truncateText(last.Text, 200),
				Date:    last.Timestamp.Format(time.RFC3339),
				DaysAgo: int(time.Since(last.Timestamp).Hours() / 24),
			})
		}

		for idx, msg := range msgs {
			if idx >= 20 {
				break
			}
			lower := strings.ToLower(msg.Text)

			if !msg.IsFromMe {
				for _, pattern := range questionPatterns() {
					if pattern.MatchString(lower) && !hasReplyAfter(msgs, idx) {
						result.UnansweredQuestions = appendLimited(result.UnansweredQuestions, FollowUpItem{
							Phone:   phone,
							Text:    truncateText(msg.Text, 200),
							Date:    msg.Timestamp.Format(time.RFC3339),
							DaysAgo: int(time.Since(msg.Timestamp).Hours() / 24),
						}, limit)
						break
					}
				}
			} else {
				for _, pattern := range promisePatterns() {
					if pattern.MatchString(lower) {
						result.PendingPromises = appendLimited(result.PendingPromises, FollowUpItem{
							Phone:   phone,
							Text:    truncateText(msg.Text, 200),
							Date:    msg.Timestamp.Format(time.RFC3339),
							DaysAgo: int(time.Since(msg.Timestamp).Hours() / 24),
						}, limit)
						break
					}
				}
				for _, pattern := range waitingPatterns() {
					if pattern.MatchString(lower) && !hasIncomingAfter(msgs, idx) {
						result.WaitingOnThem = appendLimited(result.WaitingOnThem, FollowUpItem{
							Phone:       phone,
							Text:        truncateText(msg.Text, 200),
							Date:        msg.Timestamp.Format(time.RFC3339),
							DaysWaiting: int(time.Since(msg.Timestamp).Hours() / 24),
						}, limit)
						break
					}
				}
			}

			for _, pattern := range timeReferencePatterns() {
				if pattern.MatchString(lower) {
					result.TimeSensitive = appendLimited(result.TimeSensitive, FollowUpItem{
						Phone:   phone,
						Text:    truncateText(msg.Text, 200),
						Date:    msg.Timestamp.Format(time.RFC3339),
						DaysAgo: int(time.Since(msg.Timestamp).Hours() / 24),
					}, limit)
					break
				}
			}
		}
	}

	return result, nil
}

// rowsToMessages converts sqlite row maps into Message slices.
func (m *MessagesInterface) rowsToMessages(rows []map[string]interface{}) ([]Message, error) {
	result := make([]Message, 0, len(rows))
	for _, row := range rows {
		text := extractText(row)
		ts := cocoaToTime(row["date"])
		handle := stringValue(row["id"])
		cacheRoom := stringValue(row["cache_roomnames"])

		result = append(result, Message{
			Text:         text,
			Timestamp:    ts,
			IsFromMe:     numberValue(row["is_from_me"]) == 1,
			IsGroupChat:  isGroupChatIdentifier(cacheRoom),
			GroupID:      cacheRoom,
			Phone:        handle,
			SenderHandle: handle,
		})
	}
	return result, nil
}

// runSQLiteJSON executes a query via sqlite3 CLI and parses JSON output.
func (m *MessagesInterface) runSQLiteJSON(query string) ([]map[string]interface{}, error) {
	if _, err := os.Stat(m.dbPath); err != nil {
		return nil, fmt.Errorf("open messages db: %w", err)
	}

	cmd := exec.Command("sqlite3", "-json", m.dbPath, query)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite3 query failed: %w (%s)", err, string(output))
	}

	if len(output) == 0 {
		return []map[string]interface{}{}, nil
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("parse sqlite3 json: %w", err)
	}
	return rows, nil
}

// ==== CLI helpers ====

type commandContext struct {
	contacts *ContactsManager
	messages *MessagesInterface
}

func loadContext(contactsPath, dbPath string) (*commandContext, error) {
	cm, err := LoadContacts(contactsPath)
	if err != nil {
		return nil, err
	}
	mi := NewMessagesInterface(dbPath)
	return &commandContext{contacts: cm, messages: mi}, nil
}

func addSharedFlags(fs *flag.FlagSet) (*string, *string) {
	contacts := fs.String("contacts", defaultContactsPath(), "Path to contacts.json")
	dbPath := fs.String("db", DefaultMessagesDB(), "Path to chat.db")
	return contacts, dbPath
}

func defaultContactsPath() string {
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, "config", "contacts.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(filepath.Dir(filepath.Dir(os.Args[0])), "config", "contacts.json")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "search":
		handleSearch(os.Args[2:])
	case "messages":
		handleMessages(os.Args[2:])
	case "recent":
		handleRecent(os.Args[2:])
	case "unread":
		handleUnread(os.Args[2:])
	case "send":
		handleSend(os.Args[2:])
	case "contacts":
		handleContacts(os.Args[2:])
	case "analytics":
		handleAnalytics(os.Args[2:])
	case "followup":
		handleFollowUp(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`iMessage MCP Gateway (Go)
Usage:
  imessage-gateway <command> [options]

Commands:
  search <contact>       Search messages with a contact
  messages <contact>     Show recent messages with a contact
  recent                 Show recent conversations across all contacts
  unread                 List unread inbound messages
  send <contact> <msg>   Send a message via AppleScript
  contacts               List configured contacts
  analytics [contact]    Conversation analytics (optionally scoped)
  followup               Find messages that may need a reply
`)
}

func handleSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	query := fs.String("query", "", "Filter messages containing text")
	limit := fs.Int("limit", 30, "Maximum messages to return")
	asJSON := fs.Bool("json", false, "Output as JSON")
	contactsPath, dbPath := addSharedFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "contact name is required")
		os.Exit(1)
	}

	ctx, err := loadContext(*contactsPath, *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	contact, err := ctx.contacts.ResolveContact(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	messages, err := ctx.messages.GetMessagesByPhone(contact.Phone, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	filtered := make([]Message, 0, len(messages))
	for _, m := range messages {
		if *query == "" || containsIgnoreCase(m.Text, *query) {
			filtered = append(filtered, m)
		}
	}

	if *asJSON {
		outputJSON(filtered)
		return
	}

	fmt.Printf("Messages with %s (%s):\n", contact.Name, contact.Phone)
	fmt.Println("----------------------------------------")
	for _, m := range filtered {
		sender := contact.Name
		if m.IsFromMe {
			sender = "Me"
		}
		fmt.Printf("%s | %s: %s\n", m.Timestamp.Format(time.RFC3339), sender, m.Text)
	}
}

func handleMessages(args []string) {
	fs := flag.NewFlagSet("messages", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Max messages to return")
	asJSON := fs.Bool("json", false, "Output as JSON")
	contactsPath, dbPath := addSharedFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "contact name is required")
		os.Exit(1)
	}

	ctx, err := loadContext(*contactsPath, *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	contact, err := ctx.contacts.ResolveContact(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	messages, err := ctx.messages.GetMessagesByPhone(contact.Phone, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *asJSON {
		outputJSON(messages)
		return
	}

	for _, m := range messages {
		sender := contact.Name
		if m.IsFromMe {
			sender = "Me"
		}
		fmt.Printf("%s: %s\n", sender, m.Text)
	}
}

func handleRecent(args []string) {
	fs := flag.NewFlagSet("recent", flag.ExitOnError)
	limit := fs.Int("limit", 10, "Max conversations to return")
	asJSON := fs.Bool("json", false, "Output as JSON")
	_, dbPath := addSharedFlags(fs)
	fs.Parse(args)

	ctx, err := loadContext(defaultContactsPath(), *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	messages, err := ctx.messages.GetAllRecentConversations(*limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *asJSON {
		outputJSON(messages)
		return
	}

	fmt.Println("Recent Conversations:")
	fmt.Println("----------------------------------------")
	for _, m := range messages {
		handle := m.SenderHandle
		if handle == "" {
			handle = "Unknown"
		}
		fmt.Printf("%s: %s (%s)\n", handle, truncateText(m.Text, 80), m.Timestamp.Format(time.RFC3339))
	}
}

func handleUnread(args []string) {
	fs := flag.NewFlagSet("unread", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Max messages to return")
	asJSON := fs.Bool("json", false, "Output as JSON")
	_, dbPath := addSharedFlags(fs)
	fs.Parse(args)

	ctx, err := loadContext(defaultContactsPath(), *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	messages, err := ctx.messages.GetUnreadMessages(*limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *asJSON {
		outputJSON(messages)
		return
	}

	if len(messages) == 0 {
		fmt.Println("No unread messages.")
		return
	}

	fmt.Printf("Unread Messages (%d):\n", len(messages))
	fmt.Println("----------------------------------------")
	for _, m := range messages {
		sender := m.Phone
		if sender == "" {
			sender = "Unknown"
		}
		fmt.Printf("%s: %s\n", sender, truncateText(m.Text, 150))
	}
}

func handleSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	contactsPath, dbPath := addSharedFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: send <contact> <message>")
		os.Exit(1)
	}

	ctx, err := loadContext(*contactsPath, *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	contact, err := ctx.contacts.ResolveContact(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	message := strings.Join(fs.Args()[1:], " ")
	fmt.Printf("Sending to %s (%s): %s\n", contact.Name, contact.Phone, truncateText(message, 120))
	if err := ctx.messages.SendMessage(contact.Phone, message); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to send message: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Message sent successfully.")
}

func handleContacts(args []string) {
	fs := flag.NewFlagSet("contacts", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "Output as JSON")
	contactsPath, _ := addSharedFlags(fs)
	fs.Parse(args)

	ctx, err := loadContext(*contactsPath, DefaultMessagesDB())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	contacts := ctx.contacts.List()
	if *asJSON {
		outputJSON(contacts)
		return
	}

	fmt.Printf("Contacts (%d):\n", len(contacts))
	fmt.Println("----------------------------------------")
	for _, c := range contacts {
		fmt.Printf("%s: %s\n", c.Name, c.Phone)
	}
}

func handleAnalytics(args []string) {
	fs := flag.NewFlagSet("analytics", flag.ExitOnError)
	days := fs.Int("days", 30, "Days to analyze")
	asJSON := fs.Bool("json", false, "Output as JSON")
	contactsPath, dbPath := addSharedFlags(fs)
	fs.Parse(args)

	ctx, err := loadContext(*contactsPath, *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var phone string
	var contactName string
	if fs.NArg() > 0 {
		contactName = fs.Arg(0)
		contact, err := ctx.contacts.ResolveContact(contactName)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		phone = contact.Phone
	}

	analytics, err := ctx.messages.GetConversationAnalytics(phone, *days)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *asJSON {
		outputJSON(analytics)
		return
	}

	scope := "all contacts"
	if contactName != "" {
		scope = contactName
	}

	fmt.Printf("Conversation analytics for %s (last %d days):\n", scope, *days)
	fmt.Printf("Total: %d | Sent: %d | Received: %d | Avg/day: %.1f\n", analytics.TotalMessages, analytics.SentCount, analytics.ReceivedCount, analytics.AvgDailyMessages)
	if analytics.BusiestHour != nil {
		fmt.Printf("Busiest hour: %d\n", *analytics.BusiestHour)
	}
	if analytics.BusiestDay != "" {
		fmt.Printf("Busiest day: %s\n", analytics.BusiestDay)
	}
	if len(analytics.TopContacts) > 0 {
		fmt.Println("Top contacts:")
		for _, c := range analytics.TopContacts {
			fmt.Printf("  %s: %d messages\n", c.Phone, c.MessageCount)
		}
	}
	fmt.Printf("Attachments: %d | Reactions: %d\n", analytics.AttachmentCount, analytics.ReactionCount)
}

func handleFollowUp(args []string) {
	fs := flag.NewFlagSet("followup", flag.ExitOnError)
	days := fs.Int("days", 7, "Days to look back")
	stale := fs.Int("stale", 2, "Days before conversation is stale")
	limit := fs.Int("limit", 50, "Max items per category")
	asJSON := fs.Bool("json", false, "Output as JSON")
	_, dbPath := addSharedFlags(fs)
	fs.Parse(args)

	ctx, err := loadContext(defaultContactsPath(), *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	followups, err := ctx.messages.DetectFollowUpNeeded(*days, *stale, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if *asJSON {
		outputJSON(followups)
		return
	}

	fmt.Println("Follow-ups Needed:")
	fmt.Println("----------------------------------------")
	printFollowUpCategory("Unanswered questions", followups.UnansweredQuestions)
	printFollowUpCategory("Pending promises", followups.PendingPromises)
	printFollowUpCategory("Waiting on them", followups.WaitingOnThem)
	printFollowUpCategory("Stale conversations", followups.StaleConversations)
	printFollowUpCategory("Time-sensitive", followups.TimeSensitive)
}

func printFollowUpCategory(title string, items []FollowUpItem) {
	if len(items) == 0 {
		return
	}
	fmt.Println(title + ":")
	for _, item := range items {
		fmt.Printf("- %s: %s\n", item.Phone, truncateText(item.Text, 120))
	}
	fmt.Println()
}

// ==== Helpers ====

func extractText(row map[string]interface{}) string {
	if text := stringValue(row["text"]); text != "" {
		return text
	}
	return "[message content not available]"
}

func stringValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%.0f", val)
	case json.Number:
		return val.String()
	default:
		return ""
	}
}

func numberValue(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

func cocoaToTime(raw interface{}) time.Time {
	ns := int64(numberValue(raw))
	if ns == 0 {
		return time.Time{}
	}
	return cocoaEpoch().Add(time.Duration(ns))
}

func cocoaEpoch() time.Time {
	return time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
}

func normalizeDigits(phone string) string {
	re := regexp.MustCompile(`\D`)
	return re.ReplaceAllString(phone, "")
}

func isGroupChatIdentifier(id string) bool {
	if id == "" {
		return false
	}
	if strings.HasPrefix(id, "chat") && len(id) > 4 {
		rest := id[4:]
		for _, r := range rest {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return strings.Contains(id, ",")
}

func escapeAppleScriptString(s string) string {
	replacer := strings.NewReplacer(`\\`, `\\\\`, `"`, `\\"`)
	return replacer.Replace(s)
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return strings.ReplaceAll(s, "'", "''")
}

func levenshteinDistance(a, b string) int {
	la := len(a)
	lb := len(b)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			dp[i][j] = min(dp[i-1][j]+1, min(dp[i][j-1]+1, dp[i-1][j-1]+cost))
		}
	}
	return dp[la][lb]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func containsIgnoreCase(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func truncateText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

func outputJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func appendLimited(list []FollowUpItem, item FollowUpItem, limit int) []FollowUpItem {
	if len(list) >= limit {
		return list
	}
	return append(list, item)
}

func hasReplyAfter(msgs []Message, idx int) bool {
	current := msgs[idx]
	for i := 0; i < idx; i++ {
		if msgs[i].IsFromMe && msgs[i].Timestamp.After(current.Timestamp) {
			return true
		}
	}
	return false
}

func hasIncomingAfter(msgs []Message, idx int) bool {
	current := msgs[idx]
	for i := 0; i < idx; i++ {
		if !msgs[i].IsFromMe && msgs[i].Timestamp.After(current.Timestamp) {
			return true
		}
	}
	return false
}

func questionPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`\?$`),
		regexp.MustCompile(`\bwhat\b.*\?`),
		regexp.MustCompile(`\bhow\b.*\?`),
		regexp.MustCompile(`\bwhen\b.*\?`),
		regexp.MustCompile(`\bwhere\b.*\?`),
		regexp.MustCompile(`\bwhy\b.*\?`),
		regexp.MustCompile(`\bcan you\b`),
		regexp.MustCompile(`\bcould you\b`),
		regexp.MustCompile(`\bwould you\b`),
	}
}

func promisePatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`\bi'll\b`),
		regexp.MustCompile(`\bi will\b`),
		regexp.MustCompile(`\blet me\b`),
		regexp.MustCompile(`\bgonna\b`),
		regexp.MustCompile(`\bgoing to\b`),
		regexp.MustCompile(`\bwill do\b`),
		regexp.MustCompile(`\bwill get\b`),
		regexp.MustCompile(`\bwill send\b`),
		regexp.MustCompile(`\bwill check\b`),
	}
}

func waitingPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`\bwaiting for\b`),
		regexp.MustCompile(`\blet me know\b`),
		regexp.MustCompile(`\bget back to\b`),
		regexp.MustCompile(`\bhear from\b`),
		regexp.MustCompile(`\bkeep me posted\b`),
		regexp.MustCompile(`\bkeep me updated\b`),
		regexp.MustCompile(`\blmk\b`),
	}
}

func timeReferencePatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`\btomorrow\b`),
		regexp.MustCompile(`\bnext week\b`),
		regexp.MustCompile(`\bmonday\b`),
		regexp.MustCompile(`\btuesday\b`),
		regexp.MustCompile(`\bwednesday\b`),
		regexp.MustCompile(`\bthursday\b`),
		regexp.MustCompile(`\bfriday\b`),
		regexp.MustCompile(`\bsaturday\b`),
		regexp.MustCompile(`\bsunday\b`),
		regexp.MustCompile(`\bthis week\b`),
		regexp.MustCompile(`\bend of day\b`),
		regexp.MustCompile(`\beod\b`),
		regexp.MustCompile(`\basap\b`),
		regexp.MustCompile(`\bsoon\b`),
	}
}
