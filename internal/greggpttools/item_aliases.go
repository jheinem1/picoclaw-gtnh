package greggpttools

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"greggpt-gtnh/internal/itemaliases"
)

type itemAliasCandidate struct {
	ID              int
	ResourceKey     string
	DisplayName     string
	RegistryName    string
	Damage          int
	UnlocalizedName string
	MatchedAlias    string
	AliasSource     string
	Score           int
}

func searchItemAliases(ctx context.Context, db *sql.DB, aliasPath, query string, limit int) ([]itemAliasCandidate, error) {
	aliases, err := itemaliases.Load(aliasPath)
	if err != nil {
		return nil, fmt.Errorf("load item aliases: %w", err)
	}
	if len(aliases) == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, 'item:' || registry_name || ':' || damage,
		       display_name, registry_name, damage, unlocalized_name
		FROM items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make(map[string]itemAliasCandidate)
	for rows.Next() {
		var row itemAliasCandidate
		if err := rows.Scan(&row.ID, &row.ResourceKey, &row.DisplayName, &row.RegistryName, &row.Damage, &row.UnlocalizedName); err != nil {
			return nil, err
		}
		items[itemaliases.Key(row.RegistryName, row.Damage)] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]itemAliasCandidate, 0)
	for _, alias := range aliases {
		row, ok := items[itemaliases.Key(alias.RegistryName, alias.Damage)]
		if !ok {
			continue
		}
		row.Score = itemaliases.MatchScore(query, alias.Text, row.DisplayName)
		if row.Score == 0 {
			continue
		}
		row.MatchedAlias = alias.Text
		row.AliasSource = alias.Source
		result = append(result, row)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].DisplayName != result[j].DisplayName {
			return result[i].DisplayName < result[j].DisplayName
		}
		return result[i].ResourceKey < result[j].ResourceKey
	})
	seen := map[string]bool{}
	unique := result[:0]
	for _, row := range result {
		if seen[row.ResourceKey] {
			continue
		}
		seen[row.ResourceKey] = true
		unique = append(unique, row)
		if len(unique) >= limit {
			break
		}
	}
	return unique, nil
}
