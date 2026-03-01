Runtime GTNH dataset for PicoClaw.

This directory intentionally excludes large raw dumps (recipes.json, recipes_stacks.json)
to prevent accidental full-file reads and OOM/restarts.

Use indexed files under index/:
- item_index.tsv
- recipe_index.tsv
