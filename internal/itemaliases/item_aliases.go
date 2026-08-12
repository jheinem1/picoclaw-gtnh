package itemaliases

import (
	"encoding/csv"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Alias is a searchable secondary item label, such as a subtype tooltip that
// NEI indexes even when ItemStack.getDisplayName() remains generic.
type Alias struct {
	RegistryName string
	Damage       int
	Text         string
	Source       string
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))

	value = nonAlnum.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func queryForms(value string) []string {
	base := Normalize(value)
	forms := []string{base}
	words := strings.Fields(base)
	if len(words) > 0 {
		last := words[len(words)-1]
		if strings.HasSuffix(last, "s") && len(last) > 3 {
			words[len(words)-1] = strings.TrimSuffix(last, "s")
			forms = append(forms, strings.Join(words, " "))
		}
	}
	return forms
}

func Key(registryName string, damage int) string {
	return strings.ToLower(strings.TrimSpace(registryName)) + ":" + strconv.Itoa(damage)
}

// Load reads registry_name, damage, alias, source TSV rows. A missing optional
// alias artifact is treated as an empty index so older datasets remain usable.
func Load(path string) ([]Alias, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	out := make([]Alias, 0)
	for rowNumber := 0; ; rowNumber++ {
		row, readErr := r.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if rowNumber == 0 || len(row) < 3 {
			continue
		}
		damage, parseErr := strconv.Atoi(strings.TrimSpace(row[1]))
		if parseErr != nil || strings.TrimSpace(row[0]) == "" || Normalize(row[2]) == "" {
			continue
		}
		source := ""
		if len(row) > 3 {
			source = strings.TrimSpace(row[3])
		}
		out = append(out, Alias{RegistryName: strings.TrimSpace(row[0]), Damage: damage, Text: strings.TrimSpace(row[2]), Source: source})
	}
	return out, nil
}

// MatchScore evaluates the NEI-like searchable text composed from the tooltip
// alias plus the canonical display name. This makes "Aquamarine Pigment"
// match alias "Aquamarine" + display "Pigment" without confusing it with
// "Medium Aquamarine Pigment".
func MatchScore(query, alias, displayName string) int {
	combined := Normalize(alias + " " + displayName)
	compactCombined := strings.ReplaceAll(combined, " ", "")
	best := 0
	for _, q := range queryForms(query) {
		if q == "" {
			continue
		}
		score := 600
		switch {
		case combined == q || compactCombined == strings.ReplaceAll(q, " ", ""):
			score = 1000
		case strings.Contains(combined, q):
			score = 800
		default:
			for _, token := range strings.Fields(q) {
				if !strings.Contains(combined, token) {
					score = 0
					break
				}
			}
		}
		if score > best {
			best = score
		}
	}
	return best
}
