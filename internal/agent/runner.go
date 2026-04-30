package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"greggpt-gtnh/internal/greggpttools"
)

const (
	roleUser       = "user"
	roleToolCall   = "tool_call"
	roleToolOutput = "tool"
)

type Client interface {
	CreateResponse(context.Context, ModelRequest) (ModelResponse, error)
}

type Registry interface {
	Tools(context.Context) ([]ToolDefinition, error)
	Execute(context.Context, ToolCall) (string, error)
}

type Runner struct {
	cfg      Config
	client   Client
	registry Registry
}

type Request struct {
	Channel Channel
	User    string
	Message string
	Context map[string]string
}

type ModelRequest struct {
	Model              string
	ReasoningEffort    string
	Instructions       string
	PreviousResponseID string
	Input              []InputItem
	Tools              []ToolDefinition
}

type InputItem struct {
	Role       string
	Content    string
	ToolCallID string
	ToolName   string
}

type ModelResponse struct {
	ID        string
	FinalText string
	ToolCalls []ToolCall
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type TimeoutSummaryError struct {
	Summary string
	Cause   error
}

func (e TimeoutSummaryError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "agent timed out"
}

func (e TimeoutSummaryError) Unwrap() error {
	return e.Cause
}

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func NewRunner(cfg Config, client Client, registry Registry) *Runner {
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = DefaultReasoningEffort
	}
	if cfg.MaxToolCalls == 0 {
		cfg.MaxToolCalls = DefaultMaxToolCalls
	}
	if cfg.MemoryMaxInjectedBytes == 0 {
		cfg.MemoryMaxInjectedBytes = DefaultMemoryMaxBytes
	}
	if cfg.MemoryMaxInjectedItems == 0 {
		cfg.MemoryMaxInjectedItems = DefaultMemoryMaxItems
	}
	return &Runner{
		cfg:      cfg,
		client:   client,
		registry: registry,
	}
}

func (r *Runner) Run(ctx context.Context, req Request) (string, error) {
	if r.client == nil {
		return "", errors.New("agent client is nil")
	}
	if r.registry == nil {
		return "", errors.New("agent tool registry is nil")
	}
	if strings.TrimSpace(req.Message) == "" {
		return "", errors.New("agent message is empty")
	}

	profile := ProfileForChannel(req.Channel)
	instructions := r.runtimeInstructions(profile)
	tools, err := r.registry.Tools(ctx)
	if err != nil {
		return "", err
	}

	content, err := r.requestContent(req)
	if err != nil {
		return "", err
	}
	input := []InputItem{{
		Role:    roleUser,
		Content: content,
	}}
	toolCalls := 0
	progress := make([]toolProgress, 0, r.cfg.MaxToolCalls)

	for {
		resp, err := r.client.CreateResponse(ctx, ModelRequest{
			Model:           r.cfg.Model,
			ReasoningEffort: r.cfg.ReasoningEffort,
			Instructions:    instructions,
			Input:           append([]InputItem(nil), input...),
			Tools:           append([]ToolDefinition(nil), tools...),
		})
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", TimeoutSummaryError{
					Summary: profile.formatFinal(timeoutSummary(progress)),
					Cause:   err,
				}
			}
			return "", err
		}

		if strings.TrimSpace(resp.FinalText) != "" {
			return profile.formatFinal(resp.FinalText), nil
		}
		if len(resp.ToolCalls) == 0 {
			return profile.formatFinal("I could not produce a final answer."), nil
		}

		for _, call := range resp.ToolCalls {
			if toolCalls >= r.cfg.MaxToolCalls {
				return profile.formatFinal("I hit the tool-call limit before finishing."), nil
			}
			input = append(input, InputItem{
				Role:       roleToolCall,
				Content:    string(call.Arguments),
				ToolCallID: call.ID,
				ToolName:   call.Name,
			})
			output := r.executeTool(ctx, call)
			progress = append(progress, toolProgress{
				Name:      call.Name,
				Arguments: string(call.Arguments),
				Output:    output,
			})
			input = append(input, InputItem{
				Role:       roleToolOutput,
				Content:    output,
				ToolCallID: call.ID,
				ToolName:   call.Name,
			})
			toolCalls++
		}
	}
}

func (r *Runner) runtimeInstructions(profile Profile) string {
	instructions := strings.TrimSpace(profile.Instructions)
	raw, err := os.ReadFile(filepath.Join(r.workspacePath(), "AGENTS.md"))
	if err != nil {
		return instructions
	}
	rules := strings.TrimSpace(string(raw))
	if rules == "" {
		return instructions
	}
	if instructions == "" {
		return rules
	}
	return instructions + "\n\n" + rules
}

type toolProgress struct {
	Name      string
	Arguments string
	Output    string
}

func timeoutSummary(progress []toolProgress) string {
	var b strings.Builder
	b.WriteString("I hit the 5 minute response limit before I could finish a polished answer.")
	if len(progress) == 0 {
		b.WriteString(" I did not complete any tool calls before the timeout.")
		return b.String()
	}
	b.WriteString("\n\nWork completed before timeout:")
	limit := len(progress)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		p := progress[i]
		fmt.Fprintf(&b, "\n- Ran `%s`", strings.TrimSpace(p.Name))
		if args := trimOneLine(p.Arguments, 160); args != "" && args != "{}" {
			fmt.Fprintf(&b, " with `%s`", args)
		}
		if out := trimOneLine(p.Output, 500); out != "" {
			fmt.Fprintf(&b, ": %s", out)
		}
	}
	if len(progress) > limit {
		fmt.Fprintf(&b, "\n- Plus %d more tool call(s) before the timeout.", len(progress)-limit)
	}
	return b.String()
}

func trimOneLine(text string, max int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func (r *Runner) executeTool(ctx context.Context, call ToolCall) string {
	if strings.TrimSpace(call.Name) == "" {
		return "validation error: missing tool name"
	}

	output, err := r.registry.Execute(ctx, call)
	if err == nil {
		return output
	}

	var validationErr ValidationError
	if errors.As(err, &validationErr) {
		return "validation error: " + validationErr.Error()
	}
	var validationErrPtr *ValidationError
	if errors.As(err, &validationErrPtr) && validationErrPtr != nil {
		return "validation error: " + validationErrPtr.Error()
	}
	return "tool error: " + err.Error()
}

func (r *Runner) requestContent(req Request) (string, error) {
	var b strings.Builder
	channel := req.Channel
	if channel == "" {
		channel = ChannelMinecraft
	}
	fmt.Fprintf(&b, "channel: %s\n", channel)
	if strings.TrimSpace(req.User) != "" {
		fmt.Fprintf(&b, "user: %s\n", strings.TrimSpace(req.User))
	}
	if len(req.Context) != 0 {
		keys := make([]string, 0, len(req.Context))
		for k := range req.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\n", k, req.Context[k])
		}
	}
	if r.cfg.MemoryEnabled {
		memory, err := r.injectedMemory(req)
		if err != nil {
			return "", err
		}
		if memory != "" {
			b.WriteString(memory)
			b.WriteString("\n")
		}
	}
	b.WriteString("message:\n")
	b.WriteString(strings.TrimSpace(req.Message))
	return b.String(), nil
}

func (r *Runner) injectedMemory(req Request) (string, error) {
	store := greggpttools.NewMemoryStore(r.memoryPath(), r.cfg.MemoryDefaultTTL)
	channel := req.Channel
	if channel == "" {
		channel = ChannelMinecraft
	}
	scopes := []greggpttools.MemoryScope{greggpttools.MemoryScopeGlobal}
	if channel != "" {
		scopes = append(scopes, greggpttools.MemoryScopeChannel)
	}
	user := strings.TrimSpace(req.User)
	if user != "" {
		scopes = append(scopes, greggpttools.MemoryScopeUser)
	}
	items, err := store.List(greggpttools.MemorySelector{
		Scopes:  scopes,
		Channel: string(channel),
		User:    user,
		Limit:   r.cfg.MemoryMaxInjectedItems,
	})
	if err != nil {
		return "", err
	}
	return greggpttools.FormatMemoriesForInjection(items, r.cfg.MemoryMaxInjectedItems, r.cfg.MemoryMaxInjectedBytes), nil
}

func (r *Runner) memoryPath() string {
	path := strings.TrimSpace(r.cfg.MemoryPath)
	if path == "" {
		path = greggpttools.DefaultMemoryPath
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(r.workspacePath(), path)
}

func (r *Runner) workspacePath() string {
	workspace := r.cfg.Workspace
	if workspace == "" {
		workspace = DefaultWorkspace
	}
	return workspace
}
