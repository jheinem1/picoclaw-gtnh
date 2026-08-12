package greggpttools

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeSearchSchemaPatchRejectsIncompleteRoutesAndKeepsWildcardInputs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "recipes.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE manifest(key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO manifest VALUES ('schema_version', '2');
		CREATE TABLE items(id INTEGER PRIMARY KEY, registry_name TEXT, damage INTEGER);
		INSERT INTO items VALUES
			(1, 'Forestry:beeCombs', 32767),
			(2, 'test:output', 0),
			(3, 'gregtech:gt.metaitem.01', 32306);
		CREATE TABLE fluids(id INTEGER PRIMARY KEY, fluid_name TEXT);
		CREATE TABLE recipe_handlers(id INTEGER PRIMARY KEY, name TEXT);
		INSERT INTO recipe_handlers VALUES (1, 'test.handler');
		CREATE TABLE machine_capabilities(handler_id INTEGER UNIQUE, capability_key TEXT, machine_name_hint TEXT);
		CREATE TABLE recipes(id INTEGER PRIMARY KEY, handler_id INTEGER, recipe_key TEXT, category TEXT,
			duration_ticks INTEGER, eut INTEGER, valid INTEGER, hidden INTEGER, fake INTEGER, enabled INTEGER);
		INSERT INTO recipes VALUES
			(1, 1, 'complete', 'crafting', NULL, NULL, 1, 0, 0, 1),
			(2, 1, 'incomplete', 'crafting', NULL, NULL, 1, 0, 0, 1),
			(3, 1, 'reusable-mold', 'gregtech', 20, 8, 1, 0, 0, 1);
		CREATE TABLE recipe_inputs(
			id INTEGER PRIMARY KEY, recipe_id INTEGER, amount INTEGER,
			consumed INTEGER, catalyst INTEGER);
		INSERT INTO recipe_inputs VALUES (1, 1, 1, 1, 0), (2, 2, 1, 1, 0), (3, 3, 0, 1, 0);
		CREATE TABLE recipe_input_options(id INTEGER PRIMARY KEY, input_id INTEGER, amount INTEGER);
		INSERT INTO recipe_input_options VALUES (1, 1, 1), (2, 3, 0);
		CREATE TABLE recipe_edges(
			recipe_id INTEGER, direction TEXT, position INTEGER, option_index INTEGER,
			resource_kind TEXT, resource_key TEXT, resource_name TEXT, item_id INTEGER,
			fluid_id INTEGER, ore_name TEXT, amount INTEGER, chance INTEGER,
			expected_amount REAL, is_primary INTEGER, consumed INTEGER, catalyst INTEGER);
		INSERT INTO recipe_edges VALUES
			(1, 'input', 0, 0, 'item', 'item:Forestry:beeCombs:32767', '', 1, NULL, NULL, 1, 10000, 1, 0, 1, 0),
			(1, 'output', 0, NULL, 'item', 'item:test:output:0', 'Output', 2, NULL, NULL, 1, 10000, 1, 1, 0, 0),
			(2, 'input', 0, NULL, 'unknown', 'unknown:java.lang.Integer', 'java.lang.Integer', NULL, NULL, NULL, 1, 10000, 1, 0, 1, 0),
			(2, 'output', 0, NULL, 'item', 'item:test:output:0', 'Output', 2, NULL, NULL, 1, 10000, 1, 1, 0, 0),
			(3, 'input', 0, 0, 'item', 'item:gregtech:gt.metaitem.01:32306', 'Mold (Ingot)', 3, NULL, NULL, 0, 10000, 0, 0, 1, 0),
			(3, 'output', 0, NULL, 'item', 'item:test:output:0', 'Output', 2, NULL, NULL, 1, 10000, 1, 1, 0, 0);
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join("..", "..", "scripts", "patch_recipe_search_schema.sh")
	cmd := exec.Command("bash", script, dbPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("patch schema: %v\n%s", err, output)
	}
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT count(*) FROM recipe_routes WHERE recipe_id=2").Scan(&count); err != nil || count != 0 {
		t.Fatalf("incomplete route count=%d err=%v, want 0", count, err)
	}
	var name string
	if err := db.QueryRow("SELECT input_name FROM recipe_ingredients WHERE recipe_id=1").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "item:Forestry:beeCombs:32767" {
		t.Fatalf("wildcard input_name=%q", name)
	}
	if err := db.QueryRow("SELECT count(*) FROM recipe_ingredients WHERE recipe_id=2").Scan(&count); err != nil || count != 0 {
		t.Fatalf("incomplete ingredient count=%d err=%v, want 0", count, err)
	}
	var amount, consumed, catalyst int
	if err := db.QueryRow(
		"SELECT input_amount, consumed, catalyst FROM recipe_ingredients WHERE recipe_id=3",
	).Scan(&amount, &consumed, &catalyst); err != nil {
		t.Fatal(err)
	}
	if amount != 1 || consumed != 0 || catalyst != 1 {
		t.Fatalf("reusable mold semantics amount=%d consumed=%d catalyst=%d", amount, consumed, catalyst)
	}
	if err := db.QueryRow(
		"SELECT amount FROM recipe_input_options WHERE input_id=3",
	).Scan(&amount); err != nil || amount != 1 {
		t.Fatalf("normalized input option amount=%d err=%v", amount, err)
	}
}

func TestRecipeMigrationKeepsDumpCompleteConservative(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "migrate_recipe_db_v2.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "VALUES ('dump_complete', '0')") || strings.Contains(text, "'dump_complete', CASE WHEN EXISTS") {
		t.Fatal("migration must not infer dump completeness from partial row counts")
	}
}
