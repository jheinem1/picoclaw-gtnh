package greggpttools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultOreGenerationRows = 50

var oreQuerySeparators = regexp.MustCompile(`[^a-z0-9]+`)

type oreCandidate struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

func oreGenerationTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("ore_generation_lookup", GroupGTNHData, "Look up where a GTNH ore material generates from the indexed schema-v2 worldgen data. Use this before the wiki for ore veins, small ores, Y levels, vein roles, or dimensions. A query such as 'Yttrium ore' resolves the material even when it is a secondary ore in a differently named vein.", timeout, object(
		required("query", stringSpec("Ore material or vein name, for example Yttrium ore or Niobium vein.")),
		optional("kind", enumStringSpec("Resolve the query as a material, a named vein, or infer it. Use vein for questions about every material in one named vein.", []any{"auto", "material", "vein"}, "auto")),
		optional("dimension", stringSpec("Restrict results to a dimension name, for example Barnard F, MakeMake, Triton, or Vega B.")),
		optional("include_small_ores", boolSpec("Include matching small-ore generation in addition to full ore veins.", true)),
		optional("limit", intSpec("Maximum generation rows to return.", 1, 100, defaultOreGenerationRows)),
	), func(ctx context.Context, a Arguments) (Result, error) {
		includeSmallOres := true
		if _, ok := a["include_small_ores"]; ok {
			includeSmallOres = boolArg(a, "include_small_ores")
		}
		return executeOreGenerationLookup(
			ctx,
			cfg.resolvedRecipeSQLPath(),
			timeout,
			cfg.MaxOutputBytes,
			stringArg(a, "query"),
			stringArg(a, "kind"),
			stringArg(a, "dimension"),
			includeSmallOres,
			intArg(a, "limit", defaultOreGenerationRows),
		)
	})
}

func executeOreGenerationLookup(ctx context.Context, dbPath string, timeout time.Duration, maxOutputBytes int, query, kind, dimension string, includeSmallOres bool, limit int) (Result, error) {
	rawQuery := strings.TrimSpace(query)
	if rawQuery == "" {
		return Result{}, fmt.Errorf("ore_generation_lookup query is required")
	}
	if limit <= 0 || limit > 100 {
		limit = defaultOreGenerationRows
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = DefaultMaxOutputLength
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "auto"
	}
	normalized, suffixKind := normalizeOreQuery(rawQuery)
	if normalized == "" {
		return Result{}, fmt.Errorf("ore_generation_lookup query must contain a material or vein name")
	}
	if kind == "auto" && suffixKind != "" {
		kind = suffixKind
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	db, err := sql.Open("sqlite", readOnlySQLiteDSN(dbPath))
	if err != nil {
		return Result{}, fmt.Errorf("open worldgen sqlite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if err := requireOreGenerationSchema(timeoutCtx, db); err != nil {
		return oreGenerationErrorResult(err), nil
	}
	candidates, exact, err := resolveOreCandidates(timeoutCtx, db, normalized, kind)
	if err != nil {
		if timeoutCtx.Err() != nil {
			return oreGenerationTimeoutResult(), nil
		}
		return oreGenerationErrorResult(err), nil
	}
	if len(candidates) != 1 {
		status := "not_found"
		if len(candidates) > 1 {
			status = "ambiguous"
		}
		return oreGenerationJSONResult(map[string]any{
			"query":      rawQuery,
			"normalized": normalized,
			"status":     status,
			"candidates": candidates,
			"routes":     []map[string]any{},
		}, maxOutputBytes, false)
	}

	selected := candidates[0]
	routes, truncated, err := queryOreGenerationRoutes(timeoutCtx, db, selected, dimension, includeSmallOres, limit)
	if err != nil {
		if timeoutCtx.Err() != nil {
			return oreGenerationTimeoutResult(), nil
		}
		return oreGenerationErrorResult(err), nil
	}
	status := "ok"
	if len(routes) == 0 {
		status = "no_routes"
	}
	return oreGenerationJSONResult(map[string]any{
		"query":              rawQuery,
		"normalized":         normalized,
		"status":             status,
		"exact_match":        exact,
		"resolved":           selected,
		"dimension_filter":   strings.TrimSpace(dimension),
		"include_small_ores": includeSmallOres,
		"routes":             routes,
		"truncated":          truncated,
	}, maxOutputBytes, truncated)
}

func normalizeOreQuery(query string) (string, string) {
	words := strings.Fields(oreQuerySeparators.ReplaceAllString(strings.ToLower(strings.TrimSpace(query)), " "))
	if len(words) == 0 {
		return "", ""
	}
	suffixKind := ""
	last := words[len(words)-1]
	if last == "vein" || last == "veins" {
		suffixKind = "vein"
		words = words[:len(words)-1]
	} else {
		for len(words) > 1 {
			last = words[len(words)-1]
			switch last {
			case "ore", "ores", "material", "dust", "crushed", "purified", "centrifuged":
				words = words[:len(words)-1]
				suffixKind = "material"
			default:
				return strings.Join(words, " "), suffixKind
			}
		}
	}
	return strings.Join(words, " "), suffixKind
}

func normalizeOreName(value string) string {
	return strings.Join(strings.Fields(oreQuerySeparators.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), " ")), "")
}

func requireOreGenerationSchema(ctx context.Context, db *sql.DB) error {
	var version string
	if err := db.QueryRowContext(ctx, "SELECT value FROM manifest WHERE key='schema_version'").Scan(&version); err != nil {
		return fmt.Errorf("worldgen database is missing schema_version: %w", err)
	}
	if version != "2" {
		return fmt.Errorf("worldgen lookup requires schema_version 2, found %q", version)
	}
	var available string
	if err := db.QueryRowContext(ctx, "SELECT value FROM manifest WHERE key='worldgen_data_available'").Scan(&available); err != nil || available != "1" {
		return fmt.Errorf("schema_version 2 database has no verified worldgen data; regenerate and import the recipe/worldgen dump")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='view' AND name='ore_generation_routes'").Scan(&count); err != nil {
		return fmt.Errorf("inspect worldgen schema: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("schema_version 2 database is missing ore_generation_routes; regenerate and import the recipe/worldgen dump")
	}
	return nil
}

func resolveOreCandidates(ctx context.Context, db *sql.DB, query, kind string) ([]oreCandidate, bool, error) {
	lookupKinds := []string{"material", "vein"}
	if kind == "material" || kind == "vein" {
		lookupKinds = []string{kind}
	}
	for _, lookupKind := range lookupKinds {
		found, err := findOreCandidates(ctx, db, query, lookupKind, true)
		if err != nil {
			return nil, false, err
		}
		if len(found) > 0 {
			return found, true, nil
		}
	}
	var candidates []oreCandidate
	for _, lookupKind := range lookupKinds {
		found, err := findOreCandidates(ctx, db, query, lookupKind, false)
		if err != nil {
			return nil, false, err
		}
		candidates = append(candidates, found...)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind < candidates[j].Kind
		}
		return candidates[i].Name < candidates[j].Name
	})
	return candidates, false, nil
}

func findOreCandidates(ctx context.Context, db *sql.DB, query, kind string, exact bool) ([]oreCandidate, error) {
	var sqlText string
	var args []any
	if kind == "material" {
		if exact {
			sqlText = "SELECT material_key, COALESCE(localized_name, internal_name) FROM ore_materials WHERE lower(internal_name)=? OR lower(localized_name)=? OR lower(material_key)=? ORDER BY localized_name LIMIT 10"
			args = []any{query, query, "material:" + query}
		} else {
			sqlText = "SELECT material_key, COALESCE(localized_name, internal_name) FROM ore_materials WHERE lower(internal_name) LIKE ? OR lower(localized_name) LIKE ? ORDER BY localized_name LIMIT 10"
			args = []any{"%" + query + "%", "%" + query + "%"}
		}
	} else {
		if exact {
			sqlText = "SELECT DISTINCT vein_key, display_name FROM ore_veins WHERE lower(internal_name)=? OR lower(display_name)=? OR lower(vein_key)=? ORDER BY display_name LIMIT 10"
			args = []any{query, query, "vein:" + query}
		} else {
			sqlText = "SELECT DISTINCT vein_key, display_name FROM ore_veins WHERE lower(internal_name) LIKE ? OR lower(display_name) LIKE ? ORDER BY display_name LIMIT 10"
			args = []any{"%" + query + "%", "%" + query + "%"}
		}
	}
	rows, err := db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", kind, err)
	}
	defer rows.Close()
	var out []oreCandidate
	for rows.Next() {
		var key, name string
		if err := rows.Scan(&key, &name); err != nil {
			return nil, err
		}
		out = append(out, oreCandidate{Kind: kind, Key: key, Name: name})
	}
	return out, rows.Err()
}

func queryOreGenerationRoutes(ctx context.Context, db *sql.DB, selected oreCandidate, dimension string, includeSmallOres bool, limit int) ([]map[string]any, bool, error) {
	column := "material_key"
	if selected.Kind == "vein" {
		column = "source_key"
	}
	query := "SELECT generation_kind, source_key, source_name, material_key, material_name, role, dimension_key, dimension_name, min_y, max_y, weight, density, size, amount_per_chunk FROM ore_generation_routes WHERE " + column + " = ?"
	if selected.Kind == "vein" {
		query += " AND generation_kind='vein'"
	}
	if !includeSmallOres {
		query += " AND generation_kind='vein'"
	}
	query += " ORDER BY generation_kind, source_name, role, dimension_name"
	rows, err := db.QueryContext(ctx, query, selected.Key)
	if err != nil {
		return nil, false, fmt.Errorf("query ore generation routes: %w", err)
	}
	defer rows.Close()
	dimension = normalizeOreName(dimension)
	result := make([]map[string]any, 0, limit)
	truncated := false
	for rows.Next() {
		var generationKind, sourceKey, sourceName, materialKey, materialName, role, dimensionKey, dimensionName sql.NullString
		var minY, maxY, weight, density, size, amountPerChunk sql.NullInt64
		if err := rows.Scan(&generationKind, &sourceKey, &sourceName, &materialKey, &materialName, &role, &dimensionKey, &dimensionName, &minY, &maxY, &weight, &density, &size, &amountPerChunk); err != nil {
			return nil, false, err
		}
		if dimension != "" && normalizeOreName(dimensionName.String) != dimension && normalizeOreName(dimensionKey.String) != dimension {
			continue
		}
		if len(result) >= limit {
			truncated = true
			continue
		}
		result = append(result, map[string]any{
			"generation_kind":  nullableString(generationKind),
			"source_key":       nullableString(sourceKey),
			"source_name":      nullableString(sourceName),
			"material_key":     nullableString(materialKey),
			"material_name":    nullableString(materialName),
			"role":             nullableString(role),
			"dimension_key":    nullableString(dimensionKey),
			"dimension":        nullableString(dimensionName),
			"min_y":            nullableInt(minY),
			"max_y":            nullableInt(maxY),
			"weight":           nullableInt(weight),
			"density":          nullableInt(density),
			"size":             nullableInt(size),
			"amount_per_chunk": nullableInt(amountPerChunk),
		})
	}
	return result, truncated, rows.Err()
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullableInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func oreGenerationJSONResult(payload map[string]any, maxOutputBytes int, truncated bool) (Result, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	if len(raw) > maxOutputBytes {
		routes, _ := payload["routes"].([]map[string]any)
		for len(raw) > maxOutputBytes && len(routes) > 0 {
			routes = routes[:len(routes)-1]
			payload["routes"] = routes
			payload["truncated"] = true
			truncated = true
			raw, err = json.Marshal(payload)
			if err != nil {
				return Result{}, err
			}
		}
	}
	if len(raw) > maxOutputBytes {
		return Result{}, fmt.Errorf("ore_generation_lookup output limit %d is too small for valid JSON", maxOutputBytes)
	}
	return Result{Name: "ore_generation_lookup", OK: true, Stdout: string(raw), Truncated: truncated}, nil
}

func oreGenerationErrorResult(err error) Result {
	return Result{Name: "ore_generation_lookup", OK: false, ExitCode: 1, Stderr: err.Error()}
}

func oreGenerationTimeoutResult() Result {
	return Result{Name: "ore_generation_lookup", OK: false, ExitCode: -1, TimedOut: true, Stderr: "ore_generation_lookup timed out"}
}
