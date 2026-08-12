package greggpttools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"database/sql"
	_ "modernc.org/sqlite"
)

func TestCompareRecipeTargetsBuildsBoundedSynthesisAndFinishingPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE items(id INTEGER PRIMARY KEY, registry_name TEXT, damage INTEGER, display_name TEXT);
		CREATE TABLE recipe_handlers(id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE machine_capabilities(handler_id INTEGER, capability_key TEXT, machine_name_hint TEXT);
		CREATE TABLE recipes(id INTEGER PRIMARY KEY, handler_id INTEGER, duration_ticks INTEGER, eut INTEGER, valid INTEGER, hidden INTEGER, fake INTEGER, enabled INTEGER);
		CREATE TABLE recipe_inputs(id INTEGER PRIMARY KEY, recipe_id INTEGER);
		CREATE TABLE recipe_input_options(id INTEGER PRIMARY KEY, input_id INTEGER);
		CREATE TABLE recipe_edges(recipe_id INTEGER, direction TEXT, position INTEGER, option_index INTEGER, resource_kind TEXT, resource_key TEXT, resource_name TEXT, item_id INTEGER, amount REAL, chance INTEGER, expected_amount REAL, is_primary INTEGER, consumed INTEGER, catalyst INTEGER);

		INSERT INTO items VALUES
		 (1,'enderio:alloy',1,'Energetic Alloy'),
		 (2,'gt:meta',11366,'Energetic Alloy Ingot'),
		 (3,'gt:meta',2366,'Energetic Alloy Dust'),
		 (4,'enderio:alloy',2,'Vibrant Alloy'),
		 (5,'gt:meta',11367,'Vibrant Alloy Ingot'),
		 (6,'gt:meta',10367,'Hot Vibrant Alloy Ingot'),
		 (7,'gt:meta',2367,'Vibrant Alloy Dust'),
		 (8,'gt:meta',1,'Conductive Iron Dust'),
		 (9,'gt:meta',2,'Gold Dust'),
		 (10,'gt:meta',3,'Black Steel Dust'),
		 (11,'gt:meta',4,'Endereye Dust'),
		 (12,'gt:meta',5,'Chrome Dust');

		INSERT INTO recipe_handlers VALUES
		 (1,'gt.recipe.mixer'),
		 (2,'gt.recipe.blastfurnace'),
		 (3,'gt.recipe.vacuumfreezer');
		INSERT INTO machine_capabilities VALUES
		 (1,'mixer','mixer'),
		 (1,'mixer.secondary','secondary mixer'),
		 (2,'blastfurnace','blastfurnace'),
		 (3,'vacuumfreezer','vacuumfreezer');
		INSERT INTO recipes VALUES
		 (101,1,100,7,1,0,0,1),
		 (102,2,1600,120,1,0,0,1),
		 (201,1,100,7,1,0,0,1),
		 (202,2,3000,120,1,0,0,1),
		 (203,3,100,480,1,0,0,1);

		INSERT INTO recipe_inputs VALUES
		 (1,101),(2,101),(3,101),(4,102),(5,201),(6,201),(7,201),(8,202),(9,203);
		INSERT INTO recipe_input_options VALUES
		 (1,1),(2,2),(3,3),(4,4),(5,5),(6,6),(7,7),(8,8),(9,9);

		INSERT INTO recipe_edges VALUES
		 (101,'output',0,NULL,'item','item:gt:meta:2366','Energetic Alloy Dust',3,3,10000,3,1,0,0),
		 (102,'output',0,NULL,'item','item:gt:meta:11366','Energetic Alloy Ingot',2,1,10000,1,1,0,0),
		 (201,'output',0,NULL,'item','item:gt:meta:2367','Vibrant Alloy Dust',7,3,10000,3,1,0,0),
		 (202,'output',0,NULL,'item','item:gt:meta:10367','Hot Vibrant Alloy Ingot',6,1,10000,1,1,0,0),
		 (203,'output',0,NULL,'item','item:gt:meta:11367','Vibrant Alloy Ingot',5,1,10000,1,1,0,0),
		 (101,'input',0,0,'item','item:gt:meta:1','Conductive Iron Dust',8,1,10000,1,0,1,0),
		 (101,'input',1,0,'item','item:gt:meta:2','Gold Dust',9,1,10000,1,0,1,0),
		 (101,'input',2,0,'item','item:gt:meta:3','Black Steel Dust',10,1,10000,1,0,1,0),
		 (102,'input',0,0,'item','item:gt:meta:2366','Energetic Alloy Dust',3,1,10000,1,0,1,0),
		 (201,'input',0,0,'item','item:gt:meta:2366','Energetic Alloy Dust',3,1,10000,1,0,1,0),
		 (201,'input',1,0,'item','item:gt:meta:4','Endereye Dust',11,1,10000,1,0,1,0),
		 (201,'input',2,0,'item','item:gt:meta:5','Chrome Dust',12,1,10000,1,0,1,0),
		 (202,'input',0,0,'item','item:gt:meta:2367','Vibrant Alloy Dust',7,1,10000,1,0,1,0),
		 (203,'input',0,0,'item','item:gt:meta:10367','Hot Vibrant Alloy Ingot',6,1,10000,1,0,1,0);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := compareRecipeTargets(context.Background(), path, 2*time.Second, 100_000, "Energetic Alloy", "Vibrant Alloy", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	var payload struct {
		Targets []compareTarget `json:"targets"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Targets) != 2 {
		t.Fatalf("targets = %d", len(payload.Targets))
	}
	energetic := payload.Targets[0]
	if energetic.Resolved.Name != "Energetic Alloy Ingot" {
		t.Fatalf("resolved energetic = %q", energetic.Resolved.Name)
	}
	if len(energetic.SynthesisRoutes) != 1 || energetic.SynthesisRoutes[0].RecipeID != 101 {
		t.Fatalf("energetic synthesis = %+v", energetic.SynthesisRoutes)
	}
	if len(energetic.FinishingChain) != 1 || energetic.FinishingChain[0].RecipeID != 102 {
		t.Fatalf("energetic finishing = %+v", energetic.FinishingChain)
	}
	vibrant := payload.Targets[1]
	if vibrant.Resolved.Name != "Vibrant Alloy Ingot" {
		t.Fatalf("resolved vibrant = %q", vibrant.Resolved.Name)
	}
	if len(vibrant.SynthesisRoutes) != 1 || vibrant.SynthesisRoutes[0].RecipeID != 201 {
		t.Fatalf("vibrant synthesis = %+v", vibrant.SynthesisRoutes)
	}
	if len(vibrant.FinishingChain) != 2 || vibrant.FinishingChain[0].RecipeID != 202 || vibrant.FinishingChain[1].RecipeID != 203 {
		t.Fatalf("vibrant finishing = %+v", vibrant.FinishingChain)
	}
}
