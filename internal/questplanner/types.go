package questplanner

type QuestSource struct {
	QuestsScanAt string `json:"quests_scan_at"`
}

type QuestStats struct {
	QuestCount      int `json:"quest_count"`
	OpenCount       int `json:"open_count"`
	CompletedCount  int `json:"completed_count"`
	ReadyCount      int `json:"ready_count"`
	LockedCount     int `json:"locked_count"`
	InProgressCount int `json:"in_progress_count"`
	ClaimableCount  int `json:"claimable_count"`
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

type QuestIndex struct {
	Version     int         `json:"version"`
	GeneratedAt string      `json:"generated_at"`
	Source      QuestSource `json:"source"`
	Stats       QuestStats  `json:"stats"`
	QuestLines  []QuestLine `json:"quest_lines"`
	Warnings    []string    `json:"warnings"`
	Quests      []Quest     `json:"quests"`
}

type Quest struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Description     string           `json:"description"`
	QuestLineID     string           `json:"quest_line_id"`
	QuestLine       string           `json:"quest_line"`
	QuestLineOrder  int              `json:"quest_line_order"`
	TierQuestLine   bool             `json:"tier_quest_line"`
	Completed       bool             `json:"completed"`
	CompletedBy     []string         `json:"completed_by"`
	ClaimedBy       []string         `json:"claimed_by"`
	ClaimableBy     []string         `json:"claimable_by"`
	Prerequisites   []string         `json:"prerequisites"`
	Unlocks         []string         `json:"unlocks"`
	BlockedBy       []string         `json:"blocked_by"`
	State           string           `json:"state"`
	Ready           bool             `json:"ready"`
	CompletionRatio float64          `json:"completion_ratio"`
	PlayerProgress  []PlayerProgress `json:"player_progress"`
	Tasks           []QuestTask      `json:"tasks"`
}

type PlayerProgress struct {
	UUID             string   `json:"uuid"`
	Name             string   `json:"name"`
	Completed        bool     `json:"completed"`
	Claimed          bool     `json:"claimed"`
	ClaimStatusKnown bool     `json:"claim_status_known"`
	CompletedTasks   []string `json:"completed_tasks"`
}

type QuestTask struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"`
	Description   string      `json:"description"`
	RequiredItems []QuestItem `json:"required_items"`
	CompletedBy   []string    `json:"completed_by"`
}

type QuestItem struct {
	ID          int    `json:"id"`
	Damage      int    `json:"damage"`
	Count       int    `json:"count"`
	RegName     string `json:"reg_name"`
	DisplayName string `json:"display_name"`
}

type InventorySource struct {
	PlayersScanAt string `json:"players_scan_at"`
	ChestsScanAt  string `json:"chests_scan_at"`
	MEScanAt      string `json:"me_scan_at"`
}

type InventoryIndex struct {
	Version     int                 `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	Source      InventorySource     `json:"source"`
	ItemIndex   map[string]ItemHits `json:"item_index"`
}
type InventoryTotals struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generated_at"`
	Source      InventorySource `json:"source"`
	Totals      map[string]int  `json:"totals"`
}

type ItemHits struct {
	Players []CountHit `json:"players"`
	Chests  []CountHit `json:"chests"`
	ME      []CountHit `json:"me"`
}

type CountHit struct {
	TotalCount int `json:"total_count"`
}

type StatusFile struct {
	GeneratedAt string            `json:"generated_at"`
	Stale       map[string]bool   `json:"stale"`
	Warnings    []string          `json:"warnings"`
	Errors      map[string]string `json:"errors"`
}

type TaskRow struct {
	ID          int
	Status      string
	Priority    string
	Area        string
	Title       string
	KanbanState string
	Owner       string
	Description string
}

type MaterialAssessment struct {
	TaskID     string  `json:"task_id,omitempty"`
	Identity   string  `json:"identity"`
	Name       string  `json:"name"`
	Required   int     `json:"required"`
	Available  int     `json:"available"`
	Missing    int     `json:"missing"`
	Coverage   float64 `json:"coverage"`
	Resolved   bool    `json:"resolved"`
	Resolution string  `json:"resolution"`
}

type ScoreFactor struct {
	Factor string `json:"factor"`
	Points int    `json:"points"`
	Detail string `json:"detail"`
}

type Recommendation struct {
	Recommendation       string               `json:"recommendation"`
	Source               string               `json:"source"`
	QuestID              string               `json:"quest_id,omitempty"`
	TaskID               int                  `json:"task_id,omitempty"`
	QuestLine            string               `json:"quest_line,omitempty"`
	State                string               `json:"state"`
	Eligible             bool                 `json:"eligible"`
	ExclusionReasons     []string             `json:"exclusion_reasons,omitempty"`
	WhyEasy              string               `json:"why_easy"`
	NextStep             string               `json:"next_step"`
	Confidence           string               `json:"confidence"`
	Score                int                  `json:"score"`
	ScoreBreakdown       []ScoreFactor        `json:"score_breakdown"`
	Progress             float64              `json:"progress"`
	ImmediateUnlocks     []string             `json:"immediate_unlocks,omitempty"`
	DownstreamUnlocks    int                  `json:"downstream_unlock_count,omitempty"`
	BlockedBy            []string             `json:"blocked_by,omitempty"`
	Materials            []MaterialAssessment `json:"materials,omitempty"`
	InferredRequirements []string             `json:"inferred_requirements"`
	AvailableMaterials   []string             `json:"available_materials"`
	MissingMaterials     []string             `json:"missing_materials"`
	Evidence             []string             `json:"evidence"`
	Freshness            string               `json:"freshness"`
}

type PlanResult struct {
	GeneratedAt     string           `json:"generated_at"`
	User            string           `json:"user,omitempty"`
	Message         string           `json:"message,omitempty"`
	ActiveTierLine  string           `json:"active_tier_line,omitempty"`
	Recommendations []Recommendation `json:"recommendations"`
	ExcludedCount   int              `json:"excluded_count"`
	Freshness       string           `json:"freshness"`
	Warnings        []string         `json:"warnings,omitempty"`
}
