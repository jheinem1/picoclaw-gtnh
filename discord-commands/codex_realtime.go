package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"greggpt-gtnh/internal/agent"
	"greggpt-gtnh/internal/greggptauth"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type RealtimeVoiceEventType string

const (
	RealtimeVoiceEventStarted        RealtimeVoiceEventType = "started"
	RealtimeVoiceEventTranscriptDone RealtimeVoiceEventType = "transcript_done"
	RealtimeVoiceEventAudio          RealtimeVoiceEventType = "audio"
	RealtimeVoiceEventError          RealtimeVoiceEventType = "error"
	RealtimeVoiceEventClosed         RealtimeVoiceEventType = "closed"
)

type RealtimeVoiceAudioChunk struct {
	Data []byte
}

type RealtimeVoiceEvent struct {
	Type  RealtimeVoiceEventType
	Role  string
	Text  string
	Audio RealtimeVoiceAudioChunk
}

type RealtimeVoiceBridge interface {
	AppendOpus(context.Context, []byte, time.Duration) error
	AppendSpeech(context.Context, string) error
	Events() <-chan RealtimeVoiceEvent
	Close()
}

type codexRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type codexRPCEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCResponse struct {
	Result json.RawMessage
	Error  *codexRPCError
}

type codexRealtimeVoiceBridge struct {
	cfg       VoiceConfig
	agentCfg  agent.Config
	workspace string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan struct{}
	events chan RealtimeVoiceEvent

	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[int64]chan codexRPCResponse
	threadID string
	nextID   atomic.Int64

	peer       *webrtc.PeerConnection
	inputTrack *webrtc.TrackLocalStaticSample
	readyMu    sync.Mutex
	appStarted bool
	connected  bool
	readySent  bool

	closing   atomic.Bool
	closeOnce sync.Once
}

func validateChatGPTVoiceAuthFile(authPath string) error {
	if !filepath.IsAbs(authPath) {
		return errors.New("GREGGPT_AUTH_FILE must be an absolute path for voice")
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return fmt.Errorf("read ChatGPT auth for voice at %s: %w", authPath, err)
	}
	var auth struct {
		Mode   string          `json:"auth_mode"`
		Tokens json.RawMessage `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		return fmt.Errorf("parse ChatGPT auth for voice: %w", err)
	}
	if auth.Mode != "chatgpt" {
		return fmt.Errorf("voice requires ChatGPT auth; %s uses auth_mode=%q", authPath, auth.Mode)
	}
	if len(auth.Tokens) == 0 || string(auth.Tokens) == "null" {
		return errors.New("voice ChatGPT auth is missing OAuth tokens; refresh GregGPT login")
	}
	return nil
}

func prepareChatGPTVoiceHome(cfg VoiceConfig) error {
	if !filepath.IsAbs(cfg.CodexHome) {
		return errors.New("GREGGPT_VOICE_CODEX_HOME must be an absolute path")
	}
	if err := os.MkdirAll(cfg.CodexHome, 0o700); err != nil {
		return fmt.Errorf("create isolated voice Codex home: %w", err)
	}
	if err := os.Chmod(cfg.CodexHome, 0o700); err != nil {
		return fmt.Errorf("secure isolated voice Codex home: %w", err)
	}
	source, err := filepath.Abs(cfg.AuthFile)
	if err != nil {
		return err
	}
	target := filepath.Join(cfg.CodexHome, "auth.json")
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if source == target {
		return errors.New("GREGGPT_VOICE_CODEX_HOME must not contain the shared GREGGPT_AUTH_FILE")
	}
	// Older versions copied the complete shared OAuth document into this home.
	// Remove that legacy copy so the app-server never receives a refresh token
	// on disk. It is authenticated later with a memory-only access token.
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove legacy voice Codex auth copy: %w", err)
	}
	return nil
}

func chatGPTVoiceLoginParams(creds *greggptauth.Credentials) (map[string]any, error) {
	if creds == nil || creds.Tokens == nil || strings.TrimSpace(creds.Tokens.AccessToken) == "" {
		return nil, errors.New("voice ChatGPT auth is missing an OAuth access token; refresh GregGPT login")
	}
	accountID := strings.TrimSpace(creds.AccountID())
	if accountID == "" {
		return nil, errors.New("voice ChatGPT auth is missing a ChatGPT account id; refresh GregGPT login")
	}
	return map[string]any{
		"type":             "chatgptAuthTokens",
		"accessToken":      creds.Tokens.AccessToken,
		"chatgptAccountId": accountID,
	}, nil
}

func startCodexRealtimeVoiceBridge(ctx context.Context, cfg VoiceConfig, agentCfg agent.Config, workspace string) (RealtimeVoiceBridge, error) {
	bridge := &codexRealtimeVoiceBridge{
		cfg:       cfg,
		agentCfg:  agentCfg,
		workspace: workspace,
		done:      make(chan struct{}),
		events:    make(chan RealtimeVoiceEvent, 256),
		pending:   make(map[int64]chan codexRPCResponse),
	}
	if err := bridge.start(ctx); err != nil {
		bridge.Close()
		return nil, err
	}
	return bridge, nil
}

func (b *codexRealtimeVoiceBridge) start(parent context.Context) error {
	startupTimeout := b.cfg.StartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, startupTimeout)
	defer cancel()

	if err := prepareChatGPTVoiceHome(b.cfg); err != nil {
		return err
	}
	if err := validateChatGPTVoiceAuthFile(b.cfg.AuthFile); err != nil {
		return err
	}
	// EnsureFresh serializes refresh-token rotation with every other GregGPT
	// client. Only the resulting access token and account id are handed to the
	// app-server; the shared refresh token remains in the lock-managed store.
	// External app-server auth cannot refresh itself; reconnecting creates a new,
	// short-lived bridge that repeats this locked refresh before login.
	creds, err := greggptauth.NewStore(b.cfg.AuthFile).EnsureFresh(ctx, greggptauth.RefreshOptions{})
	if err != nil {
		return fmt.Errorf("load fresh ChatGPT auth for voice: %w", err)
	}
	loginParams, err := chatGPTVoiceLoginParams(creds)
	if err != nil {
		return err
	}
	if err := b.startWebRTC(); err != nil {
		return err
	}

	b.cmd = exec.Command(b.cfg.CodexBin,
		"app-server",
		"--stdio",
		"--enable", "realtime_conversation",
		"--disable", "apps",
		"--disable", "plugins",
	)
	b.cmd.Dir = b.workspace
	b.cmd.Env = voiceCodexEnvironment(os.Environ(), b.cfg.CodexHome)
	stdin, err := b.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open Codex app-server stdin: %w", err)
	}
	stdout, err := b.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open Codex app-server stdout: %w", err)
	}
	stderr, err := b.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open Codex app-server stderr: %w", err)
	}
	b.stdin = stdin
	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}
	go b.readStdout(stdout)
	go b.readStderr(stderr)
	go func() {
		_ = b.cmd.Wait()
		close(b.done)
	}()

	var initialized struct {
		UserAgent string `json:"userAgent"`
	}
	if err := b.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "greggpt-voice-prototype",
			"title":   "GregGPT Voice Prototype",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{"experimentalApi": true},
	}, &initialized); err != nil {
		return fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := b.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("acknowledge Codex app-server initialization: %w", err)
	}

	var loginResponse struct {
		Type string `json:"type"`
	}
	if err := b.call(ctx, "account/login/start", loginParams, &loginResponse); err != nil {
		return fmt.Errorf("authenticate Codex app-server with ChatGPT access token: %w", err)
	}
	if loginResponse.Type != "chatgptAuthTokens" {
		return fmt.Errorf("authenticate Codex app-server: unexpected login type %q", loginResponse.Type)
	}

	threadParams := map[string]any{
		"model":          b.agentCfg.Model,
		"cwd":            b.workspace,
		"ephemeral":      true,
		"sandbox":        "read-only",
		"approvalPolicy": "never",
	}
	var threadResponse struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := b.call(ctx, "thread/start", threadParams, &threadResponse); err != nil {
		return fmt.Errorf("start ephemeral Codex voice thread: %w", err)
	}
	if strings.TrimSpace(threadResponse.Thread.ID) == "" {
		return errors.New("Codex app-server returned an empty voice thread id")
	}
	b.threadID = threadResponse.Thread.ID

	offer, err := b.createOffer(ctx)
	if err != nil {
		return err
	}
	realtimeParams := map[string]any{
		"threadId":              b.threadID,
		"outputModality":        "audio",
		"version":               b.cfg.CodexProtocolVersion,
		"voice":                 b.cfg.RealtimeVoice,
		"transport":             map[string]any{"type": "webrtc", "sdp": offer},
		"clientManagedHandoffs": true,
		"codexResponsesAsItems": false,
		"includeStartupContext": false,
		"prompt":                "You are GregGPT's live speech interface. Listen continuously and transcribe each user utterance accurately. Never answer user questions yourself and remain silent after user speech. Only speak when the backend explicitly appends speech text; then say that text naturally and faithfully.",
	}
	if strings.TrimSpace(b.cfg.RealtimeModel) != "" {
		realtimeParams["model"] = b.cfg.RealtimeModel
	}
	if err := b.call(ctx, "thread/realtime/start", realtimeParams, nil); err != nil {
		return fmt.Errorf("request ChatGPT-authenticated WebRTC voice start: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for WebRTC voice connection: %w", ctx.Err())
		case <-b.done:
			return errors.New("Codex app-server exited before WebRTC voice connected")
		case event := <-b.events:
			switch event.Type {
			case RealtimeVoiceEventStarted:
				log.Printf("voice_backend_ready thread_id=%s app_server=%s transport=webrtc auth=chatgpt protocol=%s voice=%s", b.threadID, initialized.UserAgent, b.cfg.CodexProtocolVersion, b.cfg.RealtimeVoice)
				return nil
			case RealtimeVoiceEventError:
				return errors.New(event.Text)
			case RealtimeVoiceEventClosed:
				return fmt.Errorf("realtime voice closed during startup: %s", event.Text)
			}
		}
	}
}

func (b *codexRealtimeVoiceBridge) startWebRTC() error {
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return fmt.Errorf("create WebRTC peer: %w", err)
	}
	inputTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		"audio",
		"greggpt-discord",
	)
	if err != nil {
		_ = peer.Close()
		return fmt.Errorf("create WebRTC Opus track: %w", err)
	}
	sender, err := peer.AddTrack(inputTrack)
	if err != nil {
		_ = peer.Close()
		return fmt.Errorf("add WebRTC Opus track: %w", err)
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buffer); err != nil {
				return
			}
		}
	}()
	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		log.Printf("voice_webrtc_remote_track codec=%s clock_rate=%d channels=%d", track.Codec().MimeType, track.Codec().ClockRate, track.Codec().Channels)
		go b.readRemoteAudio(track)
	})
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("voice_webrtc_state state=%s", state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:
			b.readyMu.Lock()
			b.connected = true
			b.emitReadyLocked()
			b.readyMu.Unlock()
		case webrtc.PeerConnectionStateFailed:
			b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventError, Text: "WebRTC voice connection failed"})
		case webrtc.PeerConnectionStateClosed:
			if !b.closing.Load() {
				b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventClosed, Text: "WebRTC connection closed"})
			}
		}
	})
	b.peer = peer
	b.inputTrack = inputTrack
	return nil
}

func (b *codexRealtimeVoiceBridge) createOffer(ctx context.Context) (string, error) {
	offer, err := b.peer.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("create WebRTC offer: %w", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(b.peer)
	if err := b.peer.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set WebRTC local description: %w", err)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-gatherComplete:
	}
	local := b.peer.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		return "", errors.New("WebRTC produced an empty SDP offer")
	}
	return local.SDP, nil
}

func (b *codexRealtimeVoiceBridge) readRemoteAudio(track *webrtc.TrackRemote) {
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			if !b.closing.Load() {
				b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventError, Text: "read WebRTC audio: " + err.Error()})
			}
			return
		}
		if len(packet.Payload) > 0 {
			b.emit(RealtimeVoiceEvent{
				Type:  RealtimeVoiceEventAudio,
				Audio: RealtimeVoiceAudioChunk{Data: append([]byte(nil), packet.Payload...)},
			})
		}
	}
}

func (b *codexRealtimeVoiceBridge) emitReadyLocked() {
	if b.appStarted && b.connected && !b.readySent {
		b.readySent = true
		b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventStarted})
	}
}

func voiceCodexEnvironment(base []string, codexHome string) []string {
	// The parent service holds Discord and other integration credentials. Codex
	// does not need them for the ChatGPT-authenticated realtime transport, and a
	// read-only filesystem sandbox does not prevent a child from reading its own
	// environment. Pass only the small set of process/runtime settings Codex
	// needs instead of trying to maintain a denylist of present and future secrets.
	allowed := map[string]struct{}{
		"HOME": {}, "USER": {}, "LOGNAME": {}, "SHELL": {}, "PATH": {},
		"TMPDIR": {}, "TMP": {}, "TEMP": {},
		"LANG": {}, "LANGUAGE": {}, "LC_ALL": {}, "TZ": {},
		"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {},
	}
	out := make([]string, 0, len(base)+1)
	for _, item := range base {
		name := item
		if idx := strings.IndexByte(item, '='); idx >= 0 {
			name = item[:idx]
		}
		if _, ok := allowed[name]; ok {
			out = append(out, item)
		}
	}
	return append(out, "CODEX_HOME="+codexHome)
}

func (b *codexRealtimeVoiceBridge) AppendOpus(ctx context.Context, opus []byte, duration time.Duration) error {
	if len(opus) == 0 {
		return nil
	}
	if duration <= 0 {
		duration = discordOpusFrameDuration
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if b.inputTrack == nil {
		return errors.New("WebRTC input track is unavailable")
	}
	return b.inputTrack.WriteSample(media.Sample{Data: append([]byte(nil), opus...), Duration: duration})
}

func (b *codexRealtimeVoiceBridge) AppendSpeech(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return b.call(ctx, "thread/realtime/appendSpeech", map[string]any{
		"threadId": b.threadID,
		"text":     text,
	}, nil)
}

func (b *codexRealtimeVoiceBridge) Events() <-chan RealtimeVoiceEvent {
	return b.events
}

func (b *codexRealtimeVoiceBridge) call(ctx context.Context, method string, params any, out any) error {
	id := b.nextID.Add(1)
	responseCh := make(chan codexRPCResponse, 1)
	b.mu.Lock()
	b.pending[id] = responseCh
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
	}()
	if err := b.write(map[string]any{"method": method, "id": id, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return errors.New("Codex app-server stopped")
	case response := <-responseCh:
		if response.Error != nil {
			return fmt.Errorf("app-server error %d: %s", response.Error.Code, response.Error.Message)
		}
		if out != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, out); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	}
}

func (b *codexRealtimeVoiceBridge) notify(method string, params any) error {
	return b.write(map[string]any{"method": method, "params": params})
}

func (b *codexRealtimeVoiceBridge) write(value any) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.stdin == nil {
		return errors.New("Codex app-server stdin is unavailable")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = b.stdin.Write(data)
	return err
}

func (b *codexRealtimeVoiceBridge) readStdout(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var envelope codexRPCEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			log.Printf("voice_app_server_protocol_error err=%q", err.Error())
			continue
		}
		if len(envelope.ID) > 0 && string(envelope.ID) != "null" {
			var id int64
			if err := json.Unmarshal(envelope.ID, &id); err == nil {
				b.mu.Lock()
				pending := b.pending[id]
				b.mu.Unlock()
				if pending != nil {
					pending <- codexRPCResponse{Result: envelope.Result, Error: envelope.Error}
				}
			}
			continue
		}
		if envelope.Method != "" {
			b.handleNotification(envelope.Method, envelope.Params)
		}
	}
	if err := scanner.Err(); err != nil && !b.closing.Load() {
		b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventError, Text: "read Codex app-server output: " + err.Error()})
	}
}

func (b *codexRealtimeVoiceBridge) readStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			log.Printf("voice_app_server: %s", line)
		}
	}
}

func (b *codexRealtimeVoiceBridge) handleNotification(method string, raw json.RawMessage) {
	switch method {
	case "thread/realtime/started":
		b.readyMu.Lock()
		b.appStarted = true
		b.emitReadyLocked()
		b.readyMu.Unlock()
	case "thread/realtime/sdp":
		var params struct {
			SDP string `json:"sdp"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventError, Text: "decode WebRTC answer: " + err.Error()})
			return
		}
		if err := b.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: params.SDP}); err != nil {
			b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventError, Text: "set WebRTC answer: " + err.Error()})
		}
	case "thread/realtime/transcript/done":
		var params struct {
			Role string `json:"role"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &params) == nil {
			b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventTranscriptDone, Role: params.Role, Text: params.Text})
		}
	case "thread/realtime/error":
		var params struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &params)
		b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventError, Text: params.Message})
	case "thread/realtime/closed":
		var params struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &params)
		b.emit(RealtimeVoiceEvent{Type: RealtimeVoiceEventClosed, Text: params.Reason})
	}
}

func (b *codexRealtimeVoiceBridge) emit(event RealtimeVoiceEvent) {
	if b.closing.Load() {
		return
	}
	select {
	case b.events <- event:
	default:
		log.Printf("voice_backend_event_dropped type=%s", event.Type)
	}
}

func (b *codexRealtimeVoiceBridge) Close() {
	b.closeOnce.Do(func() {
		b.closing.Store(true)
		if b.threadID != "" && b.stdin != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = b.call(ctx, "thread/realtime/stop", map[string]any{"threadId": b.threadID}, nil)
			cancel()
		}
		if b.peer != nil {
			_ = b.peer.Close()
		}
		if b.stdin != nil {
			_ = b.stdin.Close()
		}
		if b.cmd != nil && b.cmd.Process != nil {
			select {
			case <-b.done:
			case <-time.After(2 * time.Second):
				_ = b.cmd.Process.Kill()
				<-b.done
			}
		}
	})
}
