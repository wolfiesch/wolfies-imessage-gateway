package main

import (
	"encoding/hex"
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

const defaultDBRelative = "Library/Messages/chat.db"

type contact struct {
	Name             string `json:"name"`
	Phone            string `json:"phone"`
	RelationshipType string `json:"relationship_type"`
	Notes            string `json:"notes"`
}

type message struct {
	Text         string     `json:"text"`
	Timestamp    *time.Time `json:"timestamp,omitempty"`
	IsFromMe     bool       `json:"is_from_me"`
	Phone        string     `json:"phone,omitempty"`
	Sender       string     `json:"sender,omitempty"`
	GroupID      string     `json:"group_id,omitempty"`
	GroupName    string     `json:"group_name,omitempty"`
	DaysOld      int        `json:"days_old,omitempty"`
	MatchSnippet string     `json:"match_snippet,omitempty"`
}

type analyticsSummary struct {
	TotalMessages    int          `json:"total_messages"`
	SentCount        int          `json:"sent_count"`
	ReceivedCount    int          `json:"received_count"`
	AvgDailyMessages float64      `json:"avg_daily_messages"`
	BusiestHour      *int         `json:"busiest_hour,omitempty"`
	BusiestDay       string       `json:"busiest_day,omitempty"`
	TopContacts      []topContact `json:"top_contacts,omitempty"`
	AttachmentCount  int          `json:"attachment_count"`
	ReactionCount    int          `json:"reaction_count"`
}

type topContact struct {
	Phone        string `json:"phone"`
	MessageCount int    `json:"message_count"`
}

type followupBucket struct {
	UnansweredQuestions []map[string]any `json:"unanswered_questions"`
	PendingPromises     []map[string]any `json:"pending_promises"`
	WaitingOnThem       []map[string]any `json:"waiting_on_them"`
	StaleConversations  []map[string]any `json:"stale_conversations"`
	TimeSensitive       []map[string]any `json:"time_sensitive"`
	AnalysisPeriodDays  int              `json:"analysis_period_days"`
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "search":
		err = runSearch(args)
	case "messages":
		err = runMessages(args)
	case "recent":
		err = runRecent(args)
	case "unread":
		err = runUnread(args)
	case "send":
		err = runSend(args)
	case "contacts":
		err = runContacts(args)
	case "analytics":
		err = runAnalytics(args)
	case "followup":
		err = runFollowup(args)
	case "-h", "--help", "help":
		printHelp()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printHelp()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`iMessage Gateway (Go)

Usage:
  imessage-gateway <command> [options]

Commands:
  search <contact>       Search messages with a contact
  messages <contact>     Show recent messages with contact
  recent                 Show recent conversations
  unread                 Show unread incoming messages
  send <contact> <msg>   Send a message
  contacts               List configured contacts
  analytics [contact]    Conversation analytics
  followup               Detect follow-ups needed

Use "<command> -h" for detailed options.
`)
}

func runSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	query := fs.String("query", "", "Text to search for")
	fs.StringVar(query, "q", "", "Text to search for")
	limit := fs.Int("limit", 30, "Max messages to return")
	fs.IntVar(limit, "l", 30, "Max messages to return")
	days := fs.Int("days", 90, "Days to search back")
	fs.IntVar(days, "d", 90, "Days to search back")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("contact name required")
	}

	c, err := resolveContact(fs.Arg(0))
	if err != nil {
		return err
	}

	msgs, err := getMessagesByPhone(c.Phone, *limit, *days)
	if err != nil {
		return err
	}

	if *query != "" {
		filtered := msgs[:0]
		qLower := strings.ToLower(*query)
		for _, m := range msgs {
			if strings.Contains(strings.ToLower(m.Text), qLower) {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}

	fmt.Printf("Messages with %s (%s):\n", c.Name, c.Phone)
	fmt.Println(strings.Repeat("-", 60))
	for _, m := range msgs {
		sender := c.Name
		if m.IsFromMe {
			sender = "Me"
		}
		ts := ""
		if m.Timestamp != nil {
			ts = m.Timestamp.Format(time.RFC3339)
		}
		fmt.Printf("%s | %s: %s\n", ts, sender, truncate(m.Text, 200))
	}

	return nil
}

func runMessages(args []string) error {
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "Max messages to return")
	fs.IntVar(limit, "l", 20, "Max messages to return")
	asJSON := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("contact name required")
	}

	c, err := resolveContact(fs.Arg(0))
	if err != nil {
		return err
	}

	msgs, err := getMessagesByPhone(c.Phone, *limit, 365*5)
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(msgs)
	}

	for _, m := range msgs {
		sender := c.Name
		if m.IsFromMe {
			sender = "Me"
		}
		fmt.Printf("%s: %s\n", sender, truncate(m.Text, 200))
	}
	return nil
}

func runRecent(args []string) error {
	fs := flag.NewFlagSet("recent", flag.ContinueOnError)
	limit := fs.Int("limit", 10, "Max conversations")
	fs.IntVar(limit, "l", 10, "Max conversations")
	asJSON := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	conversations, err := getRecentConversations(*limit)
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(conversations)
	}

	fmt.Println("Recent Conversations:")
	fmt.Println(strings.Repeat("-", 60))
	for _, conv := range conversations {
		ts := ""
		if conv.Timestamp != nil {
			ts = conv.Timestamp.Format(time.RFC3339)
		}
		preview := truncate(conv.Text, 80)
		fmt.Printf("%s: %s (%s)\n", conv.Phone, preview, ts)
	}
	return nil
}

func runUnread(args []string) error {
	fs := flag.NewFlagSet("unread", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "Max messages")
	fs.IntVar(limit, "l", 20, "Max messages")
	asJSON := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	msgs, err := getUnreadMessages(*limit)
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(msgs)
	}

	if len(msgs) == 0 {
		fmt.Println("No unread messages.")
		return nil
	}

	fmt.Printf("Unread Messages (%d):\n", len(msgs))
	fmt.Println(strings.Repeat("-", 60))
	for _, m := range msgs {
		sender := m.Phone
		if m.GroupName != "" {
			sender = m.GroupName
		}
		fmt.Printf("%s: %s\n", sender, truncate(m.Text, 150))
	}
	return nil
}

func runSend(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: send <contact> <message>")
	}
	contactName := args[0]
	messageText := strings.Join(args[1:], " ")

	c, err := resolveContact(contactName)
	if err != nil {
		return err
	}

	if err := sendAppleScript(c.Phone, messageText); err != nil {
		return err
	}

	fmt.Printf("Sent to %s (%s): %s\n", c.Name, c.Phone, truncate(messageText, 50))
	return nil
}

func runContacts(args []string) error {
	fs := flag.NewFlagSet("contacts", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	contacts, err := loadContacts()
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(contacts)
	}

	fmt.Printf("Contacts (%d):\n", len(contacts))
	fmt.Println(strings.Repeat("-", 40))
	for _, c := range contacts {
		fmt.Printf("%s: %s\n", c.Name, c.Phone)
	}
	return nil
}

func runAnalytics(args []string) error {
	fs := flag.NewFlagSet("analytics", flag.ContinueOnError)
	days := fs.Int("days", 30, "Days to analyze")
	fs.IntVar(days, "d", 30, "Days to analyze")
	asJSON := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var phone string
	if fs.NArg() >= 1 {
		c, err := resolveContact(fs.Arg(0))
		if err != nil {
			return err
		}
		phone = c.Phone
	}

	summary, err := buildAnalytics(phone, *days)
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(summary)
	}

	fmt.Println("Conversation Analytics:")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("Total messages: %d\n", summary.TotalMessages)
	fmt.Printf("Sent: %d | Received: %d\n", summary.SentCount, summary.ReceivedCount)
	fmt.Printf("Avg daily: %.1f over last %d days\n", summary.AvgDailyMessages, *days)
	if summary.BusiestHour != nil {
		fmt.Printf("Busiest hour: %02d:00\n", *summary.BusiestHour)
	}
	if summary.BusiestDay != "" {
		fmt.Printf("Busiest day: %s\n", summary.BusiestDay)
	}
	fmt.Printf("Attachments: %d | Reactions: %d\n", summary.AttachmentCount, summary.ReactionCount)
	if phone == "" && len(summary.TopContacts) > 0 {
		fmt.Println("Top contacts:")
		for _, tc := range summary.TopContacts {
			fmt.Printf("  %s: %d messages\n", tc.Phone, tc.MessageCount)
		}
	}
	return nil
}

func runFollowup(args []string) error {
	fs := flag.NewFlagSet("followup", flag.ContinueOnError)
	days := fs.Int("days", 7, "Days to look back")
	fs.IntVar(days, "d", 7, "Days to look back")
	stale := fs.Int("stale", 2, "Min stale days")
	fs.IntVar(stale, "s", 2, "Min stale days")
	asJSON := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := detectFollowups(*days, *stale, 50)
	if err != nil {
		return err
	}

	if *asJSON {
		return printJSON(result)
	}

	if len(result.UnansweredQuestions) == 0 &&
		len(result.PendingPromises) == 0 &&
		len(result.WaitingOnThem) == 0 &&
		len(result.StaleConversations) == 0 &&
		len(result.TimeSensitive) == 0 {
		fmt.Println("No follow-ups needed.")
		return nil
	}

	printFollowupSection("Unanswered Questions", result.UnansweredQuestions)
	printFollowupSection("Pending Promises", result.PendingPromises)
	printFollowupSection("Waiting On Them", result.WaitingOnThem)
	printFollowupSection("Stale Conversations", result.StaleConversations)
	printFollowupSection("Time Sensitive", result.TimeSensitive)
	return nil
}

// Core helpers

func resolveContact(name string) (*contact, error) {
	contacts, err := loadContacts()
	if err != nil {
		return nil, err
	}

	lower := strings.ToLower(name)
	for _, c := range contacts {
		if strings.ToLower(c.Name) == lower {
			return &c, nil
		}
	}
	for _, c := range contacts {
		if strings.Contains(strings.ToLower(c.Name), lower) {
			return &c, nil
		}
	}

	return nil, fmt.Errorf("contact '%s' not found", name)
}

func loadContacts() ([]contact, error) {
	path := filepath.Join(findRepoRoot(), "config", "contacts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read contacts: %w", err)
	}

	var payload struct {
		Contacts []contact `json:"contacts"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("could not parse contacts: %w", err)
	}

	return payload.Contacts, nil
}

func findRepoRoot() string {
	starts := []string{}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}

	for _, start := range starts {
		dir := start
		for {
			if _, err := os.Stat(filepath.Join(dir, "config", "contacts.json")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	if len(starts) > 0 {
		return starts[0]
	}
	return "."
}

func messagesDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultDBRelative), nil
}

func getMessagesByPhone(phone string, limit int, days int) ([]message, error) {
	phonePattern := escapeLiteral("%" + phone + "%")
	query := fmt.Sprintf(`
		SELECT m.text as text,
			hex(m.attributedBody) as attributed_body,
			m.date as date,
			m.is_from_me as is_from_me,
			m.cache_roomnames as cache_roomnames,
			h.id as handle_id
		FROM message m
		JOIN handle h ON m.handle_id = h.ROWID
		WHERE h.id LIKE %s
	`, phonePattern)

	if days > 0 {
		cutoff := cocoaTimestamp(time.Now().Add(-time.Duration(days) * 24 * time.Hour))
		query += fmt.Sprintf(" AND m.date >= %d", cutoff)
	}

	query += fmt.Sprintf(" ORDER BY m.date DESC LIMIT %d;", limit)

	rows, err := runSQLiteJSON(query)
	if err != nil {
		return nil, err
	}

	msgs := make([]message, 0, len(rows))
	for _, row := range rows {
		msgs = append(msgs, buildMessageFromRow(row))
	}
	return msgs, nil
}

func getRecentConversations(limit int) ([]message, error) {
	query := fmt.Sprintf(`
		SELECT m.text as text,
			hex(m.attributedBody) as attributed_body,
			m.date as date,
			m.is_from_me as is_from_me,
			h.id as handle_id,
			m.cache_roomnames as cache_roomnames
		FROM message m
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		ORDER BY m.date DESC
		LIMIT %d;
	`, limit)

	rows, err := runSQLiteJSON(query)
	if err != nil {
		return nil, err
	}

	msgs := make([]message, 0, len(rows))
	for _, row := range rows {
		msgs = append(msgs, buildMessageFromRow(row))
	}
	return msgs, nil
}

func getUnreadMessages(limit int) ([]message, error) {
	query := fmt.Sprintf(`
		SELECT m.text as text,
			hex(m.attributedBody) as attributed_body,
			m.date as date,
			h.id as handle_id,
			m.cache_roomnames as cache_roomnames,
			c.display_name as display_name
		FROM message m
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
		LEFT JOIN chat c ON cmj.chat_id = c.ROWID
		WHERE m.is_read = 0
			AND m.is_from_me = 0
			AND m.is_finished = 1
			AND m.is_system_message = 0
			AND m.item_type = 0
		ORDER BY m.date DESC
		LIMIT %d;
	`, limit)

	rows, err := runSQLiteJSON(query)
	if err != nil {
		return nil, err
	}

	msgs := make([]message, 0, len(rows))
	now := time.Now()
	for _, row := range rows {
		msg := buildMessageFromRow(row)
		if msg.Timestamp != nil {
			msg.DaysOld = int(now.Sub(*msg.Timestamp).Hours() / 24)
		}
		msg.GroupName = stringFromRow(row, "display_name")
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func buildMessageFromRow(row map[string]any) message {
	text := stringFromRow(row, "text")
	bodyHex := stringFromRow(row, "attributed_body")
	if text == "" && bodyHex != "" {
		if decoded, err := hex.DecodeString(bodyHex); err == nil {
			text = extractTextFromBlob(decoded)
		}
	}
	if text == "" {
		text = "[message content not available]"
	}

	ts := parseCocoaTimestamp(row["date"])
	isFromMe := boolFromRow(row["is_from_me"])

	msg := message{
		Text:      text,
		Timestamp: ts,
		IsFromMe:  isFromMe,
		Phone:     stringFromRow(row, "handle_id"),
		Sender:    stringFromRow(row, "handle_id"),
		GroupID:   stringFromRow(row, "cache_roomnames"),
	}
	return msg
}

func extractTextFromBlob(blob []byte) string {
	if len(blob) == 0 {
		return ""
	}
	str := string(blob)
	re := regexp.MustCompile(`[^\x00-\x1f\x7f-\x9f]{3,}`)
	skips := []string{"NSString", "NSKeyed", "NSObject", "NSDictionary", "NSMutable"}
	longest := ""
	for _, match := range re.FindAllString(str, -1) {
		trimmed := strings.Trim(match, "+ ")
		skip := false
		for _, pat := range skips {
			if strings.Contains(trimmed, pat) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if len(trimmed) > len(longest) {
			longest = trimmed
		}
	}
	return strings.TrimSpace(longest)
}

func parseCocoaTimestamp(value any) *time.Time {
	var v int64
	switch t := value.(type) {
	case float64:
		v = int64(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			v = n
		}
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			v = n
		}
	}
	if v == 0 {
		return nil
	}
	ts := cocoaToTime(v)
	return &ts
}

func cocoaToTime(value int64) time.Time {
	base := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(value))
}

func cocoaTimestamp(t time.Time) int64 {
	base := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return t.Sub(base).Nanoseconds()
}

func stringFromRow(row map[string]any, key string) string {
	val, ok := row[key]
	if !ok || val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func boolFromRow(val any) bool {
	switch v := val.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case json.Number:
		n, _ := v.Int64()
		return n != 0
	default:
		return false
	}
}

func runSQLiteJSON(query string) ([]map[string]any, error) {
	dbPath, err := messagesDBPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("messages database not found at %s", dbPath)
	}

	cmd := exec.Command("sqlite3", "-json", dbPath, query)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite3 error: %s", strings.TrimSpace(string(output)))
	}
	if len(output) == 0 {
		return []map[string]any{}, nil
	}

	var rows []map[string]any
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("could not parse sqlite output: %w", err)
	}
	return rows, nil
}

func escapeLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func sendAppleScript(phone, text string) error {
	escapedText := strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\\"`)
	escapedPhone := strings.ReplaceAll(strings.ReplaceAll(phone, `\`, `\\`), `"`, `\\"`)
	script := fmt.Sprintf(`
tell application "Messages"
	set targetService to 1st account whose service type = iMessage
	set targetBuddy to participant "%s" of targetService
	send "%s" to targetBuddy
end tell
`, escapedPhone, escapedText)

	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to send message: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Analytics and follow-up

func buildAnalytics(phone string, days int) (analyticsSummary, error) {
	cutoff := cocoaTimestamp(time.Now().Add(-time.Duration(days) * 24 * time.Hour))
	filter := fmt.Sprintf("m.date >= %d", cutoff)
	if phone != "" {
		filter += fmt.Sprintf(" AND h.id LIKE %s", escapeLiteral("%"+phone+"%"))
	}

	summary := analyticsSummary{}

	totalsQuery := fmt.Sprintf(`
		SELECT COUNT(*) as total,
		SUM(CASE WHEN m.is_from_me = 1 THEN 1 ELSE 0 END) as sent,
		SUM(CASE WHEN m.is_from_me = 0 THEN 1 ELSE 0 END) as received
		FROM message m
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE %s
		AND (m.associated_message_type IS NULL OR m.associated_message_type = 0);
	`, filter)
	if rows, err := runSQLiteJSON(totalsQuery); err == nil && len(rows) == 1 {
		summary.TotalMessages = intFromRow(rows[0], "total")
		summary.SentCount = intFromRow(rows[0], "sent")
		summary.ReceivedCount = intFromRow(rows[0], "received")
	}
	if days > 0 {
		summary.AvgDailyMessages = float64(summary.TotalMessages) / float64(days)
	}

	hourQuery := fmt.Sprintf(`
		SELECT CAST((m.date / 1000000000 / 3600) %% 24 AS INTEGER) as hour,
		COUNT(*) as count
		FROM message m
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE %s
		GROUP BY hour
		ORDER BY count DESC
		LIMIT 1;
	`, filter)
	if rows, err := runSQLiteJSON(hourQuery); err == nil && len(rows) == 1 {
		h := intFromRow(rows[0], "hour")
		summary.BusiestHour = &h
	}

	dayQuery := fmt.Sprintf(`
		SELECT CAST((m.date / 1000000000 / 86400 + 1) %% 7 AS INTEGER) as dow,
		COUNT(*) as count
		FROM message m
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE %s
		GROUP BY dow
		ORDER BY count DESC
		LIMIT 1;
	`, filter)
	if rows, err := runSQLiteJSON(dayQuery); err == nil && len(rows) == 1 {
		dow := intFromRow(rows[0], "dow")
		daysOfWeek := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		if dow >= 0 && dow < len(daysOfWeek) {
			summary.BusiestDay = daysOfWeek[dow]
		}
	}

	attachmentQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT a.ROWID) as count
		FROM attachment a
		JOIN message_attachment_join maj ON a.ROWID = maj.attachment_id
		JOIN message m ON maj.message_id = m.ROWID
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE %s;
	`, filter)
	if rows, err := runSQLiteJSON(attachmentQuery); err == nil && len(rows) == 1 {
		summary.AttachmentCount = intFromRow(rows[0], "count")
	}

	reactionQuery := fmt.Sprintf(`
		SELECT COUNT(*) as count
		FROM message m
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE %s
		AND m.associated_message_type BETWEEN 2000 AND 3005;
	`, filter)
	if rows, err := runSQLiteJSON(reactionQuery); err == nil && len(rows) == 1 {
		summary.ReactionCount = intFromRow(rows[0], "count")
	}

	if phone == "" {
		topContactsQuery := fmt.Sprintf(`
			SELECT h.id as handle_id, COUNT(*) as msg_count
			FROM message m
			JOIN handle h ON m.handle_id = h.ROWID
			WHERE m.date >= %d
			AND (m.associated_message_type IS NULL OR m.associated_message_type = 0)
			GROUP BY h.id
			ORDER BY msg_count DESC
			LIMIT 10;
		`, cutoff)
		if rows, err := runSQLiteJSON(topContactsQuery); err == nil {
			for _, row := range rows {
				summary.TopContacts = append(summary.TopContacts, topContact{
					Phone:        stringFromRow(row, "handle_id"),
					MessageCount: intFromRow(row, "msg_count"),
				})
			}
		}
	}

	return summary, nil
}

func detectFollowups(days, staleDays, limit int) (followupBucket, error) {
	cutoff := cocoaTimestamp(time.Now().Add(-time.Duration(days) * 24 * time.Hour))
	query := fmt.Sprintf(`
		SELECT m.text as text,
			hex(m.attributedBody) as attributed_body,
			m.date as date,
			m.is_from_me as is_from_me,
			h.id as handle_id
		FROM message m
		JOIN handle h ON m.handle_id = h.ROWID
		WHERE m.date >= %d
		AND (m.associated_message_type IS NULL OR m.associated_message_type = 0)
		AND m.item_type = 0
		ORDER BY h.id, m.date DESC;
	`, cutoff)

	rows, err := runSQLiteJSON(query)
	if err != nil {
		return followupBucket{}, err
	}

	conversations := map[string][]message{}
	for _, row := range rows {
		msg := buildMessageFromRow(row)
		if msg.Phone == "" {
			continue
		}
		conversations[msg.Phone] = append(conversations[msg.Phone], msg)
	}

	result := followupBucket{
		UnansweredQuestions: []map[string]any{},
		PendingPromises:     []map[string]any{},
		WaitingOnThem:       []map[string]any{},
		StaleConversations:  []map[string]any{},
		TimeSensitive:       []map[string]any{},
		AnalysisPeriodDays:  days,
	}

	now := time.Now()
	for phone, msgs := range conversations {
		if len(msgs) == 0 {
			continue
		}
		last := msgs[0]
		if !last.IsFromMe && last.Timestamp != nil {
			if now.Sub(*last.Timestamp) > time.Duration(staleDays)*24*time.Hour {
				result.StaleConversations = append(result.StaleConversations, map[string]any{
					"phone":            phone,
					"last_message":     truncate(last.Text, 120),
					"days_since_reply": int(now.Sub(*last.Timestamp).Hours() / 24),
					"date":             last.Timestamp.Format(time.RFC3339),
				})
			}
		}

		for i, msg := range msgs {
			textLower := strings.ToLower(msg.Text)

			if !msg.IsFromMe && strings.Contains(msg.Text, "?") {
				hasReply := false
				for _, later := range msgs[:i] {
					if later.IsFromMe {
						hasReply = true
						break
					}
				}
				if !hasReply && len(result.UnansweredQuestions) < limit {
					result.UnansweredQuestions = append(result.UnansweredQuestions, map[string]any{
						"phone": phone,
						"text":  truncate(msg.Text, 200),
						"date":  timestampString(msg.Timestamp),
					})
				}
			}

			if msg.IsFromMe && containsPromise(textLower) && len(result.PendingPromises) < limit {
				result.PendingPromises = append(result.PendingPromises, map[string]any{
					"phone": phone,
					"text":  truncate(msg.Text, 200),
					"date":  timestampString(msg.Timestamp),
				})
			}

			if msg.IsFromMe && strings.Contains(textLower, "waiting") && len(result.WaitingOnThem) < limit {
				result.WaitingOnThem = append(result.WaitingOnThem, map[string]any{
					"phone": phone,
					"text":  truncate(msg.Text, 200),
					"date":  timestampString(msg.Timestamp),
				})
			}

			if containsTimeReference(textLower) && len(result.TimeSensitive) < limit {
				result.TimeSensitive = append(result.TimeSensitive, map[string]any{
					"phone": phone,
					"text":  truncate(msg.Text, 200),
					"date":  timestampString(msg.Timestamp),
				})
			}
		}
	}

	return result, nil
}

func containsPromise(text string) bool {
	patterns := []string{
		"i'll", "i will", "let me", "gonna", "going to", "will do", "will get", "will send", "will check",
	}
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

func containsTimeReference(text string) bool {
	patterns := []string{
		"tomorrow", "next week", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday",
		"this week", "end of day", "eod", "asap", "soon",
	}
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

func timestampString(ts *time.Time) string {
	if ts == nil {
		return ""
	}
	return ts.Format(time.RFC3339)
}

func printFollowupSection(title string, items []map[string]any) {
	if len(items) == 0 {
		return
	}
	fmt.Println(title + ":")
	for _, item := range items {
		fmt.Printf("- %s: %s\n", item["phone"], item["text"])
	}
	fmt.Println()
}

func intFromRow(row map[string]any, key string) int {
	val, ok := row[key]
	if !ok || val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}
