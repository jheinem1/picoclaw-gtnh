package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	disvoice "github.com/disgoorg/disgo/voice"
)

const discordOpusFrameDuration = 20 * time.Millisecond

// An Opus comfort-noise/silence packet used to give the realtime server enough
// quiet audio for server-side VAD to close an utterance.
var discordOpusSilence = []byte{0xf8, 0xff, 0xfe}

type discordVoicePlayer struct {
	ctx context.Context
	cfg VoiceConfig
	vc  disvoice.Conn

	frames chan []byte

	mu     sync.Mutex
	closed bool
}

func newDiscordVoicePlayer(ctx context.Context, cfg VoiceConfig, vc disvoice.Conn) *discordVoicePlayer {
	return &discordVoicePlayer{ctx: ctx, cfg: cfg, vc: vc, frames: make(chan []byte, 100)}
}

func (p *discordVoicePlayer) Write(chunk RealtimeVoiceAudioChunk) error {
	if len(chunk.Data) == 0 {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("Discord voice player is closed")
	}
	p.mu.Unlock()

	packet := append([]byte(nil), chunk.Data...)
	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.frames <- packet:
		return nil
	}
}

func (p *discordVoicePlayer) ProvideOpusFrame() ([]byte, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, io.EOF
	}
	select {
	case frame := <-p.frames:
		return frame, nil
	default:
		return nil, nil
	}
}

func (p *discordVoicePlayer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
}
