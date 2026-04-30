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
		{"gtnh_find_item", `{"query":"steel ingot","oredict":true}`, []string{"sh", "gtnh_find_item", "steel ingot", "--oredict"}},
		{"gtnh_item", `{"slug":"7437d11305"}`, []string{"sh", "gtnh_item", "7437d11305"}},
		{"gtnh_resolve_recipes", `{"item":"bronze fluid pipe"}`, []string{"sh", "gtnh_resolve_recipes", "bronze fluid pipe"}},
		{"gtnh_search_recipes", `{"query":"potin fluid pipe"}`, []string{"sh", "gtnh_search_recipes", "potin fluid pipe"}},
		{"gtnh_wiki_page", `{"title":"Steam Machines"}`, []string{"sh", "gtnh_wiki_page", "Steam Machines"}},
		{"inventory_status", `{}`, []string{"sh", "gtnh_inventory", "status"}},
		{"inventory_find", `{"item":"gregtech:gt.metaitem.01:11305","any_damage":true,"player":"Snow","scope":"players","limit":5}`, []string{"sh", "gtnh_inventory", "find", "--item", "gregtech:gt.metaitem.01:11305", "--any-damage", "--player", "Snow", "--scope", "players", "--limit", "5"}},
		{"inventory_find_item", `{"query":"steel ingot","oredict":true,"scope":"me","limit":7}`, []string{"sh", "gtnh_inventory", "find-item", "--query", "steel ingot", "--oredict", "--scope", "me", "--limit", "7"}},
		{"inventory_player", `{"name":"Snow","all":true}`, []string{"sh", "gtnh_inventory", "player", "--name", "Snow", "--all"}},
		{"inventory_chest", `{"x":1,"y":64,"z":-2,"dim":-1}`, []string{"sh", "gtnh_inventory", "chest", "--x", "1", "--y", "64", "--z", "-2", "--dim", "-1"}},
		{"inventory_find_block_name", `{"block":"Super Chest I","limit":5}`, []string{"sh", "gtnh_inventory", "find-block", "--block", "Super Chest I", "--limit", "5"}},
		{"inventory_find_block", `{"id":300,"meta":5,"limit":9}`, []string{"sh", "gtnh_inventory", "find-block", "--id", "300", "--meta", "5", "--limit", "9"}},
		{"inventory_refresh", `{"scope":"containers"}`, []string{"sh", "gtnh_inventory", "refresh", "--containers"}},
		{"task_board", `{}`, []string{"sh", "gtnh_tasks", "board"}},
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

	if len(tests) != len(registry.Definitions()) {
		t.Fatalf("test table covers %d tools, registry has %d", len(tests), len(registry.Definitions()))
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

func TestArgumentValidation(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	tests := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"missing required", "gtnh_find_item", `{}`, `missing required argument "query"`},
		{"unknown argument", "gtnh_find_item", `{"query":"steel","cmd":"rm -rf /"}`, `unknown argument "cmd"`},
		{"scope enum", "inventory_find", `{"item":"minecraft:stone","scope":"everywhere"}`, `argument "scope" must be one of`},
		{"status enum", "task_move", `{"id":1,"status":"blocked"}`, `argument "status" must be one of`},
		{"dim enum", "inventory_chest", `{"x":0,"y":64,"z":0,"dim":2}`, `argument "dim" must be one of`},
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

func TestExecuteUsesConfiguredWorkspaceAndOutputLimit(t *testing.T) {
	workspace := t.TempDir()
	writeScript(t, workspace, "gtnh_tasks", "printf '%s\\n' \"$PWD\"; printf 'abcdef'")
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	cfg.MaxOutputBytes = len(workspace) + 2
	registry := testRegistry(t, cfg)

	result, err := registry.Execute(context.Background(), "task_summary", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("Execute result not OK: %+v", result)
	}
	if !strings.HasPrefix(result.Stdout, workspace+"\n") {
		t.Fatalf("stdout = %q, want prefix %q", result.Stdout, workspace+"\n")
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
