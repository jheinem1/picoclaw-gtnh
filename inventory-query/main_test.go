package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceDirUsesGregGPTWorkspace(t *testing.T) {
	t.Setenv("GTNH_WORKSPACE", "")
	t.Setenv("GREGGPT_WORKSPACE", "/root/.greggpt/workspace")
	t.Setenv("GTNH_INVENTORY_INDEX_FILE", "")

	if got := workspaceDir(); got != "/root/.greggpt/workspace" {
		t.Fatalf("workspaceDir() = %q", got)
	}
	if got := defaultIndexFile(); got != "/root/.greggpt/workspace/state/inventory_index.json" {
		t.Fatalf("defaultIndexFile() = %q", got)
	}
}

func writeTestWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	indexDir := filepath.Join(dir, "gtnh-data", "index")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	items := "slug\tdisplay_name\treg_name\tname\n" +
		"7437d32652\tRobot Arm (HV)\tgregtech:gt.metaitem.01\tgt.metaitem.01.32652\n" +
		"7437d32653\tRobot Arm (EV)\tgregtech:gt.metaitem.01\tgt.metaitem.01.32653\n" +
		"7437d11305\tSteel Ingot\tgregtech:gt.metaitem.01\tgt.metaitem.01.11305\n" +
		"5391d0\tAny MV Circuit\tdreamcraft:item.CircuitMV\titem.CircuitMV\n" +
		"9001d0\tMysterious Crystal\tdreamcraft:item.MysteriousCrystal\titem.MysteriousCrystal\n" +
		"9002d0\tMysterious Crystal Block\tdreamcraft:tile.MysteriousCrystal\ttile.MysteriousCrystal\n"
	if err := os.WriteFile(filepath.Join(indexDir, "item_index.tsv"), []byte(items), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := `{
	  "version":2,
	  "source":{"players_scan_at":"2026-04-28T12:00:00Z","chests_scan_at":"2026-04-28T11:00:00Z","me_scan_at":"2026-04-28T12:04:00Z","block_inventories_scan_at":"2026-04-28T12:06:00Z","blocks_scan_at":"2026-04-28T12:05:00Z"},
	  "block_status":{"enabled":true,"registry_available":false,"reason":"block registry unavailable; numeric id/meta search only"},
	  "chests":[{"dim":0,"x":100,"y":64,"z":-25,"type":"Super Chest I","source":"block_export","items":[{"id":7437,"damage":11305,"count":32768,"slot":0,"source":"gregtech-direct"}]},{"dim":183,"x":10,"y":21,"z":-24,"type":"Super Chest I","source":"block_export","items":[{"id":7437,"damage":11305,"count":128,"slot":0,"source":"gregtech-direct"}]}],
	  "item_index":{"7437:11305":{"chests":[{"dim":0,"x":100,"y":64,"z":-25,"type":"Super Chest I","total_count":32768},{"dim":183,"x":10,"y":21,"z":-24,"type":"Super Chest I","total_count":128}],"me":[{"label":"Main ME","dim":0,"pos":{"x":1,"y":2,"z":3},"total_count":128}]}},
	  "block_index":{"300:5":{"blocks":[{"dim":0,"x":35,"y":71,"z":-8,"id":300,"meta":5}]},"2442:135":{"blocks":[{"dim":0,"x":100,"y":64,"z":-25,"id":2442,"meta":135,"reg_name":"gregtech:gt.blockmachines","name":"Super Chest I"},{"dim":183,"x":10,"y":21,"z":-24,"id":2442,"meta":135,"reg_name":"gregtech:gt.blockmachines","name":"Super Chest I"}]}}
	}`
	if err := os.WriteFile(filepath.Join(stateDir, "inventory_index.json"), []byte(idx), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveQuery_TierAliasRanksPlayerFacingName(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)

	idx := InventoryIndex{ItemIndex: map[string]ItemHits{}}
	got, err := resolveQuery("hv robot arm", idx, 3)
	if err != nil {
		t.Fatalf("resolveQuery failed: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected matches")
	}
	if got[0].DisplayName != "Robot Arm (HV)" {
		t.Fatalf("expected HV robot arm first, got %#v", got[0])
	}
}

func TestResolveQuery_PrefersPresentStorageCandidate(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)

	idx, err := loadIndex()
	if err != nil {
		t.Fatalf("loadIndex failed: %v", err)
	}
	got, err := resolveQuery("steel ingot", idx, 3)
	if err != nil {
		t.Fatalf("resolveQuery failed: %v", err)
	}
	if len(got) == 0 || got[0].Slug != "7437d11305" {
		t.Fatalf("expected present GregTech steel ingot first, got %#v", got)
	}
}

func TestScopeSet_AllIncludesME(t *testing.T) {
	players, containers, me, err := scopeSet("all")
	if err != nil {
		t.Fatalf("scopeSet failed: %v", err)
	}
	if !players || !containers || !me {
		t.Fatalf("expected all scopes true, got players=%t containers=%t me=%t", players, containers, me)
	}
}

func TestScopeSet_BothMeansPlayersAndContainers(t *testing.T) {
	players, containers, me, err := scopeSet("both")
	if err != nil {
		t.Fatalf("scopeSet failed: %v", err)
	}
	if !players || !containers || me {
		t.Fatalf("expected players and containers only, got players=%t containers=%t me=%t", players, containers, me)
	}
}

func TestResolveFindKeysRejectsExplicitAndAnyDamage(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	idx, err := loadIndex()
	if err != nil {
		t.Fatalf("loadIndex failed: %v", err)
	}
	_, _, err = resolveFindKeys("gregtech:gt.metaitem.01:11305", 0, unsetDamage, true, idx)
	if err == nil {
		t.Fatal("expected explicit damage plus any-damage to fail")
	}
}

func TestResolveExactItemMetaRequiresDamage(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	items, _, err := loadItems()
	if err != nil {
		t.Fatalf("loadItems failed: %v", err)
	}
	got, ok := resolveExactItemMeta(items, "gregtech:gt.metaitem.01:11305")
	if !ok || got.DisplayName != "Steel Ingot" {
		t.Fatalf("unexpected exact item resolution: %#v ok=%t", got, ok)
	}
	if _, ok := resolveExactItemMeta(items, "gregtech:gt.metaitem.01"); ok {
		t.Fatal("damage-less identity should not resolve in batch totals")
	}
}

func TestResolveExactItemMetaAcceptsUniqueLegacyPathPrefix(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	items, _, err := loadItems()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resolveExactItemMeta(items, "dreamcraft:CircuitMV:0")
	if !ok || got.RegName != "dreamcraft:item.CircuitMV" || got.Damage != 0 {
		t.Fatalf("unexpected compatibility resolution: %#v ok=%t", got, ok)
	}
}

func TestResolveExactItemMetaRejectsAmbiguousLegacyPathPrefix(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	items, _, err := loadItems()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveExactItemMeta(items, "dreamcraft:MysteriousCrystal:0"); ok {
		t.Fatalf("ambiguous item/tile compatibility identity resolved as %#v", got)
	}
}

func TestCmdTotalsLoadsOnceAndReturnsDimensionFilteredAggregates(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	err = cmdTotals([]string{"--item", "gregtech:gt.metaitem.01:11305", "--dim", "183", "--scope", "all"})
	_ = writer.Close()
	os.Stdout = originalStdout
	if err != nil {
		t.Fatalf("cmdTotals failed: %v", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Dim   int                 `json:"dim"`
		Items []inventoryTotalRow `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode totals JSON: %v\n%s", err, raw)
	}
	if payload.Dim != 183 || len(payload.Items) != 1 {
		t.Fatalf("unexpected totals payload: %#v", payload)
	}
	row := payload.Items[0]
	if row.Containers != 128 || row.ME != 0 || row.Total != 128 || row.ResourceKey != "item:gregtech:gt.metaitem.01:11305" {
		t.Fatalf("unexpected aggregate row: %#v", row)
	}
}

func TestResolveBlockKeys_Numeric(t *testing.T) {
	idx := InventoryIndex{BlockIndex: map[string]BlockHits{}}
	keys, label, err := resolveBlockKeys(idx, "", 300, 5)
	if err != nil {
		t.Fatalf("resolveBlockKeys failed: %v", err)
	}
	if len(keys) != 1 || keys[0] != "300:5" || label != "300:5" {
		t.Fatalf("unexpected block resolution: keys=%#v label=%q", keys, label)
	}
}

func TestResolveBlockKeys_ExportedNameWhenRegistryUnavailable(t *testing.T) {
	idx := InventoryIndex{
		BlockStatus: BlockIndexStatus{RegistryAvailable: false},
		BlockIndex: map[string]BlockHits{
			"2442:135": {Blocks: []BlockHit{{ID: 2442, Meta: 135, RegName: "gregtech:gt.blockmachines", Name: "Super Chest I"}}},
		},
	}
	keys, label, err := resolveBlockKeys(idx, "Super Chest I", 0, unsetDamage)
	if err != nil {
		t.Fatalf("resolveBlockKeys failed: %v", err)
	}
	if len(keys) != 1 || keys[0] != "2442:135" || label != "Super Chest I" {
		t.Fatalf("unexpected block resolution: keys=%#v label=%q", keys, label)
	}
}

func TestResolveBlockKeys_MissingName(t *testing.T) {
	idx := InventoryIndex{BlockStatus: BlockIndexStatus{RegistryAvailable: false}, BlockIndex: map[string]BlockHits{}}
	_, _, err := resolveBlockKeys(idx, "minecraft:stone", 0, unsetDamage)
	if err == nil {
		t.Fatal("expected missing block error")
	}
}

func TestMergeBlockHits(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	idx, err := loadIndex()
	if err != nil {
		t.Fatalf("loadIndex failed: %v", err)
	}
	hits := mergeBlockHits(idx, []string{"300:5"})
	if len(hits) != 1 || hits[0].X != 35 || hits[0].Y != 71 || hits[0].Z != -8 {
		t.Fatalf("unexpected block hits: %#v", hits)
	}
}

func TestFilterItemHitsByDimension(t *testing.T) {
	hits := ItemHits{
		Players: []PlayerHit{{Name: "Overworld", Dimension: 0}, {Name: "Pocket", Dimension: 183}},
		Chests:  []ChestHit{{Dimension: 0}, {Dimension: 183}},
		ME:      []MEHit{{Dimension: 0}, {Dimension: 183}},
	}
	got := filterItemHitsByDimension(hits, 183)
	if len(got.Players) != 1 || got.Players[0].Name != "Pocket" || len(got.Chests) != 1 || got.Chests[0].Dimension != 183 || len(got.ME) != 1 || got.ME[0].Dimension != 183 {
		t.Fatalf("unexpected dimension-filtered hits: %#v", got)
	}
}

func TestFilterItemHitsByDimensionUnset(t *testing.T) {
	hits := ItemHits{Chests: []ChestHit{{Dimension: 0}, {Dimension: 183}}}
	got := filterItemHitsByDimension(hits, unsetDimension)
	if len(got.Chests) != 2 {
		t.Fatalf("unset dimension should preserve all hits: %#v", got)
	}
}

func TestFilterBlockHitsByDimension(t *testing.T) {
	hits := []BlockHit{{Dimension: 0, X: 1}, {Dimension: 183, X: 2}}
	got := filterBlockHitsByDimension(hits, 183)
	if len(got) != 1 || got[0].Dimension != 183 || got[0].X != 2 {
		t.Fatalf("unexpected dimension-filtered blocks: %#v", got)
	}
}

func TestFilterBlockHitsByName_DisambiguatesSharedGregTechBlockKey(t *testing.T) {
	hits := []BlockHit{
		{ID: 2442, Meta: 1, Name: "Basic Miner"},
		{ID: 2442, Meta: 1, Name: "Super Chest I"},
	}
	got := filterBlockHitsByName(hits, "Super Chest I")
	if len(got) != 1 || got[0].Name != "Super Chest I" {
		t.Fatalf("unexpected filtered hits: %#v", got)
	}
}

func TestCmdChest_ExportedBlockInventory(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	if err := cmdChest([]string{"--x", "100", "--y", "64", "--z", "-25", "--dim", "0"}); err != nil {
		t.Fatalf("cmdChest failed: %v", err)
	}
}

func TestCmdCountItemUsesCompactScopedTotals(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	snapshot := `{
	  "version":2,
	  "source":{"players_scan_at":"2026-04-28T12:00:00Z","chests_scan_at":"2026-04-28T11:00:00Z","me_scan_at":"2026-04-28T12:04:00Z"},
	  "totals":{"7437:11305":205},
	  "scopes":{"7437:11305":{"players":5,"containers":128,"me":72,"total":205}}
	}`
	if err := os.WriteFile(filepath.Join(ws, "state", "quest_inventory_totals.json"), []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	err = cmdCountItem([]string{"--query", "steel ingot"})
	_ = writer.Close()
	os.Stdout = originalStdout
	if err != nil {
		t.Fatalf("cmdCountItem failed: %v", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	output := string(raw)
	if !strings.Contains(output, "Shared storage total=200 containers=128 me=72") || !strings.Contains(output, "Player inventories=5 all indexed=205") {
		t.Fatalf("unexpected compact count output: %s", output)
	}
}

func TestCmdMECraftingRanksInstalledPatternByMEDeficit(t *testing.T) {
	ws := writeTestWorkspace(t)
	t.Setenv("GTNH_WORKSPACE", ws)
	crafting := `{"version":2,"generated_at":"2026-08-11T12:01:00Z","me_scan_at":"2026-08-11T12:00:00Z","networks":[{"network_id":"main","label":"Main ME","dim":0,"items":[{"id":1,"damage":0,"count":2}],"patterns_truncated":true,"patterns":[{"craftable":true,"priority":5,"inputs":[{"id":1,"damage":0,"count":4,"display_name":"Iron Ingot"}],"outputs":[{"id":2,"damage":0,"count":1,"display_name":"Energetic Alloy"}]}],"active_crafts":[{"cpu_name":"CPU 1","output":{"id":2,"damage":0,"count":64,"display_name":"Energetic Alloy"},"remaining_count":12}]},{"network_id":"isolated","dim":0,"items":[{"id":1,"damage":0,"count":100}]}]}`
	totals := `{"version":2,"totals":{"1:0":3},"scopes":{"1:0":{"players":0,"containers":1,"me":2,"total":3}}}`
	if err := os.WriteFile(filepath.Join(ws, "state", "me_crafting.json"), []byte(crafting), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "state", "quest_inventory_totals.json"), []byte(totals), 0o600); err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	err = cmdMECrafting([]string{"--query", "energetic alloy", "--active"})
	_ = writer.Close()
	os.Stdout = originalStdout
	if err != nil {
		t.Fatalf("cmdMECrafting failed: %v", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	output := string(raw)
	for _, expected := range []string{`"patterns_truncated": true`, `"direct_me_missing_total": 2`, `"missing_from_shared": 1`, `"cpu_name": "CPU 1"`, `"me_freshness"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("ME crafting output missing %s: %s", expected, output)
		}
	}
}
