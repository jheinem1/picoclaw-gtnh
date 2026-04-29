package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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
	cfg = applyMemoryEnv(cfg)
	toolCfg := greggpttools.ConfigFromEnv()
	toolCfg.Workspace = cfg.Workspace
	toolCfg.MemoryEnabled = cfg.MemoryEnabled
	toolCfg.MemoryPath = cfg.MemoryPath
	toolCfg.MemoryDefaultTTL = cfg.MemoryDefaultTTL
	tools, err := NewToolRegistry(toolCfg)
	if err != nil {
		return nil, err
	}
	client := NewCodexAgentClient(CodexClientOptions{
		AuthFile: cfg.AuthFile,
	})
	return NewRunner(cfg, client, tools), nil
}

func applyMemoryEnv(cfg Config) Config {
	if boolEnv(EnvMemoryEnabled) {
		cfg.MemoryEnabled = true
	}
	if path := strings.TrimSpace(os.Getenv(EnvMemoryPath)); path != "" {
		cfg.MemoryPath = path
	}
	if n := positiveIntEnv(EnvMemoryMaxInjectedBytes); n > 0 {
		cfg.MemoryMaxInjectedBytes = n
	}
	if n := positiveIntEnv(EnvMemoryMaxInjectedItems); n > 0 {
		cfg.MemoryMaxInjectedItems = n
	}
	if n := positiveIntEnv(EnvMemoryDefaultTTL); n > 0 {
		cfg.MemoryDefaultTTL = time.Duration(n) * time.Second
	}
	return cfg
}

func positiveIntEnv(key string) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func boolEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
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
