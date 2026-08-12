package questplanner

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type itemMeta struct {
	Key         string
	ID          int
	Damage      int
	DisplayName string
	RegName     string
}

type Planner struct {
	quests          QuestIndex
	inventory       InventoryIndex
	inventoryTotals map[string]int
	questStatus     StatusFile
	inventoryStatus StatusFile
	tasks           []TaskRow
	itemsByKey      map[string]itemMeta
	keysByRegistry  map[string][]string
	keysByDisplay   map[string][]string
	warnings        []string
	now             func() time.Time
}

func LoadWorkspace(workspace string) (*Planner, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("workspace path is required")
	}
	p := &Planner{
		itemsByKey:     map[string]itemMeta{},
		keysByRegistry: map[string][]string{},
		keysByDisplay:  map[string][]string{},
		now:            time.Now,
	}
	compactQuestErr := loadJSON(filepath.Join(workspace, "state", "quest_planner_index.json"), &p.quests)
	if compactQuestErr != nil {
		if !os.IsNotExist(compactQuestErr) {
			p.warnings = append(p.warnings, "compact quest index unavailable: "+compactQuestErr.Error())
		}
		if err := loadJSON(filepath.Join(workspace, "state", "quest_index.json"), &p.quests); err != nil {
			return nil, fmt.Errorf("load quest index: %w", err)
		}
	}
	normalizeQuestIndex(&p.quests)
	compact := InventoryTotals{}
	compactErr := loadJSON(filepath.Join(workspace, "state", "quest_inventory_totals.json"), &compact)
	if compactErr == nil {
		p.inventory.Version = compact.Version
		p.inventory.GeneratedAt = compact.GeneratedAt
		p.inventory.Source = compact.Source
		p.inventoryTotals = compact.Totals
	} else {
		if !os.IsNotExist(compactErr) {
			p.warnings = append(p.warnings, "compact inventory totals unavailable: "+compactErr.Error())
		}
		if err := loadJSON(filepath.Join(workspace, "state", "inventory_index.json"), &p.inventory); err != nil {
			p.warnings = append(p.warnings, "inventory index unavailable: "+err.Error())
			p.inventory.ItemIndex = map[string]ItemHits{}
		}
	}
	if err := loadJSON(filepath.Join(workspace, "state", "quest_status.json"), &p.questStatus); err != nil && !os.IsNotExist(err) {
		p.warnings = append(p.warnings, "quest status unavailable: "+err.Error())
	}
	if err := loadJSON(filepath.Join(workspace, "state", "inventory_status.json"), &p.inventoryStatus); err != nil && !os.IsNotExist(err) {
		p.warnings = append(p.warnings, "inventory status unavailable: "+err.Error())
	}
	if err := p.loadItemMetadata(filepath.Join(workspace, "gtnh-data", "index", "item_index.tsv")); err != nil {
		p.warnings = append(p.warnings, "item metadata unavailable: "+err.Error())
	}
	if rows, err := loadTaskRows(filepath.Join(workspace, "state", "gtnh_tasks.tsv")); err == nil {
		p.tasks = rows
	} else if !os.IsNotExist(err) {
		p.warnings = append(p.warnings, "task log unavailable: "+err.Error())
	}
	if p.inventory.ItemIndex == nil {
		p.inventory.ItemIndex = map[string]ItemHits{}
	}
	p.warnings = sortedUnique(append(p.warnings, p.quests.Warnings...))
	return p, nil
}

func normalizeQuestIndex(index *QuestIndex) {
	questPosition := make(map[string]int, len(index.Quests))
	for i := range index.Quests {
		questPosition[index.Quests[i].ID] = i
		for taskIndex := range index.Quests[i].Tasks {
			if strings.TrimSpace(index.Quests[i].Tasks[taskIndex].ID) == "" {
				index.Quests[i].Tasks[taskIndex].ID = strconv.Itoa(taskIndex)
			}
		}
	}
	for i := range index.Quests {
		for _, prerequisiteID := range index.Quests[i].Prerequisites {
			if prerequisiteIndex, ok := questPosition[prerequisiteID]; ok {
				index.Quests[prerequisiteIndex].Unlocks = append(index.Quests[prerequisiteIndex].Unlocks, index.Quests[i].ID)
			}
		}
	}
	for i := range index.Quests {
		index.Quests[i].Unlocks = sortedUnique(index.Quests[i].Unlocks)
	}
}

func loadJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (p *Planner) loadItemMetadata(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return err
	}
	for i, row := range rows {
		if i == 0 || len(row) < 4 {
			continue
		}
		id, damage, ok := parseSlug(row[0])
		if !ok {
			continue
		}
		key := itemKey(id, damage)
		meta := itemMeta{Key: key, ID: id, Damage: damage, DisplayName: row[1], RegName: row[2]}
		p.itemsByKey[key] = meta
		regKey := normalizeIdentity(meta.RegName)
		if regKey != "" {
			p.keysByRegistry[regKey] = append(p.keysByRegistry[regKey], key)
		}
		displayKey := normalizeIdentity(meta.DisplayName)
		if displayKey != "" {
			p.keysByDisplay[displayKey] = append(p.keysByDisplay[displayKey], key)
		}
	}
	for key := range p.keysByRegistry {
		p.keysByRegistry[key] = sortedUnique(p.keysByRegistry[key])
	}
	for key := range p.keysByDisplay {
		p.keysByDisplay[key] = sortedUnique(p.keysByDisplay[key])
	}
	return nil
}

func parseSlug(slug string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(slug), "d")
	if len(parts) < 2 {
		return 0, 0, false
	}
	id, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	damage, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0, 0, false
	}
	return id, damage, true
}

func itemKey(id, damage int) string {
	return fmt.Sprintf("%d:%d", id, damage)
}

func normalizeIdentity(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func loadTaskRows(path string) ([]TaskRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	header := map[string]int{}
	for i, name := range rows[0] {
		header[strings.TrimSpace(name)] = i
	}
	value := func(row []string, name string) string {
		index, ok := header[name]
		if !ok || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[index])
	}
	out := make([]TaskRow, 0, len(rows)-1)
	for _, row := range rows[1:] {
		id, err := strconv.Atoi(value(row, "id"))
		if err != nil {
			continue
		}
		out = append(out, TaskRow{
			ID:          id,
			Status:      value(row, "status"),
			Priority:    value(row, "priority"),
			Area:        value(row, "area"),
			Title:       value(row, "title"),
			KanbanState: value(row, "kanban_status"),
			Owner:       value(row, "owner"),
			Description: value(row, "description"),
		})
	}
	return out, nil
}

func (p *Planner) inventoryCount(key string) int {
	if p.inventoryTotals != nil {
		return p.inventoryTotals[key]
	}
	hits := p.inventory.ItemIndex[key]
	total := 0
	for _, hit := range hits.Players {
		total += hit.TotalCount
	}
	for _, hit := range hits.Chests {
		total += hit.TotalCount
	}
	for _, hit := range hits.ME {
		total += hit.TotalCount
	}
	return total
}

func (p *Planner) freshness() string {
	return fmt.Sprintf("quests=%s; players=%s; containers=%s; me=%s",
		ageText(p.now(), p.quests.Source.QuestsScanAt),
		ageText(p.now(), p.inventory.Source.PlayersScanAt),
		ageText(p.now(), p.inventory.Source.ChestsScanAt),
		ageText(p.now(), p.inventory.Source.MEScanAt),
	)
}

func ageText(now time.Time, value string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return "unknown"
	}
	age := now.Sub(parsed)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return "fresh"
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
