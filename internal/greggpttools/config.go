package greggpttools

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultWorkspace       = "/root/.greggpt/workspace"
	DefaultToolTimeout     = 20 * time.Second
	DefaultMaxOutputLength = 12000
)

const (
	EnvWorkspace      = "GREGGPT_WORKSPACE"
	EnvToolTimeout    = "GREGGPT_TOOL_TIMEOUT_SECONDS"
	EnvMaxOutputBytes = "GREGGPT_TOOL_MAX_OUTPUT_BYTES"
)

type Config struct {
	Workspace      string
	ToolTimeout    time.Duration
	MaxOutputBytes int
}

func DefaultConfig() Config {
	return Config{
		Workspace:      DefaultWorkspace,
		ToolTimeout:    DefaultToolTimeout,
		MaxOutputBytes: DefaultMaxOutputLength,
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
