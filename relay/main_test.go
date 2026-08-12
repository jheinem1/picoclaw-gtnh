package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "greggpt-gtnh/internal/agent"
	"greggpt-gtnh/internal/agent/history"
)

type fakeAgentRunner struct {
	calls      []AgentRequest
	reply      string
	commentary []string
	err        error
}

type steeringAgentRunner struct {
	received chan agentcore.SteeringMessage
}

func (r *steeringAgentRunner) Run(ctx context.Context, req AgentRequest) (AgentResponse, error) {
	select {
	case steering := <-req.Steering:
		r.received <- steering
		if steering.OnCommentary != nil {
			steering.OnCommentary("Adjusting to your latest direction. This sentence is removed.")
		}
		return AgentResponse{Text: "Steve - asking for only the nearby chest: The wrench is there."}, nil
	case <-ctx.Done():
		return AgentResponse{}, ctx.Err()
	}
}

func (f *fakeAgentRunner) Run(ctx context.Context, req AgentRequest) (AgentResponse, error) {
	f.calls = append(f.calls, req)
	for _, update := range f.commentary {
		if req.OnCommentary != nil {
			req.OnCommentary(update)
		}
	}
	if f.err != nil {
		return AgentResponse{}, f.err
	}
	return AgentResponse{Text: f.reply}, nil
}

type fakeHistoryStore struct {
	recordedChats   []history.Message
	recordedReplies []history.Message
	recent          []history.Message
	recalled        []history.RecallItem
}

func (f *fakeHistoryStore) AppendMessage(ctx context.Context, msg history.Message) error {
	if msg.IsBot {
		f.recordedReplies = append(f.recordedReplies, msg)
	} else {
		f.recordedChats = append(f.recordedChats, msg)
	}
	return nil
}

func (f *fakeHistoryStore) Recent(ctx context.Context, q history.Query) ([]history.Message, error) {
	return append([]history.Message(nil), f.recent...), nil
}

func (f *fakeHistoryStore) Recall(ctx context.Context, q history.Query) ([]history.RecallItem, error) {
	return append([]history.RecallItem(nil), f.recalled...), nil
}

func TestSplitForMC(t *testing.T) {
	parts := splitForMC("one two three four five six", 10, 4)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts, got %#v", parts)
	}
	for _, p := range parts {
		if len([]rune(p)) > 10 {
			t.Fatalf("part too long: %q", p)
		}
	}
}

func TestSplitForMC_LongWord(t *testing.T) {
	parts := splitForMC("supercalifragilisticexpialidocious", 8, 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts due to maxParts cap, got %#v", parts)
	}
	for _, p := range parts {
		if len([]rune(p)) > 8 {
			t.Fatalf("part too long: %q", p)
		}
	}
}

func TestExtractCandidateTerms(t *testing.T) {
	got := extractCandidateTerms("greg what does cassiterite refine into?")
	want := []string{"cassiterite"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected terms: got=%#v want=%#v", got, want)
	}

	got = extractCandidateTerms("greg what does it take to turn yellow garnet dust into titanium?")
	want = []string{"yellow garnet dust", "titanium"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected turn-into terms: got=%#v want=%#v", got, want)
	}

	got = extractCandidateTerms("greg, how much steel do we need to make a tier 2 steam purifier?")
	want = []string{"tier 2 steam purifier", "how much steel do we need to make a tier 2 steam purifier"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected make terms: got=%#v want=%#v", got, want)
	}
}

func TestNeedsVerification_SteamThroughputQuestion(t *testing.T) {
	if !needsVerification("greg what pipes that can handle steam have higher throughput than potin fluid pipes in MV?") {
		t.Fatalf("expected steam throughput question to require verification")
	}
}

func TestInventoryIntent_MEQuestion(t *testing.T) {
	if !inventoryIntentRe.MatchString("greg do we have stainless steel plates in ME?") {
		t.Fatalf("expected ME inventory question to match inventory intent")
	}
}

func TestMaterialStorageQuestionRequiresVerification(t *testing.T) {
	question := "greg what supplies do we need for the distillation towers that we don't already have in storage?"
	if !inventoryIntentRe.MatchString(question) {
		t.Fatalf("expected material storage question to match inventory intent")
	}
	if !materialIntentRe.MatchString(question) {
		t.Fatalf("expected material storage question to match material intent")
	}
	if !needsVerification(question) {
		t.Fatalf("expected material storage question to require recipe verification")
	}
}

func TestFormatCoordinatesForMC_TupleCount(t *testing.T) {
	in := "Chests with glass: (-1173,20,3525):63; (-1256,50,-8798):57"
	got := formatCoordinatesForMC(in)
	want := "Chests with glass: [x:-1173, y:20, z:3525] count=63, [x:-1256, y:50, z:-8798] count=57"
	if got != want {
		t.Fatalf("unexpected format\n got=%q\nwant=%q", got, want)
	}
}

func TestFormatCoordinatesForMC_Dim(t *testing.T) {
	in := "Chest at (-10,64,30) dim0 count=12"
	got := formatCoordinatesForMC(in)
	want := "Chest at [x:-10, y:64, z:30, dim:0] count=12"
	if got != want {
		t.Fatalf("unexpected format\n got=%q\nwant=%q", got, want)
	}
}

func TestAskAgentUsesRunner(t *testing.T) {
	runner := &fakeAgentRunner{reply: "use bronze pipes"}
	cfg := Config{AgentTimeout: time.Second}
	ev := ConsoleEvent{Player: "Steve", Text: "greg what pipe should I make?"}

	recent := []history.Message{{Source: agentChannelMinecraft, AuthorName: "Alex", Content: "I need a wrench"}}
	recalled := []history.RecallItem{{Message: history.Message{Source: agentChannelMinecraft, AuthorName: "Alex", Content: "The wrench was in the LV chest"}, Reason: "fts"}}
	got, err := askAgent(runner, cfg, ev, "mc:relay:test", false, recent, recalled, nil, nil)
	if err != nil {
		t.Fatalf("askAgent failed: %v", err)
	}
	if got != "use bronze pipes" {
		t.Fatalf("unexpected reply: %q", got)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}
	call := runner.calls[0]
	if call.Channel != agentChannelMinecraft {
		t.Fatalf("unexpected channel: %q", call.Channel)
	}
	if call.Session != "mc:relay:test" {
		t.Fatalf("unexpected session: %q", call.Session)
	}
	if !strings.Contains(call.Message, `Minecraft player "Steve" says`) {
		t.Fatalf("prompt did not include player context: %q", call.Message)
	}
	if !strings.Contains(call.Message, "Treat clearly fictional Minecraft or factory roleplay as fiction") {
		t.Fatalf("prompt did not preserve fictional Minecraft context: %q", call.Message)
	}
	if strings.Contains(call.Message, "briefly restate what you understood") || strings.Contains(call.Message, "Use the compact shape") {
		t.Fatalf("prompt still requires the repetitive interpretation preamble: %q", call.Message)
	}
	if !reflect.DeepEqual(call.RecentHistory, recent) {
		t.Fatalf("recent history was not passed through: got=%#v want=%#v", call.RecentHistory, recent)
	}
	if !reflect.DeepEqual(call.RecalledContext, recalled) {
		t.Fatalf("recalled context was not passed through: got=%#v want=%#v", call.RecalledContext, recalled)
	}
}

func TestAskAgentRecipePromptUsesRecipeSQL(t *testing.T) {
	runner := &fakeAgentRunner{reply: "choose recipe"}
	cfg := Config{AgentTimeout: time.Second}
	ev := ConsoleEvent{Player: "Steve", Text: "greg what recipe should I use for a distillation tower?"}

	if _, err := askAgent(runner, cfg, ev, "mc:relay:recipe", true, nil, nil, nil, nil); err != nil {
		t.Fatalf("askAgent failed: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}
	prompt := runner.calls[0].Message
	if !strings.Contains(prompt, "recipe_sql") {
		t.Fatalf("prompt missing recipe_sql guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "Preserve returned quantities, provenance, and freshness exactly") {
		t.Fatalf("prompt missing concise lookup accuracy guidance: %q", prompt)
	}
	if strings.Contains(prompt, "gtnh_resolve_recipes") || strings.Contains(prompt, "gtnh_search_recipes") || strings.Contains(prompt, "gtnh_find_item") {
		t.Fatalf("prompt still references old recipe wrappers: %q", prompt)
	}
}

func TestProcessOnceUsesFakeAgentRunnerAndSaysReply(t *testing.T) {
	var said []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mc/console":
			_ = json.NewEncoder(w).Encode(ConsoleResponse{
				OK:    true,
				Count: 1,
				Events: []ConsoleEvent{{
					EventID:   "event-1",
					Timestamp: "2026-04-28T00:00:00Z",
					Player:    "Steve",
					Text:      "greg where is my wrench?",
					Triggered: true,
				}},
			})
		case "/mc/say":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode say payload: %v", err)
			}
			said = append(said, payload["text"])
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{
		BridgeURL:     server.URL,
		ConsoleLines:  10,
		ReplyMaxChars: 180,
		MaxReplyParts: 4,
		StateFile:     t.TempDir() + "/state.json",
		SessionPrefix: "mc:relay",
		AgentTimeout:  time.Second,
		HTTPTimeout:   time.Second,
	}
	state := State{Initialized: true, Seen: map[string]int64{}}
	runner := &fakeAgentRunner{reply: "Your wrench is at (1,2,3):4"}
	history := &fakeHistoryStore{
		recent:   []history.Message{{Source: agentChannelMinecraft, AuthorName: "Alex", Content: "I put the wrench away"}},
		recalled: []history.RecallItem{{Message: history.Message{Source: agentChannelMinecraft, AuthorName: "Alex", Content: "I put the wrench away"}, Reason: "fts"}},
	}

	processOnceWithHistory(server.Client(), cfg, &state, runner, history)

	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}
	if len(said) != 1 {
		t.Fatalf("expected one /mc/say call, got %#v", said)
	}
	want := "Your wrench is at [x:1, y:2, z:3] count=4"
	if said[0] != want {
		t.Fatalf("unexpected say text: got=%q want=%q", said[0], want)
	}
	if _, ok := state.Seen["event-1"]; !ok {
		t.Fatalf("event was not marked seen")
	}
	if len(history.recordedChats) != 1 {
		t.Fatalf("expected one recorded chat, got %#v", history.recordedChats)
	}
	if len(history.recordedReplies) != 1 {
		t.Fatalf("expected one recorded reply, got %#v", history.recordedReplies)
	}
	if !reflect.DeepEqual(runner.calls[0].RecentHistory, history.recent) {
		t.Fatalf("recent history was not passed to runner: got=%#v", runner.calls[0].RecentHistory)
	}
	if !reflect.DeepEqual(runner.calls[0].RecalledContext, history.recalled) {
		t.Fatalf("recalled context was not passed to runner: got=%#v", runner.calls[0].RecalledContext)
	}
}

func TestProcessOncePublishesOneSentenceCommentaryBeforeFinalReply(t *testing.T) {
	var said []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mc/console":
			_ = json.NewEncoder(w).Encode(ConsoleResponse{
				OK:    true,
				Count: 1,
				Events: []ConsoleEvent{{
					EventID:   "event-commentary",
					Timestamp: "2026-04-28T00:00:00Z",
					Player:    "Steve",
					Text:      "greg where is my wrench?",
					Triggered: true,
				}},
			})
		case "/mc/say":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode say payload: %v", err)
			}
			said = append(said, payload["text"])
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{
		BridgeURL:          server.URL,
		ConsoleLines:       10,
		ReplyMaxChars:      180,
		MaxReplyParts:      4,
		StateFile:          t.TempDir() + "/state.json",
		SessionPrefix:      "mc:relay",
		AgentTimeout:       time.Second,
		HTTPTimeout:        time.Second,
		ProgressEnabled:    true,
		ProgressMaxUpdates: 2,
	}
	state := State{Initialized: true, Seen: map[string]int64{}}
	runner := &fakeAgentRunner{
		reply: "Steve - asking where your wrench is: It is in the LV chest.",
		commentary: []string{
			"Checking the inventory index. This second sentence must be removed.",
			"Checking the inventory index. This duplicate must be removed.",
			"Comparing nearby chests! This second sentence must be removed.",
			"This update exceeds the configured cap.",
		},
	}
	history := &fakeHistoryStore{}

	processOnceWithHistory(server.Client(), cfg, &state, runner, history)

	want := []string{
		"Checking the inventory index.",
		"Comparing nearby chests!",
		"Steve - asking where your wrench is: It is in the LV chest.",
	}
	if !reflect.DeepEqual(said, want) {
		t.Fatalf("unexpected Minecraft message order: got=%#v want=%#v", said, want)
	}
	if len(history.recordedReplies) != 1 || history.recordedReplies[0].Content != want[2] {
		t.Fatalf("commentary should not be recorded in history: %#v", history.recordedReplies)
	}
}

func TestProcessOnceAcceptsUntriggeredSamePlayerMessageAsSteering(t *testing.T) {
	var mu sync.Mutex
	consoleCalls := 0
	var said []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mc/console":
			mu.Lock()
			consoleCalls++
			call := consoleCalls
			mu.Unlock()
			events := []ConsoleEvent{{
				EventID:   "event-original",
				Timestamp: "2026-04-28T00:00:00Z",
				Player:    "Steve",
				Text:      "greg find my wrench",
				Triggered: true,
			}}
			if call > 1 {
				events = append(events, ConsoleEvent{
					EventID:   "event-steering",
					Timestamp: "2026-04-28T00:00:01Z",
					Player:    "Steve",
					Text:      "only check the nearby chest",
					Triggered: false,
				})
			}
			_ = json.NewEncoder(w).Encode(ConsoleResponse{OK: true, Count: len(events), Events: events})
		case "/mc/say":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode say payload: %v", err)
			}
			mu.Lock()
			said = append(said, payload["text"])
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{
		BridgeURL:            server.URL,
		ConsoleLines:         10,
		ReplyMaxChars:        180,
		MaxReplyParts:        4,
		StateFile:            t.TempDir() + "/state.json",
		SessionPrefix:        "mc:relay",
		AgentTimeout:         time.Second,
		HTTPTimeout:          time.Second,
		ProgressEnabled:      true,
		ProgressMaxUpdates:   3,
		SteeringPollInterval: 5 * time.Millisecond,
	}
	state := State{Initialized: true, Seen: map[string]int64{}}
	runner := &steeringAgentRunner{received: make(chan agentcore.SteeringMessage, 1)}
	history := &fakeHistoryStore{}

	processOnceWithHistory(server.Client(), cfg, &state, runner, history)

	steering := <-runner.received
	if steering.Content != "only check the nearby chest" {
		t.Fatalf("steering content = %q", steering.Content)
	}
	mu.Lock()
	gotSaid := append([]string(nil), said...)
	mu.Unlock()
	wantSaid := []string{
		"Adjusting to your latest direction.",
		"Steve - asking for only the nearby chest: The wrench is there.",
	}
	if !reflect.DeepEqual(gotSaid, wantSaid) {
		t.Fatalf("Minecraft messages = %#v, want %#v", gotSaid, wantSaid)
	}
	if _, ok := state.Seen["event-steering"]; !ok {
		t.Fatal("steering event was not marked seen")
	}
	if len(history.recordedChats) != 2 {
		t.Fatalf("original and steering chat should be recorded: %#v", history.recordedChats)
	}
}

func TestOneSentenceForMCTruncatesToChatLimit(t *testing.T) {
	got := oneSentenceForMC("Checking   the inventory index without punctuation\nand another line", 24)
	if got != "Checking the inventory" {
		t.Fatalf("unexpected commentary truncation: %q", got)
	}
}

func TestProcessOnceRecordsNonTriggeredChatWithoutAgentCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mc/console":
			_ = json.NewEncoder(w).Encode(ConsoleResponse{
				OK:    true,
				Count: 1,
				Events: []ConsoleEvent{{
					EventID:   "event-2",
					Timestamp: "2026-04-28T00:00:00Z",
					Player:    "Alex",
					Text:      "anyone have spare rubber?",
					Triggered: false,
				}},
			})
		case "/mc/say":
			t.Fatalf("unexpected /mc/say call")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := Config{
		BridgeURL:     server.URL,
		ConsoleLines:  10,
		ReplyMaxChars: 180,
		MaxReplyParts: 4,
		StateFile:     t.TempDir() + "/state.json",
		SessionPrefix: "mc:relay",
		AgentTimeout:  time.Second,
		HTTPTimeout:   time.Second,
	}
	state := State{Initialized: true, Seen: map[string]int64{}}
	runner := &fakeAgentRunner{reply: "should not be called"}
	history := &fakeHistoryStore{}

	processOnceWithHistory(server.Client(), cfg, &state, runner, history)

	if len(runner.calls) != 0 {
		t.Fatalf("expected no runner calls, got %d", len(runner.calls))
	}
	if len(history.recordedChats) != 1 {
		t.Fatalf("expected one recorded chat, got %#v", history.recordedChats)
	}
	if history.recordedChats[0].Content != "anyone have spare rubber?" {
		t.Fatalf("unexpected recorded chat: %#v", history.recordedChats[0])
	}
	if len(history.recordedReplies) != 0 {
		t.Fatalf("expected no recorded replies, got %#v", history.recordedReplies)
	}
	if _, ok := state.Seen["event-2"]; !ok {
		t.Fatalf("event was not marked seen")
	}
}
