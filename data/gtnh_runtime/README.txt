Runtime GTNH dataset for GregGPT.

This directory intentionally excludes large raw dumps
to prevent accidental full-file reads and OOM/restarts.

Use indexed files under index/:
- item_index.tsv
- greggpt_recipes.sqlite (SQLite recipe database)
- oredict_index.tsv (optional; only present after importing a real ore-dict dump)
