package greggpttools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const defaultRecipeSQLRows = 50

var recipeSQLUnsafeKeyword = regexp.MustCompile(`(?i)\b(attach|alter|analyze|create|delete|detach|drop|insert|reindex|replace|update|vacuum)\b|\bpragma(?:\b|_)`)
var recipeSQLFirstKeyword = regexp.MustCompile(`(?is)^\s*(select|with)\b`)
var recipeSQLSelectKeyword = regexp.MustCompile(`(?is)\bselect\b`)

func recipeSQLTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("recipe_sql", GroupGTNHData, "Run one read-only SELECT or WITH SELECT against the indexed GTNH production database. For viable production lines: (1) resolve the target in resource_catalog or item_search, (2) query recipe_routes by output_resource_key and compare expected_output_amount, voltage_tier, capability_key, and machine_name_hint, (3) query recipe_ingredients for candidate recipe_ids, (4) send all exact item identities to inventory_totals in one call, and (5) use handler_machine_options plus placed-block search to identify missing machines. Explore alternatives recursively only for missing inputs; do not assume the first recipe is best. recipe_routes excludes invalid, hidden, fake, and disabled recipes. Duplicate result column names are preserved with _2, _3, and later suffixes.", timeout, object(
		required("sql", stringSpec("Read-only SQLite query. Must be a single SELECT or WITH SELECT statement with no semicolon.")),
		optional("max_rows", intSpec("Maximum rows to return.", 1, 100, defaultRecipeSQLRows)),
	), func(ctx context.Context, a Arguments) (Result, error) {
		return executeRecipeSQL(ctx, cfg.resolvedRecipeSQLPath(), timeout, cfg.MaxOutputBytes, stringArg(a, "sql"), intArg(a, "max_rows", defaultRecipeSQLRows))
	})
}

func executeRecipeSQL(ctx context.Context, dbPath string, timeout time.Duration, maxOutputBytes int, query string, maxRows int) (Result, error) {
	query = strings.TrimSpace(query)
	if err := validateRecipeSQL(query); err != nil {
		return Result{}, err
	}
	if maxRows <= 0 || maxRows > 100 {
		maxRows = defaultRecipeSQLRows
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = DefaultMaxOutputLength
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	db, err := sql.Open("sqlite", readOnlySQLiteDSN(dbPath))
	if err != nil {
		return Result{}, fmt.Errorf("open recipe sqlite: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(timeoutCtx, query)
	if err != nil {
		if timeoutCtx.Err() != nil {
			return recipeSQLTimeoutResult(true), nil
		}
		return recipeSQLErrorResult(fmt.Errorf("query recipe sqlite: %w", err)), nil
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return Result{}, fmt.Errorf("read recipe sqlite columns: %w", err)
	}
	resultColumns := uniqueColumnNames(columns)
	resultRows := make([]map[string]any, 0, maxRows)
	values := make([]any, len(columns))
	scan := make([]any, len(columns))
	for i := range values {
		scan[i] = &values[i]
	}

	truncatedRows := false
	for rows.Next() {
		if len(resultRows) >= maxRows {
			truncatedRows = true
			break
		}
		if err := rows.Scan(scan...); err != nil {
			return Result{}, fmt.Errorf("scan recipe sqlite row: %w", err)
		}
		row := make(map[string]any, len(resultColumns))
		for i, column := range resultColumns {
			row[column] = sqliteJSONValue(values[i])
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		if timeoutCtx.Err() != nil {
			return recipeSQLTimeoutResult(true), nil
		}
		return Result{}, fmt.Errorf("iterate recipe sqlite rows: %w", err)
	}

	payload := map[string]any{
		"columns": resultColumns,
		"rows":    resultRows,
		"count":   len(resultRows),
		"truncated": map[string]bool{
			"rows":   truncatedRows,
			"output": false,
		},
	}
	stdout, outputTruncated, err := limitedJSON(payload, maxOutputBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: "recipe_sql", OK: true, Stdout: stdout, Truncated: truncatedRows || outputTruncated}, nil
}

func validateRecipeSQL(query string) error {
	if query == "" {
		return fmt.Errorf("recipe_sql query is required")
	}
	if strings.Contains(query, ";") {
		return fmt.Errorf("recipe_sql allows exactly one statement with no semicolons")
	}
	first := recipeSQLFirstKeyword.FindStringSubmatch(query)
	if len(first) == 0 {
		return fmt.Errorf("recipe_sql only allows SELECT or WITH SELECT queries")
	}
	if strings.EqualFold(first[1], "with") && !recipeSQLSelectKeyword.MatchString(query) {
		return fmt.Errorf("recipe_sql only allows SELECT or WITH SELECT queries")
	}
	if recipeSQLUnsafeKeyword.MatchString(maskRecipeSQLLiterals(query)) {
		return fmt.Errorf("recipe_sql rejects write, PRAGMA, ATTACH, and schema-changing SQL")
	}
	return nil
}

func maskRecipeSQLLiterals(query string) string {
	var b strings.Builder
	b.Grow(len(query))
	inSingle := false
	inDouble := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch {
		case inSingle:
			if ch == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					b.WriteByte(' ')
					i++
				} else {
					inSingle = false
				}
			}
			b.WriteByte(' ')
		case inDouble:
			if ch == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					b.WriteByte(' ')
					i++
				} else {
					inDouble = false
				}
			}
			b.WriteByte(' ')
		case ch == '\'':
			inSingle = true
			b.WriteByte(' ')
		case ch == '"':
			inDouble = true
			b.WriteByte(' ')
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func readOnlySQLiteDSN(path string) string {
	if path == ":memory:" {
		return path
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	return u.String()
}

func sqliteJSONValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return v
	}
}

func uniqueColumnNames(columns []string) []string {
	out := make([]string, 0, len(columns))
	used := map[string]bool{}
	nextSuffix := map[string]int{}
	for i, column := range columns {
		base := strings.TrimSpace(column)
		if base == "" {
			base = fmt.Sprintf("column_%d", i+1)
		}
		name := base
		if used[name] {
			suffix := nextSuffix[base]
			if suffix < 2 {
				suffix = 2
			}
			for used[fmt.Sprintf("%s_%d", base, suffix)] {
				suffix++
			}
			name = fmt.Sprintf("%s_%d", base, suffix)
			nextSuffix[base] = suffix + 1
		}
		used[name] = true
		out = append(out, name)
	}
	return out
}

func limitedJSON(payload map[string]any, maxOutputBytes int) (string, bool, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	if len(raw) <= maxOutputBytes {
		return string(raw), false, nil
	}
	truncated, ok := payload["truncated"].(map[string]bool)
	if !ok {
		truncated = map[string]bool{}
	}
	truncated["output"] = true
	payload["truncated"] = truncated
	rows, _ := payload["rows"].([]map[string]any)
	originalRowCount := len(rows)
	for len(rows) > 0 {
		rows = rows[:len(rows)-1]
		payload["rows"] = rows
		payload["count"] = len(rows)
		payload["omitted_rows"] = originalRowCount - len(rows)
		raw, err = json.Marshal(payload)
		if err != nil {
			return "", false, err
		}
		if len(raw) <= maxOutputBytes {
			return string(raw), true, nil
		}
	}
	payload["rows"] = []map[string]any{}
	payload["count"] = 0
	payload["omitted_rows"] = originalRowCount
	raw, err = json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	if len(raw) > maxOutputBytes {
		return "", false, fmt.Errorf("recipe_sql output limit %d is too small for valid JSON metadata", maxOutputBytes)
	}
	return string(raw), true, nil
}

func recipeSQLErrorResult(err error) Result {
	return Result{Name: "recipe_sql", OK: false, ExitCode: 1, Stderr: err.Error()}
}

func recipeSQLTimeoutResult(timedOut bool) Result {
	return Result{Name: "recipe_sql", OK: false, ExitCode: -1, TimedOut: timedOut, Stderr: "recipe_sql timed out"}
}
