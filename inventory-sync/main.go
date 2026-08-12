package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled            bool
	DatHostToken       string
	DatHostEmail       string
	DatHostPassword    string
	DatHostServer      string
	DatHostBase        string
	WorkDir            string
	StateFile          string
	PlayersInterval    time.Duration
	ChestsInterval     time.Duration
	MEInterval         time.Duration
	MEStaleAfter       time.Duration
	MEExportPaths      []string
	QuestsInterval     time.Duration
	QuestPartyName     string
	BlockInvInterval   time.Duration
	BlockInvStaleAfter time.Duration
	BlockInvPaths      []string
	BlocksInterval     time.Duration
	HTTPTimeout        time.Duration
	MaxRegionFiles     int
	ScanDims           []int
	ChestBounds        *ChestBounds
	ChestAreas         []ChestBounds
	BlockBounds        *BlockBounds
	BlockAllowlist     map[string]bool
	BlockRegistryFile  string
	DefaultResultLimit int
	MaxResults         int
	LoopSleep          time.Duration
}

type ChestBounds struct {
	Dim  int
	MinX int
	MaxX int
	MinZ int
	MaxZ int
}

type BlockBounds struct {
	Dim  int `json:"dim"`
	MinX int `json:"min_x"`
	MinY int `json:"min_y"`
	MinZ int `json:"min_z"`
	MaxX int `json:"max_x"`
	MaxY int `json:"max_y"`
	MaxZ int `json:"max_z"`
}

type RuntimeState struct {
	LastPlayersScan     string `json:"last_players_scan"`
	LastChestsScan      string `json:"last_chests_scan"`
	LastChestsAttempt   string `json:"last_chests_scan_attempt,omitempty"`
	LastMEScan          string `json:"last_me_scan"`
	LastMEAttempt       string `json:"last_me_fetch_attempt,omitempty"`
	LastQuestsScan      string `json:"last_quests_scan"`
	LastBlockInvScan    string `json:"last_block_inventories_scan"`
	LastBlockInvAttempt string `json:"last_block_inventories_fetch_attempt,omitempty"`
	LastBlocksScan      string `json:"last_blocks_scan"`
}

type RefreshRequest struct {
	RequestedAt string `json:"requested_at"`
	Scope       string `json:"scope"`
	RequestedBy string `json:"requested_by"`
}

type SourceMeta struct {
	ServerID          string `json:"server_id"`
	PlayersScanAt     string `json:"players_scan_at"`
	ChestsScanAt      string `json:"chests_scan_at"`
	MEScanAt          string `json:"me_scan_at"`
	BlockInvScanAt    string `json:"block_inventories_scan_at,omitempty"`
	BlocksScanAt      string `json:"blocks_scan_at,omitempty"`
	DatHostSyncAt     string `json:"dathost_sync_at"`
	PlayersVersion    int    `json:"players_version"`
	ChestsVersion     int    `json:"chests_version"`
	MEVersion         int    `json:"me_version"`
	BlockInvVersion   int    `json:"block_inventories_version,omitempty"`
	BlocksVersion     int    `json:"blocks_version,omitempty"`
	ChestAreasScanAt  string `json:"chest_areas_scan_at,omitempty"`
	ChestAreasVersion int    `json:"chest_areas_version,omitempty"`
}

type IndexStats struct {
	PlayerCount        int `json:"player_count"`
	ChestCount         int `json:"chest_count"`
	IndexedItemKeys    int `json:"indexed_item_keys"`
	PlayerStacks       int `json:"player_stacks"`
	EnderStacks        int `json:"ender_stacks"`
	ChestStacks        int `json:"chest_stacks"`
	MENetworkCount     int `json:"me_network_count"`
	MEStacks           int `json:"me_stacks"`
	BlockInvCount      int `json:"block_inventory_count,omitempty"`
	BlockInvStacks     int `json:"block_inventory_stacks,omitempty"`
	RegionFilesScanned int `json:"region_files_scanned"`
	BlockCount         int `json:"block_count,omitempty"`
	IndexedBlockKeys   int `json:"indexed_block_keys,omitempty"`
	BlockRegionFiles   int `json:"block_region_files_scanned,omitempty"`
}

type ItemStack struct {
	ID     int    `json:"id"`
	Damage int    `json:"damage"`
	Count  int    `json:"count"`
	Slot   int    `json:"slot"`
	Source string `json:"source,omitempty"`
	Custom string `json:"custom_name,omitempty"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type PlayerRecord struct {
	UUID      string      `json:"uuid"`
	Name      string      `json:"name"`
	Dimension int         `json:"dim"`
	Pos       Position    `json:"pos"`
	Inventory []ItemStack `json:"inventory"`
	Ender     []ItemStack `json:"ender"`
}

type ChestRecord struct {
	Dimension   int         `json:"dim"`
	X           int         `json:"x"`
	Y           int         `json:"y"`
	Z           int         `json:"z"`
	Type        string      `json:"type"`
	Source      string      `json:"source,omitempty"`
	InventoryID string      `json:"inventory_id,omitempty"`
	Items       []ItemStack `json:"items"`
}

type MERecord struct {
	NetworkID         string              `json:"network_id,omitempty"`
	Label             string              `json:"label,omitempty"`
	Dimension         int                 `json:"dim"`
	Pos               Position            `json:"pos"`
	Items             []MEItemStack       `json:"items"`
	Patterns          []MECraftingPattern `json:"crafting_patterns,omitempty"`
	PatternsTruncated bool                `json:"crafting_patterns_truncated,omitempty"`
	ActiveCrafts      []MEActiveCraft     `json:"active_crafts,omitempty"`
}

type MEItemStack struct {
	ID          int    `json:"id"`
	Damage      int    `json:"damage"`
	Count       int    `json:"count"`
	RegName     string `json:"reg_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Name        string `json:"name,omitempty"`
}

type MECraftingPattern struct {
	Inputs        []MEItemStack `json:"inputs"`
	Outputs       []MEItemStack `json:"outputs"`
	Craftable     bool          `json:"craftable"`
	CanSubstitute bool          `json:"can_substitute"`
	Priority      int           `json:"priority"`
}

type MEActiveCraft struct {
	CPUName        string       `json:"cpu_name,omitempty"`
	Output         *MEItemStack `json:"output,omitempty"`
	StartCount     int64        `json:"start_count,omitempty"`
	RemainingCount int64        `json:"remaining_count,omitempty"`
	ElapsedMillis  int64        `json:"elapsed_millis,omitempty"`
	StorageBytes   int64        `json:"storage_bytes,omitempty"`
	UsedBytes      int64        `json:"used_bytes,omitempty"`
	CoProcessors   int          `json:"co_processors,omitempty"`
	Suspended      bool         `json:"suspended,omitempty"`
}

type PlayerSlotRef struct {
	Slot   int    `json:"slot"`
	Count  int    `json:"count"`
	Damage int    `json:"damage"`
	Source string `json:"source"`
	Custom string `json:"custom_name,omitempty"`
}

type PlayerHit struct {
	UUID       string          `json:"uuid"`
	Name       string          `json:"name"`
	Dimension  int             `json:"dim"`
	Pos        Position        `json:"pos"`
	TotalCount int             `json:"total_count"`
	Locations  []PlayerSlotRef `json:"locations"`
}

type ChestHit struct {
	Dimension   int    `json:"dim"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	Z           int    `json:"z"`
	Type        string `json:"type"`
	Source      string `json:"source,omitempty"`
	InventoryID string `json:"inventory_id,omitempty"`
	TotalCount  int    `json:"total_count"`
}

type MEHit struct {
	NetworkID  string   `json:"network_id,omitempty"`
	Label      string   `json:"label,omitempty"`
	Dimension  int      `json:"dim"`
	Pos        Position `json:"pos"`
	TotalCount int      `json:"total_count"`
}

type BlockRecord struct {
	Dimension int    `json:"dim"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Z         int    `json:"z"`
	ID        int    `json:"id"`
	Meta      int    `json:"meta"`
	RegName   string `json:"reg_name,omitempty"`
	Name      string `json:"name,omitempty"`
	Source    string `json:"source,omitempty"`
}

type BlockHit struct {
	Dimension int    `json:"dim"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Z         int    `json:"z"`
	ID        int    `json:"id"`
	Meta      int    `json:"meta"`
	RegName   string `json:"reg_name,omitempty"`
	Name      string `json:"name,omitempty"`
	Source    string `json:"source,omitempty"`
}

type ItemHits struct {
	Players []PlayerHit `json:"players"`
	Chests  []ChestHit  `json:"chests"`
	ME      []MEHit     `json:"me,omitempty"`
}

type BlockHits struct {
	Blocks []BlockHit `json:"blocks"`
}

type BlockIndexStatus struct {
	Enabled           bool         `json:"enabled"`
	Reason            string       `json:"reason,omitempty"`
	Bounds            *BlockBounds `json:"bounds,omitempty"`
	Allowlist         []string     `json:"allowlist,omitempty"`
	RegistryFile      string       `json:"registry_file,omitempty"`
	RegistryAvailable bool         `json:"registry_available"`
}

type InventoryIndex struct {
	Version     int                  `json:"version"`
	GeneratedAt string               `json:"generated_at"`
	Source      SourceMeta           `json:"source"`
	Stats       IndexStats           `json:"stats"`
	Players     []PlayerRecord       `json:"players"`
	Chests      []ChestRecord        `json:"chests"`
	ME          []MERecord           `json:"me,omitempty"`
	Blocks      []BlockRecord        `json:"blocks,omitempty"`
	ItemIndex   map[string]ItemHits  `json:"item_index"`
	BlockIndex  map[string]BlockHits `json:"block_index,omitempty"`
	BlockStatus BlockIndexStatus     `json:"block_status,omitempty"`
}

type InventoryStatus struct {
	GeneratedAt string            `json:"generated_at"`
	Source      SourceMeta        `json:"source"`
	Stats       IndexStats        `json:"stats"`
	BlockStatus BlockIndexStatus  `json:"block_status,omitempty"`
	Stale       map[string]bool   `json:"stale"`
	Errors      map[string]string `json:"errors"`
}
type InventoryScopeTotals struct {
	Players    int `json:"players"`
	Containers int `json:"containers"`
	ME         int `json:"me"`
	Total      int `json:"total"`
}
type PlannerInventorySnapshot struct {
	Version     int                             `json:"version"`
	GeneratedAt string                          `json:"generated_at"`
	Source      SourceMeta                      `json:"source"`
	Totals      map[string]int                  `json:"totals"`
	Scopes      map[string]InventoryScopeTotals `json:"scopes"`
}

type MECraftingNetwork struct {
	NetworkID         string              `json:"network_id,omitempty"`
	Label             string              `json:"label,omitempty"`
	Dimension         int                 `json:"dim"`
	Pos               Position            `json:"pos"`
	Items             []MEItemStack       `json:"items,omitempty"`
	Patterns          []MECraftingPattern `json:"patterns,omitempty"`
	PatternsTruncated bool                `json:"patterns_truncated,omitempty"`
	ActiveCrafts      []MEActiveCraft     `json:"active_crafts,omitempty"`
}

type MECraftingSnapshot struct {
	Version     int                 `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	MEScanAt    string              `json:"me_scan_at"`
	Networks    []MECraftingNetwork `json:"networks"`
}
type PlannerQuestSnapshot struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generated_at"`
	Source      QuestSourceMeta `json:"source"`
	Stats       QuestStats      `json:"stats"`
	QuestLines  []QuestLine     `json:"quest_lines,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
	Quests      []PlannerQuest  `json:"quests"`
}

type PlannerQuest struct {
	ID              string      `json:"id"`
	Title           string      `json:"title"`
	QuestLineID     string      `json:"quest_line_id,omitempty"`
	QuestLine       string      `json:"quest_line,omitempty"`
	QuestLineOrder  int         `json:"quest_line_order,omitempty"`
	TierQuestLine   bool        `json:"tier_quest_line,omitempty"`
	Completed       bool        `json:"completed"`
	ClaimableBy     []string    `json:"claimable_by,omitempty"`
	Prerequisites   []string    `json:"prerequisites,omitempty"`
	Unlocks         []string    `json:"unlocks,omitempty"`
	State           string      `json:"state"`
	CompletionRatio float64     `json:"completion_ratio,omitempty"`
	Tasks           []QuestTask `json:"tasks,omitempty"`
}

type DatHostFileEntry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Deleted bool   `json:"deleted"`
}

type nbtReader struct {
	data []byte
	pos  int
}

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func loadConfig() (Config, error) {
	cfg := Config{
		Enabled:            getenvBool("INVENTORY_SYNC_ENABLED", false),
		DatHostToken:       strings.TrimSpace(os.Getenv("DATHOST_API_TOKEN")),
		DatHostEmail:       strings.TrimSpace(os.Getenv("DATHOST_API_EMAIL")),
		DatHostPassword:    strings.TrimSpace(os.Getenv("DATHOST_API_PASSWORD")),
		DatHostServer:      strings.TrimSpace(os.Getenv("DATHOST_SERVER_ID")),
		DatHostBase:        strings.TrimRight(getenv("DATHOST_API_BASE", "https://dathost.net/api/0.1"), "/"),
		WorkDir:            getenv("INVENTORY_WORKDIR", "/root/.greggpt/workspace"),
		StateFile:          getenv("INVENTORY_STATE_FILE", "/var/lib/inventory-sync/state.json"),
		PlayersInterval:    time.Duration(max(60, getenvInt("INVENTORY_PLAYERS_INTERVAL_SECONDS", 600))) * time.Second,
		ChestsInterval:     time.Duration(max(300, getenvInt("INVENTORY_CHESTS_INTERVAL_SECONDS", 21600))) * time.Second,
		MEInterval:         time.Duration(max(60, getenvInt("INVENTORY_ME_INTERVAL_SECONDS", 300))) * time.Second,
		MEStaleAfter:       time.Duration(max(1, getenvInt("INVENTORY_ME_STALE_AFTER_SECONDS", 900))) * time.Second,
		MEExportPaths:      parseCSV(getenv("INVENTORY_ME_EXPORT_PATHS", "world/greggpt/me_index.json,world/picoclaw/me_index.json")),
		QuestsInterval:     time.Duration(max(300, getenvInt("INVENTORY_QUESTS_INTERVAL_SECONDS", 1800))) * time.Second,
		QuestPartyName:     getenv("INVENTORY_QUEST_PARTY_NAME", "Noob Squad"),
		BlockInvInterval:   time.Duration(max(60, getenvInt("INVENTORY_BLOCK_INVENTORIES_INTERVAL_SECONDS", 300))) * time.Second,
		BlockInvStaleAfter: time.Duration(max(1, getenvInt("INVENTORY_BLOCK_INVENTORIES_STALE_AFTER_SECONDS", 900))) * time.Second,
		BlockInvPaths:      parseCSV(getenv("INVENTORY_BLOCK_INVENTORY_EXPORT_PATHS", "world/picoclaw/block_inventories.json,world/greggpt/block_inventories.json")),
		BlocksInterval:     time.Duration(max(300, getenvInt("INVENTORY_BLOCKS_INTERVAL_SECONDS", 86400))) * time.Second,
		HTTPTimeout:        time.Duration(max(5, getenvInt("INVENTORY_HTTP_TIMEOUT_SECONDS", 20))) * time.Second,
		MaxRegionFiles:     max(0, getenvInt("INVENTORY_MAX_REGION_FILES_PER_RUN", 0)),
		ScanDims:           parseDims(getenv("INVENTORY_SCAN_DIMS", "0,-1,1,183")),
		ChestBounds:        parseChestBounds(strings.TrimSpace(os.Getenv("INVENTORY_CHEST_BOUNDS"))),
		ChestAreas:         parseChestAreas(strings.TrimSpace(os.Getenv("INVENTORY_CHEST_AREAS"))),
		BlockBounds:        parseBlockBounds(strings.TrimSpace(os.Getenv("INVENTORY_BLOCK_BOUNDS"))),
		BlockAllowlist:     parseBlockAllowlist(strings.TrimSpace(os.Getenv("INVENTORY_BLOCK_ALLOWLIST"))),
		BlockRegistryFile:  getenv("GTNH_BLOCK_REGISTRY", filepath.Join(getenv("INVENTORY_WORKDIR", "/root/.greggpt/workspace"), "gtnh-data", "index", "block_registry.tsv")),
		DefaultResultLimit: max(1, getenvInt("INVENTORY_DEFAULT_LIMIT", 20)),
		MaxResults:         max(1, getenvInt("INVENTORY_MAX_RESULTS", 100)),
		LoopSleep:          15 * time.Second,
	}
	if cfg.DatHostServer == "" {
		return cfg, errors.New("missing DATHOST_SERVER_ID")
	}
	if cfg.DatHostToken == "" && (cfg.DatHostEmail == "" || cfg.DatHostPassword == "") {
		return cfg, errors.New("missing DatHost auth; set DATHOST_API_TOKEN or DATHOST_API_EMAIL + DATHOST_API_PASSWORD")
	}
	return cfg, nil
}

func parseDims(raw string) []int {
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	seen := map[int]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return []int{0, -1, 1}
	}
	return out
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseChestBounds(raw string) *ChestBounds {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 5 {
		return nil
	}
	vals := make([]int, 0, 5)
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil
		}
		vals = append(vals, n)
	}
	minX := vals[1]
	maxX := vals[3]
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	minZ := vals[2]
	maxZ := vals[4]
	if minZ > maxZ {
		minZ, maxZ = maxZ, minZ
	}
	return &ChestBounds{
		Dim:  vals[0],
		MinX: minX,
		MaxX: maxX,
		MinZ: minZ,
		MaxZ: maxZ,
	}
}
func parseChestAreas(raw string) []ChestBounds {
	areas := make([]ChestBounds, 0)
	for _, part := range strings.Split(raw, ";") {
		if bounds := parseChestBounds(strings.TrimSpace(part)); bounds != nil {
			areas = append(areas, *bounds)
		}
	}
	return areas
}

func formatChestAreas(areas []ChestBounds) string {
	parts := make([]string, 0, len(areas))
	for _, area := range areas {
		parts = append(parts, fmt.Sprintf("%d,%d,%d,%d,%d", area.Dim, area.MinX, area.MinZ, area.MaxX, area.MaxZ))
	}
	return strings.Join(parts, ";")
}

func parseBlockBounds(raw string) *BlockBounds {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 7 {
		return nil
	}
	vals := make([]int, 0, 7)
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil
		}
		vals = append(vals, n)
	}
	b := &BlockBounds{Dim: vals[0], MinX: vals[1], MinY: vals[2], MinZ: vals[3], MaxX: vals[4], MaxY: vals[5], MaxZ: vals[6]}
	if b.MinX > b.MaxX {
		b.MinX, b.MaxX = b.MaxX, b.MinX
	}
	if b.MinY > b.MaxY {
		b.MinY, b.MaxY = b.MaxY, b.MinY
	}
	if b.MinZ > b.MaxZ {
		b.MinZ, b.MaxZ = b.MaxZ, b.MinZ
	}
	return b
}

func parseBlockAllowlist(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, meta, ok := parseBlockKey(part)
		if ok {
			out[blockKey(id, meta)] = true
		}
	}
	return out
}

func parseBlockKey(raw string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	id, err1 := strconv.Atoi(parts[0])
	meta, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || id <= 0 || meta < 0 {
		return 0, 0, false
	}
	return id, meta, true
}

func sortedBlockAllowlist(allow map[string]bool) []string {
	out := make([]string, 0, len(allow))
	for k := range allow {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func parseRFC3339(v string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}
	}
	return t
}

func dueSince(last string, now time.Time, interval time.Duration) bool {
	t := parseRFC3339(last)
	return t.IsZero() || now.Sub(t) >= interval
}

func fetchDue(lastAttempt, lastScan string, now time.Time, interval time.Duration) bool {
	if strings.TrimSpace(lastAttempt) != "" {
		return dueSince(lastAttempt, now, interval)
	}
	return dueSince(lastScan, now, interval)
}

func sourceIsStale(scanAt string, now time.Time, staleAfter time.Duration) bool {
	t := parseRFC3339(scanAt)
	return t.IsZero() || now.Sub(t) > staleAfter
}

func statePath(workDir, base, name string) string {
	if filepath.IsAbs(base) {
		return base
	}
	return filepath.Join(workDir, base)
}

func loadJSONFile(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func atomicWriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadRuntimeState(path string) RuntimeState {
	st := RuntimeState{}
	if err := loadJSONFile(path, &st); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("event=inventory_state_load_error file=%q err=%q", path, err.Error())
	}
	return st
}

func saveRuntimeState(path string, st RuntimeState) {
	if err := atomicWriteJSON(path, st); err != nil {
		log.Printf("event=inventory_state_save_error file=%q err=%q", path, err.Error())
	}
}

func loadIndex(path string) InventoryIndex {
	idx := InventoryIndex{Version: 1, ItemIndex: map[string]ItemHits{}, BlockIndex: map[string]BlockHits{}}
	if err := loadJSONFile(path, &idx); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("event=inventory_index_load_error file=%q err=%q", path, err.Error())
		}
		return idx
	}
	if idx.ItemIndex == nil {
		idx.ItemIndex = map[string]ItemHits{}
	}
	if idx.BlockIndex == nil {
		idx.BlockIndex = map[string]BlockHits{}
	}
	normalizeLegacyBlockSources(&idx)
	return idx
}

// Older indexes did not label block provenance. Infer it once while loading so
// the first source-specific refresh can replace stale exported blocks instead
// of retaining them as region-scan records.
func normalizeLegacyBlockSources(idx *InventoryIndex) {
	if idx == nil {
		return
	}
	exportedCoordinates := make(map[string]struct{})
	for _, chest := range idx.Chests {
		if chestSource(chest) == "block_export" {
			exportedCoordinates[blockCoordinate(chest.Dimension, chest.X, chest.Y, chest.Z)] = struct{}{}
		}
	}
	exportOnlyLegacyIndex := idx.Source.BlocksScanAt == "" && idx.Source.BlockInvScanAt != ""
	for i := range idx.Blocks {
		if strings.TrimSpace(idx.Blocks[i].Source) != "" {
			continue
		}
		_, exportedInventory := exportedCoordinates[blockCoordinate(
			idx.Blocks[i].Dimension, idx.Blocks[i].X, idx.Blocks[i].Y, idx.Blocks[i].Z,
		)]
		if exportedInventory || exportOnlyLegacyIndex {
			idx.Blocks[i].Source = "block_export"
		} else {
			idx.Blocks[i].Source = "region"
		}
	}
}

func loadRefreshRequest(path string) (RefreshRequest, bool) {
	req := RefreshRequest{}
	if err := loadJSONFile(path, &req); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("event=inventory_refresh_load_error file=%q err=%q", path, err.Error())
		}
		return RefreshRequest{}, false
	}
	s := strings.ToLower(strings.TrimSpace(req.Scope))
	if s != "players" && s != "chests" && s != "containers" && s != "me" && s != "quests" && s != "block-inventories" && s != "block_inventories" && s != "blocks" && s != "all" {
		req.Scope = "all"
	} else if s == "containers" {
		req.Scope = "chests"
	} else if s == "block_inventories" {
		req.Scope = "block-inventories"
	} else {
		req.Scope = s
	}
	return req, true
}

func clearRefreshRequest(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("event=inventory_refresh_remove_error file=%q err=%q", path, err.Error())
	}
}

func datHostRequest(ctx context.Context, client *http.Client, cfg Config, method, path string, body []byte, ctype string) ([]byte, int, error) {
	urlStr := cfg.DatHostBase + path
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, rdr)
	if err != nil {
		return nil, 0, err
	}
	if cfg.DatHostToken != "" {
		req.SetBasicAuth(cfg.DatHostToken, "")
	} else {
		req.SetBasicAuth(cfg.DatHostEmail, cfg.DatHostPassword)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(payload))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return payload, resp.StatusCode, fmt.Errorf("dathost HTTP %d: %s", resp.StatusCode, msg)
	}
	return payload, resp.StatusCode, nil
}

func datHostRequestRetry(client *http.Client, cfg Config, method, path string, body []byte, ctype string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTPTimeout)
		data, status, err := datHostRequest(ctx, client, cfg, method, path, body, ctype)
		cancel()
		if err == nil {
			return data, nil
		}
		lastErr = err
		if status == 429 || status >= 500 || status == 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		break
	}
	return nil, lastErr
}

func encodeFilePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func listFiles(client *http.Client, cfg Config, path string) ([]DatHostFileEntry, error) {
	q := url.QueryEscape(path)
	data, err := datHostRequestRetry(client, cfg, http.MethodGet, "/game-servers/"+cfg.DatHostServer+"/files?path="+q, nil, "")
	if err != nil {
		return nil, err
	}
	var out []DatHostFileEntry
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func getFile(client *http.Client, cfg Config, path string) ([]byte, error) {
	enc := encodeFilePath(path)
	return datHostRequestRetry(client, cfg, http.MethodGet, "/game-servers/"+cfg.DatHostServer+"/files/"+enc, nil, "")
}

func syncFiles(client *http.Client, cfg Config) error {
	_, err := datHostRequestRetry(client, cfg, http.MethodPost, "/game-servers/"+cfg.DatHostServer+"/files/sync", nil, "application/json")
	return err
}

func parseNameCache(raw []byte) map[string]string {
	out := map[string]string{}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return out
	}
	nc, ok := root["nameCache:9"].(map[string]any)
	if !ok {
		return out
	}
	for _, row := range nc {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		uuid, _ := m["uuid:8"].(string)
		name, _ := m["name:8"].(string)
		if strings.TrimSpace(uuid) == "" {
			continue
		}
		if strings.TrimSpace(name) == "" {
			name = uuid
		}
		out[uuid] = name
	}
	return out
}

func parseMaybeCompressedNBT(raw []byte) (map[string]any, error) {
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		raw, err = io.ReadAll(zr)
		if err != nil {
			return nil, err
		}
	}
	return parseNBTDocument(raw)
}

func parseNBTDocument(raw []byte) (map[string]any, error) {
	r := &nbtReader{data: raw}
	typeID, err := r.readU8()
	if err != nil {
		return nil, err
	}
	if typeID != 10 {
		return nil, fmt.Errorf("expected root compound, got %d", typeID)
	}
	if _, err := r.readString(); err != nil {
		return nil, err
	}
	v, err := r.readCompoundPayload()
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (r *nbtReader) remaining() int {
	return len(r.data) - r.pos
}

func (r *nbtReader) read(n int) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, io.ErrUnexpectedEOF
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *nbtReader) readU8() (byte, error) {
	b, err := r.read(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *nbtReader) readI16() (int16, error) {
	b, err := r.read(2)
	if err != nil {
		return 0, err
	}
	return int16(binary.BigEndian.Uint16(b)), nil
}

func (r *nbtReader) readU16() (uint16, error) {
	b, err := r.read(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

func (r *nbtReader) readI32() (int32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(b)), nil
}

func (r *nbtReader) readI64() (int64, error) {
	b, err := r.read(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

func (r *nbtReader) readF32() (float32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(b)), nil
}

func (r *nbtReader) readF64() (float64, error) {
	b, err := r.read(8)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
}

func (r *nbtReader) readString() (string, error) {
	n, err := r.readU16()
	if err != nil {
		return "", err
	}
	b, err := r.read(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *nbtReader) readTagPayload(tagType byte) (any, error) {
	switch tagType {
	case 1:
		b, err := r.readU8()
		return int8(b), err
	case 2:
		return r.readI16()
	case 3:
		return r.readI32()
	case 4:
		return r.readI64()
	case 5:
		return r.readF32()
	case 6:
		return r.readF64()
	case 7:
		n, err := r.readI32()
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("invalid byte array size %d", n)
		}
		b, err := r.read(int(n))
		if err != nil {
			return nil, err
		}
		copyB := make([]byte, len(b))
		copy(copyB, b)
		return copyB, nil
	case 8:
		return r.readString()
	case 9:
		elemType, err := r.readU8()
		if err != nil {
			return nil, err
		}
		n, err := r.readI32()
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("invalid list size %d", n)
		}
		arr := make([]any, 0, n)
		for i := 0; i < int(n); i++ {
			v, err := r.readTagPayload(elemType)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case 10:
		return r.readCompoundPayload()
	case 11:
		n, err := r.readI32()
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("invalid int array size %d", n)
		}
		arr := make([]int32, 0, n)
		for i := 0; i < int(n); i++ {
			v, err := r.readI32()
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case 12:
		n, err := r.readI32()
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("invalid long array size %d", n)
		}
		arr := make([]int64, 0, n)
		for i := 0; i < int(n); i++ {
			v, err := r.readI64()
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unknown tag type %d", tagType)
	}
}

func (r *nbtReader) readCompoundPayload() (map[string]any, error) {
	out := map[string]any{}
	for {
		typeID, err := r.readU8()
		if err != nil {
			return nil, err
		}
		if typeID == 0 {
			return out, nil
		}
		name, err := r.readString()
		if err != nil {
			return nil, err
		}
		v, err := r.readTagPayload(typeID)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
}

func numberToInt(v any) int {
	switch t := v.(type) {
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case uint8:
		return int(t)
	case uint16:
		return int(t)
	case uint32:
		return int(t)
	case uint64:
		return int(t)
	case float32:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func numberToInt64(v any) int64 {
	switch t := v.(type) {
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		if t > uint64(^uint64(0)>>1) {
			return int64(^uint64(0) >> 1)
		}
		return int64(t)
	case float32:
		return int64(t)
	case float64:
		return int64(t)
	default:
		return 0
	}
}

func numberToFloat(v any) float64 {
	switch t := v.(type) {
	case int8:
		return float64(t)
	case int16:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case uint8:
		return float64(t)
	case uint16:
		return float64(t)
	case uint32:
		return float64(t)
	case uint64:
		return float64(t)
	case float32:
		return float64(t)
	case float64:
		return t
	default:
		return 0
	}
}

func toMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func toList(v any) []any {
	lst, _ := v.([]any)
	return lst
}

func parseItemList(list []any, source string) []ItemStack {
	out := make([]ItemStack, 0, len(list))
	for _, row := range list {
		m := toMap(row)
		if len(m) == 0 {
			continue
		}
		stack, ok := parseItemStackCompound(m, source, 0, nil)
		if !ok {
			continue
		}
		out = append(out, stack)
		nested := parseNestedStacks(m["tag"], source+":nested", stack.Slot, 0)
		if len(nested) > 0 {
			out = append(out, nested...)
		}
	}
	return out
}

func parseItemStackCompound(m map[string]any, source string, parentSlot int, countOverride any) (ItemStack, bool) {
	id := numberToInt(m["id"])
	count := numberToInt(countOverride)
	if count <= 0 {
		count = numberToInt(firstPresent(m, "Count", "count"))
	}
	if id == 0 || count <= 0 {
		return ItemStack{}, false
	}
	slot := parentSlot
	if _, ok := m["Slot"]; ok {
		slot = numberToInt(m["Slot"])
	} else if _, ok := m["slot"]; ok {
		slot = numberToInt(m["slot"])
	}
	damage := numberToInt(firstPresent(m, "Damage", "damage"))
	custom := extractCustomName(toMap(m["tag"]))
	return ItemStack{ID: id, Damage: damage, Count: count, Slot: slot, Source: source, Custom: custom}, true
}

func parseDirectStackFields(m map[string]any, source string, parentSlot int) []ItemStack {
	count := firstPresent(m, "mItemCount", "mItemCountLong", "mItemAmount", "mStoredItemCount", "mStoredCount")
	if count == nil {
		return nil
	}
	out := make([]ItemStack, 0, 2)
	for _, key := range []string{"mItemStack", "mStoredItemStack"} {
		stackMap := toMap(m[key])
		if len(stackMap) == 0 {
			continue
		}
		stack, ok := parseItemStackCompound(stackMap, source, parentSlot, count)
		if ok {
			out = append(out, stack)
			out = append(out, parseNestedStacks(stackMap["tag"], source+":nested", stack.Slot, 0)...)
		}
	}
	return out
}

func parseNestedStacks(v any, source string, parentSlot int, depth int) []ItemStack {
	if depth > 6 {
		return nil
	}
	out := make([]ItemStack, 0, 8)
	switch t := v.(type) {
	case map[string]any:
		if direct := parseDirectStackFields(t, source, parentSlot); len(direct) > 0 {
			out = append(out, direct...)
		}
		if stack, ok := parseItemStackCompound(t, source, parentSlot, nil); ok {
			out = append(out, stack)
		}
		for key, child := range t {
			if key == "mItemStack" || key == "mStoredItemStack" {
				continue
			}
			out = append(out, parseNestedStacks(child, source, parentSlot, depth+1)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, parseNestedStacks(child, source, parentSlot, depth+1)...)
		}
	}
	return out
}

func extractCustomName(tag map[string]any) string {
	if len(tag) == 0 {
		return ""
	}
	if display := toMap(tag["display"]); len(display) > 0 {
		if n, ok := display["Name"].(string); ok {
			s := strings.TrimSpace(n)
			if s != "" {
				return s
			}
		}
		if n, ok := display["LocName"].(string); ok {
			s := strings.TrimSpace(n)
			if s != "" {
				return s
			}
		}
	}
	// Some mods store custom labels directly on tag.
	for _, k := range []string{"Name", "name", "CustomName", "custom_name", "mItemName", "title"} {
		if v, ok := tag[k].(string); ok {
			s := strings.TrimSpace(v)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func parsePlayerData(raw []byte, uuid string, names map[string]string) (PlayerRecord, error) {
	root, err := parseMaybeCompressedNBT(raw)
	if err != nil {
		return PlayerRecord{}, err
	}
	name := strings.TrimSpace(names[uuid])
	if name == "" {
		name = uuid
	}

	posList := toList(root["Pos"])
	pos := Position{}
	if len(posList) >= 3 {
		pos.X = numberToFloat(posList[0])
		pos.Y = numberToFloat(posList[1])
		pos.Z = numberToFloat(posList[2])
	}

	inventory := parseItemList(toList(root["Inventory"]), "inventory")
	ender := parseItemList(toList(root["EnderItems"]), "ender")

	return PlayerRecord{
		UUID:      uuid,
		Name:      name,
		Dimension: numberToInt(root["Dimension"]),
		Pos:       pos,
		Inventory: inventory,
		Ender:     ender,
	}, nil
}

func stringFromAny(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func parseMEItem(row map[string]any) (MEItemStack, bool) {
	id := numberToInt(row["id"])
	damage := numberToInt(row["damage"])
	if _, ok := row["Damage"]; ok {
		damage = numberToInt(row["Damage"])
	}
	count := numberToInt(row["count"])
	if _, ok := row["Count"]; ok {
		count = numberToInt(row["Count"])
	}
	if id == 0 || count <= 0 {
		return MEItemStack{}, false
	}
	return MEItemStack{
		ID:          id,
		Damage:      damage,
		Count:       count,
		RegName:     firstNonEmptyString(row["reg_name"], row["regName"], row["registry_name"]),
		DisplayName: firstNonEmptyString(row["display_name"], row["displayName"], row["label"]),
		Name:        firstNonEmptyString(row["name"], row["internal_name"]),
	}, true
}

func parseMEItems(rows []any) []MEItemStack {
	items := make([]MEItemStack, 0, len(rows))
	for _, row := range rows {
		item, ok := parseMEItem(toMap(row))
		if ok {
			items = append(items, item)
		}
	}
	return items
}

func meBoolFromAny(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
		return parsed
	default:
		return numberToInt(value) != 0
	}
}

func parseMECraftingPatterns(rows []any) []MECraftingPattern {
	patterns := make([]MECraftingPattern, 0, len(rows))
	for _, row := range rows {
		m := toMap(row)
		inputs := parseMEItems(toList(m["inputs"]))
		outputs := parseMEItems(toList(m["outputs"]))
		if len(outputs) == 0 {
			continue
		}
		patterns = append(patterns, MECraftingPattern{
			Inputs:        inputs,
			Outputs:       outputs,
			Craftable:     meBoolFromAny(m["craftable"]),
			CanSubstitute: meBoolFromAny(firstPresent(m, "can_substitute", "canSubstitute")),
			Priority:      numberToInt(m["priority"]),
		})
	}
	return patterns
}

func parseMEActiveCrafts(rows []any) []MEActiveCraft {
	crafts := make([]MEActiveCraft, 0, len(rows))
	for _, row := range rows {
		m := toMap(row)
		craft := MEActiveCraft{
			CPUName:        firstNonEmptyString(m["cpu_name"], m["cpuName"], m["name"]),
			StartCount:     numberToInt64(firstPresent(m, "start_count", "startCount")),
			RemainingCount: numberToInt64(firstPresent(m, "remaining_count", "remainingCount")),
			ElapsedMillis:  numberToInt64(firstPresent(m, "elapsed_millis", "elapsedMillis")),
			StorageBytes:   numberToInt64(firstPresent(m, "storage_bytes", "storageBytes")),
			UsedBytes:      numberToInt64(firstPresent(m, "used_bytes", "usedBytes")),
			CoProcessors:   numberToInt(firstPresent(m, "co_processors", "coProcessors")),
			Suspended:      meBoolFromAny(m["suspended"]),
		}
		if output, ok := parseMEItem(toMap(m["output"])); ok {
			craft.Output = &output
		}
		if craft.CPUName != "" || craft.Output != nil {
			crafts = append(crafts, craft)
		}
	}
	return crafts
}

func firstNonEmptyString(values ...any) string {
	for _, v := range values {
		s := stringFromAny(v)
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func parseMEExport(raw []byte) ([]MERecord, string, int, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, "", 0, err
	}
	generatedAt := firstNonEmptyString(root["generated_at"], root["generatedAt"], root["timestamp"])
	records := make([]MERecord, 0)
	stackCount := 0

	addRecord := func(net map[string]any, itemRows []any) {
		rec := MERecord{
			NetworkID: firstNonEmptyString(net["network_id"], net["networkId"], net["id"], net["uuid"]),
			Label:     firstNonEmptyString(net["label"], net["name"]),
			Dimension: numberToInt(firstPresent(net, "dim", "dimension")),
			Pos: Position{
				X: numberToFloat(firstPresent(net, "x")),
				Y: numberToFloat(firstPresent(net, "y")),
				Z: numberToFloat(firstPresent(net, "z")),
			},
			Items:             make([]MEItemStack, 0, len(itemRows)),
			Patterns:          parseMECraftingPatterns(toList(firstPresent(net, "crafting_patterns", "craftingPatterns", "patterns"))),
			PatternsTruncated: meBoolFromAny(firstPresent(net, "crafting_patterns_truncated", "craftingPatternsTruncated", "patterns_truncated")),
			ActiveCrafts:      parseMEActiveCrafts(toList(firstPresent(net, "active_crafts", "activeCrafts", "crafts"))),
		}
		for _, row := range itemRows {
			item, ok := parseMEItem(toMap(row))
			if !ok {
				continue
			}
			rec.Items = append(rec.Items, item)
			stackCount++
		}
		if len(rec.Items) > 0 || len(rec.Patterns) > 0 || len(rec.ActiveCrafts) > 0 {
			if rec.Label == "" {
				rec.Label = rec.NetworkID
			}
			if rec.Label == "" {
				rec.Label = fmt.Sprintf("ME network %d", len(records)+1)
			}
			records = append(records, rec)
		}
	}

	for _, row := range toList(root["networks"]) {
		net := toMap(row)
		if len(net) == 0 {
			continue
		}
		addRecord(net, toList(net["items"]))
	}

	topItems := toList(root["items"])
	if len(topItems) > 0 {
		grouped := map[string][]any{}
		meta := map[string]map[string]any{}
		for _, row := range topItems {
			m := toMap(row)
			if len(m) == 0 {
				continue
			}
			key := firstNonEmptyString(m["network_id"], m["networkId"], m["network"], m["label"])
			if key == "" {
				key = "default"
			}
			grouped[key] = append(grouped[key], row)
			if _, ok := meta[key]; !ok {
				meta[key] = map[string]any{
					"network_id": firstNonEmptyString(m["network_id"], m["networkId"], m["network"]),
					"label":      firstNonEmptyString(m["label"], m["network_label"], m["networkLabel"]),
					"dim":        firstPresent(m, "dim", "dimension"),
					"x":          firstPresent(m, "x"),
					"y":          firstPresent(m, "y"),
					"z":          firstPresent(m, "z"),
				}
			}
		}
		keys := make([]string, 0, len(grouped))
		for k := range grouped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			addRecord(meta[k], grouped[k])
		}
	}

	sort.Slice(records, func(i, j int) bool {
		return strings.ToLower(records[i].Label) < strings.ToLower(records[j].Label)
	})
	return records, generatedAt, stackCount, nil
}

func parseBlockInventoryExport(raw []byte) ([]ChestRecord, []BlockRecord, string, int, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil, "", 0, err
	}
	generatedAt := firstNonEmptyString(root["generated_at"], root["generatedAt"], root["timestamp"])
	rows := toList(firstPresent(root, "inventories", "containers", "blocks"))
	chests := make([]ChestRecord, 0, len(rows))
	blocks := make([]BlockRecord, 0, len(rows))
	stackCount := 0
	for _, row := range rows {
		m := toMap(row)
		if len(m) == 0 {
			continue
		}
		items := parseExportedBlockInventoryItems(toList(m["items"]), firstNonEmptyString(m["source"], m["inventory_type"], m["inventoryType"]))
		label := firstNonEmptyString(m["gt_meta_name"], m["gtMetaName"], m["block_display_name"], m["blockDisplayName"], m["display_name"], m["displayName"], m["block_reg_name"], m["blockRegName"], m["tile_id"], m["tileId"], m["tile_class"], m["tileClass"])
		chests = append(chests, ChestRecord{
			Dimension:   numberToInt(firstPresent(m, "dim", "dimension")),
			X:           numberToInt(m["x"]),
			Y:           numberToInt(m["y"]),
			Z:           numberToInt(m["z"]),
			Type:        tileEntityType(label),
			Source:      "block_export",
			InventoryID: firstNonEmptyString(m["inventory_id"], m["inventoryId"]),
			Items:       items,
		})
		stackCount += len(items)

		id := numberToInt(firstPresent(m, "block_id", "blockId", "id"))
		meta := numberToInt(firstPresent(m, "gt_meta_id", "gtMetaId", "block_meta", "blockMeta", "meta", "damage"))
		if id > 0 {
			blocks = append(blocks, BlockRecord{
				Dimension: numberToInt(firstPresent(m, "dim", "dimension")),
				X:         numberToInt(m["x"]),
				Y:         numberToInt(m["y"]),
				Z:         numberToInt(m["z"]),
				ID:        id,
				Meta:      meta,
				RegName:   firstNonEmptyString(m["block_reg_name"], m["blockRegName"], m["reg_name"], m["registry_name"]),
				Name:      firstNonEmptyString(m["gt_meta_name"], m["gtMetaName"], m["block_display_name"], m["blockDisplayName"], m["display_name"], m["displayName"], m["name"]),
				Source:    "block_export",
			})
		}
	}
	chests = dedupeExportedChests(chests)
	blocks = mergeBlockRecords(nil, blocks, "block_export")
	stackCount = countChestStacks(chests)
	sort.Slice(chests, func(i, j int) bool {
		if chests[i].Dimension != chests[j].Dimension {
			return chests[i].Dimension < chests[j].Dimension
		}
		if chests[i].X != chests[j].X {
			return chests[i].X < chests[j].X
		}
		if chests[i].Y != chests[j].Y {
			return chests[i].Y < chests[j].Y
		}
		return chests[i].Z < chests[j].Z
	})
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Dimension != blocks[j].Dimension {
			return blocks[i].Dimension < blocks[j].Dimension
		}
		if blocks[i].X != blocks[j].X {
			return blocks[i].X < blocks[j].X
		}
		if blocks[i].Y != blocks[j].Y {
			return blocks[i].Y < blocks[j].Y
		}
		return blocks[i].Z < blocks[j].Z
	})
	return chests, blocks, generatedAt, stackCount, nil
}

func dedupeExportedChests(chests []ChestRecord) []ChestRecord {
	byIdentity := make(map[string]ChestRecord, len(chests))
	order := make([]string, 0, len(chests))
	for _, chest := range chests {
		key := strings.TrimSpace(chest.InventoryID)
		if key == "" {
			key = chestIdentity(chest)
		}
		if _, found := byIdentity[key]; !found {
			order = append(order, key)
		}
		byIdentity[key] = chest
	}
	out := make([]ChestRecord, 0, len(byIdentity))
	for _, key := range order {
		out = append(out, byIdentity[key])
	}
	return out
}

func parseExportedBlockInventoryItems(rows []any, defaultSource string) []ItemStack {
	out := make([]ItemStack, 0, len(rows))
	if defaultSource == "" {
		defaultSource = "block_export"
	}
	for _, row := range rows {
		m := toMap(row)
		if len(m) == 0 {
			continue
		}
		source := firstNonEmptyString(m["source"], defaultSource)
		stack, ok := parseItemStackCompound(m, source, 0, nil)
		if !ok {
			continue
		}
		stack.Custom = firstNonEmptyString(m["custom_name"], m["customName"], stack.Custom)
		out = append(out, stack)
	}
	return out
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func tileEntityType(raw string) string {
	typ := strings.TrimSpace(raw)
	if typ == "" {
		return "unknown"
	}
	return typ
}

func tileEntityShouldIndex(raw string) bool {
	typ := strings.ToLower(strings.TrimSpace(raw))
	if typ == "" {
		return true
	}

	if typ == "chest" || typ == "minecraft:chest" {
		return true
	}
	if strings.Contains(typ, "chest") || strings.Contains(typ, "hopper") {
		return true
	}
	// Catch common machine/automation blocks that keep item lists in TE NBT.
	if strings.Contains(typ, "machine") || strings.HasPrefix(typ, "gregtech:") {
		return true
	}
	if strings.Contains(typ, "barrel") || strings.Contains(typ, "crate") || strings.Contains(typ, "drawer") {
		return true
	}
	// Unknown TE ids can still hold item arrays; keep them eligible and
	// let parseTileEntityItems decide by actual payload.
	return true
}

func parseTileEntityItems(te map[string]any) []ItemStack {
	keys := []string{
		"Items",
		"mInventory",
		"mInventoryItems",
		"mInputItems",
		"mOutputItems",
		"Inventory",
		"inventory",
		"inv",
		"Inv",
		"Buffer",
		"buffer",
		"Contents",
		"contents",
	}

	out := make([]ItemStack, 0, 27)
	parsedTopLevelKeys := map[string]bool{}
	for _, k := range keys {
		items := parseItemList(toList(te[k]), "tile")
		if len(items) > 0 {
			out = append(out, items...)
			parsedTopLevelKeys[k] = true
		}
	}

	if direct := parseDirectStackFields(te, "tile", 0); len(direct) > 0 {
		out = append(out, direct...)
		parsedTopLevelKeys["mItemStack"] = true
		parsedTopLevelKeys["mStoredItemStack"] = true
	}

	if len(out) == 0 {
		for k, v := range te {
			lst := toList(v)
			if len(lst) == 0 {
				continue
			}
			first := toMap(lst[0])
			if len(first) == 0 {
				continue
			}
			if _, hasID := first["id"]; hasID {
				out = append(out, parseItemList(lst, "tile")...)
				parsedTopLevelKeys[k] = true
				continue
			}
			if _, hasCount := first["Count"]; hasCount {
				out = append(out, parseItemList(lst, "tile")...)
				parsedTopLevelKeys[k] = true
			}
		}
	}
	for k, v := range te {
		if parsedTopLevelKeys[k] {
			continue
		}
		out = append(out, parseNestedStacks(v, "tile:nested", 0, 0)...)
	}

	// De-dupe in case a TE mirrors the same list under multiple keys.
	seen := map[string]bool{}
	deduped := make([]ItemStack, 0, len(out))
	for _, it := range out {
		k := fmt.Sprintf("%d:%d:%d:%d:%s", it.ID, it.Damage, it.Count, it.Slot, it.Source)
		if seen[k] {
			continue
		}
		seen[k] = true
		deduped = append(deduped, it)
	}
	return deduped
}

func parseMCAChests(raw []byte, dim int) ([]ChestRecord, error) {
	if len(raw) < 8192 {
		return nil, fmt.Errorf("invalid mca size: %d", len(raw))
	}
	out := make([]ChestRecord, 0, 64)
	for i := 0; i < 1024; i++ {
		base := i * 4
		off := int(raw[base])<<16 | int(raw[base+1])<<8 | int(raw[base+2])
		sectors := int(raw[base+3])
		if off == 0 || sectors == 0 {
			continue
		}
		start := off * 4096
		if start+5 > len(raw) {
			continue
		}
		length := int(binary.BigEndian.Uint32(raw[start : start+4]))
		if length <= 1 || start+4+length > len(raw) {
			continue
		}
		compression := raw[start+4]
		payload := raw[start+5 : start+4+length]
		var chunkRaw []byte
		switch compression {
		case 1:
			zr, err := gzip.NewReader(bytes.NewReader(payload))
			if err != nil {
				continue
			}
			chunkRaw, err = io.ReadAll(zr)
			zr.Close()
			if err != nil {
				continue
			}
		case 2:
			zr, err := zlib.NewReader(bytes.NewReader(payload))
			if err != nil {
				continue
			}
			chunkRaw, err = io.ReadAll(zr)
			zr.Close()
			if err != nil {
				continue
			}
		default:
			continue
		}

		root, err := parseNBTDocument(chunkRaw)
		if err != nil {
			continue
		}
		level := toMap(root["Level"])
		if len(level) == 0 {
			continue
		}
		for _, teRow := range toList(level["TileEntities"]) {
			te := toMap(teRow)
			if len(te) == 0 {
				continue
			}
			typ, _ := te["id"].(string)
			if !tileEntityShouldIndex(typ) {
				continue
			}
			items := parseTileEntityItems(te)
			if len(items) == 0 {
				continue
			}
			out = append(out, ChestRecord{
				Dimension: dim,
				X:         numberToInt(te["x"]),
				Y:         numberToInt(te["y"]),
				Z:         numberToInt(te["z"]),
				Type:      tileEntityType(typ),
				Items:     items,
			})
		}
	}
	return out, nil
}

type blockMeta struct {
	RegName string
	Name    string
}

func loadBlockRegistry(path string) (map[string]blockMeta, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	lines := strings.Split(string(raw), "\n")
	out := map[string]blockMeta{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if i == 0 && len(cols) > 0 && strings.Contains(strings.ToLower(cols[0]), "id") {
			continue
		}
		if len(cols) < 3 {
			continue
		}
		id, err1 := strconv.Atoi(strings.TrimSpace(cols[0]))
		meta, err2 := strconv.Atoi(strings.TrimSpace(cols[1]))
		if err1 != nil || err2 != nil {
			continue
		}
		m := blockMeta{RegName: strings.TrimSpace(cols[2])}
		if len(cols) > 3 {
			m.Name = strings.TrimSpace(cols[3])
		}
		out[blockKey(id, meta)] = m
	}
	return out, len(out) > 0
}

func blockKey(id, meta int) string {
	return fmt.Sprintf("%d:%d", id, meta)
}

func blockAllowed(id, meta int, allow map[string]bool) bool {
	if len(allow) == 0 {
		return true
	}
	return allow[blockKey(id, meta)]
}

func inBlockBounds(dim, x, y, z int, bounds *BlockBounds) bool {
	if bounds == nil {
		return true
	}
	return dim == bounds.Dim &&
		x >= bounds.MinX && x <= bounds.MaxX &&
		y >= bounds.MinY && y <= bounds.MaxY &&
		z >= bounds.MinZ && z <= bounds.MaxZ
}

func nibbleGet(buf []byte, idx int) int {
	if idx < 0 || idx>>1 >= len(buf) {
		return 0
	}
	b := buf[idx>>1]
	if idx&1 == 1 {
		return int((b >> 4) & 0x0f)
	}
	return int(b & 0x0f)
}

func decodeSectionBlocks(section map[string]any, dim, chunkX, chunkZ int, bounds *BlockBounds, allow map[string]bool, registry map[string]blockMeta) []BlockRecord {
	sectionY := numberToInt(section["Y"])
	baseY := sectionY * 16
	if bounds != nil && (dim != bounds.Dim || baseY > bounds.MaxY || baseY+15 < bounds.MinY) {
		return nil
	}
	out := make([]BlockRecord, 0, 64)
	blocks16, hasBlocks16 := section["Blocks16"].([]byte)
	data16, _ := section["Data16"].([]byte)
	blocks, hasBlocks := section["Blocks"].([]byte)
	add, _ := section["Add"].([]byte)
	data, _ := section["Data"].([]byte)
	if hasBlocks16 {
		if len(blocks16) < 8192 {
			return nil
		}
		for idx := 0; idx < 4096; idx++ {
			off := idx * 2
			id := int(binary.BigEndian.Uint16(blocks16[off : off+2]))
			meta := 0
			if len(data16) >= off+2 {
				meta = int(binary.BigEndian.Uint16(data16[off : off+2]))
			}
			if id == 0 || !blockAllowed(id, meta, allow) {
				continue
			}
			lx, ly, lz := idx&15, (idx>>8)&15, (idx>>4)&15
			x, y, z := chunkX*16+lx, baseY+ly, chunkZ*16+lz
			if !inBlockBounds(dim, x, y, z, bounds) {
				continue
			}
			rec := BlockRecord{Dimension: dim, X: x, Y: y, Z: z, ID: id, Meta: meta, Source: "region"}
			if m, ok := registry[blockKey(id, meta)]; ok {
				rec.RegName, rec.Name = m.RegName, m.Name
			}
			out = append(out, rec)
		}
		return out
	}
	if !hasBlocks || len(blocks) < 4096 {
		return nil
	}
	for idx := 0; idx < 4096; idx++ {
		id := int(blocks[idx])
		if len(add) >= 2048 {
			id |= nibbleGet(add, idx) << 8
		}
		meta := 0
		if len(data) >= 2048 {
			meta = nibbleGet(data, idx)
		}
		if id == 0 || !blockAllowed(id, meta, allow) {
			continue
		}
		lx, ly, lz := idx&15, (idx>>8)&15, (idx>>4)&15
		x, y, z := chunkX*16+lx, baseY+ly, chunkZ*16+lz
		if !inBlockBounds(dim, x, y, z, bounds) {
			continue
		}
		rec := BlockRecord{Dimension: dim, X: x, Y: y, Z: z, ID: id, Meta: meta, Source: "region"}
		if m, ok := registry[blockKey(id, meta)]; ok {
			rec.RegName, rec.Name = m.RegName, m.Name
		}
		out = append(out, rec)
	}
	return out
}

func parseMCABlocks(raw []byte, dim int, bounds *BlockBounds, allow map[string]bool, registry map[string]blockMeta) ([]BlockRecord, error) {
	if len(raw) < 8192 {
		return nil, fmt.Errorf("invalid mca size: %d", len(raw))
	}
	out := make([]BlockRecord, 0, 512)
	for i := 0; i < 1024; i++ {
		base := i * 4
		off := int(raw[base])<<16 | int(raw[base+1])<<8 | int(raw[base+2])
		sectors := int(raw[base+3])
		if off == 0 || sectors == 0 {
			continue
		}
		start := off * 4096
		if start+5 > len(raw) {
			continue
		}
		length := int(binary.BigEndian.Uint32(raw[start : start+4]))
		if length <= 1 || start+4+length > len(raw) {
			continue
		}
		compression := raw[start+4]
		payload := raw[start+5 : start+4+length]
		var chunkRaw []byte
		switch compression {
		case 1:
			zr, err := gzip.NewReader(bytes.NewReader(payload))
			if err != nil {
				continue
			}
			chunkRaw, err = io.ReadAll(zr)
			zr.Close()
			if err != nil {
				continue
			}
		case 2:
			zr, err := zlib.NewReader(bytes.NewReader(payload))
			if err != nil {
				continue
			}
			chunkRaw, err = io.ReadAll(zr)
			zr.Close()
			if err != nil {
				continue
			}
		default:
			continue
		}
		root, err := parseNBTDocument(chunkRaw)
		if err != nil {
			continue
		}
		level := toMap(root["Level"])
		if len(level) == 0 {
			continue
		}
		chunkX := numberToInt(level["xPos"])
		chunkZ := numberToInt(level["zPos"])
		for _, row := range toList(level["Sections"]) {
			out = append(out, decodeSectionBlocks(toMap(row), dim, chunkX, chunkZ, bounds, allow, registry)...)
		}
	}
	return out, nil
}

func itemKey(id, damage int) string {
	return fmt.Sprintf("%d:%d", id, damage)
}

func indexFromData(players []PlayerRecord, chests []ChestRecord, me []MERecord, blocks []BlockRecord, source SourceMeta, stats IndexStats, blockStatus BlockIndexStatus) InventoryIndex {
	idx := InventoryIndex{
		Version:     2,
		GeneratedAt: nowUTC(),
		Source:      source,
		Stats:       stats,
		Players:     players,
		Chests:      chests,
		ME:          me,
		Blocks:      blocks,
		ItemIndex:   map[string]ItemHits{},
		BlockIndex:  map[string]BlockHits{},
		BlockStatus: blockStatus,
	}

	for _, p := range players {
		all := make([]ItemStack, 0, len(p.Inventory)+len(p.Ender))
		all = append(all, p.Inventory...)
		all = append(all, p.Ender...)
		for _, it := range all {
			k := itemKey(it.ID, it.Damage)
			h := idx.ItemIndex[k]
			found := false
			for i := range h.Players {
				if h.Players[i].UUID == p.UUID {
					h.Players[i].TotalCount += it.Count
					h.Players[i].Locations = append(h.Players[i].Locations, PlayerSlotRef{Slot: it.Slot, Count: it.Count, Damage: it.Damage, Source: it.Source, Custom: it.Custom})
					found = true
					break
				}
			}
			if !found {
				h.Players = append(h.Players, PlayerHit{
					UUID:       p.UUID,
					Name:       p.Name,
					Dimension:  p.Dimension,
					Pos:        p.Pos,
					TotalCount: it.Count,
					Locations:  []PlayerSlotRef{{Slot: it.Slot, Count: it.Count, Damage: it.Damage, Source: it.Source, Custom: it.Custom}},
				})
			}
			idx.ItemIndex[k] = h
		}
	}

	for _, c := range chests {
		for _, it := range c.Items {
			k := itemKey(it.ID, it.Damage)
			h := idx.ItemIndex[k]
			found := false
			for i := range h.Chests {
				if h.Chests[i].Dimension == c.Dimension && h.Chests[i].X == c.X && h.Chests[i].Y == c.Y && h.Chests[i].Z == c.Z {
					h.Chests[i].TotalCount += it.Count
					found = true
					break
				}
			}
			if !found {
				h.Chests = append(h.Chests, ChestHit{Dimension: c.Dimension, X: c.X, Y: c.Y, Z: c.Z, Type: c.Type, Source: chestSource(c), InventoryID: c.InventoryID, TotalCount: it.Count})
			}
			idx.ItemIndex[k] = h
		}
	}

	for _, network := range me {
		for _, it := range network.Items {
			k := itemKey(it.ID, it.Damage)
			h := idx.ItemIndex[k]
			found := false
			for i := range h.ME {
				if h.ME[i].NetworkID == network.NetworkID && h.ME[i].Label == network.Label &&
					h.ME[i].Dimension == network.Dimension &&
					int(h.ME[i].Pos.X) == int(network.Pos.X) && int(h.ME[i].Pos.Y) == int(network.Pos.Y) && int(h.ME[i].Pos.Z) == int(network.Pos.Z) {
					h.ME[i].TotalCount += it.Count
					found = true
					break
				}
			}
			if !found {
				h.ME = append(h.ME, MEHit{
					NetworkID:  network.NetworkID,
					Label:      network.Label,
					Dimension:  network.Dimension,
					Pos:        network.Pos,
					TotalCount: it.Count,
				})
			}
			idx.ItemIndex[k] = h
		}
	}

	for _, b := range blocks {
		k := blockKey(b.ID, b.Meta)
		h := idx.BlockIndex[k]
		h.Blocks = append(h.Blocks, BlockHit{
			Dimension: b.Dimension,
			X:         b.X,
			Y:         b.Y,
			Z:         b.Z,
			ID:        b.ID,
			Meta:      b.Meta,
			RegName:   b.RegName,
			Name:      b.Name,
			Source:    b.Source,
		})
		idx.BlockIndex[k] = h
	}

	for k, h := range idx.ItemIndex {
		sort.Slice(h.Players, func(i, j int) bool {
			if h.Players[i].TotalCount == h.Players[j].TotalCount {
				return strings.ToLower(h.Players[i].Name) < strings.ToLower(h.Players[j].Name)
			}
			return h.Players[i].TotalCount > h.Players[j].TotalCount
		})
		sort.Slice(h.Chests, func(i, j int) bool {
			if h.Chests[i].TotalCount == h.Chests[j].TotalCount {
				if h.Chests[i].Dimension == h.Chests[j].Dimension {
					if h.Chests[i].X == h.Chests[j].X {
						if h.Chests[i].Y == h.Chests[j].Y {
							return h.Chests[i].Z < h.Chests[j].Z
						}
						return h.Chests[i].Y < h.Chests[j].Y
					}
					return h.Chests[i].X < h.Chests[j].X
				}
				return h.Chests[i].Dimension < h.Chests[j].Dimension
			}
			return h.Chests[i].TotalCount > h.Chests[j].TotalCount
		})
		sort.Slice(h.ME, func(i, j int) bool {
			if h.ME[i].TotalCount == h.ME[j].TotalCount {
				return strings.ToLower(h.ME[i].Label) < strings.ToLower(h.ME[j].Label)
			}
			return h.ME[i].TotalCount > h.ME[j].TotalCount
		})
		idx.ItemIndex[k] = h
	}
	for k, h := range idx.BlockIndex {
		sort.Slice(h.Blocks, func(i, j int) bool {
			if h.Blocks[i].Dimension != h.Blocks[j].Dimension {
				return h.Blocks[i].Dimension < h.Blocks[j].Dimension
			}
			if h.Blocks[i].X != h.Blocks[j].X {
				return h.Blocks[i].X < h.Blocks[j].X
			}
			if h.Blocks[i].Y != h.Blocks[j].Y {
				return h.Blocks[i].Y < h.Blocks[j].Y
			}
			return h.Blocks[i].Z < h.Blocks[j].Z
		})
		idx.BlockIndex[k] = h
	}
	idx.Stats.IndexedItemKeys = len(idx.ItemIndex)
	idx.Stats.IndexedBlockKeys = len(idx.BlockIndex)
	idx.Stats.BlockCount = len(idx.Blocks)
	return idx
}

func scanPlayers(client *http.Client, cfg Config) ([]PlayerRecord, map[string]string, int, int, error) {
	nameMap := map[string]string{}
	nameRaw, err := getFile(client, cfg, "world/betterquesting/NameCache.json")
	if err == nil {
		nameMap = parseNameCache(nameRaw)
	}

	entries, err := listFiles(client, cfg, "world/playerdata/")
	if err != nil {
		return nil, nameMap, 0, 0, err
	}
	players := make([]PlayerRecord, 0, len(entries))
	invStacks := 0
	enderStacks := 0
	for _, e := range entries {
		if e.Deleted || !strings.HasSuffix(e.Path, ".dat") {
			continue
		}
		uuid := strings.TrimSuffix(filepath.Base(e.Path), ".dat")
		raw, err := getFile(client, cfg, "world/playerdata/"+filepath.Base(e.Path))
		if err != nil {
			log.Printf("event=inventory_player_file_error file=%q err=%q", e.Path, err.Error())
			continue
		}
		p, err := parsePlayerData(raw, uuid, nameMap)
		if err != nil {
			log.Printf("event=inventory_player_parse_error file=%q err=%q", e.Path, err.Error())
			continue
		}
		invStacks += len(p.Inventory)
		enderStacks += len(p.Ender)
		players = append(players, p)
	}
	sort.Slice(players, func(i, j int) bool {
		return strings.ToLower(players[i].Name) < strings.ToLower(players[j].Name)
	})
	return players, nameMap, invStacks, enderStacks, nil
}

func dimPath(dim int) (string, bool) {
	switch dim {
	case 0:
		return "world/region/", true
	default:
		return fmt.Sprintf("world/DIM%d/region/", dim), true
	}
}

func scanChests(client *http.Client, cfg Config) ([]ChestRecord, int, int, error) {
	all := make([]ChestRecord, 0, 512)
	regionCount := 0
	chestStacks := 0
	scanErrors := make([]string, 0)
	for _, dim := range cfg.ScanDims {
		path, ok := dimPath(dim)
		if !ok {
			continue
		}
		entries, err := listFiles(client, cfg, path)
		if err != nil {
			log.Printf("event=inventory_region_list_error path=%q err=%q", path, err.Error())
			scanErrors = append(scanErrors, fmt.Sprintf("list %s: %v", path, err))
			continue
		}
		regionFiles := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.Deleted || !strings.HasSuffix(e.Path, ".mca") {
				continue
			}
			regionFiles = append(regionFiles, e.Path)
		}
		sort.Strings(regionFiles)
		if cfg.MaxRegionFiles > 0 && len(regionFiles) > cfg.MaxRegionFiles {
			scanErrors = append(scanErrors, fmt.Sprintf("%s has %d region files, exceeding configured complete-scan limit %d", path, len(regionFiles), cfg.MaxRegionFiles))
			continue
		}
		for _, relPath := range regionFiles {
			regionCount++
			fullPath := path + filepath.Base(relPath)
			raw, err := getFile(client, cfg, fullPath)
			if err != nil {
				log.Printf("event=inventory_region_file_error file=%q err=%q", fullPath, err.Error())
				scanErrors = append(scanErrors, fmt.Sprintf("download %s: %v", fullPath, err))
				continue
			}
			chests, err := parseMCAChests(raw, dim)
			if err != nil {
				log.Printf("event=inventory_region_parse_error file=%q err=%q", fullPath, err.Error())
				scanErrors = append(scanErrors, fmt.Sprintf("parse %s: %v", fullPath, err))
				continue
			}
			for _, c := range chests {
				if cfg.ChestBounds != nil {
					if c.Dimension != cfg.ChestBounds.Dim ||
						c.X < cfg.ChestBounds.MinX || c.X > cfg.ChestBounds.MaxX ||
						c.Z < cfg.ChestBounds.MinZ || c.Z > cfg.ChestBounds.MaxZ {
						continue
					}
				}
				chestStacks += len(c.Items)
				all = append(all, c)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Dimension != all[j].Dimension {
			return all[i].Dimension < all[j].Dimension
		}
		if all[i].X != all[j].X {
			return all[i].X < all[j].X
		}
		if all[i].Y != all[j].Y {
			return all[i].Y < all[j].Y
		}
		return all[i].Z < all[j].Z
	})
	if len(scanErrors) > 0 {
		return nil, regionCount, 0, fmt.Errorf("incomplete chest scan: %s", strings.Join(scanErrors, "; "))
	}
	return all, regionCount, chestStacks, nil
}

func parseRegionCoords(path string) (int, int, bool) {
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "r.") || !strings.HasSuffix(base, ".mca") {
		return 0, 0, false
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(base, "r."), ".mca")
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	rx, err1 := strconv.Atoi(parts[0])
	rz, err2 := strconv.Atoi(parts[1])
	return rx, rz, err1 == nil && err2 == nil
}
func regionIntersectsChestAreas(dim int, regionPath string, areas []ChestBounds) bool {
	rx, rz, ok := parseRegionCoords(regionPath)
	if !ok {
		return true
	}
	minX, maxX := rx*512, rx*512+511
	minZ, maxZ := rz*512, rz*512+511
	for _, area := range areas {
		if dim == area.Dim && maxX >= area.MinX && minX <= area.MaxX && maxZ >= area.MinZ && minZ <= area.MaxZ {
			return true
		}
	}
	return false
}

func chestInAreas(chest ChestRecord, areas []ChestBounds) bool {
	for _, area := range areas {
		if chest.Dimension == area.Dim &&
			chest.X >= area.MinX && chest.X <= area.MaxX &&
			chest.Z >= area.MinZ && chest.Z <= area.MaxZ {
			return true
		}
	}
	return false
}

func scanChestAreas(client *http.Client, cfg Config) ([]ChestRecord, int, int, error) {
	if len(cfg.ChestAreas) == 0 {
		return nil, 0, 0, nil
	}
	dimSet := make(map[int]bool)
	for _, area := range cfg.ChestAreas {
		dimSet[area.Dim] = true
	}
	dims := make([]int, 0, len(dimSet))
	for dim := range dimSet {
		dims = append(dims, dim)
	}
	sort.Ints(dims)

	all := make([]ChestRecord, 0)
	regionCount := 0
	chestStacks := 0
	scanErrors := make([]string, 0)
	for _, dim := range dims {
		path, _ := dimPath(dim)
		entries, err := listFiles(client, cfg, path)
		if err != nil {
			scanErrors = append(scanErrors, fmt.Sprintf("list %s: %v", path, err))
			continue
		}
		regionFiles := make([]string, 0)
		for _, entry := range entries {
			if entry.Deleted || !strings.HasSuffix(entry.Path, ".mca") || !regionIntersectsChestAreas(dim, entry.Path, cfg.ChestAreas) {
				continue
			}
			regionFiles = append(regionFiles, entry.Path)
		}
		sort.Strings(regionFiles)
		if cfg.MaxRegionFiles > 0 && len(regionFiles) > cfg.MaxRegionFiles {
			scanErrors = append(scanErrors, fmt.Sprintf("%s priority areas need %d region files, exceeding configured limit %d", path, len(regionFiles), cfg.MaxRegionFiles))
			continue
		}
		for _, relPath := range regionFiles {
			regionCount++
			fullPath := path + filepath.Base(relPath)
			raw, err := getFile(client, cfg, fullPath)
			if err != nil {
				scanErrors = append(scanErrors, fmt.Sprintf("download %s: %v", fullPath, err))
				continue
			}
			chests, err := parseMCAChests(raw, dim)
			if err != nil {
				scanErrors = append(scanErrors, fmt.Sprintf("parse %s: %v", fullPath, err))
				continue
			}
			for _, chest := range chests {
				if !chestInAreas(chest, cfg.ChestAreas) {
					continue
				}
				chest.Source = "area"
				chestStacks += len(chest.Items)
				all = append(all, chest)
			}
		}
	}
	if len(scanErrors) > 0 {
		return nil, regionCount, 0, fmt.Errorf("incomplete priority chest-area scan: %s", strings.Join(scanErrors, "; "))
	}
	return all, regionCount, chestStacks, nil
}

func regionIntersectsBlockBounds(dim int, regionPath string, bounds *BlockBounds) bool {
	if bounds == nil {
		return true
	}
	if dim != bounds.Dim {
		return false
	}
	rx, rz, ok := parseRegionCoords(regionPath)
	if !ok {
		return true
	}
	minX, maxX := rx*512, rx*512+511
	minZ, maxZ := rz*512, rz*512+511
	return maxX >= bounds.MinX && minX <= bounds.MaxX && maxZ >= bounds.MinZ && minZ <= bounds.MaxZ
}

func blockScanStatus(cfg Config, registryAvailable bool) BlockIndexStatus {
	status := BlockIndexStatus{
		Enabled:           cfg.BlockBounds != nil || len(cfg.BlockAllowlist) > 0,
		Bounds:            cfg.BlockBounds,
		Allowlist:         sortedBlockAllowlist(cfg.BlockAllowlist),
		RegistryFile:      cfg.BlockRegistryFile,
		RegistryAvailable: registryAvailable,
	}
	if !status.Enabled {
		status.Reason = "block scan requires INVENTORY_BLOCK_BOUNDS or INVENTORY_BLOCK_ALLOWLIST"
	} else if !registryAvailable {
		status.Reason = "block registry unavailable; numeric id/meta search only"
	}
	return status
}

func scanBlocks(client *http.Client, cfg Config) ([]BlockRecord, int, BlockIndexStatus, error) {
	registry, registryAvailable := loadBlockRegistry(cfg.BlockRegistryFile)
	status := blockScanStatus(cfg, registryAvailable)
	if !status.Enabled {
		return nil, 0, status, errors.New(status.Reason)
	}

	all := make([]BlockRecord, 0, 1024)
	regionCount := 0
	scanErrors := make([]string, 0)
	for _, dim := range cfg.ScanDims {
		path, ok := dimPath(dim)
		if !ok {
			continue
		}
		entries, err := listFiles(client, cfg, path)
		if err != nil {
			log.Printf("event=inventory_block_region_list_error path=%q err=%q", path, err.Error())
			scanErrors = append(scanErrors, fmt.Sprintf("list %s: %v", path, err))
			continue
		}
		regionFiles := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.Deleted || !strings.HasSuffix(e.Path, ".mca") || !regionIntersectsBlockBounds(dim, e.Path, cfg.BlockBounds) {
				continue
			}
			regionFiles = append(regionFiles, e.Path)
		}
		sort.Strings(regionFiles)
		if cfg.MaxRegionFiles > 0 && len(regionFiles) > cfg.MaxRegionFiles {
			scanErrors = append(scanErrors, fmt.Sprintf("%s has %d region files, exceeding configured complete-scan limit %d", path, len(regionFiles), cfg.MaxRegionFiles))
			continue
		}
		for _, relPath := range regionFiles {
			regionCount++
			fullPath := path + filepath.Base(relPath)
			raw, err := getFile(client, cfg, fullPath)
			if err != nil {
				log.Printf("event=inventory_block_region_file_error file=%q err=%q", fullPath, err.Error())
				scanErrors = append(scanErrors, fmt.Sprintf("download %s: %v", fullPath, err))
				continue
			}
			blocks, err := parseMCABlocks(raw, dim, cfg.BlockBounds, cfg.BlockAllowlist, registry)
			if err != nil {
				log.Printf("event=inventory_block_region_parse_error file=%q err=%q", fullPath, err.Error())
				scanErrors = append(scanErrors, fmt.Sprintf("parse %s: %v", fullPath, err))
				continue
			}
			all = append(all, blocks...)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Dimension != all[j].Dimension {
			return all[i].Dimension < all[j].Dimension
		}
		if all[i].X != all[j].X {
			return all[i].X < all[j].X
		}
		if all[i].Y != all[j].Y {
			return all[i].Y < all[j].Y
		}
		return all[i].Z < all[j].Z
	})
	if len(scanErrors) > 0 {
		return nil, regionCount, status, fmt.Errorf("incomplete block scan: %s", strings.Join(scanErrors, "; "))
	}
	return all, regionCount, status, nil
}

func scanME(client *http.Client, cfg Config) ([]MERecord, string, int, error) {
	paths := cfg.MEExportPaths
	if len(paths) == 0 {
		paths = []string{"world/greggpt/me_index.json", "world/picoclaw/me_index.json"}
	}
	var lastErr error
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		raw, err := getFile(client, cfg, path)
		if err != nil {
			lastErr = err
			continue
		}
		records, generatedAt, stackCount, err := parseMEExport(raw)
		if err != nil {
			return nil, "", 0, fmt.Errorf("parse %s: %w", path, err)
		}
		if generatedAt == "" {
			generatedAt = nowUTC()
		}
		return records, generatedAt, stackCount, nil
	}
	if lastErr != nil {
		return nil, "", 0, lastErr
	}
	return nil, "", 0, errors.New("no ME export paths configured")
}

func scanBlockInventories(client *http.Client, cfg Config) ([]ChestRecord, []BlockRecord, string, int, error) {
	paths := cfg.BlockInvPaths
	if len(paths) == 0 {
		paths = []string{"world/picoclaw/block_inventories.json", "world/greggpt/block_inventories.json"}
	}
	var lastErr error
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		for attempt := 1; attempt <= 3; attempt++ {
			raw, err := getFile(client, cfg, path)
			if err != nil {
				lastErr = err
				break
			}
			chests, blocks, generatedAt, stackCount, err := parseBlockInventoryExport(raw)
			if err != nil {
				lastErr = fmt.Errorf("parse %s after %d attempts: %w", path, attempt, err)
				continue
			}
			if generatedAt == "" {
				generatedAt = nowUTC()
			}
			return chests, blocks, generatedAt, stackCount, nil
		}
	}
	if lastErr != nil {
		return nil, nil, "", 0, lastErr
	}
	return nil, nil, "", 0, errors.New("no block inventory export paths configured")
}

func mergeChestRecords(existing, replacement []ChestRecord, source string) []ChestRecord {
	candidates := make([]ChestRecord, 0, len(existing)+len(replacement))
	for _, c := range existing {
		if chestSource(c) == source {
			continue
		}
		candidates = append(candidates, c)
	}
	candidates = append(candidates, replacement...)
	byInventory := make(map[string]ChestRecord, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, c := range candidates {
		key := chestIdentity(c)
		previous, found := byInventory[key]
		if !found {
			order = append(order, key)
		}
		if !found || chestPriority(c) >= chestPriority(previous) {
			byInventory[key] = c
		}
	}
	out := make([]ChestRecord, 0, len(byInventory))
	for _, key := range order {
		out = append(out, byInventory[key])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dimension != out[j].Dimension {
			return out[i].Dimension < out[j].Dimension
		}
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].Z < out[j].Z
	})
	return out
}

func mergeBlockRecords(existing, replacement []BlockRecord, source string) []BlockRecord {
	out := make([]BlockRecord, 0, len(existing)+len(replacement))
	for _, b := range existing {
		if blockSource(b) == source {
			continue
		}
		out = append(out, b)
	}
	out = append(out, replacement...)
	byCoordinate := make(map[string]BlockRecord, len(out))
	for _, b := range out {
		key := blockCoordinate(b.Dimension, b.X, b.Y, b.Z)
		previous, found := byCoordinate[key]
		if !found || blockPriority(b) >= blockPriority(previous) {
			byCoordinate[key] = b
		}
	}
	out = out[:0]
	for _, b := range byCoordinate {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dimension != out[j].Dimension {
			return out[i].Dimension < out[j].Dimension
		}
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].Z < out[j].Z
	})
	return out
}

func blockCoordinate(dimension, x, y, z int) string {
	return fmt.Sprintf("%d:%d:%d:%d", dimension, x, y, z)
}

func chestIdentity(c ChestRecord) string {
	return fmt.Sprintf("position:%d:%d:%d:%d", c.Dimension, c.X, c.Y, c.Z)
}

func chestPriority(c ChestRecord) int {
	if chestSource(c) == "block_export" {
		return 2
	}
	return 1
}

func blockSource(b BlockRecord) string {
	if strings.TrimSpace(b.Source) != "" {
		return b.Source
	}
	return "region"
}

func blockPriority(b BlockRecord) int {
	if blockSource(b) == "block_export" {
		return 2
	}
	return 1
}

func chestSource(c ChestRecord) string {
	if c.Source != "" {
		return c.Source
	}
	return "region"
}

func countChestStacks(chests []ChestRecord) int {
	n := 0
	for _, c := range chests {
		n += len(c.Items)
	}
	return n
}
func plannerInventorySnapshot(index InventoryIndex) PlannerInventorySnapshot {
	totals := make(map[string]int, len(index.ItemIndex))
	scopes := make(map[string]InventoryScopeTotals, len(index.ItemIndex))
	for key, hits := range index.ItemIndex {
		counts := InventoryScopeTotals{}
		for _, hit := range hits.Players {
			counts.Players += hit.TotalCount
		}
		for _, hit := range hits.Chests {
			counts.Containers += hit.TotalCount
		}
		for _, hit := range hits.ME {
			counts.ME += hit.TotalCount
		}
		counts.Total = counts.Players + counts.Containers + counts.ME
		totals[key] = counts.Total
		scopes[key] = counts
	}
	return PlannerInventorySnapshot{
		Version:     2,
		GeneratedAt: index.GeneratedAt,
		Source:      index.Source,
		Totals:      totals,
		Scopes:      scopes,
	}
}

func meCraftingSnapshot(index InventoryIndex) MECraftingSnapshot {
	networks := make([]MECraftingNetwork, 0, len(index.ME))
	for _, network := range index.ME {
		if len(network.Patterns) == 0 && len(network.ActiveCrafts) == 0 {
			continue
		}
		networks = append(networks, MECraftingNetwork{
			NetworkID:         network.NetworkID,
			Label:             network.Label,
			Dimension:         network.Dimension,
			Pos:               network.Pos,
			Items:             network.Items,
			Patterns:          network.Patterns,
			PatternsTruncated: network.PatternsTruncated,
			ActiveCrafts:      network.ActiveCrafts,
		})
	}
	return MECraftingSnapshot{
		Version:     2,
		GeneratedAt: index.GeneratedAt,
		MEScanAt:    index.Source.MEScanAt,
		Networks:    networks,
	}
}

func plannerQuestSnapshot(index QuestIndex) PlannerQuestSnapshot {
	quests := make([]PlannerQuest, 0, len(index.Quests))
	for _, quest := range index.Quests {
		quests = append(quests, PlannerQuest{
			ID:              quest.ID,
			Title:           quest.Title,
			QuestLineID:     quest.QuestLineID,
			QuestLine:       quest.QuestLine,
			QuestLineOrder:  quest.QuestLineOrder,
			TierQuestLine:   quest.TierQuestLine,
			Completed:       quest.Completed,
			ClaimableBy:     quest.ClaimableBy,
			Prerequisites:   quest.Prerequisites,
			Unlocks:         quest.Unlocks,
			State:           quest.State,
			CompletionRatio: quest.CompletionRatio,
			Tasks:           quest.Tasks,
		})
	}
	return PlannerQuestSnapshot{
		Version:     1,
		GeneratedAt: index.GeneratedAt,
		Source:      index.Source,
		Stats:       index.Stats,
		QuestLines:  index.QuestLines,
		Warnings:    index.Warnings,
		Quests:      quests,
	}
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("event=inventory_startup_error err=%q", err.Error())
	}

	stateFile := cfg.StateFile
	indexFile := filepath.Join(cfg.WorkDir, "state", "inventory_index.json")
	statusFile := filepath.Join(cfg.WorkDir, "state", "inventory_status.json")
	questIndexFile := filepath.Join(cfg.WorkDir, "state", "quest_index.json")
	questStatusFile := filepath.Join(cfg.WorkDir, "state", "quest_status.json")
	refreshFile := filepath.Join(cfg.WorkDir, "state", "inventory_refresh.json")
	plannerInventoryFile := filepath.Join(cfg.WorkDir, "state", "quest_inventory_totals.json")
	meCraftingFile := filepath.Join(cfg.WorkDir, "state", "me_crafting.json")
	plannerQuestFile := filepath.Join(cfg.WorkDir, "state", "quest_planner_index.json")

	client := &http.Client{Timeout: cfg.HTTPTimeout + 3*time.Second}
	state := loadRuntimeState(stateFile)

	if cfg.ChestBounds != nil {
		log.Printf("event=inventory_startup_ok enabled=%t workdir=%q state_file=%q players_interval=%s chests_interval=%s me_interval=%s quests_interval=%s block_inventory_interval=%s blocks_interval=%s chest_bounds=%d,%d,%d,%d,%d", cfg.Enabled, cfg.WorkDir, stateFile, cfg.PlayersInterval, cfg.ChestsInterval, cfg.MEInterval, cfg.QuestsInterval, cfg.BlockInvInterval, cfg.BlocksInterval, cfg.ChestBounds.Dim, cfg.ChestBounds.MinX, cfg.ChestBounds.MinZ, cfg.ChestBounds.MaxX, cfg.ChestBounds.MaxZ)
	} else {
		log.Printf("event=inventory_startup_ok enabled=%t workdir=%q state_file=%q players_interval=%s chests_interval=%s me_interval=%s quests_interval=%s block_inventory_interval=%s blocks_interval=%s", cfg.Enabled, cfg.WorkDir, stateFile, cfg.PlayersInterval, cfg.ChestsInterval, cfg.MEInterval, cfg.QuestsInterval, cfg.BlockInvInterval, cfg.BlocksInterval)
	}

	for {
		if !cfg.Enabled {
			time.Sleep(cfg.LoopSleep)
			continue
		}

		now := time.Now().UTC()
		playersDue := dueSince(state.LastPlayersScan, now, cfg.PlayersInterval)
		chestsDue := fetchDue(state.LastChestsAttempt, state.LastChestsScan, now, cfg.ChestsInterval)
		meDue := fetchDue(state.LastMEAttempt, state.LastMEScan, now, cfg.MEInterval)
		questsDue := dueSince(state.LastQuestsScan, now, cfg.QuestsInterval)
		blockInvDue := fetchDue(state.LastBlockInvAttempt, state.LastBlockInvScan, now, cfg.BlockInvInterval)
		blocksDue := (cfg.BlockBounds != nil || len(cfg.BlockAllowlist) > 0) && dueSince(state.LastBlocksScan, now, cfg.BlocksInterval)

		refreshReq, hasRefresh := loadRefreshRequest(refreshFile)
		if hasRefresh {
			switch refreshReq.Scope {
			case "players":
				playersDue = true
			case "chests":
				chestsDue = true
			case "me":
				meDue = true
			case "quests":
				questsDue = true
			case "block-inventories":
				blockInvDue = true
			case "blocks":
				blocksDue = true
			default:
				playersDue = true
				chestsDue = true
				meDue = true
				questsDue = true
				blockInvDue = true
				blocksDue = cfg.BlockBounds != nil || len(cfg.BlockAllowlist) > 0
			}
		}

		if !playersDue && !chestsDue && !meDue && !questsDue && !blockInvDue && !blocksDue {
			time.Sleep(cfg.LoopSleep)
			continue
		}

		errorsMap := map[string]string{}
		syncAt := ""
		if err := syncFiles(client, cfg); err != nil {
			errorsMap["api"] = err.Error()
			log.Printf("event=inventory_sync_error err=%q", err.Error())
		} else {
			syncAt = nowUTC()
		}

		prev := loadIndex(indexFile)
		players := prev.Players
		chests := prev.Chests
		me := prev.ME
		blocks := prev.Blocks
		stats := prev.Stats
		source := prev.Source
		_, registryAvailable := loadBlockRegistry(cfg.BlockRegistryFile)
		blockStatus := blockScanStatus(cfg, registryAvailable)
		source.ServerID = cfg.DatHostServer
		if syncAt != "" {
			source.DatHostSyncAt = syncAt
		}

		if playersDue {
			p, _, invCount, enderCount, err := scanPlayers(client, cfg)
			if err != nil {
				errorsMap["players"] = err.Error()
				log.Printf("event=inventory_players_scan_error err=%q", err.Error())
			} else {
				players = p
				state.LastPlayersScan = nowUTC()
				source.PlayersScanAt = state.LastPlayersScan
				source.PlayersVersion++
				stats.PlayerCount = len(players)
				stats.PlayerStacks = invCount
				stats.EnderStacks = enderCount
			}
		}

		if meDue {
			attemptAt := nowUTC()
			state.LastMEAttempt = attemptAt
			networks, meScanAt, meStacks, err := scanME(client, cfg)
			if err != nil {
				errorsMap["me"] = err.Error()
				log.Printf("event=inventory_me_scan_error err=%q", err.Error())
			} else if sourceIsStale(meScanAt, now, cfg.MEStaleAfter) {
				errorsMap["me"] = fmt.Sprintf("stale ME export generated_at=%s stale_after=%s", meScanAt, cfg.MEStaleAfter)
				log.Printf("event=inventory_me_scan_stale generated_at=%q stale_after=%s", meScanAt, cfg.MEStaleAfter)
			} else {
				me = networks
				state.LastMEScan = meScanAt
				source.MEScanAt = state.LastMEScan
				source.MEVersion++
				stats.MENetworkCount = len(me)
				stats.MEStacks = meStacks
			}
		}

		if questsDue {
			questIndex, err := scanQuests(client, cfg, source.DatHostSyncAt)
			if err != nil {
				errorsMap["quests"] = err.Error()
				log.Printf("event=inventory_quests_scan_error err=%q", err.Error())
			} else {
				state.LastQuestsScan = questIndex.Source.QuestsScanAt
				if err := atomicWriteJSON(questIndexFile, questIndex); err != nil {
					log.Printf("event=quest_index_write_error file=%q err=%q", questIndexFile, err.Error())
					errorsMap["quest_index_write"] = err.Error()
				}
				if err := atomicWriteJSON(plannerQuestFile, plannerQuestSnapshot(questIndex)); err != nil {
					log.Printf("event=quest_planner_index_write_error file=%q err=%q", plannerQuestFile, err.Error())
					errorsMap["quest_planner_index_write"] = err.Error()
				}
				if err := atomicWriteJSON(questStatusFile, statusFromQuestIndex(questIndex, nil)); err != nil {
					log.Printf("event=quest_status_write_error file=%q err=%q", questStatusFile, err.Error())
				}
			}
		}

		if blocksDue {
			b, regionCount, status, err := scanBlocks(client, cfg)
			blockStatus = status
			if err != nil {
				errorsMap["blocks"] = err.Error()
				log.Printf("event=inventory_blocks_scan_error err=%q", err.Error())
			} else {
				blocks = b
				state.LastBlocksScan = nowUTC()
				source.BlocksScanAt = state.LastBlocksScan
				source.BlocksVersion++
				stats.BlockRegionFiles = regionCount
				stats.BlockCount = len(blocks)
			}
		}

		if blockInvDue {
			attemptAt := nowUTC()
			state.LastBlockInvAttempt = attemptAt
			c, b, blockInvScanAt, blockInvStacks, err := scanBlockInventories(client, cfg)
			if err != nil {
				errorsMap["block_inventories"] = err.Error()
				log.Printf("event=inventory_block_inventories_scan_error err=%q", err.Error())
			} else if sourceIsStale(blockInvScanAt, now, cfg.BlockInvStaleAfter) {
				errorsMap["block_inventories"] = fmt.Sprintf("stale block inventory export generated_at=%s stale_after=%s", blockInvScanAt, cfg.BlockInvStaleAfter)
				log.Printf("event=inventory_block_inventories_scan_stale generated_at=%q stale_after=%s", blockInvScanAt, cfg.BlockInvStaleAfter)
			} else {
				chests = mergeChestRecords(chests, c, "block_export")
				blocks = mergeBlockRecords(blocks, b, "block_export")
				state.LastBlockInvScan = blockInvScanAt
				source.BlockInvScanAt = state.LastBlockInvScan
				source.BlockInvVersion++
				stats.BlockInvCount = len(c)
				stats.BlockInvStacks = blockInvStacks
				stats.ChestCount = len(chests)
				stats.ChestStacks = countChestStacks(chests)
				stats.BlockCount = len(blocks)
				if len(b) > 0 && !blockStatus.Enabled {
					blockStatus.Reason = "block scan disabled; using exported inventory block positions"
				}
			}
			if len(cfg.ChestAreas) > 0 {
				areaChests, areaRegions, areaStacks, areaErr := scanChestAreas(client, cfg)
				if areaErr != nil {
					errorsMap["chest_areas"] = areaErr.Error()
					log.Printf("event=inventory_chest_areas_scan_error areas=%q err=%q", formatChestAreas(cfg.ChestAreas), areaErr.Error())
				} else {
					chests = mergeChestRecords(chests, areaChests, "area")
					source.ChestAreasScanAt = nowUTC()
					source.ChestAreasVersion++
					stats.ChestCount = len(chests)
					stats.ChestStacks = countChestStacks(chests)
					log.Printf("event=inventory_chest_areas_scan_complete areas=%q regions=%d chests=%d stacks=%d", formatChestAreas(cfg.ChestAreas), areaRegions, len(areaChests), areaStacks)
				}
			}
		}

		// Run the expensive all-region chest pass after the small, live exporter
		// fetches. A slow chest scan must not prevent ME or pocket-dimension
		// machine data from being refreshed and published first.
		if chestsDue {
			state.LastChestsAttempt = nowUTC()
			// A complete world scan can run for many minutes. Persist the attempt
			// before starting so a restart or failed scan cannot immediately launch
			// another expensive pass from the last successful timestamp.
			saveRuntimeState(stateFile, state)
			c, regionCount, chestStacks, err := scanChests(client, cfg)
			if err != nil {
				errorsMap["chests"] = err.Error()
				log.Printf("event=inventory_chests_scan_error err=%q", err.Error())
			} else {
				for i := range c {
					c[i].Source = "region"
				}
				chests = mergeChestRecords(chests, c, "region")
				state.LastChestsScan = nowUTC()
				source.ChestsScanAt = state.LastChestsScan
				source.ChestsVersion++
				stats.ChestCount = len(chests)
				stats.ChestStacks = countChestStacks(chests)
				stats.RegionFilesScanned = regionCount
				_ = chestStacks
			}
		}

		index := indexFromData(players, chests, me, blocks, source, stats, blockStatus)
		if err := atomicWriteJSON(indexFile, index); err != nil {
			log.Printf("event=inventory_index_write_error file=%q err=%q", indexFile, err.Error())
			errorsMap["index_write"] = err.Error()
		}
		if err := atomicWriteJSON(plannerInventoryFile, plannerInventorySnapshot(index)); err != nil {
			log.Printf("event=quest_inventory_totals_write_error file=%q err=%q", plannerInventoryFile, err.Error())
			errorsMap["quest_inventory_totals_write"] = err.Error()
		}
		if err := atomicWriteJSON(meCraftingFile, meCraftingSnapshot(index)); err != nil {
			log.Printf("event=me_crafting_write_error file=%q err=%q", meCraftingFile, err.Error())
			errorsMap["me_crafting_write"] = err.Error()
		}

		now2 := time.Now().UTC()
		status := InventoryStatus{
			GeneratedAt: nowUTC(),
			Source:      index.Source,
			Stats:       index.Stats,
			BlockStatus: index.BlockStatus,
			Stale: map[string]bool{
				"players":           sourceIsStale(index.Source.PlayersScanAt, now2, 30*time.Minute),
				"chests":            sourceIsStale(index.Source.ChestsScanAt, now2, 24*time.Hour),
				"chest_areas":       sourceIsStale(index.Source.ChestAreasScanAt, now2, cfg.BlockInvStaleAfter),
				"me":                sourceIsStale(index.Source.MEScanAt, now2, cfg.MEStaleAfter),
				"block_inventories": sourceIsStale(index.Source.BlockInvScanAt, now2, cfg.BlockInvStaleAfter),
				"blocks":            index.BlockStatus.Enabled && (parseRFC3339(index.Source.BlocksScanAt).IsZero() || now2.Sub(parseRFC3339(index.Source.BlocksScanAt)) > 7*24*time.Hour),
			},
			Errors: errorsMap,
		}
		if err := atomicWriteJSON(statusFile, status); err != nil {
			log.Printf("event=inventory_status_write_error file=%q err=%q", statusFile, err.Error())
		}

		saveRuntimeState(stateFile, state)
		if hasRefresh {
			clearRefreshRequest(refreshFile)
		}

		log.Printf("event=inventory_cycle_complete players=%d chests=%d me_networks=%d blocks=%d item_keys=%d block_keys=%d quests_due=%t", index.Stats.PlayerCount, index.Stats.ChestCount, index.Stats.MENetworkCount, index.Stats.BlockCount, index.Stats.IndexedItemKeys, index.Stats.IndexedBlockKeys, questsDue)
		time.Sleep(cfg.LoopSleep)
	}
}

func writeOutputs(indexFile, statusFile string, index InventoryIndex, errorsMap map[string]string) {
	if err := atomicWriteJSON(indexFile, index); err != nil {
		log.Printf("event=inventory_index_write_error file=%q err=%q", indexFile, err.Error())
		errorsMap["index_write"] = err.Error()
	}

	now2 := time.Now().UTC()
	status := InventoryStatus{
		GeneratedAt: nowUTC(),
		Source:      index.Source,
		Stats:       index.Stats,
		BlockStatus: index.BlockStatus,
		Stale: map[string]bool{
			"players":           sourceIsStale(index.Source.PlayersScanAt, now2, 30*time.Minute),
			"chests":            sourceIsStale(index.Source.ChestsScanAt, now2, 24*time.Hour),
			"me":                sourceIsStale(index.Source.MEScanAt, now2, 15*time.Minute),
			"block_inventories": sourceIsStale(index.Source.BlockInvScanAt, now2, 15*time.Minute),
			"blocks":            index.BlockStatus.Enabled && (parseRFC3339(index.Source.BlocksScanAt).IsZero() || now2.Sub(parseRFC3339(index.Source.BlocksScanAt)) > 7*24*time.Hour),
		},
		Errors: errorsMap,
	}
	if err := atomicWriteJSON(statusFile, status); err != nil {
		log.Printf("event=inventory_status_write_error file=%q err=%q", statusFile, err.Error())
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
