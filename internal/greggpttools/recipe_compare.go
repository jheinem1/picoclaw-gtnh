package greggpttools

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const defaultRecipeCompareRoutes = 1

// recipeCompareTool provides a bounded, deterministic alternative to asking the
// model to invent and recursively execute several recipe_sql queries. It keeps
// inventory lookup separate: inventory_query_items contains the exact identities
// that should be sent to inventory_totals in one call.
func recipeCompareTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("recipe_compare", GroupGTNHData, "Compare two exact production targets with bounded recipe-database queries. It resolves canonical craftable forms, selects direct base-material synthesis routes, follows only same-material finishing steps (for example dust to hot ingot to ingot), and returns exact item identities for one inventory_totals call. Prefer this over recursive recipe_sql for questions such as Energetic Alloy versus Vibrant Alloy.", timeout, object(
		required("target_a", stringSpec("First target display name, for example Energetic Alloy.")),
		required("target_b", stringSpec("Second target display name, for example Vibrant Alloy.")),
		optional("routes_per_target", intSpec("Maximum base-material synthesis routes returned per target.", 1, 5, defaultRecipeCompareRoutes)),
	), func(ctx context.Context, a Arguments) (Result, error) {
		return compareRecipeTargets(ctx, cfg.resolvedRecipeSQLPath(), timeout, cfg.MaxOutputBytes, stringArg(a, "target_a"), stringArg(a, "target_b"), intArg(a, "routes_per_target", defaultRecipeCompareRoutes))
	})
}

type compareResource struct {
	Kind         string `json:"kind"`
	Key          string `json:"resource_key"`
	Name         string `json:"name"`
	RegistryName string `json:"registry_name,omitempty"`
	Damage       *int   `json:"damage,omitempty"`
}

type compareIngredient struct {
	Position     int    `json:"position"`
	OptionIndex  int    `json:"option_index"`
	Kind         string `json:"kind"`
	ResourceKey  string `json:"resource_key"`
	Name         string `json:"name"`
	RegistryName string `json:"registry_name,omitempty"`
	Damage       *int   `json:"damage,omitempty"`
	Amount       int64  `json:"amount"`
	Consumed     bool   `json:"consumed"`
	Catalyst     bool   `json:"catalyst"`
}

type compareRoute struct {
	RecipeID      int64               `json:"recipe_id"`
	Handler       string              `json:"handler"`
	Capability    string              `json:"capability,omitempty"`
	MachineHint   string              `json:"machine_hint,omitempty"`
	VoltageTier   string              `json:"voltage_tier,omitempty"`
	DurationTicks *int64              `json:"duration_ticks,omitempty"`
	EUt           *int64              `json:"eut,omitempty"`
	OutputAmount  float64             `json:"output_amount"`
	Chance        int64               `json:"chance"`
	Ingredients   []compareIngredient `json:"ingredients"`
	nonFamily     int
}

type compareTarget struct {
	Requested           string            `json:"requested"`
	Material            string            `json:"material"`
	Resolved            compareResource   `json:"resolved"`
	SynthesisResource   compareResource   `json:"synthesis_resource"`
	SynthesisRoutes     []compareRoute    `json:"synthesis_routes"`
	FinishingChain      []compareRoute    `json:"finishing_chain"`
	InventoryQueryItems []compareResource `json:"inventory_query_items"`
}

func compareRecipeTargets(ctx context.Context, dbPath string, timeout time.Duration, maxOutputBytes int, targetA, targetB string, routeLimit int) (Result, error) {
	if strings.TrimSpace(targetA) == "" || strings.TrimSpace(targetB) == "" {
		return Result{}, fmt.Errorf("recipe_compare requires target_a and target_b")
	}
	if routeLimit < 1 {
		routeLimit = 1
	} else if routeLimit > 5 {
		routeLimit = 5
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(dbPath))
	if err != nil {
		return Result{}, fmt.Errorf("open recipe sqlite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	targets := make([]compareTarget, 0, 2)
	for _, requested := range []string{targetA, targetB} {
		target, err := buildRecipeComparisonTarget(timeoutCtx, db, strings.TrimSpace(requested), routeLimit)
		if err != nil {
			if timeoutCtx.Err() != nil {
				return Result{Name: "recipe_compare", OK: false, ExitCode: -1, TimedOut: true, Stderr: "recipe_compare timed out"}, nil
			}
			return Result{Name: "recipe_compare", OK: false, ExitCode: 1, Stderr: err.Error()}, nil
		}
		targets = append(targets, target)
	}
	payload := map[string]any{
		"targets": targets,
		"limits":  map[string]int{"routes_per_target": routeLimit, "same_material_finishing_steps": 3},
	}
	stdout, truncated, err := limitedJSON(payload, maxOutputBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: "recipe_compare", OK: true, Stdout: stdout, Truncated: truncated}, nil
}

func buildRecipeComparisonTarget(ctx context.Context, db *sql.DB, requested string, routeLimit int) (compareTarget, error) {
	resolved, err := resolveCraftableResource(ctx, db, requested)
	if err != nil {
		return compareTarget{}, fmt.Errorf("resolve %q: %w", requested, err)
	}
	material := materialStem(resolved.Name)
	synthesis := resolved
	if dust, ok, err := exactResourceByName(ctx, db, material+" Dust", true); err != nil {
		return compareTarget{}, err
	} else if ok {
		synthesis = dust
	}

	allSynthesis, err := loadCompareRoutes(ctx, db, synthesis.Key, material)
	if err != nil {
		return compareTarget{}, err
	}
	sort.SliceStable(allSynthesis, func(i, j int) bool { return compareRouteLess(allSynthesis[i], allSynthesis[j]) })
	synthesisRoutes := make([]compareRoute, 0, routeLimit)
	for _, route := range allSynthesis {
		if route.nonFamily < 2 {
			continue
		}
		synthesisRoutes = append(synthesisRoutes, route)
		if len(synthesisRoutes) == routeLimit {
			break
		}
	}
	if len(synthesisRoutes) == 0 {
		return compareTarget{}, fmt.Errorf("%q has no bounded base-material synthesis route; use recipe_sql for manual inspection", synthesis.Name)
	}

	finishing, err := findSameMaterialFinishingChain(ctx, db, synthesis, resolved, material, 3)
	if err != nil {
		return compareTarget{}, err
	}
	items := inventoryItemsForRoutes(synthesisRoutes)
	return compareTarget{
		Requested: requested, Material: material, Resolved: resolved,
		SynthesisResource: synthesis, SynthesisRoutes: synthesisRoutes,
		FinishingChain: finishing, InventoryQueryItems: items,
	}, nil
}

func resolveCraftableResource(ctx context.Context, db *sql.DB, requested string) (compareResource, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT 'item', 'item:' || i.registry_name || ':' || i.damage,
		       i.display_name, i.registry_name, i.damage,
		       (SELECT count(*) FROM recipe_edges oe JOIN recipes r ON r.id=oe.recipe_id
		        WHERE oe.direction='output'
		          AND oe.resource_key='item:' || i.registry_name || ':' || i.damage
		          AND oe.is_primary=1 AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1
		          AND NOT EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=r.id
		                          AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio
		                                          WHERE rio.input_id=ri.id))) AS route_count
		FROM items i
		WHERE lower(i.display_name)=lower(?)
		   OR lower(i.display_name)=lower(? || ' Ingot')
		ORDER BY CASE WHEN route_count>0 THEN 0 ELSE 1 END,
		         CASE WHEN lower(i.display_name)=lower(?) THEN 0 ELSE 1 END,
		         route_count DESC, i.registry_name, i.damage
		LIMIT 1`, requested, requested, requested)
	if err != nil {
		return compareResource{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return compareResource{}, fmt.Errorf("no exact item or craftable Ingot form found; call item_search and retry with an exact display name")
	}
	var r compareResource
	var damage sql.NullInt64
	var routeCount int
	if err := rows.Scan(&r.Kind, &r.Key, &r.Name, &r.RegistryName, &damage, &routeCount); err != nil {
		return compareResource{}, err
	}
	if damage.Valid {
		value := int(damage.Int64)
		r.Damage = &value
	}
	return r, rows.Err()
}

func exactResourceByName(ctx context.Context, db *sql.DB, name string, requireRoute bool) (compareResource, bool, error) {
	query := `SELECT 'item', 'item:' || i.registry_name || ':' || i.damage,
		i.display_name, i.registry_name, i.damage
		FROM items i WHERE lower(i.display_name)=lower(?)`
	if requireRoute {
		query += ` AND EXISTS (
			SELECT 1 FROM recipe_edges oe JOIN recipes r ON r.id=oe.recipe_id
			WHERE oe.direction='output'
			  AND oe.resource_key='item:' || i.registry_name || ':' || i.damage
			  AND oe.is_primary=1 AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1
			  AND NOT EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=r.id
			                  AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id))
		)`
	}
	query += ` ORDER BY i.registry_name, i.damage LIMIT 1`
	var r compareResource
	var damage sql.NullInt64
	err := db.QueryRowContext(ctx, query, name).Scan(&r.Kind, &r.Key, &r.Name, &r.RegistryName, &damage)
	if err == sql.ErrNoRows {
		return compareResource{}, false, nil
	}
	if err != nil {
		return compareResource{}, false, err
	}
	if damage.Valid {
		value := int(damage.Int64)
		r.Damage = &value
	}
	return r, true, nil
}

func loadCompareRoutes(ctx context.Context, db *sql.DB, outputKey, material string) ([]compareRoute, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.id, h.name,
		       COALESCE((SELECT mc.capability_key FROM machine_capabilities mc
		                 WHERE mc.handler_id=h.id ORDER BY mc.capability_key LIMIT 1), ''),
		       COALESCE((SELECT mc.machine_name_hint FROM machine_capabilities mc
		                 WHERE mc.handler_id=h.id ORDER BY mc.capability_key LIMIT 1), ''),
		       CASE WHEN r.eut IS NULL THEN '' WHEN abs(r.eut)<=8 THEN 'ULV'
		            WHEN abs(r.eut)<=32 THEN 'LV' WHEN abs(r.eut)<=128 THEN 'MV'
		            WHEN abs(r.eut)<=512 THEN 'HV' WHEN abs(r.eut)<=2048 THEN 'EV'
		            WHEN abs(r.eut)<=8192 THEN 'IV' WHEN abs(r.eut)<=32768 THEN 'LuV'
		            WHEN abs(r.eut)<=131072 THEN 'ZPM' WHEN abs(r.eut)<=524288 THEN 'UV'
		            ELSE 'UHV+' END,
		       r.duration_ticks, r.eut, oe.amount, oe.chance,
		       ie.position, COALESCE(ie.option_index, 0), ie.resource_kind, ie.resource_key,
		       COALESCE(NULLIF(trim(ie.resource_name), ''), ie.resource_key),
		       COALESCE(ii.registry_name, ''), ii.damage,
		       ie.amount, ie.consumed, ie.catalyst
		FROM recipe_edges oe
		JOIN recipes r ON r.id=oe.recipe_id
		JOIN recipe_handlers h ON h.id=r.handler_id
		JOIN recipe_edges ie ON ie.recipe_id=r.id AND ie.direction='input'
		LEFT JOIN items ii ON ii.id=ie.item_id
		WHERE oe.direction='output' AND oe.resource_key=? AND oe.is_primary=1
		  AND r.valid=1 AND r.hidden=0 AND r.fake=0 AND r.enabled=1
		  AND ie.resource_key NOT IN ('item:', 'fluid:', 'oredict:')
		  AND NOT EXISTS (SELECT 1 FROM recipe_inputs ri WHERE ri.recipe_id=r.id
		                  AND NOT EXISTS (SELECT 1 FROM recipe_input_options rio WHERE rio.input_id=ri.id))
		ORDER BY r.id, ie.position, ie.option_index`, outputKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routeByID := map[int64]*compareRoute{}
	order := make([]int64, 0)
	positionNonFamily := map[int64]map[int]bool{}
	for rows.Next() {
		var routeID int64
		var handler, capability, machine, tier string
		var duration, eut sql.NullInt64
		var outputAmount float64
		var chance int64
		var input compareIngredient
		var registry string
		var damage sql.NullInt64
		var consumed, catalyst int
		if err := rows.Scan(&routeID, &handler, &capability, &machine, &tier, &duration, &eut, &outputAmount, &chance,
			&input.Position, &input.OptionIndex, &input.Kind, &input.ResourceKey, &input.Name, &registry, &damage,
			&input.Amount, &consumed, &catalyst); err != nil {
			return nil, err
		}
		route := routeByID[routeID]
		if route == nil {
			route = &compareRoute{RecipeID: routeID, Handler: handler, Capability: capability, MachineHint: machine, VoltageTier: tier, OutputAmount: outputAmount, Chance: chance, Ingredients: []compareIngredient{}}
			if duration.Valid {
				value := duration.Int64
				route.DurationTicks = &value
			}
			if eut.Valid {
				value := eut.Int64
				route.EUt = &value
			}
			routeByID[routeID] = route
			positionNonFamily[routeID] = map[int]bool{}
			order = append(order, routeID)
		}
		input.RegistryName = registry
		if damage.Valid {
			value := int(damage.Int64)
			input.Damage = &value
		}
		input.Consumed, input.Catalyst = consumed != 0, catalyst != 0
		route.Ingredients = append(route.Ingredients, input)
		if input.Amount > 0 && input.Consumed && !input.Catalyst && !sameMaterial(input.Name, material) {
			positionNonFamily[routeID][input.Position] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]compareRoute, 0, len(order))
	for _, id := range order {
		route := routeByID[id]
		route.nonFamily = len(positionNonFamily[id])
		result = append(result, *route)
	}
	return result, nil
}

func findSameMaterialFinishingChain(ctx context.Context, db *sql.DB, from, target compareResource, material string, maxSteps int) ([]compareRoute, error) {
	if from.Key == target.Key {
		return []compareRoute{}, nil
	}
	type state struct {
		resource compareResource
		chain    []compareRoute
	}
	queue := []state{{resource: target}}
	seen := map[string]bool{target.Key: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if len(current.chain) >= maxSteps {
			continue
		}
		routes, err := loadCompareRoutes(ctx, db, current.resource.Key, material)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(routes, func(i, j int) bool { return finishingRouteLess(routes[i], routes[j], material) })
		for _, route := range routes {
			for _, input := range route.Ingredients {
				if !input.Consumed || input.Catalyst || !sameMaterial(input.Name, material) {
					continue
				}
				chain := append(append([]compareRoute{}, current.chain...), route)
				if input.ResourceKey == from.Key {
					for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
						chain[i], chain[j] = chain[j], chain[i]
					}
					return chain, nil
				}
				if seen[input.ResourceKey] {
					continue
				}
				seen[input.ResourceKey] = true
				queue = append(queue, state{resource: compareResource{Kind: input.Kind, Key: input.ResourceKey, Name: input.Name, RegistryName: input.RegistryName, Damage: input.Damage}, chain: chain})
			}
		}
	}
	return nil, fmt.Errorf("no same-material finishing chain from %q to %q within %d steps", from.Name, target.Name, maxSteps)
}

func compareRouteLess(a, b compareRoute) bool {
	aMixer, bMixer := strings.Contains(strings.ToLower(a.Handler), "mixer"), strings.Contains(strings.ToLower(b.Handler), "mixer")
	if aMixer != bMixer {
		return aMixer
	}
	if tierRank(a.VoltageTier) != tierRank(b.VoltageTier) {
		return tierRank(a.VoltageTier) < tierRank(b.VoltageTier)
	}
	if nullableValue(a.EUt) != nullableValue(b.EUt) {
		return nullableValue(a.EUt) < nullableValue(b.EUt)
	}
	if nullableValue(a.DurationTicks) != nullableValue(b.DurationTicks) {
		return nullableValue(a.DurationTicks) < nullableValue(b.DurationTicks)
	}
	return a.RecipeID < b.RecipeID
}

func finishingRouteLess(a, b compareRoute, material string) bool {
	preferred := func(r compareRoute) int {
		for _, input := range r.Ingredients {
			name := strings.ToLower(input.Name)
			if sameMaterial(input.Name, material) && (strings.HasSuffix(name, " dust") || strings.HasPrefix(name, "hot ")) {
				return 0
			}
		}
		return 1
	}
	if preferred(a) != preferred(b) {
		return preferred(a) < preferred(b)
	}
	if nullableValue(a.DurationTicks) != nullableValue(b.DurationTicks) {
		return nullableValue(a.DurationTicks) < nullableValue(b.DurationTicks)
	}
	return a.RecipeID < b.RecipeID
}

func materialStem(name string) string {
	stem := strings.TrimSpace(name)
	if strings.HasPrefix(strings.ToLower(stem), "hot ") {
		stem = strings.TrimSpace(stem[4:])
	}
	for _, suffix := range []string{" Ingot", " Dust", " Nugget", " Plate", " Block"} {
		if strings.HasSuffix(strings.ToLower(stem), strings.ToLower(suffix)) {
			return strings.TrimSpace(stem[:len(stem)-len(suffix)])
		}
	}
	return stem
}

func sameMaterial(name, material string) bool {
	return strings.Contains(strings.ToLower(name), strings.ToLower(material))
}

func tierRank(tier string) int {
	for i, value := range []string{"ULV", "LV", "MV", "HV", "EV", "IV", "LuV", "ZPM", "UV", "UHV+"} {
		if strings.EqualFold(tier, value) {
			return i
		}
	}
	return 99
}

func nullableValue(value *int64) int64 {
	if value == nil {
		return 1 << 62
	}
	return *value
}

func inventoryItemsForRoutes(routes []compareRoute) []compareResource {
	byKey := map[string]compareResource{}
	for _, route := range routes {
		for _, input := range route.Ingredients {
			if input.Kind != "item" || input.Amount <= 0 || !input.Consumed || input.Catalyst || input.RegistryName == "" {
				continue
			}
			byKey[input.ResourceKey] = compareResource{Kind: input.Kind, Key: input.ResourceKey, Name: input.Name, RegistryName: input.RegistryName, Damage: input.Damage}
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]compareResource, 0, len(keys))
	for _, key := range keys {
		items = append(items, byKey[key])
	}
	return items
}
