package greggpttools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityMapReadsWorkspaceFile(t *testing.T) {
	workspace := t.TempDir()
	want := "# Identity Map\n\n- Discord: exx\n  - Minecraft: __exx\n"
	if err := os.WriteFile(filepath.Join(workspace, "IDENTITIES.md"), []byte(want), 0o600); err != nil {
		t.Fatalf("write identities: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	registry := testRegistry(t, cfg)

	result, err := registry.Execute(context.Background(), "identity_map", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("identity_map returned error: %v", err)
	}
	if !result.OK || !strings.Contains(result.Stdout, "__exx") {
		t.Fatalf("unexpected identity result: %+v", result)
	}
}
