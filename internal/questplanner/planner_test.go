package questplanner

import (
	"strings"
	"testing"
	"time"
)

func testPlanner() *Planner {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	return &Planner{
		quests: QuestIndex{
			Version: 2,
			Source:  QuestSource{QuestsScanAt: now.Add(-time.Minute).Format(time.RFC3339)},
			QuestLines: []QuestLine{
				{Name: "Tier 4 - EV", Order: 4, Tier: true, CompletedCount: 1, OpenCount: 3},
			},
		},
		inventory: InventoryIndex{
			Source: InventorySource{
				PlayersScanAt: now.Add(-time.Minute).Format(time.RFC3339),
				ChestsScanAt:  now.Add(-time.Minute).Format(time.RFC3339),
				MEScanAt:      now.Add(-time.Minute).Format(time.RFC3339),
			},
			ItemIndex: map[string]ItemHits{},
		},
		questStatus:     StatusFile{Stale: map[string]bool{}},
		inventoryStatus: StatusFile{Stale: map[string]bool{}},
		itemsByKey:      map[string]itemMeta{},
		keysByRegistry:  map[string][]string{},
		keysByDisplay:   map[string][]string{},
		now:             func() time.Time { return now },
	}
}

func TestPlanExcludesLockedQuestsAndUsesExactInventory(t *testing.T) {
	p := testPlanner()
	p.inventory.ItemIndex["7437:11305"] = ItemHits{
		Players: []CountHit{{TotalCount: 3}},
		Chests:  []CountHit{{TotalCount: 5}},
		ME:      []CountHit{{TotalCount: 8}},
	}
	p.quests.Quests = []Quest{
		{ID: "1", Title: "Foundation", Completed: true, State: "completed_claimed", Unlocks: []string{"2"}},
		{
			ID:            "2",
			Title:         "Build the EBF",
			QuestLine:     "Tier 4 - EV",
			TierQuestLine: true,
			Prerequisites: []string{"1"},
			Unlocks:       []string{"3"},
			State:         "ready",
			Ready:         true,
			Tasks:         []QuestTask{{ID: "0", Type: "bq_standard:retrieval", RequiredItems: []QuestItem{{ID: 7437, Damage: 11305, Count: 16, DisplayName: "Steel Ingot"}}}},
		},
		{ID: "3", Title: "Locked Future", QuestLine: "Tier 4 - EV", TierQuestLine: true, Prerequisites: []string{"999"}, State: "locked"},
	}

	plan := p.Plan("Snow", "what should I do next", 5)
	if len(plan.Recommendations) != 1 {
		t.Fatalf("recommendations = %#v, want one eligible quest", plan.Recommendations)
	}
	got := plan.Recommendations[0]
	if got.QuestID != "2" || !got.Eligible {
		t.Fatalf("unexpected recommendation: %#v", got)
	}
	if len(got.Materials) != 1 || got.Materials[0].Available != 16 || got.Materials[0].Missing != 0 {
		t.Fatalf("unexpected material assessment: %#v", got.Materials)
	}
	if !strings.Contains(got.NextStep, "Submit") || got.Confidence != "high" {
		t.Fatalf("unexpected action/confidence: %#v", got)
	}
	locked, err := p.ExplainQuest("3", "Snow", "")
	if err != nil {
		t.Fatal(err)
	}
	if locked.Eligible || locked.State != "locked" || strings.Join(locked.BlockedBy, ",") != "999" {
		t.Fatalf("locked quest should be excluded: %#v", locked)
	}
}

func TestRecommendPrefersProgressAndReportsShortage(t *testing.T) {
	p := testPlanner()
	p.inventory.ItemIndex["10:0"] = ItemHits{ME: []CountHit{{TotalCount: 6}}}
	p.quests.Quests = []Quest{
		{ID: "10", Title: "Ready Quest", QuestLine: "Tier 4 - EV", TierQuestLine: true, State: "ready"},
		{
			ID:              "11",
			Title:           "Nearly There",
			QuestLine:       "Tier 4 - EV",
			TierQuestLine:   true,
			State:           "in_progress",
			CompletionRatio: 0.5,
			Tasks:           []QuestTask{{ID: "0", RequiredItems: []QuestItem{{ID: 10, Damage: 0, Count: 10, DisplayName: "Circuit"}}}},
		},
	}

	got := p.Recommend("Snow", "")
	if got.QuestID != "11" {
		t.Fatalf("recommendation = %#v, want in-progress quest", got)
	}
	if !strings.Contains(got.NextStep, "Acquire 4 more Circuit") {
		t.Fatalf("next step = %q, want exact shortage", got.NextStep)
	}
	if strings.Join(got.MissingMaterials, ",") != "Circuit x4" {
		t.Fatalf("missing materials = %#v", got.MissingMaterials)
	}
}

func TestClaimRecommendationIsPersonalized(t *testing.T) {
	p := testPlanner()
	p.quests.Quests = []Quest{{
		ID:          "20",
		Title:       "A Finished Quest",
		Completed:   true,
		State:       "completed_unclaimed",
		ClaimableBy: []string{"Snow"},
	}}

	forSnow := p.Recommend("Snow", "")
	if forSnow.QuestID != "20" || forSnow.State != "completed_unclaimed" || !strings.Contains(forSnow.NextStep, "as Snow") {
		t.Fatalf("unexpected Snow recommendation: %#v", forSnow)
	}
	forAlex := p.Recommend("Alex", "")
	if forAlex.Source != "none" {
		t.Fatalf("completed quest should not be recommended to Alex: %#v", forAlex)
	}
}

func TestRequestedTierFiltersCandidates(t *testing.T) {
	p := testPlanner()
	p.quests.Quests = []Quest{
		{ID: "30", Title: "EV Work", QuestLine: "Tier 4 - EV", TierQuestLine: true, State: "ready"},
		{ID: "31", Title: "HV Work", QuestLine: "Tier 3 - HV", TierQuestLine: true, State: "ready"},
	}
	got := p.Recommend("Snow", "give me an HV quest")
	if got.QuestID != "31" {
		t.Fatalf("recommendation = %#v, want HV quest", got)
	}
}

func TestTaskOwnershipAndPriorityAreDeterministic(t *testing.T) {
	p := testPlanner()
	p.tasks = []TaskRow{
		{ID: 1, Title: "Someone else's task", KanbanState: "doing", Priority: "high", Owner: "Alex"},
		{ID: 2, Title: "Snow's task", KanbanState: "todo", Priority: "high", Owner: "Snow"},
	}
	got := p.Recommend("Snow", "")
	if got.Source != "task_log" || got.TaskID != 2 {
		t.Fatalf("recommendation = %#v, want Snow's task", got)
	}
}

func TestStaleInventoryLowersConfidence(t *testing.T) {
	p := testPlanner()
	p.inventoryStatus.Stale["me"] = true
	p.inventory.ItemIndex["1:0"] = ItemHits{ME: []CountHit{{TotalCount: 1}}}
	p.quests.Quests = []Quest{{
		ID: "40", Title: "Stale Materials", State: "ready",
		Tasks: []QuestTask{{ID: "0", RequiredItems: []QuestItem{{ID: 1, Count: 1, DisplayName: "Stone"}}}},
	}}
	got := p.Recommend("Snow", "")
	if got.Confidence != "low" {
		t.Fatalf("confidence = %q, want low", got.Confidence)
	}
}
