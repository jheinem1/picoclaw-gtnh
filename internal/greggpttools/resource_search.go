package greggpttools

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const defaultResourceSearchLimit = 10

// resourceSearchTool resolves both item and fluid identities from the recipe
// database without exposing its internal row IDs. Its resource_key values are
// the stable keys consumed by recipe_routes and recipe_ingredients.
func resourceSearchTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("resource_search", GroupGTNHData, "Safely resolve an item or fluid name to exact resource_catalog identities and stable recipe resource keys. Exact display-name matches rank first. Results include primary and total production-route counts; use a returned resource_key with recipe_routes instead of guessing item or fluid schema fields.", timeout, object(
		required("query", stringSpec("Item display name, registry name, fluid name, or localized fluid name.")),
		optional("limit", intSpec("Maximum combined item and fluid candidates to return.", 1, 30, defaultResourceSearchLimit)),
	), func(ctx context.Context, a Arguments) (Result, error) {
		return searchRecipeResources(ctx, cfg.resolvedRecipeSQLPath(), cfg.resolvedItemAliasesPath(), timeout, cfg.MaxOutputBytes, stringArg(a, "query"), intArg(a, "limit", defaultResourceSearchLimit))
	})
}

type resourceSearchCandidate struct {
	Kind                 string `json:"resource_kind"`
	Key                  string `json:"resource_key"`
	Name                 string `json:"resource_name"`
	RegistryName         string `json:"registry_name,omitempty"`
	Damage               *int   `json:"damage,omitempty"`
	FluidName            string `json:"fluid_name,omitempty"`
	PrimaryRouteCount    int    `json:"primary_route_count"`
	ProductionRouteCount int    `json:"production_route_count"`
	exactName            bool
	retrievalOrder       int
}

func searchRecipeResources(ctx context.Context, dbPath, aliasPath string, timeout time.Duration, maxOutputBytes int, query string, limit int) (Result, error) {
	normalizedQuery := normalizeReferenceText(query)
	terms := referenceTerms(normalizedQuery)
	if len(terms) == 0 {
		terms = strings.Fields(normalizedQuery)
	}
	if len(terms) == 0 {
		return Result{}, fmt.Errorf("resource_search query is required")
	}
	if limit <= 0 || limit > 30 {
		limit = defaultResourceSearchLimit
	}

	ftsTerms := make([]string, 0, len(terms))
	for _, term := range terms {
		ftsTerms = append(ftsTerms, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	ftsQuery := strings.Join(ftsTerms, " AND ")

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(dbPath))
	if err != nil {
		return Result{}, fmt.Errorf("open recipe sqlite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	candidateLimit := limit * 3
	aliasRows, err := searchItemAliases(timeoutCtx, db, aliasPath, query, candidateLimit)
	if err != nil {
		return resourceSearchFailure(timeoutCtx, "query item aliases", err), nil
	}
	aliasItems := make([]resourceSearchCandidate, 0, len(aliasRows))
	for index, row := range aliasRows {
		damage := row.Damage
		aliasItems = append(aliasItems, resourceSearchCandidate{
			Kind: "item", Key: row.ResourceKey, Name: row.DisplayName,
			RegistryName: row.RegistryName, Damage: &damage,
			exactName: row.Score == 1000, retrievalOrder: index,
		})
	}
	items, err := searchItemResources(timeoutCtx, db, ftsQuery, normalizedQuery, candidateLimit)
	if err != nil {
		return resourceSearchFailure(timeoutCtx, "query item resource index", err), nil
	}
	fluids, err := searchFluidResources(timeoutCtx, db, terms, normalizedQuery, candidateLimit)
	if err != nil {
		return resourceSearchFailure(timeoutCtx, "query fluid resources", err), nil
	}
	seen := map[string]bool{}
	candidates := make([]resourceSearchCandidate, 0, len(aliasItems)+len(items)+len(fluids))
	for _, group := range [][]resourceSearchCandidate{aliasItems, items, fluids} {
		for _, candidate := range group {
			if seen[candidate.Key] {
				continue
			}
			seen[candidate.Key] = true
			candidates = append(candidates, candidate)
		}
	}
	if err := loadResourceRouteCounts(timeoutCtx, db, candidates); err != nil {
		return resourceSearchFailure(timeoutCtx, "query resource route counts", err), nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].exactName != candidates[j].exactName {
			return candidates[i].exactName
		}
		if candidates[i].retrievalOrder != candidates[j].retrievalOrder {
			return candidates[i].retrievalOrder < candidates[j].retrievalOrder
		}
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind < candidates[j].Kind
		}
		return candidates[i].Key < candidates[j].Key
	})
	rowLimitReached := len(candidates) > limit
	if rowLimitReached {
		candidates = candidates[:limit]
	}

	rows := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		row := map[string]any{
			"resource_kind":          candidate.Kind,
			"resource_key":           candidate.Key,
			"resource_name":          candidate.Name,
			"primary_route_count":    candidate.PrimaryRouteCount,
			"production_route_count": candidate.ProductionRouteCount,
		}
		if candidate.RegistryName != "" {
			row["registry_name"] = candidate.RegistryName
		}
		if candidate.Damage != nil {
			row["damage"] = *candidate.Damage
		}
		if candidate.FluidName != "" {
			row["fluid_name"] = candidate.FluidName
		}
		rows = append(rows, row)
	}
	payload := map[string]any{
		"query":     query,
		"fts_query": ftsQuery,
		"rows":      rows,
		"count":     len(rows),
		"truncated": map[string]bool{"output": false, "rows": rowLimitReached},
	}
	stdout, truncated, err := limitedJSON(payload, maxOutputBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: "resource_search", OK: true, Stdout: stdout, Truncated: truncated}, nil
}

func searchItemResources(ctx context.Context, db *sql.DB, ftsQuery, normalizedQuery string, limit int) ([]resourceSearchCandidate, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT rc.resource_key, COALESCE(rc.resource_name, rc.resource_key), COALESCE(rc.registry_name, ''), rc.damage
		FROM item_search s
		JOIN resource_catalog rc ON rc.resource_kind='item' AND rc.resource_id=s.rowid
		WHERE item_search MATCH ?
		ORDER BY CASE WHEN lower(trim(COALESCE(rc.resource_name, rc.resource_key)))=lower(trim(?)) THEN 0 ELSE 1 END,
		         bm25(item_search), lower(COALESCE(rc.resource_name, rc.resource_key)), rc.resource_key
		LIMIT ?`, ftsQuery, normalizedQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]resourceSearchCandidate, 0, limit)
	for rows.Next() {
		var candidate resourceSearchCandidate
		var damage sql.NullInt64
		if err := rows.Scan(&candidate.Key, &candidate.Name, &candidate.RegistryName, &damage); err != nil {
			return nil, err
		}
		candidate.Kind = "item"
		candidate.exactName = normalizeReferenceText(candidate.Name) == normalizedQuery
		candidate.retrievalOrder = len(result)
		if damage.Valid {
			value := int(damage.Int64)
			candidate.Damage = &value
		}
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func searchFluidResources(ctx context.Context, db *sql.DB, terms []string, normalizedQuery string, limit int) ([]resourceSearchCandidate, error) {
	predicates := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms)+2)
	for _, term := range terms {
		predicates = append(predicates, "lower(COALESCE(rc.resource_name, '') || ' ' || COALESCE(rc.fluid_name, '')) LIKE ?")
		args = append(args, "%"+strings.ToLower(term)+"%")
	}
	args = append(args, normalizedQuery, limit)
	query := `
		SELECT rc.resource_key, rc.resource_name, COALESCE(rc.fluid_name, '')
		FROM resource_catalog rc
		WHERE rc.resource_kind='fluid' AND ` + strings.Join(predicates, " AND ") + `
		ORDER BY CASE WHEN lower(trim(rc.resource_name))=lower(trim(?)) THEN 0 ELSE 1 END,
		         lower(rc.resource_name), rc.resource_key
		LIMIT ?`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]resourceSearchCandidate, 0, limit)
	for rows.Next() {
		var candidate resourceSearchCandidate
		if err := rows.Scan(&candidate.Key, &candidate.Name, &candidate.FluidName); err != nil {
			return nil, err
		}
		candidate.Kind = "fluid"
		candidate.exactName = normalizeReferenceText(candidate.Name) == normalizedQuery
		candidate.retrievalOrder = len(result)
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func loadResourceRouteCounts(ctx context.Context, db *sql.DB, candidates []resourceSearchCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	placeholders := make([]string, len(candidates))
	args := make([]any, len(candidates))
	indexByKey := make(map[string][]int, len(candidates))
	for index, candidate := range candidates {
		placeholders[index] = "?"
		args[index] = candidate.Key
		indexByKey[candidate.Key] = append(indexByKey[candidate.Key], index)
	}
	rows, err := db.QueryContext(ctx, `
		SELECT output_resource_key,
		       count(DISTINCT recipe_id),
		       count(DISTINCT CASE WHEN is_primary=1 THEN recipe_id END)
		FROM recipe_routes
		WHERE output_resource_key IN (`+strings.Join(placeholders, ",")+`)
		GROUP BY output_resource_key`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var productionCount, primaryCount int
		if err := rows.Scan(&key, &productionCount, &primaryCount); err != nil {
			return err
		}
		for _, index := range indexByKey[key] {
			candidates[index].ProductionRouteCount = productionCount
			candidates[index].PrimaryRouteCount = primaryCount
		}
	}
	return rows.Err()
}

func resourceSearchFailure(ctx context.Context, operation string, err error) Result {
	if ctx.Err() != nil {
		return Result{Name: "resource_search", OK: false, ExitCode: -1, TimedOut: true, Stderr: "resource_search timed out"}
	}
	return Result{Name: "resource_search", OK: false, ExitCode: 1, Stderr: fmt.Sprintf("%s: %v", operation, err)}
}
