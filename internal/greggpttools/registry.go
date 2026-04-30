package greggpttools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

var (
	scopeEnum        = []any{"players", "chests", "containers", "me", "both", "all"}
	refreshScopeEnum = []any{"players", "chests", "containers", "me", "block-inventories", "blocks", "all"}
	statusEnum       = []any{"todo", "doing", "paused", "done"}
	priorityEnum     = []any{"low", "med", "high"}
	listStatusEnum   = []any{"open", "done", "all"}
	dimEnum          = []any{-1, 0, 1}
	memoryScopeEnum  = []any{"global", "channel", "user"}
)

type Registry struct {
	cfg    Config
	memory *MemoryStore
	tools  map[string]Tool
	names  []string
}

func NewRegistry(cfg Config) (*Registry, error) {
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = DefaultToolTimeout
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = DefaultMaxOutputLength
	}

	r := &Registry{cfg: cfg, tools: map[string]Tool{}}
	if cfg.MemoryEnabled {
		r.memory = NewMemoryStore(cfg.resolvedMemoryPath(), cfg.MemoryDefaultTTL)
	}
	for _, tool := range buildTools(cfg.ToolTimeout, r.memory) {
		name := tool.definition.Name
		if _, exists := r.tools[name]; exists {
			return nil, fmt.Errorf("duplicate tool definition %q", name)
		}
		r.tools[name] = tool
		r.names = append(r.names, name)
	}
	sort.Strings(r.names)
	return r, nil
}

func MustRegistry(cfg Config) *Registry {
	r, err := NewRegistry(cfg)
	if err != nil {
		panic(err)
	}
	return r
}

func (r *Registry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.names))
	for _, name := range r.names {
		out = append(out, r.tools[name].definition)
	}
	return out
}

func (r *Registry) Definition(name string) (ToolDefinition, bool) {
	tool, ok := r.tools[name]
	if !ok {
		return ToolDefinition{}, false
	}
	return tool.definition, true
}

func (r *Registry) Validate(name string, raw json.RawMessage) error {
	tool, ok := r.tools[name]
	if !ok {
		return fmt.Errorf("unknown tool %q", name)
	}
	_, err := parseArguments(tool.definition.Parameters, raw)
	return err
}

func buildTools(defaultTimeout time.Duration, memory *MemoryStore) []Tool {
	short := minDuration(defaultTimeout, 10*time.Second)
	medium := minDuration(defaultTimeout, 20*time.Second)
	network := minDuration(defaultTimeout, 30*time.Second)

	tools := []Tool{
		tool("gtnh_find_item", GroupGTNHQuery, "Find GTNH items by text query.", medium, object(
			required("query", stringSpec("Item name or search text.")),
			optional("oredict", boolSpec("Search the ore dictionary cache.", false)),
		), func(a Arguments) ([]string, error) {
			argv := []string{"sh", "gtnh_find_item", stringArg(a, "query")}
			if boolArg(a, "oredict") {
				argv = append(argv, "--oredict")
			}
			return argv, nil
		}),
		tool("gtnh_item", GroupGTNHQuery, "Look up an exact GTNH item slug.", medium, object(
			required("slug", stringSpec("Exact item slug, for example 7437d11305.")),
		), func(a Arguments) ([]string, error) {
			return []string{"sh", "gtnh_item", stringArg(a, "slug")}, nil
		}),
		tool("gtnh_resolve_recipes", GroupGTNHQuery, "Resolve an item name and return matching recipes.", medium, object(
			required("item", stringSpec("Item name to resolve.")),
		), func(a Arguments) ([]string, error) {
			return []string{"sh", "gtnh_resolve_recipes", stringArg(a, "item")}, nil
		}),
		tool("gtnh_search_recipes", GroupGTNHQuery, "Search GTNH recipes by text.", medium, object(
			required("query", stringSpec("Recipe search text.")),
		), func(a Arguments) ([]string, error) {
			return []string{"sh", "gtnh_search_recipes", stringArg(a, "query")}, nil
		}),
		tool("gtnh_wiki_page", GroupGTNHQuery, "Fetch a GTNH wiki page summary.", network, object(
			required("title", stringSpec("Wiki page title.")),
		), func(a Arguments) ([]string, error) {
			return []string{"sh", "gtnh_wiki_page", stringArg(a, "title")}, nil
		}),

		tool("inventory_status", GroupInventory, "Show inventory index freshness and stats.", short, object(), func(Arguments) ([]string, error) {
			return []string{"sh", "gtnh_inventory", "status"}, nil
		}),
		tool("inventory_find", GroupInventory, "Find exact item locations in player, container, or ME storage.", medium, object(
			required("item", stringSpec("Exact item registry name, optionally with damage, for example minecraft:iron_ingot:0.")),
			optional("any_damage", boolSpec("Aggregate all damage values for this registry name.", false)),
			optional("player", stringSpec("Restrict player inventory lookup to this player name or UUID.")),
			optional("scope", enumStringSpec("Lookup scope.", scopeEnum, "all")),
			optional("limit", intSpec("Maximum locations to print.", 1, 100, 20)),
		), func(a Arguments) ([]string, error) {
			argv := []string{"sh", "gtnh_inventory", "find", "--item", stringArg(a, "item")}
			if boolArg(a, "any_damage") {
				argv = append(argv, "--any-damage")
			}
			if player := stringArg(a, "player"); player != "" {
				argv = append(argv, "--player", player)
			}
			argv = appendScopeAndLimit(argv, a, "all", 20)
			return argv, nil
		}),
		tool("inventory_find_item", GroupInventory, "Resolve a natural-language item query and find item locations in players, containers, or ME. Do not use for placed block coordinates; use inventory_find_block_name for questions like where is Super Chest I.", medium, object(
			required("query", stringSpec("Natural-language item name.")),
			optional("oredict", boolSpec("Resolve through the ore dictionary cache.", false)),
			optional("scope", enumStringSpec("Lookup scope.", scopeEnum, "all")),
			optional("limit", intSpec("Maximum locations to print.", 1, 100, 20)),
		), func(a Arguments) ([]string, error) {
			argv := []string{"sh", "gtnh_inventory", "find-item", "--query", stringArg(a, "query")}
			if boolArg(a, "oredict") {
				argv = append(argv, "--oredict")
			}
			argv = appendScopeAndLimit(argv, a, "all", 20)
			return argv, nil
		}),
		tool("inventory_player", GroupInventory, "Show a player's inventory.", medium, object(
			required("name", stringSpec("Player name.")),
			optional("all", boolSpec("Include nested inventory contents.", false)),
		), func(a Arguments) ([]string, error) {
			argv := []string{"sh", "gtnh_inventory", "player", "--name", stringArg(a, "name")}
			if boolArg(a, "all") {
				argv = append(argv, "--all")
			}
			return argv, nil
		}),
		tool("inventory_chest", GroupInventory, "Show a container at exact coordinates.", medium, object(
			required("x", intSpec("X coordinate.", -30000000, 30000000, nil)),
			required("y", intSpec("Y coordinate.", -2048, 4096, nil)),
			required("z", intSpec("Z coordinate.", -30000000, 30000000, nil)),
			optional("dim", enumIntSpec("Minecraft dimension.", dimEnum, 0)),
		), func(a Arguments) ([]string, error) {
			return []string{
				"sh", "gtnh_inventory", "chest",
				"--x", strconv.Itoa(intArg(a, "x", 0)),
				"--y", strconv.Itoa(intArg(a, "y", 0)),
				"--z", strconv.Itoa(intArg(a, "z", 0)),
				"--dim", strconv.Itoa(intArg(a, "dim", 0)),
			}, nil
		}),
		tool("inventory_find_block_name", GroupInventory, "Find indexed placed block locations by block display or registry name. Use this first for placed block coordinates and questions like where is the Super Chest or where is Super Chest I.", medium, object(
			required("block", stringSpec("Block display or registry name, for example Super Chest I or gregtech:gt.blockmachines.")),
			optional("limit", intSpec("Maximum locations to print.", 1, 100, 20)),
		), func(a Arguments) ([]string, error) {
			argv := []string{"sh", "gtnh_inventory", "find-block", "--block", stringArg(a, "block")}
			if limit := intArg(a, "limit", 0); limit > 0 {
				argv = append(argv, "--limit", strconv.Itoa(limit))
			}
			return argv, nil
		}),
		tool("inventory_find_block", GroupInventory, "Find indexed placed block locations by numeric block id/meta. Prefer inventory_find_block_name when the user names a block such as Super Chest I.", medium, object(
			required("id", intSpec("Numeric Minecraft block id.", 1, 65535, nil)),
			required("meta", intSpec("Block metadata value.", 0, 65535, nil)),
			optional("limit", intSpec("Maximum locations to print.", 1, 100, 20)),
		), func(a Arguments) ([]string, error) {
			argv := []string{
				"sh", "gtnh_inventory", "find-block",
				"--id", strconv.Itoa(intArg(a, "id", 0)),
				"--meta", strconv.Itoa(intArg(a, "meta", 0)),
			}
			if limit := intArg(a, "limit", 0); limit > 0 {
				argv = append(argv, "--limit", strconv.Itoa(limit))
			}
			return argv, nil
		}),
		tool("inventory_refresh", GroupInventory, "Request inventory index refresh.", short, object(
			optional("scope", enumStringSpec("Refresh scope.", refreshScopeEnum, "all")),
		), func(a Arguments) ([]string, error) {
			scope := stringArg(a, "scope")
			if scope == "" {
				scope = "all"
			}
			return []string{"sh", "gtnh_inventory", "refresh", "--" + scope}, nil
		}),

		tool("task_board", GroupTask, "Show the GTNH task board.", short, object(), func(Arguments) ([]string, error) {
			return []string{"sh", "gtnh_tasks", "board"}, nil
		}),
		tool("task_board_json", GroupTask, "Show the GTNH task board as JSON.", short, object(), func(Arguments) ([]string, error) {
			return []string{"sh", "gtnh_tasks", "board-json"}, nil
		}),
		tool("task_in_progress_json", GroupTask, "Show in-progress GTNH tasks as JSON.", short, object(), func(Arguments) ([]string, error) {
			return []string{"sh", "gtnh_tasks", "in-progress-json"}, nil
		}),
		tool("task_list", GroupTask, "List GTNH tasks.", short, object(
			optional("status", enumStringSpec("Task list filter.", listStatusEnum, "open")),
			optional("area", stringSpec("Optional area filter.")),
		), func(a Arguments) ([]string, error) {
			status := stringArg(a, "status")
			if status == "" {
				status = "open"
			}
			argv := []string{"sh", "gtnh_tasks", "list", "--" + status}
			if area := stringArg(a, "area"); area != "" {
				argv = append(argv, "--area", area)
			}
			return argv, nil
		}),
		tool("task_add", GroupTask, "Add a GTNH task.", short, object(
			required("title", stringSpec("Task title.")),
			optional("priority", enumStringSpec("Task priority.", priorityEnum, "med")),
			optional("area", stringSpec("Task area.")),
			optional("status", enumStringSpec("Kanban status.", statusEnum, "todo")),
			optional("owners", stringArraySpec("Task owner IDs.")),
			optional("paused_reason", stringSpec("Reason when adding a paused task.")),
			optional("description", stringSpec("Living task description.")),
		), func(a Arguments) ([]string, error) {
			argv := []string{"sh", "gtnh_tasks", "add", stringArg(a, "title")}
			if priority := stringArg(a, "priority"); priority != "" {
				argv = append(argv, "--priority", priority)
			}
			if area := stringArg(a, "area"); area != "" {
				argv = append(argv, "--area", area)
			}
			if status := stringArg(a, "status"); status != "" {
				argv = append(argv, "--status", status)
			}
			for _, owner := range stringSliceArg(a, "owners") {
				argv = append(argv, "--owner", owner)
			}
			if reason := stringArg(a, "paused_reason"); reason != "" {
				argv = append(argv, "--paused-reason", reason)
			}
			if description := stringArg(a, "description"); description != "" {
				argv = append(argv, "--description", description)
			}
			return argv, nil
		}),
		tool("task_move", GroupTask, "Move a GTNH task between kanban statuses.", short, object(
			required("id", intSpec("Task ID.", 1, 999999, nil)),
			required("status", enumStringSpec("Kanban status.", statusEnum, nil)),
			optional("owners", stringArraySpec("Owner IDs to set when moving to doing.")),
			optional("reason", stringSpec("Pause reason.")),
		), func(a Arguments) ([]string, error) {
			argv := []string{"sh", "gtnh_tasks", "move", strconv.Itoa(intArg(a, "id", 0)), "--status", stringArg(a, "status")}
			for _, owner := range stringSliceArg(a, "owners") {
				argv = append(argv, "--owner", owner)
			}
			if reason := stringArg(a, "reason"); reason != "" {
				argv = append(argv, "--reason", reason)
			}
			return argv, nil
		}),
		taskOwnersTool("task_assign", "assign", "Assign owners to a GTNH task.", short),
		taskOwnersTool("task_unassign", "unassign", "Remove owners from a GTNH task.", short),
		taskOwnersTool("task_reassign", "reassign", "Replace owners on an in-progress GTNH task.", short),
		taskIDTextTool("task_pause", "pause", "Pause a GTNH task with a reason.", "reason", "Pause reason.", short),
		taskIDTool("task_unpause", "unpause", "Unpause a GTNH task.", short),
		taskIDTextTool("task_describe", "describe", "Set a GTNH task description.", "description", "Task description.", short),
		taskIDTextTool("task_status_update", "status-update", "Append a GTNH task status update.", "text", "Status update text.", short),
		taskIDTool("task_status_history", "status-history", "Show GTNH task status history.", short),
		taskIDTool("task_done", "done", "Mark a GTNH task done.", short),
		taskIDTool("task_reopen", "reopen", "Reopen a GTNH task.", short),
		taskIDTool("task_show", "show", "Show GTNH task detail.", short),
		tool("task_summary", GroupTask, "Show GTNH task summary counts.", short, object(), func(Arguments) ([]string, error) {
			return []string{"sh", "gtnh_tasks", "summary"}, nil
		}),

		tool("mc_online", GroupMinecraft, "List online Minecraft players through the bridge.", network, object(
			optional("lines", intSpec("Console lines to scan.", 1, 5000, 500)),
		), func(a Arguments) ([]string, error) {
			return []string{"sh", "mc_online", strconv.Itoa(intArg(a, "lines", 500))}, nil
		}),
		tool("mc_poll", GroupMinecraft, "Poll Minecraft chat events through the bridge.", network, object(
			optional("lines", intSpec("Console lines to scan.", 1, 5000, 500)),
		), func(a Arguments) ([]string, error) {
			return []string{"sh", "mc_poll", strconv.Itoa(intArg(a, "lines", 500))}, nil
		}),
		tool("mc_say", GroupMinecraft, "Send a Minecraft chat message through the bridge.", network, object(
			required("text", stringSpec("Chat text to send.")),
		), func(a Arguments) ([]string, error) {
			return []string{"sh", "mc_say", stringArg(a, "text")}, nil
		}),
	}
	if memory != nil {
		tools = append(tools, memoryTools(memory, short)...)
	}
	return tools
}

func tool(name string, group Group, description string, timeout time.Duration, schema JSONSchema, build func(Arguments) ([]string, error)) Tool {
	return Tool{
		definition: ToolDefinition{
			Name:        name,
			Group:       group,
			Description: description,
			Parameters:  schema,
			Timeout:     timeout.String(),
		},
		timeout:   timeout,
		buildArgv: build,
	}
}

func nativeTool(name string, group Group, description string, timeout time.Duration, schema JSONSchema, execute func(context.Context, Arguments) (Result, error)) Tool {
	return Tool{
		definition: ToolDefinition{
			Name:        name,
			Group:       group,
			Description: description,
			Parameters:  schema,
			Timeout:     timeout.String(),
		},
		timeout: timeout,
		execute: execute,
	}
}

type prop struct {
	name     string
	spec     ParamSpec
	required bool
}

func object(props ...prop) JSONSchema {
	schema := JSONSchema{
		Type:                 "object",
		Properties:           map[string]ParamSpec{},
		AdditionalProperties: false,
	}
	for _, p := range props {
		schema.Properties[p.name] = p.spec
		if p.required {
			schema.Required = append(schema.Required, p.name)
		}
	}
	return schema
}

func required(name string, spec ParamSpec) prop {
	return prop{name: name, spec: spec, required: true}
}

func optional(name string, spec ParamSpec) prop {
	return prop{name: name, spec: spec}
}

func stringSpec(description string) ParamSpec {
	return ParamSpec{Type: "string", Description: description, MinLength: 1}
}

func boolSpec(description string, defaultValue bool) ParamSpec {
	return ParamSpec{Type: "boolean", Description: description, Default: defaultValue}
}

func intSpec(description string, min int, max int, defaultValue any) ParamSpec {
	return ParamSpec{Type: "integer", Description: description, Minimum: intPtr(min), Maximum: intPtr(max), Default: defaultValue}
}

func enumStringSpec(description string, values []any, defaultValue any) ParamSpec {
	return ParamSpec{Type: "string", Description: description, Enum: values, Default: defaultValue}
}

func enumIntSpec(description string, values []any, defaultValue any) ParamSpec {
	return ParamSpec{Type: "integer", Description: description, Enum: values, Default: defaultValue}
}

func stringArraySpec(description string) ParamSpec {
	return ParamSpec{Type: "array", Description: description, Items: &ParamSpec{Type: "string", MinLength: 1}, MinItems: 1}
}

func appendScopeAndLimit(argv []string, args Arguments, defaultScope string, defaultLimit int) []string {
	scope := stringArg(args, "scope")
	if scope == "" {
		scope = defaultScope
	}
	argv = append(argv, "--scope", scope)
	return append(argv, "--limit", strconv.Itoa(intArg(args, "limit", defaultLimit)))
}

func taskIDTool(name, command, description string, timeout time.Duration) Tool {
	return tool(name, GroupTask, description, timeout, object(
		required("id", intSpec("Task ID.", 1, 999999, nil)),
	), func(a Arguments) ([]string, error) {
		return []string{"sh", "gtnh_tasks", command, strconv.Itoa(intArg(a, "id", 0))}, nil
	})
}

func taskIDTextTool(name, command, description, argName, argDescription string, timeout time.Duration) Tool {
	return tool(name, GroupTask, description, timeout, object(
		required("id", intSpec("Task ID.", 1, 999999, nil)),
		required(argName, stringSpec(argDescription)),
	), func(a Arguments) ([]string, error) {
		return []string{"sh", "gtnh_tasks", command, strconv.Itoa(intArg(a, "id", 0)), stringArg(a, argName)}, nil
	})
}

func taskOwnersTool(name, command, description string, timeout time.Duration) Tool {
	return tool(name, GroupTask, description, timeout, object(
		required("id", intSpec("Task ID.", 1, 999999, nil)),
		required("owners", stringArraySpec("Owner IDs.")),
	), func(a Arguments) ([]string, error) {
		argv := []string{"sh", "gtnh_tasks", command, strconv.Itoa(intArg(a, "id", 0))}
		argv = append(argv, stringSliceArg(a, "owners")...)
		return argv, nil
	})
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
