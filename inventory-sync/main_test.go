package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseItemList_ExtractsNestedAndCustomNames(t *testing.T) {
	list := []any{
		map[string]any{
			"id":     float64(6412),
			"Count":  float64(1),
			"Damage": float64(0),
			"Slot":   float64(5),
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

func TestParseMEExport_Networks(t *testing.T) {
	raw := []byte(`{
	  "generated_at":"2026-04-28T12:00:00Z",
	  "networks":[{
	    "network_id":"main",
	    "label":"Main ME",
	    "dim":0,
	    "x":10,
	    "y":64,
	    "z":20,
	    "items":[
	      {"id":7437,"damage":11305,"count":2048,"reg_name":"gregtech:gt.metaitem.01","display_name":"Steel Ingot"}
	    ]
	  }]
	}`)

	records, generatedAt, stackCount, err := parseMEExport(raw)
	if err != nil {
		t.Fatalf("parseMEExport failed: %v", err)
	}
	if generatedAt != "2026-04-28T12:00:00Z" {
		t.Fatalf("unexpected generated_at: %q", generatedAt)
	}
	if stackCount != 1 {
		t.Fatalf("expected one ME stack, got %d", stackCount)
	}
	if len(records) != 1 {
		t.Fatalf("expected one ME network, got %d", len(records))
	}
	if records[0].Label != "Main ME" || records[0].Items[0].Count != 2048 {
		t.Fatalf("unexpected ME record: %#v", records[0])
	}
}

func TestScanMEFetchesGregGPTPathOnly(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		if r.URL.Path != "/game-servers/server-1/files/world/greggpt/me_index.json" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"generated_at":"2026-04-28T12:00:00Z","networks":[]}`))
	}))
	defer server.Close()

	cfg := Config{
		DatHostBase:   server.URL,
		DatHostServer: "server-1",
		DatHostToken:  "token",
		HTTPTimeout:   time.Second,
	}

	records, generatedAt, stackCount, err := scanME(server.Client(), cfg)
	if err != nil {
		t.Fatalf("scanME failed: %v", err)
	}
	if len(records) != 0 || stackCount != 0 {
		t.Fatalf("expected empty ME export, got records=%#v stackCount=%d", records, stackCount)
	}
	if generatedAt != "2026-04-28T12:00:00Z" {
		t.Fatalf("unexpected generated_at: %q", generatedAt)
	}
	if len(requested) != 1 {
		t.Fatalf("expected exactly one ME fetch with no fallback, got %d requests: %#v", len(requested), requested)
	}
	if !strings.Contains(requested[0], "greggpt") {
		t.Fatalf("scanME requested unexpected ME export path: %q", requested[0])
	}
}

func TestIndexFromData_IncludesMEHits(t *testing.T) {
	index := indexFromData(nil, nil, []MERecord{
		{
			NetworkID: "main",
			Label:     "Main ME",
			Dimension: 0,
			Pos:       Position{X: 10, Y: 64, Z: 20},
			Items: []MEItemStack{
				{ID: 7437, Damage: 11305, Count: 2048, DisplayName: "Steel Ingot"},
			},
		},
	}, SourceMeta{MEScanAt: "2026-04-28T12:00:00Z"}, IndexStats{})

	hits := index.ItemIndex["7437:11305"].ME
	if len(hits) != 1 {
		t.Fatalf("expected one ME hit, got %#v", hits)
	}
	if hits[0].TotalCount != 2048 {
		t.Fatalf("expected count 2048, got %d", hits[0].TotalCount)
	}
	if index.Version != 2 {
		t.Fatalf("expected index version 2, got %d", index.Version)
	}
}
