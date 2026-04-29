package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	for {
		resp, err := r.client.CreateResponse(ctx, ModelRequest{
			Model:        r.cfg.Model,
			Instructions: profile.Instructions,
			Input:        append([]InputItem(nil), input...),
			Tools:        append([]ToolDefinition(nil), tools...),
		})
		if err != nil {
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
	workspace := r.cfg.Workspace
	if workspace == "" {
		workspace = DefaultWorkspace
	}
	return filepath.Join(workspace, path)
}
