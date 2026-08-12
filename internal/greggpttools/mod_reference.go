package greggpttools

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const defaultModReferenceLimit = 5

func itemIDLookupTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("item_id_lookup", GroupGTNHData, "Resolve a Minecraft numeric item ID and damage/meta against the exported GTNH runtime item index. Use this for requests such as 'what is item 8852/0'. Do not query items.id in recipe_sql: that column is an internal database row key, not the Minecraft numeric item ID.", timeout, object(
		required("id", intSpec("Minecraft numeric item ID.", 0, 65535, nil)),
		optional("damage", intSpec("Item damage/meta value.", 0, 32767, 0)),
	), func(ctx context.Context, a Arguments) (Result, error) {
		return lookupItemID(ctx, cfg.resolvedItemIndexPath(), cfg.MaxOutputBytes, intArg(a, "id", 0), intArg(a, "damage", 0))
	})
}

func modReferenceSearchTool(cfg Config, timeout time.Duration) Tool {
	return nativeTool("mod_reference_search", GroupGTNHData, "Search the bounded local reference corpus generated from exact installed GTNH mod artifacts. Use for source-defined mechanics, genetics, mutations, configuration, or implementation details that recipe_sql and the wiki cannot establish. Results include mod version, artifact hash, and source symbol; do not claim coverage for mods absent from the corpus.", timeout, object(
		required("query", stringSpec("Mechanic, trait, class, symbol, or value to find in the indexed mod references.")),
		optional("mod", stringSpec("Optional mod ID or name filter, for example botany or binnie.")),
		optional("limit", intSpec("Maximum reference records to return.", 1, 20, defaultModReferenceLimit)),
	), func(ctx context.Context, a Arguments) (Result, error) {
		return searchModReferences(ctx, cfg.resolvedModReferencePath(), cfg.MaxOutputBytes, stringArg(a, "query"), stringArg(a, "mod"), intArg(a, "limit", defaultModReferenceLimit))
	})
}

func lookupItemID(ctx context.Context, path string, maxOutputBytes, id, damage int) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("open item index: %w", err)
	}
	defer f.Close()

	target := strconv.Itoa(id) + "d" + strconv.Itoa(damage)
	rows := make([]map[string]any, 0, 1)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for line := 0; scanner.Scan(); line++ {
		if err := ctx.Err(); err != nil {
			return Result{Name: "item_id_lookup", OK: false, ExitCode: -1, TimedOut: true, Stderr: err.Error()}, nil
		}
		record := strings.SplitN(scanner.Text(), "\t", 4)
		if line == 0 || len(record) < 4 || strings.TrimSpace(record[0]) != target {
			continue
		}
		rows = append(rows, map[string]any{
			"id":               id,
			"damage":           damage,
			"display_name":     strings.TrimSpace(record[1]),
			"registry_name":    strings.TrimSpace(record[2]),
			"unlocalized_name": strings.TrimSpace(record[3]),
			"source":           filepath.Base(path),
		})
	}
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("read item index: %w", err)
	}
	payload := map[string]any{"id": id, "damage": damage, "rows": rows, "count": len(rows), "truncated": map[string]bool{"output": false}}
	stdout, truncated, err := limitedJSON(payload, maxOutputBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: "item_id_lookup", OK: true, Stdout: stdout, Truncated: truncated}, nil
}

type modReferenceRecord struct {
	ModID        string
	Version      string
	Artifact     string
	ArtifactHash string
	Source       string
	Subject      string
	Content      string
	Score        int
}

func searchModReferences(ctx context.Context, root string, maxOutputBytes int, query, modFilter string, limit int) (Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, fmt.Errorf("mod_reference_search query is required")
	}
	if limit <= 0 || limit > 20 {
		limit = defaultModReferenceLimit
	}
	records := make([]modReferenceRecord, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".tsv") {
			return nil
		}
		fileRecords, err := readModReferenceFile(path)
		if err != nil {
			return err
		}
		records = append(records, fileRecords...)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Name: "mod_reference_search", OK: false, ExitCode: 1, Stderr: "mod reference corpus is not installed"}, nil
		}
		if ctx.Err() != nil {
			return Result{Name: "mod_reference_search", OK: false, ExitCode: -1, TimedOut: true, Stderr: ctx.Err().Error()}, nil
		}
		return Result{}, fmt.Errorf("read mod reference corpus: %w", err)
	}

	for i := range records {
		records[i].Score = scoreModReference(records[i], query, modFilter)
	}
	records = filterScoredReferences(records)
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Score != records[j].Score {
			return records[i].Score > records[j].Score
		}
		if records[i].ModID != records[j].ModID {
			return records[i].ModID < records[j].ModID
		}
		return records[i].Subject < records[j].Subject
	})
	truncatedRows := len(records) > limit
	if truncatedRows {
		records = records[:limit]
	}
	rows := make([]map[string]any, 0, len(records))
	for _, record := range records {
		rows = append(rows, map[string]any{
			"mod_id":          record.ModID,
			"version":         record.Version,
			"artifact":        record.Artifact,
			"artifact_sha256": record.ArtifactHash,
			"source":          record.Source,
			"subject":         record.Subject,
			"content":         record.Content,
		})
	}
	payload := map[string]any{"query": query, "mod": modFilter, "rows": rows, "count": len(rows), "truncated": map[string]bool{"rows": truncatedRows, "output": false}}
	stdout, outputTruncated, err := limitedJSON(payload, maxOutputBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: "mod_reference_search", OK: true, Stdout: stdout, Truncated: truncatedRows || outputTruncated}, nil
}

func readModReferenceFile(path string) ([]modReferenceRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	rows := make([]modReferenceRecord, 0)
	for line := 0; ; line++ {
		record, err := r.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if line == 0 {
			continue
		}
		if len(record) < 7 {
			return nil, fmt.Errorf("%s: row %d has %d columns, want 7", path, line+1, len(record))
		}
		rows = append(rows, modReferenceRecord{ModID: record[0], Version: record[1], Artifact: record[2], ArtifactHash: record[3], Source: record[4], Subject: record[5], Content: record[6]})
	}
}

func scoreModReference(record modReferenceRecord, query, modFilter string) int {
	modNeedle := normalizeReferenceText(modFilter)
	modHaystack := normalizeReferenceText(record.ModID + " " + record.Artifact)
	if modNeedle != "" && !strings.Contains(modHaystack, modNeedle) {
		return 0
	}
	queryPhrase := normalizeReferenceText(query)
	subject := normalizeReferenceText(record.Subject)
	content := normalizeReferenceText(record.Content + " " + record.Source)
	score := 0
	if queryPhrase != "" && strings.Contains(subject, queryPhrase) {
		score += 100
	}
	if queryPhrase != "" && strings.Contains(content, queryPhrase) {
		score += 40
	}
	for _, term := range referenceTerms(queryPhrase) {
		if strings.Contains(subject, term) {
			score += 20
		}
		if strings.Contains(content, term) {
			score += 4
		}
	}
	return score
}

func filterScoredReferences(records []modReferenceRecord) []modReferenceRecord {
	out := records[:0]
	for _, record := range records {
		if record.Score > 0 {
			out = append(out, record)
		}
	}
	return out
}

func normalizeReferenceText(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

func referenceTerms(query string) []string {
	stop := map[string]bool{"a": true, "an": true, "and": true, "files": true, "find": true, "for": true, "get": true, "how": true, "in": true, "mod": true, "the": true, "to": true, "trait": true, "with": true}
	terms := make([]string, 0)
	for _, term := range strings.Fields(query) {
		if len(term) > 1 && !stop[term] {
			terms = append(terms, term)
		}
	}
	return terms
}
