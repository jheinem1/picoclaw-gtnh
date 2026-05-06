# GregGPT Recipe SQLite Schema

`greggpt_recipes.sqlite` is the only runtime recipe data source. Raw recipe JSON,
`recipe_index.tsv`, `gtnh_resolve_recipes`, and `gtnh_search_recipes` are obsolete
recipe paths and must not be used as fallbacks.

## Required Tables

```sql
CREATE TABLE manifest (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  registry_name TEXT NOT NULL,
  damage INTEGER NOT NULL,
  display_name TEXT,
  unlocalized_name TEXT,
  max_damage INTEGER,
  UNIQUE(registry_name, damage)
);

CREATE TABLE fluids (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  fluid_name TEXT NOT NULL UNIQUE,
  localized_name TEXT
);

-- Reserved for a future merge with the separate ore-dictionary dump.
CREATE TABLE ore_dict_entries (ore_name TEXT NOT NULL, item_id INTEGER NOT NULL);

CREATE TABLE recipe_handlers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE recipes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  handler_id INTEGER NOT NULL,
  recipe_key TEXT NOT NULL,
  category TEXT NOT NULL,
  duration_ticks INTEGER,
  eut INTEGER,
  special_value INTEGER,
  hidden INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE recipe_inputs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  recipe_id INTEGER NOT NULL,
  position INTEGER NOT NULL,
  kind TEXT NOT NULL,
  amount INTEGER,
  label TEXT
);

CREATE TABLE recipe_input_options (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  input_id INTEGER NOT NULL,
  option_index INTEGER NOT NULL,
  kind TEXT NOT NULL,
  item_id INTEGER,
  fluid_id INTEGER,
  amount INTEGER,
  ore_name TEXT,
  label TEXT
);

CREATE TABLE recipe_outputs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  recipe_id INTEGER NOT NULL,
  position INTEGER NOT NULL,
  kind TEXT NOT NULL,
  amount INTEGER,
  item_id INTEGER,
  fluid_id INTEGER,
  chance INTEGER,
  label TEXT
);

CREATE TABLE recipe_metadata (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  recipe_id INTEGER NOT NULL,
  key TEXT NOT NULL,
  value TEXT
);
```

## Required Indexes

```sql
CREATE INDEX idx_items_registry_damage ON items(registry_name, damage);
CREATE INDEX idx_recipe_inputs_recipe ON recipe_inputs(recipe_id);
CREATE INDEX idx_recipe_outputs_recipe ON recipe_outputs(recipe_id);
CREATE INDEX idx_recipes_handler ON recipes(handler_id);
```

## Sample Queries

Find candidate items:

```sql
SELECT id, display_name, registry_name, damage
FROM items
WHERE lower(display_name) LIKE lower('%distillation tower%')
   OR lower(registry_name) LIKE lower('%distillation tower%')
ORDER BY display_name
LIMIT 20;
```

List recipes that output an item:

```sql
SELECT r.id, r.recipe_key, h.name AS handler_name, r.category, r.duration_ticks, r.eut
FROM recipes r
JOIN recipe_handlers h ON h.id = r.handler_id
JOIN recipe_outputs o ON o.recipe_id = r.id
JOIN items i ON i.id = o.item_id
WHERE i.registry_name = 'gregtech:gt.blockmachines' AND i.damage = 1126
ORDER BY h.name, r.id
LIMIT 20;
```

List inputs for one recipe. Use this shape for user-facing ingredient counts;
`recipe_input_options.amount` is the resolved option quantity when present, and
falls back to `recipe_inputs.amount` for optionless rows.

```sql
SELECT
  ri.position,
  rio.option_index,
  COALESCE(rio.kind, ri.kind) AS kind,
  COALESCE(rio.amount, ri.amount) AS amount,
  COALESCE(item.display_name, fluid.localized_name, rio.ore_name, rio.label, ri.label) AS ingredient,
  item.registry_name,
  item.damage
FROM recipe_inputs ri
LEFT JOIN recipe_input_options rio ON rio.input_id = ri.id
LEFT JOIN items item ON item.id = rio.item_id
LEFT JOIN fluids fluid ON fluid.id = rio.fluid_id
WHERE ri.recipe_id = 123
ORDER BY ri.position, rio.option_index;
```

List outputs for one recipe:

```sql
SELECT
  ro.position,
  ro.kind,
  ro.amount,
  COALESCE(item.display_name, fluid.localized_name, ro.label) AS output,
  item.registry_name,
  item.damage
FROM recipe_outputs ro
LEFT JOIN items item ON item.id = ro.item_id
LEFT JOIN fluids fluid ON fluid.id = ro.fluid_id
WHERE ro.recipe_id = 123
ORDER BY ro.position;
```

Detect recipe rows with no material inputs:

```sql
SELECT r.id, r.recipe_key, h.name AS handler_name, r.category
FROM recipes r
JOIN recipe_handlers h ON h.id = r.handler_id
LEFT JOIN recipe_inputs i ON i.recipe_id = r.id
WHERE i.recipe_id IS NULL
LIMIT 50;
```

## Validation Rules

- `manifest.schema_version` must exist.
- `items`, `recipe_handlers`, `recipes`, and `recipe_outputs` must be non-empty.
- Any recipe used for a build/material answer must have at least one
  `recipe_inputs` row. Empty-input rows are data-quality warnings, not valid
  no-material recipes.
- Distillation Tower must return either non-empty candidate inputs or a clear
  data-quality warning.
