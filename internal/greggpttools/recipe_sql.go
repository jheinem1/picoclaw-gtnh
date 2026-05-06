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
	return nativeTool("recipe_sql", GroupGTNHData, "Run a read-only SELECT or WITH SELECT query against the indexed GTNH recipe SQLite database.", timeout, object(
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
		row := make(map[string]any, len(columns))
		for i, column := range columns {
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
		"columns": columns,
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
	payload["rows"] = []map[string]any{}
	payload["count"] = 0
	raw, err = json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	if len(raw) <= maxOutputBytes {
		return string(raw), true, nil
	}
	return string(raw[:maxOutputBytes]), true, nil
}

func recipeSQLErrorResult(err error) Result {
	return Result{Name: "recipe_sql", OK: false, ExitCode: 1, Stderr: err.Error()}
}

func recipeSQLTimeoutResult(timedOut bool) Result {
	return Result{Name: "recipe_sql", OK: false, ExitCode: -1, TimedOut: timedOut, Stderr: "recipe_sql timed out"}
}
