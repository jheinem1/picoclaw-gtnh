package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"greggpt-gtnh/internal/agent/history"
	"greggpt-gtnh/internal/greggpttools"
)

func TestRunnerFinalOnly(t *testing.T) {
	client := &fakeClient{responses: []ModelResponse{{FinalText: "**Use a steam macerator.**"}}}
	registry := newFakeRegistry()
	runner := NewRunner(Config{Model: "test-model"}, client, registry)

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelMinecraft,
		User:    "Snobacco",
		Message: "greg how do I crush ore",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got != "Use a steam macerator." {
		t.Fatalf("unexpected final text: %q", got)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected one model request, got %d", len(client.requests))
	}
	modelReq := client.requests[0]
	if modelReq.Model != "test-model" {
		t.Fatalf("unexpected model: %q", modelReq.Model)
	}
	if !strings.Contains(modelReq.Instructions, "ASCII only") {
		t.Fatalf("minecraft instructions missing ASCII rule: %q", modelReq.Instructions)
	}
	if !strings.Contains(modelReq.Input[0].Content, "channel: minecraft") {
		t.Fatalf("request missing channel context: %q", modelReq.Input[0].Content)
	}
}

func TestRunnerLoadsWorkspaceAgentsInstructions(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("Use inventory_find_block_name for Super Chest coordinates."), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	client := &fakeClient{responses: []ModelResponse{{FinalText: "ok"}}}
	runner := NewRunner(Config{Workspace: workspace}, client, newFakeRegistry())

	if _, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		Message: "where is the Super Chest?",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected one model request, got %d", len(client.requests))
	}
	instructions := client.requests[0].Instructions
	if !strings.Contains(instructions, "Markdown is allowed") || !strings.Contains(instructions, "inventory_find_block_name") {
		t.Fatalf("runtime instructions missing profile or AGENTS.md content: %q", instructions)
	}
}

func TestRunnerFormatsMultilineContextAsDelimitedBlock(t *testing.T) {
	client := &fakeClient{responses: []ModelResponse{{FinalText: "ok"}}}
	runner := NewRunner(Config{}, client, newFakeRegistry())

	if _, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		Message: "current prompt",
		Context: map[string]string{
			"discord_recent_messages": "old: first\nold: second",
		},
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	content := client.requests[0].Input[0].Content
	if !strings.Contains(content, "discord_recent_messages:\n<<<\nold: first\nold: second\n>>>\nmessage:\ncurrent prompt") {
		t.Fatalf("multiline context was not delimited before current message: %q", content)
	}
}

func TestRunnerFormatsStructuredHistoryAndRecall(t *testing.T) {
	client := &fakeClient{responses: []ModelResponse{{FinalText: "ok"}}}
	runner := NewRunner(Config{}, client, newFakeRegistry())

	if _, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		Message: "current prompt",
		History: []history.Message{{
			Source:     "discord",
			ChannelID:  "general",
			AuthorName: "Alice",
			Content:    "prior discord context",
			Timestamp:  time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		}, {
			Source:     "minecraft",
			ChannelID:  "minecraft",
			AuthorName: "Steve",
			Content:    "prior minecraft context",
			Timestamp:  time.Date(2026, 4, 30, 12, 1, 0, 0, time.UTC),
		}},
		RecalledContext: []history.RecallItem{{
			Message: history.Message{
				Source:     "discord",
				AuthorName: "Bob",
				Content:    "matched recalled context",
			},
			Reason: "fts",
			Score:  0.75,
		}},
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	content := client.requests[0].Input[0].Content
	for _, want := range []string{
		"recent_history:\n<<<",
		"Prior unified Discord/Minecraft context only",
		"discord/general 2026-04-30T12:00:00Z Alice: prior discord context",
		"minecraft/minecraft 2026-04-30T12:01:00Z Steve: prior minecraft context",
		"recalled_context:\n<<<",
		"matched recalled context",
		"message:\ncurrent prompt",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("request content missing %q: %s", want, content)
		}
	}
}

func TestRunnerSingleTool(t *testing.T) {
	client := &fakeClient{responses: []ModelResponse{
		{ToolCalls: []ToolCall{toolCall("call-1", "lookup", `{"q":"steel"}`)}},
		{FinalText: "Steel needs a blast furnace."},
	}}
	registry := newFakeRegistry()
	registry.outputs["lookup"] = "tool says steel"
	runner := NewRunner(Config{}, client, registry)

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		User:    "jhein",
		Message: "how do I make steel?",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got != "Steel needs a blast furnace." {
		t.Fatalf("unexpected final text: %q", got)
	}
	if len(registry.calls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(registry.calls))
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two model requests, got %d", len(client.requests))
	}
	secondInput := client.requests[1].Input
	last := secondInput[len(secondInput)-1]
	if last.Role != roleToolOutput || last.ToolCallID != "call-1" || last.Content != "tool says steel" {
		t.Fatalf("unexpected tool output item: %#v", last)
	}
	if !strings.Contains(client.requests[0].Instructions, "Markdown is allowed") {
		t.Fatalf("discord instructions missing markdown allowance: %q", client.requests[0].Instructions)
	}
}

func TestRunnerMultiTool(t *testing.T) {
	client := &fakeClient{responses: []ModelResponse{
		{ToolCalls: []ToolCall{
			toolCall("call-1", "lookup", `{"q":"ebf"}`),
			toolCall("call-2", "inventory", `{"item":"steel"}`),
		}},
		{FinalText: "Build the EBF, then check your steel stock."},
	}}
	registry := newFakeRegistry()
	registry.outputs["lookup"] = "electric blast furnace"
	registry.outputs["inventory"] = "steel: 128"
	runner := NewRunner(Config{MaxToolCalls: 4}, client, registry)

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		Message: "what next for steel?",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got != "Build the EBF, then check your steel stock." {
		t.Fatalf("unexpected final text: %q", got)
	}
	if len(registry.calls) != 2 {
		t.Fatalf("expected two tool calls, got %d", len(registry.calls))
	}
	input := client.requests[1].Input
	if len(input) != 5 ||
		input[1].Role != roleToolCall || input[1].ToolCallID != "call-1" ||
		input[2].Role != roleToolOutput || input[2].Content != "electric blast furnace" ||
		input[3].Role != roleToolCall || input[3].ToolCallID != "call-2" ||
		input[4].Role != roleToolOutput || input[4].Content != "steel: 128" {
		t.Fatalf("missing ordered tool outputs: %#v", input)
	}
}

func TestRunnerToolErrorBecomesToolOutput(t *testing.T) {
	client := &fakeClient{responses: []ModelResponse{
		{ToolCalls: []ToolCall{toolCall("call-1", "lookup", `{"q":""}`)}},
		{FinalText: "The lookup failed validation."},
	}}
	registry := newFakeRegistry()
	registry.errs["lookup"] = ValidationError{Message: "q is required"}
	runner := NewRunner(Config{}, client, registry)

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		Message: "look this up",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got != "The lookup failed validation." {
		t.Fatalf("unexpected final text: %q", got)
	}
	last := client.requests[1].Input[len(client.requests[1].Input)-1]
	if last.Content != "validation error: q is required" {
		t.Fatalf("unexpected validation output: %q", last.Content)
	}
}

func TestRunnerMaxToolCalls(t *testing.T) {
	client := &fakeClient{responses: []ModelResponse{
		{ToolCalls: []ToolCall{toolCall("call-1", "lookup", `{"q":"one"}`)}},
		{ToolCalls: []ToolCall{toolCall("call-2", "lookup", `{"q":"two"}`)}},
	}}
	registry := newFakeRegistry()
	registry.outputs["lookup"] = "again"
	runner := NewRunner(Config{MaxToolCalls: 1}, client, registry)

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelMinecraft,
		Message: "keep searching",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got != "I hit the tool-call limit before finishing." {
		t.Fatalf("unexpected exhausted response: %q", got)
	}
	if len(registry.calls) != 1 {
		t.Fatalf("expected only one executed tool call, got %d", len(registry.calls))
	}
}

func TestRunnerBackendErrorReturnsToCaller(t *testing.T) {
	wantErr := errors.New("auth failed")
	client := &fakeClient{err: wantErr}
	runner := NewRunner(Config{}, client, newFakeRegistry())

	_, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		Message: "hello",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected backend error, got %v", err)
	}
}

func TestRunnerTimeoutSummarizesToolProgress(t *testing.T) {
	client := &fakeClient{
		responses: []ModelResponse{
			{ToolCalls: []ToolCall{toolCall("call-1", "lookup", `{"q":"super chest"}`)}},
		},
		errAfterResponses: context.DeadlineExceeded,
	}
	registry := newFakeRegistry()
	registry.outputs["lookup"] = "error: ambiguous item query \"Super Chest\" matched 8 items"
	runner := NewRunner(Config{}, client, registry)

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		User:    "exx",
		Message: "where is the super chest?",
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	var timeoutErr TimeoutSummaryError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected TimeoutSummaryError, got %T: %v", err, err)
	}
	if !strings.Contains(timeoutErr.Summary, "5 minute response limit") || !strings.Contains(timeoutErr.Summary, "ambiguous item query") {
		t.Fatalf("unexpected summary: %q", timeoutErr.Summary)
	}
	if got != "" {
		t.Fatalf("expected empty returned text with summary in error, got %q", got)
	}
}

func TestRunnerTimeoutUsesNoToolRecoverySummary(t *testing.T) {
	client := &fakeClient{
		responses: []ModelResponse{
			{ToolCalls: []ToolCall{toolCall("call-1", "lookup", `{"q":"super chest"}`)}},
			{FinalText: "I found the lookup failed because Super Chest is ambiguous; narrow the item name."},
		},
		errsByRequest: []error{nil, context.DeadlineExceeded, nil},
	}
	registry := newFakeRegistry()
	registry.outputs["lookup"] = "error: ambiguous item query \"Super Chest\" matched 8 items"
	runner := NewRunner(Config{}, client, registry)

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		User:    "exx",
		Message: "where is the super chest?",
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	var timeoutErr TimeoutSummaryError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected TimeoutSummaryError, got %T: %v", err, err)
	}
	if timeoutErr.Summary != "I found the lookup failed because Super Chest is ambiguous; narrow the item name." {
		t.Fatalf("unexpected recovered summary: %q", timeoutErr.Summary)
	}
	if got != "" {
		t.Fatalf("expected empty returned text with summary in error, got %q", got)
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected initial, timed-out, and recovery requests; got %d", len(client.requests))
	}
	recovery := client.requests[2]
	if !recovery.DisableTools {
		t.Fatalf("recovery request did not disable tools: %#v", recovery)
	}
	if len(recovery.Tools) != 0 {
		t.Fatalf("recovery request carried tools: %#v", recovery.Tools)
	}
	if !strings.Contains(recovery.Input[0].Content, "ambiguous item query") ||
		!strings.Contains(recovery.Input[0].Content, "Tool output `lookup`") {
		t.Fatalf("recovery transcript missing accumulated tool output: %q", recovery.Input[0].Content)
	}
}

func TestNewRunnerReadsTimeoutSummarySeconds(t *testing.T) {
	t.Setenv(EnvTimeoutSummary, "7")

	runner := NewRunner(Config{}, &fakeClient{}, newFakeRegistry())

	if runner.cfg.TimeoutSummary != 7*time.Second {
		t.Fatalf("TimeoutSummary = %s, want 7s", runner.cfg.TimeoutSummary)
	}
}

func TestRunnerInjectsSelectedMemory(t *testing.T) {
	workspace := t.TempDir()
	memoryPath := filepath.Join(workspace, "state", "greggpt_memory.json")
	store := greggpttools.NewMemoryStore(memoryPath, 0)
	for _, entry := range []greggpttools.MemoryEntry{
		{Scope: greggpttools.MemoryScopeGlobal, Key: "base", Value: "Steam age"},
		{Scope: greggpttools.MemoryScopeChannel, Channel: "discord", Key: "channel_goal", Value: "Build EBF"},
		{Scope: greggpttools.MemoryScopeChannel, Channel: "minecraft", Key: "other_channel", Value: "Do not inject"},
		{Scope: greggpttools.MemoryScopeUser, User: "jhein", Key: "user_pref", Value: "Likes concise answers"},
		{Scope: greggpttools.MemoryScopeUser, User: "someone_else", Key: "other_user", Value: "Do not inject"},
	} {
		if _, err := store.Remember(entry, nil); err != nil {
			t.Fatalf("Remember returned error: %v", err)
		}
	}

	client := &fakeClient{responses: []ModelResponse{{FinalText: "ok"}}}
	runner := NewRunner(Config{
		Workspace:              workspace,
		MemoryEnabled:          true,
		MemoryPath:             "state/greggpt_memory.json",
		MemoryMaxInjectedItems: 8,
		MemoryMaxInjectedBytes: 2000,
	}, client, newFakeRegistry())

	got, err := runner.Run(context.Background(), Request{
		Channel: ChannelDiscord,
		User:    "jhein",
		Message: "what next?",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got != "ok" {
		t.Fatalf("unexpected final text: %q", got)
	}
	content := client.requests[0].Input[0].Content
	for _, want := range []string{"memory:", "Steam age", "Build EBF", "Likes concise answers"} {
		if !strings.Contains(content, want) {
			t.Fatalf("request content missing %q: %s", want, content)
		}
	}
	for _, unwanted := range []string{"Do not inject", "other_channel", "other_user"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("request content included %q: %s", unwanted, content)
		}
	}
}

func toolCall(id, name, args string) ToolCall {
	return ToolCall{
		ID:        id,
		Name:      name,
		Arguments: json.RawMessage(args),
	}
}

type fakeClient struct {
	responses         []ModelResponse
	requests          []ModelRequest
	err               error
	errsByRequest     []error
	errAfterResponses error
}

func (f *fakeClient) CreateResponse(_ context.Context, req ModelRequest) (ModelResponse, error) {
	f.requests = append(f.requests, req)
	if len(f.errsByRequest) != 0 {
		err := f.errsByRequest[0]
		f.errsByRequest = f.errsByRequest[1:]
		if err != nil {
			return ModelResponse{}, err
		}
	}
	if f.err != nil {
		return ModelResponse{}, f.err
	}
	if len(f.responses) == 0 {
		if f.errAfterResponses != nil {
			return ModelResponse{}, f.errAfterResponses
		}
		return ModelResponse{}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

type fakeRegistry struct {
	defs    []ToolDefinition
	outputs map[string]string
	errs    map[string]error
	calls   []ToolCall
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		defs: []ToolDefinition{{
			Name:        "lookup",
			Description: "looks up GTNH data",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		outputs: map[string]string{},
		errs:    map[string]error{},
	}
}

func (f *fakeRegistry) Tools(context.Context) ([]ToolDefinition, error) {
	return append([]ToolDefinition(nil), f.defs...), nil
}

func (f *fakeRegistry) Execute(_ context.Context, call ToolCall) (string, error) {
	f.calls = append(f.calls, call)
	if err := f.errs[call.Name]; err != nil {
		return "", err
	}
	if output, ok := f.outputs[call.Name]; ok {
		return output, nil
	}
	return "", ValidationError{Message: "unknown tool: " + call.Name}
}
