package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeBridgeForTest() *Bridge {
	cfg := Config{
		ListenAddr:       ":0",
		DatHostToken:     "test-token",
		DatHostServer:    "test-server",
		DatHostBase:      "https://dathost.net/api/0.1",
		DefaultLines:     500,
		ReplyMaxChars:    180,
		Timeout:          5 * time.Second,
		StateFile:        "/tmp/dathost-bridge-test-state.json",
		DedupeMax:        1000,
		UseEmailPassword: false,
	}
	return newBridge(cfg)
}

func TestParseChatEvent_DatHostPrefixAndMinecraftChat(t *testing.T) {
	b := makeBridgeForTest()
	line := consoleLine{
		Text: "Feb 27 00:31:06:  [00:31:06] [Server thread/INFO]: <Snobacco> greg how do I make steel",
	}

	ev, ok := b.parseChatEvent(line)
	if !ok {
		t.Fatalf("expected chat event to parse")
	}
	if ev.Player != "Snobacco" {
		t.Fatalf("unexpected player: %q", ev.Player)
	}
	if ev.Text != "greg how do I make steel" {
		t.Fatalf("unexpected text: %q", ev.Text)
	}
	if !ev.Triggered {
		t.Fatalf("expected trigger=true for 'greg' substring")
	}
	if ev.EventID == "" {
		t.Fatalf("expected non-empty event id")
	}
}

func TestParseChatEvent_IgnoresServerLines(t *testing.T) {
	b := makeBridgeForTest()
	line := consoleLine{
		Text: "Feb 27 01:14:34:  [01:14:34] [Server thread/INFO]: [Server] greg test",
	}
	_, ok := b.parseChatEvent(line)
	if ok {
		t.Fatalf("expected server line to be ignored")
	}
}

func TestParseChatEvent_NonTriggerPlayerChat(t *testing.T) {
	b := makeBridgeForTest()
	line := consoleLine{
		Text: "Feb 27 01:14:34:  [01:14:34] [Server thread/INFO]: <SugaryCoffee> anyone got steel?",
	}
	ev, ok := b.parseChatEvent(line)
	if !ok {
		t.Fatalf("expected player chat to parse")
	}
	if ev.Triggered {
		t.Fatalf("expected trigger=false for non-greg message")
	}
}

func TestParseChatEvent_CaseInsensitiveTrigger(t *testing.T) {
	b := makeBridgeForTest()
	line := consoleLine{
		Text: "Feb 27 01:14:34:  [01:14:34] [Server thread/INFO]: <SugaryCoffee> GrEg can you help",
	}
	ev, ok := b.parseChatEvent(line)
	if !ok {
		t.Fatalf("expected player chat to parse")
	}
	if !ev.Triggered {
		t.Fatalf("expected trigger=true for mixed-case greg")
	}
}

func TestSanitizeSayText(t *testing.T) {
	msg, ok, reason := sanitizeSayText("hello world", 180)
	if !ok || reason != "" || msg != "hello world" {
		t.Fatalf("expected valid message, got ok=%v reason=%q msg=%q", ok, reason, msg)
	}

	msg, ok, reason = sanitizeSayText("Greg’s okey—dokey…", 180)
	if !ok || reason != "" || msg != "Greg's okey-dokey..." {
		t.Fatalf("expected ASCII-normalized message, got ok=%v reason=%q msg=%q", ok, reason, msg)
	}

	_, ok, _ = sanitizeSayText("/op me", 180)
	if ok {
		t.Fatalf("expected slash command reject")
	}

	_, ok, _ = sanitizeSayText("hello\nworld", 180)
	if ok {
		t.Fatalf("expected newline reject")
	}
}

func TestParseOnlineListLine_WithPlayers(t *testing.T) {
	players, err := parseOnlineListLine("There are 2/20 players online: __exx, SugaryCoffee")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(players))
	}
	if players[0].Name != "__exx" || players[1].Name != "SugaryCoffee" {
		t.Fatalf("unexpected players: %+v", players)
	}
}

func TestParseOnlineListLine_NoPlayers(t *testing.T) {
	players, err := parseOnlineListLine("There are 0/20 players online:")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(players) != 0 {
		t.Fatalf("expected no players, got %d", len(players))
	}
}

func TestParseOnlineListLine_Invalid(t *testing.T) {
	_, err := parseOnlineListLine("joined the game")
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestGetMCPositionsRefreshesAndFiltersSnapshot(t *testing.T) {
	var sawSync bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/game-servers/server-1/files/sync":
			sawSync = true
			w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/game-servers/server-1/files/world/greggpt/player_positions.json":
			w.Write([]byte(`{"generated_at":"2026-08-11T20:15:00Z","players":[{"name":"Snow","uuid":"snow-id","dim":0,"x":12.5,"y":64,"z":-8.25},{"name":"Greg","dim":183,"x":1,"y":2,"z":3}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	b := newBridge(Config{
		DatHostToken: "token", DatHostServer: "server-1", DatHostBase: server.URL,
		Timeout: time.Second, StateFile: t.TempDir() + "/state.json", DedupeMax: 1000,
		PlayerPositionsPath:   "world/greggpt/player_positions.json",
		PlayerPositionsMaxAge: 365 * 24 * time.Hour,
	})
	req := httptest.NewRequest(http.MethodGet, "/mc/positions?player=snow", nil)
	rec := httptest.NewRecorder()
	b.getMCPositions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got PlayerPositionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !sawSync || !got.OK || got.Source != "greggpt_player_export" || got.GeneratedAt != "2026-08-11T20:15:00Z" {
		t.Fatalf("unexpected response metadata: sync=%v response=%#v", sawSync, got)
	}
	if len(got.Players) != 1 || got.Players[0].Name != "Snow" || got.Players[0].X != 12.5 || got.Players[0].Z != -8.25 {
		t.Fatalf("unexpected filtered players: %#v", got.Players)
	}
}

func TestGetMCPositionsRejectsStaleSnapshot(t *testing.T) {
	stale := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Write([]byte(`{}`))
		case http.MethodGet:
			w.Write([]byte(`{"generated_at":"` + stale + `","players":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	b := newBridge(Config{
		DatHostToken: "token", DatHostServer: "server-1", DatHostBase: server.URL,
		Timeout: time.Second, StateFile: t.TempDir() + "/state.json", DedupeMax: 1000,
		PlayerPositionsPath: "world/greggpt/player_positions.json", PlayerPositionsMaxAge: 30 * time.Second,
	})
	rec := httptest.NewRecorder()
	b.getMCPositions(rec, httptest.NewRequest(http.MethodGet, "/mc/positions", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "stale") {
		t.Fatalf("missing stale error: %s", rec.Body.String())
	}
}
