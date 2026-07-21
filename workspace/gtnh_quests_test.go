package workspace_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGTNHQuestsListLimitIsClampedTo500(t *testing.T) {
	quests := make([]map[string]any, 0, 550)
	for i := 0; i < 550; i++ {
		quests = append(quests, map[string]any{
			"id":        fmt.Sprintf("quest-%d", i),
			"completed": false,
		})
	}

	indexPath := filepath.Join(t.TempDir(), "quest_index.json")
	raw, err := json.Marshal(map[string]any{"quests": quests})
	if err != nil {
		t.Fatalf("marshal quest index: %v", err)
	}
	if err := os.WriteFile(indexPath, raw, 0o600); err != nil {
		t.Fatalf("write quest index: %v", err)
	}

	cmd := exec.Command("sh", "./gtnh_quests", "open-json", "--limit", "900")
	cmd.Env = append(os.Environ(), "GTNH_QUEST_INDEX_FILE="+indexPath)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gtnh_quests failed: %v", err)
	}

	var result struct {
		Quests []json.RawMessage `json:"quests"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(result.Quests) != 500 {
		t.Fatalf("quest count = %d, want 500", len(result.Quests))
	}
}

func TestGTNHQuestsRejectsInvalidLimit(t *testing.T) {
	cmd := exec.Command("sh", "./gtnh_quests", "open-json", "--limit", "many")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("gtnh_quests succeeded unexpectedly: %s", output)
	}
	if !strings.Contains(string(output), "limit must be a positive integer") {
		t.Fatalf("error = %q, want positive-integer guard", output)
	}
}

func TestGTNHQuestsShowMissingQuestFails(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "quest_index.json")
	if err := os.WriteFile(indexPath, []byte(`{"quests":[{"id":"known"}]}`), 0o600); err != nil {
		t.Fatalf("write quest index: %v", err)
	}
	cmd := exec.Command("sh", "./gtnh_quests", "show", "missing")
	cmd.Env = append(os.Environ(), "GTNH_QUEST_INDEX_FILE="+indexPath)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("missing quest succeeded unexpectedly: %s", output)
	}
	if !strings.Contains(string(output), "quest missing not found") {
		t.Fatalf("missing quest error = %q", output)
	}
}
