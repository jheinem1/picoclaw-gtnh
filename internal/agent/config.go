package agent

import "time"

const (
	DefaultModel        = "gpt-5.3-codex"
	DefaultWorkspace    = "/root/.greggpt/workspace"
	DefaultAuthFile     = "/root/.greggpt/auth.json"
	DefaultMaxToolCalls = 8
)

const (
	EnvModel        = "GREGGPT_MODEL"
	EnvWorkspace    = "GREGGPT_WORKSPACE"
	EnvAuthFile     = "GREGGPT_AUTH_FILE"
	EnvAgentTimeout = "GREGGPT_AGENT_TIMEOUT_SECONDS"
	EnvMaxToolCalls = "GREGGPT_MAX_TOOL_CALLS"
)

type Config struct {
	Model        string
	Workspace    string
	AuthFile     string
	Timeout      time.Duration
	MaxToolCalls int
}

type Channel string

const (
	ChannelMinecraft Channel = "minecraft"
	ChannelDiscord   Channel = "discord"
)
