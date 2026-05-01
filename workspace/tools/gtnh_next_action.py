#!/usr/bin/env python3
import argparse
import json
import os
import re
import subprocess
from pathlib import Path


def workspace_dir() -> Path:
    if os.environ.get("GTNH_WORKSPACE"):
        return Path(os.environ["GTNH_WORKSPACE"]).resolve()
    return Path(__file__).resolve().parents[1]


def load_json(path: Path, fallback):
    try:
        with path.open("r", encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return fallback
    except json.JSONDecodeError:
        return fallback


def run_workspace(args, cwd: Path) -> str:
    try:
        proc = subprocess.run(
            args,
            cwd=str(cwd),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=20,
            check=False,
        )
    except Exception as exc:
        return f"error: {exc}"
    out = proc.stdout.strip()
    err = proc.stderr.strip()
    if proc.returncode != 0 and err:
        return f"error: {err}"
    return out or err


def task_rows(tasks_json):
    if isinstance(tasks_json, dict):
        for key in ("tasks", "items", "rows"):
            if isinstance(tasks_json.get(key), list):
                return tasks_json[key]
        columns = tasks_json.get("columns")
        if isinstance(columns, dict):
            rows = []
            for values in columns.values():
                if isinstance(values, list):
                    rows.extend(values)
            return rows
    if isinstance(tasks_json, list):
        return tasks_json
    return []


def text_of(row):
    if not isinstance(row, dict):
        return str(row)
    parts = []
    for key in ("title", "description"):
        value = row.get(key)
        if value:
            parts.append(str(value))
    return ". ".join(parts)


def infer_queries(text):
    text = re.sub(r"#\d+", "", text)
    patterns = [
        r"\bbuild\s+(?:a|an|the)?\s*([^.;,\n]+)",
        r"\bmake\s+(?:a|an|the)?\s*([^.;,\n]+)",
        r"\bcraft\s+(?:a|an|the)?\s*([^.;,\n]+)",
        r"\bautomate\s+([^.;,\n]+)",
        r"\bsetup\s+(?:a|an|the)?\s*([^.;,\n]+)",
    ]
    out = []
    for pat in patterns:
        for m in re.finditer(pat, text, re.I):
            q = re.sub(r"\b(line|chain|setup|system)$", "", m.group(1), flags=re.I).strip()
            if q and q.lower() not in {x.lower() for x in out}:
                out.append(q)
    words = text.strip()
    if not out and 3 <= len(words) <= 80:
        out.append(words)
    return out[:3]


def quest_score(quest):
    tasks = quest.get("tasks") or []
    item_count = 0
    for task in tasks:
        item_count += len(task.get("required_items") or [])
    score = 40
    if item_count:
        score += min(30, item_count * 6)
    if quest.get("description"):
        score += 4
    return score


def task_score(row):
    status = str(row.get("kanban_status") or row.get("status") or "").lower() if isinstance(row, dict) else ""
    score = 35
    if status == "doing":
        score += 12
    if status == "paused":
        score -= 30
    if isinstance(row, dict) and row.get("description"):
        score += 8
    return score


def material_summary_for_quest(quest):
    available = []
    missing = []
    inferred = []
    for task in quest.get("tasks") or []:
        for item in task.get("required_items") or []:
            name = item.get("display_name") or item.get("reg_name") or (
                f"{item.get('id', 0)}:{item.get('damage', 0)}" if item.get("id") else "unknown item"
            )
            count = item.get("count") or 1
            inferred.append(f"{name} x{count}")
            available.append(f"{name} x{count} requirement identified in questbook; verify exact count in inventory if needed")
    return inferred, available, missing


def recommend(args):
    ws = workspace_dir()
    skill = ws / "skills" / "gtnh-next-action" / "SKILL.md"
    quest_index = load_json(ws / "state" / "quest_index.json", {})
    quest_status = load_json(ws / "state" / "quest_status.json", {})
    inventory_status = load_json(ws / "state" / "inventory_status.json", {})

    task_json_raw = run_workspace(["sh", "gtnh_tasks", "board-json"], ws)
    try:
        tasks_json = json.loads(task_json_raw)
    except json.JSONDecodeError:
        tasks_json = {}

    candidates = []
    for quest in quest_index.get("quests") or []:
        if quest.get("completed"):
            continue
        candidates.append(("questbook", quest_score(quest), quest))

    for row in task_rows(tasks_json):
        if isinstance(row, dict) and str(row.get("kanban_status") or row.get("status") or "").lower() == "done":
            continue
        candidates.append(("task_log", task_score(row), row))

    if not candidates:
        return {
            "recommendation": "No open quest or task is indexed yet",
            "source": "none",
            "why_easy": "There is no reliable candidate in the current quest/task indexes.",
            "next_step": "Refresh quest, task, and inventory indexes, then ask again.",
            "confidence": "low",
            "inferred_requirements": [],
            "available_materials": [],
            "missing_materials": ["open quest/task index data"],
            "evidence": [f"skill={skill}", "no candidates found"],
            "freshness": freshness(quest_status, inventory_status),
        }

    candidates.sort(key=lambda row: row[1], reverse=True)
    source, score, obj = candidates[0]
    evidence = [f"skill={skill}", f"candidate_score={score}", f"source={source}"]
    inferred = []
    available = []
    missing = []

    if source == "questbook":
        title = obj.get("title") or f"Quest {obj.get('id')}"
        inferred, available, missing = material_summary_for_quest(obj)
        evidence.append(f"quest_id={obj.get('id')}")
        why = "It is an open quest with concrete indexed questbook data."
        next_step = f"Open quest {obj.get('id')} and complete: {title}."
        confidence = "medium" if inferred else "low"
    else:
        title = str(obj.get("title") if isinstance(obj, dict) else obj)
        queries = infer_queries(text_of(obj))
        inferred = [f"inferred deliverable: {q}" for q in queries]
        for q in queries[:2]:
            item = run_workspace(["sh", "gtnh_find_item", q], ws)
            evidence.append(f"item_lookup[{q}]=" + one_line(item, 240))
            inv = run_workspace(["sh", "gtnh_inventory", "find-item", "--query", q, "--scope", "all", "--limit", "5"], ws)
            evidence.append(f"inventory_lookup[{q}]=" + one_line(inv, 240))
            if "Inventory find" in inv or "Resolved item" in inv:
                available.append(f"{q}: inventory lookup ran; see evidence for locations/counts")
            elif "ambiguous" in inv.lower():
                missing.append(f"{q}: ambiguous item identity")
            else:
                missing.append(f"{q}: not found directly in inventory")
        why = "It is an open user task with inferred deliverables and bounded GTNH lookups."
        next_step = f"Work on task: {title}."
        confidence = "medium" if available else "low"

    return {
        "recommendation": title,
        "source": source,
        "why_easy": why,
        "next_step": next_step,
        "confidence": confidence,
        "inferred_requirements": inferred,
        "available_materials": available,
        "missing_materials": missing,
        "evidence": evidence,
        "freshness": freshness(quest_status, inventory_status),
    }


def one_line(text, limit):
    text = " ".join(str(text).split())
    if len(text) > limit:
        return text[: limit - 3] + "..."
    return text


def freshness(quest_status, inventory_status):
    parts = []
    q_scan = ((quest_status.get("source") or {}).get("quests_scan_at")) if isinstance(quest_status, dict) else None
    if q_scan:
        parts.append(f"quests={q_scan}")
    inv_source = inventory_status.get("source") if isinstance(inventory_status, dict) else {}
    for key, label in (("players_scan_at", "players"), ("chests_scan_at", "containers"), ("me_scan_at", "me")):
        value = (inv_source or {}).get(key)
        if value:
            parts.append(f"{label}={value}")
    return "; ".join(parts) if parts else "freshness unavailable"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["recommend"])
    parser.add_argument("--user", default="")
    parser.add_argument("--channel", default="")
    parser.add_argument("--message", default="")
    args = parser.parse_args()
    print(json.dumps(recommend(args), indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
