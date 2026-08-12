package greggpttools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const defaultItemSearchLimit = 10

func itemSearchTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("item_search", GroupGTNHData, "Safely resolve an item name, registry identity, or NEI-searchable tooltip/subtype alias to exact GTNH registry identities and stable recipe resource keys. Search identity does not imply that an ordinary indexed recipe exists.", timeout, object(
		required("query", stringSpec("Item display name, registry name, unlocalized name, or NEI tooltip/subtype name.")),
		optional("limit", intSpec("Maximum candidates to return.", 1, 30, defaultItemSearchLimit)),
	), func(ctx context.Context, a Arguments) (Result, error) {
		return searchRecipeItems(ctx, cfg.resolvedRecipeSQLPath(), cfg.resolvedItemAliasesPath(), timeout, cfg.MaxOutputBytes, stringArg(a, "query"), intArg(a, "limit", defaultItemSearchLimit))
	})
}

func searchRecipeItems(ctx context.Context, dbPath, aliasPath string, timeout time.Duration, maxOutputBytes int, query string, limit int) (Result, error) {
	terms := referenceTerms(normalizeReferenceText(query))
	if len(terms) == 0 {
		terms = strings.Fields(normalizeReferenceText(query))
	}
	if len(terms) == 0 {
		return Result{}, fmt.Errorf("item_search query is required")
	}
	if limit <= 0 || limit > 30 {
		limit = defaultItemSearchLimit
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

	aliasRows, err := searchItemAliases(timeoutCtx, db, aliasPath, query, limit)
	if err != nil {
		return Result{Name: "item_search", OK: false, ExitCode: 1, Stderr: fmt.Sprintf("query item aliases: %v", err)}, nil
	}

	rows, err := db.QueryContext(timeoutCtx, `
		SELECT i.id,
		       'item:' || i.registry_name || ':' || i.damage AS resource_key,
		       i.display_name, i.registry_name, i.damage, i.unlocalized_name
		FROM item_search s
		JOIN items i ON i.id=s.rowid
		WHERE item_search MATCH ?
		ORDER BY CASE WHEN lower(trim(i.display_name)) = lower(trim(?)) THEN 0 ELSE 1 END,
		         bm25(item_search), lower(i.display_name), i.registry_name, i.damage
		LIMIT ?`, ftsQuery, query, limit)
	if err != nil {
		if timeoutCtx.Err() != nil {
			return Result{Name: "item_search", OK: false, ExitCode: -1, TimedOut: true, Stderr: "item_search timed out"}, nil
		}
		return Result{Name: "item_search", OK: false, ExitCode: 1, Stderr: fmt.Sprintf("query item search index: %v", err)}, nil
	}
	defer rows.Close()
	resultRows := make([]map[string]any, 0, limit)
	seen := map[string]bool{}
	for _, row := range aliasRows {
		resultRows = append(resultRows, map[string]any{
			"internal_item_row_id": row.ID,
			"resource_key":         row.ResourceKey,
			"display_name":         row.DisplayName,
			"registry_name":        row.RegistryName,
			"damage":               row.Damage,
			"unlocalized_name":     row.UnlocalizedName,
			"matched_alias":        row.MatchedAlias,
			"alias_source":         row.AliasSource,
		})
		seen[row.ResourceKey] = true
	}
	for rows.Next() {
		var id, damage int
		var resourceKey, displayName, registryName, unlocalizedName string
		if err := rows.Scan(&id, &resourceKey, &displayName, &registryName, &damage, &unlocalizedName); err != nil {
			return Result{}, fmt.Errorf("scan item search row: %w", err)
		}
		if seen[resourceKey] || len(resultRows) >= limit {
			continue
		}
		resultRows = append(resultRows, map[string]any{
			"internal_item_row_id": id,
			"resource_key":         resourceKey,
			"display_name":         displayName,
			"registry_name":        registryName,
			"damage":               damage,
			"unlocalized_name":     unlocalizedName,
		})
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("iterate item search rows: %w", err)
	}
	if len(resultRows) > limit {
		resultRows = resultRows[:limit]
	}
	payload := map[string]any{
		"query":     query,
		"fts_query": ftsQuery,
		"rows":      resultRows,
		"count":     len(resultRows),
		"truncated": map[string]bool{"output": false, "rows": len(resultRows) == limit},
	}
	stdout, truncated, err := limitedJSON(payload, maxOutputBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: "item_search", OK: true, Stdout: stdout, Truncated: truncated}, nil
}
