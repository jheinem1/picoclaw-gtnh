package agent

import "time"

const (
	DefaultModel           = "gpt-5.5"
	DefaultReasoningEffort = "low"
	DefaultWorkspace       = "/root/.greggpt/workspace"
	DefaultAuthFile        = "/root/.greggpt/auth.json"
	DefaultMaxToolCalls    = 100
	DefaultMemoryMaxBytes  = 3000
	DefaultMemoryMaxItems  = 8
	DefaultHistoryPath     = "state/greggpt_history.sqlite"
	DefaultHistoryMessages = 20
	DefaultRecallMaxBytes  = 5000
	DefaultRecallMaxItems  = 12
	DefaultTimeoutSummary  = 25 * time.Second
)

const (
	EnvModel                  = "GREGGPT_MODEL"
	EnvReasoningEffort        = "GREGGPT_REASONING_EFFORT"
	EnvWorkspace              = "GREGGPT_WORKSPACE"
	EnvAuthFile               = "GREGGPT_AUTH_FILE"
	EnvAgentTimeout           = "GREGGPT_AGENT_TIMEOUT_SECONDS"
	EnvTimeoutSummary         = "GREGGPT_TIMEOUT_SUMMARY_SECONDS"
	EnvMaxToolCalls           = "GREGGPT_MAX_TOOL_CALLS"
	EnvMemoryEnabled          = "GREGGPT_MEMORY_ENABLED"
	EnvMemoryPath             = "GREGGPT_MEMORY_PATH"
	EnvMemoryMaxInjectedBytes = "GREGGPT_MEMORY_MAX_INJECTED_BYTES"
	EnvMemoryMaxInjectedItems = "GREGGPT_MEMORY_MAX_INJECTED_ITEMS"
	EnvMemoryDefaultTTL       = "GREGGPT_MEMORY_DEFAULT_TTL_SECONDS"
	EnvHistoryEnabled         = "GREGGPT_HISTORY_ENABLED"
	EnvHistoryPath            = "GREGGPT_HISTORY_PATH"
	EnvHistoryMaxMessages     = "GREGGPT_HISTORY_MAX_MESSAGES"
	EnvRecallMaxItems         = "GREGGPT_RECALLED_CONTEXT_MAX_ITEMS"
	EnvRecallMaxBytes         = "GREGGPT_RECALLED_CONTEXT_MAX_BYTES"
)

type Config struct {
	Model                  string
	ReasoningEffort        string
	Workspace              string
	AuthFile               string
	Timeout                time.Duration
	TimeoutSummary         time.Duration
	MaxToolCalls           int
	MemoryEnabled          bool
	MemoryPath             string
	MemoryMaxInjectedBytes int
	MemoryMaxInjectedItems int
	MemoryDefaultTTL       time.Duration
	HistoryEnabled         bool
	HistoryPath            string
	HistoryMaxMessages     int
	RecallMaxItems         int
	RecallMaxBytes         int
}

type Channel string

const (
	ChannelMinecraft Channel = "minecraft"
	ChannelDiscord   Channel = "discord"
)
