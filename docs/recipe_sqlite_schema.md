# GregGPT Production SQLite Schema

`greggpt_recipes.sqlite` is the runtime source for recipe, machine-capability,
resource-identity, and ore-generation data. Schema version 2 is designed for
model-driven exploration: SQL exposes alternatives and current facts, while the
agent decides which production lines to investigate.

## Model-facing views

### `resource_catalog`

One canonical row per item or fluid. `resource_key` is stable across the recipe
and inventory tools:

- `item:<registry_name>:<damage>`
- `fluid:<fluid_name>`

Use `item_search MATCH '<terms>'` for fast item-name resolution. The FTS rowid
is the corresponding `items.id`.

### `recipe_routes`

One row per recipe output, including:

- exact output identity and quantity;
- normalized `chance`, where 10000 is 100 percent;
- `expected_output_amount`;
- `is_primary`, so byproducts are not mistaken for a route's main product;
- handler, capability, machine-name hint, EU/t, and voltage tier.

The view excludes recipes that are invalid, hidden, fake, or disabled.

### `recipe_ingredients`

One row per input alternative. Rows preserve the input position and option
index, exact item/fluid/ore-dictionary identity, quantity, and the `consumed`
and `catalyst` flags. Alternatives sharing an input position are choices, not
additional required ingredients.

### `handler_machine_options`

Connects recipe handlers to their normalized capability and machine-name hint.
`machine_options` can contain exact item/block identities, tier ranges, and
provenance when a trustworthy mapping is available. A null option means the
capability is known but no exact machine mapping has been asserted.

### `recipe_data_quality`

Reports validity and input/output completeness per recipe. `dump_errors` holds
per-record failures that were rolled back instead of leaving partial recipes.

### `ore_generation_routes`

Normalized vein and small-ore routes, including material role, internal
dimension key, user-facing dimension name,
height range, weight/density/size, and small-ore amount per chunk.
The same normalized vein key may identify multiple initialized layers when GTNH
registers that vein separately for different dimensions; the integer table ID
identifies a layer, while queries intentionally group matching keys.
When a fresh dump's recipe coverage is not acceptable, use
`scripts/import_worldgen_db.sh` to atomically merge only its verified worldgen
tables into an existing migrated schema-v2 recipe database.

## Core tables

- `manifest`
- `items`, `fluids`, `ore_dict_entries`
- `recipe_handlers`, `recipes`
- `recipe_inputs`, `recipe_input_options`, `recipe_outputs`
- `recipe_edges`, the materialized and indexed traversal layer used by the
  model-facing route and ingredient views
- `recipe_metadata`, `dump_errors`
- `machine_capabilities`, `machine_options`
- `ore_materials`, `ore_veins`, `ore_vein_materials`,
  `ore_vein_dimensions`, `small_ores`, `small_ore_dimensions`

Foreign keys use integer row IDs internally. Agent-facing joins should use the
stable resource keys exposed by the views.

## Production-line workflow

Resolve a target:

```sql
SELECT i.id, i.display_name, i.registry_name, i.damage
FROM item_search s
JOIN items i ON i.id = s.rowid
WHERE item_search MATCH 'yttrium'
ORDER BY i.display_name
LIMIT 20
```

Compare routes for exact Yttrium Dust:

```sql
SELECT recipe_id, handler_name, capability_key, machine_name_hint,
       voltage_tier, output_amount, chance, expected_output_amount, is_primary
FROM recipe_routes
WHERE output_resource_key = 'item:gregtech:gt.metaitem.01:2045'
ORDER BY is_primary DESC, expected_output_amount DESC, eut, duration_ticks
LIMIT 30
```

Fetch every input alternative for selected recipes in one query:

```sql
SELECT recipe_id, input_position, option_index, input_resource_key,
       input_name, input_amount, consumed, catalyst,
       registry_name, damage
FROM recipe_ingredients
WHERE recipe_id IN (37955, 51984, 117182)
ORDER BY recipe_id, input_position, option_index
```

Each input position is one required ingredient group across both item and fluid
inputs. Rows sharing a position are alternatives; rows at different positions
are simultaneous requirements.

Send the returned exact item identities to `inventory_totals` in one call.
Compare totals with `input_amount`, then use `handler_machine_options` and placed
block search—optionally filtered to dimension 183—to identify machine gaps.
Recursively inspect recipes only for missing inputs on promising alternatives.

## Required indexes

Schema v2 indexes both traversal directions:

- recipe to inputs/outputs;
- output item/fluid to producing recipes;
- input option item/fluid/ore name to consuming recipes;
- input to alternatives;
- handler and usable-recipe filters;
- machine capability to exact options;
- ore material/dimension to generation routes;
- FTS5 item-name search.

## Completeness rules

- `manifest.schema_version` must equal `2`.
- `manifest.dump_complete=1` only when recipe and worldgen passes report no
  record failures and every required source has nonzero coverage.
- `manifest.worldgen_data_available=1` only when a successful dump contains ore
  veins.
- `crafting_recipe_count` and `furnace_recipe_count` are separate; furnace rows
  cannot make a failed crafting pass appear complete.
- Every output has a non-null normalized chance.
- Partial recipe inserts are rolled back with a savepoint and recorded in
  `dump_errors`.
- `recipe_routes` must never expose invalid, hidden, fake, or disabled rows.

Legacy schema-v1 files can be upgraded with
`scripts/migrate_recipe_db_v2.sh`. The migration restores query structure and
chance normalization but correctly leaves `dump_complete=0` and
`worldgen_data_available=0` when the legacy dump lacks crafting/worldgen data.
A fresh dump is required to fill those missing sources.
