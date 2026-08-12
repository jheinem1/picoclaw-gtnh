package workspace_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type wikiSearchMatch struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary"`
}

type wikiPageOutput struct {
	OK       bool              `json:"ok"`
	Exact    bool              `json:"exact"`
	Error    string            `json:"error"`
	Query    string            `json:"query"`
	Title    string            `json:"title"`
	URL      string            `json:"url"`
	Summary  string            `json:"summary"`
	Strategy string            `json:"strategy"`
	Matches  []wikiSearchMatch `json:"matches"`
	Source   string            `json:"source"`
}

func TestGTNHWikiPageMegaElectricBlastFurnaceIncludesConstruction(t *testing.T) {
	var requestValues url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestValues = r.URL.Query()
		if got := requestValues.Get("titles"); got != "Mega Electric Blast Furnace" {
			t.Fatalf("titles = %q, want Mega Electric Blast Furnace", got)
		}
		writeWikiPage(t, w, "Mega Electric Blast Furnace", strings.Join([]string{
			"<p>The Mega Electric Blast Furnace is a late-game multiblock.</p>",
			"",
			"== Construction ==",
			"Construction requires the controller, energy hatches, input/output buses, maintenance hatch, muffler hatch, and heating coils.",
			"Use TecTech energy tunnels for higher tier power handling.",
		}, "\n"))
	}))
	defer server.Close()

	out := runWikiPage(t, server.URL, "Mega Electric Blast Furnace", nil)
	if !out.OK {
		t.Fatalf("ok = false, error = %q", out.Error)
	}
	if requestValues.Get("exintro") != "" {
		t.Fatalf("request unexpectedly used exintro=%q", requestValues.Get("exintro"))
	}
	if requestValues.Get("explaintext") != "1" {
		t.Fatalf("explaintext = %q, want 1", requestValues.Get("explaintext"))
	}
	for _, want := range []string{
		"Mega Electric Blast Furnace is a late-game multiblock.",
		"Construction",
		"controller, energy hatches",
	} {
		if !strings.Contains(out.Summary, want) {
			t.Fatalf("summary missing %q: %q", want, out.Summary)
		}
	}
	if strings.Contains(out.Summary, "<p>") {
		t.Fatalf("summary was not cleaned: %q", out.Summary)
	}
}

func TestGTNHWikiPageElectricBlastFurnaceIsBoundedAndKeepsConstruction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("titles"); got != "Electric Blast Furnace" {
			t.Fatalf("titles = %q, want Electric Blast Furnace", got)
		}
		writeWikiPage(t, w, "Electric Blast Furnace", strings.Join([]string{
			"The Electric Blast Furnace is used for high-temperature recipes.",
			"== Construction ==",
			"Construction uses a 3x3x4 hollow casing shape with a controller, energy hatch, maintenance hatch, input bus, output bus, and muffler.",
			strings.Repeat("extra recipe details ", 40),
		}, "\n"))
	}))
	defer server.Close()

	out := runWikiPage(t, server.URL, "Electric Blast Furnace", []string{"GTNH_WIKI_MAX_CHARS=220"})
	if !out.OK {
		t.Fatalf("ok = false, error = %q", out.Error)
	}
	if len(out.Summary) > 220 {
		t.Fatalf("summary length = %d, want <= 220: %q", len(out.Summary), out.Summary)
	}
	for _, want := range []string{
		"Electric Blast Furnace is used for high-temperature recipes.",
		"Construction",
		"3x3x4 hollow casing shape",
	} {
		if !strings.Contains(out.Summary, want) {
			t.Fatalf("summary missing %q: %q", want, out.Summary)
		}
	}
}

func TestGTNHWikiPageSearchesWhenExactPageIsEmpty(t *testing.T) {
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "search" {
			searchCalls++
			if got := r.URL.Query().Get("srsearch"); got != "Thaumcraft autocrafting" {
				t.Fatalf("srsearch = %q, want Thaumcraft autocrafting", got)
			}
			if got := r.URL.Query().Get("srwhat"); got != "text" {
				t.Fatalf("srwhat = %q, want text", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": []map[string]any{{
				"title":   "Thaumic Energistics",
				"snippet": "Essentia storage and <span>Applied Energistics</span> integration.",
			}}}})
			return
		}
		writeWikiPage(t, w, "Empty Page", " \n\t ")
	}))
	defer server.Close()

	out := runWikiPage(t, server.URL, "Thaumcraft autocrafting", nil)
	if !out.OK || out.Exact {
		t.Fatalf("result = %+v, want successful non-exact fallback", out)
	}
	if searchCalls != 1 {
		t.Fatalf("search calls = %d, want 1", searchCalls)
	}
	if out.Strategy != "query" || len(out.Matches) != 1 {
		t.Fatalf("fallback = %+v, want one query match", out)
	}
	if out.Matches[0].Title != "Thaumic Energistics" || strings.Contains(out.Matches[0].Summary, "<span>") {
		t.Fatalf("match = %+v, want cleaned Thaumic Energistics result", out.Matches[0])
	}
}

func TestGTNHWikiPageSearchReturnsRelevantParentPageExtract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("list") == "search":
			if got := r.URL.Query().Get("srwhat"); got != "text" {
				t.Fatalf("srwhat = %q, want text", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": []map[string]any{{
				"title":   "Applied Energistics 2",
				"snippet": "Quantum table row without the answer.",
			}}}})
		case r.URL.Query().Get("titles") == "Applied Energistics 2":
			writeWikiPage(t, w, "Applied Energistics 2", strings.Join([]string{
				strings.Repeat("introductory storage and channel details ", 80),
				"ME Quantum Ring",
				"A pair of Quantum Rings are linked by placing a Quantum Entangled Singularity in each one. Quantum Entangled Singularities are always generated in pairs.",
			}, "\n"))
		default:
			writeWikiPage(t, w, "Missing", "")
		}
	}))
	defer server.Close()

	out := runWikiPage(t, server.URL, "Quantum Entangled Singularity", nil)
	if !out.OK || out.Exact || len(out.Matches) != 1 {
		t.Fatalf("result = %+v, want one non-exact match", out)
	}
	for _, want := range []string{"ME Quantum Ring", "linked by placing", "always generated in pairs"} {
		if !strings.Contains(out.Matches[0].Summary, want) {
			t.Fatalf("summary missing %q: %q", want, out.Matches[0].Summary)
		}
	}
}

func TestGTNHWikiPageBroadensConversationalAutocraftingQuery(t *testing.T) {
	searchQueries := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") != "search" {
			writeWikiPage(t, w, "Missing", "")
			return
		}
		query := r.URL.Query().Get("srsearch")
		searchQueries = append(searchQueries, query)
		w.Header().Set("Content-Type", "application/json")
		search := []map[string]any{}
		if query == "thaum* autom*" {
			search = append(search, map[string]any{
				"title":   "Complete Guide to Thaumic Energistics Automation",
				"snippet": "Request magically crafted items from an AE system.",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": search}})
	}))
	defer server.Close()

	query := "what all do we need for thaumcraft autocrafting in our ME system"
	out := runWikiPage(t, server.URL, query, nil)
	if !out.OK || out.Exact || out.Strategy != "broadened_terms" {
		t.Fatalf("result = %+v, want broadened fallback", out)
	}
	if len(searchQueries) != 2 || searchQueries[0] != query || searchQueries[1] != "thaum* autom*" {
		t.Fatalf("search queries = %#v", searchQueries)
	}
	if len(out.Matches) != 1 || out.Matches[0].Title != "Complete Guide to Thaumic Energistics Automation" {
		t.Fatalf("matches = %+v", out.Matches)
	}
}
func runWikiPage(t *testing.T, apiBase, title string, extraEnv []string) wikiPageOutput {
	t.Helper()
	cmd := exec.Command("sh", "./gtnh_wiki_page", title)
	cmd.Env = append(os.Environ(), append([]string{"GTNH_WIKI_API_BASE=" + apiBase}, extraEnv...)...)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("gtnh_wiki_page failed: %v", err)
	}
	var out wikiPageOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal output %q: %v", raw, err)
	}
	return out
}

func runWikiPageExpectFailure(t *testing.T, apiBase, title string, extraEnv []string) wikiPageOutput {
	t.Helper()
	cmd := exec.Command("sh", "./gtnh_wiki_page", title)
	cmd.Env = append(os.Environ(), append([]string{"GTNH_WIKI_API_BASE=" + apiBase}, extraEnv...)...)
	raw, err := cmd.Output()
	if err == nil {
		t.Fatalf("gtnh_wiki_page succeeded unexpectedly: %s", raw)
	}
	var out wikiPageOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal failure output %q: %v", raw, err)
	}
	return out
}

func writeWikiPage(t *testing.T, w http.ResponseWriter, title, extract string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{
		"query": map[string]any{
			"pages": []map[string]any{{
				"pageid":  1,
				"title":   title,
				"fullurl": "https://wiki.gtnewhorizons.com/wiki/" + strings.ReplaceAll(title, " ", "_"),
				"extract": extract,
			}},
		},
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
