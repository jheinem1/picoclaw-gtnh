package greggpttools

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestItemSearchEscapesUserSyntaxAndReturnsStableIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE items (id INTEGER PRIMARY KEY, registry_name TEXT, damage INTEGER, display_name TEXT, unlocalized_name TEXT);
		CREATE VIRTUAL TABLE item_search USING fts5(display_name, registry_name, unlocalized_name, content='items', content_rowid='id');
		INSERT INTO items VALUES (1, 'gregtech:gt.metaitem.01', 32652, 'Robot Arm (HV)', 'gt.metaitem.01.32652');
		INSERT INTO item_search(rowid, display_name, registry_name, unlocalized_name) SELECT id, display_name, registry_name, unlocalized_name FROM items;
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Workspace = t.TempDir()
	cfg.RecipeSQLPath = path
	registry := testRegistry(t, cfg)
	result, err := registry.Execute(context.Background(), "item_search", json.RawMessage(`{"query":"Robot Arm (HV)","limit":5}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK || !strings.Contains(result.Stdout, `"resource_key":"item:gregtech:gt.metaitem.01:32652"`) {
		t.Fatalf("unexpected item search result: %+v", result)
	}
	if strings.Contains(result.Stdout, `"resource_id"`) || !strings.Contains(result.Stdout, `"internal_item_row_id":1`) {
		t.Fatalf("item search must clearly label SQLite row IDs as internal: %s", result.Stdout)
	}
}

func TestItemSearchRanksExactDisplayNameBeforeToolParts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE items (id INTEGER PRIMARY KEY, registry_name TEXT, damage INTEGER, display_name TEXT, unlocalized_name TEXT);
		CREATE VIRTUAL TABLE item_search USING fts5(display_name, registry_name, unlocalized_name, content='items', content_rowid='id');
		INSERT INTO items VALUES
			(1, 'TGregworks:tGregToolPartCrossbar', 1635, 'Energetic Alloy Crossbar', 'item.tgregtoolpartcrossbar'),
			(2, 'TGregworks:tGregToolPartChunk', 1635, 'Energetic Alloy Shard', 'item.tgregtoolpartchunk'),
			(3, 'EnderIO:itemAlloy', 1, 'Energetic Alloy', 'item.itemAlloy.energeticAlloy');
		INSERT INTO item_search(rowid, display_name, registry_name, unlocalized_name) SELECT id, display_name, registry_name, unlocalized_name FROM items;
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := searchRecipeItems(context.Background(), path, "", 5*time.Second, 1<<20, "  ENERGETIC ALLOY  ", 3)
	if err != nil {
		t.Fatalf("searchRecipeItems returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("item search failed: %+v", result)
	}
	var payload struct {
		Rows []struct {
			DisplayName string `json:"display_name"`
			ResourceKey string `json:"resource_key"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("decode item search output: %v", err)
	}
	if len(payload.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %s", len(payload.Rows), result.Stdout)
	}
	if got := payload.Rows[0]; got.DisplayName != "Energetic Alloy" || got.ResourceKey != "item:EnderIO:itemAlloy:1" {
		t.Fatalf("exact display-name match was not ranked first: %+v", got)
	}
}

func TestItemSearchResolvesTooltipAliasToExactVariant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE items (id INTEGER PRIMARY KEY, registry_name TEXT, damage INTEGER, display_name TEXT, unlocalized_name TEXT);
		CREATE VIRTUAL TABLE item_search USING fts5(display_name, registry_name, unlocalized_name, content='items', content_rowid='id');
		INSERT INTO items VALUES
			(1, 'Botany:pigment', 0, 'Pigment', 'item.pigment'),
			(2, 'Botany:pigment', 42, 'Pigment', 'item.pigment');
		INSERT INTO item_search(rowid, display_name, registry_name, unlocalized_name)
		SELECT id, display_name, registry_name, unlocalized_name FROM items;
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	aliasPath := filepath.Join(t.TempDir(), "item_aliases.tsv")
	aliases := "registry_name\tdamage\talias\tsource\n" +
		"Botany:pigment\t0\tAquamarine\ttooltip\n" +
		"Botany:pigment\t42\tMedium Aquamarine\ttooltip\n"
	if err := os.WriteFile(aliasPath, []byte(aliases), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := searchRecipeItems(context.Background(), path, aliasPath, 5*time.Second, 1<<20, "Aquamarine Pigment", 10)
	if err != nil {
		t.Fatalf("searchRecipeItems returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("item search failed: %+v", result)
	}
	var payload struct {
		Rows []struct {
			ResourceKey  string `json:"resource_key"`
			DisplayName  string `json:"display_name"`
			RegistryName string `json:"registry_name"`
			Damage       int    `json:"damage"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("decode item search output: %v", err)
	}
	if len(payload.Rows) == 0 {
		t.Fatalf("tooltip alias returned no candidates: %s", result.Stdout)
	}
	got := payload.Rows[0]
	if got.ResourceKey != "item:Botany:pigment:0" || got.RegistryName != "Botany:pigment" || got.Damage != 0 {
		t.Fatalf("Aquamarine tooltip alias did not resolve to metadata 0 ahead of Medium Aquamarine metadata 42: %+v", got)
	}
	if got.DisplayName != "Pigment" {
		t.Fatalf("alias lookup must preserve the canonical item display name, got %+v", got)
	}
}
