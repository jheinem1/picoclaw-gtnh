#!/usr/bin/env python3
import json
import pathlib
import sqlite3
import sys

BASE = pathlib.Path(__file__).resolve().parents[2]
DATA_DIR = BASE / "data" / "gtnh"
INDEX_DIR = DATA_DIR / "index"
STACKS_PATH = DATA_DIR / "recipes_stacks.json"
RECIPES_PATH = DATA_DIR / "recipes.json"
DB_PATH = INDEX_DIR / "gtnh.db"


def load_json(path: pathlib.Path):
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def create_schema(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.executescript(
        """
        PRAGMA journal_mode=WAL;
        PRAGMA synchronous=NORMAL;

        DROP TABLE IF EXISTS items;
        DROP TABLE IF EXISTS fluids;
        DROP TABLE IF EXISTS recipes;

        CREATE TABLE items (
          slug TEXT PRIMARY KEY,
          id INTEGER,
          display_name TEXT,
          reg_name TEXT,
          name TEXT,
          nbt_json TEXT
        );

        CREATE TABLE fluids (
          slug TEXT PRIMARY KEY,
          id INTEGER,
          display_name TEXT,
          reg_name TEXT,
          name TEXT,
          nbt_json TEXT
        );

        CREATE TABLE recipes (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          query_item_slug TEXT,
          query_item_json TEXT,
          handler_id TEXT,
          handler_name TEXT,
          tab_name TEXT,
          out_item_slug TEXT,
          out_item_json TEXT,
          ingredients_json TEXT,
          other_stacks_json TEXT
        );

        CREATE INDEX idx_items_display_name ON items(display_name);
        CREATE INDEX idx_items_reg_name ON items(reg_name);
        CREATE INDEX idx_items_name ON items(name);

        CREATE INDEX idx_recipes_query_item_slug ON recipes(query_item_slug);
        CREATE INDEX idx_recipes_out_item_slug ON recipes(out_item_slug);
        CREATE INDEX idx_recipes_handler_id ON recipes(handler_id);
        """
    )
    conn.commit()


def insert_items(conn: sqlite3.Connection, table: str, rows: dict) -> int:
    cur = conn.cursor()
    count = 0
    for slug, meta in rows.items():
        cur.execute(
            f"""
            INSERT INTO {table}(slug, id, display_name, reg_name, name, nbt_json)
            VALUES (?, ?, ?, ?, ?, ?)
            """,
            (
                slug,
                meta.get("id"),
                meta.get("displayName", ""),
                meta.get("regName", ""),
                meta.get("name", ""),
                json.dumps(meta.get("nbt", {}), separators=(",", ":")),
            ),
        )
        count += 1
    conn.commit()
    return count


def insert_recipes(conn: sqlite3.Connection, data: dict) -> int:
    queries = data.get("queries", [])
    cur = conn.cursor()
    count = 0

    for q in queries:
        query_item = q.get("queryItem", "")
        query_item_slug = ""
        if isinstance(query_item, str):
            query_item_slug = query_item
        elif isinstance(query_item, dict):
            query_item_slug = str(query_item.get("itemSlug", ""))
        for handler in q.get("handlers", []):
            hid = handler.get("id", "")
            hname = handler.get("name", "")
            tname = handler.get("tabName", "")

            for recipe in handler.get("recipes", []):
                generic = recipe.get("generic", {})
                out_item = generic.get("outItem", "")
                out_item_slug = ""
                if isinstance(out_item, str):
                    out_item_slug = out_item
                elif isinstance(out_item, dict):
                    out_item_slug = str(out_item.get("itemSlug", ""))
                ingredients = generic.get("ingredients", [])
                other_stacks = generic.get("otherStacks", [])

                cur.execute(
                    """
                    INSERT INTO recipes(
                      query_item_slug, query_item_json, handler_id, handler_name, tab_name,
                      out_item_slug, out_item_json, ingredients_json, other_stacks_json
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        query_item_slug,
                        json.dumps(query_item, separators=(",", ":")),
                        hid,
                        hname,
                        tname,
                        out_item_slug,
                        json.dumps(out_item, separators=(",", ":")),
                        json.dumps(ingredients, separators=(",", ":")),
                        json.dumps(other_stacks, separators=(",", ":")),
                    ),
                )
                count += 1

    conn.commit()
    return count


def main() -> int:
    for path in (STACKS_PATH, RECIPES_PATH):
        if not path.exists():
            print(f"missing: {path}", file=sys.stderr)
            return 1

    INDEX_DIR.mkdir(parents=True, exist_ok=True)
    if DB_PATH.exists():
        DB_PATH.unlink()

    stacks = load_json(STACKS_PATH)
    recipes = load_json(RECIPES_PATH)

    conn = sqlite3.connect(DB_PATH)
    try:
        create_schema(conn)
        item_count = insert_items(conn, "items", stacks.get("items", {}))
        fluid_count = insert_items(conn, "fluids", stacks.get("fluids", {}))
        recipe_count = insert_recipes(conn, recipes)
    finally:
        conn.close()

    print(f"wrote: {DB_PATH}")
    print(f"items: {item_count}")
    print(f"fluids: {fluid_count}")
    print(f"recipes: {recipe_count}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
