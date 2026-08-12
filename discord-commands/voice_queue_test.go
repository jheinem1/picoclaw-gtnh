package main

import (
	"context"
	"testing"
	"time"
)

func TestEnqueueLatestVoiceTurnQueuesWhenEmpty(t *testing.T) {
	turns := make(chan voiceTurn, 1)
	want := voiceTurn{transcript: "first", queuedAt: time.Now()}
	if replaced := enqueueLatestVoiceTurn(turns, want); replaced {
		t.Fatal("empty queue reported a replacement")
	}
	if got := <-turns; got.transcript != want.transcript {
		t.Fatalf("queued transcript = %q, want %q", got.transcript, want.transcript)
	}
}

func TestEnqueueLatestVoiceTurnReplacesPendingTurn(t *testing.T) {
	turns := make(chan voiceTurn, 1)
	turns <- voiceTurn{transcript: "stale", queuedAt: time.Now()}
	want := voiceTurn{transcript: "latest", queuedAt: time.Now()}
	if replaced := enqueueLatestVoiceTurn(turns, want); !replaced {
		t.Fatal("full queue did not report a replacement")
	}
	if got := <-turns; got.transcript != want.transcript {
		t.Fatalf("queued transcript = %q, want latest transcript %q", got.transcript, want.transcript)
	}
}

func TestDaveJoinAttemptTimeoutSplitsStartupBudget(t *testing.T) {
	if got := daveJoinAttemptTimeout(30 * time.Second); got != 15*time.Second {
		t.Fatalf("30 second startup budget split = %s, want 15s", got)
	}
	if got := daveJoinAttemptTimeout(4 * time.Second); got != 5*time.Second {
		t.Fatalf("short startup budget split = %s, want 5s minimum", got)
	}
}

func TestVoiceSessionTerminateCancelsAndNotifiesManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	terminal := make(chan *discordVoiceSession, 1)
	session := &discordVoiceSession{
		ctx:        ctx,
		cancel:     cancel,
		onTerminal: func(got *discordVoiceSession) { terminal <- got },
	}
	session.terminate("backend failed")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("terminal voice session context was not cancelled")
	}
	select {
	case got := <-terminal:
		if got != session {
			t.Fatal("terminal callback received the wrong session")
		}
	case <-time.After(time.Second):
		t.Fatal("terminal voice session did not notify manager")
	}
	if got := session.LastError(); got != "backend failed" {
		t.Fatalf("last error=%q", got)
	}
}
