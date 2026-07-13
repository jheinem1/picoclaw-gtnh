package greggptauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"greggpt-gtnh/internal/filelock"
)

const (
	DefaultRefreshInterval = 8 * time.Hour

	openAIRefreshTokenURL = "https://auth.openai.com/oauth/token"
	openAIClientID        = "app_EMoamEEZ73f0CkXaXp7hrann"
)

var ErrMissingCredentials = errors.New("missing greggpt oauth credentials")

type Credentials struct {
	Provider    string     `json:"provider,omitempty"`
	AuthMethod  string     `json:"auth_method,omitempty"`
	AuthMode    string     `json:"auth_mode,omitempty"`
	Tokens      *TokenData `json:"tokens,omitempty"`
	LastRefresh *time.Time `json:"last_refresh,omitempty"`
}

type TokenData struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

type Store struct {
	path string
}

type RefreshOptions struct {
	HTTPClient      *http.Client
	TokenURL        string
	ClientID        string
	RefreshInterval time.Duration
	Now             func() time.Time
}

type refreshRequest struct {
	ClientID     string `json:"client_id"`
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func NewStore(path string) Store {
	if path == "" {
		path = AuthFileFromEnv()
	}
	return Store{path: path}
}

func AuthFileFromEnv() string {
	if path := strings.TrimSpace(os.Getenv(EnvAuthFile)); path != "" {
		return path
	}
	return DefaultAuthFile
}

func (s Store) Path() string {
	return s.path
}

func (s Store) Load() (*Credentials, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrMissingCredentials
	}
	if err != nil {
		return nil, err
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	return &creds, nil
}

func (s Store) Save(creds *Credentials) error {
	lock, err := filelock.Acquire(context.Background(), s.lockPath())
	if err != nil {
		return err
	}
	defer lock.Release()
	return s.saveUnlocked(creds)
}

func (s Store) saveUnlocked(creds *Credentials) error {
	if creds == nil {
		return ErrMissingCredentials
	}
	normalized := *creds
	normalized.setDefaults()
	if err := normalized.Validate(); err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(&normalized, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".auth-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	return nil
}

func (s Store) EnsureFresh(ctx context.Context, opts RefreshOptions) (*Credentials, error) {
	lock, err := filelock.Acquire(ctx, s.lockPath())
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	creds, err := s.Load()
	if err != nil {
		return nil, err
	}
	if !creds.ShouldRefresh(now(opts), refreshInterval(opts)) {
		return creds, nil
	}

	refreshed, err := Refresh(ctx, creds, opts)
	if err != nil {
		return nil, err
	}
	if err := s.saveUnlocked(refreshed); err != nil {
		return nil, err
	}
	return refreshed, nil
}

func (s Store) lockPath() string {
	return s.path + ".lock"
}

func (c *Credentials) Validate() error {
	if c == nil || c.Tokens == nil {
		return ErrMissingCredentials
	}
	if strings.TrimSpace(c.Tokens.AccessToken) == "" || strings.TrimSpace(c.Tokens.RefreshToken) == "" {
		return ErrMissingCredentials
	}
	return nil
}

func (c *Credentials) ShouldRefresh(at time.Time, interval time.Duration) bool {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	if c == nil || c.LastRefresh == nil {
		return true
	}
	return !at.Before(c.LastRefresh.Add(interval))
}

func (c *Credentials) AccountID() string {
	if c == nil || c.Tokens == nil {
		return ""
	}
	if accountID := strings.TrimSpace(c.Tokens.AccountID); accountID != "" {
		return accountID
	}
	return accountIDFromJWT(c.Tokens.IDToken)
}

func Refresh(ctx context.Context, creds *Credentials, opts RefreshOptions) (*Credentials, error) {
	if err := creds.Validate(); err != nil {
		return nil, err
	}

	body := refreshRequest{
		ClientID:     clientID(opts),
		GrantType:    "refresh_token",
		RefreshToken: creds.Tokens.RefreshToken,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL(opts), strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := httpClient(opts).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("refresh openai oauth token: %s: %s", res.Status, strings.TrimSpace(string(responseBody)))
	}

	var decoded refreshResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, err
	}
	if decoded.AccessToken == "" && decoded.RefreshToken == "" && decoded.IDToken == "" {
		return nil, errors.New("refresh openai oauth token: response did not include tokens")
	}

	refreshed := *creds
	tokens := *creds.Tokens
	if decoded.IDToken != "" {
		tokens.IDToken = decoded.IDToken
	}
	if decoded.AccessToken != "" {
		tokens.AccessToken = decoded.AccessToken
	}
	if decoded.RefreshToken != "" {
		tokens.RefreshToken = decoded.RefreshToken
	}
	refreshed.Tokens = &tokens
	refreshedAt := now(opts)
	refreshed.LastRefresh = &refreshedAt
	refreshed.setDefaults()
	return &refreshed, nil
}

func (c *Credentials) setDefaults() {
	if c.Provider == "" {
		c.Provider = ProviderOpenAI
	}
	if c.AuthMethod == "" {
		c.AuthMethod = AuthMethodOAuth
	}
	if c.AuthMode == "" {
		c.AuthMode = "chatgpt"
	}
}

func httpClient(opts RefreshOptions) *http.Client {
	if opts.HTTPClient != nil {
		return opts.HTTPClient
	}
	return http.DefaultClient
}

func tokenURL(opts RefreshOptions) string {
	if opts.TokenURL != "" {
		return opts.TokenURL
	}
	return openAIRefreshTokenURL
}

func clientID(opts RefreshOptions) string {
	if opts.ClientID != "" {
		return opts.ClientID
	}
	return openAIClientID
}

func refreshInterval(opts RefreshOptions) time.Duration {
	if opts.RefreshInterval != 0 {
		return opts.RefreshInterval
	}
	return DefaultRefreshInterval
}

func now(opts RefreshOptions) time.Time {
	if opts.Now != nil {
		return opts.Now().UTC()
	}
	return time.Now().UTC()
}

func accountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Auth.AccountID
}
