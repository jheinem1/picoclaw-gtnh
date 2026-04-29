package agent

import "time"

const (
	DefaultModel           = "gpt-5.3-codex-spark"
	DefaultReasoningEffort = "medium"
	DefaultWorkspace       = "/root/.greggpt/workspace"
	DefaultAuthFile        = "/root/.greggpt/auth.json"
	DefaultMaxToolCalls    = 8
	DefaultMemoryMaxBytes  = 3000
	DefaultMemoryMaxItems  = 8
)

const (
	EnvModel                  = "GREGGPT_MODEL"
	EnvReasoningEffort        = "GREGGPT_REASONING_EFFORT"
	EnvWorkspace              = "GREGGPT_WORKSPACE"
	EnvAuthFile               = "GREGGPT_AUTH_FILE"
	EnvAgentTimeout           = "GREGGPT_AGENT_TIMEOUT_SECONDS"
	EnvMaxToolCalls           = "GREGGPT_MAX_TOOL_CALLS"
	EnvMemoryEnabled          = "GREGGPT_MEMORY_ENABLED"
	EnvMemoryPath             = "GREGGPT_MEMORY_PATH"
	EnvMemoryMaxInjectedBytes = "GREGGPT_MEMORY_MAX_INJECTED_BYTES"
	EnvMemoryMaxInjectedItems = "GREGGPT_MEMORY_MAX_INJECTED_ITEMS"
	EnvMemoryDefaultTTL       = "GREGGPT_MEMORY_DEFAULT_TTL_SECONDS"
)

type Config struct {
	Model                  string
	ReasoningEffort        string
	Workspace              string
	AuthFile               string
	Timeout                time.Duration
	MaxToolCalls           int
	MemoryEnabled          bool
	MemoryPath             string
	MemoryMaxInjectedBytes int
	MemoryMaxInjectedItems int
	MemoryDefaultTTL       time.Duration
}

type Channel string

const (
	ChannelMinecraft Channel = "minecraft"
	ChannelDiscord   Channel = "discord"
)
