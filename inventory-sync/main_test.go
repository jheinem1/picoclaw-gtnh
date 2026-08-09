package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestParseBlockInventoryExport_SuperChest(t *testing.T) {
	raw := []byte(`{
	  "generated_at":"2026-04-29T22:00:00Z",
	  "inventories":[{
	    "dim":0,
	    "x":100,
	    "y":64,
	    "z":-25,
	    "tile_class":"gregtech.common.tileentities.storage.GT_MetaTileEntity_DigitalChest",
	    "block_id":2442,
	    "block_meta":0,
	    "gt_meta_id":135,
	    "gt_meta_name":"Super Chest I",
	    "block_reg_name":"gregtech:gt.blockmachines",
	    "block_display_name":"Machine",
	    "source":"gregtech-direct",
	    "items":[
	      {"id":7437,"damage":11305,"count":32768,"slot":0,"source":"gregtech-direct","display_name":"Steel Ingot"}
	    ]
	  }]
	}`)

	chests, blocks, generatedAt, stackCount, err := parseBlockInventoryExport(raw)
	if err != nil {
		t.Fatalf("parseBlockInventoryExport failed: %v", err)
	}
	if generatedAt != "2026-04-29T22:00:00Z" || stackCount != 1 {
		t.Fatalf("unexpected export metadata: generatedAt=%q stackCount=%d", generatedAt, stackCount)
	}
	if len(chests) != 1 || chests[0].Source != "block_export" || chests[0].Type != "Super Chest I" {
		t.Fatalf("unexpected chest records: %#v", chests)
	}
	if chests[0].Items[0].ID != 7437 || chests[0].Items[0].Count != 32768 || chests[0].Items[0].Source != "gregtech-direct" {
		t.Fatalf("unexpected exported stack: %#v", chests[0].Items[0])
	}
	if len(blocks) != 1 || blocks[0].Meta != 135 || blocks[0].RegName != "gregtech:gt.blockmachines" || blocks[0].Name != "Super Chest I" {
		t.Fatalf("unexpected block records: %#v", blocks)
	}
}

func TestParseBlockInventoryExportDeduplicatesStableInventoryIdentity(t *testing.T) {
	raw := []byte(`{
	  "generated_at":"2026-07-21T04:33:00Z",
	  "inventories":[
	    {"dim":183,"x":14,"y":43,"z":-6,"inventory_id":"block_export:183:15:43:-6","block_id":4209,"block_display_name":"Reactor Chamber","items":[{"id":4209,"damage":0,"count":1,"slot":0}]},
	    {"dim":183,"x":15,"y":43,"z":-6,"inventory_id":"block_export:183:15:43:-6","block_id":4209,"block_display_name":"Reactor Chamber","items":[{"id":4209,"damage":0,"count":1,"slot":0}]}
	  ]
	}`)

	chests, blocks, _, stackCount, err := parseBlockInventoryExport(raw)
	if err != nil {
		t.Fatalf("parseBlockInventoryExport failed: %v", err)
	}
	if len(chests) != 1 || stackCount != 1 {
		t.Fatalf("stable inventory identity was double-counted: chests=%#v stackCount=%d", chests, stackCount)
	}
	if len(blocks) != 2 {
		t.Fatalf("distinct placed block coordinates should remain searchable, got %#v", blocks)
	}
}

func TestMergeChestRecordsPrefersFreshBlockExportAtSameCoordinate(t *testing.T) {
	existing := []ChestRecord{{Dimension: 183, X: 1, Y: 2, Z: 3, Source: "region", Items: []ItemStack{{ID: 7, Count: 64}}}}
	replacement := []ChestRecord{{Dimension: 183, X: 1, Y: 2, Z: 3, Source: "block_export", Items: []ItemStack{{ID: 7, Count: 8}}}}
	got := mergeChestRecords(existing, replacement, "block_export")
	if len(got) != 1 || got[0].Source != "block_export" || got[0].Items[0].Count != 8 {
		t.Fatalf("cross-source coordinate was not deduplicated: %#v", got)
	}
}

func TestMergeBlockRecordsReplacesWholeSourceSnapshot(t *testing.T) {
	existing := []BlockRecord{
		{Dimension: 183, X: 1, Y: 2, Z: 3, ID: 10, Source: "block_export"},
		{Dimension: 183, X: 4, Y: 5, Z: 6, ID: 11, Source: "region"},
	}
	replacement := []BlockRecord{{Dimension: 183, X: 7, Y: 8, Z: 9, ID: 12, Source: "block_export"}}
	got := mergeBlockRecords(existing, replacement, "block_export")
	if len(got) != 2 {
		t.Fatalf("unexpected merged blocks: %#v", got)
	}
	for _, block := range got {
		if block.X == 1 {
			t.Fatalf("stale block-export record survived replacement: %#v", got)
		}
	}
}

func TestLoadIndexInfersLegacyBlockExportSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.json")
	raw := []byte(`{
	  "source":{"block_inventories_scan_at":"2026-07-20T12:00:00Z","blocks_scan_at":"2026-07-20T12:05:00Z"},
	  "chests":[{"dim":183,"x":1,"y":2,"z":3,"source":"block_export","items":[]}],
	  "blocks":[
	    {"dim":183,"x":1,"y":2,"z":3,"id":10},
	    {"dim":183,"x":4,"y":5,"z":6,"id":11}
	  ]
	}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	idx := loadIndex(path)
	if got := idx.Blocks[0].Source; got != "block_export" {
		t.Fatalf("block matching an exported inventory got source %q", got)
	}
	if got := idx.Blocks[1].Source; got != "region" {
		t.Fatalf("unmatched legacy block got source %q", got)
	}
}

func TestLoadIndexTreatsExportOnlyLegacyBlocksAsExported(t *testing.T) {
	idx := InventoryIndex{
		Source: SourceMeta{BlockInvScanAt: "2026-07-20T12:00:00Z"},
		Blocks: []BlockRecord{{Dimension: 183, X: 1, Y: 2, Z: 3}},
	}
	normalizeLegacyBlockSources(&idx)
	if got := idx.Blocks[0].Source; got != "block_export" {
		t.Fatalf("export-only legacy block got source %q", got)
	}
}

func TestDimPathSupportsPocketDimension(t *testing.T) {
	path, ok := dimPath(183)
	if !ok || path != "world/DIM183/region/" {
		t.Fatalf("dimPath(183) = %q, %t", path, ok)
	}
}

func TestScanBlockInventoriesRetriesTruncatedExport(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
		if attempts < 3 {
			_, _ = w.Write([]byte(`{"generated_at":"2026-07-21T01:00:00Z","inventories":[`))
			return
		}
		_, _ = w.Write([]byte(`{"generated_at":"2026-07-21T01:00:00Z","inventories":[{"dim":183,"x":1,"y":64,"z":2,"block_id":54,"block_meta":0,"block_display_name":"Chest","items":[]}]}`))
	}))
	defer server.Close()

	cfg := Config{
		DatHostBase:   server.URL,
		DatHostServer: "server-1",
		DatHostToken:  "token",
		HTTPTimeout:   time.Second,
		BlockInvPaths: []string{"world/picoclaw/block_inventories.json"},
	}
	chests, blocks, generatedAt, _, err := scanBlockInventories(server.Client(), cfg)
	if err != nil {
		t.Fatalf("scanBlockInventories failed after retry: %v", err)
	}
	if attempts != 3 || generatedAt != "2026-07-21T01:00:00Z" || len(chests) != 1 || len(blocks) != 1 || chests[0].Dimension != 183 {
		t.Fatalf("unexpected retry result: attempts=%d generatedAt=%q chests=%#v blocks=%#v", attempts, generatedAt, chests, blocks)
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
		MEExportPaths: []string{"world/greggpt/me_index.json", "world/picoclaw/me_index.json"},
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
		MEExportPaths: []string{"world/greggpt/me_index.json", "world/picoclaw/me_index.json"},
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

func TestLoadConfig_ExportStaleThresholds(t *testing.T) {
	t.Setenv("DATHOST_SERVER_ID", "server-1")
	t.Setenv("DATHOST_API_TOKEN", "token")
	t.Setenv("INVENTORY_ME_STALE_AFTER_SECONDS", "")
	t.Setenv("INVENTORY_BLOCK_INVENTORIES_STALE_AFTER_SECONDS", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg.MEStaleAfter != 900*time.Second {
		t.Fatalf("unexpected default ME stale threshold: %s", cfg.MEStaleAfter)
	}
	if cfg.BlockInvStaleAfter != 900*time.Second {
		t.Fatalf("unexpected default block inventory stale threshold: %s", cfg.BlockInvStaleAfter)
	}

	t.Setenv("INVENTORY_ME_STALE_AFTER_SECONDS", "120")
	t.Setenv("INVENTORY_BLOCK_INVENTORIES_STALE_AFTER_SECONDS", "45")

	cfg, err = loadConfig()
	if err != nil {
		t.Fatalf("loadConfig with custom thresholds failed: %v", err)
	}
	if cfg.MEStaleAfter != 120*time.Second {
		t.Fatalf("unexpected ME stale threshold: %s", cfg.MEStaleAfter)
	}
	if cfg.BlockInvStaleAfter != 45*time.Second {
		t.Fatalf("unexpected block inventory stale threshold: %s", cfg.BlockInvStaleAfter)
	}
}

func TestFetchDueUsesAttemptTimestampOverExportGeneratedAt(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 20, 0, 0, time.UTC)
	lastExport := "2026-04-30T12:00:00Z"
	lastAttempt := "2026-04-30T12:19:00Z"

	if !fetchDue("", lastExport, now, 5*time.Minute) {
		t.Fatal("expected export timestamp alone to be due")
	}
	if fetchDue(lastAttempt, lastExport, now, 5*time.Minute) {
		t.Fatal("expected recent fetch attempt to suppress retry even when export generated_at is old")
	}
}

func TestFailedChestAttemptUsesAttemptTimestampOverLastSuccessfulScan(t *testing.T) {
	now := time.Date(2026, 8, 6, 6, 30, 0, 0, time.UTC)
	lastSuccess := "2026-07-21T05:29:29Z"
	lastAttempt := "2026-08-06T06:25:00Z"

	if !fetchDue("", lastSuccess, now, 6*time.Hour) {
		t.Fatal("expected a stale successful chest scan with no attempt timestamp to be due")
	}
	if fetchDue(lastAttempt, lastSuccess, now, 6*time.Hour) {
		t.Fatal("expected a recent failed chest attempt to suppress an immediate full-world retry")
	}
}

func TestSourceIsStaleUsesConfiguredThreshold(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 20, 0, 0, time.UTC)
	generatedAt := "2026-04-30T12:05:30Z"

	if sourceIsStale(generatedAt, now, 15*time.Minute) {
		t.Fatal("expected export inside configured stale threshold to be fresh")
	}
	if !sourceIsStale(generatedAt, now, 10*time.Minute) {
		t.Fatal("expected export outside configured stale threshold to be stale")
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

func TestIndexFromData_IncludesExportedBlockInventoryContainersAndBlocks(t *testing.T) {
	index := indexFromData(nil, []ChestRecord{
		{
			Dimension: 0,
			X:         100,
			Y:         64,
			Z:         -25,
			Type:      "Super Chest I",
			Source:    "block_export",
			Items:     []ItemStack{{ID: 7437, Damage: 11305, Count: 32768, Slot: 0, Source: "gregtech-direct"}},
		},
	}, nil, []BlockRecord{
		{Dimension: 0, X: 100, Y: 64, Z: -25, ID: 2442, Meta: 135, RegName: "gregtech:gt.blockmachines", Name: "Super Chest I"},
	}, SourceMeta{BlockInvScanAt: "2026-04-29T22:00:00Z"}, IndexStats{}, BlockIndexStatus{})

	hits := index.ItemIndex["7437:11305"].Chests
	if len(hits) != 1 || hits[0].TotalCount != 32768 || hits[0].Type != "Super Chest I" {
		t.Fatalf("expected exported block inventory item hit, got %#v", hits)
	}
	blockHits := index.BlockIndex["2442:135"].Blocks
	if len(blockHits) != 1 || blockHits[0].Name != "Super Chest I" {
		t.Fatalf("expected exported block location hit, got %#v", blockHits)
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

func TestParseQuestDatabaseExtractsOpenQuestItems(t *testing.T) {
	raw := []byte(`{
	  "questDatabase:9": {
	    "42": {
	      "questID:3": 42,
	      "properties:10": {
	        "name:8": "Make Steel",
	        "desc:8": "Submit steel ingots"
	      },
	      "tasks:9": [{
	        "taskID:8": "bq_standard:retrieval",
	        "requiredItems:9": [{
	          "id:3": 7437,
	          "damage:3": 11305,
	          "Count": 16,
	          "displayName": "Steel Ingot"
	        }]
	      }]
	    }
	  }
	}`)

	quests, err := parseQuestDatabase(raw)
	if err != nil {
		t.Fatalf("parseQuestDatabase failed: %v", err)
	}
	if len(quests) != 1 {
		t.Fatalf("expected one quest, got %#v", quests)
	}
	q := quests[0]
	if q.ID != "42" || q.Title != "Make Steel" || q.Description != "Submit steel ingots" {
		t.Fatalf("unexpected quest metadata: %#v", q)
	}
	if len(q.Tasks) != 1 || len(q.Tasks[0].RequiredItems) != 1 {
		t.Fatalf("expected one required item, got %#v", q.Tasks)
	}
	item := q.Tasks[0].RequiredItems[0]
	if item.ID != 7437 || item.Damage != 11305 || item.Count != 16 || item.DisplayName != "Steel Ingot" {
		t.Fatalf("unexpected required item: %#v", item)
	}
}

func TestParseQuestDatabaseExtractsNBTStyleBetterQuestingIDs(t *testing.T) {
	raw := []byte(`{
	  "questDatabase:9": {
	    "0:10": {
	      "questIDLow:4": -4782315901562449638,
	      "questIDHigh:4": -337303635474561203,
	      "tasks:9": {
	        "0:10": {
	          "taskID:8": "bq_standard:retrieval",
	          "requiredItems:9": {
	            "0:10": {
	              "Damage:2": 148,
	              "id:8": "gregtech:gt.blockmachines",
		            "Count:3": 4
	            }
	          }
	        }
	      },
	      "properties:10": {
	        "betterquesting:10": {
	          "name:8": "Something From Nothing pt 2",
	          "desc:8": "Build the multiblock"
	        }
	      }
	    }
	  }
	}`)

	quests, err := parseQuestDatabase(raw)
	if err != nil {
		t.Fatalf("parseQuestDatabase failed: %v", err)
	}
	if len(quests) != 1 {
		t.Fatalf("expected one quest, got %#v", quests)
	}
	q := quests[0]
	if q.ID != "-337303635474561203:-4782315901562449638" || q.Title != "Something From Nothing pt 2" || q.Description != "Build the multiblock" {
		t.Fatalf("unexpected quest metadata: %#v", q)
	}
	if len(q.Tasks) != 1 || len(q.Tasks[0].RequiredItems) != 1 {
		t.Fatalf("expected one task item, got %#v", q.Tasks)
	}
	if q.Tasks[0].ID != "0" {
		t.Fatalf("expected stable task id 0, got %#v", q.Tasks[0])
	}
	item := q.Tasks[0].RequiredItems[0]
	if item.RegName != "gregtech:gt.blockmachines" || item.Damage != 148 || item.Count != 4 {
		t.Fatalf("unexpected required item: %#v", item)
	}
}

func TestParseQuestLinesMapsTierLineToQuests(t *testing.T) {
	raw := []byte(`{
	  "questLines:9": {
	    "6:10": {
	      "order:3": 6,
	      "questLineIDLow:4": 6,
	      "questLineIDHigh:4": 0,
	      "properties:10": {
	        "betterquesting:10": {
	          "name:8": "Tier 4 - EV"
	        }
	      },
	      "quests:9": {
	        "0:10": {
	          "questIDLow:4": -7880854746900516763,
	          "questIDHigh:4": -3190394247917321884,
	          "x:3": 348,
	          "y:3": 132
	        },
	        "1:10": {
	          "questIDLow:4": 212,
	          "questIDHigh:4": 0,
	          "x:3": 480,
	          "y:3": 276
	        }
	      }
	    }
	  }
	}`)

	byQuest, lines := parseQuestLines(raw)
	if len(lines) != 1 {
		t.Fatalf("expected one quest line, got %#v", lines)
	}
	line := lines[0]
	if line.ID != "6" || line.Name != "Tier 4 - EV" || line.Order != 6 || !line.Tier {
		t.Fatalf("unexpected tier line: %#v", line)
	}
	if byQuest["-3190394247917321884:-7880854746900516763"].Name != "Tier 4 - EV" {
		t.Fatalf("high/low quest id was not mapped: %#v", byQuest)
	}
	if byQuest["212"].Name != "Tier 4 - EV" {
		t.Fatalf("numeric quest id was not mapped: %#v", byQuest)
	}
}

func TestParseQuestProgressMarksCompletedIDs(t *testing.T) {
	raw := []byte(`{
	  "questProgress": {
	    "42": {"questID:3": 42, "completed": true},
	    "43": {"questID:3": 43, "completed": false},
	    "44": true
	  }
	}`)

	got := parseQuestProgress(raw)
	if !got["42"] || !got["44"] {
		t.Fatalf("expected completed quest ids 42 and 44, got %#v", got)
	}
	if got["43"] {
		t.Fatalf("quest 43 should not be complete: %#v", got)
	}
}

func TestParseQuestProgressMarksNBTStyleCompletedIDs(t *testing.T) {
	raw := []byte(`{
	  "questProgress:9": {
	    "0:10": {
	      "questIDLow:4": -5367828810714122460,
	      "questIDHigh:4": 3813996469394622463,
	      "tasks:9": {
	        "0:10": {
	          "completeUsers:9": {"0:8": "0cc6814a-f873-44eb-b8fd-494effdc0126"},
	          "taskID:8": "bq_standard:retrieval"
	        }
	      },
	      "completed:9": {
	        "0:10": {
	          "uuid:8": "0cc6814a-f873-44eb-b8fd-494effdc0126",
	          "claimed:1": 0
	        }
	      }
	    }
	  }
	}`)

	got := parseQuestProgress(raw)
	if !got["3813996469394622463:-5367828810714122460"] {
		t.Fatalf("expected high:low quest id completed, got %#v", got)
	}
	if got["0"] {
		t.Fatalf("task completion should not be treated as quest id 0: %#v", got)
	}
	details := parseQuestProgressRecord(raw).Quests["3813996469394622463:-5367828810714122460"]
	if !details.Completed || details.Claimed || !details.ClaimStatusKnown {
		t.Fatalf("unexpected quest completion details: %#v", details)
	}
	if strings.Join(details.CompletedTasks, ",") != "0" {
		t.Fatalf("expected task 0 completion, got %#v", details.CompletedTasks)
	}
}

func TestParseQuestProgressSelectsClaimForProgressFileOwner(t *testing.T) {
	raw := []byte(`{
	  "questProgress:9": {
	    "0:10": {
	      "questIDLow:4": 42,
	      "questIDHigh:4": 0,
	      "completed:9": {
	        "0:10": {"uuid:8": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "claimed:1": 1},
	        "1:10": {"uuid:8": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "claimed:1": 0}
	      }
	    }
	  }
	}`)

	a := parseQuestProgressRecordForUser(raw, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa").Quests["42"]
	b := parseQuestProgressRecordForUser(raw, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb").Quests["42"]
	if !a.ClaimStatusKnown || !a.Claimed {
		t.Fatalf("unexpected claimed state for a: %#v", a)
	}
	if !b.ClaimStatusKnown || b.Claimed {
		t.Fatalf("unexpected claimed state for b: %#v", b)
	}
}

func TestScanQuestsBuildsDeterministicReadinessAndProgress(t *testing.T) {
	files := map[string]string{
		"world/betterquesting/QuestDatabase.json": `{
		  "questDatabase:9": {
		    "42": {"questID:3": 42, "properties:10": {"name:8": "Make Steel"}},
		    "43": {
		      "questID:3": 43,
		      "preRequisites:11": [42],
		      "properties:10": {"name:8": "Build the EBF"},
		      "tasks:9": {
		        "0:10": {"taskID:8": "bq_standard:retrieval", "requiredItems:9": [{"id:3": 7437, "damage:3": 11305, "Count": 16, "displayName": "Steel Ingot"}]},
		        "1:10": {"taskID:8": "bq_standard:checkbox", "description:8": "Assemble the structure"}
		      }
		    },
		    "44": {"questID:3": 44, "preRequisites:11": [999], "properties:10": {"name:8": "Future Work"}}
		  }
		}`,
		"world/betterquesting/NameCache.json":       `{"nameCache:9":{"a":{"uuid:8":"11111111-1111-1111-1111-111111111111","name:8":"Snow"}}}`,
		"world/betterquesting/QuestingParties.json": `{"questingParties:9":{"7":{"partyID:3":7,"name:8":"Noob Squad","members:9":[{"uuid:8":"11111111-1111-1111-1111-111111111111"}]}}}`,
		"world/betterquesting/QuestProgress/11111111-1111-1111-1111-111111111111.json": `{
		  "questProgress:9": {
		    "0:10": {"questIDLow:4":42,"questIDHigh:4":0,"completed:9":{"0:10":{"uuid:8":"11111111-1111-1111-1111-111111111111","claimed:1":1}}},
		    "1:10": {"questIDLow:4":43,"questIDHigh:4":0,"tasks:9":{"0:10":{"taskID:8":"bq_standard:retrieval","completeUsers:9":{"0:8":"11111111-1111-1111-1111-111111111111"}}}}
		  }
		}`,
	}
	server := questFileServer(t, files)
	defer server.Close()

	index, err := scanQuests(&http.Client{}, Config{
		DatHostBase:    server.URL,
		DatHostServer:  "srv",
		QuestPartyName: "Noob Squad",
		HTTPTimeout:    time.Second,
	}, "")
	if err != nil {
		t.Fatalf("scanQuests failed: %v", err)
	}
	if index.Version != 2 {
		t.Fatalf("index version = %d, want 2", index.Version)
	}
	byID := map[string]QuestRecord{}
	for _, quest := range index.Quests {
		byID[quest.ID] = quest
	}
	if byID["42"].State != "completed_claimed" || strings.Join(byID["42"].Unlocks, ",") != "43" {
		t.Fatalf("unexpected completed quest: %#v", byID["42"])
	}
	if byID["43"].State != "in_progress" || !byID["43"].Ready || byID["43"].CompletionRatio != 0.5 {
		t.Fatalf("unexpected in-progress quest: %#v", byID["43"])
	}
	if strings.Join(byID["43"].Tasks[0].CompletedBy, ",") != "Snow" {
		t.Fatalf("expected Snow task progress, got %#v", byID["43"].Tasks)
	}
	if byID["44"].State != "locked" || strings.Join(byID["44"].BlockedBy, ",") != "999" {
		t.Fatalf("unexpected locked quest: %#v", byID["44"])
	}
	if index.Stats.CompletedCount != 1 || index.Stats.InProgressCount != 1 || index.Stats.LockedCount != 1 {
		t.Fatalf("unexpected quest stats: %#v", index.Stats)
	}
}

func TestParseQuestingPartiesResolvesNoobSquadMembers(t *testing.T) {
	raw := []byte(`{
	  "questingParties:9": {
	    "7": {
	      "partyID:3": 7,
	      "name:8": "Noob Squad",
	      "members:9": [
	        {"uuid:8": "11111111-1111-1111-1111-111111111111"},
	        {"uuid:8": "22222222222222222222222222222222"}
	      ]
	    }
	  }
	}`)

	parties := parseQuestingParties(raw, map[string]string{
		"11111111111111111111111111111111": "SugarYCoffee",
		"22222222222222222222222222222222": "Stevobbo",
	})
	if len(parties) != 1 {
		t.Fatalf("expected one party, got %#v", parties)
	}
	party := parties[0]
	if party.Name != "Noob Squad" || party.ID != "7" || party.MemberCount != 2 {
		t.Fatalf("unexpected party metadata: %#v", party)
	}
	if party.Members[0].Name != "Stevobbo" || party.Members[1].Name != "SugarYCoffee" {
		t.Fatalf("expected resolved sorted members, got %#v", party.Members)
	}
}

func TestScanQuestsUsesSelectedPartyProgressOnly(t *testing.T) {
	files := map[string]string{
		"world/betterquesting/QuestDatabase.json": `{
		  "questDatabase:9": {
		    "42": {"questID:3": 42, "properties:10": {"name:8": "Make Steel"}},
		    "43": {"questID:3": 43, "properties:10": {"name:8": "Make Bronze"}}
		  },
		  "questLines:9": {
		    "6:10": {
		      "order:3": 6,
		      "questLineIDLow:4": 6,
		      "questLineIDHigh:4": 0,
		      "properties:10": {"betterquesting:10": {"name:8": "Tier 4 - EV"}},
		      "quests:9": {
		        "0:10": {"questIDLow:4": 42, "questIDHigh:4": 0},
		        "1:10": {"questIDLow:4": 43, "questIDHigh:4": 0}
		      }
		    }
		  }
		}`,
		"world/betterquesting/NameCache.json": `{
		  "nameCache:9": {
		    "a": {"uuid:8": "11111111-1111-1111-1111-111111111111", "name:8": "SugarYCoffee"},
		    "b": {"uuid:8": "22222222-2222-2222-2222-222222222222", "name:8": "Stevobbo"},
		    "c": {"uuid:8": "33333333-3333-3333-3333-333333333333", "name:8": "OtherPlayer"}
		  }
		}`,
		"world/betterquesting/QuestingParties.json": `{
		  "questingParties:9": {
		    "7": {
		      "partyID:3": 7,
		      "name:8": "Noob Squad",
		      "members:9": [
		        {"uuid:8": "11111111-1111-1111-1111-111111111111"},
		        {"uuid:8": "22222222-2222-2222-2222-222222222222"}
		      ]
		    }
		  }
		}`,
		"world/betterquesting/QuestProgress/11111111-1111-1111-1111-111111111111.json": `{"questProgress":{"42":{"questID:3":42,"completed":true}}}`,
		"world/betterquesting/QuestProgress/22222222-2222-2222-2222-222222222222.json": `{"questProgress":{"42":{"questID:3":42,"completed":true}}}`,
		"world/betterquesting/QuestProgress/33333333-3333-3333-3333-333333333333.json": `{"questProgress":{"43":{"questID:3":43,"completed":true}}}`,
	}
	server := questFileServer(t, files)
	defer server.Close()

	index, err := scanQuests(&http.Client{}, Config{
		DatHostBase:    server.URL,
		DatHostServer:  "srv",
		QuestPartyName: "Noob Squad",
		HTTPTimeout:    time.Second,
	}, "2026-05-02T00:00:00Z")
	if err != nil {
		t.Fatalf("scanQuests failed: %v", err)
	}
	if index.Party.Name != "Noob Squad" || index.Party.MemberCount != 2 {
		t.Fatalf("unexpected selected party: %#v", index.Party)
	}
	if index.Source.ProgressFiles != 3 || index.Source.MatchedProgressFiles != 2 {
		t.Fatalf("unexpected progress file counts: %#v", index.Source)
	}
	if index.Stats.CompletedCount != 1 || index.Stats.OpenCount != 1 {
		t.Fatalf("unexpected quest stats: %#v", index.Stats)
	}
	if len(index.QuestLines) != 1 || index.QuestLines[0].Name != "Tier 4 - EV" || index.QuestLines[0].OpenCount != 1 || index.QuestLines[0].CompletedCount != 1 {
		t.Fatalf("unexpected quest line stats: %#v", index.QuestLines)
	}
	byID := map[string]QuestRecord{}
	for _, quest := range index.Quests {
		byID[quest.ID] = quest
	}
	if !byID["42"].Completed || strings.Join(byID["42"].CompletedBy, ",") != "Stevobbo,SugarYCoffee" {
		t.Fatalf("expected quest 42 completed by party members, got %#v", byID["42"])
	}
	if byID["43"].Completed {
		t.Fatalf("quest 43 should ignore non-party progress: %#v", byID["43"])
	}
	if byID["43"].QuestLine != "Tier 4 - EV" || !byID["43"].TierQuestLine {
		t.Fatalf("quest 43 should be mapped to tier quest line: %#v", byID["43"])
	}
}

func TestScanQuestsWarnsWhenSelectedPartyMissing(t *testing.T) {
	files := map[string]string{
		"world/betterquesting/QuestDatabase.json":   `{"questDatabase:9":{"42":{"questID:3":42,"properties:10":{"name:8":"Make Steel"}}}}`,
		"world/betterquesting/NameCache.json":       `{"nameCache:9":{}}`,
		"world/betterquesting/QuestingParties.json": `{"questingParties:9":{}}`,
	}
	server := questFileServer(t, files)
	defer server.Close()

	index, err := scanQuests(&http.Client{}, Config{
		DatHostBase:    server.URL,
		DatHostServer:  "srv",
		QuestPartyName: "Noob Squad",
		HTTPTimeout:    time.Second,
	}, "")
	if err != nil {
		t.Fatalf("scanQuests failed: %v", err)
	}
	joined := strings.Join(index.Warnings, "\n")
	if !strings.Contains(joined, `quest party "Noob Squad" not found`) {
		t.Fatalf("missing selected party warning: %#v", index.Warnings)
	}
	if index.Stats.CompletedCount != 0 || index.Stats.OpenCount != 1 {
		t.Fatalf("unexpected stats for missing party: %#v", index.Stats)
	}
}

func questFileServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") == "world/betterquesting/QuestProgress/" {
			var entries []string
			for path := range files {
				if strings.HasPrefix(path, "world/betterquesting/QuestProgress/") {
					entries = append(entries, `{"path":"`+path+`"}`)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[" + strings.Join(entries, ",") + "]"))
			return
		}
		const prefix = "/game-servers/srv/files/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if data, ok := files[path]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(data))
			return
		}
		http.NotFound(w, r)
	}))
}
