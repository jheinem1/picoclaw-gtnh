package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"greggpt-gtnh/internal/greggpttools"
)

func TestToolRegistrySeparatesValidationAndExecutionErrors(t *testing.T) {
	cfg := greggpttools.DefaultConfig()
	cfg.Workspace = t.TempDir()
	registry, err := NewToolRegistry(cfg)
	if err != nil {
		t.Fatalf("NewToolRegistry returned error: %v", err)
	}

	_, err = registry.Execute(context.Background(), ToolCall{Name: "inventory_find_item", Arguments: json.RawMessage(`{}`)})
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("schema failure should be ValidationError, got %T: %v", err, err)
	}

	_, err = registry.Execute(context.Background(), ToolCall{Name: "identity_map", Arguments: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected missing identity file execution error")
	}
	if errors.As(err, &validationErr) || !strings.Contains(err.Error(), "read identity map") {
		t.Fatalf("execution failure was mislabeled: %T: %v", err, err)
	}
}
