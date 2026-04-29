package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const unsetDamage = -1 << 31

type SourceMeta struct {
	PlayersScanAt  string `json:"players_scan_at"`
	ChestsScanAt   string `json:"chests_scan_at"`
	MEScanAt       string `json:"me_scan_at"`
	BlockInvScanAt string `json:"block_inventories_scan_at,omitempty"`
	BlocksScanAt   string `json:"blocks_scan_at"`
	DatHostSyncAt  string `json:"dathost_sync_at"`
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
	BlockInvCount      int `json:"block_inventory_count"`
	BlockInvStacks     int `json:"block_inventory_stacks"`
	RegionFilesScanned int `json:"region_files_scanned"`
	BlockCount         int `json:"block_count"`
	IndexedBlockKeys   int `json:"indexed_block_keys"`
	BlockRegionFiles   int `json:"block_region_files_scanned"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type ItemStack struct {
	ID     int    `json:"id"`
	Damage int    `json:"damage"`
	Count  int    `json:"count"`
	Slot   int    `json:"slot"`
	Source string `json:"source,omitempty"`
	Custom string `json:"custom_name,omitempty"`
}

type MEItemStack struct {
	ID          int    `json:"id"`
	Damage      int    `json:"damage"`
	Count       int    `json:"count"`
	RegName     string `json:"reg_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Name        string `json:"name,omitempty"`
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
	Dimension int         `json:"dim"`
	X         int         `json:"x"`
	Y         int         `json:"y"`
	Z         int         `json:"z"`
	Type      string      `json:"type"`
	Source    string      `json:"source,omitempty"`
	Items     []ItemStack `json:"items"`
}

type MERecord struct {
	NetworkID string        `json:"network_id,omitempty"`
	Label     string        `json:"label,omitempty"`
	Dimension int           `json:"dim"`
	Pos       Position      `json:"pos"`
	Items     []MEItemStack `json:"items"`
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
	Dimension  int    `json:"dim"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Z          int    `json:"z"`
	Type       string `json:"type"`
	TotalCount int    `json:"total_count"`
}

type MEHit struct {
	NetworkID  string   `json:"network_id,omitempty"`
	Label      string   `json:"label,omitempty"`
	Dimension  int      `json:"dim"`
	Pos        Position `json:"pos"`
	TotalCount int      `json:"total_count"`
}

type ItemHits struct {
	Players []PlayerHit `json:"players"`
	Chests  []ChestHit  `json:"chests"`
	ME      []MEHit     `json:"me,omitempty"`
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
}

type BlockHits struct {
	Blocks []BlockHit `json:"blocks"`
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

type ItemMeta struct {
	Slug        string
	DisplayName string
	RegName     string
	Name        string
	ID          int
	Damage      int
	Aliases     []string
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
var colorCode = regexp.MustCompile(`(?i)§[0-9a-fk-or]`)
var tierSuffix = regexp.MustCompile(`(?i)^(.+?)\s*\(([a-z0-9]+)\)$`)

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func workspaceDir() string {
	if v := strings.TrimSpace(os.Getenv("GTNH_WORKSPACE")); v != "" {
		return v
	}
	if wd, err := os.Getwd(); err == nil {
		if filepath.Base(wd) == "workspace" {
			return wd
		}
	}
	return filepath.Dir(getenv("GTNH_INVENTORY_INDEX_FILE", filepath.Join("workspace", "state", "inventory_index.json")))
}

func defaultIndexFile() string {
	ws := workspaceDir()
	return getenv("GTNH_INVENTORY_INDEX_FILE", filepath.Join(ws, "state", "inventory_index.json"))
}

func defaultStatusFile() string {
	ws := workspaceDir()
	return getenv("GTNH_INVENTORY_STATUS_FILE", filepath.Join(ws, "state", "inventory_status.json"))
}

func defaultRefreshFile() string {
	ws := workspaceDir()
	return getenv("GTNH_INVENTORY_REFRESH_FILE", filepath.Join(ws, "state", "inventory_refresh.json"))
}

func defaultItemsIndex() string {
	ws := workspaceDir()
	return getenv("GTNH_ITEMS_INDEX", filepath.Join(ws, "gtnh-data", "index", "item_index.tsv"))
}

func loadJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func loadIndex() (InventoryIndex, error) {
	idx := InventoryIndex{ItemIndex: map[string]ItemHits{}, BlockIndex: map[string]BlockHits{}}
	err := loadJSON(defaultIndexFile(), &idx)
	if idx.ItemIndex == nil {
		idx.ItemIndex = map[string]ItemHits{}
	}
	if idx.BlockIndex == nil {
		idx.BlockIndex = map[string]BlockHits{}
	}
	return idx, err
}

func normalize(s string) string {
	s = colorCode.ReplaceAllString(s, "")
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func compact(s string) string {
	return strings.ReplaceAll(normalize(s), " ", "")
}

func parseSlug(slug string) (int, int) {
	parts := strings.Split(slug, "d")
	if len(parts) == 0 {
		return 0, 0
	}
	id, _ := strconv.Atoi(parts[0])
	damage := 0
	if len(parts) > 1 {
		damage, _ = strconv.Atoi(parts[len(parts)-1])
	}
	return id, damage
}

func addAlias(seen map[string]bool, aliases *[]string, value string) {
	n := normalize(value)
	if n == "" || seen[n] {
		return
	}
	seen[n] = true
	*aliases = append(*aliases, n)
	if m := tierSuffix.FindStringSubmatch(value); len(m) == 3 {
		alt := normalize(m[2] + " " + m[1])
		if alt != "" && !seen[alt] {
			seen[alt] = true
			*aliases = append(*aliases, alt)
		}
	}
}

func loadItems() ([]ItemMeta, map[string]ItemMeta, error) {
	f, err := os.Open(defaultItemsIndex())
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	out := make([]ItemMeta, 0, len(rows))
	byKey := map[string]ItemMeta{}
	for i, row := range rows {
		if i == 0 || len(row) < 4 {
			continue
		}
		id, damage := parseSlug(row[0])
		meta := ItemMeta{Slug: row[0], DisplayName: row[1], RegName: row[2], Name: row[3], ID: id, Damage: damage}
		seen := map[string]bool{}
		addAlias(seen, &meta.Aliases, meta.DisplayName)
		addAlias(seen, &meta.Aliases, colorCode.ReplaceAllString(meta.DisplayName, ""))
		addAlias(seen, &meta.Aliases, meta.RegName)
		addAlias(seen, &meta.Aliases, meta.Name)
		out = append(out, meta)
		byKey[itemKey(id, damage)] = meta
	}
	return out, byKey, nil
}

func itemKey(id, damage int) string {
	return fmt.Sprintf("%d:%d", id, damage)
}

func keysPresent(idx InventoryIndex) map[string]bool {
	out := map[string]bool{}
	for k, h := range idx.ItemIndex {
		if len(h.Players) > 0 || len(h.Chests) > 0 || len(h.ME) > 0 {
			out[k] = true
		}
	}
	return out
}

func keyCounts(idx InventoryIndex) map[string]int {
	out := map[string]int{}
	for k, h := range idx.ItemIndex {
		for _, p := range h.Players {
			out[k] += p.TotalCount
		}
		for _, c := range h.Chests {
			out[k] += c.TotalCount
		}
		for _, me := range h.ME {
			out[k] += me.TotalCount
		}
	}
	return out
}

func registryRank(reg string) int {
	reg = strings.ToLower(strings.TrimSpace(reg))
	switch {
	case strings.HasPrefix(reg, "minecraft:"):
		return 0
	case strings.HasPrefix(reg, "gregtech:"):
		return 1
	default:
		return 5
	}
}

func resolveQuery(query string, idx InventoryIndex, limit int) ([]ItemMeta, error) {
	items, _, err := loadItems()
	if err != nil {
		return nil, err
	}
	qn := normalize(query)
	qc := compact(query)
	qTokens := strings.Fields(qn)
	present := keysPresent(idx)
	counts := keyCounts(idx)
	type cand struct {
		score int
		count int
		item  ItemMeta
	}
	cands := make([]cand, 0)
	seen := map[string]bool{}
	for _, it := range items {
		best := 1_000_000
		for _, a := range it.Aliases {
			ac := strings.ReplaceAll(a, " ", "")
			score := 1_000_000
			switch {
			case a == qn:
				score = 0
			case ac == qc:
				score = 2
			case strings.Contains(a, qn):
				score = 50 + len(a)
			case allTokensIn(qTokens, a):
				score = 120 + len(a)
			case anyTokenIn(qTokens, a):
				score = 400 + len(a)
			}
			if score < best {
				best = score
			}
		}
		if best >= 1_000_000 {
			continue
		}
		if present[itemKey(it.ID, it.Damage)] {
			best -= 10
		}
		if seen[it.Slug] {
			continue
		}
		seen[it.Slug] = true
		cands = append(cands, cand{score: best, count: counts[itemKey(it.ID, it.Damage)], item: it})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score == cands[j].score {
			di := strings.ToLower(cands[i].item.DisplayName)
			dj := strings.ToLower(cands[j].item.DisplayName)
			if di != dj {
				return di < dj
			}
			ri := registryRank(cands[i].item.RegName)
			rj := registryRank(cands[j].item.RegName)
			if ri != rj {
				return ri < rj
			}
			if cands[i].count != cands[j].count {
				return cands[i].count > cands[j].count
			}
			return cands[i].item.Slug < cands[j].item.Slug
		}
		return cands[i].score < cands[j].score
	})
	if limit <= 0 {
		limit = 20
	}
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]ItemMeta, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.item)
	}
	return out, nil
}

func allTokensIn(tokens []string, hay string) bool {
	hits := 0
	usable := 0
	for _, tok := range tokens {
		if len(tok) < 2 {
			continue
		}
		usable++
		if strings.Contains(hay, tok) {
			hits++
		}
	}
	return usable > 0 && hits == usable
}

func anyTokenIn(tokens []string, hay string) bool {
	for _, tok := range tokens {
		if len(tok) >= 3 && strings.Contains(hay, tok) {
			return true
		}
	}
	return false
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, strings.TrimSpace(s))
	return t
}

func ageText(ts string) string {
	t := parseTime(ts)
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds old", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm old", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh old", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd old", int(d.Hours()/24))
	}
}

func freshness(meta SourceMeta) string {
	return fmt.Sprintf("Freshness: players: %s | containers: %s | block inventories: %s | ME: %s | blocks: %s", ageText(meta.PlayersScanAt), ageText(meta.ChestsScanAt), ageText(meta.BlockInvScanAt), ageText(meta.MEScanAt), ageText(meta.BlocksScanAt))
}

func cmdStatus() error {
	var st InventoryStatus
	if err := loadJSON(defaultStatusFile(), &st); err != nil {
		return err
	}
	fmt.Println("Inventory Index Status")
	fmt.Println("Generated:", valueOr(st.GeneratedAt, "(unknown)"))
	fmt.Println(freshness(st.Source))
	fmt.Printf("Players: %d | Containers: %d | Exported block inventories: %d | ME networks: %d | Item keys: %d | Blocks: %d | Block keys: %d\n", st.Stats.PlayerCount, st.Stats.ChestCount, st.Stats.BlockInvCount, st.Stats.MENetworkCount, st.Stats.IndexedItemKeys, st.Stats.BlockCount, st.Stats.IndexedBlockKeys)
	if st.BlockStatus.RegistryAvailable {
		fmt.Println("Block registry: available")
	} else {
		fmt.Println("Block registry: unavailable; numeric id/meta search only")
	}
	if st.BlockStatus.Reason != "" {
		fmt.Println("Block status:", st.BlockStatus.Reason)
	}
	for _, key := range []string{"players", "chests", "block_inventories", "me", "blocks"} {
		if st.Stale[key] {
			fmt.Printf("WARNING: %s data is stale\n", key)
		}
	}
	if len(st.Errors) == 0 {
		fmt.Println("Errors: none")
		return nil
	}
	fmt.Println("Errors:")
	keys := make([]string, 0, len(st.Errors))
	for k := range st.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("- %s: %s\n", k, st.Errors[k])
	}
	return nil
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func scopeSet(scope string) (players, containers, me bool, err error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "all", "both":
		return true, true, true, nil
	case "players":
		return true, false, false, nil
	case "chests", "containers":
		return false, true, false, nil
	case "me":
		return false, false, true, nil
	default:
		return false, false, false, fmt.Errorf("--scope must be players, chests, containers, me, both, or all")
	}
}

func selectedPlayer(idx InventoryIndex, filter string) *PlayerRecord {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return nil
	}
	for i := range idx.Players {
		if strings.ToLower(idx.Players[i].Name) == filter || strings.ToLower(idx.Players[i].UUID) == filter {
			return &idx.Players[i]
		}
	}
	return nil
}

func cmdFind(args []string) error {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	item := fs.String("item", "", "")
	id := fs.Int("id", 0, "")
	damage := fs.Int("damage", unsetDamage, "")
	anyDamage := fs.Bool("any-damage", false, "")
	scope := fs.String("scope", "all", "")
	player := fs.String("player", "", "")
	limit := fs.Int("limit", 20, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	keys, label, err := resolveFindKeys(*item, *id, *damage, *anyDamage, idx)
	if err != nil {
		return err
	}
	return printFind(idx, keys, label, *scope, *player, *limit, "exact")
}

func resolveFindKeys(item string, id int, damage int, anyDamage bool, idx InventoryIndex) ([]string, string, error) {
	_, byKey, _ := loadItems()
	if strings.TrimSpace(item) != "" {
		base := item
		explicitDamage := ""
		parts := strings.Split(item, ":")
		if len(parts) == 3 {
			base = strings.Join(parts[:2], ":")
			explicitDamage = parts[2]
		}
		if explicitDamage != "" {
			damage, _ = strconv.Atoi(explicitDamage)
		}
		items, _, err := loadItems()
		if err != nil {
			return nil, "", err
		}
		keys := make([]string, 0)
		for _, it := range items {
			if strings.EqualFold(it.RegName, base) {
				if anyDamage || explicitDamage == "" && damage == unsetDamage || it.Damage == damage {
					keys = append(keys, itemKey(it.ID, it.Damage))
				}
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return nil, "", fmt.Errorf("error: no items matched --item %s", item)
		}
		if !anyDamage && explicitDamage != "" {
			keys = keys[:1]
		}
		label := item
		if len(keys) == 1 {
			if meta, ok := byKey[keys[0]]; ok {
				label = fmt.Sprintf("%d:%d (%s)", meta.ID, meta.Damage, valueOr(meta.DisplayName, meta.RegName))
			}
		}
		return keys, label, nil
	}
	if id == 0 || damage == unsetDamage {
		return nil, "", errors.New("error: provide --item <mod:name[:damage]> or --id with --damage")
	}
	key := itemKey(id, damage)
	label := key
	if meta, ok := byKey[key]; ok {
		label = fmt.Sprintf("%s (%s)", key, valueOr(meta.DisplayName, meta.RegName))
	}
	return []string{key}, label, nil
}

func cmdFindItem(args []string) error {
	fs := flag.NewFlagSet("find-item", flag.ContinueOnError)
	query := fs.String("query", "", "")
	scope := fs.String("scope", "all", "")
	player := fs.String("player", "", "")
	limit := fs.Int("limit", 20, "")
	fs.Bool("oredict", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *query == "" && fs.NArg() > 0 {
		*query = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(*query) == "" {
		return errors.New("error: --query is required")
	}
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	matches, err := resolveQuery(*query, idx, 8)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("item resolve failed for query: %s", *query)
	}
	bestScoreGroup := matches[:1]
	if len(matches) > 1 && !itemMatchesQuery(matches[0], *query) {
		fmt.Fprintf(os.Stderr, "error: ambiguous item query %q matched %d items; use --item modname:name[:damage]\n", *query, len(matches))
		for _, m := range matches {
			fmt.Fprintf(os.Stderr, "- %s (slug=%s reg=%s:%d)\n", m.DisplayName, m.Slug, m.RegName, m.Damage)
		}
		return errors.New("ambiguous item query")
	}
	keys := []string{itemKey(bestScoreGroup[0].ID, bestScoreGroup[0].Damage)}
	label := fmt.Sprintf("%s -> %s", *query, bestScoreGroup[0].DisplayName)
	fmt.Printf("Resolved item query %q -> slug=%s id=%d damage=%d\n", *query, bestScoreGroup[0].Slug, bestScoreGroup[0].ID, bestScoreGroup[0].Damage)
	return printFind(idx, keys, label, *scope, *player, *limit, "resolved")
}

func itemMatchesQuery(item ItemMeta, query string) bool {
	qn := normalize(query)
	for _, alias := range item.Aliases {
		if alias == qn {
			return true
		}
	}
	return false
}

func printFind(idx InventoryIndex, keys []string, label, scope, playerFilter string, limit int, mode string) error {
	usePlayers, useContainers, useME, err := scopeSet(scope)
	if err != nil {
		return err
	}
	if limit <= 0 {
		limit = 20
	}
	ref := selectedPlayer(idx, playerFilter)
	fmt.Println("Item:", label)
	fmt.Println(freshness(idx.Source))
	fmt.Printf("Inventory find mode=%s keys=%d scope=%s", mode, len(keys), valueOr(scope, "all"))
	if playerFilter != "" {
		fmt.Printf(" player=%s", playerFilter)
	}
	fmt.Println()
	if ref != nil {
		fmt.Printf("Reference player: %s pos=(%.0f,%.0f,%.0f) dim=%d\n", ref.Name, ref.Pos.X, ref.Pos.Y, ref.Pos.Z, ref.Dimension)
	}
	hits := mergeHits(idx, keys)
	if usePlayers {
		fmt.Println("Players:")
		printPlayers(hits.Players, playerFilter, limit)
	}
	if useContainers {
		fmt.Println("Containers:")
		printChests(hits.Chests, ref, limit)
	}
	if useME {
		fmt.Println("ME:")
		printME(hits.ME, limit)
	}
	return nil
}

func mergeHits(idx InventoryIndex, keys []string) ItemHits {
	out := ItemHits{}
	for _, k := range keys {
		h := idx.ItemIndex[k]
		out.Players = append(out.Players, h.Players...)
		out.Chests = append(out.Chests, h.Chests...)
		out.ME = append(out.ME, h.ME...)
	}
	return out
}

func printPlayers(players []PlayerHit, filter string, limit int) {
	type acc struct {
		PlayerHit
	}
	by := map[string]*acc{}
	for _, p := range players {
		if filter != "" && !strings.EqualFold(p.Name, filter) && !strings.EqualFold(p.UUID, filter) {
			continue
		}
		row := by[p.UUID]
		if row == nil {
			cp := p
			row = &acc{PlayerHit: cp}
			by[p.UUID] = row
		} else {
			row.TotalCount += p.TotalCount
			row.Locations = append(row.Locations, p.Locations...)
		}
	}
	rows := make([]acc, 0, len(by))
	for _, v := range by {
		rows = append(rows, *v)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TotalCount > rows[j].TotalCount })
	if len(rows) == 0 {
		fmt.Println("(none)")
		return
	}
	for i, p := range rows {
		if i >= limit {
			break
		}
		fmt.Printf("- %s (%s) count=%d pos=(%.0f,%.0f,%.0f) dim=%d\n", p.Name, p.UUID, p.TotalCount, p.Pos.X, p.Pos.Y, p.Pos.Z, p.Dimension)
	}
}

func printChests(chests []ChestHit, ref *PlayerRecord, limit int) {
	sort.Slice(chests, func(i, j int) bool {
		di := chestDistance(chests[i], ref)
		dj := chestDistance(chests[j], ref)
		if di == dj {
			return chests[i].TotalCount > chests[j].TotalCount
		}
		return di < dj
	})
	if len(chests) == 0 {
		fmt.Println("(none)")
		return
	}
	for i, c := range chests {
		if i >= limit {
			break
		}
		fmt.Printf("- count=%d at (%d,%d,%d) dim=%d type=%s", c.TotalCount, c.X, c.Y, c.Z, c.Dimension, valueOr(c.Type, "container"))
		if ref != nil && ref.Dimension == c.Dimension {
			fmt.Printf(" dist=%.1f", chestDistance(c, ref))
		}
		fmt.Println()
	}
}

func chestDistance(c ChestHit, ref *PlayerRecord) float64 {
	if ref == nil || ref.Dimension != c.Dimension {
		return math.MaxFloat64
	}
	dx := float64(c.X) - ref.Pos.X
	dy := float64(c.Y) - ref.Pos.Y
	dz := float64(c.Z) - ref.Pos.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func printME(rows []MEHit, limit int) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalCount == rows[j].TotalCount {
			return strings.ToLower(rows[i].Label) < strings.ToLower(rows[j].Label)
		}
		return rows[i].TotalCount > rows[j].TotalCount
	})
	if len(rows) == 0 {
		fmt.Println("(none)")
		return
	}
	for i, m := range rows {
		if i >= limit {
			break
		}
		fmt.Printf("- %s count=%d at (%.0f,%.0f,%.0f) dim=%d\n", valueOr(m.Label, "ME network"), m.TotalCount, m.Pos.X, m.Pos.Y, m.Pos.Z, m.Dimension)
	}
}

func cmdChest(args []string) error {
	fs := flag.NewFlagSet("chest", flag.ContinueOnError)
	x := fs.Int("x", 0, "")
	y := fs.Int("y", 0, "")
	z := fs.Int("z", 0, "")
	dim := fs.Int("dim", 0, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	var found *ChestRecord
	for i := range idx.Chests {
		c := &idx.Chests[i]
		if c.Dimension == *dim && c.X == *x && c.Y == *y && c.Z == *z {
			found = c
			break
		}
	}
	if found == nil {
		fmt.Printf("Container at (%d,%d,%d) dim=%d\n", *x, *y, *z, *dim)
		fmt.Println(freshness(idx.Source))
		fmt.Println("(none)")
		return nil
	}
	fmt.Printf("Container at (%d,%d,%d) dim=%d type=%s", found.X, found.Y, found.Z, found.Dimension, valueOr(found.Type, "container"))
	if found.Source != "" {
		fmt.Printf(" source=%s", found.Source)
	}
	fmt.Println()
	fmt.Println(freshness(idx.Source))
	if len(found.Items) == 0 {
		fmt.Println("(empty)")
		return nil
	}
	_, byKey, _ := loadItems()
	items := append([]ItemStack(nil), found.Items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Slot != items[j].Slot {
			return items[i].Slot < items[j].Slot
		}
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return items[i].Damage < items[j].Damage
	})
	for _, it := range items {
		label := fmt.Sprintf("%d:%d", it.ID, it.Damage)
		if meta, ok := byKey[itemKey(it.ID, it.Damage)]; ok {
			label = valueOr(meta.DisplayName, meta.RegName)
		}
		fmt.Printf("- slot=%d count=%d item=%s id=%d damage=%d", it.Slot, it.Count, label, it.ID, it.Damage)
		if it.Source != "" {
			fmt.Printf(" src=%s", it.Source)
		}
		if it.Custom != "" {
			fmt.Printf(" name=%q", it.Custom)
		}
		fmt.Println()
	}
	return nil
}

func blockKey(id, meta int) string {
	return fmt.Sprintf("%d:%d", id, meta)
}

func cmdFindBlock(args []string) error {
	fs := flag.NewFlagSet("find-block", flag.ContinueOnError)
	block := fs.String("block", "", "")
	id := fs.Int("id", 0, "")
	meta := fs.Int("meta", unsetDamage, "")
	limit := fs.Int("limit", 20, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	keys, label, err := resolveBlockKeys(idx, *block, *id, *meta)
	if err != nil {
		return err
	}
	if *limit <= 0 {
		*limit = 20
	}
	fmt.Println("Block:", label)
	fmt.Println(freshness(idx.Source))
	if !idx.BlockStatus.RegistryAvailable {
		fmt.Println("Block registry: unavailable; using exported block names when present, otherwise numeric id/meta search only")
	}
	if idx.BlockStatus.Reason != "" {
		fmt.Println("Block status:", idx.BlockStatus.Reason)
	}
	fmt.Printf("Block find keys=%d\n", len(keys))
	hits := mergeBlockHits(idx, keys)
	if len(hits) == 0 {
		fmt.Println("(none)")
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Dimension != hits[j].Dimension {
			return hits[i].Dimension < hits[j].Dimension
		}
		if hits[i].X != hits[j].X {
			return hits[i].X < hits[j].X
		}
		if hits[i].Y != hits[j].Y {
			return hits[i].Y < hits[j].Y
		}
		return hits[i].Z < hits[j].Z
	})
	for i, h := range hits {
		if i >= *limit {
			break
		}
		name := valueOr(h.Name, h.RegName)
		if name != "" {
			fmt.Printf("- %d:%d (%s) at (%d,%d,%d) dim=%d\n", h.ID, h.Meta, name, h.X, h.Y, h.Z, h.Dimension)
		} else {
			fmt.Printf("- %d:%d at (%d,%d,%d) dim=%d\n", h.ID, h.Meta, h.X, h.Y, h.Z, h.Dimension)
		}
	}
	return nil
}

func resolveBlockKeys(idx InventoryIndex, block string, id, meta int) ([]string, string, error) {
	block = strings.TrimSpace(block)
	if block != "" {
		base := block
		explicitMeta := unsetDamage
		parts := strings.Split(block, ":")
		if len(parts) == 3 {
			base = strings.Join(parts[:2], ":")
			explicitMeta, _ = strconv.Atoi(parts[2])
		}
		queryNorm := normalize(base)
		keys := make([]string, 0)
		for k, h := range idx.BlockIndex {
			for _, b := range h.Blocks {
				if blockRecordMatches(b, base, queryNorm) && (explicitMeta == unsetDamage || b.Meta == explicitMeta) {
					keys = append(keys, k)
					break
				}
			}
		}
		sort.Strings(keys)
		keys = dedupeStrings(keys)
		if len(keys) == 0 {
			return nil, "", fmt.Errorf("error: no indexed blocks matched --block %s", block)
		}
		return keys, block, nil
	}
	if id <= 0 || meta == unsetDamage {
		return nil, "", errors.New("error: provide --block <mod:name[:meta]> or --id with --meta")
	}
	return []string{blockKey(id, meta)}, blockKey(id, meta), nil
}

func blockRecordMatches(b BlockHit, base, queryNorm string) bool {
	if strings.EqualFold(b.RegName, base) || strings.EqualFold(b.Name, base) {
		return true
	}
	if queryNorm == "" {
		return false
	}
	return normalize(b.RegName) == queryNorm || normalize(b.Name) == queryNorm
}

func dedupeStrings(in []string) []string {
	out := in[:0]
	last := ""
	for _, s := range in {
		if s == last {
			continue
		}
		out = append(out, s)
		last = s
	}
	return out
}

func mergeBlockHits(idx InventoryIndex, keys []string) []BlockHit {
	out := make([]BlockHit, 0)
	for _, k := range keys {
		out = append(out, idx.BlockIndex[k].Blocks...)
	}
	return out
}

func cmdRefresh(args []string) error {
	scope := "all"
	for _, arg := range args {
		switch arg {
		case "--players":
			scope = "players"
		case "--chests", "--containers":
			scope = "chests"
		case "--me":
			scope = "me"
		case "--block-inventories", "--block_inventories":
			scope = "block-inventories"
		case "--blocks":
			scope = "blocks"
		case "--all":
			scope = "all"
		default:
			return fmt.Errorf("unknown refresh option %s", arg)
		}
	}
	path := defaultRefreshFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]string{"requested_at": time.Now().UTC().Format(time.RFC3339), "scope": scope, "requested_by": "tool"})
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Printf("refresh requested (%s)\n", scope)
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gtnh_inventory_query status|find|find-item|find-block|chest|refresh ...")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "status":
		err = cmdStatus()
	case "find":
		err = cmdFind(os.Args[2:])
	case "find-item":
		err = cmdFindItem(os.Args[2:])
	case "find-block":
		err = cmdFindBlock(os.Args[2:])
	case "chest":
		err = cmdChest(os.Args[2:])
	case "refresh":
		err = cmdRefresh(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	flag.CommandLine.SetOutput(os.Stderr)
}
