package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	agentcore "greggpt-gtnh/internal/agent"
	"greggpt-gtnh/internal/agent/history"

	_ "modernc.org/sqlite"
)

type Config struct {
	BridgeURL            string
	PollInterval         time.Duration
	ConsoleLines         int
	ReplyMaxChars        int
	MaxReplyParts        int
	StateFile            string
	SessionPrefix        string
	AgentTimeout         time.Duration
	HTTPTimeout          time.Duration
	Workspace            string
	HistoryEnabled       bool
	HistoryPath          string
	HistoryMaxMessages   int
	RecallMaxItems       int
	ProgressEnabled      bool
	ProgressMaxUpdates   int
	SteeringPollInterval time.Duration
}

type State struct {
	Initialized bool             `json:"initialized"`
	Seen        map[string]int64 `json:"seen"`
}

type ConsoleEvent struct {
	EventID   string `json:"event_id"`
	Timestamp string `json:"timestamp"`
	Player    string `json:"player"`
	Text      string `json:"text"`
	Triggered bool   `json:"triggered"`
}

type ConsoleResponse struct {
	OK     bool           `json:"ok"`
	Count  int            `json:"count"`
	Events []ConsoleEvent `json:"events"`
}

const (
	greggptDefaultWorkspace = "/root/.greggpt/workspace"
	greggptEnvWorkspace     = "GREGGPT_WORKSPACE"
	greggptEnvAgentTimeout  = "GREGGPT_AGENT_TIMEOUT_SECONDS"
	agentChannelMinecraft   = "minecraft"
)

type AgentRequest struct {
	Channel         string
	Session         string
	Message         string
	RecentHistory   []history.Message
	RecalledContext []history.RecallItem
	OnCommentary    func(string)
	Steering        <-chan agentcore.SteeringMessage
}

type AgentResponse struct {
	Text string
}

type AgentRunner interface {
	Run(context.Context, AgentRequest) (AgentResponse, error)
}

type HistoryStore interface {
	AppendMessage(context.Context, history.Message) error
	Recent(context.Context, history.Query) ([]history.Message, error)
	Recall(context.Context, history.Query) ([]history.RecallItem, error)
}

type sharedAgentRunner struct {
	runner *agentcore.Runner
}

type startupErrorAgentRunner struct {
	err error
}

func (r *sharedAgentRunner) Run(ctx context.Context, req AgentRequest) (AgentResponse, error) {
	if r == nil || r.runner == nil {
		return AgentResponse{}, errors.New("agent runner is nil")
	}
	coreReq := agentcore.Request{
		Channel: agentcore.Channel(req.Channel),
		User:    req.Session,
		Message: req.Message,
		Context: map[string]string{
			"session": req.Session,
		},
		History:         append([]history.Message(nil), req.RecentHistory...),
		RecalledContext: append([]history.RecallItem(nil), req.RecalledContext...),
		OnCommentary:    req.OnCommentary,
		Steering:        req.Steering,
	}
	text, err := r.runner.Run(ctx, coreReq)
	if err != nil {
		return AgentResponse{}, err
	}
	return AgentResponse{Text: text}, nil
}

func (r startupErrorAgentRunner) Run(context.Context, AgentRequest) (AgentResponse, error) {
	return AgentResponse{}, r.err
}

var unresolvedRe = regexp.MustCompile(`(?i)(could not resolve|no exact recipe chain found|no exact recipe|no recipe (chain )?found|not found)`)
var turnIntoRe = regexp.MustCompile(`(?i)turn\s+(.+?)\s+into\s+(.+?)(?:\?|$)`)
var refineIntoRe = regexp.MustCompile(`(?i)what does\s+(.+?)\s+refine into(?:\?|$)`)
var makeRe = regexp.MustCompile(`(?i)(?:how much .* to )?make\s+(?:a|an|the)?\s*(.+?)(?:\?|$)`)
var specificGTRe = regexp.MustCompile(`(?i)\b(recipe|recipes|refine|smelt|craft|make|turn .* into|what does .* (do|refine)|ore|dust|ingot|plate|rod|pickaxe|tool material|supplies|materials|missing)\b`)
var gtnhDomainRe = regexp.MustCompile(`(?i)\b(gtnh|gregtech|steam|pipe|fluid|throughput|boiler|turbine|lv|mv|hv|ev|iv|luv|zpm|uv|machine|multiblock|ore|dust|ingot|plate|rod|cable|wire)\b`)
var taskBoardRe = regexp.MustCompile(`(?i)\b(task\s*board|tasks?\s+board|open\s+tasks?|task\s+list)\b`)
var taskMutationIntentRe = regexp.MustCompile(`(?i)\b(assign|reassign|move|pause|unpause|resume|reopen|describe|description|update|status update|progress update)\b`)
var inventoryIntentRe = regexp.MustCompile(`(?i)\b(who has|where is|which chest|inventory|inventories|in chests?|in my chest|has item|holding|stored|storage|already have|in me|me system|do we have)\b`)
var materialIntentRe = regexp.MustCompile(`(?i)\b(supplies|materials|missing|already have|in storage|stored)\b`)
var safetyGuardReplyRe = regexp.MustCompile(`(?i)safety guard|dangerous pattern`)
var coordTupleCountRe = regexp.MustCompile(`\((-?\d+),(-?\d+),(-?\d+)\)(?:×|:)(\d+)`)
var coordTupleDimCountRe = regexp.MustCompile(`\((-?\d+),(-?\d+),(-?\d+)\)\s*dim\s*=?\s*(-?\d+)\s*count\s*=?\s*(\d+)`)
var coordTupleCountDimRe = regexp.MustCompile(`\((-?\d+),(-?\d+),(-?\d+)\)\s*count\s*=?\s*(\d+)\s*dim\s*=?\s*(-?\d+)`)
var coordTupleDimRe = regexp.MustCompile(`\((-?\d+),(-?\d+),(-?\d+)\)\s*dim\s*=?\s*(-?\d+)`)
var coordDimRe = regexp.MustCompile(`\bdim\s*=?\s*(-?\d+)\b`)
var coordTupleRe = regexp.MustCompile(`\((-?\d+),(-?\d+),(-?\d+)\)`)

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
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
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
	poll := getenvInt("DATHOST_POLL_INTERVAL_SECONDS", 10)
	if poll < 2 {
		poll = 2
	}

	return Config{
		BridgeURL:            strings.TrimRight(getenv("DATHOST_BRIDGE_URL", "http://dathost-bridge:8080"), "/"),
		PollInterval:         time.Duration(poll) * time.Second,
		ConsoleLines:         getenvInt("DATHOST_CONSOLE_LINES", 500),
		ReplyMaxChars:        getenvInt("MC_REPLY_MAX_CHARS", 180),
		MaxReplyParts:        getenvInt("MC_REPLY_MAX_PARTS", 4),
		StateFile:            getenv("MC_RELAY_STATE_FILE", "/var/lib/mc-relay/state.json"),
		SessionPrefix:        getenv("MC_RELAY_SESSION", "mc:relay"),
		AgentTimeout:         time.Duration(getenvInt(greggptEnvAgentTimeout, 60)) * time.Second,
		HTTPTimeout:          time.Duration(getenvInt("MC_RELAY_HTTP_TIMEOUT_SECONDS", 20)) * time.Second,
		Workspace:            getenv(greggptEnvWorkspace, greggptDefaultWorkspace),
		HistoryEnabled:       getenvBool(agentcore.EnvHistoryEnabled, true),
		HistoryPath:          getenv(agentcore.EnvHistoryPath, agentcore.DefaultHistoryPath),
		HistoryMaxMessages:   getenvInt(agentcore.EnvHistoryMaxMessages, agentcore.DefaultHistoryMessages),
		RecallMaxItems:       getenvInt(agentcore.EnvRecallMaxItems, agentcore.DefaultRecallMaxItems),
		ProgressEnabled:      getenvBool("GREGGPT_MINECRAFT_PROGRESS_ENABLED", true),
		ProgressMaxUpdates:   getenvInt("GREGGPT_MINECRAFT_PROGRESS_MAX_UPDATES", 3),
		SteeringPollInterval: time.Duration(getenvInt("GREGGPT_MINECRAFT_STEERING_POLL_SECONDS", 2)) * time.Second,
	}
}

func loadState(path string) State {
	st := State{Seen: map[string]int64{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("event=state_load_error file=%q err=%q", path, err.Error())
		}
		return st
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		log.Printf("event=state_parse_error file=%q err=%q", path, err.Error())
		return State{Seen: map[string]int64{}}
	}
	if st.Seen == nil {
		st.Seen = map[string]int64{}
	}
	return st
}

func saveState(path string, st State) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("event=state_dir_error file=%q err=%q", path, err.Error())
		return
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Printf("event=state_encode_error file=%q err=%q", path, err.Error())
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("event=state_write_error file=%q err=%q", tmp, err.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("event=state_rename_error file=%q err=%q", path, err.Error())
	}
}

func newHistoryStore(cfg Config) HistoryStore {
	if !cfg.HistoryEnabled {
		return nil
	}
	store, err := history.Open(resolveHistoryPath(cfg))
	if err != nil {
		log.Printf("event=history_open_error path=%q err=%q", resolveHistoryPath(cfg), err.Error())
		return nil
	}
	return store
}

func resolveHistoryPath(cfg Config) string {
	path := strings.TrimSpace(cfg.HistoryPath)
	if path == "" {
		path = agentcore.DefaultHistoryPath
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cfg.Workspace, path)
}

func pruneSeen(st *State, max int) {
	if len(st.Seen) <= max {
		return
	}
	type row struct {
		id string
		ts int64
	}
	rows := make([]row, 0, len(st.Seen))
	for id, ts := range st.Seen {
		rows = append(rows, row{id: id, ts: ts})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ts < rows[j].ts })
	drop := len(rows) - max
	for i := 0; i < drop; i++ {
		delete(st.Seen, rows[i].id)
	}
}

func getConsole(client *http.Client, cfg Config) (ConsoleResponse, error) {
	return getConsoleWithContext(context.Background(), client, cfg)
}

func getConsoleWithContext(ctx context.Context, client *http.Client, cfg Config) (ConsoleResponse, error) {
	url := fmt.Sprintf("%s/mc/console?lines=%d", cfg.BridgeURL, cfg.ConsoleLines)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ConsoleResponse{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ConsoleResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return ConsoleResponse{}, fmt.Errorf("bridge HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out ConsoleResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return ConsoleResponse{}, err
	}
	return out, nil
}

func say(client *http.Client, cfg Config, text string) error {
	payload, _ := json.Marshal(map[string]string{"text": text})
	url := cfg.BridgeURL + "/mc/say"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bridge HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type minecraftCommentaryPublisher struct {
	client    *http.Client
	cfg       Config
	eventID   string
	player    string
	sent      map[string]struct{}
	sentCount int
}

func newMinecraftCommentaryPublisher(client *http.Client, cfg Config, eventID, player string) *minecraftCommentaryPublisher {
	return &minecraftCommentaryPublisher{
		client:  client,
		cfg:     cfg,
		eventID: eventID,
		player:  player,
		sent:    make(map[string]struct{}),
	}
}

func (p *minecraftCommentaryPublisher) Publish(text string) {
	if p == nil || p.client == nil || !p.cfg.ProgressEnabled {
		return
	}
	text = oneSentenceForMC(text, p.cfg.ReplyMaxChars)
	if text == "" {
		return
	}
	if _, exists := p.sent[text]; exists {
		return
	}
	if p.cfg.ProgressMaxUpdates > 0 && p.sentCount >= p.cfg.ProgressMaxUpdates {
		log.Printf("event=commentary_suppressed event_id=%q player=%q reason=max_updates", p.eventID, p.player)
		return
	}
	if err := say(p.client, p.cfg, text); err != nil {
		log.Printf("event=commentary_send_error event_id=%q player=%q err=%q", p.eventID, p.player, err.Error())
		return
	}
	p.sent[text] = struct{}{}
	p.sentCount++
	log.Printf("event=commentary_sent event_id=%q player=%q commentary_len=%d", p.eventID, p.player, len([]rune(text)))
}

func oneSentenceForMC(text string, maxChars int) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i == len(runes)-1 || unicode.IsSpace(runes[i+1]) {
			runes = runes[:i+1]
			break
		}
	}
	parts := splitForMC(string(runes), maxChars, 1)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func startMinecraftSteeringMonitor(client *http.Client, cfg Config, st *State, history HistoryStore, activePlayer string, onCommentary func(string)) (<-chan agentcore.SteeringMessage, func()) {
	steering := make(chan agentcore.SteeringMessage, 32)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	interval := cfg.SteeringPollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				resp, err := getConsoleWithContext(ctx, client, cfg)
				if err != nil {
					log.Printf("event=steering_poll_error player=%q err=%q", activePlayer, err.Error())
					continue
				}
				for _, ev := range resp.Events {
					if !strings.EqualFold(strings.TrimSpace(ev.Player), strings.TrimSpace(activePlayer)) || strings.TrimSpace(ev.Text) == "" {
						continue
					}
					id := firstNonEmpty(ev.EventID, fallbackID(ev))
					if _, seen := st.Seen[id]; seen {
						continue
					}
					message := agentcore.SteeringMessage{Content: strings.TrimSpace(ev.Text), OnCommentary: onCommentary}
					select {
					case steering <- message:
						st.Seen[id] = time.Now().Unix()
						recordMinecraftChat(history, ev)
						pruneSeen(st, 10000)
						saveState(cfg.StateFile, *st)
						log.Printf("event=steering_queued event_id=%q player=%q content_len=%d", id, ev.Player, len(ev.Text))
					default:
						log.Printf("event=steering_dropped event_id=%q player=%q reason=queue_full", id, ev.Player)
					}
				}
			}
		}
	}()
	stop := func() {
		cancel()
		<-done
	}
	return steering, stop
}

func trimChars(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}

func askAgentWithPrompt(runner AgentRunner, cfg Config, session, prompt string, recent []history.Message, recalled []history.RecallItem, onCommentary func(string), steering <-chan agentcore.SteeringMessage) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.AgentTimeout)
	defer cancel()
	out, err := runner.Run(ctx, AgentRequest{
		Channel:         agentChannelMinecraft,
		Session:         session,
		Message:         prompt,
		RecentHistory:   append([]history.Message(nil), recent...),
		RecalledContext: append([]history.RecallItem(nil), recalled...),
		OnCommentary:    onCommentary,
		Steering:        steering,
	})
	if err != nil {
		return "", fmt.Errorf("agent call failed: %w", err)
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return "", errors.New("agent returned empty output")
	}
	return text, nil
}

func askAgent(runner AgentRunner, cfg Config, ev ConsoleEvent, session string, mustVerify bool, recent []history.Message, recalled []history.RecallItem, onCommentary func(string), steering <-chan agentcore.SteeringMessage) (string, error) {
	prompt := fmt.Sprintf("Minecraft player %q says: %q. Reply directly as GregGPT in GTNH context. Keep it concise, ASCII plain text, and do not add a routine summary or '<player> - asking ...' preamble.", ev.Player, ev.Text)
	prompt += "\nTreat clearly fictional Minecraft or factory roleplay as fiction. Give real-world emergency guidance only when the player clearly indicates real-world danger; if that distinction is genuinely unclear, ask one brief clarifying question."
	prompt += "\nDo not claim a command was blocked by safety guard unless a tool call in this same turn returned stderr containing that exact phrase."
	if mustVerify {
		prompt += "\nVerify current facts with the appropriate native tool before answering: recipe_sql for recipes, inventory tools for live stock and locations, quest tools for progression, and GTNH wiki lookup for documentation. Preserve returned quantities and freshness exactly. If the result is ambiguous, ask one concise clarifying question."
	}

	reply, err := askAgentWithPrompt(runner, cfg, session, prompt, recent, recalled, onCommentary, steering)
	if err == nil {
		return reply, nil
	}

	// Retry once with stricter guidance in a fresh session to avoid bad tool-call loops.
	retryPrompt := fmt.Sprintf(
		"Reply directly and concisely to Minecraft player %q in GTNH context, without an '<player> - asking ...' preamble. If a required lookup fails, say you could not resolve it from the current snapshot.", ev.Player,
	)
	retryReply, retryErr := askAgentWithPrompt(runner, cfg, session+":retry", retryPrompt+"\n\nUser: "+ev.Text, recent, recalled, onCommentary, steering)
	if retryErr == nil {
		return retryReply, nil
	}
	return "", fmt.Errorf("primary error: %v | retry error: %v", err, retryErr)
}

func fallbackReply(cfg Config, ev ConsoleEvent) string {
	msg := "I hit a lookup error on that one."
	if hint := buildLookupHint(cfg, ev.Text); hint != "" {
		return msg + " " + hint
	}
	return msg + " Ask again with the exact item name and I'll retry."
}

func workspaceCommand(ctx context.Context, cfg Config, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cfg.Workspace
	return cmd
}

func sessionForEvent(cfg Config, eventID string) string {
	if strings.TrimSpace(eventID) == "" {
		return cfg.SessionPrefix
	}
	return cfg.SessionPrefix + ":" + eventID
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func normalizeQueryText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	for _, p := range []string{"greg ", "greg,", "greg:", "greg;"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimSpace(strings.TrimPrefix(s, p))
			break
		}
	}
	s = strings.TrimSpace(s)
	s = strings.Trim(s, " ?!.,;:\"'")
	return s
}

func extractCandidateTerms(text string) []string {
	raw := normalizeQueryText(text)
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 3)
	add := func(v string) {
		v = strings.TrimSpace(strings.Trim(v, " ?!.,;:\"'"))
		if v == "" {
			return
		}
		for _, existing := range out {
			if strings.EqualFold(existing, v) {
				return
			}
		}
		out = append(out, v)
	}

	if m := turnIntoRe.FindStringSubmatch(raw); len(m) == 3 {
		add(m[1])
		add(m[2])
		return out
	}
	if m := makeRe.FindStringSubmatch(raw); len(m) == 2 {
		add(m[1])
	}
	if m := refineIntoRe.FindStringSubmatch(raw); len(m) == 2 {
		add(m[1])
		return out
	}
	add(raw)
	return out
}

func findItemMatches(cfg Config, query string, limit int) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	dbPath := filepath.Join(cfg.Workspace, "gtnh-data", "index", "greggpt_recipes.sqlite")
	u := url.URL{Scheme: "file", Path: dbPath}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()

	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	like := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := db.QueryContext(ctx, `
		SELECT display_name
		FROM items
		WHERE lower(COALESCE(display_name, '')) LIKE ?
		   OR lower(registry_name) LIKE ?
		   OR lower(COALESCE(unlocalized_name, '')) LIKE ?
		ORDER BY
			CASE
				WHEN lower(COALESCE(display_name, '')) = lower(?) THEN 0
				WHEN lower(COALESCE(display_name, '')) LIKE lower(?) THEN 1
				ELSE 2
			END,
			display_name
		LIMIT ?`, like, like, like, query, query+"%", limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]string, 0, limit)
	for rows.Next() {
		var raw sql.NullString
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(raw.String)
		if name == "" {
			continue
		}
		dup := false
		for _, ex := range results {
			if strings.EqualFold(ex, name) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		results = append(results, name)
		if len(results) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func buildLookupHint(cfg Config, question string) string {
	terms := extractCandidateTerms(question)
	if len(terms) == 0 {
		return ""
	}

	hints := make([]string, 0, len(terms))
	for _, term := range terms {
		matches, err := findItemMatches(cfg, term, 2)
		if err != nil || len(matches) == 0 {
			continue
		}
		hints = append(hints, fmt.Sprintf("%s -> %s", term, strings.Join(matches, ", ")))
	}

	if len(hints) == 0 {
		return "Try exact item naming (for example: cassiterite, yellow garnet dust)."
	}
	return "Closest matches: " + strings.Join(hints, " | ")
}

func buildClarificationPrompt(cfg Config, question string) string {
	terms := extractCandidateTerms(question)
	for _, term := range terms {
		matches, err := findItemMatches(cfg, term, 3)
		if err != nil || len(matches) == 0 {
			continue
		}
		return fmt.Sprintf("I need the exact item to search recipes. For %q, did you mean: %s? Reply with the exact item name.", term, strings.Join(matches, ", "))
	}
	return "I need the exact GTNH item name to search recipes. Reply with the exact output item (include tier/voltage if relevant), and I'll run it."
}

func enrichUnresolvedReply(cfg Config, ev ConsoleEvent, reply string) string {
	if !unresolvedRe.MatchString(reply) {
		return reply
	}
	if prompt := buildClarificationPrompt(cfg, ev.Text); prompt != "" {
		return prompt
	}
	return reply
}

func formatCoordinatesForMC(reply string) string {
	if strings.TrimSpace(reply) == "" {
		return reply
	}
	out := coordTupleDimCountRe.ReplaceAllString(reply, "[x:$1, y:$2, z:$3, dim:$4] count=$5")
	out = coordTupleCountDimRe.ReplaceAllString(out, "[x:$1, y:$2, z:$3, dim:$5] count=$4")
	out = coordTupleDimRe.ReplaceAllString(out, "[x:$1, y:$2, z:$3, dim:$4]")
	out = coordTupleCountRe.ReplaceAllString(out, "[x:$1, y:$2, z:$3] count=$4")
	out = coordTupleRe.ReplaceAllString(out, "[x:$1, y:$2, z:$3]")
	out = coordDimRe.ReplaceAllString(out, "dim:$1")
	out = strings.ReplaceAll(out, ";", ",")
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	return strings.TrimSpace(out)
}

func splitForMC(text string, maxChars, maxParts int) []string {
	text = strings.TrimSpace(text)
	if text == "" || maxChars <= 0 || maxParts <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	parts := make([]string, 0, maxParts)
	cur := ""
	for _, w := range words {
		if cur == "" {
			if len([]rune(w)) > maxChars {
				r := []rune(w)
				for len(r) > 0 && len(parts) < maxParts {
					n := maxChars
					if len(r) < n {
						n = len(r)
					}
					parts = append(parts, string(r[:n]))
					r = r[n:]
				}
				cur = ""
				if len(parts) >= maxParts {
					break
				}
				continue
			}
			cur = w
			continue
		}

		cand := cur + " " + w
		if len([]rune(cand)) <= maxChars {
			cur = cand
			continue
		}
		parts = append(parts, cur)
		if len(parts) >= maxParts {
			break
		}
		if len([]rune(w)) > maxChars {
			r := []rune(w)
			for len(r) > 0 && len(parts) < maxParts {
				n := maxChars
				if len(r) < n {
					n = len(r)
				}
				parts = append(parts, string(r[:n]))
				r = r[n:]
			}
			cur = ""
			if len(parts) >= maxParts {
				break
			}
		} else {
			cur = w
		}
	}
	if cur != "" && len(parts) < maxParts {
		parts = append(parts, cur)
	}
	return parts
}

func needsVerification(question string) bool {
	q := normalizeQueryText(question)
	if specificGTRe.MatchString(q) {
		return true
	}
	if materialIntentRe.MatchString(q) && inventoryIntentRe.MatchString(q) {
		return true
	}
	return strings.Contains(question, "?") && gtnhDomainRe.MatchString(question)
}

func isTaskBoardQuery(text string) bool {
	return taskBoardRe.MatchString(text)
}

func taskBoardMCReply(cfg Config) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := workspaceCommand(ctx, cfg, "sh", "gtnh_tasks", "summary")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("task summary failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	total, open, done, highOpen := 0, 0, 0, 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "total:"):
			fmt.Sscanf(line, "total: %d", &total)
		case strings.HasPrefix(line, "open:"):
			fmt.Sscanf(line, "open: %d", &open)
		case strings.HasPrefix(line, "done:"):
			fmt.Sscanf(line, "done: %d", &done)
		case strings.HasPrefix(line, "high_open:"):
			fmt.Sscanf(line, "high_open: %d", &highOpen)
		}
	}

	return fmt.Sprintf("Task board: open=%d (high=%d), done=%d, total=%d. Ask in Discord for full board.", open, highOpen, done, total), nil
}

func fallbackID(ev ConsoleEvent) string {
	sum := sha1.Sum([]byte(ev.Timestamp + "|" + ev.Player + "|" + ev.Text))
	return hex.EncodeToString(sum[:8])
}

func processOnce(client *http.Client, cfg Config, st *State, runner AgentRunner) {
	history := newHistoryStore(cfg)
	if closer, ok := history.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	processOnceWithHistory(client, cfg, st, runner, history)
}

func processOnceWithHistory(client *http.Client, cfg Config, st *State, runner AgentRunner, history HistoryStore) {
	resp, err := getConsole(client, cfg)
	if err != nil {
		log.Printf("event=poll_error err=%q", err.Error())
		return
	}

	if !st.Initialized {
		now := time.Now().Unix()
		for _, ev := range resp.Events {
			id := strings.TrimSpace(ev.EventID)
			if id == "" {
				id = fallbackID(ev)
			}
			st.Seen[id] = now
		}
		st.Initialized = true
		pruneSeen(st, 10000)
		saveState(cfg.StateFile, *st)
		log.Printf("event=seed_complete seeded=%d", len(resp.Events))
		return
	}

	now := time.Now().Unix()
	triggerCount := 0
	repliedCount := 0
	fallbackCount := 0
	for _, ev := range resp.Events {
		id := firstNonEmpty(ev.EventID, fallbackID(ev))
		if _, ok := st.Seen[id]; ok {
			continue
		}
		st.Seen[id] = now
		recordMinecraftChat(history, ev)
		if !ev.Triggered {
			continue
		}
		triggerCount++

		if isTaskBoardQuery(ev.Text) {
			reply, err := taskBoardMCReply(cfg)
			if err != nil {
				log.Printf("event=task_board_reply_error event_id=%q player=%q err=%q", id, ev.Player, err.Error())
				reply = "Task board lookup failed. Ask in Discord and I will retry."
			}
			parts := splitForMC(reply, cfg.ReplyMaxChars, cfg.MaxReplyParts)
			if len(parts) == 0 {
				parts = []string{trimChars(reply, cfg.ReplyMaxChars)}
			}
			sent := 0
			for i, part := range parts {
				if err := say(client, cfg, part); err != nil {
					log.Printf("event=say_error event_id=%q player=%q part=%d/%d err=%q", id, ev.Player, i+1, len(parts), err.Error())
					break
				}
				sent++
				time.Sleep(250 * time.Millisecond)
			}
			if sent > 0 {
				repliedCount++
				recordMinecraftReply(history, ev, strings.Join(parts[:sent], " "))
				log.Printf("event=reply_sent event_id=%q player=%q parts=%d reply_preview=%q", id, ev.Player, sent, parts[0])
			}
			continue
		}

		mustVerify := needsVerification(ev.Text)
		recent, recalled := historyContext(history, cfg, ev)
		var onCommentary func(string)
		if cfg.ProgressEnabled {
			commentary := newMinecraftCommentaryPublisher(client, cfg, id, ev.Player)
			onCommentary = commentary.Publish
		}
		steering, stopSteering := startMinecraftSteeringMonitor(client, cfg, st, history, ev.Player, onCommentary)
		reply, err := askAgent(runner, cfg, ev, sessionForEvent(cfg, id), mustVerify, recent, recalled, onCommentary, steering)
		stopSteering()
		if err != nil {
			fallbackCount++
			log.Printf("event=agent_error event_id=%q player=%q err=%q", id, ev.Player, err.Error())
			reply = fallbackReply(cfg, ev)
		}
		reply = enrichUnresolvedReply(cfg, ev, reply)
		reply = formatCoordinatesForMC(reply)
		parts := splitForMC(reply, cfg.ReplyMaxChars, cfg.MaxReplyParts)
		if len(parts) == 0 {
			parts = []string{trimChars(reply, cfg.ReplyMaxChars)}
		}
		sent := 0
		for i, part := range parts {
			if err := say(client, cfg, part); err != nil {
				log.Printf("event=say_error event_id=%q player=%q part=%d/%d err=%q", id, ev.Player, i+1, len(parts), err.Error())
				break
			}
			sent++
			time.Sleep(250 * time.Millisecond)
		}
		if sent == 0 {
			continue
		}
		repliedCount++
		recordMinecraftReply(history, ev, strings.Join(parts[:sent], " "))
		log.Printf("event=reply_sent event_id=%q player=%q parts=%d reply_preview=%q", id, ev.Player, sent, parts[0])
	}

	pruneSeen(st, 10000)
	saveState(cfg.StateFile, *st)
	log.Printf("event=poll_success events=%d trigger_count=%d replied=%d fallback_count=%d", len(resp.Events), triggerCount, repliedCount, fallbackCount)
}

func recordMinecraftChat(history HistoryStore, ev ConsoleEvent) {
	if history == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := history.AppendMessage(ctx, historyMessageFromEvent(ev)); err != nil {
		log.Printf("event=history_record_chat_error event_id=%q player=%q err=%q", firstNonEmpty(ev.EventID, fallbackID(ev)), ev.Player, err.Error())
	}
}

func recordMinecraftReply(history HistoryStore, ev ConsoleEvent, reply string) {
	if history == nil || strings.TrimSpace(reply) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg := historyMessageFromEvent(ev)
	msg.AuthorID = "greggpt"
	msg.AuthorName = "GregGPT"
	msg.Content = reply
	msg.ExternalMessageID = firstNonEmpty(ev.EventID, fallbackID(ev)) + ":reply"
	msg.Timestamp = time.Now().UTC()
	msg.IsBot = true
	if err := history.AppendMessage(ctx, msg); err != nil {
		log.Printf("event=history_record_reply_error event_id=%q player=%q err=%q", firstNonEmpty(ev.EventID, fallbackID(ev)), ev.Player, err.Error())
	}
}

func historyContext(store HistoryStore, cfg Config, ev ConsoleEvent) ([]history.Message, []history.RecallItem) {
	if store == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	recent, err := store.Recent(ctx, history.Query{
		Limit:       cfg.HistoryMaxMessages,
		IncludeBots: true,
	})
	if err != nil {
		log.Printf("event=history_recent_error event_id=%q player=%q err=%q", firstNonEmpty(ev.EventID, fallbackID(ev)), ev.Player, err.Error())
	}
	recalled, err := store.Recall(ctx, history.Query{
		Text:        ev.Text,
		Limit:       cfg.RecallMaxItems,
		IncludeBots: true,
	})
	if err != nil {
		log.Printf("event=history_recall_error event_id=%q player=%q err=%q", firstNonEmpty(ev.EventID, fallbackID(ev)), ev.Player, err.Error())
	}
	return recent, recalled
}

func historyMessageFromEvent(ev ConsoleEvent) history.Message {
	ts := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(ev.Timestamp)); err == nil {
		ts = parsed.UTC()
	}
	return history.Message{
		Source:            agentChannelMinecraft,
		ChannelID:         agentChannelMinecraft,
		ChannelName:       "Minecraft",
		AuthorID:          strings.TrimSpace(ev.Player),
		AuthorName:        strings.TrimSpace(ev.Player),
		Content:           strings.TrimSpace(ev.Text),
		Timestamp:         ts,
		ExternalMessageID: firstNonEmpty(ev.EventID, fallbackID(ev)),
	}
}

func newAgentRunner(cfg Config) AgentRunner {
	runner, err := agentcore.NewDefaultRunner(agentcore.Config{
		Model:              getenv(agentcore.EnvModel, agentcore.DefaultModel),
		ReasoningEffort:    getenv(agentcore.EnvReasoningEffort, agentcore.DefaultReasoningEffort),
		Workspace:          cfg.Workspace,
		AuthFile:           getenv(agentcore.EnvAuthFile, agentcore.DefaultAuthFile),
		Timeout:            cfg.AgentTimeout,
		MaxToolCalls:       getenvInt(agentcore.EnvMaxToolCalls, agentcore.DefaultMaxToolCalls),
		HistoryEnabled:     cfg.HistoryEnabled,
		HistoryPath:        cfg.HistoryPath,
		HistoryMaxMessages: cfg.HistoryMaxMessages,
		RecallMaxItems:     cfg.RecallMaxItems,
	})
	if err != nil {
		return startupErrorAgentRunner{err: err}
	}
	return &sharedAgentRunner{runner: runner}
}

func main() {
	cfg := loadConfig()
	client := &http.Client{Timeout: cfg.HTTPTimeout}
	state := loadState(cfg.StateFile)
	runner := newAgentRunner(cfg)

	log.Printf("event=startup bridge=%q poll_interval=%s lines=%d reply_max=%d state=%q workspace=%q", cfg.BridgeURL, cfg.PollInterval.String(), cfg.ConsoleLines, cfg.ReplyMaxChars, cfg.StateFile, cfg.Workspace)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	processOnce(client, cfg, &state, runner)
	for range ticker.C {
		processOnce(client, cfg, &state, runner)
	}
}
