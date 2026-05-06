package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"greggpt-gtnh/internal/greggptauth"

	"github.com/openai/openai-go/v3/responses"
)

func TestToResponseRequestAddsGTNHWikiWebSearch(t *testing.T) {
	req, err := toResponseRequest(ModelRequest{
		Model:           "gpt-5",
		ReasoningEffort: "medium",
		Tools: []ToolDefinition{{
			Name:        "recipe_sql",
			Description: "Run a read-only recipe SQL query.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("toResponseRequest() error = %v", err)
	}

	if len(req.Tools) != 2 {
		t.Fatalf("len(Tools) = %d, want 2", len(req.Tools))
	}
	webSearch := req.Tools[0].OfWebSearch
	if webSearch == nil {
		t.Fatalf("first tool is not web_search: %+v", req.Tools[0])
	}
	if webSearch.Type != responses.WebSearchToolTypeWebSearch {
		t.Fatalf("web_search type = %q, want %q", webSearch.Type, responses.WebSearchToolTypeWebSearch)
	}
	if webSearch.SearchContextSize != responses.WebSearchToolSearchContextSizeMedium {
		t.Fatalf("search context size = %q, want medium", webSearch.SearchContextSize)
	}
	if got := webSearch.Filters.AllowedDomains; len(got) != 1 || got[0] != gtnhWikiSearchDomain {
		t.Fatalf("allowed domains = %#v, want [%q]", got, gtnhWikiSearchDomain)
	}
	if len(req.Include) != 1 || req.Include[0] != responses.ResponseIncludableWebSearchCallActionSources {
		t.Fatalf("Include = %#v, want web search action sources", req.Include)
	}
	if req.Reasoning.Effort != "medium" {
		t.Fatalf("Reasoning.Effort = %q, want medium", req.Reasoning.Effort)
	}
	if req.Tools[1].OfFunction == nil || req.Tools[1].OfFunction.Name != "recipe_sql" {
		t.Fatalf("second tool is not original function tool: %+v", req.Tools[1])
	}
}

func TestToResponseRequestDisableToolsOmitsWebAndFunctionTools(t *testing.T) {
	req, err := toResponseRequest(ModelRequest{
		Model:        "gpt-5",
		DisableTools: true,
		Tools: []ToolDefinition{{
			Name:        "recipe_sql",
			Description: "Run a read-only recipe SQL query.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("toResponseRequest() error = %v", err)
	}

	if len(req.Tools) != 0 {
		t.Fatalf("len(Tools) = %d, want 0: %+v", len(req.Tools), req.Tools)
	}
	if len(req.Include) != 0 {
		t.Fatalf("Include = %#v, want empty", req.Include)
	}
}

func TestCodexClientSelectsAccountIDHeader(t *testing.T) {
	tests := []struct {
		name       string
		accountID  string
		idToken    string
		wantHeader string
	}{
		{
			name:       "direct account id wins",
			accountID:  "acct_direct",
			idToken:    makeAgentJWT(t, "acct_from_jwt"),
			wantHeader: "acct_direct",
		},
		{
			name:       "jwt account id fallback",
			idToken:    makeAgentJWT(t, "acct_from_jwt"),
			wantHeader: "acct_from_jwt",
		},
		{
			name:       "no account id omits header",
			wantHeader: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAccountID string
			var gotOriginator string
			var gotBeta string
			var gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/responses" {
					t.Fatalf("request path = %s, want /responses", r.URL.Path)
				}
				gotAccountID = r.Header.Get(chatGPTAccountIDHeader)
				gotOriginator = r.Header.Get(codexOriginatorHeader)
				gotBeta = r.Header.Get(openAIBetaHeader)
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":0,"model":"gpt-5-codex","output":[]}`))
			}))
			defer server.Close()

			authFile := filepath.Join(t.TempDir(), "auth.json")
			lastRefresh := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
			if err := greggptauth.NewStore(authFile).Save(&greggptauth.Credentials{
				Tokens: &greggptauth.TokenData{
					IDToken:      tt.idToken,
					AccessToken:  "access_token",
					RefreshToken: "refresh_token",
					AccountID:    tt.accountID,
				},
				LastRefresh: &lastRefresh,
			}); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			client := NewCodexClient(CodexClientOptions{
				AuthFile: authFile,
				BaseURL:  server.URL,
				RefreshOptions: greggptauth.RefreshOptions{
					Now: func() time.Time { return lastRefresh.Add(time.Hour) },
				},
			})
			_, err := client.CreateResponse(context.Background(), responses.ResponseNewParams{})
			if err != nil {
				t.Fatalf("CreateResponse() error = %v", err)
			}

			if gotAccountID != tt.wantHeader {
				t.Fatalf("%s = %q, want %q", chatGPTAccountIDHeader, gotAccountID, tt.wantHeader)
			}
			if gotOriginator != codexOriginatorValue {
				t.Fatalf("%s = %q, want %q", codexOriginatorHeader, gotOriginator, codexOriginatorValue)
			}
			if gotBeta != openAIBetaValue {
				t.Fatalf("%s = %q, want %q", openAIBetaHeader, gotBeta, openAIBetaValue)
			}
			if gotAuth != "Bearer access_token" {
				t.Fatalf("Authorization = %q, want bearer token", gotAuth)
			}
		})
	}
}

func makeAgentJWT(t *testing.T, accountID string) string {
	t.Helper()
	payload := map[string]any{}
	if accountID != "" {
		payload["https://api.openai.com/auth"] = map[string]any{"chatgpt_account_id": accountID}
	}
	return encodeAgentJWTPart(t, map[string]any{"alg": "none"}) + "." + encodeAgentJWTPart(t, payload) + ".signature"
}

func encodeAgentJWTPart(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}
