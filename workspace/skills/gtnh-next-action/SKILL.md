---
name: gtnh-next-action
description: Analyze GTNH questbook, freeform task log, recipes, and inventory to recommend one easy next action.
---

# GTNH Next Action

Use this skill only for requests like "greg what do I need to do" or "what should I do next".

## Goal

Return exactly one actionable GTNH recommendation. Prefer evidence-backed work that can be completed or advanced with currently indexed materials.

## Sources Of Truth

- Questbook facts must come from `sh gtnh_quests ...`.
- Task-log facts must come from `sh gtnh_tasks ...`.
- Inventory/material facts must come from `sh gtnh_inventory ...`.
- Recipe facts must come from `recipe_sql`; item identity facts must come from `sh gtnh_find_item` or `sh gtnh_item`.
- Do not invent quest progress, item counts, or material availability.

## Workflow

1. Inspect open quests and open tasks.
2. Treat task-log requirements as inferred unless the task text explicitly lists requirements.
3. For freeform player tasks, infer concrete deliverables and likely material requirements from the task title/description, then verify with GTNH lookup tools.
4. Check all indexed inventory scopes: players, containers, and ME.
5. Choose one best candidate, not a ranked shortlist.
6. If task text is too vague, prefer a concrete open quest with item requirements or return a low-confidence recommendation with the smallest useful next clarification.

## Required Output Shape

Return JSON with:

- `recommendation`: one short task title.
- `source`: `questbook`, `task_log`, or `questbook+task_log`.
- `why_easy`: concise reason.
- `next_step`: one concrete player action.
- `confidence`: `high`, `medium`, or `low`.
- `inferred_requirements`: array of strings.
- `available_materials`: array of strings with checked evidence.
- `missing_materials`: array of strings.
- `evidence`: array of source/tool facts.
- `freshness`: concise inventory and quest freshness summary.

Never return multiple recommendations.
