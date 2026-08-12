#!/usr/bin/env bash
set -euo pipefail

DB_PATH="${1:-}"
if [[ -z "$DB_PATH" || ! -s "$DB_PATH" ]]; then
  echo "usage: scripts/migrate_recipe_db_v2.sh <greggpt_recipes.sqlite>" >&2
  exit 2
fi
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required" >&2
  exit 1
fi

has_column() {
  sqlite3 "$DB_PATH" "SELECT 1 FROM pragma_table_info('$1') WHERE name='$2' LIMIT 1;" | grep -qx 1
}

has_table() {
  sqlite3 "$DB_PATH" "SELECT 1 FROM sqlite_schema WHERE type='table' AND name='$1' LIMIT 1;" | grep -qx 1
}

add_column() {
  if ! has_column "$1" "$2"; then
    sqlite3 "$DB_PATH" "ALTER TABLE $1 ADD COLUMN $2 $3;"
  fi
}

add_column recipes fake "INTEGER NOT NULL DEFAULT 0"
add_column recipes enabled "INTEGER NOT NULL DEFAULT 1"
add_column recipes valid "INTEGER NOT NULL DEFAULT 1"
add_column recipe_inputs consumed "INTEGER NOT NULL DEFAULT 1"
add_column recipe_inputs catalyst "INTEGER NOT NULL DEFAULT 0"
add_column recipe_outputs is_primary "INTEGER NOT NULL DEFAULT 0"

# Early schema-v2 compatibility builds stored only the display name. Rebuild
# those link tables so queries can filter by the stable internal dimension key.
if has_table ore_vein_dimensions && ! has_column ore_vein_dimensions dimension_key; then
  sqlite3 "$DB_PATH" <<'SQL'
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;
DROP VIEW IF EXISTS ore_generation_routes;
ALTER TABLE ore_vein_dimensions RENAME TO ore_vein_dimensions_legacy;
CREATE TABLE ore_vein_dimensions (
  vein_id INTEGER NOT NULL,
  dimension_key TEXT NOT NULL,
  dimension_name TEXT NOT NULL,
  min_y INTEGER NOT NULL,
  max_y INTEGER NOT NULL,
  PRIMARY KEY(vein_id, dimension_key),
  FOREIGN KEY(vein_id) REFERENCES ore_veins(id)
);
INSERT INTO ore_vein_dimensions(vein_id, dimension_key, dimension_name, min_y, max_y)
SELECT vein_id, dimension_name, dimension_name, min_y, max_y
FROM ore_vein_dimensions_legacy;
DROP TABLE ore_vein_dimensions_legacy;
COMMIT;
SQL
fi

if has_table small_ore_dimensions && ! has_column small_ore_dimensions dimension_key; then
  sqlite3 "$DB_PATH" <<'SQL'
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;
DROP VIEW IF EXISTS ore_generation_routes;
ALTER TABLE small_ore_dimensions RENAME TO small_ore_dimensions_legacy;
CREATE TABLE small_ore_dimensions (
  small_ore_id INTEGER NOT NULL,
  dimension_key TEXT NOT NULL,
  dimension_name TEXT NOT NULL,
  PRIMARY KEY(small_ore_id, dimension_key),
  FOREIGN KEY(small_ore_id) REFERENCES small_ores(id)
);
INSERT INTO small_ore_dimensions(small_ore_id, dimension_key, dimension_name)
SELECT small_ore_id, dimension_name, dimension_name
FROM small_ore_dimensions_legacy;
DROP TABLE small_ore_dimensions_legacy;
COMMIT;
SQL
fi

echo "migrating recipe rows, indexes, views, and compatibility worldgen tables"
sqlite3 "$DB_PATH" <<'SQL'
BEGIN IMMEDIATE;

CREATE INDEX IF NOT EXISTS idx_recipe_metadata_recipe_key ON recipe_metadata(recipe_id, key);
UPDATE recipe_outputs
SET chance = COALESCE(
  (SELECT CAST(value AS INTEGER)
   FROM recipe_metadata
   WHERE recipe_metadata.recipe_id = recipe_outputs.recipe_id
     AND recipe_metadata.key = 'output_chance_' || recipe_outputs.position
   ORDER BY recipe_metadata.id DESC
   LIMIT 1),
  chance,
  10000
);
UPDATE recipes
SET fake = CASE
  WHEN hidden <> 0 THEN fake
  WHEN handler_id IN (SELECT id FROM recipe_handlers WHERE lower(name) LIKE '%fake%') THEN 1
  ELSE fake
END;
UPDATE recipes
SET valid = CASE
  WHEN EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id = recipes.id)
   AND EXISTS (SELECT 1 FROM recipe_outputs ro WHERE ro.recipe_id = recipes.id)
  THEN 1 ELSE 0 END;

-- GregTech represents reusable tools, molds, shapes, and programmed circuits
-- as zero-sized item stacks. Normalize that implementation detail into a
-- one-item presence requirement which is not consumed.
UPDATE recipe_inputs
SET amount=1, consumed=0, catalyst=1
WHERE amount=0
  AND recipe_id IN (SELECT id FROM recipes WHERE category='gregtech');
UPDATE recipe_input_options
SET amount=1
WHERE amount=0
  AND input_id IN (
    SELECT ri.id
    FROM recipe_inputs ri
    JOIN recipes r ON r.id=ri.recipe_id
    WHERE r.category='gregtech' AND ri.catalyst=1
  );

-- Legacy dumps numbered item and fluid arrays independently from zero. Shift
-- every fluid group only when a recipe still has a colliding item position so
-- the migration is idempotent and each position represents one requirement.
UPDATE recipe_inputs AS target
SET position = position + (
  SELECT COALESCE(max(item_input.position), -1) + 1
  FROM recipe_inputs AS item_input
  WHERE item_input.recipe_id=target.recipe_id AND item_input.kind<>'fluid'
)
WHERE target.kind='fluid'
  AND EXISTS (
    SELECT 1
    FROM recipe_inputs AS item_input
    JOIN recipe_inputs AS fluid_input
      ON fluid_input.recipe_id=item_input.recipe_id
     AND fluid_input.position=item_input.position
     AND fluid_input.kind='fluid'
    WHERE item_input.recipe_id=target.recipe_id AND item_input.kind<>'fluid'
  );
UPDATE recipe_outputs AS target
SET position = position + (
  SELECT COALESCE(max(item_output.position), -1) + 1
  FROM recipe_outputs AS item_output
  WHERE item_output.recipe_id=target.recipe_id AND item_output.kind<>'fluid'
)
WHERE target.kind='fluid'
  AND EXISTS (
    SELECT 1
    FROM recipe_outputs AS item_output
    JOIN recipe_outputs AS fluid_output
      ON fluid_output.recipe_id=item_output.recipe_id
     AND fluid_output.position=item_output.position
     AND fluid_output.kind='fluid'
    WHERE item_output.recipe_id=target.recipe_id AND item_output.kind<>'fluid'
  );
UPDATE recipe_outputs SET is_primary = CASE WHEN position = 0 THEN 1 ELSE 0 END;

CREATE TABLE IF NOT EXISTS machine_capabilities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  handler_id INTEGER NOT NULL UNIQUE,
  capability_key TEXT NOT NULL,
  machine_name_hint TEXT NOT NULL,
  FOREIGN KEY(handler_id) REFERENCES recipe_handlers(id)
);
CREATE TABLE IF NOT EXISTS machine_options (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  capability_id INTEGER NOT NULL,
  item_id INTEGER,
  block_registry_name TEXT,
  block_meta INTEGER,
  display_name TEXT NOT NULL,
  tier_name TEXT,
  min_eut INTEGER,
  max_eut INTEGER,
  source TEXT NOT NULL,
  FOREIGN KEY(capability_id) REFERENCES machine_capabilities(id),
  FOREIGN KEY(item_id) REFERENCES items(id)
);
CREATE TABLE IF NOT EXISTS dump_errors (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  category TEXT NOT NULL,
  recipe_key TEXT,
  handler_name TEXT,
  message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS recipe_edges (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  recipe_id INTEGER NOT NULL,
  direction TEXT NOT NULL CHECK(direction IN ('input','output')),
  position INTEGER NOT NULL,
  option_index INTEGER,
  resource_kind TEXT NOT NULL,
  resource_key TEXT NOT NULL,
  resource_name TEXT,
  item_id INTEGER,
  fluid_id INTEGER,
  ore_name TEXT,
  amount INTEGER,
  chance INTEGER NOT NULL DEFAULT 10000,
  expected_amount REAL,
  is_primary INTEGER NOT NULL DEFAULT 0,
  consumed INTEGER NOT NULL DEFAULT 1,
  catalyst INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(recipe_id) REFERENCES recipes(id),
  FOREIGN KEY(item_id) REFERENCES items(id),
  FOREIGN KEY(fluid_id) REFERENCES fluids(id)
);
CREATE TABLE IF NOT EXISTS ore_materials (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  material_key TEXT NOT NULL UNIQUE,
  internal_name TEXT NOT NULL UNIQUE,
  localized_name TEXT
);
CREATE TABLE IF NOT EXISTS ore_veins (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  vein_key TEXT NOT NULL,
  internal_name TEXT NOT NULL,
  display_name TEXT NOT NULL,
  min_y INTEGER NOT NULL,
  max_y INTEGER NOT NULL,
  weight INTEGER NOT NULL,
  density INTEGER NOT NULL,
  size INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS ore_vein_materials (
  vein_id INTEGER NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('primary','secondary','between','sporadic')),
  material_id INTEGER NOT NULL,
  PRIMARY KEY(vein_id, role),
  FOREIGN KEY(vein_id) REFERENCES ore_veins(id),
  FOREIGN KEY(material_id) REFERENCES ore_materials(id)
);
CREATE TABLE IF NOT EXISTS ore_vein_dimensions (
  vein_id INTEGER NOT NULL,
  dimension_key TEXT NOT NULL,
  dimension_name TEXT NOT NULL,
  min_y INTEGER NOT NULL,
  max_y INTEGER NOT NULL,
  PRIMARY KEY(vein_id, dimension_key),
  FOREIGN KEY(vein_id) REFERENCES ore_veins(id)
);
CREATE TABLE IF NOT EXISTS small_ores (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  small_ore_key TEXT NOT NULL,
  internal_name TEXT NOT NULL,
  material_id INTEGER NOT NULL,
  min_y INTEGER NOT NULL,
  max_y INTEGER NOT NULL,
  amount_per_chunk INTEGER NOT NULL,
  FOREIGN KEY(material_id) REFERENCES ore_materials(id)
);
CREATE TABLE IF NOT EXISTS small_ore_dimensions (
  small_ore_id INTEGER NOT NULL,
  dimension_key TEXT NOT NULL,
  dimension_name TEXT NOT NULL,
  PRIMARY KEY(small_ore_id, dimension_key),
  FOREIGN KEY(small_ore_id) REFERENCES small_ores(id)
);
INSERT OR IGNORE INTO machine_capabilities(handler_id, capability_key, machine_name_hint)
SELECT id,
       lower(replace(replace(replace(name, 'gt.recipe.', ''), '.', '_'), ' ', '_')),
       replace(replace(name, 'gt.recipe.', ''), '_', ' ')
FROM recipe_handlers;

DELETE FROM recipe_edges;
DELETE FROM sqlite_sequence WHERE name='recipe_edges';
INSERT INTO recipe_edges(
  recipe_id, direction, position, option_index, resource_kind, resource_key,
  resource_name, item_id, fluid_id, ore_name, amount, chance, expected_amount,
  is_primary, consumed, catalyst
)
SELECT ro.recipe_id, 'output', ro.position, NULL, ro.kind,
       CASE WHEN ro.item_id IS NOT NULL THEN 'item:' || i.registry_name || ':' || i.damage
            WHEN ro.fluid_id IS NOT NULL THEN 'fluid:' || f.fluid_name
            ELSE ro.kind || ':' || COALESCE(ro.label, '') END,
       COALESCE(i.display_name, f.localized_name, f.fluid_name, ro.label),
       ro.item_id, ro.fluid_id, NULL, ro.amount, ro.chance,
       CAST(ro.amount AS REAL) * ro.chance / 10000.0, ro.is_primary, 0, 0
FROM recipe_outputs ro
LEFT JOIN items i ON i.id=ro.item_id
LEFT JOIN fluids f ON f.id=ro.fluid_id;
INSERT INTO recipe_edges(
  recipe_id, direction, position, option_index, resource_kind, resource_key,
  resource_name, item_id, fluid_id, ore_name, amount, chance, expected_amount,
  is_primary, consumed, catalyst
)
SELECT ri.recipe_id, 'input', ri.position, rio.option_index,
       COALESCE(rio.kind, ri.kind),
       CASE WHEN rio.item_id IS NOT NULL THEN 'item:' || i.registry_name || ':' || i.damage
            WHEN rio.fluid_id IS NOT NULL THEN 'fluid:' || f.fluid_name
            WHEN rio.ore_name IS NOT NULL THEN 'oredict:' || rio.ore_name
            ELSE COALESCE(rio.kind, ri.kind) || ':' || COALESCE(rio.label, ri.label, '') END,
       COALESCE(i.display_name, f.localized_name, f.fluid_name, rio.ore_name, rio.label, ri.label),
       rio.item_id, rio.fluid_id, rio.ore_name, COALESCE(rio.amount, ri.amount),
       10000, COALESCE(rio.amount, ri.amount), 0, ri.consumed, ri.catalyst
FROM recipe_inputs ri
LEFT JOIN recipe_input_options rio ON rio.input_id=ri.id
LEFT JOIN items i ON i.id=rio.item_id
LEFT JOIN fluids f ON f.id=rio.fluid_id;

CREATE INDEX IF NOT EXISTS idx_items_display_name ON items(display_name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_fluids_localized_name ON fluids(localized_name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_recipes_usable ON recipes(valid, hidden, fake, enabled);
CREATE INDEX IF NOT EXISTS idx_recipe_input_options_input ON recipe_input_options(input_id);
CREATE INDEX IF NOT EXISTS idx_recipe_input_options_item ON recipe_input_options(item_id);
CREATE INDEX IF NOT EXISTS idx_recipe_input_options_fluid ON recipe_input_options(fluid_id);
CREATE INDEX IF NOT EXISTS idx_recipe_input_options_ore ON recipe_input_options(ore_name);
CREATE INDEX IF NOT EXISTS idx_recipe_outputs_item ON recipe_outputs(item_id);
CREATE INDEX IF NOT EXISTS idx_recipe_outputs_fluid ON recipe_outputs(fluid_id);
CREATE INDEX IF NOT EXISTS idx_recipe_edges_resource ON recipe_edges(direction, resource_key);
CREATE INDEX IF NOT EXISTS idx_recipe_edges_recipe ON recipe_edges(recipe_id, direction, position, option_index);
CREATE UNIQUE INDEX IF NOT EXISTS idx_recipe_edges_unique_position ON recipe_edges(recipe_id, direction, position, COALESCE(option_index, -1));
DROP INDEX IF EXISTS idx_recipe_edges_item;
DROP INDEX IF EXISTS idx_recipe_edges_fluid;
CREATE INDEX IF NOT EXISTS idx_machine_options_capability ON machine_options(capability_id);
CREATE INDEX IF NOT EXISTS idx_ore_materials_internal_name ON ore_materials(internal_name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_ore_materials_localized_name ON ore_materials(localized_name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_ore_veins_key ON ore_veins(vein_key);
CREATE INDEX IF NOT EXISTS idx_ore_veins_name ON ore_veins(display_name COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_ore_vein_materials_material ON ore_vein_materials(material_id, vein_id);
CREATE INDEX IF NOT EXISTS idx_ore_vein_dimensions_key ON ore_vein_dimensions(dimension_key, vein_id);
CREATE INDEX IF NOT EXISTS idx_ore_vein_dimensions_dimension ON ore_vein_dimensions(dimension_name COLLATE NOCASE, vein_id);
CREATE INDEX IF NOT EXISTS idx_small_ores_material ON small_ores(material_id);
CREATE INDEX IF NOT EXISTS idx_small_ores_key ON small_ores(small_ore_key);
CREATE INDEX IF NOT EXISTS idx_small_ore_dimensions_key ON small_ore_dimensions(dimension_key, small_ore_id);
CREATE INDEX IF NOT EXISTS idx_small_ore_dimensions_dimension ON small_ore_dimensions(dimension_name COLLATE NOCASE, small_ore_id);

DROP VIEW IF EXISTS resource_catalog;
CREATE VIEW resource_catalog AS
SELECT 'item' AS resource_kind, id AS resource_id,
       'item:' || registry_name || ':' || damage AS resource_key,
       display_name AS resource_name, registry_name, damage, NULL AS fluid_name
FROM items
UNION ALL
SELECT 'fluid', id, 'fluid:' || fluid_name,
       COALESCE(localized_name, fluid_name), NULL, NULL, fluid_name
FROM fluids;

DROP VIEW IF EXISTS recipe_routes;
CREATE VIEW recipe_routes AS
SELECT r.id AS recipe_id, r.recipe_key, r.category, h.name AS handler_name,
       mc.capability_key, mc.machine_name_hint, r.duration_ticks, r.eut,
       CASE WHEN r.eut IS NULL THEN NULL
            WHEN abs(r.eut)<=8 THEN 'ULV' WHEN abs(r.eut)<=32 THEN 'LV'
            WHEN abs(r.eut)<=128 THEN 'MV' WHEN abs(r.eut)<=512 THEN 'HV'
            WHEN abs(r.eut)<=2048 THEN 'EV' WHEN abs(r.eut)<=8192 THEN 'IV'
            WHEN abs(r.eut)<=32768 THEN 'LuV' WHEN abs(r.eut)<=131072 THEN 'ZPM'
            WHEN abs(r.eut)<=524288 THEN 'UV' ELSE 'UHV+' END AS voltage_tier,
       e.position AS output_position, e.resource_kind AS output_kind,
       e.resource_key AS output_resource_key, e.resource_name AS output_name,
       i.registry_name, i.damage, f.fluid_name, e.amount AS output_amount,
       e.chance, e.expected_amount AS expected_output_amount, e.is_primary
FROM recipe_edges e
JOIN recipes r ON r.id=e.recipe_id
JOIN recipe_handlers h ON h.id=r.handler_id
LEFT JOIN machine_capabilities mc ON mc.handler_id=h.id
LEFT JOIN items i ON i.id=e.item_id
LEFT JOIN fluids f ON f.id=e.fluid_id
WHERE e.direction='output' AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1
  AND NOT EXISTS (
    SELECT 1
    FROM recipe_inputs ri
    WHERE ri.recipe_id=r.id
      AND NOT EXISTS (
        SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id
      )
  );

DROP VIEW IF EXISTS recipe_ingredients;
CREATE VIEW recipe_ingredients AS
SELECT e.recipe_id, e.position AS input_position, e.option_index,
       e.resource_kind AS input_kind, e.resource_key AS input_resource_key,
       COALESCE(NULLIF(trim(e.resource_name), ''), e.resource_key) AS input_name,
       i.registry_name, i.damage, f.fluid_name,
       e.ore_name, e.amount AS input_amount, e.consumed, e.catalyst
FROM recipe_edges e
JOIN recipes r ON r.id=e.recipe_id
LEFT JOIN items i ON i.id=e.item_id
LEFT JOIN fluids f ON f.id=e.fluid_id
WHERE e.direction='input'
  AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1
  AND e.resource_key NOT IN ('item:', 'fluid:', 'oredict:')
  AND NOT EXISTS (
    SELECT 1
    FROM recipe_inputs ri
    WHERE ri.recipe_id=r.id
      AND NOT EXISTS (
        SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id
      )
  );

DROP VIEW IF EXISTS handler_machine_options;
CREATE VIEW handler_machine_options AS
SELECT mc.id AS capability_id, mc.handler_id, h.name AS handler_name,
       mc.capability_key, mc.machine_name_hint, mo.item_id,
       i.registry_name AS item_registry_name, i.damage AS item_damage,
       mo.block_registry_name, mo.block_meta, mo.display_name, mo.tier_name,
       mo.min_eut, mo.max_eut, mo.source
FROM machine_capabilities mc
JOIN recipe_handlers h ON h.id=mc.handler_id
LEFT JOIN machine_options mo ON mo.capability_id=mc.id
LEFT JOIN items i ON i.id=mo.item_id;

DROP VIEW IF EXISTS recipe_data_quality;
CREATE VIEW recipe_data_quality AS
SELECT r.id AS recipe_id, r.recipe_key, h.name AS handler_name, r.category,
       r.valid, r.hidden, r.fake, r.enabled,
       (SELECT count(*) FROM recipe_inputs ri WHERE ri.recipe_id=r.id) AS input_count,
       (SELECT count(*) FROM recipe_outputs ro WHERE ro.recipe_id=r.id) AS output_count,
       (SELECT count(*) FROM recipe_inputs ri
        LEFT JOIN recipe_input_options rio ON rio.input_id=ri.id
        WHERE ri.recipe_id=r.id AND rio.id IS NULL) AS optionless_input_count
FROM recipes r JOIN recipe_handlers h ON h.id=r.handler_id;

DROP VIEW IF EXISTS ore_generation_routes;
CREATE VIEW ore_generation_routes AS
SELECT 'vein' AS generation_kind, v.vein_key AS source_key,
       v.display_name AS source_name, m.material_key,
       COALESCE(m.localized_name, m.internal_name) AS material_name,
       vm.role, d.dimension_key, d.dimension_name, d.min_y, d.max_y,
       v.weight, v.density, v.size, NULL AS amount_per_chunk
FROM ore_veins v
JOIN ore_vein_materials vm ON vm.vein_id=v.id
JOIN ore_materials m ON m.id=vm.material_id
JOIN ore_vein_dimensions d ON d.vein_id=v.id
UNION ALL
SELECT 'small_ore', s.small_ore_key, s.internal_name, m.material_key,
       COALESCE(m.localized_name, m.internal_name), 'small', d.dimension_key, d.dimension_name,
       s.min_y, s.max_y, NULL, NULL, NULL, s.amount_per_chunk
FROM small_ores s
JOIN ore_materials m ON m.id=s.material_id
JOIN small_ore_dimensions d ON d.small_ore_id=s.id;

INSERT OR REPLACE INTO manifest(key, value) VALUES ('schema_version', '2');
INSERT OR REPLACE INTO manifest(key, value) VALUES ('migrated_from_schema_version', '1');
INSERT OR REPLACE INTO manifest(key, value) VALUES ('crafting_recipe_count', (SELECT CAST(count(*) AS TEXT) FROM recipes WHERE category='crafting'));
INSERT OR REPLACE INTO manifest(key, value) VALUES ('furnace_recipe_count', (SELECT CAST(count(*) AS TEXT) FROM recipes WHERE category='furnace'));
-- A migration cannot prove that every required dump pass completed without
-- record errors. Only a fresh dump can set this authoritative flag to one.
INSERT OR REPLACE INTO manifest(key, value) VALUES ('dump_complete', '0');
INSERT OR REPLACE INTO manifest(key, value) VALUES ('worldgen_data_available', CASE WHEN EXISTS (SELECT 1 FROM ore_veins) THEN '1' ELSE '0' END);

COMMIT;

.print rebuilding item search index
CREATE VIRTUAL TABLE IF NOT EXISTS item_search USING fts5(
  display_name, registry_name, unlocalized_name,
  content='items', content_rowid='id'
);
INSERT INTO item_search(item_search) VALUES('delete-all');
INSERT INTO item_search(rowid, display_name, registry_name, unlocalized_name)
SELECT id, display_name, registry_name, unlocalized_name FROM items;
.print analyzing query indexes
ANALYZE;
SQL

sqlite3 "$DB_PATH" "PRAGMA integrity_check;" | grep -qx ok
echo "migrated recipe database to schema v2: $DB_PATH"
