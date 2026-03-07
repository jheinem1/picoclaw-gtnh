package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DiscordToken string
	PollInterval time.Duration
	WorkDir      string
	HTTPTimeout  time.Duration
	Board        BoardConfig
	InProgress   InProgressConfig
}

type BoardConfig struct {
	Enabled           bool
	ChannelID         string
	Title             string
	MaxItemsPerColumn int
	StateFile         string
	PinMessage        bool
	TasksCommand      string
}

type InProgressConfig struct {
	Enabled      bool
	ChannelID    string
	StateFile    string
	TasksCommand string
	MaxUpdates   int
}

type SyncState struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	LastHash  string `json:"last_hash"`
}

type InProgressMessageState struct {
	MessageID string `json:"message_id"`
	LastHash  string `json:"last_hash"`
}

type InProgressSyncState struct {
	ChannelID    string                            `json:"channel_id"`
	TaskMessages map[string]InProgressMessageState `json:"task_messages"`
}

type BoardSummary struct {
	Total  int `json:"total"`
	Todo   int `json:"todo"`
	Doing  int `json:"doing"`
	Paused int `json:"paused"`
	Done   int `json:"done"`
}

type BoardTask struct {
	ID           int    `json:"id"`
	Status       string `json:"status"`
	Priority     string `json:"priority"`
	Area         string `json:"area"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	Title        string `json:"title"`
	SortKey      int    `json:"sort_key"`
	Owner        string `json:"owner"`
	PausedReason string `json:"paused_reason"`
	Description  string `json:"description"`
}

type BoardColumns struct {
	Todo   []BoardTask `json:"todo"`
	Doing  []BoardTask `json:"doing"`
	Paused []BoardTask `json:"paused"`
	Done   []BoardTask `json:"done"`
}

type BoardPayload struct {
	Board   string       `json:"board"`
	Summary BoardSummary `json:"summary"`
	Columns BoardColumns `json:"columns"`
}

type StatusUpdate struct {
	Timestamp string `json:"timestamp"`
	Author    string `json:"author"`
	Text      string `json:"text"`
}

type InProgressTask struct {
	ID            int            `json:"id"`
	Status        string         `json:"status"`
	Priority      string         `json:"priority"`
	Area          string         `json:"area"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
	Title         string         `json:"title"`
	SortKey       int            `json:"sort_key"`
	Owner         string         `json:"owner"`
	PausedReason  string         `json:"paused_reason"`
	Description   string         `json:"description"`
	StatusUpdates []StatusUpdate `json:"status_updates"`
}

type InProgressPayload struct {
	Tasks []InProgressTask `json:"tasks"`
	Count int              `json:"count"`
}

type DiscordMessage struct {
	ID string `json:"id"`
}

type DiscordChannel struct {
	ID      string `json:"id"`
	GuildID string `json:"guild_id"`
}

type DiscordMessagePayload struct {
	Embeds []DiscordEmbed `json:"embeds"`
}

type DiscordEmbed struct {
	Title       string             `json:"title,omitempty"`
	Description string             `json:"description,omitempty"`
	Color       int                `json:"color,omitempty"`
	Fields      []DiscordEmbedItem `json:"fields,omitempty"`
	Footer      *DiscordFooter     `json:"footer,omitempty"`
}

type DiscordEmbedItem struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type DiscordFooter struct {
	Text string `json:"text"`
}

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func loadConfig() Config {
	poll := getenvInt("KANBAN_POLL_INTERVAL_SECONDS", 10)
	if poll < 5 {
		poll = 5
	}

	token := strings.TrimSpace(os.Getenv("KANBAN_DISCORD_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("PICOCLAW_CHANNELS_DISCORD_TOKEN"))
	}

	return Config{
		DiscordToken: token,
		PollInterval: time.Duration(poll) * time.Second,
		WorkDir:      getenv("KANBAN_WORKDIR", "/root/.picoclaw/workspace"),
		HTTPTimeout:  time.Duration(getenvInt("KANBAN_HTTP_TIMEOUT_SECONDS", 15)) * time.Second,
		Board: BoardConfig{
			Enabled:           getenvBool("KANBAN_ENABLED", false),
			ChannelID:         strings.TrimSpace(getenv("KANBAN_CHANNEL_ID", "")),
			Title:             getenv("KANBAN_TITLE", "GTNH Kanban Board"),
			MaxItemsPerColumn: getenvInt("KANBAN_MAX_ITEMS_PER_COLUMN", 15),
			StateFile:         getenv("KANBAN_STATE_FILE", "/var/lib/kanban-sync/state.json"),
			PinMessage:        getenvBool("KANBAN_PIN_MESSAGE", true),
			TasksCommand:      getenv("KANBAN_TASKS_COMMAND", "sh gtnh_tasks board-json"),
		},
		InProgress: InProgressConfig{
			Enabled:      getenvBool("KANBAN_IN_PROGRESS_ENABLED", false),
			ChannelID:    strings.TrimSpace(getenv("KANBAN_IN_PROGRESS_CHANNEL_ID", "")),
			StateFile:    getenv("KANBAN_IN_PROGRESS_STATE_FILE", "/var/lib/kanban-sync/in-progress-state.json"),
			TasksCommand: getenv("KANBAN_IN_PROGRESS_TASKS_COMMAND", "sh gtnh_tasks in-progress-json"),
			MaxUpdates:   getenvInt("KANBAN_IN_PROGRESS_MAX_UPDATES", 8),
		},
	}
}

func loadState(path string) SyncState {
	st := SyncState{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("event=kanban_state_load_error file=%q err=%q", path, err.Error())
		}
		return st
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		log.Printf("event=kanban_state_parse_error file=%q err=%q", path, err.Error())
		return SyncState{}
	}
	return st
}

func loadInProgressState(path string) InProgressSyncState {
	st := InProgressSyncState{TaskMessages: map[string]InProgressMessageState{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("event=in_progress_state_load_error file=%q err=%q", path, err.Error())
		}
		return st
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		log.Printf("event=in_progress_state_parse_error file=%q err=%q", path, err.Error())
		return InProgressSyncState{TaskMessages: map[string]InProgressMessageState{}}
	}
	if st.TaskMessages == nil {
		st.TaskMessages = map[string]InProgressMessageState{}
	}
	return st
}

func saveJSONFile(path string, v any, eventPrefix string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("event=%s_dir_error err=%q", eventPrefix, err.Error())
		return
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("event=%s_encode_error err=%q", eventPrefix, err.Error())
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("event=%s_write_error err=%q", eventPrefix, err.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("event=%s_rename_error err=%q", eventPrefix, err.Error())
	}
}

func saveState(path string, st SyncState) {
	saveJSONFile(path, st, "kanban_state")
}

func saveInProgressState(path string, st InProgressSyncState) {
	saveJSONFile(path, st, "in_progress_state")
}

func runJSONCommand[T any](cfg Config, command string) (T, error) {
	var parsed T
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = cfg.WorkDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return parsed, fmt.Errorf("tasks command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return parsed, fmt.Errorf("tasks json parse failed: %w", err)
	}
	return parsed, nil
}

func runBoardJSON(cfg Config) (BoardPayload, error) {
	return runJSONCommand[BoardPayload](cfg, cfg.Board.TasksCommand)
}

func runInProgressJSON(cfg Config) (InProgressPayload, error) {
	return runJSONCommand[InProgressPayload](cfg, cfg.InProgress.TasksCommand)
}

func cut(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	if max < 4 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func formatLine(t BoardTask) string {
	pri := strings.ToLower(strings.TrimSpace(t.Priority))
	if pri == "" {
		pri = "med"
	}
	if strings.EqualFold(strings.TrimSpace(t.Status), "doing") {
		owner := strings.TrimSpace(t.Owner)
		if owner == "" {
			owner = "unassigned"
		}
		return fmt.Sprintf("%d - \"%s\" [%s] (in progress: %s)", t.ID, cut(t.Title, 52), pri, cut(owner, 28))
	}
	if strings.EqualFold(strings.TrimSpace(t.Status), "paused") {
		reason := strings.TrimSpace(t.PausedReason)
		if reason == "" {
			reason = "blocked"
		}
		return fmt.Sprintf("%d - \"%s\" [%s] (paused: %s)", t.ID, cut(t.Title, 52), pri, cut(reason, 40))
	}
	return fmt.Sprintf("%d - \"%s\" [%s]", t.ID, cut(t.Title, 72), pri)
}

func columnText(tasks []BoardTask, maxItems int) string {
	if len(tasks) == 0 {
		return "```text\n(none)\n```"
	}
	if maxItems <= 0 {
		maxItems = 10
	}
	lines := make([]string, 0, maxItems+1)
	shown := len(tasks)
	if shown > maxItems {
		shown = maxItems
	}
	for i := 0; i < shown; i++ {
		lines = append(lines, formatLine(tasks[i]))
	}
	if len(tasks) > shown {
		lines = append(lines, fmt.Sprintf("... +%d more", len(tasks)-shown))
	}

	value := "```text\n" + strings.Join(lines, "\n") + "\n```"
	if len(value) <= 1024 {
		return value
	}

	for len(lines) > 1 {
		lines = lines[:len(lines)-1]
		value = "```text\n" + strings.Join(lines, "\n") + "\n```"
		if len(value) <= 1024 {
			return value
		}
	}
	return "```text\n" + cut(lines[0], 1000) + "\n```"
}

func renderBoardMessage(cfg BoardConfig, board BoardPayload) DiscordMessagePayload {
	title := cfg.Title
	if strings.TrimSpace(board.Board) != "" {
		title = strings.TrimSpace(board.Board)
	}
	embed := DiscordEmbed{
		Title: title,
		Color: 0xF97316,
		Fields: []DiscordEmbedItem{
			{Name: fmt.Sprintf("Backlog (%d)", board.Summary.Todo), Value: columnText(board.Columns.Todo, cfg.MaxItemsPerColumn), Inline: false},
			{Name: fmt.Sprintf("In Progress (%d)", board.Summary.Doing), Value: columnText(board.Columns.Doing, cfg.MaxItemsPerColumn), Inline: false},
			{Name: fmt.Sprintf("Paused (%d)", board.Summary.Paused), Value: columnText(board.Columns.Paused, cfg.MaxItemsPerColumn), Inline: false},
			{Name: fmt.Sprintf("Completed (%d)", board.Summary.Done), Value: columnText(board.Columns.Done, cfg.MaxItemsPerColumn), Inline: false},
		},
		Footer: &DiscordFooter{Text: fmt.Sprintf("Total tasks: %d | auto-refresh", board.Summary.Total)},
	}
	return DiscordMessagePayload{Embeds: []DiscordEmbed{embed}}
}

func formatStatusUpdates(updates []StatusUpdate, maxUpdates int) string {
	if len(updates) == 0 {
		return "No status updates yet."
	}
	if maxUpdates <= 0 {
		maxUpdates = 8
	}
	lines := make([]string, 0, maxUpdates)
	for i := len(updates) - 1; i >= 0 && len(lines) < maxUpdates; i-- {
		update := updates[i]
		line := "- " + strings.TrimSpace(update.Timestamp)
		if author := strings.TrimSpace(update.Author); author != "" {
			line += " [" + cut(author, 32) + "]"
		}
		text := strings.TrimSpace(update.Text)
		if text != "" {
			line += " " + text
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "No status updates yet."
	}

	value := strings.Join(lines, "\n")
	if len(value) <= 1024 {
		return value
	}
	for len(lines) > 1 {
		lines = lines[:len(lines)-1]
		value = strings.Join(lines, "\n")
		if len(value) <= 1024 {
			return value
		}
	}
	return cut(lines[0], 1024)
}

func renderInProgressMessage(cfg InProgressConfig, task InProgressTask) DiscordMessagePayload {
	description := strings.TrimSpace(task.Description)
	if description == "" {
		description = "No description yet."
	}

	owner := strings.TrimSpace(task.Owner)
	if owner == "" {
		owner = "unassigned"
	}
	priority := strings.TrimSpace(task.Priority)
	if priority == "" {
		priority = "med"
	}
	area := strings.TrimSpace(task.Area)
	if area == "" {
		area = "general"
	}
	updatedAt := strings.TrimSpace(task.UpdatedAt)
	if updatedAt == "" {
		updatedAt = "unknown"
	}

	embed := DiscordEmbed{
		Title:       cut(fmt.Sprintf("%d - %s", task.ID, task.Title), 256),
		Description: cut(description, 4096),
		Color:       0xF97316,
		Fields: []DiscordEmbedItem{
			{Name: "Owner", Value: cut(owner, 1024), Inline: true},
			{Name: "Priority", Value: cut(priority, 1024), Inline: true},
			{Name: "Area", Value: cut(area, 1024), Inline: true},
			{Name: "Updated", Value: cut(updatedAt, 1024), Inline: false},
			{Name: "Status Updates", Value: formatStatusUpdates(task.StatusUpdates, cfg.MaxUpdates), Inline: false},
		},
		Footer: &DiscordFooter{Text: "Removed automatically when the task leaves in progress"},
	}
	return DiscordMessagePayload{Embeds: []DiscordEmbed{embed}}
}

func payloadHash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha1.Sum(raw)
	return hex.EncodeToString(h[:])
}

func newRequest(ctx context.Context, method, url, token string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("User-Agent", "picoclaw-gtnh-kanban-sync/1.0")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func doDiscordJSON(client *http.Client, cfg Config, method, path string, body []byte) ([]byte, int, error) {
	url := "https://discord.com/api/v10" + path
	attempts := 4
	for attempt := 1; attempt <= attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTPTimeout)
		req, err := newRequest(ctx, method, url, cfg.DiscordToken, body)
		if err != nil {
			cancel()
			return nil, 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			if attempt == attempts {
				return nil, 0, err
			}
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		cancel()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return respBody, resp.StatusCode, nil
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			if attempt < attempts {
				retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
				if retryAfter != "" {
					if s, err := strconv.ParseFloat(retryAfter, 64); err == nil && s > 0 {
						time.Sleep(time.Duration(s * float64(time.Second)))
						continue
					}
				}
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
		}

		return respBody, resp.StatusCode, fmt.Errorf("discord API %s %s failed: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil, 0, fmt.Errorf("discord API %s %s failed after retries", method, path)
}

func fetchGuildID(client *http.Client, cfg Config, channelID string) (string, error) {
	body, _, err := doDiscordJSON(client, cfg, http.MethodGet, "/channels/"+channelID, nil)
	if err != nil {
		return "", err
	}
	var channel DiscordChannel
	if err := json.Unmarshal(body, &channel); err != nil {
		return "", err
	}
	return strings.TrimSpace(channel.GuildID), nil
}

func createMessage(client *http.Client, cfg Config, channelID string, payload DiscordMessagePayload) (string, error) {
	raw, _ := json.Marshal(payload)
	body, _, err := doDiscordJSON(client, cfg, http.MethodPost, "/channels/"+channelID+"/messages", raw)
	if err != nil {
		return "", err
	}
	var msg DiscordMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return "", err
	}
	if strings.TrimSpace(msg.ID) == "" {
		return "", fmt.Errorf("discord create message returned empty id")
	}
	return msg.ID, nil
}

func editMessage(client *http.Client, cfg Config, channelID, messageID string, payload DiscordMessagePayload) error {
	raw, _ := json.Marshal(payload)
	_, _, err := doDiscordJSON(client, cfg, http.MethodPatch, "/channels/"+channelID+"/messages/"+messageID, raw)
	return err
}

func deleteMessage(client *http.Client, cfg Config, channelID, messageID string) error {
	_, _, err := doDiscordJSON(client, cfg, http.MethodDelete, "/channels/"+channelID+"/messages/"+messageID, nil)
	return err
}

func pinMessage(client *http.Client, cfg Config, channelID, messageID string) error {
	_, _, err := doDiscordJSON(client, cfg, http.MethodPut, "/channels/"+channelID+"/pins/"+messageID, nil)
	return err
}

func syncBoardOnce(client *http.Client, cfg Config, st *SyncState) error {
	board, err := runBoardJSON(cfg)
	if err != nil {
		return err
	}
	payload := renderBoardMessage(cfg.Board, board)
	hash := payloadHash(payload)

	if st.ChannelID == cfg.Board.ChannelID && st.MessageID != "" && st.LastHash == hash {
		return nil
	}

	if strings.TrimSpace(st.GuildID) == "" {
		guildID, err := fetchGuildID(client, cfg, cfg.Board.ChannelID)
		if err != nil {
			log.Printf("event=kanban_channel_lookup_failed channel_id=%s err=%q", cfg.Board.ChannelID, err.Error())
		} else {
			st.GuildID = guildID
		}
	}

	messageID := st.MessageID
	if strings.TrimSpace(messageID) == "" || st.ChannelID != cfg.Board.ChannelID {
		messageID, err = createMessage(client, cfg, cfg.Board.ChannelID, payload)
		if err != nil {
			return err
		}
		if cfg.Board.PinMessage {
			if err := pinMessage(client, cfg, cfg.Board.ChannelID, messageID); err != nil {
				log.Printf("event=kanban_pin_failed channel_id=%s message_id=%s err=%q", cfg.Board.ChannelID, messageID, err.Error())
			}
		}
		log.Printf("event=kanban_message_created channel_id=%s message_id=%s", cfg.Board.ChannelID, messageID)
	} else {
		err = editMessage(client, cfg, cfg.Board.ChannelID, messageID, payload)
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 404") {
				messageID, err = createMessage(client, cfg, cfg.Board.ChannelID, payload)
				if err != nil {
					return err
				}
				if cfg.Board.PinMessage {
					if err := pinMessage(client, cfg, cfg.Board.ChannelID, messageID); err != nil {
						log.Printf("event=kanban_pin_failed channel_id=%s message_id=%s err=%q", cfg.Board.ChannelID, messageID, err.Error())
					}
				}
				log.Printf("event=kanban_message_recreated channel_id=%s message_id=%s", cfg.Board.ChannelID, messageID)
			} else {
				return err
			}
		} else {
			log.Printf("event=kanban_message_updated channel_id=%s message_id=%s", cfg.Board.ChannelID, messageID)
		}
	}

	st.ChannelID = cfg.Board.ChannelID
	st.MessageID = messageID
	st.LastHash = hash
	saveState(cfg.Board.StateFile, *st)
	return nil
}

func deleteTrackedMessages(client *http.Client, cfg Config, channelID string, messages map[string]InProgressMessageState) {
	for taskID, tracked := range messages {
		if strings.TrimSpace(tracked.MessageID) == "" {
			continue
		}
		if err := deleteMessage(client, cfg, channelID, tracked.MessageID); err != nil && !strings.Contains(err.Error(), "HTTP 404") {
			log.Printf("event=in_progress_message_delete_failed channel_id=%s task_id=%s message_id=%s err=%q", channelID, taskID, tracked.MessageID, err.Error())
		}
	}
}

func syncInProgressOnce(client *http.Client, cfg Config, st *InProgressSyncState) error {
	if st.TaskMessages == nil {
		st.TaskMessages = map[string]InProgressMessageState{}
	}
	if st.ChannelID != "" && st.ChannelID != cfg.InProgress.ChannelID {
		deleteTrackedMessages(client, cfg, st.ChannelID, st.TaskMessages)
		st.TaskMessages = map[string]InProgressMessageState{}
		st.ChannelID = cfg.InProgress.ChannelID
	}

	payload, err := runInProgressJSON(cfg)
	if err != nil {
		return err
	}

	sort.Slice(payload.Tasks, func(i, j int) bool {
		if payload.Tasks[i].SortKey == payload.Tasks[j].SortKey {
			return payload.Tasks[i].ID < payload.Tasks[j].ID
		}
		return payload.Tasks[i].SortKey < payload.Tasks[j].SortKey
	})

	seen := make(map[string]struct{}, len(payload.Tasks))
	changed := false

	for _, task := range payload.Tasks {
		taskID := strconv.Itoa(task.ID)
		seen[taskID] = struct{}{}
		msgPayload := renderInProgressMessage(cfg.InProgress, task)
		hash := payloadHash(msgPayload)
		tracked := st.TaskMessages[taskID]

		if st.ChannelID == cfg.InProgress.ChannelID && tracked.MessageID != "" && tracked.LastHash == hash {
			continue
		}

		if strings.TrimSpace(tracked.MessageID) == "" || st.ChannelID != cfg.InProgress.ChannelID {
			messageID, err := createMessage(client, cfg, cfg.InProgress.ChannelID, msgPayload)
			if err != nil {
				return err
			}
			st.TaskMessages[taskID] = InProgressMessageState{MessageID: messageID, LastHash: hash}
			log.Printf("event=in_progress_message_created channel_id=%s task_id=%s message_id=%s", cfg.InProgress.ChannelID, taskID, messageID)
			changed = true
			continue
		}

		err = editMessage(client, cfg, cfg.InProgress.ChannelID, tracked.MessageID, msgPayload)
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 404") {
				messageID, createErr := createMessage(client, cfg, cfg.InProgress.ChannelID, msgPayload)
				if createErr != nil {
					return createErr
				}
				st.TaskMessages[taskID] = InProgressMessageState{MessageID: messageID, LastHash: hash}
				log.Printf("event=in_progress_message_recreated channel_id=%s task_id=%s message_id=%s", cfg.InProgress.ChannelID, taskID, messageID)
				changed = true
				continue
			}
			return err
		}

		tracked.LastHash = hash
		st.TaskMessages[taskID] = tracked
		log.Printf("event=in_progress_message_updated channel_id=%s task_id=%s message_id=%s", cfg.InProgress.ChannelID, taskID, tracked.MessageID)
		changed = true
	}

	for taskID, tracked := range st.TaskMessages {
		if _, ok := seen[taskID]; ok {
			continue
		}
		if strings.TrimSpace(tracked.MessageID) != "" {
			err := deleteMessage(client, cfg, cfg.InProgress.ChannelID, tracked.MessageID)
			if err != nil && !strings.Contains(err.Error(), "HTTP 404") {
				return err
			}
			log.Printf("event=in_progress_message_deleted channel_id=%s task_id=%s message_id=%s", cfg.InProgress.ChannelID, taskID, tracked.MessageID)
		}
		delete(st.TaskMessages, taskID)
		changed = true
	}

	if st.ChannelID != cfg.InProgress.ChannelID {
		st.ChannelID = cfg.InProgress.ChannelID
		changed = true
	}

	if changed {
		saveInProgressState(cfg.InProgress.StateFile, *st)
	}
	return nil
}

func main() {
	cfg := loadConfig()
	if !cfg.Board.Enabled && !cfg.InProgress.Enabled {
		log.Printf("event=kanban_disabled message=%q", "KANBAN_ENABLED=false and KANBAN_IN_PROGRESS_ENABLED=false, kanban-sync idle")
		for {
			time.Sleep(5 * time.Minute)
		}
	}
	if cfg.DiscordToken == "" {
		log.Fatalf("missing Discord bot token: set KANBAN_DISCORD_TOKEN or PICOCLAW_CHANNELS_DISCORD_TOKEN")
	}
	if cfg.Board.Enabled && cfg.Board.ChannelID == "" {
		log.Fatalf("missing KANBAN_CHANNEL_ID")
	}
	if cfg.InProgress.Enabled && cfg.InProgress.ChannelID == "" {
		log.Fatalf("missing KANBAN_IN_PROGRESS_CHANNEL_ID")
	}

	client := &http.Client{Timeout: cfg.HTTPTimeout + 5*time.Second}
	boardState := loadState(cfg.Board.StateFile)
	inProgressState := loadInProgressState(cfg.InProgress.StateFile)

	if cfg.Board.Enabled {
		if err := syncBoardOnce(client, cfg, &boardState); err != nil {
			log.Printf("event=kanban_sync_error err=%q", err.Error())
		}
	}
	if cfg.InProgress.Enabled {
		if err := syncInProgressOnce(client, cfg, &inProgressState); err != nil {
			log.Printf("event=in_progress_sync_error err=%q", err.Error())
		}
	}

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if cfg.Board.Enabled {
			if err := syncBoardOnce(client, cfg, &boardState); err != nil {
				log.Printf("event=kanban_sync_error err=%q", err.Error())
			}
		}
		if cfg.InProgress.Enabled {
			if err := syncInProgressOnce(client, cfg, &inProgressState); err != nil {
				log.Printf("event=in_progress_sync_error err=%q", err.Error())
			}
		}
	}
}
