package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"greggpt-gtnh/internal/agent"
	"greggpt-gtnh/internal/agent/history"

	"github.com/bwmarrin/discordgo"
)

type fakeAgentRunner struct {
	requests []DiscordAgentRequest
	resp     DiscordAgentResponse
	err      error
}

func (f *fakeAgentRunner) Run(ctx context.Context, req DiscordAgentRequest) (DiscordAgentResponse, error) {
	f.requests = append(f.requests, req)
	return f.resp, f.err
}

type fakeHistoryBackend struct {
	records []history.Message
	recent  []history.Message
	recall  []history.RecallItem
}

func (f *fakeHistoryBackend) AppendMessage(ctx context.Context, msg history.Message) error {
	f.records = append(f.records, msg)
	return nil
}

func (f *fakeHistoryBackend) Recent(ctx context.Context, q history.Query) ([]history.Message, error) {
	return append([]history.Message(nil), f.recent...), nil
}

func (f *fakeHistoryBackend) Recall(ctx context.Context, q history.Query) ([]history.RecallItem, error) {
	return append([]history.RecallItem(nil), f.recall...), nil
}

type capturingAgentClient struct {
	requests []agent.ModelRequest
	final    string
}

func (c *capturingAgentClient) CreateResponse(ctx context.Context, req agent.ModelRequest) (agent.ModelResponse, error) {
	c.requests = append(c.requests, req)
	return agent.ModelResponse{FinalText: c.final}, nil
}

type emptyToolRegistry struct{}

func (emptyToolRegistry) Tools(context.Context) ([]agent.ToolDefinition, error) {
	return nil, nil
}

func (emptyToolRegistry) Execute(context.Context, agent.ToolCall) (string, error) {
	return "", nil
}

func TestSplitNames(t *testing.T) {
	got := splitNames("Alice, Bob;Carol  Dana")
	want := []string{"Alice", "Bob", "Carol", "Dana"}
	if len(got) != len(want) {
		t.Fatalf("unexpected length: got=%#v want=%#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitNames mismatch at %d: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("unexpected short truncate: %q", got)
	}
	if got := truncate("this string is longer", 10); got != "this st..." {
		t.Fatalf("unexpected long truncate: %q", got)
	}
}

func TestWrapCodeBlock(t *testing.T) {
	got := wrapCodeBlock(`{"ok":true}`)
	want := "```json\n{\"ok\":true}\n```"
	if got != want {
		t.Fatalf("unexpected json code block: %q", got)
	}
}

func TestInventoryCommandScopeChoicesIncludeMEAndAll(t *testing.T) {
	cmd := inventoryCommand()
	var findItemScopes []string
	for _, opt := range cmd.Options {
		if opt.Name != "find_item" {
			continue
		}
		for _, sub := range opt.Options {
			if sub.Name == "scope" {
				for _, choice := range sub.Choices {
					findItemScopes = append(findItemScopes, choice.Value.(string))
				}
			}
		}
	}
	want := map[string]bool{"all": true, "containers": true, "me": true}
	for _, got := range findItemScopes {
		delete(want, got)
	}
	if len(want) != 0 {
		t.Fatalf("missing inventory scope choices: %#v (got %#v)", want, findItemScopes)
	}
}

func TestQueryCommandOnlyExposesWikiPage(t *testing.T) {
	cmd := queryCommand()
	var names []string
	for _, opt := range cmd.Options {
		names = append(names, opt.Name)
	}
	if len(names) != 1 || names[0] != "wiki_page" {
		t.Fatalf("query command options = %#v, want only wiki_page", names)
	}
}

func TestGregGPTMentionUnauthorizedUser(t *testing.T) {
	runner := &fakeAgentRunner{resp: DiscordAgentResponse{Reply: "should not run"}}
	svc := &Service{
		cfg: Config{
			MentionAgent: true,
			AllowedUsers: userSet([]string{"allowed-user"}),
		},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "<@123> how many circuits do we have?",
		AuthorID:    "other-user",
		ChannelID:   "channel",
		MessageID:   "message",
		MentionsBot: true,
	}, nil, nil, nil)

	if !result.Handled || result.Reason != "not_allowed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("runner should not be called for unauthorized users: %#v", runner.requests)
	}
}

func TestGregGPTMentionNonMentionIgnored(t *testing.T) {
	runner := &fakeAgentRunner{resp: DiscordAgentResponse{Reply: "should not run"}}
	svc := &Service{
		cfg:    Config{MentionAgent: true},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "how many circuits do we have?",
		AuthorID:    "user",
		ChannelID:   "channel",
		MessageID:   "message",
		MentionsBot: false,
	}, nil, nil, nil)

	if result.Handled || result.Reason != "not_mentioned" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("runner should not be called for non-mentions: %#v", runner.requests)
	}
}

func TestGregGPTMentionInventoryRoutesToAgent(t *testing.T) {
	runner := &fakeAgentRunner{resp: DiscordAgentResponse{Reply: "circuits: 42"}}
	svc := &Service{
		cfg:    Config{MentionAgent: true},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "<@123> how many LV circuits are in ME?",
		AuthorID:    "user",
		ChannelID:   "channel",
		MessageID:   "message",
		MentionsBot: true,
	}, nil, nil, nil)

	if !result.Handled || result.Reply != "circuits: 42" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one runner request, got %#v", runner.requests)
	}
	if got := runner.requests[0].Text; got != "how many LV circuits are in ME?" {
		t.Fatalf("unexpected agent text: %q", got)
	}
}

func TestGregGPTMentionPassesStructuredHistoryAndRecall(t *testing.T) {
	runner := &fakeAgentRunner{resp: DiscordAgentResponse{Reply: "ok"}}
	recent := []history.Message{{
		Source:     "discord",
		ChannelID:  "discord-channel",
		AuthorID:   "other-user",
		AuthorName: "Other User",
		Content:    "prior discord note",
		Timestamp:  time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}, {
		Source:     "minecraft",
		ChannelID:  "minecraft",
		AuthorName: "Player",
		Content:    "prior minecraft note",
		Timestamp:  time.Date(2026, 4, 30, 12, 1, 0, 0, time.UTC),
	}}
	recall := []history.RecallItem{{
		Message: history.Message{
			Source:     "discord",
			AuthorName: "Other User",
			Content:    "remembered context",
		},
		Reason: "remembered",
		Score:  0.9,
	}}
	svc := &Service{
		cfg:    Config{MentionAgent: true},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "<@123> summarize prior context",
		AuthorID:    "user",
		AuthorName:  "User",
		ChannelID:   "discord-channel",
		MessageID:   "message",
		MentionsBot: true,
	}, nil, recent, recall)

	if !result.Handled || result.Reason != "ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one runner request, got %#v", runner.requests)
	}
	req := runner.requests[0]
	if len(req.History) != len(recent) || req.History[0].Content != "prior discord note" || req.History[1].Source != "minecraft" {
		t.Fatalf("structured history was not passed through: %#v", req.History)
	}
	if len(req.RecalledContext) != 1 || req.RecalledContext[0].Message.Content != "remembered context" {
		t.Fatalf("recalled context was not passed through: %#v", req.RecalledContext)
	}
}

func TestCommandAgentRunnerPassesStructuredHistoryAndRecall(t *testing.T) {
	client := &capturingAgentClient{final: "ok"}
	r := &commandAgentRunner{
		runner: agent.NewRunner(agent.Config{}, client, &emptyToolRegistry{}),
	}
	recent := []history.Message{{
		Source:     "discord",
		AuthorName: "player",
		Content:    "recent history",
	}}
	recall := []history.RecallItem{{
		Message: history.Message{
			Source:     "minecraft",
			AuthorName: "player",
			Content:    "recalled item",
		},
		Reason: "match",
	}}

	resp, err := r.Run(context.Background(), DiscordAgentRequest{
		Channel:         agent.ChannelDiscord,
		Text:            "what changed?",
		UserID:          "user",
		History:         recent,
		RecalledContext: recall,
	})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if resp.Reply != "ok" {
		t.Fatalf("unexpected reply: %#v", resp)
	}
	if len(client.requests) != 1 || len(client.requests[0].Input) != 1 {
		t.Fatalf("expected one model request, got %#v", client.requests)
	}
	content := client.requests[0].Input[0].Content
	for _, want := range []string{"recent_history:", "discord player: recent history", "recalled_context:", "minecraft player: recalled item"} {
		if !strings.Contains(content, want) {
			t.Fatalf("agent request content missing %q: %s", want, content)
		}
	}
}

func TestGregGPTMentionRecipeWikiAndTaskMutationsRouteToAgent(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "recipe", text: "<@123> what is the recipe chain for an electric blast furnace?"},
		{name: "wiki", text: "<@123> summarize the wiki page for cleanroom"},
		{name: "task mutation", text: "<@123> add a high priority task to build the IV line"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeAgentRunner{resp: DiscordAgentResponse{Reply: "ok"}}
			svc := &Service{
				cfg:    Config{MentionAgent: true},
				runner: runner,
			}

			result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
				Content:     tc.text,
				AuthorID:    "user",
				ChannelID:   "channel",
				MessageID:   "message",
				MentionsBot: true,
			}, nil, nil, nil)

			if !result.Handled || result.Reason != "ok" {
				t.Fatalf("unexpected result: %#v", result)
			}
			if len(runner.requests) != 1 {
				t.Fatalf("expected one runner request, got %#v", runner.requests)
			}
			if strings.Contains(runner.requests[0].Text, "<@123>") {
				t.Fatalf("mention was not stripped from agent text: %q", runner.requests[0].Text)
			}
		})
	}
}

func TestFormatDiscordHistoryTruncatesChronologicalContext(t *testing.T) {
	history := []discordHistoryMessage{
		{AuthorName: "latest", Content: "third message"},
		{AuthorName: "middle", Content: "second message"},
		{AuthorName: "oldest", Content: "first message"},
	}

	got := formatDiscordHistory(history, false, 180)

	if strings.Contains(got, "oldest: first message") {
		t.Fatalf("oldest history should be dropped first when truncating: %q", got)
	}
	if !strings.Contains(got, "middle: second message\nlatest: third message") {
		t.Fatalf("retained history should stay chronological and preserve newest messages, got %q", got)
	}
	if len(got) > 180 {
		t.Fatalf("history exceeded max chars: len=%d text=%q", len(got), got)
	}
	if !strings.HasPrefix(got, "Prior Discord context only.") {
		t.Fatalf("history should be explicitly labeled as prior context: %q", got)
	}
}

func TestCleanBotMentionTextPreservesOtherMentions(t *testing.T) {
	got := cleanBotMentionText("<@123> what does <@456> need?", "123")
	if got != "what does <@456> need?" {
		t.Fatalf("unexpected cleaned mention text: %q", got)
	}
}

func TestFormatDiscordHistoryExcludesBotMessagesByDefault(t *testing.T) {
	history := []discordHistoryMessage{
		{AuthorName: "bot", Content: "bot reply", IsBot: true},
		{AuthorName: "player", Content: "player question"},
	}

	got := formatDiscordHistory(history, false, 4000)

	if strings.Contains(got, "bot reply") {
		t.Fatalf("bot message included despite includeBots=false: %q", got)
	}
	if !strings.Contains(got, "player: player question") {
		t.Fatalf("player message missing from history: %q", got)
	}
	if !strings.Contains(got, "current request is in the message field") {
		t.Fatalf("history missing prior-context warning: %q", got)
	}
}

func TestFormatDiscordHistoryIncludesAttachmentMetadataOnly(t *testing.T) {
	history := []discordHistoryMessage{{
		AuthorName: "player",
		Content:    "see this",
		Attachments: []discordHistoryAttachment{{
			Filename:    "setup.png",
			ContentType: "image/png",
			Size:        12345,
			Width:       640,
			Height:      480,
		}},
	}}

	got := formatDiscordHistory(history, false, 4000)

	for _, want := range []string{"player: see this", "filename=setup.png", "content_type=image/png", "size=12345", "dimensions=640x480"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted history missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "http://") || strings.Contains(got, "https://") {
		t.Fatalf("attachment URLs should not be included: %q", got)
	}
}

func TestRecordObservedDiscordMessageStoresNonMentionContentAndAttachmentMetadata(t *testing.T) {
	hist := &fakeHistoryBackend{}
	svc := &Service{
		cfg: Config{
			Agent: AgentConfig{Config: agent.Config{HistoryEnabled: true}},
		},
		hist: hist,
	}
	timestamp := time.Date(2026, 4, 30, 13, 0, 0, 0, time.UTC)

	svc.recordObservedDiscordMessage(context.Background(), &discordgo.Message{
		ID:        "message-id",
		ChannelID: "channel-id",
		Content:   "  non mention context  ",
		Timestamp: timestamp,
		Author: &discordgo.User{
			ID:       "user-id",
			Username: "User",
		},
		Attachments: []*discordgo.MessageAttachment{{
			Filename:    "setup.png",
			ContentType: "image/png",
			Size:        123,
			Width:       64,
			Height:      32,
		}},
	})

	if len(hist.records) != 1 {
		t.Fatalf("expected one history record, got %#v", hist.records)
	}
	got := hist.records[0]
	if got.Source != "discord" || got.ChannelID != "channel-id" || got.ExternalMessageID != "message-id" {
		t.Fatalf("unexpected history identity: %#v", got)
	}
	for _, want := range []string{"non mention context", "filename=setup.png", "content_type=image/png", "size=123", "dimensions=64x32"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("recorded content missing %q: %#v", want, got)
		}
	}
	if !got.Timestamp.Equal(timestamp) {
		t.Fatalf("unexpected timestamp: %s", got.Timestamp)
	}
}

func TestRecordObservedDiscordMessageHonorsBotIncludeConfig(t *testing.T) {
	hist := &fakeHistoryBackend{}
	svc := &Service{
		cfg: Config{
			DiscordHistoryIncludeBot: false,
			Agent:                    AgentConfig{Config: agent.Config{HistoryEnabled: true}},
		},
		hist: hist,
	}

	svc.recordObservedDiscordMessage(context.Background(), &discordgo.Message{
		ID:        "bot-message",
		ChannelID: "channel-id",
		Content:   "bot context",
		Author:    &discordgo.User{ID: "bot-id", Username: "Bot", Bot: true},
	})

	if len(hist.records) != 0 {
		t.Fatalf("bot message should not be recorded when include bots is disabled: %#v", hist.records)
	}
}

func TestRecordGregGPTDiscordReplyStoresAfterSendPath(t *testing.T) {
	hist := &fakeHistoryBackend{}
	svc := &Service{
		cfg: Config{
			Agent: AgentConfig{Config: agent.Config{HistoryEnabled: true}},
		},
		hist: hist,
	}

	svc.recordGregGPTDiscordReply(context.Background(), discordMentionMessage{
		BotID:     "bot-id",
		ChannelID: "channel-id",
		MessageID: "source-message",
	}, " reply text ", "sent-message")

	if len(hist.records) != 1 {
		t.Fatalf("expected reply record, got %#v", hist.records)
	}
	got := hist.records[0]
	if !got.IsBot || got.AuthorID != "bot-id" || got.AuthorName != "GregGPT" || got.Content != "reply text" || got.ExternalMessageID != "sent-message" {
		t.Fatalf("unexpected reply record: %#v", got)
	}
}

func TestGregGPTMentionInventoryRetryUsesHistory(t *testing.T) {
	runner := &fakeAgentRunner{resp: DiscordAgentResponse{Reply: "retried"}}
	svc := &Service{
		cfg: Config{
			MentionAgent:             true,
			DiscordHistoryMaxChars:   4000,
			DiscordHistoryIncludeBot: false,
		},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "<@123> retry",
		AuthorID:    "user",
		ChannelID:   "channel",
		MessageID:   "message",
		MentionsBot: true,
	}, []discordHistoryMessage{
		{AuthorName: "GregGPT", Content: `previous output: gtnh_inventory find-item --query "stainless steel dust" --scope me`, IsBot: true},
	}, nil, nil)

	if !result.Handled || result.Reply != "retried" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one runner request, got %#v", runner.requests)
	}
	retry := runner.requests[0].RetryInventory
	if retry == nil || retry.Query != "stainless steel dust" || retry.Scope != "me" {
		t.Fatalf("unexpected retry metadata: %#v", retry)
	}
	if !strings.Contains(runner.requests[0].Text, "stainless steel dust") {
		t.Fatalf("retry prompt did not include query: %q", runner.requests[0].Text)
	}
	if strings.Contains(runner.requests[0].RecentMessages, "stainless steel dust") {
		t.Fatalf("bot retry source should not be included in recent context by default: %q", runner.requests[0].RecentMessages)
	}
}

func TestGregGPTMentionAgentErrorReply(t *testing.T) {
	runner := &fakeAgentRunner{err: errors.New("agent unavailable")}
	svc := &Service{
		cfg:    Config{MentionAgent: true},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "<@123> summarize the kanban board",
		AuthorID:    "user",
		ChannelID:   "channel",
		MessageID:   "message",
		MentionsBot: true,
	}, nil, nil, nil)

	if !result.Handled || result.Reason != "agent_error" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.Contains(result.Reply, "agent unavailable") {
		t.Fatalf("expected error in reply, got %q", result.Reply)
	}
}

func TestGregGPTMentionTimeoutSummaryReply(t *testing.T) {
	runner := &fakeAgentRunner{
		resp: DiscordAgentResponse{Reply: "I hit the 5 minute response limit.\n\nWork completed before timeout:\n- Ran `inventory_find_item`: ambiguous item query"},
		err:  errors.New("context deadline exceeded"),
	}
	svc := &Service{
		cfg:    Config{MentionAgent: true},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "<@123> where are the stainless steel dusts?",
		AuthorID:    "user",
		ChannelID:   "channel",
		MessageID:   "message",
		MentionsBot: true,
	}, nil, nil, nil)

	if !result.Handled || result.Reason != "agent_timeout_summary" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Contains(result.Reply, "GregGPT could not handle that") {
		t.Fatalf("timeout summary should not include raw error wrapper: %q", result.Reply)
	}
	if !strings.Contains(result.Reply, "ambiguous item query") {
		t.Fatalf("expected progress summary in reply, got %q", result.Reply)
	}
}
