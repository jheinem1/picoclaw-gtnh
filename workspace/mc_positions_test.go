package workspace_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPositionsFormatsFilteredPlayer(t *testing.T) {
	tmp := t.TempDir()
	argsPath := filepath.Join(tmp, "curl-args")
	curlPath := filepath.Join(tmp, "curl")
	curlScript := "#!/usr/bin/env sh\nprintf '%s\\n' \"$*\" > \"$CURL_ARGS_PATH\"\nprintf '%s\\n' '{\"ok\":true,\"generated_at\":\"2026-08-11T20:15:00Z\",\"source\":\"greggpt_player_export\",\"players\":[{\"name\":\"Snow\",\"dim\":0,\"x\":12.5,\"y\":64,\"z\":-8.25}]}'\n"
	if err := os.WriteFile(curlPath, []byte(curlScript), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", "./mc_positions", "Snow")
	cmd.Env = append(os.Environ(), "PATH="+tmp+":"+os.Getenv("PATH"), "CURL_ARGS_PATH="+argsPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mc_positions failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "Snow: dim 0 at x=12.5 y=64 z=-8.25") || !strings.Contains(text, "2026-08-11T20:15:00Z") {
		t.Fatalf("unexpected output: %s", text)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "/mc/positions?player=Snow") {
		t.Fatalf("unexpected curl args: %s", args)
	}
}

func TestMCPositionsSurfacesBridgeError(t *testing.T) {
	tmp := t.TempDir()
	curlPath := filepath.Join(tmp, "curl")
	curlScript := "#!/usr/bin/env sh\nprintf '%s\\n' '{\"ok\":false,\"error\":\"player positions export is stale\"}'\n"
	if err := os.WriteFile(curlPath, []byte(curlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "./mc_positions")
	cmd.Env = append(os.Environ(), "PATH="+tmp+":"+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("mc_positions unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(string(output), "player positions export is stale") {
		t.Fatalf("structured bridge error was lost: %s", output)
	}
}
