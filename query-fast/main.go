package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
var colorCode = regexp.MustCompile(`(?i)§[0-9a-fk-or]`)
var tierSuffix = regexp.MustCompile(`(?i)^(.+?)\s*\(([a-z0-9]+)\)$`)

var tiers = map[string]bool{"ulv": true, "lv": true, "mv": true, "hv": true, "ev": true, "iv": true, "luv": true, "zpm": true, "uv": true, "uhv": true, "uev": true, "uiv": true, "umv": true, "uxv": true}

type Item struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	RegName     string `json:"reg_name"`
	Name        string `json:"name"`
}

type Recipe struct {
	QuerySlug  string `json:"query_slug"`
	QueryName  string `json:"query_name"`
	OutSlug    string `json:"out_slug"`
	OutName    string `json:"out_name"`
	Handler    string `json:"handler"`
	Tab        string `json:"tab"`
	Ingredients string `json:"ingredients"`
}

func workspace() string {
	if v := os.Getenv("GTNH_WORKSPACE"); v != "" {
		return v
	}
	exe, _ := os.Executable()
	return filepath.Dir(filepath.Dir(exe))
}

func indexPath(env, rel string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return filepath.Join(workspace(), rel)
}

func normalize(s string) string {
	s = colorCode.ReplaceAllString(strings.ToLower(s), "")
	s = nonAlnum.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func tierAlias(display string) string {
	m := tierSuffix.FindStringSubmatch(display)
	if len(m) != 3 {
		return ""
	}
	return normalize(m[2] + " " + m[1])
}

func tierDisplay(query string) string {
	parts := strings.Fields(strings.ToLower(query))
	if len(parts) < 2 || !tiers[parts[0]] {
		return ""
	}
	return strings.Join(parts[1:], " ") + " (" + parts[0] + ")"
}

func ageText(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	age := int(time.Since(st.ModTime()).Seconds())
	if age < 0 {
		age = 0
	}
	switch {
	case age < 60:
		return fmt.Sprintf("%ds old", age)
	case age < 3600:
		return fmt.Sprintf("%dm old", age/60)
	case age < 172800:
		return fmt.Sprintf("%dh old", age/3600)
	default:
		return fmt.Sprintf("%dd old", age/86400)
	}
}

func freshness(items, recipes, oredict string) map[string]string {
	return map[string]string{"item_index": ageText(items), "recipe_index": ageText(recipes), "oredict_index": ageText(oredict)}
}

func readItems(path, query string, limit int) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	qn, tq := normalize(query), tierDisplay(query)
	tokens := strings.Fields(qn)
	type cand struct {
		score int
		name  string
		item  Item
	}
	var exact []Item
	var cands []cand
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) < 4 {
			continue
		}
		it := Item{Slug: cols[0], DisplayName: cols[1], RegName: cols[2], Name: cols[3]}
		displayLower := strings.ToLower(it.DisplayName)
		aliases := []string{normalize(it.DisplayName), tierAlias(it.DisplayName), normalize(it.RegName), normalize(it.Name)}
		if aliases[0] == qn || aliases[1] == qn || aliases[2] == qn || aliases[3] == qn || (tq != "" && displayLower == tq) {
			exact = append(exact, it)
			if len(exact) >= limit {
				return exact, nil
			}
			continue
		}
		joined := strings.Join(aliases, " ")
		score := 1_000_000
		if qn != "" && (strings.Contains(aliases[0], qn) || strings.Contains(aliases[1], qn) || strings.Contains(aliases[2], qn) || strings.Contains(aliases[3], qn)) {
			score = 50 + len(aliases[0])
		} else {
			hits := 0
			for _, tok := range tokens {
				if len(tok) >= 3 && strings.Contains(joined, tok) {
					hits++
				}
			}
			if hits > 0 {
				score = 200 - hits*30 + len(aliases[0])
			}
		}
		if score < 1_000_000 {
			cands = append(cands, cand{score: score, name: aliases[0], item: it})
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score < cands[j].score
		}
		return cands[i].name < cands[j].name
	})
	seen := map[string]bool{}
	out := []Item{}
	for _, c := range cands {
		if seen[c.item.Slug] {
			continue
		}
		seen[c.item.Slug] = true
		out = append(out, c.item)
		if len(out) >= limit {
			break
		}
	}
	return out, sc.Err()
}

func readRecipes(path, slug string) ([]Recipe, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []Recipe{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		line := sc.Text()
		if !strings.Contains(line, slug) {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 7 || (cols[0] != slug && cols[2] != slug) {
			continue
		}
		out = append(out, Recipe{QuerySlug: cols[0], QueryName: cols[1], OutSlug: cols[2], OutName: cols[3], Handler: cols[4], Tab: cols[5], Ingredients: cols[6]})
		if len(out) >= 30 {
			break
		}
	}
	return out, sc.Err()
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println(`{"ok":false,"error":"usage: gtnh_query_fast find-item|resolve-recipes <text>"}`)
		os.Exit(1)
	}
	itemsPath := indexPath("GTNH_ITEMS_INDEX", "gtnh-data/index/item_index.tsv")
	recipesPath := indexPath("GTNH_RECIPES_INDEX", "gtnh-data/index/recipe_index.tsv")
	oredictPath := indexPath("GTNH_OREDICT_INDEX", "gtnh-data/index/oredict_index.tsv")
	query := strings.Join(os.Args[2:], " ")
	items, err := readItems(itemsPath, query, 20)
	if err != nil {
		fmt.Printf(`{"ok":false,"error":%q}`+"\n", err.Error())
		os.Exit(1)
	}
	switch os.Args[1] {
	case "find-item":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "query": query, "oredict": false, "items": items, "freshness": freshness(itemsPath, recipesPath, oredictPath)})
	case "resolve-recipes":
		if len(items) == 0 {
			fmt.Println(`{"ok":false,"error":"item not found"}`)
			os.Exit(1)
		}
		recipes, err := readRecipes(recipesPath, items[0].Slug)
		if err != nil {
			fmt.Printf(`{"ok":false,"error":%q}`+"\n", err.Error())
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "query": query, "slug": items[0].Slug, "recipes": recipes, "matched_items": items[:1], "confidence": "single-best", "sources": []string{"gtnh-data/index/item_index.tsv", "gtnh-data/index/recipe_index.tsv"}, "freshness": freshness(itemsPath, recipesPath, oredictPath)})
	default:
		fmt.Println(`{"ok":false,"error":"unsupported command"}`)
		os.Exit(1)
	}
}
