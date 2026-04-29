package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"greggpt-gtnh/internal/greggpttools"
)

type ToolRegistry struct {
	registry *greggpttools.Registry
}

func NewDefaultRunner(cfg Config) (*Runner, error) {
	if cfg.Workspace == "" {
		cfg.Workspace = DefaultWorkspace
	}
	if cfg.AuthFile == "" {
		cfg.AuthFile = DefaultAuthFile
	}
	toolCfg := greggpttools.ConfigFromEnv()
	toolCfg.Workspace = cfg.Workspace
	tools, err := NewToolRegistry(toolCfg)
	if err != nil {
		return nil, err
	}
	client := NewCodexAgentClient(CodexClientOptions{
		AuthFile: cfg.AuthFile,
	})
	return NewRunner(cfg, client, tools), nil
}

func NewToolRegistry(cfg greggpttools.Config) (*ToolRegistry, error) {
	registry, err := greggpttools.NewRegistry(cfg)
	if err != nil {
		return nil, err
	}
	return &ToolRegistry{registry: registry}, nil
}

func (r *ToolRegistry) Tools(context.Context) ([]ToolDefinition, error) {
	if r == nil || r.registry == nil {
		return nil, fmt.Errorf("tool registry is nil")
	}
	defs := r.registry.Definitions()
	out := make([]ToolDefinition, 0, len(defs))
	for _, def := range defs {
		raw, err := json.Marshal(def.Parameters)
		if err != nil {
			return nil, fmt.Errorf("marshal parameters for tool %q: %w", def.Name, err)
		}
		out = append(out, ToolDefinition{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  json.RawMessage(raw),
		})
	}
	return out, nil
}

func (r *ToolRegistry) Execute(ctx context.Context, call ToolCall) (string, error) {
	if r == nil || r.registry == nil {
		return "", fmt.Errorf("tool registry is nil")
	}
	result, err := r.registry.Execute(ctx, call.Name, call.Arguments)
	if err != nil {
		return "", ValidationError{Message: err.Error()}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal tool result: %w", err)
	}
	return string(raw), nil
}
