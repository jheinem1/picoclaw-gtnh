package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ListenAddr       string
	DatHostToken     string
	DatHostEmail     string
	DatHostPassword  string
	DatHostServer    string
	DatHostBase      string
	DefaultLines     int
	ReplyMaxChars    int
	Timeout          time.Duration
	StateFile        string
	DedupeMax        int
	UseEmailPassword bool
}

type BridgeState struct {
	Seen  map[string]int64 `json:"seen"`
	Order []string         `json:"order"`
}

type Bridge struct {
	cfg         Config
	client      *http.Client
	state       BridgeState
	stateMu     sync.Mutex
	chatParsers []*regexp.Regexp
	tsPrefixRe  *regexp.Regexp
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

type consoleLine struct {
	Text      string
	Timestamp string
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

func loadConfig() (Config, error) {
	cfg := Config{
		ListenAddr:      getenv("DATHOST_BRIDGE_LISTEN", ":8080"),
		DatHostToken:    strings.TrimSpace(os.Getenv("DATHOST_API_TOKEN")),
		DatHostEmail:    strings.TrimSpace(os.Getenv("DATHOST_API_EMAIL")),
		DatHostPassword: strings.TrimSpace(os.Getenv("DATHOST_API_PASSWORD")),
		DatHostServer:   strings.TrimSpace(os.Getenv("DATHOST_SERVER_ID")),
		DatHostBase:     strings.TrimRight(getenv("DATHOST_API_BASE", "https://dathost.net/api/0.1"), "/"),
		DefaultLines:    getenvInt("DATHOST_CONSOLE_LINES", 500),
		ReplyMaxChars:   getenvInt("MC_REPLY_MAX_CHARS", 180),
		Timeout:         time.Duration(getenvInt("DATHOST_HTTP_TIMEOUT_SECONDS", 15)) * time.Second,
		StateFile:       getenv("DATHOST_BRIDGE_STATE_FILE", "/var/lib/dathost-bridge/state.json"),
		DedupeMax:       getenvInt("DATHOST_DEDUPE_MAX", 4000),
	}

	if cfg.DatHostToken == "" {
		if cfg.DatHostEmail == "" || cfg.DatHostPassword == "" {
			return cfg, errors.New("missing DatHost auth; set DATHOST_API_TOKEN or both DATHOST_API_EMAIL + DATHOST_API_PASSWORD")
		}
		cfg.UseEmailPassword = true
	}
	if cfg.DatHostServer == "" {
		return cfg, errors.New("missing DATHOST_SERVER_ID")
	}
	if cfg.DefaultLines < 1 {
		cfg.DefaultLines = 500
	}
	if cfg.ReplyMaxChars < 1 {
		cfg.ReplyMaxChars = 180
	}
	if cfg.DedupeMax < 100 {
		cfg.DedupeMax = 4000
	}
	return cfg, nil
}

func newBridge(cfg Config) *Bridge {
	b := &Bridge{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		state: BridgeState{
			Seen:  map[string]int64{},
			Order: []string{},
		},
		chatParsers: []*regexp.Regexp{
			regexp.MustCompile(`^\[[0-9:]{8}\]\s+\[[^\]]+\]:\s*<([^>]{1,32})>\s*(.+)$`),
			regexp.MustCompile(`^\[[0-9:]{8}\]\s+\[[^\]]+\]\s+\[[^\]]+\]:\s*<([^>]{1,32})>\s*(.+)$`),
			regexp.MustCompile(`^\[[^\]]+\]:\s*<([^>]{1,32})>\s*(.+)$`),
			regexp.MustCompile(`^<([^>]{1,32})>\s*(.+)$`),
			regexp.MustCompile(`^\[CHAT\]\s*([^:]{1,32}):\s*(.+)$`),
		},
		tsPrefixRe: regexp.MustCompile(`^([A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}):\s+(.*)$`),
	}
	b.loadState()
	return b
}

func (b *Bridge) loadState() {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()

	data, err := os.ReadFile(b.cfg.StateFile)
	if err != nil {
		return
	}

	var st BridgeState
	if err := json.Unmarshal(data, &st); err != nil {
		log.Printf("event=state_load_error error=%q", err.Error())
		return
	}
	if st.Seen == nil {
		st.Seen = map[string]int64{}
	}
	b.state = st
	b.pruneLocked()
}

func (b *Bridge) saveStateLocked() {
	if err := os.MkdirAll(filepath.Dir(b.cfg.StateFile), 0o755); err != nil {
		log.Printf("event=state_save_error error=%q", err.Error())
		return
	}
	body, err := json.MarshalIndent(b.state, "", "  ")
	if err != nil {
		log.Printf("event=state_save_error error=%q", err.Error())
		return
	}
	tmp := b.cfg.StateFile + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		log.Printf("event=state_save_error error=%q", err.Error())
		return
	}
	if err := os.Rename(tmp, b.cfg.StateFile); err != nil {
		log.Printf("event=state_save_error error=%q", err.Error())
	}
}

func (b *Bridge) pruneLocked() {
	for len(b.state.Order) > b.cfg.DedupeMax {
		oldest := b.state.Order[0]
		b.state.Order = b.state.Order[1:]
		delete(b.state.Seen, oldest)
	}
}

func (b *Bridge) seenBefore(eventID string) bool {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	if _, ok := b.state.Seen[eventID]; ok {
		return true
	}
	now := time.Now().Unix()
	b.state.Seen[eventID] = now
	b.state.Order = append(b.state.Order, eventID)
	b.pruneLocked()
	b.saveStateLocked()
	return false
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (b *Bridge) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": time.Now().UTC().Format(time.RFC3339),
	})
}

func parseLinesParam(r *http.Request, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("lines"))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if n < 1 {
		return fallback
	}
	if n > 5000 {
		return 5000
	}
	return n
}

func (b *Bridge) getMCConsole(w http.ResponseWriter, r *http.Request) {
	lines := parseLinesParam(r, b.cfg.DefaultLines)
	raw, status, err := b.callDatHost(r.Context(), http.MethodGet, fmt.Sprintf("/game-servers/%s/console", b.cfg.DatHostServer), "", nil)
	if err != nil {
		log.Printf("event=poll_error status=%d lines=%d error=%q", status, lines, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	all, err := parseConsolePayload(raw)
	if err != nil {
		log.Printf("event=poll_error status=%d lines=%d error=%q", status, lines, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": "unable to parse console payload",
		})
		return
	}

	if len(all) > lines {
		all = all[len(all)-lines:]
	}

	events := make([]ConsoleEvent, 0, len(all))
	for _, line := range all {
		ev, ok := b.parseChatEvent(line)
		if !ok {
			continue
		}
		if b.seenBefore(ev.EventID) {
			continue
		}
		events = append(events, ev)
	}

	triggerCount := 0
	for _, ev := range events {
		if ev.Triggered {
			triggerCount++
		}
	}

	log.Printf("event=poll_success lines=%d events=%d trigger_count=%d", lines, len(events), triggerCount)
	writeJSON(w, http.StatusOK, ConsoleResponse{
		OK:     true,
		Count:  len(events),
		Events: events,
	})
}

func (b *Bridge) postMCSay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("event=say_reject reason=bad_json")
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "invalid JSON body",
		})
		return
	}

	msg, ok, reason := sanitizeSayText(req.Text, b.cfg.ReplyMaxChars)
	if !ok {
		log.Printf("event=say_reject reason=%s", reason)
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": reason,
		})
		return
	}

	cmd := "say " + msg
	_, status, err := b.sendConsoleCommand(r.Context(), cmd)
	if err != nil {
		log.Printf("event=say_error status=%d error=%q", status, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	log.Printf("event=say_success chars=%d", len([]rune(msg)))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"sent":    msg,
		"command": cmd,
	})
}

func (b *Bridge) sendConsoleCommand(ctx context.Context, cmd string) ([]byte, int, error) {
	path := fmt.Sprintf("/game-servers/%s/console", b.cfg.DatHostServer)
	form := url.Values{}
	form.Set("line", cmd)
	return b.callDatHost(ctx, http.MethodPost, path, "application/x-www-form-urlencoded", []byte(form.Encode()))
}

func (b *Bridge) callDatHost(ctx context.Context, method, path, contentType string, body []byte) ([]byte, int, error) {
	url := b.cfg.DatHostBase + path
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	if b.cfg.UseEmailPassword {
		req.SetBasicAuth(b.cfg.DatHostEmail, b.cfg.DatHostPassword)
	} else {
		req.SetBasicAuth(b.cfg.DatHostToken, "")
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return data, resp.StatusCode, fmt.Errorf("dathost HTTP %d: %s", resp.StatusCode, msg)
	}
	return data, resp.StatusCode, nil
}

func parseConsolePayload(raw []byte) ([]consoleLine, error) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return extractConsoleLines(payload), nil
}

func extractConsoleLines(v any) []consoleLine {
	switch t := v.(type) {
	case string:
		return splitStringLines(t)
	case []any:
		out := make([]consoleLine, 0, len(t))
		for _, it := range t {
			out = append(out, extractConsoleLines(it)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"lines", "entries", "data", "console"} {
			if val, ok := t[key]; ok {
				return extractConsoleLines(val)
			}
		}
		line := firstStringField(t, "line", "message", "text", "entry")
		if line == "" {
			return nil
		}
		ts := firstStringField(t, "timestamp", "time", "created_at", "date")
		return []consoleLine{{Text: line, Timestamp: ts}}
	default:
		return nil
	}
}

func splitStringLines(s string) []consoleLine {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	out := make([]consoleLine, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, consoleLine{Text: p, Timestamp: ""})
	}
	return out
}

func firstStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if strings.TrimSpace(t) != "" {
				return t
			}
		default:
			s := strings.TrimSpace(fmt.Sprintf("%v", t))
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func (b *Bridge) parseChatEvent(line consoleLine) (ConsoleEvent, bool) {
	raw := strings.TrimSpace(line.Text)
	if raw == "" {
		return ConsoleEvent{}, false
	}
	normalizedRaw, parsedTs := b.normalizeDatHostLine(raw)
	lowerRaw := strings.ToLower(raw)
	if strings.Contains(lowerRaw, "[server]") {
		return ConsoleEvent{}, false
	}

	var player string
	var text string
	for _, re := range b.chatParsers {
		m := re.FindStringSubmatch(normalizedRaw)
		if len(m) == 3 {
			player = strings.TrimSpace(m[1])
			text = strings.TrimSpace(m[2])
			break
		}
	}
	if player == "" || text == "" {
		return ConsoleEvent{}, false
	}
	if strings.EqualFold(player, "server") {
		return ConsoleEvent{}, false
	}

	ts := strings.TrimSpace(line.Timestamp)
	if ts == "" {
		ts = parsedTs
	}
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}

	triggered := strings.Contains(strings.ToLower(text), "greg")
	sum := sha1.Sum([]byte(ts + "|" + raw))
	id := hex.EncodeToString(sum[:8])

	return ConsoleEvent{
		EventID:   id,
		Timestamp: ts,
		Player:    player,
		Text:      text,
		Triggered: triggered,
	}, true
}

func (b *Bridge) normalizeDatHostLine(raw string) (string, string) {
	m := b.tsPrefixRe.FindStringSubmatch(raw)
	if len(m) != 3 {
		return raw, ""
	}

	prefix := strings.TrimSpace(m[1])
	rest := strings.TrimSpace(m[2])
	if rest == "" {
		return raw, ""
	}

	year := time.Now().UTC().Year()
	parsed, err := time.ParseInLocation("2006 Jan 2 15:04:05", fmt.Sprintf("%d %s", year, prefix), time.UTC)
	if err != nil {
		return rest, prefix
	}
	return rest, parsed.UTC().Format(time.RFC3339)
}

func sanitizeSayText(input string, maxChars int) (string, bool, string) {
	text := strings.TrimSpace(toASCII(input))
	if text == "" {
		return "", false, "text is required"
	}
	if strings.ContainsAny(text, "\r\n") {
		return "", false, "newlines are not allowed"
	}
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, ";") || strings.HasPrefix(text, "\\") {
		return "", false, "command-like prefixes are not allowed"
	}
	for _, r := range text {
		if r < 32 || r == 127 {
			return "", false, "control characters are not allowed"
		}
	}

	trimmed := []rune(text)
	if len(trimmed) > maxChars {
		trimmed = trimmed[:maxChars]
	}
	return string(trimmed), true, ""
}

func toASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\u2018', '\u2019':
			b.WriteRune('\'')
		case '\u201C', '\u201D':
			b.WriteRune('"')
		case '\u2013', '\u2014':
			b.WriteRune('-')
		case '\u2026':
			b.WriteString("...")
		case '\u00A0':
			b.WriteRune(' ')
		default:
			if r >= 32 && r <= 126 {
				b.WriteRune(r)
			} else if r < 32 || r == 127 {
				// keep control characters; existing validation will reject them
				b.WriteRune(r)
			} else {
				b.WriteRune('?')
			}
		}
	}
	return b.String()
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("event=startup_error error=%q", err.Error())
	}

	bridge := newBridge(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", bridge.healthz)
	mux.HandleFunc("/mc/console", bridge.getMCConsole)
	mux.HandleFunc("/mc/say", bridge.postMCSay)

	authMode := "token"
	if cfg.UseEmailPassword {
		authMode = "email_password"
	}
	log.Printf("event=startup_ok listen=%q base=%q server_id=%q default_lines=%d reply_max=%d auth_mode=%q", cfg.ListenAddr, cfg.DatHostBase, cfg.DatHostServer, cfg.DefaultLines, cfg.ReplyMaxChars, authMode)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("event=shutdown_error error=%q", err.Error())
	}
}
