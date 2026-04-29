package greggpttools

import (
	"context"
	"time"
)

type Group string

const (
	GroupGTNHQuery Group = "gtnh_query"
	GroupInventory Group = "inventory"
	GroupTask      Group = "task"
	GroupMinecraft Group = "minecraft"
	GroupMemory    Group = "memory"
)

type ToolDefinition struct {
	Name        string     `json:"name"`
	Group       Group      `json:"group"`
	Description string     `json:"description"`
	Parameters  JSONSchema `json:"parameters"`
	Timeout     string     `json:"timeout"`
}

type JSONSchema struct {
	Type                 string               `json:"type"`
	Properties           map[string]ParamSpec `json:"properties"`
	Required             []string             `json:"required,omitempty"`
	AdditionalProperties bool                 `json:"additionalProperties"`
}

type ParamSpec struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []any       `json:"enum,omitempty"`
	Items       *ParamSpec  `json:"items,omitempty"`
	Minimum     *int        `json:"minimum,omitempty"`
	Maximum     *int        `json:"maximum,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	MinLength   int         `json:"minLength,omitempty"`
	MinItems    int         `json:"minItems,omitempty"`
}

type Result struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timed_out"`
	Truncated bool   `json:"truncated"`
}

type Tool struct {
	definition ToolDefinition
	timeout    time.Duration
	buildArgv  func(Arguments) ([]string, error)
	execute    func(context.Context, Arguments) (Result, error)
}

func (t Tool) Definition() ToolDefinition {
	return t.definition
}
