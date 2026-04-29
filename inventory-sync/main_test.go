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

func TestParseTileEntityItems_GregTechDirectStackCount(t *testing.T) {
	te := map[string]any{
		"id":         "GT_MetaTileEntity_DigitalChest",
		"mItemCount": int64(32768),
		"mItemStack": map[string]any{
			"id":     int16(7437),
			"Count":  int8(1),
			"Damage": int16(11305),
		},
	}

	stacks := parseTileEntityItems(te)
	if len(stacks) != 1 {
		t.Fatalf("expected 1 direct stack, got %d: %#v", len(stacks), stacks)
	}
	if stacks[0].ID != 7437 || stacks[0].Damage != 11305 {
		t.Fatalf("unexpected direct stack item: %#v", stacks[0])
	}
	if stacks[0].Count != 32768 {
		t.Fatalf("expected mItemCount override 32768, got %d", stacks[0].Count)
	}
	if stacks[0].Source != "tile" {
		t.Fatalf("expected source tile, got %q", stacks[0].Source)
	}
}

func TestParseTileEntityItems_RecursesMachineInventoriesWithoutTopLevelDoubleCount(t *testing.T) {
	te := map[string]any{
		"id": "gregtech:machine",
		"mInventory": []any{
			map[string]any{
				"id":     float64(50),
				"Count":  float64(64),
				"Damage": float64(0),
				"Slot":   float64(2),
			},
		},
		"machineState": map[string]any{
			"fluidHatch": map[string]any{
				"savedStack": map[string]any{
					"id":     float64(7437),
					"Count":  float64(3),
					"Damage": float64(11305),
					"Slot":   float64(7),
				},
			},
		},
	}

	stacks := parseTileEntityItems(te)
	if len(stacks) != 2 {
		t.Fatalf("expected top-level and nested stacks without double count, got %d: %#v", len(stacks), stacks)
	}
	if stacks[0].ID != 50 || stacks[0].Count != 64 || stacks[0].Source != "tile" {
		t.Fatalf("unexpected top-level stack: %#v", stacks[0])
	}
	if stacks[1].ID != 7437 || stacks[1].Count != 3 || stacks[1].Slot != 7 || stacks[1].Source != "tile:nested" {
		t.Fatalf("unexpected nested stack: %#v", stacks[1])
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

func TestScanMEFetchesConfiguredPath(t *testing.T) {
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
		MEExportPaths:  []string{"world/greggpt/me_index.json", "world/picoclaw/me_index.json"},
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

func TestScanMEFallsBackToPicoClawPath(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		if r.URL.Path != "/game-servers/server-1/files/world/picoclaw/me_index.json" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"generated_at":"2026-04-29T21:03:50Z","networks":[]}`))
	}))
	defer server.Close()

	cfg := Config{
		DatHostBase:   server.URL,
		DatHostServer: "server-1",
		DatHostToken:  "token",
		HTTPTimeout:   time.Second,
		MEExportPaths:  []string{"world/greggpt/me_index.json", "world/picoclaw/me_index.json"},
	}

	records, generatedAt, stackCount, err := scanME(server.Client(), cfg)
	if err != nil {
		t.Fatalf("scanME failed: %v", err)
	}
	if len(records) != 0 || stackCount != 0 {
		t.Fatalf("expected empty ME export, got records=%#v stackCount=%d", records, stackCount)
	}
	if generatedAt != "2026-04-29T21:03:50Z" {
		t.Fatalf("unexpected generated_at: %q", generatedAt)
	}
	if len(requested) != 2 {
		t.Fatalf("expected primary and fallback ME fetches, got %d requests: %#v", len(requested), requested)
	}
	if !strings.Contains(requested[0], "greggpt") || !strings.Contains(requested[1], "picoclaw") {
		t.Fatalf("scanME requested unexpected paths: %#v", requested)
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
	}, nil, SourceMeta{MEScanAt: "2026-04-28T12:00:00Z"}, IndexStats{}, BlockIndexStatus{})

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

func setTestNibble(buf []byte, idx int, value int) {
	off := idx >> 1
	if idx&1 == 1 {
		buf[off] = (buf[off] & 0x0f) | byte((value&0x0f)<<4)
	} else {
		buf[off] = (buf[off] & 0xf0) | byte(value&0x0f)
	}
}

func TestDecodeSectionBlocks_BlocksDataAdd(t *testing.T) {
	blocks := make([]byte, 4096)
	data := make([]byte, 2048)
	add := make([]byte, 2048)
	idx := (7 << 8) | (8 << 4) | 3
	blocks[idx] = byte(300 & 0xff)
	setTestNibble(add, idx, 300>>8)
	setTestNibble(data, idx, 5)

	got := decodeSectionBlocks(map[string]any{
		"Y":      int8(4),
		"Blocks": blocks,
		"Data":   data,
		"Add":    add,
	}, 0, 2, -1, &BlockBounds{Dim: 0, MinX: 30, MaxX: 40, MinY: 70, MaxY: 72, MinZ: -10, MaxZ: -5}, map[string]bool{"300:5": true}, nil)

	if len(got) != 1 {
		t.Fatalf("expected one decoded block, got %#v", got)
	}
	if got[0].ID != 300 || got[0].Meta != 5 || got[0].X != 35 || got[0].Y != 71 || got[0].Z != -8 {
		t.Fatalf("unexpected decoded block: %#v", got[0])
	}
}

func TestDecodeSectionBlocks_Blocks16Data16(t *testing.T) {
	blocks16 := make([]byte, 8192)
	data16 := make([]byte, 8192)
	idx := (1 << 8) | (2 << 4) | 3
	off := idx * 2
	blocks16[off] = 0x10
	blocks16[off+1] = 0x01
	data16[off] = 0x01
	data16[off+1] = 0x01

	got := decodeSectionBlocks(map[string]any{
		"Y":        int8(0),
		"Blocks16": blocks16,
		"Data16":   data16,
	}, 1, 0, 0, nil, map[string]bool{"4097:257": true}, map[string]blockMeta{"4097:257": blockMeta{RegName: "mod:block", Name: "Block"}})

	if len(got) != 1 {
		t.Fatalf("expected one decoded Blocks16 block, got %#v", got)
	}
	if got[0].ID != 4097 || got[0].Meta != 257 || got[0].RegName != "mod:block" || got[0].Name != "Block" {
		t.Fatalf("unexpected Blocks16 decoded block: %#v", got[0])
	}
}

func TestIndexFromData_IncludesBlockHits(t *testing.T) {
	index := indexFromData(nil, nil, nil, []BlockRecord{
		{Dimension: 0, X: 1, Y: 64, Z: 2, ID: 300, Meta: 5},
	}, SourceMeta{BlocksScanAt: "2026-04-28T12:00:00Z"}, IndexStats{}, BlockIndexStatus{Enabled: true})

	hits := index.BlockIndex["300:5"].Blocks
	if len(hits) != 1 {
		t.Fatalf("expected one block hit, got %#v", hits)
	}
	if index.Stats.BlockCount != 1 || index.Stats.IndexedBlockKeys != 1 {
		t.Fatalf("unexpected block stats: %#v", index.Stats)
	}
}
