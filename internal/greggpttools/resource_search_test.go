package greggpttools

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResourceSearchIncludesItemsAndFluidsWithStableKeysAndRouteCounts(t *testing.T) {
	path := resourceSearchFixture(t)
	result, err := searchRecipeResources(context.Background(), path, "", 5*time.Second, 1<<20, "Energetic Alloy", 10)
	if err != nil {
		t.Fatalf("searchRecipeResources returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("resource search failed: %+v", result)
	}
	var payload struct {
		Rows []struct {
			Kind                 string `json:"resource_kind"`
			Key                  string `json:"resource_key"`
			Name                 string `json:"resource_name"`
			PrimaryRouteCount    int    `json:"primary_route_count"`
			ProductionRouteCount int    `json:"production_route_count"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("decode resource search output: %v", err)
	}
	if len(payload.Rows) < 3 {
		t.Fatalf("expected item and fluid candidates: %s", result.Stdout)
	}
	if got := payload.Rows[0]; got.Key != "item:EnderIO:itemAlloy:1" || got.Name != "Energetic Alloy" {
		t.Fatalf("exact display name did not rank first: %+v", got)
	}
	var fluidFound bool
	for _, row := range payload.Rows {
		if row.Key == "fluid:molten.energeticalloy" {
			fluidFound = true
			if row.Kind != "fluid" || row.PrimaryRouteCount != 2 || row.ProductionRouteCount != 3 {
				t.Fatalf("unexpected fluid identity/counts: %+v", row)
			}
		}
	}
	if !fluidFound {
		t.Fatalf("molten energetic alloy fluid missing: %s", result.Stdout)
	}
}

func TestResourceSearchEscapesFTSSyntaxAndRanksExactFluidName(t *testing.T) {
	path := resourceSearchFixture(t)
	result, err := searchRecipeResources(context.Background(), path, "", 5*time.Second, 1<<20, `Molten "Energetic" Alloy`, 10)
	if err != nil {
		t.Fatalf("searchRecipeResources returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("resource search failed for quoted query: %+v", result)
	}
	var payload struct {
		Rows []struct {
			Kind string `json:"resource_kind"`
			Key  string `json:"resource_key"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("decode resource search output: %v", err)
	}
	if len(payload.Rows) == 0 || payload.Rows[0].Kind != "fluid" || payload.Rows[0].Key != "fluid:molten.energeticalloy" {
		t.Fatalf("exact fluid name did not rank first: %s", result.Stdout)
	}
}

func TestResourceSearchResolvesTooltipAliasWithoutRecipeRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE items (id INTEGER PRIMARY KEY, registry_name TEXT, damage INTEGER, display_name TEXT, unlocalized_name TEXT);
		CREATE TABLE fluids (id INTEGER PRIMARY KEY, fluid_name TEXT, localized_name TEXT);
		CREATE TABLE recipe_routes (recipe_id INTEGER, output_resource_key TEXT, is_primary INTEGER);
		CREATE VIEW resource_catalog AS
		SELECT 'item' AS resource_kind, id AS resource_id,
		       'item:' || registry_name || ':' || damage AS resource_key,
		       display_name AS resource_name, registry_name, damage, NULL AS fluid_name
		FROM items
		UNION ALL
		SELECT 'fluid', id, 'fluid:' || fluid_name,
		       COALESCE(localized_name, fluid_name), NULL, NULL, fluid_name
		FROM fluids;
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

	result, err := searchRecipeResources(context.Background(), path, aliasPath, 5*time.Second, 1<<20, "Aquamarine Pigment", 10)
	if err != nil {
		t.Fatalf("searchRecipeResources returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("resource search failed: %+v", result)
	}
	var payload struct {
		Rows []struct {
			Kind                 string `json:"resource_kind"`
			Key                  string `json:"resource_key"`
			Name                 string `json:"resource_name"`
			RegistryName         string `json:"registry_name"`
			Damage               int    `json:"damage"`
			PrimaryRouteCount    int    `json:"primary_route_count"`
			ProductionRouteCount int    `json:"production_route_count"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("decode resource search output: %v", err)
	}
	if len(payload.Rows) == 0 {
		t.Fatalf("tooltip alias returned no candidates: %s", result.Stdout)
	}
	got := payload.Rows[0]
	if got.Kind != "item" || got.Key != "item:Botany:pigment:0" || got.RegistryName != "Botany:pigment" || got.Damage != 0 {
		t.Fatalf("Aquamarine tooltip alias did not resolve to metadata 0 ahead of Medium Aquamarine metadata 42: %+v", got)
	}
	if got.Name != "Pigment" {
		t.Fatalf("alias lookup must preserve the canonical resource name, got %+v", got)
	}
	if got.PrimaryRouteCount != 0 || got.ProductionRouteCount != 0 {
		t.Fatalf("NEI-visible aliases must resolve even when the item has no recipe routes: %+v", got)
	}
}

func resourceSearchFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE items (id INTEGER PRIMARY KEY, registry_name TEXT, damage INTEGER, display_name TEXT, unlocalized_name TEXT);
		CREATE TABLE fluids (id INTEGER PRIMARY KEY, fluid_name TEXT, localized_name TEXT);
		CREATE TABLE recipe_routes (recipe_id INTEGER, output_resource_key TEXT, is_primary INTEGER);
		CREATE VIEW resource_catalog AS
		SELECT 'item' AS resource_kind, id AS resource_id,
		       'item:' || registry_name || ':' || damage AS resource_key,
		       display_name AS resource_name, registry_name, damage, NULL AS fluid_name
		FROM items
		UNION ALL
		SELECT 'fluid', id, 'fluid:' || fluid_name,
		       COALESCE(localized_name, fluid_name), NULL, NULL, fluid_name
		FROM fluids;
		CREATE VIRTUAL TABLE item_search USING fts5(display_name, registry_name, unlocalized_name, content='items', content_rowid='id');
		INSERT INTO items VALUES
			(1, 'TGregworks:tGregToolPartCrossbar', 1635, 'Energetic Alloy Crossbar', 'item.tgregtoolpartcrossbar'),
			(2, 'EnderIO:itemAlloy', 1, 'Energetic Alloy', 'item.itemAlloy.energeticAlloy');
		INSERT INTO fluids VALUES (1, 'molten.energeticalloy', 'Molten Energetic Alloy');
		INSERT INTO item_search(rowid, display_name, registry_name, unlocalized_name)
		SELECT id, display_name, registry_name, unlocalized_name FROM items;
		INSERT INTO recipe_routes VALUES
			(10, 'item:TGregworks:tGregToolPartCrossbar:1635', 1),
			(20, 'fluid:molten.energeticalloy', 1),
			(21, 'fluid:molten.energeticalloy', 1),
			(22, 'fluid:molten.energeticalloy', 0);
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
