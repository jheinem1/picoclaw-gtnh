package greggpttools

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRecipeSQLExecutesReadOnlySelect(t *testing.T) {
	dbPath := createRecipeSQLTestDB(t)
	cfg := DefaultConfig()
	cfg.Workspace = t.TempDir()
	cfg.RecipeSQLPath = dbPath
	registry := testRegistry(t, cfg)

	result, err := registry.Execute(context.Background(), "recipe_sql", json.RawMessage(`{"sql":"SELECT handler_name, output FROM recipes ORDER BY id","max_rows":1}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("result not OK: %+v", result)
	}
	if !result.Truncated {
		t.Fatalf("expected row truncation, got %+v", result)
	}
	if !strings.Contains(result.Stdout, `"handler_name":"Assembler"`) {
		t.Fatalf("stdout missing first row: %s", result.Stdout)
	}
	if strings.Contains(result.Stdout, `"Cutter"`) {
		t.Fatalf("stdout included row past max_rows: %s", result.Stdout)
	}
}

func TestRecipeSQLRejectsUnsafeSQL(t *testing.T) {
	registry := testRegistry(t, DefaultConfig())
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"semicolon", "SELECT * FROM recipes; SELECT * FROM recipes", "no semicolons"},
		{"insert", "INSERT INTO recipes(output) VALUES ('x')", "only allows SELECT"},
		{"update in cte", "WITH changed AS (UPDATE recipes SET output='x' RETURNING id) SELECT id FROM changed", "rejects write"},
		{"pragma", "PRAGMA table_info(recipes)", "only allows SELECT"},
		{"pragma after select", "SELECT * FROM pragma_table_info('recipes')", "rejects write"},
		{"attach", "ATTACH DATABASE '/tmp/other.sqlite' AS other", "only allows SELECT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := registry.Execute(context.Background(), "recipe_sql", mustJSON(t, map[string]any{"sql": tt.sql}))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func createRecipeSQLTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "greggpt_recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE recipes (
			id INTEGER PRIMARY KEY,
			handler_name TEXT NOT NULL,
			output TEXT NOT NULL
		);
		INSERT INTO recipes(handler_name, output) VALUES
			('Assembler', 'Bronze Fluid Pipe'),
			('Cutter', 'Potin Plate');
	`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	return path
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}
