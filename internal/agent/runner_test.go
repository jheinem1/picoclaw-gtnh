package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
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

func toolCall(id, name, args string) ToolCall {
	return ToolCall{
		ID:        id,
		Name:      name,
		Arguments: json.RawMessage(args),
	}
}

type fakeClient struct {
	responses []ModelResponse
	requests  []ModelRequest
	err       error
}

func (f *fakeClient) CreateResponse(_ context.Context, req ModelRequest) (ModelResponse, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return ModelResponse{}, f.err
	}
	if len(f.responses) == 0 {
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
