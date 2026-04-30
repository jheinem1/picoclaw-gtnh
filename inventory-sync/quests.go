package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type QuestSourceMeta struct {
	ServerID       string `json:"server_id"`
	QuestsScanAt   string `json:"quests_scan_at"`
	DatHostSyncAt  string `json:"dathost_sync_at,omitempty"`
	ProgressFiles  int    `json:"progress_files"`
	QuestDBPresent bool   `json:"quest_database_present"`
	NameCacheFound bool   `json:"name_cache_found"`
	PartiesFound   bool   `json:"questing_parties_found"`
}

type QuestStats struct {
	QuestCount      int `json:"quest_count"`
	OpenCount       int `json:"open_count"`
	CompletedCount  int `json:"completed_count"`
	RequiredItemCnt int `json:"required_item_count"`
}

type QuestIndex struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generated_at"`
	Source      QuestSourceMeta `json:"source"`
	Stats       QuestStats      `json:"stats"`
	Quests      []QuestRecord   `json:"quests"`
}

type QuestStatus struct {
	GeneratedAt string            `json:"generated_at"`
	Source      QuestSourceMeta   `json:"source"`
	Stats       QuestStats        `json:"stats"`
	Stale       map[string]bool   `json:"stale"`
	Errors      map[string]string `json:"errors"`
}

type QuestRecord struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Description   string      `json:"description,omitempty"`
	Completed     bool        `json:"completed"`
	Prerequisites []string    `json:"prerequisites,omitempty"`
	Tasks         []QuestTask `json:"tasks,omitempty"`
}

type QuestTask struct {
	Type          string      `json:"type,omitempty"`
	Description   string      `json:"description,omitempty"`
	RequiredItems []QuestItem `json:"required_items,omitempty"`
}

type QuestItem struct {
	ID          int    `json:"id,omitempty"`
	Damage      int    `json:"damage,omitempty"`
	Count       int    `json:"count,omitempty"`
	RegName     string `json:"reg_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

func scanQuests(client *http.Client, cfg Config, syncAt string) (QuestIndex, error) {
	dbRaw, err := getFile(client, cfg, "world/betterquesting/QuestDatabase.json")
	if err != nil {
		return QuestIndex{}, err
	}
	quests, err := parseQuestDatabase(dbRaw)
	if err != nil {
		return QuestIndex{}, err
	}
	nameCacheFound := false
	if _, err := getFile(client, cfg, "world/betterquesting/NameCache.json"); err == nil {
		nameCacheFound = true
	}
	partiesFound := false
	if _, err := getFile(client, cfg, "world/betterquesting/QuestingParties.json"); err == nil {
		partiesFound = true
	}

	completed := map[string]bool{}
	progressFiles := 0
	if entries, err := listFiles(client, cfg, "world/betterquesting/QuestProgress/"); err == nil {
		for _, e := range entries {
			if e.Deleted || !strings.HasSuffix(e.Path, ".json") {
				continue
			}
			raw, err := getFile(client, cfg, "world/betterquesting/QuestProgress/"+filepath.Base(e.Path))
			if err != nil {
				continue
			}
			progressFiles++
			for id := range parseQuestProgress(raw) {
				completed[id] = true
			}
		}
	}

	completedCount := 0
	requiredItems := 0
	for i := range quests {
		if completed[quests[i].ID] {
			quests[i].Completed = true
			completedCount++
		}
		for _, task := range quests[i].Tasks {
			requiredItems += len(task.RequiredItems)
		}
	}

	sort.Slice(quests, func(i, j int) bool {
		ai, aok := atoiStrict(quests[i].ID)
		bi, bok := atoiStrict(quests[j].ID)
		if aok && bok && ai != bi {
			return ai < bi
		}
		return quests[i].ID < quests[j].ID
	})

	now := nowUTC()
	return QuestIndex{
		Version:     1,
		GeneratedAt: now,
		Source: QuestSourceMeta{
			ServerID:       cfg.DatHostServer,
			QuestsScanAt:   now,
			DatHostSyncAt:  syncAt,
			ProgressFiles:  progressFiles,
			QuestDBPresent: true,
			NameCacheFound: nameCacheFound,
			PartiesFound:   partiesFound,
		},
		Stats: QuestStats{
			QuestCount:      len(quests),
			OpenCount:       len(quests) - completedCount,
			CompletedCount:  completedCount,
			RequiredItemCnt: requiredItems,
		},
		Quests: quests,
	}, nil
}

func statusFromQuestIndex(index QuestIndex, errorsMap map[string]string) QuestStatus {
	if errorsMap == nil {
		errorsMap = map[string]string{}
	}
	now := time.Now().UTC()
	scanAt := parseRFC3339(index.Source.QuestsScanAt)
	return QuestStatus{
		GeneratedAt: nowUTC(),
		Source:      index.Source,
		Stats:       index.Stats,
		Stale: map[string]bool{
			"quests": scanAt.IsZero() || now.Sub(scanAt) > 2*time.Hour,
		},
		Errors: errorsMap,
	}
}

func parseQuestDatabase(raw []byte) ([]QuestRecord, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	nodes := map[string]map[string]any{}
	collectQuestNodes(root, "", nodes)

	quests := make([]QuestRecord, 0, len(nodes))
	for id, node := range nodes {
		q := QuestRecord{
			ID:            id,
			Title:         firstString(node, "name", "name:8", "Name", "title", "title:8", "Title", "questName", "questName:8"),
			Description:   firstString(node, "desc", "desc:8", "Desc", "description", "description:8", "Description", "subtitle", "text", "body"),
			Prerequisites: collectStringIDsFromKeys(node, "preRequisites", "prerequisites", "preReqs", "requiredQuests"),
			Tasks:         collectQuestTasks(node),
		}
		if q.Title == "" {
			q.Title = "Quest " + id
		}
		quests = append(quests, q)
	}
	return quests, nil
}

func collectQuestNodes(v any, keyHint string, out map[string]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		id := firstID(x, keyHint)
		if id != "" && looksLikeQuestNode(x) {
			out[id] = x
		}
		for k, child := range x {
			collectQuestNodes(child, k, out)
		}
	case []any:
		for _, child := range x {
			collectQuestNodes(child, keyHint, out)
		}
	}
}

func looksLikeQuestNode(m map[string]any) bool {
	if firstString(m, "name", "Name", "title", "Title", "questName", "questName:8") != "" {
		return true
	}
	if _, ok := m["tasks"]; ok {
		return true
	}
	if _, ok := m["tasks:9"]; ok {
		return true
	}
	if _, ok := m["properties:10"]; ok {
		return true
	}
	return false
}

func firstID(m map[string]any, keyHint string) string {
	for _, key := range []string{"questID", "questID:3", "id", "id:3", "ID"} {
		if n, ok := intFromAny(m[key]); ok {
			return strconv.Itoa(n)
		}
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if _, err := strconv.Atoi(keyHint); err == nil && keyHint != "" {
		return keyHint
	}
	return ""
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := stringFromQuestAny(m[key]); ok && strings.TrimSpace(s) != "" {
			return cleanQuestText(s)
		}
	}
	if props, ok := m["properties:10"].(map[string]any); ok {
		return firstString(props, keys...)
	}
	if props, ok := m["properties"].(map[string]any); ok {
		return firstString(props, keys...)
	}
	return ""
}

func cleanQuestText(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(s, "\u00a7r", "")), " "))
}

func stringFromQuestAny(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case map[string]any:
		for _, key := range []string{"text", "value", "name"} {
			if s, ok := stringFromQuestAny(x[key]); ok {
				return s, true
			}
		}
	}
	return "", false
}

func collectQuestTasks(node map[string]any) []QuestTask {
	var tasks []QuestTask
	for _, key := range []string{"tasks", "tasks:9", "tasks:10"} {
		collectTasksFromAny(node[key], &tasks)
	}
	if len(tasks) == 0 {
		items := collectQuestItems(node)
		if len(items) > 0 {
			tasks = append(tasks, QuestTask{Type: "item", RequiredItems: items})
		}
	}
	return tasks
}

func collectTasksFromAny(v any, out *[]QuestTask) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			collectTasksFromAny(item, out)
		}
	case map[string]any:
		items := collectQuestItems(x)
		desc := firstString(x, "name", "name:8", "title", "title:8", "desc", "desc:8", "description", "description:8")
		taskType := firstString(x, "taskID", "type", "taskType")
		if len(items) > 0 || desc != "" || taskType != "" {
			*out = append(*out, QuestTask{Type: taskType, Description: desc, RequiredItems: items})
			return
		}
		for _, child := range x {
			if childMap, ok := child.(map[string]any); ok {
				if _, hasID := childMap["id"]; hasID {
					continue
				}
			}
			collectTasksFromAny(child, out)
		}
	}
}

func collectQuestItems(v any) []QuestItem {
	var out []QuestItem
	collectQuestItemsInto(v, &out)
	return dedupeQuestItems(out)
}

func collectQuestItemsInto(v any, out *[]QuestItem) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			collectQuestItemsInto(child, out)
		}
	case map[string]any:
		if item, ok := questItemFromMap(x); ok {
			*out = append(*out, item)
			return
		}
		for _, child := range x {
			collectQuestItemsInto(child, out)
		}
	}
}

func questItemFromMap(m map[string]any) (QuestItem, bool) {
	id, hasID := firstIntAny(m, "id", "id:3", "itemID", "itemID:3")
	reg, _ := stringFromQuestAny(firstAny(m, "registryName", "registry_name", "regName", "itemName", "name"))
	display, _ := stringFromQuestAny(firstAny(m, "displayName", "display_name", "display"))
	if !hasID && strings.TrimSpace(reg) == "" && strings.TrimSpace(display) == "" {
		return QuestItem{}, false
	}
	damage, _ := firstIntAny(m, "damage", "damage:3", "meta", "meta:3", "Damage")
	count, ok := firstIntAny(m, "required", "required:3", "count", "Count", "amount", "amount:3", "requiredCount")
	if !ok || count <= 0 {
		count = 1
	}
	return QuestItem{ID: id, Damage: damage, Count: count, RegName: strings.TrimSpace(reg), DisplayName: cleanQuestText(display)}, true
}

func firstAny(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func firstIntAny(m map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		if n, ok := intFromAny(m[key]); ok {
			return n, true
		}
	}
	return 0, false
}

func intFromAny(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		n := int(x)
		return n, float64(n) == x
	case int:
		return x, true
	case int64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	}
	return 0, false
}

func dedupeQuestItems(items []QuestItem) []QuestItem {
	seen := map[string]bool{}
	out := make([]QuestItem, 0, len(items))
	for _, item := range items {
		key := fmt.Sprintf("%d:%d:%s:%s:%d", item.ID, item.Damage, item.RegName, item.DisplayName, item.Count)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func collectStringIDsFromKeys(m map[string]any, keys ...string) []string {
	seen := map[string]bool{}
	for _, key := range keys {
		collectIDs(m[key], seen)
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func collectIDs(v any, out map[string]bool) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			collectIDs(child, out)
		}
	case map[string]any:
		if id := firstID(x, ""); id != "" {
			out[id] = true
		}
		for _, child := range x {
			collectIDs(child, out)
		}
	default:
		if n, ok := intFromAny(x); ok {
			out[strconv.Itoa(n)] = true
		}
	}
}

func parseQuestProgress(raw []byte) map[string]bool {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	collectCompletedQuestIDs(root, "", out)
	return out
}

func collectCompletedQuestIDs(v any, keyHint string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		id := firstID(x, keyHint)
		if id != "" && boolFromAny(firstAny(x, "complete", "completed", "done", "claimed")) {
			out[id] = true
		}
		if id != "" {
			if n, ok := firstIntAny(x, "state", "status"); ok && n > 0 {
				out[id] = true
			}
		}
		for k, child := range x {
			collectCompletedQuestIDs(child, k, out)
		}
	case []any:
		for _, child := range x {
			collectCompletedQuestIDs(child, keyHint, out)
		}
	default:
		if boolFromAny(x) {
			if _, err := strconv.Atoi(keyHint); err == nil && keyHint != "" {
				out[keyHint] = true
			}
		}
	}
}

func boolFromAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x > 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "complete", "completed", "done", "claimed", "1":
			return true
		}
	}
	return false
}

func atoiStrict(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}
