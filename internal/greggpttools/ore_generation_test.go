package greggpttools

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestOreGenerationLookupFindsSecondaryOreByMaterial(t *testing.T) {
	registry := oreGenerationTestRegistry(t)
	result, err := registry.Execute(context.Background(), "ore_generation_lookup", json.RawMessage(`{"query":"Yttrium Ore"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("result not OK: %+v", result)
	}
	for _, want := range []string{`"status":"ok"`, `"name":"Yttrium"`, `"source_name":"Niobium"`, `"role":"secondary"`, `"dimension":"Barnard F"`, `"min_y":5`, `"max_y":30`} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("stdout missing %s: %s", want, result.Stdout)
		}
	}
	if strings.Contains(result.Stdout, `"generation_kind":"small_ore"`) {
		t.Fatalf("unexpected small-ore route for Yttrium: %s", result.Stdout)
	}
}

func TestOreGenerationLookupInfersNamedVein(t *testing.T) {
	registry := oreGenerationTestRegistry(t)
	result, err := registry.Execute(context.Background(), "ore_generation_lookup", json.RawMessage(`{"query":"Niobium vein","dimension":"Barnard F"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK || !strings.Contains(result.Stdout, `"kind":"vein"`) {
		t.Fatalf("vein was not resolved: %+v", result)
	}
	if !strings.Contains(result.Stdout, `"material_name":"Yttrium"`) || !strings.Contains(result.Stdout, `"material_name":"Niobium"`) {
		t.Fatalf("vein materials missing: %s", result.Stdout)
	}
	if strings.Contains(result.Stdout, `"dimension":"Triton"`) {
		t.Fatalf("dimension filter was not applied: %s", result.Stdout)
	}
}

func TestOreGenerationLookupIncludesSmallOre(t *testing.T) {
	registry := oreGenerationTestRegistry(t)
	result, err := registry.Execute(context.Background(), "ore_generation_lookup", json.RawMessage(`{"query":"Copper ore"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Stdout, `"generation_kind":"small_ore"`) || !strings.Contains(result.Stdout, `"amount_per_chunk":16`) {
		t.Fatalf("small-ore generation missing: %s", result.Stdout)
	}
}

func TestOreGenerationLookupRequiresMigratedDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE manifest(key TEXT PRIMARY KEY, value TEXT); INSERT INTO manifest VALUES ('schema_version','1')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	cfg := DefaultConfig()
	cfg.Workspace = t.TempDir()
	cfg.RecipeSQLPath = path
	registry := testRegistry(t, cfg)
	result, err := registry.Execute(context.Background(), "ore_generation_lookup", json.RawMessage(`{"query":"Yttrium"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.OK || !strings.Contains(result.Stderr, "requires schema_version 2") {
		t.Fatalf("old schema was not rejected clearly: %+v", result)
	}
}

func oreGenerationTestRegistry(t *testing.T) *Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "greggpt_recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE manifest(key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO manifest VALUES ('schema_version', '2'), ('worldgen_data_available', '1');
		CREATE TABLE ore_materials(id INTEGER PRIMARY KEY, material_key TEXT UNIQUE, internal_name TEXT, localized_name TEXT);
		CREATE TABLE ore_veins(id INTEGER PRIMARY KEY, vein_key TEXT UNIQUE, internal_name TEXT, display_name TEXT, min_y INTEGER, max_y INTEGER, weight INTEGER, density INTEGER, size INTEGER);
		CREATE TABLE ore_vein_materials(vein_id INTEGER, role TEXT, material_id INTEGER);
		CREATE TABLE ore_vein_dimensions(vein_id INTEGER, dimension_key TEXT, dimension_name TEXT, min_y INTEGER, max_y INTEGER);
		CREATE TABLE small_ores(id INTEGER PRIMARY KEY, small_ore_key TEXT UNIQUE, internal_name TEXT, material_id INTEGER, min_y INTEGER, max_y INTEGER, amount_per_chunk INTEGER);
		CREATE TABLE small_ore_dimensions(small_ore_id INTEGER, dimension_key TEXT, dimension_name TEXT);
		INSERT INTO ore_materials VALUES
			(1, 'material:niobium', 'Niobium', 'Niobium'),
			(2, 'material:yttrium', 'Yttrium', 'Yttrium'),
			(3, 'material:barite', 'Barite', 'Barite'),
			(4, 'material:copper', 'Copper', 'Copper');
		INSERT INTO ore_veins VALUES (1, 'vein:niobium', 'Niobium', 'Niobium', 5, 30, 60, 2, 24);
		INSERT INTO ore_vein_materials VALUES (1, 'primary', 1), (1, 'secondary', 2), (1, 'between', 3);
		INSERT INTO ore_vein_dimensions VALUES (1, 'barnarda5', 'Barnard F', 5, 30), (1, 'triton', 'Triton', 5, 30);
		INSERT INTO small_ores VALUES (1, 'small_ore:copper', 'Copper', 4, 20, 80, 16);
		INSERT INTO small_ore_dimensions VALUES (1, 'overworld', 'Overworld');
		CREATE VIEW ore_generation_routes AS
		SELECT 'vein' AS generation_kind, v.vein_key AS source_key, v.display_name AS source_name,
		       m.material_key, m.localized_name AS material_name, vm.role, d.dimension_key, d.dimension_name,
		       d.min_y, d.max_y, v.weight, v.density, v.size, NULL AS amount_per_chunk
		FROM ore_veins v JOIN ore_vein_materials vm ON vm.vein_id=v.id
		JOIN ore_materials m ON m.id=vm.material_id JOIN ore_vein_dimensions d ON d.vein_id=v.id
		UNION ALL
		SELECT 'small_ore', s.small_ore_key, s.internal_name, m.material_key, m.localized_name,
		       'small', d.dimension_key, d.dimension_name, s.min_y, s.max_y, NULL, NULL, NULL, s.amount_per_chunk
		FROM small_ores s JOIN ore_materials m ON m.id=s.material_id
		JOIN small_ore_dimensions d ON d.small_ore_id=s.id;
	`); err != nil {
		t.Fatalf("seed worldgen sqlite: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Workspace = t.TempDir()
	cfg.RecipeSQLPath = path
	return testRegistry(t, cfg)
}
