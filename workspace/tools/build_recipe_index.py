#!/usr/bin/env python3
import json
import pathlib
import sys

BASE = pathlib.Path(__file__).resolve().parents[2]
DATA_DIR = BASE / "data" / "gtnh"
STACKS_PATH = DATA_DIR / "recipes_stacks.json"
RECIPES_PATH = DATA_DIR / "recipes.json"
OUTDIR = DATA_DIR / "index"
OUT_PATH = OUTDIR / "recipe_index.tsv"


def slug_map(stacks: dict) -> tuple[dict, dict]:
    out = {}
    keys = {}
    for slug, meta in stacks.get("items", {}).items():
        out[slug] = {
            "display_name": str(meta.get("displayName", "")).strip(),
            "reg_name": str(meta.get("regName", "")).strip(),
            "name": str(meta.get("name", "")).strip(),
        }
        if slug:
            keys[slug] = slug
        name = str(meta.get("name", "")).strip()
        reg = str(meta.get("regName", "")).strip()
        item_id = str(meta.get("id", "")).strip()
        if name:
            keys[name] = slug
        if reg:
            keys[reg] = slug
        if item_id:
            keys[item_id] = slug
    return out, keys


def stack_slug(stack, key_to_slug: dict) -> str:
    if isinstance(stack, str):
        s = stack.strip()
        if s in key_to_slug:
            return key_to_slug[s]
        return s
    if not isinstance(stack, dict):
        return ""
    name = str(stack.get("name") or "").strip()
    reg = str(stack.get("regName") or "").strip()
    item_id = str(stack.get("id") or "").strip()
    if name and name in key_to_slug:
        return key_to_slug[name]
    if reg and reg in key_to_slug:
        return key_to_slug[reg]
    if item_id and item_id in key_to_slug:
        return key_to_slug[item_id]
    return name


def stack_name(stack, names: dict, key_to_slug: dict) -> str:
    slug = stack_slug(stack, key_to_slug)
    if slug and slug in names:
        return names[slug]["display_name"] or names[slug]["reg_name"] or names[slug]["name"] or slug
    display = str(stack.get("displayName") or "").strip() if isinstance(stack, dict) else ""
    return display or slug


def clean_field(text: str) -> str:
    return str(text).replace("\t", " ").replace("\n", " ").strip()


def recipe_ingredients_text(recipe: dict, names: dict, key_to_slug: dict) -> str:
    generic = recipe.get("generic", {}) if isinstance(recipe, dict) else {}
    ingredients = generic.get("ingredients", []) if isinstance(generic, dict) else []
    out = []
    for ing in ingredients:
        if isinstance(ing, dict):
            if "options" in ing and isinstance(ing["options"], list) and ing["options"]:
                first = ing["options"][0]
                out.append(stack_name(first, names, key_to_slug))
            else:
                out.append(stack_name(ing, names, key_to_slug))
    return ", ".join([x for x in out if x])[:2000]


def main() -> int:
    if not STACKS_PATH.exists():
        print(f"missing: {STACKS_PATH}", file=sys.stderr)
        return 1
    if not RECIPES_PATH.exists():
        print(f"missing: {RECIPES_PATH}", file=sys.stderr)
        return 1

    with STACKS_PATH.open("r", encoding="utf-8") as fh:
        stacks_data = json.load(fh)
    with RECIPES_PATH.open("r", encoding="utf-8") as fh:
        recipes_data = json.load(fh)

    names, key_to_slug = slug_map(stacks_data)
    queries = recipes_data.get("queries", [])
    rows = 0

    OUTDIR.mkdir(parents=True, exist_ok=True)
    with OUT_PATH.open("w", encoding="utf-8") as out:
        out.write("query_slug\tquery_name\tout_slug\tout_name\thandler\ttab\tingredients\n")
        for query in queries:
            query_item = query.get("queryItem", {}) if isinstance(query, dict) else {}
            q_slug = stack_slug(query_item, key_to_slug)
            q_name = stack_name(query_item, names, key_to_slug)

            handlers = query.get("handlers", []) if isinstance(query, dict) else []
            for handler in handlers:
                h_name = clean_field(handler.get("name", ""))
                h_tab = clean_field(handler.get("tabName", ""))
                for recipe in handler.get("recipes", []) if isinstance(handler, dict) else []:
                    generic = recipe.get("generic", {}) if isinstance(recipe, dict) else {}
                    out_item = generic.get("outItem", {}) if isinstance(generic, dict) else {}
                    out_slug = stack_slug(out_item, key_to_slug)
                    out_name = stack_name(out_item, names, key_to_slug)
                    ingredients = clean_field(recipe_ingredients_text(recipe, names, key_to_slug))
                    out.write(
                        "\t".join(
                            [
                                clean_field(q_slug),
                                clean_field(q_name),
                                clean_field(out_slug),
                                clean_field(out_name),
                                h_name,
                                h_tab,
                                ingredients,
                            ]
                        )
                        + "\n"
                    )
                    rows += 1

    print(f"wrote: {OUT_PATH}")
    print(f"rows: {rows}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
