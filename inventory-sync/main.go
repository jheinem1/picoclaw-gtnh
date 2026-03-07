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
	HTTPTimeout        time.Duration
	MaxRegionFiles     int
	ScanDims           []int
	ChestBounds        *ChestBounds
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

type RuntimeState struct {
	LastPlayersScan string `json:"last_players_scan"`
	LastChestsScan  string `json:"last_chests_scan"`
}

type RefreshRequest struct {
	RequestedAt string `json:"requested_at"`
	Scope       string `json:"scope"`
	RequestedBy string `json:"requested_by"`
}

type SourceMeta struct {
	ServerID       string `json:"server_id"`
	PlayersScanAt  string `json:"players_scan_at"`
	ChestsScanAt   string `json:"chests_scan_at"`
	DatHostSyncAt  string `json:"dathost_sync_at"`
	PlayersVersion int    `json:"players_version"`
	ChestsVersion  int    `json:"chests_version"`
}

type IndexStats struct {
	PlayerCount        int `json:"player_count"`
	ChestCount         int `json:"chest_count"`
	IndexedItemKeys    int `json:"indexed_item_keys"`
	PlayerStacks       int `json:"player_stacks"`
	EnderStacks        int `json:"ender_stacks"`
	ChestStacks        int `json:"chest_stacks"`
	RegionFilesScanned int `json:"region_files_scanned"`
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
	Dimension int         `json:"dim"`
	X         int         `json:"x"`
	Y         int         `json:"y"`
	Z         int         `json:"z"`
	Type      string      `json:"type"`
	Items     []ItemStack `json:"items"`
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

type ItemHits struct {
	Players []PlayerHit `json:"players"`
	Chests  []ChestHit  `json:"chests"`
}

type InventoryIndex struct {
	Version     int                 `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	Source      SourceMeta          `json:"source"`
	Stats       IndexStats          `json:"stats"`
	Players     []PlayerRecord      `json:"players"`
	Chests      []ChestRecord       `json:"chests"`
	ItemIndex   map[string]ItemHits `json:"item_index"`
}

type InventoryStatus struct {
	GeneratedAt string            `json:"generated_at"`
	Source      SourceMeta        `json:"source"`
	Stats       IndexStats        `json:"stats"`
	Stale       map[string]bool   `json:"stale"`
	Errors      map[string]string `json:"errors"`
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
		WorkDir:            getenv("INVENTORY_WORKDIR", "/root/.picoclaw/workspace"),
		StateFile:          getenv("INVENTORY_STATE_FILE", "/var/lib/inventory-sync/state.json"),
		PlayersInterval:    time.Duration(max(60, getenvInt("INVENTORY_PLAYERS_INTERVAL_SECONDS", 600))) * time.Second,
		ChestsInterval:     time.Duration(max(300, getenvInt("INVENTORY_CHESTS_INTERVAL_SECONDS", 21600))) * time.Second,
		HTTPTimeout:        time.Duration(max(5, getenvInt("INVENTORY_HTTP_TIMEOUT_SECONDS", 20))) * time.Second,
		MaxRegionFiles:     max(0, getenvInt("INVENTORY_MAX_REGION_FILES_PER_RUN", 64)),
		ScanDims:           parseDims(getenv("INVENTORY_SCAN_DIMS", "0,-1,1")),
		ChestBounds:        parseChestBounds(strings.TrimSpace(os.Getenv("INVENTORY_CHEST_BOUNDS"))),
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
	idx := InventoryIndex{Version: 1, ItemIndex: map[string]ItemHits{}}
	if err := loadJSONFile(path, &idx); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("event=inventory_index_load_error file=%q err=%q", path, err.Error())
		}
		return idx
	}
	if idx.ItemIndex == nil {
		idx.ItemIndex = map[string]ItemHits{}
	}
	return idx
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
	if s != "players" && s != "chests" && s != "all" {
		req.Scope = "all"
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
		id := numberToInt(m["id"])
		damage := numberToInt(m["Damage"])
		count := numberToInt(m["Count"])
		slot := numberToInt(m["Slot"])
		if id == 0 || count <= 0 {
			continue
		}
		custom := extractCustomName(toMap(m["tag"]))
		out = append(out, ItemStack{ID: id, Damage: damage, Count: count, Slot: slot, Source: source, Custom: custom})
		nested := parseNestedStacks(m["tag"], source+":nested", slot, 0)
		if len(nested) > 0 {
			out = append(out, nested...)
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
		id := numberToInt(t["id"])
		count := numberToInt(t["Count"])
		if id != 0 && count > 0 {
			damage := numberToInt(t["Damage"])
			slot := parentSlot
			if _, ok := t["Slot"]; ok {
				slot = numberToInt(t["Slot"])
			}
			custom := extractCustomName(toMap(t["tag"]))
			out = append(out, ItemStack{ID: id, Damage: damage, Count: count, Slot: slot, Source: source, Custom: custom})
		}
		for _, child := range t {
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
	for _, k := range keys {
		items := parseItemList(toList(te[k]), "tile")
		if len(items) > 0 {
			out = append(out, items...)
		}
	}

	if len(out) == 0 {
		for _, v := range te {
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
				continue
			}
			if _, hasCount := first["Count"]; hasCount {
				out = append(out, parseItemList(lst, "tile")...)
			}
		}
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

func itemKey(id, damage int) string {
	return fmt.Sprintf("%d:%d", id, damage)
}

func indexFromData(players []PlayerRecord, chests []ChestRecord, source SourceMeta, stats IndexStats) InventoryIndex {
	idx := InventoryIndex{
		Version:     1,
		GeneratedAt: nowUTC(),
		Source:      source,
		Stats:       stats,
		Players:     players,
		Chests:      chests,
		ItemIndex:   map[string]ItemHits{},
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
				h.Chests = append(h.Chests, ChestHit{Dimension: c.Dimension, X: c.X, Y: c.Y, Z: c.Z, Type: c.Type, TotalCount: it.Count})
			}
			idx.ItemIndex[k] = h
		}
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
		idx.ItemIndex[k] = h
	}
	idx.Stats.IndexedItemKeys = len(idx.ItemIndex)
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
	case -1:
		return "world/DIM-1/region/", true
	case 1:
		return "world/DIM1/region/", true
	default:
		return "", false
	}
}

func scanChests(client *http.Client, cfg Config) ([]ChestRecord, int, int, error) {
	all := make([]ChestRecord, 0, 512)
	regionCount := 0
	chestStacks := 0
	for _, dim := range cfg.ScanDims {
		path, ok := dimPath(dim)
		if !ok {
			continue
		}
		entries, err := listFiles(client, cfg, path)
		if err != nil {
			log.Printf("event=inventory_region_list_error path=%q err=%q", path, err.Error())
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
			regionFiles = regionFiles[:cfg.MaxRegionFiles]
		}
		for _, relPath := range regionFiles {
			regionCount++
			fullPath := path + filepath.Base(relPath)
			raw, err := getFile(client, cfg, fullPath)
			if err != nil {
				log.Printf("event=inventory_region_file_error file=%q err=%q", fullPath, err.Error())
				continue
			}
			chests, err := parseMCAChests(raw, dim)
			if err != nil {
				log.Printf("event=inventory_region_parse_error file=%q err=%q", fullPath, err.Error())
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
	return all, regionCount, chestStacks, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("event=inventory_startup_error err=%q", err.Error())
	}

	stateFile := cfg.StateFile
	indexFile := filepath.Join(cfg.WorkDir, "state", "inventory_index.json")
	statusFile := filepath.Join(cfg.WorkDir, "state", "inventory_status.json")
	refreshFile := filepath.Join(cfg.WorkDir, "state", "inventory_refresh.json")

	client := &http.Client{Timeout: cfg.HTTPTimeout + 3*time.Second}
	state := loadRuntimeState(stateFile)

	if cfg.ChestBounds != nil {
		log.Printf("event=inventory_startup_ok enabled=%t workdir=%q state_file=%q players_interval=%s chests_interval=%s chest_bounds=%d,%d,%d,%d,%d", cfg.Enabled, cfg.WorkDir, stateFile, cfg.PlayersInterval, cfg.ChestsInterval, cfg.ChestBounds.Dim, cfg.ChestBounds.MinX, cfg.ChestBounds.MinZ, cfg.ChestBounds.MaxX, cfg.ChestBounds.MaxZ)
	} else {
		log.Printf("event=inventory_startup_ok enabled=%t workdir=%q state_file=%q players_interval=%s chests_interval=%s", cfg.Enabled, cfg.WorkDir, stateFile, cfg.PlayersInterval, cfg.ChestsInterval)
	}

	for {
		if !cfg.Enabled {
			time.Sleep(cfg.LoopSleep)
			continue
		}

		now := time.Now().UTC()
		playersDue := parseRFC3339(state.LastPlayersScan).IsZero() || now.Sub(parseRFC3339(state.LastPlayersScan)) >= cfg.PlayersInterval
		chestsDue := parseRFC3339(state.LastChestsScan).IsZero() || now.Sub(parseRFC3339(state.LastChestsScan)) >= cfg.ChestsInterval

		refreshReq, hasRefresh := loadRefreshRequest(refreshFile)
		if hasRefresh {
			switch refreshReq.Scope {
			case "players":
				playersDue = true
			case "chests":
				chestsDue = true
			default:
				playersDue = true
				chestsDue = true
			}
		}

		if !playersDue && !chestsDue {
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
		stats := prev.Stats
		source := prev.Source
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

		if chestsDue {
			c, regionCount, chestStacks, err := scanChests(client, cfg)
			if err != nil {
				errorsMap["chests"] = err.Error()
				log.Printf("event=inventory_chests_scan_error err=%q", err.Error())
			} else {
				chests = c
				state.LastChestsScan = nowUTC()
				source.ChestsScanAt = state.LastChestsScan
				source.ChestsVersion++
				stats.ChestCount = len(chests)
				stats.ChestStacks = chestStacks
				stats.RegionFilesScanned = regionCount
			}
		}

		index := indexFromData(players, chests, source, stats)
		if err := atomicWriteJSON(indexFile, index); err != nil {
			log.Printf("event=inventory_index_write_error file=%q err=%q", indexFile, err.Error())
			errorsMap["index_write"] = err.Error()
		}

		now2 := time.Now().UTC()
		status := InventoryStatus{
			GeneratedAt: nowUTC(),
			Source:      index.Source,
			Stats:       index.Stats,
			Stale: map[string]bool{
				"players": parseRFC3339(index.Source.PlayersScanAt).IsZero() || now2.Sub(parseRFC3339(index.Source.PlayersScanAt)) > 30*time.Minute,
				"chests":  parseRFC3339(index.Source.ChestsScanAt).IsZero() || now2.Sub(parseRFC3339(index.Source.ChestsScanAt)) > 24*time.Hour,
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

		log.Printf("event=inventory_cycle_complete players=%d chests=%d item_keys=%d", index.Stats.PlayerCount, index.Stats.ChestCount, index.Stats.IndexedItemKeys)
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
		Stale: map[string]bool{
			"players": parseRFC3339(index.Source.PlayersScanAt).IsZero() || now2.Sub(parseRFC3339(index.Source.PlayersScanAt)) > 30*time.Minute,
			"chests":  parseRFC3339(index.Source.ChestsScanAt).IsZero() || now2.Sub(parseRFC3339(index.Source.ChestsScanAt)) > 24*time.Hour,
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
