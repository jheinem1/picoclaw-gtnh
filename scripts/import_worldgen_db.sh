#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DB="${1:-${SOURCE_DB:-}}"
DEST_DB="${2:-$ROOT/data/gtnh/index/greggpt_recipes.sqlite}"

if [[ -z "$SOURCE_DB" || ! -s "$SOURCE_DB" ]]; then
  echo "usage: scripts/import_worldgen_db.sh <fresh-schema-v2-db> [target-schema-v2-db]" >&2
  exit 2
fi
if [[ ! -s "$DEST_DB" ]]; then
  echo "missing target recipe/worldgen database: $DEST_DB" >&2
  exit 1
fi
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required" >&2
  exit 1
fi

source_version="$(sqlite3 "$SOURCE_DB" "SELECT value FROM manifest WHERE key='schema_version';")"
source_available="$(sqlite3 "$SOURCE_DB" "SELECT value FROM manifest WHERE key='worldgen_data_available';")"
target_version="$(sqlite3 "$DEST_DB" "SELECT value FROM manifest WHERE key='schema_version';")"
if [[ "$source_version" != "2" || "$source_available" != "1" ]]; then
  echo "source must be a schema-v2 dump with verified worldgen data" >&2
  exit 1
fi
if [[ "$target_version" != "2" ]]; then
  echo "target must be migrated to schema version 2 before importing worldgen data" >&2
  exit 1
fi

source_uri="file:$SOURCE_DB?mode=ro"
source_uri="${source_uri//\'/\'\'}"
mkdir -p "$(dirname "$DEST_DB")"
tmp="$(mktemp "$DEST_DB.worldgen.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
cp -f "$DEST_DB" "$tmp"

sqlite3 "$tmp" <<SQL
PRAGMA foreign_keys=OFF;
ATTACH DATABASE '$source_uri' AS fresh;
BEGIN IMMEDIATE;
DROP VIEW IF EXISTS ore_generation_routes;
DROP TABLE IF EXISTS small_ore_dimensions;
DROP TABLE IF EXISTS small_ores;
DROP TABLE IF EXISTS ore_vein_dimensions;
DROP TABLE IF EXISTS ore_vein_materials;
DROP TABLE IF EXISTS ore_veins;
DROP TABLE IF EXISTS ore_materials;

CREATE TABLE ore_materials (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  material_key TEXT NOT NULL UNIQUE,
  internal_name TEXT NOT NULL UNIQUE,
  localized_name TEXT
);
CREATE TABLE ore_veins (
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
CREATE TABLE ore_vein_materials (
  vein_id INTEGER NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('primary','secondary','between','sporadic')),
  material_id INTEGER NOT NULL,
  PRIMARY KEY(vein_id, role),
  FOREIGN KEY(vein_id) REFERENCES ore_veins(id),
  FOREIGN KEY(material_id) REFERENCES ore_materials(id)
);
CREATE TABLE ore_vein_dimensions (
  vein_id INTEGER NOT NULL,
  dimension_key TEXT NOT NULL,
  dimension_name TEXT NOT NULL,
  min_y INTEGER NOT NULL,
  max_y INTEGER NOT NULL,
  PRIMARY KEY(vein_id, dimension_key),
  FOREIGN KEY(vein_id) REFERENCES ore_veins(id)
);
CREATE TABLE small_ores (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  small_ore_key TEXT NOT NULL,
  internal_name TEXT NOT NULL,
  material_id INTEGER NOT NULL,
  min_y INTEGER NOT NULL,
  max_y INTEGER NOT NULL,
  amount_per_chunk INTEGER NOT NULL,
  FOREIGN KEY(material_id) REFERENCES ore_materials(id)
);
CREATE TABLE small_ore_dimensions (
  small_ore_id INTEGER NOT NULL,
  dimension_key TEXT NOT NULL,
  dimension_name TEXT NOT NULL,
  PRIMARY KEY(small_ore_id, dimension_key),
  FOREIGN KEY(small_ore_id) REFERENCES small_ores(id)
);

INSERT INTO ore_materials(id, material_key, internal_name, localized_name)
SELECT id, material_key, internal_name, localized_name FROM fresh.ore_materials;
INSERT INTO ore_veins(id, vein_key, internal_name, display_name, min_y, max_y, weight, density, size)
SELECT id, vein_key, internal_name, display_name, min_y, max_y, weight, density, size FROM fresh.ore_veins;
INSERT INTO ore_vein_materials(vein_id, role, material_id)
SELECT vein_id, role, material_id FROM fresh.ore_vein_materials;
INSERT INTO ore_vein_dimensions(vein_id, dimension_key, dimension_name, min_y, max_y)
SELECT vein_id, dimension_key,
       CASE WHEN lower(dimension_key)='makemake' THEN 'MakeMake' ELSE dimension_name END,
       min_y, max_y FROM fresh.ore_vein_dimensions;
INSERT INTO small_ores(id, small_ore_key, internal_name, material_id, min_y, max_y, amount_per_chunk)
SELECT id, small_ore_key, internal_name, material_id, min_y, max_y, amount_per_chunk FROM fresh.small_ores;
INSERT INTO small_ore_dimensions(small_ore_id, dimension_key, dimension_name)
SELECT small_ore_id, dimension_key,
       CASE WHEN lower(dimension_key)='makemake' THEN 'MakeMake' ELSE dimension_name END
FROM fresh.small_ore_dimensions;

CREATE INDEX idx_ore_materials_internal_name ON ore_materials(internal_name COLLATE NOCASE);
CREATE INDEX idx_ore_materials_localized_name ON ore_materials(localized_name COLLATE NOCASE);
CREATE INDEX idx_ore_veins_key ON ore_veins(vein_key);
CREATE INDEX idx_ore_veins_name ON ore_veins(display_name COLLATE NOCASE);
CREATE INDEX idx_ore_vein_materials_material ON ore_vein_materials(material_id, vein_id);
CREATE INDEX idx_ore_vein_dimensions_key ON ore_vein_dimensions(dimension_key, vein_id);
CREATE INDEX idx_ore_vein_dimensions_dimension ON ore_vein_dimensions(dimension_name COLLATE NOCASE, vein_id);
CREATE INDEX idx_small_ores_material ON small_ores(material_id);
CREATE INDEX idx_small_ores_key ON small_ores(small_ore_key);
CREATE INDEX idx_small_ore_dimensions_key ON small_ore_dimensions(dimension_key, small_ore_id);
CREATE INDEX idx_small_ore_dimensions_dimension ON small_ore_dimensions(dimension_name COLLATE NOCASE, small_ore_id);

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
       COALESCE(m.localized_name, m.internal_name), 'small',
       d.dimension_key, d.dimension_name, s.min_y, s.max_y,
       NULL, NULL, NULL, s.amount_per_chunk
FROM small_ores s
JOIN ore_materials m ON m.id=s.material_id
JOIN small_ore_dimensions d ON d.small_ore_id=s.id;

INSERT OR REPLACE INTO manifest(key, value) VALUES
  ('worldgen_data_available', '1'),
  ('ore_vein_count', (SELECT CAST(count(*) AS TEXT) FROM ore_veins)),
  ('small_ore_count', (SELECT CAST(count(*) AS TEXT) FROM small_ores)),
  ('worldgen_error_count', '0'),
  ('worldgen_imported_at_millis', CAST(unixepoch('now') * 1000 AS TEXT));
COMMIT;
DETACH DATABASE fresh;
PRAGMA foreign_keys=ON;
SQL

sqlite3 "$tmp" "PRAGMA integrity_check;" | grep -qx ok
if [[ -n "$(sqlite3 "$tmp" "PRAGMA foreign_key_check;")" ]]; then
  echo "worldgen import produced foreign-key violations" >&2
  exit 1
fi
vein_count="$(sqlite3 "$tmp" "SELECT count(*) FROM ore_veins;")"
route_count="$(sqlite3 "$tmp" "SELECT count(*) FROM ore_generation_routes;")"
if [[ "$vein_count" -le 0 || "$route_count" -le 0 ]]; then
  echo "worldgen import produced no queryable routes" >&2
  exit 1
fi

mv -f "$tmp" "$DEST_DB"
trap - EXIT
echo "imported $vein_count ore veins and queryable worldgen routes into: $DEST_DB"
