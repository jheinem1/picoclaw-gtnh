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

type wikiPageOutput struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Query   string `json:"query"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary"`
	Source  string `json:"source"`
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

func TestGTNHWikiPageFailsWhenExtractIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeWikiPage(t, w, "Empty Page", " \n\t ")
	}))
	defer server.Close()

	out := runWikiPageExpectFailure(t, server.URL, "Empty Page", nil)
	if out.OK {
		t.Fatalf("ok = true, want failure")
	}
	if !strings.Contains(out.Error, "empty extract for page: Empty Page") {
		t.Fatalf("error = %q, want empty extract message", out.Error)
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
