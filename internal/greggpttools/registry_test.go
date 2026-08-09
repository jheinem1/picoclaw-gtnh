package greggpttools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestToolDefinitionsAreStrictObjects(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	definitions := registry.Definitions()
	if len(definitions) == 0 {
		t.Fatal("expected tool definitions")
	}

	seen := map[string]bool{}
	for _, def := range definitions {
		if def.Name == "" {
			t.Fatalf("definition has empty name: %+v", def)
		}
		if seen[def.Name] {
			t.Fatalf("duplicate definition %q", def.Name)
		}
		seen[def.Name] = true
		if def.Description == "" {
			t.Fatalf("%s: missing description", def.Name)
		}
		if def.Group == "" {
			t.Fatalf("%s: missing group", def.Name)
		}
		if def.Parameters.Type != "object" {
			t.Fatalf("%s: parameters type = %q, want object", def.Name, def.Parameters.Type)
		}
		if def.Parameters.AdditionalProperties {
			t.Fatalf("%s: expected additionalProperties=false", def.Name)
		}
		raw, err := json.Marshal(def.Parameters)
		if err != nil {
			t.Fatalf("%s: marshal parameters: %v", def.Name, err)
		}
		if !strings.Contains(string(raw), `"properties"`) {
			t.Fatalf("%s: serialized parameters are missing properties: %s", def.Name, raw)
		}
		for _, required := range def.Parameters.Required {
			if _, ok := def.Parameters.Properties[required]; !ok {
				t.Fatalf("%s: required argument %q is not in properties", def.Name, required)
			}
		}
	}
}

func TestArgvGenerationForEveryTool(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	tests := []struct {
		name string
		args string
		want []string
	}{
		{"gtnh_wiki_page", `{"title":"Steam Machines"}`, []string{"sh", "gtnh_wiki_page", "Steam Machines"}},
		{"inventory_status", `{}`, []string{"sh", "gtnh_inventory", "status"}},
		{"inventory_find", `{"item":"gregtech:gt.metaitem.01","any_damage":true,"player":"Snow","dim":183,"scope":"players","limit":5}`, []string{"sh", "gtnh_inventory", "find", "--item", "gregtech:gt.metaitem.01", "--any-damage", "--player", "Snow", "--dim", "183", "--scope", "players", "--limit", "5"}},
		{"inventory_totals", `{"items":["gregtech:gt.metaitem.01:2045","IC2:itemCellEmpty:0"],"dim":183,"scope":"all"}`, []string{"sh", "gtnh_inventory", "totals", "--item", "gregtech:gt.metaitem.01:2045", "--item", "IC2:itemCellEmpty:0", "--dim", "183", "--scope", "all"}},
		{"inventory_find_item", `{"query":"steel ingot","player":"Snow","dim":183,"scope":"me","limit":7}`, []string{"sh", "gtnh_inventory", "find-item", "--query", "steel ingot", "--player", "Snow", "--dim", "183", "--scope", "me", "--limit", "7"}},
		{"inventory_player", `{"name":"Snow","all":true}`, []string{"sh", "gtnh_inventory", "player", "--name", "Snow", "--all"}},
		{"inventory_chest", `{"x":1,"y":64,"z":-2,"dim":-1}`, []string{"sh", "gtnh_inventory", "chest", "--x", "1", "--y", "64", "--z", "-2", "--dim", "-1"}},
		{"inventory_find_block_name", `{"block":"Super Chest I","dim":183,"limit":5}`, []string{"sh", "gtnh_inventory", "find-block", "--block", "Super Chest I", "--dim", "183", "--limit", "5"}},
		{"inventory_find_block", `{"id":300,"meta":5,"dim":183,"limit":9}`, []string{"sh", "gtnh_inventory", "find-block", "--id", "300", "--meta", "5", "--dim", "183", "--limit", "9"}},
		{"inventory_refresh", `{"scope":"chests"}`, []string{"sh", "gtnh_inventory", "refresh", "--chests"}},
		{"quest_status", `{}`, []string{"sh", "gtnh_quests", "status"}},
		{"quest_open_json", `{"limit":10}`, []string{"sh", "gtnh_quests", "open-json", "--limit", "10"}},
		{"quest_completed_json", `{"limit":10}`, []string{"sh", "gtnh_quests", "completed-json", "--limit", "10"}},
		{"quest_show", `{"id":"42"}`, []string{"sh", "gtnh_quests", "show", "42"}},
		{"quest_refresh", `{}`, []string{"sh", "gtnh_quests", "refresh"}},
		{"quest_explain", `{"id":"42","user":"Snow","message":"EV only"}`, []string{"sh", "gtnh_next_action", "explain", "--id", "42", "--user", "Snow", "--message", "EV only"}},
		{"next_action_recommendation", `{"user":"Snow","message":"what do I need to do"}`, []string{"sh", "gtnh_next_action", "recommend", "--user", "Snow", "--message", "what do I need to do"}},
		{"next_action_plan", `{"user":"Snow","message":"EV plan","limit":4}`, []string{"sh", "gtnh_next_action", "plan", "--user", "Snow", "--message", "EV plan", "--limit", "4"}},
		{"task_board", `{}`, []string{"sh", "gtnh_tasks", "board-code"}},
		{"task_board_json", `{}`, []string{"sh", "gtnh_tasks", "board-json"}},
		{"task_in_progress_json", `{}`, []string{"sh", "gtnh_tasks", "in-progress-json"}},
		{"task_list", `{"status":"all","area":"power"}`, []string{"sh", "gtnh_tasks", "list", "--all", "--area", "power"}},
		{"task_add", `{"title":"Build MV EBF line","priority":"high","area":"steel","status":"paused","owners":["Snow","Alice"],"paused_reason":"blocked","description":"Need TNT"}`, []string{"sh", "gtnh_tasks", "add", "Build MV EBF line", "--priority", "high", "--area", "steel", "--status", "paused", "--owner", "Snow", "--owner", "Alice", "--paused-reason", "blocked", "--description", "Need TNT"}},
		{"task_move", `{"id":3,"status":"doing","owners":["Snow"],"reason":"ready"}`, []string{"sh", "gtnh_tasks", "move", "3", "--status", "doing", "--owner", "Snow", "--reason", "ready"}},
		{"task_assign", `{"id":3,"owners":["Snow","Alice"]}`, []string{"sh", "gtnh_tasks", "assign", "3", "Snow", "Alice"}},
		{"task_unassign", `{"id":3,"owners":["Alice"]}`, []string{"sh", "gtnh_tasks", "unassign", "3", "Alice"}},
		{"task_reassign", `{"id":3,"owners":["Snow"]}`, []string{"sh", "gtnh_tasks", "reassign", "3", "Snow"}},
		{"task_pause", `{"id":3,"reason":"waiting on TNT"}`, []string{"sh", "gtnh_tasks", "pause", "3", "waiting on TNT"}},
		{"task_unpause", `{"id":3}`, []string{"sh", "gtnh_tasks", "unpause", "3"}},
		{"task_describe", `{"id":3,"description":"Need coils"}`, []string{"sh", "gtnh_tasks", "describe", "3", "Need coils"}},
		{"task_status_update", `{"id":3,"text":"Built heaters"}`, []string{"sh", "gtnh_tasks", "status-update", "3", "Built heaters"}},
		{"task_status_history", `{"id":3}`, []string{"sh", "gtnh_tasks", "status-history", "3"}},
		{"task_done", `{"id":3}`, []string{"sh", "gtnh_tasks", "done", "3"}},
		{"task_reopen", `{"id":3}`, []string{"sh", "gtnh_tasks", "reopen", "3"}},
		{"task_show", `{"id":3}`, []string{"sh", "gtnh_tasks", "show", "3"}},
		{"task_summary", `{}`, []string{"sh", "gtnh_tasks", "summary"}},
		{"mc_online", `{"lines":100}`, []string{"sh", "mc_online", "100"}},
		{"mc_poll", `{"lines":50}`, []string{"sh", "mc_poll", "50"}},
		{"mc_say", `{"text":"hello base"}`, []string{"sh", "mc_say", "hello base"}},
	}

	if len(tests) != countArgvTools(registry) {
		t.Fatalf("test table covers %d argv tools, registry has %d", len(tests), countArgvTools(registry))
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := registry.argvForTest(tt.name, json.RawMessage(tt.args))
			if err != nil {
				t.Fatalf("argvForTest returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("argv mismatch\ngot:  %#v\nwant: %#v", got, tt.want)
			}
			if got[0] == "sh" && len(got) > 1 && got[1] == "-c" {
				t.Fatalf("tool used shell command string: %#v", got)
			}
		})
	}
}

func TestQuestListLimitIsRaisedAndClamped(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	for _, toolName := range []string{"quest_open_json", "quest_completed_json"} {
		t.Run(toolName, func(t *testing.T) {
			def, ok := registry.Definition(toolName)
			if !ok {
				t.Fatalf("definition %q not found", toolName)
			}
			limit := def.Parameters.Properties["limit"]
			if limit.Maximum == nil || *limit.Maximum != 500 {
				t.Fatalf("limit maximum = %v, want 500", limit.Maximum)
			}
			got, err := registry.argvForTest(toolName, json.RawMessage(`{"limit":300}`))
			if err != nil {
				t.Fatalf("argvForTest with raised limit returned error: %v", err)
			}
			if got[len(got)-1] != "300" {
				t.Fatalf("raised-limit argv = %#v, want final limit 300", got)
			}
			if err := registry.Validate(toolName, json.RawMessage(`{"limit":501}`)); err != nil {
				t.Fatalf("oversized limit should be clamped, got validation error: %v", err)
			}
			got, err = registry.argvForTest(toolName, json.RawMessage(`{"limit":501}`))
			if err != nil {
				t.Fatalf("argvForTest returned error: %v", err)
			}
			if got[len(got)-1] != "500" {
				t.Fatalf("clamped argv = %#v, want final limit 500", got)
			}
			if err := registry.Validate(toolName, json.RawMessage(`{"limit":0}`)); err == nil || !strings.Contains(err.Error(), "must be >= 1") {
				t.Fatalf("lower-bound validation error = %v, want >= 1 guard", err)
			}
		})
	}
}

func TestRecipeToolRegistrySurface(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	var recipeTools []string
	for _, def := range registry.Definitions() {
		if strings.Contains(def.Name, "recipe") {
			recipeTools = append(recipeTools, def.Name)
		}
	}
	if !reflect.DeepEqual(recipeTools, []string{"recipe_sql"}) {
		t.Fatalf("recipe tool surface = %#v, want only recipe_sql", recipeTools)
	}
	for _, name := range []string{"gtnh_resolve_recipes", "gtnh_search_recipes", "gtnh_find_item", "gtnh_item"} {
		if _, ok := registry.Definition(name); ok {
			t.Fatalf("%s should not be registered", name)
		}
		if err := registry.Validate(name, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "unknown tool") {
			t.Fatalf("Validate(%s) error = %v, want unknown tool", name, err)
		}
	}
}

func TestNextActionToolsUseBoundedDeterministicTimeout(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	for _, name := range []string{"next_action_recommendation", "next_action_plan", "quest_explain"} {
		def, ok := registry.Definition(name)
		if !ok {
			t.Fatalf("%s definition not found", name)
		}
		if def.Timeout != "20s" {
			t.Fatalf("%s timeout = %q, want 20s", name, def.Timeout)
		}
	}
}

func TestArgumentValidation(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	tests := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"missing required", "inventory_find_item", `{}`, `missing required argument "query"`},
		{"unknown argument", "inventory_find_item", `{"query":"steel","cmd":"rm -rf /"}`, `unknown argument "cmd"`},
		{"removed oredict option", "inventory_find_item", `{"query":"steel","oredict":true}`, `unknown argument "oredict"`},
		{"scope enum", "inventory_find", `{"item":"minecraft:stone","scope":"everywhere"}`, `argument "scope" must be one of`},
		{"status enum", "task_move", `{"id":1,"status":"blocked"}`, `argument "status" must be one of`},
		{"dimension range", "inventory_chest", `{"x":0,"y":64,"z":0,"dim":2147483648}`, `argument "dim" must be <= 2147483647`},
		{"integer range", "mc_poll", `{"lines":5001}`, `argument "lines" must be <= 5000`},
		{"array item", "task_assign", `{"id":1,"owners":["Snow",""]}`, `argument "owners[1]" must not be empty`},
		{"type mismatch", "task_done", `{"id":"3"}`, `argument "id" must be an integer`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.Validate(tt.tool, json.RawMessage(tt.args))
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestCorrectedToolSchemaRanges(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	if err := registry.Validate("inventory_chest", json.RawMessage(`{"x":1,"y":64,"z":2,"dim":183}`)); err != nil {
		t.Fatalf("custom dimension should be accepted: %v", err)
	}
	if err := registry.Validate("next_action_plan", json.RawMessage(`{"limit":20}`)); err != nil {
		t.Fatalf("planner limit 20 should be accepted: %v", err)
	}
	if err := registry.Validate("inventory_find", json.RawMessage(`{"item":"minecraft:stone","scope":"both"}`)); err != nil {
		t.Fatalf("supported legacy scope alias should be accepted: %v", err)
	}
	if err := registry.Validate("next_action_plan", json.RawMessage(`{"channel":"discord"}`)); err == nil || !strings.Contains(err.Error(), `unknown argument "channel"`) {
		t.Fatalf("removed channel argument error = %v", err)
	}
}

func TestExecuteUsesConfiguredWorkspaceAndOutputLimit(t *testing.T) {
	workspace := t.TempDir()
	expectedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("resolve workspace symlinks: %v", err)
	}
	writeScript(t, workspace, "gtnh_tasks", "printf '%s\\n' \"$PWD\"; printf 'abcdef'")
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	cfg.MaxOutputBytes = len(expectedWorkspace) + 2
	registry := testRegistry(t, cfg)

	result, err := registry.Execute(context.Background(), "task_summary", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("Execute result not OK: %+v", result)
	}
	if !strings.HasPrefix(result.Stdout, expectedWorkspace+"\n") {
		t.Fatalf("stdout = %q, want prefix %q", result.Stdout, expectedWorkspace+"\n")
	}
	if len(result.Stdout)+len(result.Stderr) > cfg.MaxOutputBytes {
		t.Fatalf("output length = %d, want <= %d", len(result.Stdout)+len(result.Stderr), cfg.MaxOutputBytes)
	}
	if !result.Truncated {
		t.Fatal("expected truncated output")
	}
}

func TestExecuteEnforcesToolTimeout(t *testing.T) {
	workspace := t.TempDir()
	writeScript(t, workspace, "mc_poll", "while :; do :; done")
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	cfg.ToolTimeout = 20 * time.Millisecond
	registry := testRegistry(t, cfg)

	result, err := registry.Execute(context.Background(), "mc_poll", json.RawMessage(`{"lines":1}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("expected timeout result, got %+v", result)
	}
	if result.OK {
		t.Fatalf("timeout result should not be OK: %+v", result)
	}
}

func testRegistry(t *testing.T, cfg Config) *Registry {
	t.Helper()
	if cfg.Workspace == "" || cfg.Workspace == DefaultWorkspace {
		cfg.Workspace = t.TempDir()
	}
	registry, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	return registry
}

func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/usr/bin/env sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write script %s: %v", name, err)
	}
}

func countArgvTools(registry *Registry) int {
	count := 0
	for _, tool := range registry.tools {
		if tool.buildArgv != nil {
			count++
		}
	}
	return count
}
