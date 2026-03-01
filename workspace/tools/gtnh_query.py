#!/usr/bin/env python3
import argparse
import json
import pathlib
import sqlite3
import sys

BASE = pathlib.Path(__file__).resolve().parents[2]
DB_PATH = BASE / "data" / "gtnh" / "index" / "gtnh.db"


def fail(msg: str) -> int:
    print(json.dumps({"ok": False, "error": msg}))
    return 1


def connect() -> sqlite3.Connection:
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    return conn


def row_to_item(row: sqlite3.Row) -> dict:
    return {
        "slug": row["slug"],
        "id": row["id"],
        "display_name": row["display_name"],
        "reg_name": row["reg_name"],
        "name": row["name"],
        "source": "gtnh-data/recipes_stacks.json",
    }


def cmd_find_item(conn: sqlite3.Connection, args: argparse.Namespace) -> int:
    cur = conn.cursor()
    q = "%" + args.text.lower() + "%"
    limit = max(1, min(args.limit, 50))

    rows = cur.execute(
        """
        SELECT slug, id, display_name, reg_name, name
        FROM items
        WHERE lower(display_name) LIKE ?
           OR lower(reg_name) LIKE ?
           OR lower(name) LIKE ?
        ORDER BY
          CASE
            WHEN lower(display_name)=lower(?) THEN 0
            WHEN lower(reg_name)=lower(?) THEN 1
            ELSE 2
          END,
          display_name ASC
        LIMIT ?
        """,
        (q, q, q, args.text, args.text, limit),
    ).fetchall()

    print(json.dumps({"ok": True, "count": len(rows), "items": [row_to_item(r) for r in rows]}, ensure_ascii=False))
    return 0


def cmd_item_by_slug(conn: sqlite3.Connection, args: argparse.Namespace) -> int:
    cur = conn.cursor()
    row = cur.execute(
        "SELECT slug, id, display_name, reg_name, name FROM items WHERE slug=?",
        (args.slug,),
    ).fetchone()
    if row is None:
        print(json.dumps({"ok": False, "error": "item not found", "slug": args.slug}))
        return 2

    print(json.dumps({"ok": True, "item": row_to_item(row)}, ensure_ascii=False))
    return 0


def cmd_recipes_for_slug(conn: sqlite3.Connection, args: argparse.Namespace) -> int:
    cur = conn.cursor()
    limit = max(1, min(args.limit, 100))

    recipe_rows = cur.execute(
        """
        SELECT id, query_item_json, handler_id, handler_name, tab_name, out_item_json, ingredients_json, other_stacks_json
        FROM recipes
        WHERE query_item_slug=? OR out_item_slug=?
        LIMIT ?
        """,
        (args.slug, args.slug, limit),
    ).fetchall()

    out = []
    for r in recipe_rows:
        out.append(
            {
                "id": r["id"],
                "query_item": json.loads(r["query_item_json"] or "null"),
                "handler": {
                    "id": r["handler_id"],
                    "name": r["handler_name"],
                    "tab": r["tab_name"],
                },
                "out_item": json.loads(r["out_item_json"] or "null"),
                "ingredients": json.loads(r["ingredients_json"] or "[]"),
                "other_stacks": json.loads(r["other_stacks_json"] or "[]"),
            }
        )

    print(
        json.dumps(
            {
                "ok": True,
                "slug": args.slug,
                "count": len(out),
                "recipes": out,
                "sources": ["gtnh-data/recipes.json", "gtnh-data/recipes_stacks.json"],
            },
            ensure_ascii=False,
        )
    )
    return 0


def cmd_resolve_and_recipes(conn: sqlite3.Connection, args: argparse.Namespace) -> int:
    cur = conn.cursor()
    q = "%" + args.item.lower() + "%"
    item = cur.execute(
        """
        SELECT slug, id, display_name, reg_name, name
        FROM items
        WHERE lower(display_name) LIKE ?
           OR lower(reg_name) LIKE ?
           OR lower(name) LIKE ?
        ORDER BY
          CASE
            WHEN lower(display_name)=lower(?) THEN 0
            WHEN lower(reg_name)=lower(?) THEN 1
            ELSE 2
          END,
          display_name ASC
        LIMIT 1
        """,
        (q, q, q, args.item, args.item),
    ).fetchone()

    if item is None:
        print(json.dumps({"ok": False, "error": "item not found", "query": args.item}))
        return 2

    rows = cur.execute(
        """
        SELECT id, query_item_json, handler_id, handler_name, tab_name, out_item_json, ingredients_json, other_stacks_json
        FROM recipes
        WHERE query_item_slug=? OR out_item_slug=?
        LIMIT ?
        """,
        (item["slug"], item["slug"], max(1, min(args.limit, 100))),
    ).fetchall()

    recipes = []
    for r in rows:
        recipes.append(
            {
                "id": r["id"],
                "query_item": json.loads(r["query_item_json"] or "null"),
                "handler": {
                    "id": r["handler_id"],
                    "name": r["handler_name"],
                    "tab": r["tab_name"],
                },
                "out_item": json.loads(r["out_item_json"] or "null"),
                "ingredients": json.loads(r["ingredients_json"] or "[]"),
                "other_stacks": json.loads(r["other_stacks_json"] or "[]"),
            }
        )

    print(
        json.dumps(
            {
                "ok": True,
                "query": args.item,
                "item": row_to_item(item),
                "recipes_count": len(recipes),
                "recipes": recipes,
                "sources": ["gtnh-data/recipes_stacks.json", "gtnh-data/recipes.json"],
            },
            ensure_ascii=False,
        )
    )
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Targeted GTNH indexed query tool")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_find = sub.add_parser("find-item", help="Find items by text")
    p_find.add_argument("text")
    p_find.add_argument("--limit", type=int, default=10)

    p_slug = sub.add_parser("item", help="Lookup exact item by slug")
    p_slug.add_argument("slug")

    p_rec = sub.add_parser("recipes", help="Lookup recipes by exact slug")
    p_rec.add_argument("slug")
    p_rec.add_argument("--limit", type=int, default=10)

    p_auto = sub.add_parser("resolve-recipes", help="Resolve item text then return recipes")
    p_auto.add_argument("item")
    p_auto.add_argument("--limit", type=int, default=10)

    return parser


def main() -> int:
    if not DB_PATH.exists():
        return fail(f"index database missing: {DB_PATH}; run workspace/tools/build_gtnh_db.py")

    parser = build_parser()
    args = parser.parse_args()

    conn = connect()
    try:
        if args.cmd == "find-item":
            return cmd_find_item(conn, args)
        if args.cmd == "item":
            return cmd_item_by_slug(conn, args)
        if args.cmd == "recipes":
            return cmd_recipes_for_slug(conn, args)
        if args.cmd == "resolve-recipes":
            return cmd_resolve_and_recipes(conn, args)
        return fail(f"unknown command: {args.cmd}")
    finally:
        conn.close()


if __name__ == "__main__":
    sys.exit(main())
