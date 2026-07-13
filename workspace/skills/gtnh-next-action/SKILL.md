---
name: gtnh-next-action
description: Present deterministic GTNH quest-plan results for next-action and to-do requests.
---

# GTNH Next Action

Use this skill only for requests like "greg what do I need to do" or "what should I do next".

## Goal

Use the deterministic planner as the source of truth. Return one actionable recommendation for singular requests, or the requested number of entries for explicit plan/list requests.

## Sources Of Truth

- Questbook facts must come from `sh gtnh_quests ...`.
- Task-log facts must come from `sh gtnh_tasks ...`.
- Inventory/material facts must come from `sh gtnh_inventory ...`.
- Recipe facts and recipe-index item metadata must come from `recipe_sql`; live storage availability must come from `sh gtnh_inventory ...`.
- Do not invent quest progress, item counts, or material availability.

## Workflow

1. Call `next_action_recommendation` for a singular request or `next_action_plan` for an explicit plan/list.
2. Pass the player name when known so task completion, ownership, and reward claims can be personalized.
3. Pass the original message so tier constraints are applied deterministically.
4. Present the returned `next_step`, material delta, reason, confidence, and freshness concisely.
5. Do not recommend candidates marked `eligible=false`, recalculate scores, or claim unresolved materials are available.
6. If the planner returns no candidate, report its smallest next step instead of guessing from raw open quests.

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

For singular requests, return only the highest-ranked recommendation. For explicit plan/list requests, preserve planner order and the requested limit.
