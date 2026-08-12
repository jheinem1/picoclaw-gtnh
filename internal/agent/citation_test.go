package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFromResponseExtractsWebSearchURLCitations(t *testing.T) {
	var response Response
	err := json.Unmarshal([]byte(`{
		"id":"resp_search",
		"output":[
			{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"Quantum Entangled Singularity GTNH"}},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[
				{"type":"output_text","text":"É first citea","annotations":[{"type":"url_citation","start_index":8,"end_index":16,"title":"First","url":"https://wiki.gtnewhorizons.com/wiki/Applied_Energistics_2"}]},
				{"type":"output_text","text":" and citeb","annotations":[{"type":"url_citation","start_index":5,"end_index":13,"title":"Second","url":"https://github.com/GTNewHorizons/Applied-Energistics-2-Unofficial"}]}
			]}
		]
	}`), &response)
	if err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}

	got := fromResponse(&response)
	if got.FinalText != "É first citea and citeb" {
		t.Fatalf("FinalText = %q", got.FinalText)
	}
	if len(got.ToolCalls) != 0 {
		t.Fatalf("hosted web search became a local tool call: %#v", got.ToolCalls)
	}
	want := []URLCitation{
		{StartIndex: 8, EndIndex: 16, Title: "First", URL: "https://wiki.gtnewhorizons.com/wiki/Applied_Energistics_2"},
		{StartIndex: 21, EndIndex: 29, Title: "Second", URL: "https://github.com/GTNewHorizons/Applied-Energistics-2-Unofficial"},
	}
	if !reflect.DeepEqual(got.URLCitations, want) {
		t.Fatalf("URLCitations = %#v, want %#v", got.URLCitations, want)
	}
}

func TestFromResponseIgnoresMalformedURLCitations(t *testing.T) {
	var response Response
	err := json.Unmarshal([]byte(`{
		"id":"resp_bad_citations",
		"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{
			"type":"output_text","text":"answer","annotations":[
				{"type":"url_citation","start_index":3,"end_index":99,"title":"Out of bounds","url":"https://example.com"},
				{"type":"file_citation","file_id":"file_1","filename":"notes.txt","index":0}
			]
		}]}]
	}`), &response)
	if err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}
	got := fromResponse(&response)
	if got.FinalText != "answer" || len(got.URLCitations) != 0 {
		t.Fatalf("parsed malformed citations: %#v", got)
	}
}

func TestRunnerRendersSingularityWebCitationByChannel(t *testing.T) {
	const marker = "citewiki"
	answer := "One Quantum Entangled Singularity goes in each Quantum Ring; create the linked pair by exploding an AE2 Singularity with Enderpearl Dust. " + marker
	startByte := strings.Index(answer, marker)
	startRune := utf8.RuneCountInString(answer[:startByte])
	citation := URLCitation{
		StartIndex: startRune,
		EndIndex:   startRune + utf8.RuneCountInString(marker),
		Title:      "Applied Energistics 2",
		URL:        "https://wiki.gtnewhorizons.com/wiki/Applied_Energistics_2",
	}

	tests := []struct {
		name       string
		channel    Channel
		wantLink   bool
		wantMarker bool
	}{
		{name: "discord clickable citation", channel: ChannelDiscord, wantLink: true},
		{name: "minecraft clean text", channel: ChannelMinecraft},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClient{responses: []ModelResponse{{FinalText: answer, URLCitations: []URLCitation{citation}}}}
			runner := NewRunner(Config{Model: "test-model"}, client, newFakeRegistry())
			got, err := runner.Run(t.Context(), Request{Channel: tt.channel, User: "Snow", Message: "What singularities does the AE dimensional gate need?"})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if strings.Contains(got, marker) != tt.wantMarker {
				t.Fatalf("raw citation marker presence = %t in %q", strings.Contains(got, marker), got)
			}
			link := "[[1]](<https://wiki.gtnewhorizons.com/wiki/Applied_Energistics_2>)"
			if strings.Contains(got, link) != tt.wantLink {
				t.Fatalf("clickable citation presence = %t in %q", strings.Contains(got, link), got)
			}
			if !strings.Contains(got, "goes in each Quantum Ring") || !strings.Contains(got, "Enderpearl Dust") {
				t.Fatalf("answer content changed: %q", got)
			}
		})
	}
}

func TestRenderURLCitationsDeduplicatesAndRejectsUnsafeURLs(t *testing.T) {
	text := "Fact. cite"
	citations := []URLCitation{
		{StartIndex: 6, EndIndex: 10, URL: "https://example.com/source"},
		{StartIndex: 6, EndIndex: 10, URL: "https://example.com/source"},
		{StartIndex: 6, EndIndex: 10, URL: "javascript:alert(1)"},
	}
	got := renderURLCitations(text, citations, true)
	if got != "Fact. [[1]](<https://example.com/source>)" {
		t.Fatalf("renderURLCitations() = %q", got)
	}
}
