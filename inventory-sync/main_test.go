package main

import "testing"

func TestParseItemList_ExtractsNestedAndCustomNames(t *testing.T) {
	list := []any{
		map[string]any{
			"id":    float64(6412),
			"Count": float64(1),
			"Damage": float64(0),
			"Slot":  float64(5),
			"tag": map[string]any{
				"display": map[string]any{
					"Name": "Quest Bag",
				},
				"Items": []any{
					map[string]any{
						"id":     float64(50),
						"Count":  float64(64),
						"Damage": float64(0),
						"Slot":   float64(1),
						"tag": map[string]any{
							"display": map[string]any{"Name": "Torch Stack"},
						},
					},
				},
			},
		},
	}

	stacks := parseItemList(list, "inventory")
	if len(stacks) != 2 {
		t.Fatalf("expected 2 stacks (container + nested), got %d", len(stacks))
	}

	container := stacks[0]
	if container.ID != 6412 {
		t.Fatalf("unexpected container id: %d", container.ID)
	}
	if container.Custom != "Quest Bag" {
		t.Fatalf("expected container custom name %q, got %q", "Quest Bag", container.Custom)
	}

	nested := stacks[1]
	if nested.ID != 50 {
		t.Fatalf("unexpected nested id: %d", nested.ID)
	}
	if nested.Count != 64 {
		t.Fatalf("expected nested count 64, got %d", nested.Count)
	}
	if nested.Slot != 1 {
		t.Fatalf("expected nested slot from child slot 1, got %d", nested.Slot)
	}
	if nested.Source != "inventory:nested" {
		t.Fatalf("expected nested source inventory:nested, got %q", nested.Source)
	}
	if nested.Custom != "Torch Stack" {
		t.Fatalf("expected nested custom name %q, got %q", "Torch Stack", nested.Custom)
	}
}

func TestParseNestedStacks_RecursesDeepItemsLists(t *testing.T) {
	root := map[string]any{
		"foo": map[string]any{
			"bar": []any{
				map[string]any{
					"id":     float64(20),
					"Count":  float64(12),
					"Damage": float64(0),
					"Slot":   float64(2),
				},
			},
		},
	}

	stacks := parseNestedStacks(root, "inventory:nested", 9, 0)
	if len(stacks) != 1 {
		t.Fatalf("expected 1 nested stack, got %d", len(stacks))
	}
	if stacks[0].ID != 20 {
		t.Fatalf("expected nested id 20, got %d", stacks[0].ID)
	}
	if stacks[0].Slot != 2 {
		t.Fatalf("expected slot from nested stack as 2, got %d", stacks[0].Slot)
	}
}

func TestExtractCustomName_FallbackKeys(t *testing.T) {
	tag := map[string]any{
		"mItemName": "Renamed Backpack",
	}
	if got := extractCustomName(tag); got != "Renamed Backpack" {
		t.Fatalf("expected fallback custom name, got %q", got)
	}
}
