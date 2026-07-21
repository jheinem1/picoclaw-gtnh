package questplanner

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var voltagePattern = regexp.MustCompile(`(?i)\b(steam|lv|mv|hv|ev|iv|luv|zpm|uv|uhv|uev|uiv|umv)\b`)
var tierPattern = regexp.MustCompile(`(?i)\btier\s*([0-9]+(?:\.[0-9]+)?)\b`)

func (p *Planner) Recommend(user, message string) Recommendation {
	plan := p.Plan(user, message, 1)
	if len(plan.Recommendations) > 0 {
		return plan.Recommendations[0]
	}
	return Recommendation{
		Recommendation:       "No eligible quest or task is currently supported by the indexes",
		Source:               "none",
		State:                "unavailable",
		Eligible:             false,
		WhyEasy:              "Every candidate is completed, locked, assigned elsewhere, or outside the requested tier.",
		NextStep:             "Refresh quest and inventory data, then inspect blocked quests.",
		Confidence:           "low",
		InferredRequirements: []string{},
		AvailableMaterials:   []string{},
		MissingMaterials:     []string{"eligible quest or task"},
		Evidence:             []string{"eligible_candidates=0"},
		Freshness:            p.freshness(),
	}
}

func (p *Planner) Plan(user, message string, limit int) PlanResult {
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	activeTier := p.activeTierLine()
	tierHint := requestedTier(message)
	questByID := p.questMap()
	candidates := make([]Recommendation, 0, len(p.quests.Quests)+len(p.tasks))
	excluded := 0
	for _, quest := range p.quests.Quests {
		candidate := p.evaluateQuest(quest, questByID, activeTier, tierHint, user)
		if candidate.State == "completed_unclaimed" && !requestsRewardClaim(message) {
			excluded++
			continue
		}
		if candidate.Eligible {
			candidates = append(candidates, candidate)
		} else {
			excluded++
		}
	}
	if tierHint == "" {
		for _, task := range p.tasks {
			candidate := p.evaluateTask(task, user)
			if candidate.Eligible {
				candidates = append(candidates, candidate)
			} else {
				excluded++
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].Source != candidates[j].Source {
			return candidates[i].Source < candidates[j].Source
		}
		if candidates[i].QuestID != candidates[j].QuestID {
			return idLess(candidates[i].QuestID, candidates[j].QuestID)
		}
		if candidates[i].TaskID != candidates[j].TaskID {
			return candidates[i].TaskID < candidates[j].TaskID
		}
		return strings.ToLower(candidates[i].Recommendation) < strings.ToLower(candidates[j].Recommendation)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return PlanResult{
		GeneratedAt:     p.now().UTC().Format(time.RFC3339),
		User:            strings.TrimSpace(user),
		Message:         strings.TrimSpace(message),
		ActiveTierLine:  activeTier,
		Recommendations: candidates,
		ExcludedCount:   excluded,
		Freshness:       p.freshness(),
		Warnings:        p.allWarnings(),
	}
}

func (p *Planner) ExplainQuest(id, user, message string) (Recommendation, error) {
	questByID := p.questMap()
	quest, ok := questByID[strings.TrimSpace(id)]
	if !ok {
		return Recommendation{}, fmt.Errorf("quest %q not found", id)
	}
	return p.evaluateQuest(quest, questByID, p.activeTierLine(), requestedTier(message), user), nil
}

func (p *Planner) evaluateQuest(quest Quest, questByID map[string]Quest, activeTier, tierHint, user string) Recommendation {
	state, blockedBy := effectiveState(quest, questByID)
	claimable := questClaimableFor(quest, user)
	eligible := true
	exclusions := []string{}
	if quest.Completed {
		if claimable {
			state = "completed_unclaimed"
		} else {
			eligible = false
			if state == "completed_unclaimed" && strings.TrimSpace(user) == "" {
				exclusions = append(exclusions, "a player name is required to determine who can claim the reward")
			} else {
				exclusions = append(exclusions, "quest is already complete for the selected party")
			}
		}
	} else if len(blockedBy) > 0 {
		eligible = false
		exclusions = append(exclusions, "unmet prerequisites: "+strings.Join(blockedBy, ", "))
	}
	if tierHint != "" && !strings.Contains(strings.ToLower(quest.QuestLine), tierHint) {
		eligible = false
		exclusions = append(exclusions, "quest line does not match requested tier "+tierHint)
	}

	materials := p.assessMaterials(quest, user)
	downstream := downstreamCount(quest.ID, questByID)
	actionUnlocks := append([]string(nil), quest.Unlocks...)
	actionDownstream := downstream
	if quest.Completed {
		// BetterQuesting prerequisite edges are satisfied by quest completion,
		// not by collecting the per-player reward afterward.
		actionUnlocks = nil
		actionDownstream = 0
	}
	factors := []ScoreFactor{}
	score := 0
	add := func(factor string, points int, detail string) {
		score += points
		factors = append(factors, ScoreFactor{Factor: factor, Points: points, Detail: detail})
	}
	switch state {
	case "completed_unclaimed":
		add("claimable_reward", 1000, "reward can be claimed by the requesting player")
	case "completed_claimed", "completed_claim_unknown":
		add("completed", 0, "quest is complete and is not an eligible work candidate")
	case "in_progress":
		add("continuity", 500, "quest has recorded task progress")
	default:
		add("ready", 300, "all indexed prerequisites are complete")
	}
	if quest.TierQuestLine {
		add("main_tier", 100, "quest is in a main tier quest line")
	}
	if activeTier != "" && strings.EqualFold(quest.QuestLine, activeTier) {
		add("active_tier", 120, "quest is in the highest active tier line")
	}
	if tierHint != "" && strings.Contains(strings.ToLower(quest.QuestLine), tierHint) {
		add("requested_tier", 200, "quest matches the requested tier")
	}
	if quest.CompletionRatio > 0 && !quest.Completed {
		add("recorded_progress", int(math.Round(quest.CompletionRatio*100)), fmt.Sprintf("%.0f%% of indexed tasks complete", quest.CompletionRatio*100))
	}
	if len(materials) > 0 && state != "completed_unclaimed" {
		coverage, fullyAvailable, unresolved := materialCoverage(materials)
		add("material_coverage", int(math.Round(coverage*120)), fmt.Sprintf("%.0f%% of exact item requirements indexed", coverage*100))
		if fullyAvailable && unresolved == 0 {
			add("materials_ready", 40, "all exact item requirements are currently indexed")
		}
		add("requirement_complexity", -minInt(64, len(materials)*8), fmt.Sprintf("%d distinct item requirements", len(materials)))
		missingKinds := 0
		for _, material := range materials {
			if material.Missing > 0 || !material.Resolved {
				missingKinds++
			}
		}
		if missingKinds > 0 {
			add("material_shortages", -minInt(60, missingKinds*12), fmt.Sprintf("%d unresolved or short item requirements", missingKinds))
		}
	}
	if len(actionUnlocks) > 0 {
		add("immediate_unlocks", minInt(90, len(actionUnlocks)*15), fmt.Sprintf("unlocks %d immediate quests", len(actionUnlocks)))
	}
	if actionDownstream > len(actionUnlocks) {
		add("downstream_unlocks", minInt(60, (actionDownstream-len(actionUnlocks))*4), fmt.Sprintf("opens a path to %d downstream quests", actionDownstream))
	}
	if questDataStale(p) {
		add("stale_quest_data", -100, "quest index is stale or has unknown freshness")
	}
	inventoryEvidenceStale := len(materials) > 0 && inventoryDataStale(p)
	if inventoryEvidenceStale {
		add("stale_inventory", -80, "inventory evidence is stale or incomplete")
	}
	if !eligible {
		add("ineligible", -10000, strings.Join(exclusions, "; "))
	}

	inferred, available, missing := summarizeMaterials(materials)
	confidence := p.questConfidence(materials)
	if !eligible || state == "locked" {
		confidence = "high"
	}
	why := questWhy(state, quest, materials, downstream)
	next := questNextStep(state, quest, materials, user)
	if inventoryEvidenceStale {
		why = "The questbook path is eligible, but material feasibility is based on stale or incomplete inventory evidence. " + why
		next = "Refresh inventory data first; if the indexed counts remain valid, " + lowerFirst(next)
	}
	evidence := []string{
		"quest_id=" + quest.ID,
		"quest_state=" + state,
		fmt.Sprintf("prerequisites_complete=%t", len(blockedBy) == 0),
		fmt.Sprintf("immediate_unlocks=%d", len(actionUnlocks)),
		fmt.Sprintf("downstream_unlocks=%d", actionDownstream),
		fmt.Sprintf("inventory_fresh=%t", !inventoryEvidenceStale),
	}
	if quest.Completed {
		evidence = append(evidence, "reward_claim_unlocks_quests=false")
	}
	for _, material := range materials {
		evidence = append(evidence, fmt.Sprintf("inventory[%s]=%d/%d resolved=%t", material.Identity, material.Available, material.Required, material.Resolved))
	}
	return Recommendation{
		Recommendation:       valueOr(quest.Title, "Quest "+quest.ID),
		Source:               "questbook",
		QuestID:              quest.ID,
		QuestLine:            quest.QuestLine,
		State:                state,
		Eligible:             eligible,
		ExclusionReasons:     exclusions,
		WhyEasy:              why,
		NextStep:             next,
		Confidence:           confidence,
		Score:                score,
		ScoreBreakdown:       factors,
		Progress:             quest.CompletionRatio,
		ImmediateUnlocks:     actionUnlocks,
		DownstreamUnlocks:    actionDownstream,
		BlockedBy:            blockedBy,
		Materials:            materials,
		InferredRequirements: inferred,
		AvailableMaterials:   available,
		MissingMaterials:     missing,
		Evidence:             evidence,
		Freshness:            p.freshness(),
	}
}

func requestsRewardClaim(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "claim") || strings.Contains(message, "reward")
}

func (p *Planner) evaluateTask(task TaskRow, user string) Recommendation {
	status := strings.ToLower(valueOr(task.KanbanState, task.Status))
	eligible := status != "done" && status != "paused"
	exclusions := []string{}
	if !eligible {
		exclusions = append(exclusions, "task is "+valueOr(status, "not open"))
	}
	if eligible && strings.TrimSpace(user) != "" && strings.TrimSpace(task.Owner) != "" && !nameInList(user, strings.Split(task.Owner, ",")) {
		eligible = false
		exclusions = append(exclusions, "task is assigned to "+task.Owner)
	}
	factors := []ScoreFactor{}
	score := 0
	add := func(factor string, points int, detail string) {
		score += points
		factors = append(factors, ScoreFactor{Factor: factor, Points: points, Detail: detail})
	}
	if status == "doing" {
		add("continuity", 480, "task is already in progress")
	} else {
		add("open_task", 180, "task is open")
	}
	switch strings.ToLower(task.Priority) {
	case "high":
		add("priority", 80, "task priority is high")
	case "med", "medium":
		add("priority", 35, "task priority is medium")
	}
	if strings.TrimSpace(user) != "" && nameInList(user, strings.Split(task.Owner, ",")) {
		add("assigned_owner", 30, "task is assigned to the requesting player")
	}
	if !eligible {
		add("ineligible", -10000, strings.Join(exclusions, "; "))
	}
	description := strings.TrimSpace(task.Description)
	next := "Work on task: " + valueOr(task.Title, "Task "+strconv.Itoa(task.ID)) + "."
	if description != "" {
		next = description
	}
	return Recommendation{
		Recommendation:       valueOr(task.Title, "Task "+strconv.Itoa(task.ID)),
		Source:               "task_log",
		TaskID:               task.ID,
		State:                valueOr(status, "todo"),
		Eligible:             eligible,
		ExclusionReasons:     exclusions,
		WhyEasy:              "This is a deterministic task-board candidate based on status, priority, and ownership.",
		NextStep:             next,
		Confidence:           "medium",
		Score:                score,
		ScoreBreakdown:       factors,
		InferredRequirements: []string{},
		AvailableMaterials:   []string{},
		MissingMaterials:     []string{},
		Evidence: []string{
			fmt.Sprintf("task_id=%d", task.ID),
			"task_status=" + valueOr(status, "todo"),
			"task_priority=" + valueOr(task.Priority, "unspecified"),
			"task_owner=" + valueOr(task.Owner, "unassigned"),
		},
		Freshness: p.freshness(),
	}
}

func (p *Planner) assessMaterials(quest Quest, user string) []MaterialAssessment {
	byIdentity := map[string]*MaterialAssessment{}
	order := []string{}
	for _, task := range quest.Tasks {
		if taskCompletedFor(task, user) {
			continue
		}
		for _, item := range task.RequiredItems {
			keys, identity, name, resolved, resolution := p.resolveQuestItem(item)
			if strings.TrimSpace(identity) == "" {
				identity = fmt.Sprintf("unresolved:%s:%d:%d", normalizeIdentity(name), item.ID, item.Damage)
			}
			assessment := byIdentity[identity]
			if assessment == nil {
				assessment = &MaterialAssessment{TaskID: task.ID, Identity: identity, Name: name, Resolved: resolved, Resolution: resolution}
				byIdentity[identity] = assessment
				order = append(order, identity)
			}
			required := item.Count
			if required < 1 {
				required = 1
			}
			assessment.Required += required
			if resolved {
				assessment.Available = 0
				for _, key := range keys {
					assessment.Available += p.inventoryCount(key)
				}
			}
		}
	}
	out := make([]MaterialAssessment, 0, len(order))
	for _, identity := range order {
		assessment := *byIdentity[identity]
		assessment.Missing = maxInt(0, assessment.Required-assessment.Available)
		if assessment.Required > 0 && assessment.Resolved {
			assessment.Coverage = math.Min(1, float64(assessment.Available)/float64(assessment.Required))
		}
		out = append(out, assessment)
	}
	return out
}

func (p *Planner) resolveQuestItem(item QuestItem) ([]string, string, string, bool, string) {
	name := valueOr(item.DisplayName, item.RegName)
	if item.ID != 0 {
		key := itemKey(item.ID, item.Damage)
		if meta, ok := p.itemsByKey[key]; ok {
			name = valueOr(item.DisplayName, valueOr(meta.DisplayName, meta.RegName))
		}
		return []string{key}, key, valueOr(name, key), true, "numeric_id_damage"
	}
	if strings.TrimSpace(item.RegName) != "" {
		keys := p.filterDamage(p.keysByRegistry[normalizeIdentity(item.RegName)], item.Damage)
		if len(keys) > 0 {
			return keys, strings.ToLower(item.RegName) + ":" + strconv.Itoa(item.Damage), valueOr(name, item.RegName), true, "registry_name_damage"
		}
		return nil, strings.ToLower(item.RegName) + ":" + strconv.Itoa(item.Damage), valueOr(name, item.RegName), false, "registry_name_not_resolved"
	}
	if strings.TrimSpace(item.DisplayName) != "" {
		keys := p.filterDamage(p.keysByDisplay[normalizeIdentity(item.DisplayName)], item.Damage)
		if len(keys) == 1 {
			return keys, keys[0], item.DisplayName, true, "unique_display_name_damage"
		}
		resolution := "display_name_not_resolved"
		if len(keys) > 1 {
			resolution = "ambiguous_display_name_damage"
		}
		return nil, "display:" + normalizeIdentity(item.DisplayName) + ":" + strconv.Itoa(item.Damage), item.DisplayName, false, resolution
	}
	return nil, "", "unknown item", false, "missing_item_identity"
}

func (p *Planner) filterDamage(keys []string, damage int) []string {
	out := []string{}
	for _, key := range keys {
		meta, ok := p.itemsByKey[key]
		if ok && meta.Damage == damage {
			out = append(out, key)
		}
	}
	return sortedUnique(out)
}

func effectiveState(quest Quest, questByID map[string]Quest) (string, []string) {
	blocked := []string{}
	for _, prerequisiteID := range quest.Prerequisites {
		prerequisite, ok := questByID[prerequisiteID]
		if !ok || !prerequisite.Completed {
			blocked = append(blocked, prerequisiteID)
		}
	}
	blocked = sortedUnique(blocked)
	if quest.Completed {
		if quest.State == "completed_unclaimed" || quest.State == "completed_claim_unknown" {
			return quest.State, blocked
		}
		return "completed_claimed", blocked
	}
	if len(blocked) > 0 {
		return "locked", blocked
	}
	if quest.State == "in_progress" || quest.CompletionRatio > 0 {
		return "in_progress", blocked
	}
	return "ready", blocked
}

func questClaimableFor(quest Quest, user string) bool {
	if strings.TrimSpace(user) == "" {
		return false
	}
	if nameInList(user, quest.ClaimableBy) {
		return true
	}
	for _, progress := range quest.PlayerProgress {
		if (strings.EqualFold(progress.Name, user) || strings.EqualFold(progress.UUID, user)) && progress.Completed && progress.ClaimStatusKnown && !progress.Claimed {
			return true
		}
	}
	return false
}

func taskCompletedFor(task QuestTask, user string) bool {
	if strings.TrimSpace(user) == "" {
		return len(task.CompletedBy) > 0
	}
	return nameInList(user, task.CompletedBy)
}

func materialCoverage(materials []MaterialAssessment) (float64, bool, int) {
	required := 0
	covered := 0
	fullyAvailable := true
	unresolved := 0
	for _, material := range materials {
		required += material.Required
		if !material.Resolved {
			unresolved++
			fullyAvailable = false
			continue
		}
		covered += minInt(material.Required, material.Available)
		if material.Available < material.Required {
			fullyAvailable = false
		}
	}
	if required == 0 {
		return 0, false, unresolved
	}
	return float64(covered) / float64(required), fullyAvailable, unresolved
}

func summarizeMaterials(materials []MaterialAssessment) ([]string, []string, []string) {
	inferred := []string{}
	available := []string{}
	missing := []string{}
	for _, material := range materials {
		inferred = append(inferred, fmt.Sprintf("%s x%d", material.Name, material.Required))
		if !material.Resolved {
			missing = append(missing, fmt.Sprintf("%s: identity unresolved (%s)", material.Name, material.Resolution))
			continue
		}
		if material.Available > 0 {
			available = append(available, fmt.Sprintf("%s x%d indexed", material.Name, material.Available))
		}
		if material.Missing > 0 {
			missing = append(missing, fmt.Sprintf("%s x%d", material.Name, material.Missing))
		}
	}
	return inferred, available, missing
}

func questWhy(state string, quest Quest, materials []MaterialAssessment, downstream int) string {
	switch state {
	case "completed_unclaimed":
		return "The quest is complete and has an indexed unclaimed reward for this player."
	case "completed_claimed":
		return "The quest is complete and its indexed reward claim is complete."
	case "completed_claim_unknown":
		return "The quest is complete, but the reward-claim state was not present in the indexed progress data."
	case "in_progress":
		return fmt.Sprintf("The quest is already %.0f%% complete, so finishing it preserves momentum.", quest.CompletionRatio*100)
	}
	if len(materials) > 0 {
		coverage, fullyAvailable, unresolved := materialCoverage(materials)
		if fullyAvailable && unresolved == 0 {
			return "All exact item requirements are present in the indexed party inventory."
		}
		if coverage > 0 {
			return fmt.Sprintf("The quest is ready and %.0f%% of its exact item requirements are indexed.", coverage*100)
		}
	}
	if downstream > 0 {
		return fmt.Sprintf("The quest is ready and advances a path containing %d downstream quests.", downstream)
	}
	return "The quest is ready and all indexed prerequisites are complete."
}

func questNextStep(state string, quest Quest, materials []MaterialAssessment, user string) string {
	title := valueOr(quest.Title, "Quest "+quest.ID)
	if state == "completed_unclaimed" {
		return fmt.Sprintf("Claim the reward for %s%s.", title, playerSuffix(user))
	}
	for _, material := range materials {
		if !material.Resolved {
			return fmt.Sprintf("Resolve the exact item identity for %s in quest %s before gathering materials.", material.Name, quest.ID)
		}
		if material.Missing > 0 {
			return fmt.Sprintf("Acquire %d more %s for %s.", material.Missing, material.Name, title)
		}
	}
	if len(materials) > 0 {
		return fmt.Sprintf("Submit the indexed materials for %s.", title)
	}
	for _, task := range quest.Tasks {
		if !taskCompletedFor(task, user) && strings.TrimSpace(task.Description) != "" {
			return task.Description
		}
	}
	return fmt.Sprintf("Open quest %s and perform its next incomplete task: %s.", quest.ID, title)
}

func (p *Planner) questConfidence(materials []MaterialAssessment) string {
	if p.quests.Version < 2 || questDataStale(p) {
		return "low"
	}
	if len(materials) == 0 {
		return "medium"
	}
	if inventoryDataStale(p) {
		return "low"
	}
	for _, material := range materials {
		if !material.Resolved {
			return "low"
		}
	}
	return "high"
}

func questDataStale(p *Planner) bool {
	if p.questStatus.Stale["quests"] {
		return true
	}
	return timestampStale(p.now(), p.quests.Source.QuestsScanAt, 2*time.Hour)
}

func inventoryDataStale(p *Planner) bool {
	for _, key := range []string{"players", "chests", "containers", "block_inventories", "me"} {
		if p.inventoryStatus.Stale[key] {
			return true
		}
	}
	return timestampStale(p.now(), p.inventory.Source.PlayersScanAt, 2*time.Hour) ||
		timestampStale(p.now(), p.inventory.Source.ChestsScanAt, 2*time.Hour) ||
		timestampStale(p.now(), p.inventory.Source.MEScanAt, 2*time.Hour)
}

func lowerFirst(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	runes := []rune(value)
	return strings.ToLower(string(runes[0])) + string(runes[1:])
}

func timestampStale(now time.Time, value string, maxAge time.Duration) bool {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	return err != nil || now.Sub(parsed) > maxAge
}

func (p *Planner) activeTierLine() string {
	var active *QuestLine
	for i := range p.quests.QuestLines {
		line := &p.quests.QuestLines[i]
		if !line.Tier || line.CompletedCount <= 0 || line.OpenCount <= 0 {
			continue
		}
		if active == nil || line.Order > active.Order || line.Order == active.Order && line.Name < active.Name {
			active = line
		}
	}
	if active == nil {
		return ""
	}
	return active.Name
}

func requestedTier(message string) string {
	if match := tierPattern.FindStringSubmatch(message); len(match) == 2 {
		return "tier " + strings.ToLower(match[1])
	}
	if match := voltagePattern.FindStringSubmatch(message); len(match) == 2 {
		return strings.ToLower(match[1])
	}
	return ""
}

func (p *Planner) questMap() map[string]Quest {
	out := make(map[string]Quest, len(p.quests.Quests))
	for _, quest := range p.quests.Quests {
		out[quest.ID] = quest
	}
	return out
}

func downstreamCount(id string, quests map[string]Quest) int {
	seen := map[string]bool{id: true}
	var visit func(string)
	visit = func(current string) {
		quest, ok := quests[current]
		if !ok {
			return
		}
		for _, next := range quest.Unlocks {
			if seen[next] {
				continue
			}
			seen[next] = true
			visit(next)
		}
	}
	visit(id)
	return len(seen) - 1
}

func (p *Planner) allWarnings() []string {
	warnings := append([]string(nil), p.warnings...)
	for key, value := range p.questStatus.Errors {
		warnings = append(warnings, "quest status "+key+": "+value)
	}
	for key, value := range p.inventoryStatus.Errors {
		warnings = append(warnings, "inventory status "+key+": "+value)
	}
	return sortedUnique(warnings)
}

func nameInList(name string, values []string) bool {
	name = strings.TrimSpace(name)
	for _, value := range values {
		if strings.EqualFold(name, strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func playerSuffix(user string) string {
	if strings.TrimSpace(user) == "" {
		return ""
	}
	return " as " + strings.TrimSpace(user)
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func idLess(a, b string) bool {
	ai, aErr := strconv.ParseInt(a, 10, 64)
	bi, bErr := strconv.ParseInt(b, 10, 64)
	if aErr == nil && bErr == nil {
		return ai < bi
	}
	return a < b
}
