#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DB="${1:-${SOURCE_DB:-}}"
DEST_DB="$ROOT/data/gtnh/index/greggpt_recipes.sqlite"

if [[ -z "$SOURCE_DB" ]]; then
  echo "usage: scripts/import_recipe_db.sh <path-to-greggpt_recipes.sqlite>" >&2
  echo "or set SOURCE_DB=/path/to/greggpt_recipes.sqlite" >&2
  exit 2
fi
if [[ ! -s "$SOURCE_DB" ]]; then
  echo "missing recipe sqlite dump: $SOURCE_DB" >&2
  exit 1
fi

mkdir -p "$(dirname "$DEST_DB")"
tmp="$(mktemp "$DEST_DB.tmp.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
cp -f "$SOURCE_DB" "$tmp"

if [[ ! -s "$tmp" ]]; then
  echo "recipe database copy is empty: $tmp" >&2
  exit 1
fi

if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$tmp" "PRAGMA integrity_check;" | grep -qx "ok"
  schema_version="$(sqlite3 "$tmp" "SELECT value FROM manifest WHERE key='schema_version';")"
  needs_v2_reconcile=0
  if [[ "$schema_version" == "2" ]]; then
    if [[ "$(sqlite3 "$tmp" "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='recipe_edges';")" != "1" ]]; then
      needs_v2_reconcile=1
    elif [[ "$(sqlite3 "$tmp" "SELECT count(*) FROM pragma_table_info('ore_vein_dimensions') WHERE name='dimension_key';")" != "1" ]]; then
      needs_v2_reconcile=1
    elif [[ "$(sqlite3 "$tmp" "SELECT count(*) FROM sqlite_schema WHERE type='view' AND name='recipe_routes' AND lower(sql) LIKE '%recipe_input_options%';")" != "1" ]]; then
      needs_v2_reconcile=1
    elif [[ "$(sqlite3 "$tmp" "SELECT count(*) FROM sqlite_schema WHERE type='view' AND name='recipe_ingredients' AND lower(sql) LIKE '%recipe_input_options%' AND lower(sql) LIKE '%nullif(trim(e.resource_name)%';")" != "1" ]]; then
      needs_v2_reconcile=1
    elif [[ "$(sqlite3 "$tmp" "SELECT count(*) FROM recipe_inputs ri JOIN recipes r ON r.id=ri.recipe_id WHERE r.category='gregtech' AND ri.amount=0;")" != "0" ]]; then
      needs_v2_reconcile=1
    fi
  fi
  if [[ "$schema_version" == "1" || "$needs_v2_reconcile" == "1" ]]; then
    "$ROOT/scripts/migrate_recipe_db_v2.sh" "$tmp"
    schema_version="2"
  elif [[ "$schema_version" != "2" ]]; then
    echo "unsupported recipe/worldgen schema version: ${schema_version:-missing} (expected 2)" >&2
    exit 1
  fi
  for table in manifest items fluids recipe_handlers recipes recipe_inputs recipe_input_options recipe_outputs recipe_edges recipe_metadata machine_capabilities machine_options dump_errors ore_materials ore_veins ore_vein_materials ore_vein_dimensions small_ores small_ore_dimensions; do
    sqlite3 "$tmp" "SELECT 1 FROM $table LIMIT 1;" >/dev/null
  done
  for view in resource_catalog recipe_routes recipe_ingredients handler_machine_options recipe_data_quality ore_generation_routes; do
    sqlite3 "$tmp" "SELECT 1 FROM $view LIMIT 1;" >/dev/null
  done
  if [[ "$(sqlite3 "$tmp" "SELECT count(*) FROM recipe_outputs WHERE chance IS NULL;")" != "0" ]]; then
    echo "recipe database contains outputs without normalized chance" >&2
    exit 1
  fi
  malformed_ingredients="$(sqlite3 "$tmp" "SELECT count(*) FROM recipe_ingredients WHERE input_resource_key IS NULL OR trim(coalesce(input_name,''))='' OR input_kind='unknown' OR input_resource_key IN ('item:','fluid:','oredict:') OR input_resource_key LIKE 'unknown:%';")"
  if [[ "$malformed_ingredients" != "0" ]]; then
    echo "recipe search view exposes $malformed_ingredients malformed ingredient identities" >&2
    exit 1
  fi
  incomplete_routes="$(sqlite3 "$tmp" "SELECT count(*) FROM recipe_routes rr WHERE EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=rr.recipe_id AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id));")"
  if [[ "$incomplete_routes" != "0" ]]; then
    echo "recipe search view exposes $incomplete_routes routes with unresolved inputs" >&2
    exit 1
  fi
  sqlite3 "$tmp" "SELECT 1 FROM item_search WHERE item_search MATCH 'machine' LIMIT 1;" >/dev/null
  if [[ "$(sqlite3 "$tmp" "SELECT value FROM manifest WHERE key='worldgen_data_available';")" != "1" ]]; then
    echo "warning: imported schema v2 database has no worldgen rows; ore_generation_lookup will require a fresh dump" >&2
  fi
fi

mv -f "$tmp" "$DEST_DB"
trap - EXIT
echo "imported recipe database: $DEST_DB"
