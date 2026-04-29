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
	  "source":{"players_scan_at":"2026-04-28T12:00:00Z","chests_scan_at":"2026-04-28T11:00:00Z","me_scan_at":"2026-04-28T12:04:00Z"},
	  "item_index":{"7437:11305":{"me":[{"label":"Main ME","dim":0,"pos":{"x":1,"y":2,"z":3},"total_count":128}]}}
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
