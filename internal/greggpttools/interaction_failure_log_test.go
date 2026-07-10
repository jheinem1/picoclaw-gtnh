package greggpttools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInteractionFailureLogDefinition(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	def, ok := registry.Definition("interaction_failure_log")
	if !ok {
		t.Fatal("interaction_failure_log definition not found")
	}
	if def.Group != GroupDiagnostics {
		t.Fatalf("group = %q, want %q", def.Group, GroupDiagnostics)
	}
	if def.Parameters.AdditionalProperties {
		t.Fatal("expected strict schema")
	}
	wantRequired := []string{"reason", "request_summary", "failure_summary"}
	if !reflect.DeepEqual(def.Parameters.Required, wantRequired) {
		t.Fatalf("required = %#v, want %#v", def.Parameters.Required, wantRequired)
	}
	for _, name := range wantRequired {
		spec, ok := def.Parameters.Properties[name]
		if !ok {
			t.Fatalf("missing property %q", name)
		}
		if spec.Type != "string" || spec.MinLength == 0 {
			t.Fatalf("%s spec = %+v, want non-empty string", name, spec)
		}
	}
	failedTools := def.Parameters.Properties["failed_tools"]
	if failedTools.Type != "array" || failedTools.Items == nil || failedTools.Items.Type != "string" {
		t.Fatalf("failed_tools spec = %+v, want string array", failedTools)
	}
}

func TestInteractionFailureLogAppendCreatesParentAndWritesJSONL(t *testing.T) {
	workspace := t.TempDir()
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	registry := testRegistry(t, cfg)

	result, err := registry.Execute(context.Background(), "interaction_failure_log", json.RawMessage(`{
		"reason":"missing local data",
		"request_summary":"user asked for current ME stock",
		"failure_summary":"inventory export was missing",
		"failed_tools":["inventory_status","inventory_find_item"],
		"next_step":"refresh inventory export"
	}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("result not OK: %+v", result)
	}

	path := filepath.Join(workspace, DefaultFailedInteractionsPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1: %q", len(lines), raw)
	}
	var entry interactionFailureLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("parse JSONL entry: %v", err)
	}
	if entry.SchemaVersion != interactionFailureLogSchemaVersion {
		t.Fatalf("schema version = %d", entry.SchemaVersion)
	}
	if entry.TimestampUTC == "" {
		t.Fatal("timestamp_utc was empty")
	}
	if entry.Reason != "missing local data" || entry.RequestSummary != "user asked for current ME stock" || entry.FailureSummary != "inventory export was missing" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if !reflect.DeepEqual(entry.FailedTools, []string{"inventory_status", "inventory_find_item"}) {
		t.Fatalf("failed tools = %#v", entry.FailedTools)
	}
	if entry.NextStep != "refresh inventory export" {
		t.Fatalf("next step = %q", entry.NextStep)
	}
}

func TestInteractionFailureLogMultipleCallsAppendParseableRecords(t *testing.T) {
	workspace := t.TempDir()
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	cfg.FailedInteractionsPath = "nested/failures.jsonl"
	registry := testRegistry(t, cfg)

	for _, reason := range []string{"broken tool", "ambiguous request"} {
		args := `{"reason":` + strconvQuote(reason) + `,"request_summary":"request","failure_summary":"failed"}`
		if _, err := registry.Execute(context.Background(), "interaction_failure_log", json.RawMessage(args)); err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(workspace, "nested/failures.jsonl"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("line count = %d, want 2: %q", len(lines), raw)
	}
	for i, line := range lines {
		var entry interactionFailureLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parse line %d: %v", i, err)
		}
	}
}

func TestInteractionFailureLogValidationRequiresSummaries(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	tests := []struct {
		name string
		args string
		want string
	}{
		{"missing reason", `{"request_summary":"request","failure_summary":"failed"}`, `missing required argument "reason"`},
		{"missing request", `{"reason":"broken tool","failure_summary":"failed"}`, `missing required argument "request_summary"`},
		{"missing failure", `{"reason":"broken tool","request_summary":"request"}`, `missing required argument "failure_summary"`},
		{"empty reason", `{"reason":" ","request_summary":"request","failure_summary":"failed"}`, `argument "reason" must not be empty`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.Validate("interaction_failure_log", json.RawMessage(tt.args))
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestFailedInteractionsPathResolution(t *testing.T) {
	workspace := t.TempDir()
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	if got, want := cfg.resolvedFailedInteractionsPath(), filepath.Join(workspace, DefaultFailedInteractionsPath); got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}

	cfg.FailedInteractionsPath = "custom/failures.jsonl"
	if got, want := cfg.resolvedFailedInteractionsPath(), filepath.Join(workspace, "custom/failures.jsonl"); got != want {
		t.Fatalf("relative path = %q, want %q", got, want)
	}

	absolute := filepath.Join(t.TempDir(), "failures.jsonl")
	cfg.FailedInteractionsPath = absolute
	if got := cfg.resolvedFailedInteractionsPath(); got != absolute {
		t.Fatalf("absolute path = %q, want %q", got, absolute)
	}
}

func TestConfigFromEnvReadsFailedInteractionsPath(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv(EnvWorkspace, workspace)
	t.Setenv(EnvFailedInteractionsPath, "env/failures.jsonl")

	cfg := ConfigFromEnv()
	if got, want := cfg.FailedInteractionsPath, "env/failures.jsonl"; got != want {
		t.Fatalf("FailedInteractionsPath = %q, want %q", got, want)
	}
	if got, want := cfg.resolvedFailedInteractionsPath(), filepath.Join(workspace, "env/failures.jsonl"); got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func strconvQuote(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
