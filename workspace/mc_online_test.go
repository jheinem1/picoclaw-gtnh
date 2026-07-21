package workspace_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCOnlineAPIFailureExitsNonzero(t *testing.T) {
	binDir := t.TempDir()
	curlPath := filepath.Join(binDir, "curl")
	if err := os.WriteFile(curlPath, []byte("#!/usr/bin/env sh\nprintf '%s\\n' '{\"ok\":false,\"error\":\"bridge unavailable\"}'\n"), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	cmd := exec.Command("sh", "./mc_online", "10")
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("API-level failure exited successfully: %s", output)
	}
	if !strings.Contains(string(output), "bridge unavailable") {
		t.Fatalf("unexpected API failure output: %q", output)
	}
}
