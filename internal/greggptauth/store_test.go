package greggptauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreLoadSaveTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewStore(path)
	lastRefresh := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	creds := &Credentials{
		Tokens: &TokenData{
			IDToken:      makeJWT(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_from_jwt"}}),
			AccessToken:  "access",
			RefreshToken: "refresh",
			AccountID:    "acct_direct",
		},
		LastRefresh: &lastRefresh,
	}

	if err := store.Save(creds); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Provider != ProviderOpenAI {
		t.Fatalf("Provider = %q, want %q", loaded.Provider, ProviderOpenAI)
	}
	if loaded.AuthMethod != AuthMethodOAuth {
		t.Fatalf("AuthMethod = %q, want %q", loaded.AuthMethod, AuthMethodOAuth)
	}
	if loaded.Tokens.AccessToken != "access" || loaded.Tokens.RefreshToken != "refresh" {
		t.Fatalf("loaded tokens = %#v", loaded.Tokens)
	}
	if got := loaded.AccountID(); got != "acct_direct" {
		t.Fatalf("AccountID() = %q, want acct_direct", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("auth file mode = %o, want 600", got)
	}
}

func TestStoreLoadMissingCredentials(t *testing.T) {
	_, err := NewStore(filepath.Join(t.TempDir(), "missing.json")).Load()
	if !errors.Is(err, ErrMissingCredentials) {
		t.Fatalf("Load() error = %v, want ErrMissingCredentials", err)
	}
}

func TestEnsureFreshRefreshThreshold(t *testing.T) {
	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC)
	lastRefresh := now.Add(-DefaultRefreshInterval)
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewStore(path)
	if err := store.Save(&Credentials{
		Tokens: &TokenData{
			AccessToken:  "old_access",
			RefreshToken: "old_refresh",
		},
		LastRefresh: &lastRefresh,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	var refreshCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalled = true
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("refresh path = %s, want /oauth/token", r.URL.Path)
		}
		var got refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if got.ClientID != openAIClientID || got.GrantType != "refresh_token" || got.RefreshToken != "old_refresh" {
			t.Fatalf("refresh request = %#v", got)
		}
		_, _ = w.Write([]byte(`{"access_token":"new_access","refresh_token":"new_refresh"}`))
	}))
	defer server.Close()

	creds, err := store.EnsureFresh(context.Background(), RefreshOptions{
		TokenURL: server.URL + "/oauth/token",
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("EnsureFresh() error = %v", err)
	}
	if !refreshCalled {
		t.Fatal("refresh endpoint was not called")
	}
	if creds.Tokens.AccessToken != "new_access" || creds.Tokens.RefreshToken != "new_refresh" {
		t.Fatalf("refreshed tokens = %#v", creds.Tokens)
	}
	if creds.LastRefresh == nil || !creds.LastRefresh.Equal(now) {
		t.Fatalf("LastRefresh = %v, want %v", creds.LastRefresh, now)
	}
}

func TestEnsureFreshSkipsBeforeRefreshThreshold(t *testing.T) {
	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC)
	lastRefresh := now.Add(-DefaultRefreshInterval + time.Second)
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewStore(path)
	if err := store.Save(&Credentials{
		Tokens: &TokenData{
			AccessToken:  "old_access",
			RefreshToken: "old_refresh",
		},
		LastRefresh: &lastRefresh,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("refresh endpoint should not be called before threshold")
	}))
	defer server.Close()

	creds, err := store.EnsureFresh(context.Background(), RefreshOptions{
		TokenURL: server.URL,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("EnsureFresh() error = %v", err)
	}
	if creds.Tokens.AccessToken != "old_access" {
		t.Fatalf("AccessToken = %q, want old_access", creds.Tokens.AccessToken)
	}
}

func TestEnsureFreshRefreshFailure(t *testing.T) {
	now := time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC)
	lastRefresh := now.Add(-DefaultRefreshInterval)
	path := filepath.Join(t.TempDir(), "auth.json")
	store := NewStore(path)
	if err := store.Save(&Credentials{
		Tokens: &TokenData{
			AccessToken:  "old_access",
			RefreshToken: "old_refresh",
		},
		LastRefresh: &lastRefresh,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"refresh_token_reused"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := store.EnsureFresh(context.Background(), RefreshOptions{
		TokenURL: server.URL,
		Now:      func() time.Time { return now },
	})
	if err == nil {
		t.Fatal("EnsureFresh() error = nil, want refresh failure")
	}

	loaded, loadErr := store.Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if loaded.Tokens.AccessToken != "old_access" {
		t.Fatalf("AccessToken after failed refresh = %q, want old_access", loaded.Tokens.AccessToken)
	}
}

func makeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	encode := func(v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		return base64URL(data)
	}
	return encode(map[string]any{"alg": "none"}) + "." + encode(payload) + ".signature"
}

func base64URL(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}
