package main

import (
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
