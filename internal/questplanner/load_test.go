package questplanner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadWorkspaceNormalizesV1GraphAndResolvesRegistryItems(t *testing.T) {
	workspace := t.TempDir()
	for _, dir := range []string{"state", filepath.Join("gtnh-data", "index")} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	questJSON := fmt.Sprintf(`{
	  "version":1,
	  "source":{"quests_scan_at":%q},
	  "quests":[
	    {"id":"1","title":"Done","completed":true},
	    {"id":"2","title":"Registry Quest","prerequisites":["1"],"tasks":[{"required_items":[{"reg_name":"gregtech:gt.metaitem.01","damage":11305,"count":4,"display_name":"Steel Ingot"}]}]}
	  ]
	}`, now)
	inventoryJSON := fmt.Sprintf(`{
	  "version":1,
	  "source":{"players_scan_at":%q,"chests_scan_at":%q,"me_scan_at":%q},
	  "item_index":{"7437:11305":{"me":[{"total_count":4}]}}
	}`, now, now, now)
	writeTestFile(t, filepath.Join(workspace, "state", "quest_index.json"), questJSON)
	writeTestFile(t, filepath.Join(workspace, "state", "inventory_index.json"), inventoryJSON)
	writeTestFile(t, filepath.Join(workspace, "gtnh-data", "index", "item_index.tsv"), "slug\tdisplay_name\treg_name\tname\n7437d11305\tSteel Ingot\tgregtech:gt.metaitem.01\tgt.metaitem.01.11305\n")
	writeTestFile(t, filepath.Join(workspace, "state", "gtnh_tasks.tsv"), "id\tstatus\tpriority\tarea\tcreated_at\tupdated_at\ttitle\tkanban_status\tsort_key\towner\tpaused_reason\tdescription\n")

	planner, err := LoadWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := planner.quests.Quests[0].Unlocks; len(got) != 1 || got[0] != "2" {
		t.Fatalf("normalized unlocks = %#v, want quest 2", got)
	}
	if got := planner.quests.Quests[1].Tasks[0].ID; got != "0" {
		t.Fatalf("normalized task id = %q, want 0", got)
	}
	recommendation := planner.Recommend("Snow", "")
	if recommendation.QuestID != "2" || len(recommendation.Materials) != 1 || recommendation.Materials[0].Available != 4 {
		t.Fatalf("unexpected recommendation: %#v", recommendation)
	}
	if recommendation.Materials[0].Resolution != "registry_name_damage" {
		t.Fatalf("unexpected registry resolution: %#v", recommendation.Materials[0])
	}
}
func TestLoadWorkspacePrefersCompactInventoryTotals(t *testing.T) {
	workspace := t.TempDir()
	for _, dir := range []string{"state", filepath.Join("gtnh-data", "index")} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	writeTestFile(t, filepath.Join(workspace, "state", "quest_index.json"), fmt.Sprintf(`{
	  "version":2,
	  "source":{"quests_scan_at":%q},
	  "quests":[{"id":"2","title":"Compact Quest","state":"ready","tasks":[{"required_items":[{"reg_name":"gregtech:gt.metaitem.01","damage":11305,"count":4,"display_name":"Steel Ingot"}]}]}]
	}`, now))
	writeTestFile(t, filepath.Join(workspace, "state", "quest_planner_index.json"), fmt.Sprintf(`{
	  "version":1,
	  "source":{"quests_scan_at":%q},
	  "quests":[{"id":"2","title":"Fast Compact Quest","state":"ready","tasks":[{"required_items":[{"reg_name":"gregtech:gt.metaitem.01","damage":11305,"count":4,"display_name":"Steel Ingot"}]}]}]
	}`, now))
	writeTestFile(t, filepath.Join(workspace, "state", "quest_inventory_totals.json"), fmt.Sprintf(`{
	  "version":1,
	  "source":{"players_scan_at":%q,"chests_scan_at":%q,"me_scan_at":%q},
	  "totals":{"7437:11305":9}
	}`, now, now, now))
	writeTestFile(t, filepath.Join(workspace, "gtnh-data", "index", "item_index.tsv"), "slug\tdisplay_name\treg_name\tname\n7437d11305\tSteel Ingot\tgregtech:gt.metaitem.01\tgt.metaitem.01.11305\n")

	planner, err := LoadWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	recommendation := planner.Recommend("Snow", "")
	if recommendation.Recommendation != "Fast Compact Quest" || len(recommendation.Materials) != 1 || recommendation.Materials[0].Available != 9 {
		t.Fatalf("compact quest and inventory snapshots were not used: %#v", recommendation)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
