#!/usr/bin/env bash
set -euo pipefail

db_path=${1:-}
if [[ -z "$db_path" || ! -s "$db_path" ]]; then
  echo "usage: $0 /path/to/greggpt_recipes.sqlite" >&2
  exit 2
fi
if command -v sqlite3 >/dev/null 2>&1; then
  query_db() { sqlite3 -readonly "$db_path" "$1"; }
  patch_db() {
    sqlite3 "$db_path" <<'SQL'
BEGIN IMMEDIATE;
-- GregTech encodes reusable tools, molds, shapes, and programmed circuits as
-- zero-sized stacks. Expose them as one-item presence requirements which are
-- catalysts rather than consumed ingredients.
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
UPDATE recipe_edges
SET amount=1, expected_amount=1, consumed=0, catalyst=1
WHERE direction='input' AND amount=0
  AND recipe_id IN (SELECT id FROM recipes WHERE category='gregtech');
DROP VIEW IF EXISTS recipe_routes;
CREATE VIEW recipe_routes AS SELECT r.id AS recipe_id, r.recipe_key, r.category, h.name AS handler_name, mc.capability_key, mc.machine_name_hint, r.duration_ticks, r.eut, CASE WHEN r.eut IS NULL THEN NULL WHEN abs(r.eut)<=8 THEN 'ULV' WHEN abs(r.eut)<=32 THEN 'LV' WHEN abs(r.eut)<=128 THEN 'MV' WHEN abs(r.eut)<=512 THEN 'HV' WHEN abs(r.eut)<=2048 THEN 'EV' WHEN abs(r.eut)<=8192 THEN 'IV' WHEN abs(r.eut)<=32768 THEN 'LuV' WHEN abs(r.eut)<=131072 THEN 'ZPM' WHEN abs(r.eut)<=524288 THEN 'UV' ELSE 'UHV+' END AS voltage_tier, e.position AS output_position, e.resource_kind AS output_kind, e.resource_key AS output_resource_key, e.resource_name AS output_name, i.registry_name, i.damage, f.fluid_name, e.amount AS output_amount, e.chance, e.expected_amount AS expected_output_amount, e.is_primary FROM recipe_edges e JOIN recipes r ON r.id=e.recipe_id JOIN recipe_handlers h ON h.id=r.handler_id LEFT JOIN machine_capabilities mc ON mc.handler_id=h.id LEFT JOIN items i ON i.id=e.item_id LEFT JOIN fluids f ON f.id=e.fluid_id WHERE e.direction='output' AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1 AND NOT EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=r.id AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id));
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
  AND NOT EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=r.id AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id));
COMMIT;
SQL
  }
else
  command -v python3 >/dev/null 2>&1 || { echo "sqlite3 or python3 is required" >&2; exit 1; }
  query_db() {
    python3 - "$db_path" "$1" <<'PY'
import sqlite3
import sys
with sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True) as db:
    row = db.execute(sys.argv[2]).fetchone()
    if row is not None and row[0] is not None:
        print(row[0])
PY
  }
  patch_db() {
    python3 - "$db_path" <<'PY'
import sqlite3
import sys
sql = """
BEGIN IMMEDIATE;
-- GregTech encodes reusable tools, molds, shapes, and programmed circuits as
-- zero-sized stacks. Expose them as one-item presence requirements which are
-- catalysts rather than consumed ingredients.
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
UPDATE recipe_edges
SET amount=1, expected_amount=1, consumed=0, catalyst=1
WHERE direction='input' AND amount=0
  AND recipe_id IN (SELECT id FROM recipes WHERE category='gregtech');
DROP VIEW IF EXISTS recipe_routes;
CREATE VIEW recipe_routes AS SELECT r.id AS recipe_id, r.recipe_key, r.category, h.name AS handler_name, mc.capability_key, mc.machine_name_hint, r.duration_ticks, r.eut, CASE WHEN r.eut IS NULL THEN NULL WHEN abs(r.eut)<=8 THEN 'ULV' WHEN abs(r.eut)<=32 THEN 'LV' WHEN abs(r.eut)<=128 THEN 'MV' WHEN abs(r.eut)<=512 THEN 'HV' WHEN abs(r.eut)<=2048 THEN 'EV' WHEN abs(r.eut)<=8192 THEN 'IV' WHEN abs(r.eut)<=32768 THEN 'LuV' WHEN abs(r.eut)<=131072 THEN 'ZPM' WHEN abs(r.eut)<=524288 THEN 'UV' ELSE 'UHV+' END AS voltage_tier, e.position AS output_position, e.resource_kind AS output_kind, e.resource_key AS output_resource_key, e.resource_name AS output_name, i.registry_name, i.damage, f.fluid_name, e.amount AS output_amount, e.chance, e.expected_amount AS expected_output_amount, e.is_primary FROM recipe_edges e JOIN recipes r ON r.id=e.recipe_id JOIN recipe_handlers h ON h.id=r.handler_id LEFT JOIN machine_capabilities mc ON mc.handler_id=h.id LEFT JOIN items i ON i.id=e.item_id LEFT JOIN fluids f ON f.id=e.fluid_id WHERE e.direction='output' AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1 AND NOT EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=r.id AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id));
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
  AND NOT EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=r.id AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id));
COMMIT;
"""
with sqlite3.connect(sys.argv[1]) as db:
    try:
        db.executescript(sql)
    except Exception:
        db.rollback()
        raise
PY
  }
fi

schema_version=$(query_db "SELECT value FROM manifest WHERE key='schema_version';")
if [[ "$schema_version" != "2" ]]; then
  echo "recipe database must use schema version 2" >&2
  exit 1
fi
command -v python3 >/dev/null 2>&1 || {
  echo "python3 is required for a consistent pre-patch backup" >&2
  exit 1
}
backup_path=${BACKUP_PATH:-$db_path.before-recipe-search-patch-$(date -u +%Y%m%dT%H%M%SZ)-$$}
if [[ -e "$backup_path" ]]; then
  echo "refusing to overwrite existing backup: $backup_path" >&2
  exit 1
fi
mkdir -p "$(dirname "$backup_path")"
backup_tmp="$backup_path.tmp.$$"
trap 'rm -f "$backup_tmp"' EXIT
python3 - "$db_path" "$backup_tmp" <<'PY'
import sqlite3
import sys

source = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
destination = sqlite3.connect(sys.argv[2])
try:
    source.backup(destination)
finally:
    destination.close()
    source.close()
PY
chmod --reference="$db_path" "$backup_tmp"
if ! ln "$backup_tmp" "$backup_path"; then
  echo "could not publish backup without overwriting an existing file: $backup_path" >&2
  exit 1
fi
rm -f "$backup_tmp"
echo "created consistent pre-patch backup: $backup_path"

patch_db
malformed=$(query_db "SELECT count(*) FROM recipe_ingredients WHERE input_resource_key IS NULL OR trim(coalesce(input_name,''))='' OR input_kind='unknown' OR input_resource_key IN ('item:','fluid:','oredict:') OR input_resource_key LIKE 'unknown:%';")
if [[ "$malformed" != "0" ]]; then
  echo "recipe search view still exposes $malformed malformed ingredient identities" >&2
  exit 1
fi
incomplete_routes=$(query_db "SELECT count(*) FROM recipe_routes rr WHERE EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=rr.recipe_id AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id));")
if [[ "$incomplete_routes" != "0" ]]; then
  echo "recipe search view still exposes $incomplete_routes routes with unresolved inputs" >&2
  exit 1
fi
printf 'patched recipe search schema: %s (malformed_visible_ingredients=%s incomplete_routes=%s)\n' "$db_path" "$malformed" "$incomplete_routes"
