package greggpttools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultWorkspace              = "/root/.greggpt/workspace"
	DefaultToolTimeout            = 20 * time.Second
	DefaultMaxOutputLength        = 12000
	DefaultMemoryPath             = "state/greggpt_memory.json"
	DefaultFailedInteractionsPath = "state/failed_interactions.jsonl"
	DefaultRecipeSQLPath          = "gtnh-data/index/greggpt_recipes.sqlite"
)

const (
	EnvWorkspace              = "GREGGPT_WORKSPACE"
	EnvToolTimeout            = "GREGGPT_TOOL_TIMEOUT_SECONDS"
	EnvMaxOutputBytes         = "GREGGPT_TOOL_MAX_OUTPUT_BYTES"
	EnvRecipeSQLPath          = "GREGGPT_RECIPE_SQLITE_PATH"
	EnvMemoryEnabled          = "GREGGPT_MEMORY_ENABLED"
	EnvMemoryPath             = "GREGGPT_MEMORY_PATH"
	EnvMemoryDefaultTTL       = "GREGGPT_MEMORY_DEFAULT_TTL_SECONDS"
	EnvFailedInteractionsPath = "GREGGPT_FAILED_INTERACTIONS_PATH"
)

type Config struct {
	Workspace              string
	ToolTimeout            time.Duration
	MaxOutputBytes         int
	RecipeSQLPath          string
	MemoryEnabled          bool
	MemoryPath             string
	MemoryDefaultTTL       time.Duration
	FailedInteractionsPath string
}

func DefaultConfig() Config {
	return Config{
		Workspace:              DefaultWorkspace,
		ToolTimeout:            DefaultToolTimeout,
		MaxOutputBytes:         DefaultMaxOutputLength,
		RecipeSQLPath:          DefaultRecipeSQLPath,
		MemoryPath:             DefaultMemoryPath,
		FailedInteractionsPath: DefaultFailedInteractionsPath,
	}
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()
	if workspace := strings.TrimSpace(os.Getenv(EnvWorkspace)); workspace != "" {
		cfg.Workspace = workspace
	}
	if timeout := positiveIntEnv(EnvToolTimeout); timeout > 0 {
		cfg.ToolTimeout = time.Duration(timeout) * time.Second
	}
	if maxOutput := positiveIntEnv(EnvMaxOutputBytes); maxOutput > 0 {
		cfg.MaxOutputBytes = maxOutput
	}
	if path := strings.TrimSpace(os.Getenv(EnvRecipeSQLPath)); path != "" {
		cfg.RecipeSQLPath = path
	}
	cfg.MemoryEnabled = boolEnv(EnvMemoryEnabled)
	if path := strings.TrimSpace(os.Getenv(EnvMemoryPath)); path != "" {
		cfg.MemoryPath = path
	}
	if ttl := positiveIntEnv(EnvMemoryDefaultTTL); ttl > 0 {
		cfg.MemoryDefaultTTL = time.Duration(ttl) * time.Second
	}
	if path := strings.TrimSpace(os.Getenv(EnvFailedInteractionsPath)); path != "" {
		cfg.FailedInteractionsPath = path
	}
	return cfg
}

func (c Config) resolvedRecipeSQLPath() string {
	path := strings.TrimSpace(c.RecipeSQLPath)
	if path == "" {
		path = DefaultRecipeSQLPath
	}
	if filepath.IsAbs(path) || path == ":memory:" {
		return path
	}
	workspace := c.Workspace
	if workspace == "" {
		workspace = DefaultWorkspace
	}
	return filepath.Join(workspace, path)
}

func (c Config) resolvedMemoryPath() string {
	path := strings.TrimSpace(c.MemoryPath)
	if path == "" {
		path = DefaultMemoryPath
	}
	if filepath.IsAbs(path) {
		return path
	}
	workspace := c.Workspace
	if workspace == "" {
		workspace = DefaultWorkspace
	}
	return filepath.Join(workspace, path)
}

func (c Config) resolvedFailedInteractionsPath() string {
	path := strings.TrimSpace(c.FailedInteractionsPath)
	if path == "" {
		path = DefaultFailedInteractionsPath
	}
	if filepath.IsAbs(path) {
		return path
	}
	workspace := c.Workspace
	if workspace == "" {
		workspace = DefaultWorkspace
	}
	return filepath.Join(workspace, path)
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
