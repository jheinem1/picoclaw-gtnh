#!/usr/bin/env python3
import csv
import json
import os
import re
import sys
import time


BASE = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
ITEMS = os.environ.get("GTNH_ITEMS_INDEX", os.path.join(BASE, "gtnh-data", "index", "item_index.tsv"))
RECIPES = os.environ.get("GTNH_RECIPES_INDEX", os.path.join(BASE, "gtnh-data", "index", "recipe_index.tsv"))
OREDICT = os.environ.get("GTNH_OREDICT_INDEX", os.path.join(BASE, "gtnh-data", "index", "oredict_index.tsv"))
TIERS = {"ulv", "lv", "mv", "hv", "ev", "iv", "luv", "zpm", "uv", "uhv", "uev", "uiv", "umv", "uxv"}


def fail(message):
    print(json.dumps({"ok": False, "error": message}, separators=(",", ":")))
    raise SystemExit(1)


def age_text(path):
    if not os.path.exists(path):
        return "missing"
    age = max(0, int(time.time() - os.path.getmtime(path)))
    if age < 60:
        return f"{age}s old"
    if age < 3600:
        return f"{age // 60}m old"
    if age < 172800:
        return f"{age // 3600}h old"
    return f"{age // 86400}d old"


def freshness():
    return {
        "item_index": age_text(ITEMS),
        "recipe_index": age_text(RECIPES),
        "oredict_index": age_text(OREDICT),
    }


def norm(value):
    value = re.sub(r"(?i)§[0-9a-fk-or]", "", value or "")
    value = re.sub(r"[^a-z0-9]+", " ", value.lower()).strip()
    return re.sub(r" +", " ", value)


def tier_alias(display):
    m = re.match(r"^(.+?)\s*\(([A-Za-z0-9]+)\)\s*$", display or "")
    if not m:
        return ""
    return norm(f"{m.group(2)} {m.group(1)}")


def tier_display(query):
    parts = query.lower().split()
    if len(parts) < 2 or parts[0] not in TIERS:
        return ""
    return f'{" ".join(parts[1:])} ({parts[0]})'


def item_rows():
    with open(ITEMS, newline="", encoding="utf-8", errors="replace") as handle:
        reader = csv.reader(handle, delimiter="\t")
        next(reader, None)
        for row in reader:
            if len(row) < 4:
                continue
            yield row


def recipe_rows():
    with open(RECIPES, newline="", encoding="utf-8", errors="replace") as handle:
        reader = csv.reader(handle, delimiter="\t")
        next(reader, None)
        for row in reader:
            if len(row) < 7:
                continue
            yield row


def item_obj(row):
    return {"slug": row[0], "display_name": row[1], "reg_name": row[2], "name": row[3]}


def resolve_items(query, limit=20):
    qn = norm(query)
    tq = tier_display(query)
    exact = []
    candidates = []
    tokens = [tok for tok in qn.split() if len(tok) >= 3]
    for row in item_rows():
        aliases = [norm(row[1]), tier_alias(row[1]), norm(row[2]), norm(row[3])]
        display_lower = (row[1] or "").lower()
        if qn in aliases or (tq and display_lower == tq):
            exact.append(item_obj(row))
            if len(exact) >= limit:
                break
            continue
        joined = " ".join(aliases)
        score = 1_000_000
        if any(qn and qn in alias for alias in aliases):
            score = 50 + len(aliases[0])
        else:
            hits = sum(1 for tok in tokens if tok in joined)
            if hits:
                score = 200 - hits * 30 + len(aliases[0])
        if score < 1_000_000:
            candidates.append((score, aliases[0], item_obj(row)))
    if exact:
        return exact
    candidates.sort(key=lambda item: (item[0], item[1]))
    seen = set()
    out = []
    for _, _, obj in candidates:
        if obj["slug"] in seen:
            continue
        seen.add(obj["slug"])
        out.append(obj)
        if len(out) >= limit:
            break
    return out


def find_item(args):
    query = " ".join(args).strip()
    if not query:
        fail("usage: find-item <text>")
    out = {"ok": True, "query": query, "oredict": False, "items": resolve_items(query)}
    out["freshness"] = freshness()
    print(json.dumps(out, separators=(",", ":")))


def resolve_recipes(args):
    query = " ".join(args).strip()
    if not query:
        fail("usage: resolve-recipes <text>")
    items = resolve_items(query, 1)
    if not items:
        fail("item not found")
    slug = items[0]["slug"]
    recipes = []
    for row in recipe_rows():
        if row[0] != slug and row[2] != slug:
            continue
        recipes.append({
            "query_slug": row[0],
            "query_name": row[1],
            "out_slug": row[2],
            "out_name": row[3],
            "handler": row[4],
            "tab": row[5],
            "ingredients": row[6],
        })
        if len(recipes) >= 30:
            break
    out = {
        "ok": True,
        "query": query,
        "slug": slug,
        "recipes": recipes,
        "matched_items": items,
        "confidence": "single-best",
        "sources": ["gtnh-data/index/item_index.tsv", "gtnh-data/index/recipe_index.tsv"],
        "freshness": freshness(),
    }
    print(json.dumps(out, separators=(",", ":")))


def main():
    if len(sys.argv) < 2:
        fail("usage: gtnh_query_fast.py find-item|resolve-recipes <text>")
    if sys.argv[1] == "find-item":
        find_item(sys.argv[2:])
    elif sys.argv[1] == "resolve-recipes":
        resolve_recipes(sys.argv[2:])
    else:
        fail("unsupported command")


if __name__ == "__main__":
    main()
