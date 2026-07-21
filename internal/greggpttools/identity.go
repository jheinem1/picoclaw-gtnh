package greggpttools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func identityMapTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("identity_map", GroupIdentity, "Read the small collaborative Discord-to-Minecraft identity map. Use before personalizing inventory, quest, or task results when the Minecraft identity is uncertain.", timeout, object(), func(context.Context, Arguments) (Result, error) {
		path := filepath.Join(cfg.Workspace, "IDENTITIES.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return Result{}, fmt.Errorf("read identity map: %w", err)
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return Result{Name: "identity_map", OK: false, ExitCode: 1, Stderr: "identity map is empty"}, nil
		}
		truncated := false
		if len(text) > cfg.MaxOutputBytes {
			text = text[:cfg.MaxOutputBytes]
			truncated = true
		}
		return Result{Name: "identity_map", OK: true, Stdout: text, Truncated: truncated}, nil
	})
}
