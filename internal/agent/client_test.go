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
