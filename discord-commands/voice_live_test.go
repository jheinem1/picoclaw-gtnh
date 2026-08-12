package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TestLiveVoiceSmoke exercises the same live voice manager used by /voice join
// and /voice leave. It is opt-in because it opens a real Discord voice
// connection and a Codex app-server realtime session.
func TestLiveVoiceSmoke(t *testing.T) {
	if os.Getenv("GREGGPT_LIVE_VOICE_SMOKE") != "1" {
		t.Skip("set GREGGPT_LIVE_VOICE_SMOKE=1 to run the live Discord voice smoke test")
	}

	guildID := strings.TrimSpace(os.Getenv("GREGGPT_LIVE_VOICE_GUILD_ID"))
	voiceChannelID := strings.TrimSpace(os.Getenv("GREGGPT_LIVE_VOICE_CHANNEL_ID"))
	textChannelID := strings.TrimSpace(os.Getenv("GREGGPT_LIVE_VOICE_TEXT_CHANNEL_ID"))
	if guildID == "" || voiceChannelID == "" || textChannelID == "" {
		t.Fatal("GREGGPT_LIVE_VOICE_GUILD_ID, GREGGPT_LIVE_VOICE_CHANNEL_ID, and GREGGPT_LIVE_VOICE_TEXT_CHANNEL_ID are required")
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Voice.Enabled {
		t.Fatal("live voice is disabled")
	}

	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		t.Fatalf("create Discord session: %v", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates
	if err := session.Open(); err != nil {
		t.Fatalf("open Discord session: %v", err)
	}
	defer session.Close()

	manager := newDiscordVoiceManager(cfg, session, nil)
	defer manager.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	joined, err := manager.Join(ctx, VoiceJoinRequest{
		GuildID:        guildID,
		VoiceChannelID: voiceChannelID,
		TextChannelID:  textChannelID,
		UserID:         "live-smoke-test",
	})
	if err != nil {
		t.Fatalf("join live voice: %v", err)
	}
	if !strings.Contains(joined, voiceChannelID) {
		t.Fatalf("join response does not name channel: %q", joined)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		status := manager.Status(context.Background(), guildID)
		if strings.Contains(status, "is active in") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("voice manager did not become active: %s", status)
		}
		time.Sleep(250 * time.Millisecond)
	}

	left, err := manager.Leave(context.Background(), guildID)
	if err != nil {
		t.Fatalf("leave live voice: %v", err)
	}
	if !strings.Contains(left, "is disabled") {
		t.Fatalf("unexpected leave response: %q", left)
	}
	if status := manager.Status(context.Background(), guildID); !strings.Contains(status, "ready but inactive") {
		t.Fatalf("voice manager did not return inactive: %s", status)
	}
}
