package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"greggpt-gtnh/internal/agent"
	"greggpt-gtnh/internal/greggptauth"
)

func TestVoiceCodexEnvironmentUsesSafeAllowlist(t *testing.T) {
	env := voiceCodexEnvironment([]string{
		"PATH=/usr/bin",
		"HOME=/root",
		"LANG=C.UTF-8",
		"CODEX_HOME=/old",
		"OPENAI_API_KEY=must-not-survive",
		"DISCORD_BOT_TOKEN=discord-secret",
		"GREGGPT_DISCORD_TOKEN=discord-secret-2",
		"KANBAN_DISCORD_TOKEN=kanban-secret",
		"DATHOST_API_TOKEN=dathost-token",
		"DATHOST_API_PASSWORD=dathost-password",
		"FUTURE_SECRET=value",
		"KEEP=value",
	}, "/voice-home")
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{
		"OPENAI_API_KEY=", "DISCORD_BOT_TOKEN=", "GREGGPT_DISCORD_TOKEN=",
		"KANBAN_DISCORD_TOKEN=", "DATHOST_API_TOKEN=", "DATHOST_API_PASSWORD=",
		"FUTURE_SECRET=", "KEEP=", "CODEX_HOME=/old",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("voice environment retained forbidden entry %q: %v", forbidden, env)
		}
	}
	for _, required := range []string{"PATH=/usr/bin", "HOME=/root", "LANG=C.UTF-8", "CODEX_HOME=/voice-home"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("voice environment missing expected entry %q: %v", required, env)
		}
	}
}

func TestCodexRealtimeVoiceWebRTCWithChatGPTAuth(t *testing.T) {
	if os.Getenv("GREGGPT_VOICE_LIVE_TEST") != "1" {
		t.Skip("set GREGGPT_VOICE_LIVE_TEST=1 to run the authenticated realtime smoke test")
	}
	authHome := os.Getenv("GREGGPT_VOICE_CODEX_HOME")
	if authHome == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		authHome = filepath.Join(userHome, ".codex")
	}
	workspace, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cfg := VoiceConfig{
		Enabled:              true,
		CodexBin:             "codex",
		CodexHome:            t.TempDir(),
		AuthFile:             filepath.Join(authHome, "auth.json"),
		RealtimeVoice:        "cove",
		StartupTimeout:       45 * time.Second,
		CodexProtocolVersion: "v3",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bridge, err := startCodexRealtimeVoiceBridge(ctx, cfg, agent.Config{Model: agent.DefaultModel}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	for range 25 {
		if err := bridge.AppendOpus(ctx, discordOpusSilence, discordOpusFrameDuration); err != nil {
			t.Fatal(err)
		}
		time.Sleep(discordOpusFrameDuration)
	}
	if err := bridge.AppendSpeech(ctx, "Say only: GregGPT voice test successful."); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for WebRTC Opus audio")
		case event := <-bridge.Events():
			switch event.Type {
			case RealtimeVoiceEventAudio:
				if len(event.Audio.Data) == 0 {
					t.Fatal("received empty WebRTC Opus audio")
				}
				return
			case RealtimeVoiceEventError:
				t.Fatal(event.Text)
			case RealtimeVoiceEventClosed:
				t.Fatalf("realtime session closed: %s", event.Text)
			}
		}
	}
}

func TestPrepareChatGPTVoiceHomeDoesNotCopyAuth(t *testing.T) {
	root := t.TempDir()
	authFile := filepath.Join(root, "shared", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authFile), 0o700); err != nil {
		t.Fatal(err)
	}
	authJSON := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"access","refresh_token":"refresh","account_id":"account"}}`)
	if err := os.WriteFile(authFile, authJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(root, "voice-codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyCopy := filepath.Join(codexHome, "auth.json")
	if err := os.WriteFile(legacyCopy, authJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := prepareChatGPTVoiceHome(VoiceConfig{AuthFile: authFile, CodexHome: codexHome}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyCopy); !os.IsNotExist(err) {
		t.Fatalf("isolated voice auth file still exists: %v", err)
	}
	got, err := os.ReadFile(authFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(authJSON) {
		t.Fatal("shared auth file was modified")
	}
}

func TestPrepareChatGPTVoiceHomeRejectsSharedAuthPath(t *testing.T) {
	codexHome := t.TempDir()
	err := prepareChatGPTVoiceHome(VoiceConfig{
		AuthFile:  filepath.Join(codexHome, "auth.json"),
		CodexHome: codexHome,
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("prepareChatGPTVoiceHome() error = %v", err)
	}
}

func TestChatGPTVoiceLoginParamsUseAccessTokenOnly(t *testing.T) {
	params, err := chatGPTVoiceLoginParams(&greggptauth.Credentials{
		Tokens: &greggptauth.TokenData{
			AccessToken:  "access-secret",
			RefreshToken: "refresh-must-not-be-copied",
			AccountID:    "account-123",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := params["type"]; got != "chatgptAuthTokens" {
		t.Fatalf("type = %v", got)
	}
	if got := params["accessToken"]; got != "access-secret" {
		t.Fatalf("accessToken = %v", got)
	}
	if got := params["chatgptAccountId"]; got != "account-123" {
		t.Fatalf("chatgptAccountId = %v", got)
	}
	if len(params) != 3 {
		t.Fatalf("login params unexpectedly contain additional credential fields: %v", params)
	}
	for key, value := range params {
		if strings.Contains(strings.ToLower(key), "refresh") || value == "refresh-must-not-be-copied" {
			t.Fatalf("login params exposed refresh token via %q", key)
		}
	}
}
