package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
		"7437d11305\tSteel Ingot\tgregtech:gt.metaitem.01\tgt.metaitem.01.11305\n"
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
