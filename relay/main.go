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
	"regexp"
	"sort"
	"strings"
	"time"
)

type Config struct {
	BridgeURL     string
	PollInterval  time.Duration
	ConsoleLines  int
	ReplyMaxChars int
	MaxReplyParts int
	StateFile     string
	SessionPrefix string
	AgentTimeout  time.Duration
	HTTPTimeout   time.Duration
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

var logPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}\s`)
var unresolvedRe = regexp.MustCompile(`(?i)(could not resolve|no exact recipe chain found|no exact recipe|no recipe (chain )?found|not found)`)
var turnIntoRe = regexp.MustCompile(`(?i)turn\s+(.+?)\s+into\s+(.+?)(?:\?|$)`)
var refineIntoRe = regexp.MustCompile(`(?i)what does\s+(.+?)\s+refine into(?:\?|$)`)
var makeRe = regexp.MustCompile(`(?i)(?:how much .* to )?make\s+(?:a|an|the)?\s*(.+?)(?:\?|$)`)
var specificGTRe = regexp.MustCompile(`(?i)\b(recipe|recipes|refine|smelt|craft|make|turn .* into|what does .* (do|refine)|ore|dust|ingot|plate|rod|pickaxe|tool material)\b`)
var gtnhDomainRe = regexp.MustCompile(`(?i)\b(gtnh|gregtech|steam|pipe|fluid|throughput|boiler|turbine|lv|mv|hv|ev|iv|luv|zpm|uv|machine|multiblock|ore|dust|ingot|plate|rod|cable|wire)\b`)
var taskBoardRe = regexp.MustCompile(`(?i)\b(task\s*board|tasks?\s+board|open\s+tasks?|task\s+list)\b`)
var taskMutationIntentRe = regexp.MustCompile(`(?i)\b(assign|reassign|move|pause|unpause|resume|reopen|describe|description|update|status update|progress update)\b`)
var inventoryIntentRe = regexp.MustCompile(`(?i)\b(who has|where is|which chest|inventory|inventories|in chests?|in my chest|has item|holding|stored)\b`)
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

func loadConfig() Config {
	poll := getenvInt("DATHOST_POLL_INTERVAL_SECONDS", 10)
	if poll < 2 {
		poll = 2
	}

	return Config{
		BridgeURL:     strings.TrimRight(getenv("DATHOST_BRIDGE_URL", "http://dathost-bridge:8080"), "/"),
		PollInterval:  time.Duration(poll) * time.Second,
		ConsoleLines:  getenvInt("DATHOST_CONSOLE_LINES", 500),
		ReplyMaxChars: getenvInt("MC_REPLY_MAX_CHARS", 180),
		MaxReplyParts: getenvInt("MC_REPLY_MAX_PARTS", 4),
		StateFile:     getenv("MC_RELAY_STATE_FILE", "/var/lib/mc-relay/state.json"),
		SessionPrefix: getenv("MC_RELAY_SESSION", "mc:relay"),
		AgentTimeout:  time.Duration(getenvInt("MC_RELAY_AGENT_TIMEOUT_SECONDS", 60)) * time.Second,
		HTTPTimeout:   time.Duration(getenvInt("MC_RELAY_HTTP_TIMEOUT_SECONDS", 20)) * time.Second,
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
	url := fmt.Sprintf("%s/mc/console?lines=%d", cfg.BridgeURL, cfg.ConsoleLines)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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

func trimChars(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}

func parseAgentOutput(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "🦞") {
			return strings.TrimSpace(strings.TrimPrefix(line, "🦞"))
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || logPrefix.MatchString(line) {
			continue
		}
		return line
	}
	return ""
}

func askAgentWithPrompt(cfg Config, session, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.AgentTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "picoclaw", "agent", "--session", session, "--message", prompt)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("agent call failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	text := strings.TrimSpace(parseAgentOutput(string(out)))
	if text == "" {
		return "", fmt.Errorf("agent returned empty output: %s", strings.TrimSpace(string(out)))
	}
	return text, nil
}

func askAgent(cfg Config, ev ConsoleEvent, session string, mustVerify bool) (string, error) {
	prompt := fmt.Sprintf("Minecraft player '%s' asked: %q. Reply as GregGPT in GTNH context by default. Keep it concise, plain text, no markdown.", ev.Player, ev.Text)
	prompt += "\nTool execution rule: execute one direct workspace command only; do not use cd, &&, pipes, or chained shell fragments."
	prompt += "\nDo not claim a command was blocked by safety guard unless a tool call in this same turn returned stderr containing that exact phrase."
	if taskMutationIntentRe.MatchString(ev.Text) {
		prompt += "\nIf this is a GTNH task-management request, you should execute the task command directly in workspace using sh gtnh_tasks, then reply with the command result."
		prompt += "\nYou do have access to task tools. Do not claim you cannot run board commands."
		prompt += "\nUseful commands: sh gtnh_tasks reassign <id> <owner>, sh gtnh_tasks move <id> --status todo|doing|paused|done [--owner <id>] [--reason \"...\"], sh gtnh_tasks pause <id> \"...\", sh gtnh_tasks unpause <id>, sh gtnh_tasks describe <id> \"...\", sh gtnh_tasks status-update <id> \"...\"."
		prompt += "\nFor requests to add a progress update or status update to an existing task, use exactly: sh gtnh_tasks status-update <id> \"<update text>\"."
		prompt += "\nDo not invent alternate task-update commands, and do not prepend cd or use shell chaining."
	}
	if inventoryIntentRe.MatchString(ev.Text) {
		prompt += "\nIf this asks who has an item, where an item is stored, or chest/inventory location, run exactly one inventory command (no cd/&& chaining) and answer only from that command output."
		prompt += "\nUseful commands: sh gtnh_inventory status; sh gtnh_inventory find --item <mod:name[:damage]> [--scope players|chests|both] [--limit <n>] for exact lookups; sh gtnh_inventory find-item --query \"<name>\" [--scope players|chests|both] [--limit <n>] for natural-language requests; sh gtnh_inventory player --name <player>|--uuid <uuid>; sh gtnh_inventory chest --x <int> --y <int> --z <int> [--dim 0|-1|1]."
		prompt += "\nIf the request names a specific player (for example __exx), include --player <name> in gtnh_inventory find to avoid top-N false negatives."
		prompt += "\nDo not claim you lack access to inventory/chest data if these tools are available."
		prompt += "\nFor name-based requests (for example steel ingot), use sh gtnh_inventory find-item --query \"<name>\" first; do not guess numeric IDs."
		prompt += "\nStale fallback is forbidden: do not say jq/curl is missing unless the command you ran in this turn returned that exact stderr."
		prompt += "\nIf find is called with --id but without --damage, treat it as invalid input and rerun with --item or find-item."
		prompt += "\nValidate output before answering: accepted markers are lines starting with Inventory find, Inventory Index Status, Resolved item, or error:. If markers are missing, treat as tool failure and retry once."
		prompt += "\nMinecraft coordinate format: use JourneyMap-style tags like [x:<num>, y:<num>, z:<num>, dim:<num>] and include count=<num> outside the brackets when relevant."
	}
	if mustVerify {
		prompt += "\nVerification is required for this question. Prefer web verification from the GTNH wiki (wiki.gtnewhorizons.com) when possible; use local gtnh_query data as fallback/cross-check."
		prompt += "\nRun lookup commands directly (for example `sh gtnh_wiki_search \"<query>\"`), without `cd` or command chaining."
		prompt += "\nBefore answering, you must execute at least one lookup command and base the reply on its output."
		prompt += "\nUse the command that matches user intent:"
		prompt += "\n- general wiki topic lookup: sh gtnh_wiki_search \"<query>\""
		prompt += "\n- specific wiki page summary: sh gtnh_wiki_page \"<title>\""
		prompt += "\n- fuzzy item lookup: sh gtnh_find_item \"<query>\""
		prompt += "\n- recipe search across close matches: sh gtnh_search_recipes \"<query>\""
		prompt += "\n- exact item recipes: sh gtnh_resolve_recipes \"<item>\""
		prompt += "\nIf lookup is ambiguous or missing, ask one concise clarifying question and do not present failure as final."
		prompt += "\nDo not claim you need the user to provide a page before searching. Try one lookup first, then clarify only if results are ambiguous."
	}

	reply, err := askAgentWithPrompt(cfg, session, prompt)
	if err == nil {
		return reply, nil
	}

	// Retry once with stricter guidance in a fresh session to avoid bad tool-call loops.
	retryPrompt := fmt.Sprintf(
		"Reply concisely. Prefer GTNH context. Do not run more than one tool call. If lookup fails, say you could not resolve it from current snapshot.",
	)
	retryReply, retryErr := askAgentWithPrompt(cfg, session+":retry", retryPrompt+"\n\nUser: "+ev.Text)
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

func directWikiReply(cfg Config, question string) (string, error) {
	terms := extractCandidateTerms(question)
	if len(terms) == 0 {
		return "", errors.New("no candidate terms")
	}
	query := terms[0]

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "gtnh_wiki_search", query)
	cmd.Dir = "/root/.picoclaw/workspace"
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gtnh_wiki_search failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var payload struct {
		OK      bool `json:"ok"`
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
		TotalHits int `json:"total_hits"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", err
	}
	if !payload.OK {
		return "", errors.New("wiki search returned not ok")
	}
	if len(payload.Results) == 0 {
		return "I checked the GTNH wiki and couldn’t find a direct page match for that wording. Try the exact item/block name and I’ll rerun it.", nil
	}
	first := payload.Results[0]
	return trimChars(fmt.Sprintf("Top wiki match for '%s': %s (%s)", query, first.Title, first.URL), cfg.ReplyMaxChars), nil
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

	cmd := exec.CommandContext(ctx, "sh", "gtnh_query", "find-item", query)
	cmd.Dir = "/root/.picoclaw/workspace"
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("find-item failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var payload struct {
		Items []struct {
			DisplayName string `json:"display_name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, err
	}

	results := make([]string, 0, limit)
	for _, it := range payload.Items {
		name := strings.TrimSpace(it.DisplayName)
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
	return strings.Contains(question, "?") && gtnhDomainRe.MatchString(question)
}

func isTaskBoardQuery(text string) bool {
	return taskBoardRe.MatchString(text)
}

func taskBoardMCReply() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "gtnh_tasks", "summary")
	cmd.Dir = "/root/.picoclaw/workspace"
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

func processOnce(client *http.Client, cfg Config, st *State) {
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
		if !ev.Triggered {
			continue
		}
		triggerCount++

		if isTaskBoardQuery(ev.Text) {
			reply, err := taskBoardMCReply()
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
				log.Printf("event=reply_sent event_id=%q player=%q parts=%d reply_preview=%q", id, ev.Player, sent, parts[0])
			}
			continue
		}

		mustVerify := needsVerification(ev.Text)
		reply, err := askAgent(cfg, ev, sessionForEvent(cfg, id), mustVerify)
		if err != nil {
			fallbackCount++
			log.Printf("event=agent_error event_id=%q player=%q err=%q", id, ev.Player, err.Error())
			reply = fallbackReply(cfg, ev)
		}
		if mustVerify && safetyGuardReplyRe.MatchString(reply) {
			if verified, vErr := directWikiReply(cfg, ev.Text); vErr == nil && strings.TrimSpace(verified) != "" {
				log.Printf("event=verify_override event_id=%q player=%q reason=%q", id, ev.Player, "safety_guard_claim")
				reply = verified
			} else if vErr != nil {
				log.Printf("event=verify_override_error event_id=%q player=%q err=%q", id, ev.Player, vErr.Error())
			}
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
		log.Printf("event=reply_sent event_id=%q player=%q parts=%d reply_preview=%q", id, ev.Player, sent, parts[0])
	}

	pruneSeen(st, 10000)
	saveState(cfg.StateFile, *st)
	log.Printf("event=poll_success events=%d trigger_count=%d replied=%d fallback_count=%d", len(resp.Events), triggerCount, repliedCount, fallbackCount)
}

func main() {
	cfg := loadConfig()
	client := &http.Client{Timeout: cfg.HTTPTimeout}
	state := loadState(cfg.StateFile)

	log.Printf("event=startup bridge=%q poll_interval=%s lines=%d reply_max=%d state=%q", cfg.BridgeURL, cfg.PollInterval.String(), cfg.ConsoleLines, cfg.ReplyMaxChars, cfg.StateFile)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	processOnce(client, cfg, &state)
	for range ticker.C {
		processOnce(client, cfg, &state)
	}
}
