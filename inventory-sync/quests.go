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
	ServerID             string `json:"server_id"`
	QuestsScanAt         string `json:"quests_scan_at"`
	DatHostSyncAt        string `json:"dathost_sync_at,omitempty"`
	ProgressFiles        int    `json:"progress_files"`
	MatchedProgressFiles int    `json:"matched_progress_files"`
	QuestDBPresent       bool   `json:"quest_database_present"`
	NameCacheFound       bool   `json:"name_cache_found"`
	PartiesFound         bool   `json:"questing_parties_found"`
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
	Party       QuestParty      `json:"party"`
	Stats       QuestStats      `json:"stats"`
	QuestLines  []QuestLine     `json:"quest_lines,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
	Quests      []QuestRecord   `json:"quests"`
}

type QuestStatus struct {
	GeneratedAt string            `json:"generated_at"`
	Source      QuestSourceMeta   `json:"source"`
	Party       QuestParty        `json:"party"`
	Stats       QuestStats        `json:"stats"`
	Stale       map[string]bool   `json:"stale"`
	Warnings    []string          `json:"warnings,omitempty"`
	Errors      map[string]string `json:"errors"`
}

type QuestParty struct {
	ID            string             `json:"id,omitempty"`
	Name          string             `json:"name"`
	SelectionMode string             `json:"selection_mode"`
	MemberCount   int                `json:"member_count"`
	Members       []QuestPartyMember `json:"members,omitempty"`
}

type QuestPartyMember struct {
	UUID              string `json:"uuid"`
	Name              string `json:"name,omitempty"`
	ProgressFileFound bool   `json:"progress_file_found"`
}

type QuestLine struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Order          int    `json:"order"`
	Tier           bool   `json:"tier"`
	QuestCount     int    `json:"quest_count"`
	OpenCount      int    `json:"open_count"`
	CompletedCount int    `json:"completed_count"`
}

type QuestRecord struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Description    string      `json:"description,omitempty"`
	QuestLineID    string      `json:"quest_line_id,omitempty"`
	QuestLine      string      `json:"quest_line,omitempty"`
	QuestLineOrder int         `json:"quest_line_order,omitempty"`
	TierQuestLine  bool        `json:"tier_quest_line,omitempty"`
	Completed      bool        `json:"completed"`
	CompletedBy    []string    `json:"completed_by,omitempty"`
	Prerequisites  []string    `json:"prerequisites,omitempty"`
	Tasks          []QuestTask `json:"tasks,omitempty"`
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

type QuestProgress struct {
	Completed map[string]bool `json:"completed,omitempty"`
}

func scanQuests(client *http.Client, cfg Config, syncAt string) (QuestIndex, error) {
	warnings := []string{}
	dbRaw, err := getFile(client, cfg, "world/betterquesting/QuestDatabase.json")
	if err != nil {
		return QuestIndex{}, err
	}
	quests, err := parseQuestDatabase(dbRaw)
	if err != nil {
		return QuestIndex{}, err
	}
	lineByQuestID, questLines := parseQuestLines(dbRaw)
	for i := range quests {
		if line, ok := lineByQuestID[quests[i].ID]; ok {
			quests[i].QuestLineID = line.ID
			quests[i].QuestLine = line.Name
			quests[i].QuestLineOrder = line.Order
			quests[i].TierQuestLine = line.Tier
		}
	}
	nameMap := map[string]string{}
	nameCacheFound := false
	if nameRaw, err := getFile(client, cfg, "world/betterquesting/NameCache.json"); err == nil {
		nameMap = parseNameCacheFlexible(nameRaw)
		nameCacheFound = true
	} else {
		warnings = append(warnings, "NameCache.json not found; party members may be shown by UUID")
	}
	nameLookup := normalizedNameMap(nameMap)

	var parties []QuestParty
	partiesFound := false
	if partyRaw, err := getFile(client, cfg, "world/betterquesting/QuestingParties.json"); err == nil {
		parties = parseQuestingParties(partyRaw, nameLookup)
		partiesFound = true
	} else {
		warnings = append(warnings, "QuestingParties.json not found; selected party progress cannot be resolved")
	}
	party, selected := selectQuestParty(parties, cfg.QuestPartyName)
	if !selected {
		warnings = append(warnings, fmt.Sprintf("quest party %q not found", cfg.QuestPartyName))
	}
	if party.Name == "" {
		party.Name = cfg.QuestPartyName
		party.SelectionMode = "missing"
	}
	if len(party.Members) == 0 {
		warnings = append(warnings, fmt.Sprintf("quest party %q has no indexed members", party.Name))
	}

	completedBy := map[string]map[string]bool{}
	progressFiles := 0
	matchedProgressFiles := 0
	progressByUUID := map[string]QuestProgress{}
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
			uuid := normalizeUUID(strings.TrimSuffix(filepath.Base(e.Path), ".json"))
			progressByUUID[uuid] = parseQuestProgressRecord(raw)
		}
	} else {
		warnings = append(warnings, "QuestProgress directory not found; selected party progress cannot be resolved")
	}
	for i := range party.Members {
		memberUUID := normalizeUUID(party.Members[i].UUID)
		progress, ok := progressByUUID[memberUUID]
		if !ok {
			continue
		}
		party.Members[i].ProgressFileFound = true
		matchedProgressFiles++
		memberName := party.Members[i].Name
		if memberName == "" {
			memberName = party.Members[i].UUID
		}
		for id := range progress.Completed {
			if completedBy[id] == nil {
				completedBy[id] = map[string]bool{}
			}
			completedBy[id][memberName] = true
		}
	}
	if len(party.Members) > 0 && matchedProgressFiles == 0 {
		warnings = append(warnings, fmt.Sprintf("no QuestProgress files matched selected party %q members", party.Name))
	}
	party.MemberCount = len(party.Members)

	completedCount := 0
	requiredItems := 0
	lineStats := map[string]*QuestLine{}
	for _, line := range questLines {
		lineCopy := line
		lineStats[line.ID] = &lineCopy
	}
	for i := range quests {
		if by := sortedKeys(completedBy[quests[i].ID]); len(by) > 0 {
			quests[i].Completed = true
			quests[i].CompletedBy = by
			completedCount++
		}
		if line := lineStats[quests[i].QuestLineID]; line != nil {
			line.QuestCount++
			if quests[i].Completed {
				line.CompletedCount++
			} else {
				line.OpenCount++
			}
		}
		for _, task := range quests[i].Tasks {
			requiredItems += len(task.RequiredItems)
		}
	}
	questLines = make([]QuestLine, 0, len(lineStats))
	for _, line := range lineStats {
		questLines = append(questLines, *line)
	}
	sort.Slice(questLines, func(i, j int) bool {
		if questLines[i].Order != questLines[j].Order {
			return questLines[i].Order < questLines[j].Order
		}
		return questLines[i].Name < questLines[j].Name
	})

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
			ServerID:             cfg.DatHostServer,
			QuestsScanAt:         now,
			DatHostSyncAt:        syncAt,
			ProgressFiles:        progressFiles,
			MatchedProgressFiles: matchedProgressFiles,
			QuestDBPresent:       true,
			NameCacheFound:       nameCacheFound,
			PartiesFound:         partiesFound,
		},
		Party: party,
		Stats: QuestStats{
			QuestCount:      len(quests),
			OpenCount:       len(quests) - completedCount,
			CompletedCount:  completedCount,
			RequiredItemCnt: requiredItems,
		},
		QuestLines: questLines,
		Warnings:   warnings,
		Quests:     quests,
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
		Party:       index.Party,
		Stats:       index.Stats,
		Stale: map[string]bool{
			"quests": scanAt.IsZero() || now.Sub(scanAt) > 2*time.Hour,
		},
		Warnings: index.Warnings,
		Errors:   errorsMap,
	}
}

func parseQuestDatabase(raw []byte) ([]QuestRecord, error) {
	var root any
	if err := decodeQuestJSON(raw, &root); err != nil {
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

func parseQuestLines(raw []byte) (map[string]QuestLine, []QuestLine) {
	var root any
	if err := decodeQuestJSON(raw, &root); err != nil {
		return map[string]QuestLine{}, nil
	}
	lineByQuestID := map[string]QuestLine{}
	lineByID := map[string]QuestLine{}
	collectQuestLines(root, "", lineByQuestID, lineByID)
	lines := make([]QuestLine, 0, len(lineByID))
	for _, line := range lineByID {
		lines = append(lines, line)
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].Order != lines[j].Order {
			return lines[i].Order < lines[j].Order
		}
		return lines[i].Name < lines[j].Name
	})
	return lineByQuestID, lines
}

func collectQuestLines(v any, keyHint string, lineByQuestID map[string]QuestLine, lineByID map[string]QuestLine) {
	switch x := v.(type) {
	case map[string]any:
		if line, questIDs, ok := questLineFromMap(x, keyHint); ok {
			lineByID[line.ID] = line
			for _, questID := range questIDs {
				lineByQuestID[questID] = line
			}
			return
		}
		for k, child := range x {
			collectQuestLines(child, k, lineByQuestID, lineByID)
		}
	case []any:
		for _, child := range x {
			collectQuestLines(child, keyHint, lineByQuestID, lineByID)
		}
	}
}

func questLineFromMap(m map[string]any, keyHint string) (QuestLine, []string, bool) {
	if _, ok := m["quests:9"]; !ok {
		if _, ok := m["quests"]; !ok {
			return QuestLine{}, nil, false
		}
	}
	name := firstStringScoped(m, "name", "name:8", "Name", "title", "title:8")
	if name == "" {
		return QuestLine{}, nil, false
	}
	questIDs := collectQuestLineQuestIDs(firstAny(m, "quests:9", "quests"))
	if len(questIDs) == 0 {
		return QuestLine{}, nil, false
	}
	id := questLineIDFromMap(m)
	if id == "" {
		id = keyHint
	}
	order, _ := firstIntAny(m, "order", "order:3")
	line := QuestLine{
		ID:    strings.TrimSpace(id),
		Name:  name,
		Order: order,
		Tier:  isTierQuestLine(name),
	}
	return line, questIDs, true
}

func collectQuestLineQuestIDs(v any) []string {
	seen := map[string]bool{}
	collectQuestLineQuestIDsInto(v, seen)
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func collectQuestLineQuestIDsInto(v any, seen map[string]bool) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			collectQuestLineQuestIDsInto(child, seen)
		}
	case map[string]any:
		if id := questIDFromMap(x); id != "" {
			seen[id] = true
			return
		}
		for _, child := range x {
			collectQuestLineQuestIDsInto(child, seen)
		}
	}
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
	if firstAny(m, "questLineIDLow", "questLineIDLow:4", "questLineIDHigh", "questLineIDHigh:4") != nil {
		return false
	}
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
	if id := questIDFromMap(m); id != "" {
		return id
	}
	for _, key := range []string{"questID", "questID:3", "id", "id:3", "ID"} {
		if n, ok := intFromAny(m[key]); ok {
			return strconv.Itoa(n)
		}
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if n, ok := numericKeyHint(keyHint); ok {
		return strconv.Itoa(n)
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
	for _, child := range m {
		if childMap, ok := child.(map[string]any); ok {
			if s := firstString(childMap, keys...); s != "" {
				return s
			}
		}
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
	reg, _ := stringFromQuestAny(firstAny(m, "registryName", "registry_name", "regName", "itemName", "id:8", "name"))
	if !hasID {
		if s, ok := stringFromQuestAny(firstAny(m, "id")); ok && strings.Contains(s, ":") {
			reg = s
		}
	}
	display, _ := stringFromQuestAny(firstAny(m, "displayName", "display_name", "display"))
	if !hasID && strings.TrimSpace(reg) == "" && strings.TrimSpace(display) == "" {
		return QuestItem{}, false
	}
	damage, _ := firstIntAny(m, "damage", "damage:3", "Damage", "Damage:2", "meta", "meta:3", "Meta")
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

func parseNameCacheFlexible(raw []byte) map[string]string {
	out := map[string]string{}
	for uuid, name := range parseNameCache(raw) {
		out[uuid] = name
	}
	var root any
	if err := decodeQuestJSON(raw, &root); err != nil {
		return out
	}
	collectNameCacheRows(root, out)
	return out
}

func collectNameCacheRows(v any, out map[string]string) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			collectNameCacheRows(child, out)
		}
	case map[string]any:
		uuid := firstStringRaw(x, "uuid", "uuid:8", "UUID", "id", "id:8")
		name := firstStringRaw(x, "name", "name:8", "Name", "username", "username:8")
		if uuid != "" {
			if name == "" {
				name = uuid
			}
			out[uuid] = name
		}
		for _, child := range x {
			collectNameCacheRows(child, out)
		}
	}
}

func normalizedNameMap(names map[string]string) map[string]string {
	out := map[string]string{}
	for uuid, name := range names {
		out[normalizeUUID(uuid)] = strings.TrimSpace(name)
	}
	return out
}

func parseQuestingParties(raw []byte, names map[string]string) []QuestParty {
	var root any
	if err := decodeQuestJSON(raw, &root); err != nil {
		return nil
	}
	var parties []QuestParty
	collectQuestParties(root, "", names, &parties)
	sort.Slice(parties, func(i, j int) bool {
		return strings.ToLower(parties[i].Name) < strings.ToLower(parties[j].Name)
	})
	return parties
}

func collectQuestParties(v any, keyHint string, names map[string]string, out *[]QuestParty) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			collectQuestParties(child, keyHint, names, out)
		}
	case map[string]any:
		if party, ok := questPartyFromMap(x, keyHint, names); ok {
			*out = append(*out, party)
			return
		}
		for k, child := range x {
			collectQuestParties(child, k, names, out)
		}
	}
}

func questPartyFromMap(m map[string]any, keyHint string, names map[string]string) (QuestParty, bool) {
	name := firstStringScoped(m, "name", "name:8", "Name", "partyName", "partyName:8")
	members := collectPartyMembers(m, names)
	if name == "" || len(members) == 0 {
		return QuestParty{}, false
	}
	id := firstStringRaw(m, "partyID", "partyID:3", "partyId", "id", "id:3", "ID")
	if id == "" {
		id = keyHint
	}
	return QuestParty{
		ID:            strings.TrimSpace(id),
		Name:          name,
		SelectionMode: "name",
		MemberCount:   len(members),
		Members:       members,
	}, true
}

func firstStringScoped(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := stringFromQuestAny(m[key]); ok && strings.TrimSpace(s) != "" {
			return cleanQuestText(s)
		}
	}
	for _, propKey := range []string{"properties:10", "properties"} {
		if props, ok := m[propKey].(map[string]any); ok {
			if s := firstString(props, keys...); s != "" {
				return s
			}
		}
	}
	return ""
}

func collectPartyMembers(v any, names map[string]string) []QuestPartyMember {
	seen := map[string]bool{}
	var out []QuestPartyMember
	collectPartyMembersInto(v, names, seen, &out)
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func collectPartyMembersInto(v any, names map[string]string, seen map[string]bool, out *[]QuestPartyMember) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			collectPartyMembersInto(child, names, seen, out)
		}
	case map[string]any:
		uuid := firstStringRaw(x, "uuid", "uuid:8", "UUID", "playerUUID", "playerUUID:8")
		if uuid != "" {
			key := normalizeUUID(uuid)
			if !seen[key] {
				seen[key] = true
				*out = append(*out, QuestPartyMember{UUID: strings.TrimSpace(uuid), Name: names[key]})
			}
			return
		}
		for _, child := range x {
			collectPartyMembersInto(child, names, seen, out)
		}
	}
}

func selectQuestParty(parties []QuestParty, name string) (QuestParty, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, party := range parties {
		if strings.ToLower(strings.TrimSpace(party.Name)) == want {
			party.SelectionMode = "name"
			return party, true
		}
	}
	return QuestParty{}, false
}

func parseQuestProgress(raw []byte) map[string]bool {
	return parseQuestProgressRecord(raw).Completed
}

func parseQuestProgressRecord(raw []byte) QuestProgress {
	var root any
	if err := decodeQuestJSON(raw, &root); err != nil {
		return QuestProgress{Completed: map[string]bool{}}
	}
	out := map[string]bool{}
	collectCompletedQuestIDs(root, "", out)
	return QuestProgress{Completed: out}
}

func decodeQuestJSON(raw []byte, out any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	return dec.Decode(out)
}

func collectCompletedQuestIDs(v any, keyHint string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		id := firstID(x, keyHint)
		if firstAny(x, "taskID", "taskID:8", "taskType") != nil {
			id = ""
		}
		if id != "" && boolFromAny(firstAny(x, "complete", "completed", "done", "claimed")) {
			out[id] = true
		}
		if id != "" {
			for _, key := range []string{"completed:9", "completeUsers:9"} {
				if completedMap, ok := x[key].(map[string]any); ok && len(completedMap) > 0 {
					out[id] = true
				}
			}
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

func firstStringRaw(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := rawStringFromAny(m[key]); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func rawStringFromAny(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		n := int(x)
		if float64(n) == x {
			return strconv.Itoa(n), true
		}
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case json.Number:
		return x.String(), true
	case map[string]any:
		for _, key := range []string{"text", "value", "name"} {
			if s, ok := rawStringFromAny(x[key]); ok {
				return s, true
			}
		}
	}
	return "", false
}

func questIDFromMap(m map[string]any) string {
	low, hasLow := firstInt64Any(m, "questIDLow", "questIDLow:4")
	high, hasHigh := firstInt64Any(m, "questIDHigh", "questIDHigh:4")
	if !hasLow && !hasHigh {
		return ""
	}
	if !hasHigh || high == 0 {
		return strconv.FormatInt(low, 10)
	}
	return strconv.FormatInt(high, 10) + ":" + strconv.FormatInt(low, 10)
}

func questLineIDFromMap(m map[string]any) string {
	low, hasLow := firstInt64Any(m, "questLineIDLow", "questLineIDLow:4")
	high, hasHigh := firstInt64Any(m, "questLineIDHigh", "questLineIDHigh:4")
	if !hasLow && !hasHigh {
		return ""
	}
	if !hasHigh || high == 0 {
		return strconv.FormatInt(low, 10)
	}
	return strconv.FormatInt(high, 10) + ":" + strconv.FormatInt(low, 10)
}

func firstInt64Any(m map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		if n, ok := int64FromAny(m[key]); ok {
			return n, true
		}
	}
	return 0, false
}

func int64FromAny(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		n := int64(x)
		return n, float64(n) == x
	case int:
		return int64(x), true
	case int64:
		return x, true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func numericKeyHint(keyHint string) (int, bool) {
	keyHint = strings.TrimSpace(keyHint)
	if keyHint == "" {
		return 0, false
	}
	if before, _, ok := strings.Cut(keyHint, ":"); ok {
		keyHint = before
	}
	n, err := strconv.Atoi(keyHint)
	return n, err == nil
}

func isTierQuestLine(name string) bool {
	name = strings.TrimSpace(stripFormattingCodes(name))
	return strings.HasPrefix(strings.ToLower(name), "tier ")
}

func stripFormattingCodes(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		if skip {
			skip = false
			continue
		}
		if r == '§' {
			skip = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func normalizeUUID(uuid string) string {
	u := strings.ToLower(strings.TrimSpace(uuid))
	u = strings.TrimPrefix(strings.TrimSuffix(u, "}"), "{")
	return strings.ReplaceAll(u, "-", "")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func atoiStrict(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}
