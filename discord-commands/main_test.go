package main

import (
	"context"
	"errors"
	"strings"
	"testing"
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

func TestFormatWikiSearchOutput(t *testing.T) {
	raw := `{"ok":true,"query":"electric blast furnace","total_hits":3,"results":[{"title":"Electric blast furnace","url":"https://wiki.gtnewhorizons.com/wiki/Electric_blast_furnace"},{"title":"Electric Blast Furnace","url":"https://wiki.gtnewhorizons.com/wiki/Electric_Blast_Furnace"}],"source":"wiki.gtnewhorizons.com/w/api.php"}`
	got := formatWikiSearchOutput(raw)
	if got == raw {
		t.Fatalf("expected formatted output, got raw json")
	}
	if got != "electric blast furnace (3 hits)\n1. Electric blast furnace - https://wiki.gtnewhorizons.com/wiki/Electric_blast_furnace\n2. Electric Blast Furnace - https://wiki.gtnewhorizons.com/wiki/Electric_Blast_Furnace" {
		t.Fatalf("unexpected formatted output: %q", got)
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
	}, nil)

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
	}, nil)

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
	}, nil)

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
			}, nil)

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

func TestGregGPTMentionInventoryRetryUsesHistory(t *testing.T) {
	runner := &fakeAgentRunner{resp: DiscordAgentResponse{Reply: "retried"}}
	svc := &Service{
		cfg:    Config{MentionAgent: true},
		runner: runner,
	}

	result := svc.processGregGPTMention(context.Background(), discordMentionMessage{
		Content:     "<@123> retry",
		AuthorID:    "user",
		ChannelID:   "channel",
		MessageID:   "message",
		MentionsBot: true,
	}, []string{`previous output: gtnh_inventory find-item --query "stainless steel dust" --scope me`})

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
	}, nil)

	if !result.Handled || result.Reason != "agent_error" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.Contains(result.Reply, "agent unavailable") {
		t.Fatalf("expected error in reply, got %q", result.Reply)
	}
}
