package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"greggpt-gtnh/internal/agent"

	"github.com/bwmarrin/discordgo"
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/gateway"
	disvoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"
	"github.com/disgoorg/snowflake/v2"
	davesession "github.com/thomas-vilte/dave-go/session"
)

type VoiceConfig struct {
	Enabled              bool
	CodexBin             string
	CodexHome            string
	AuthFile             string
	RealtimeModel        string
	RealtimeVoice        string
	SilenceTimeout       time.Duration
	TranscriptTimeout    time.Duration
	StartupTimeout       time.Duration
	MaxUtterance         time.Duration
	OutputSilence        time.Duration
	CodexProtocolVersion string
}

func loadVoiceConfig(authFile string) VoiceConfig {
	codexHome := filepath.Join(filepath.Dir(authFile), "voice-codex")
	return VoiceConfig{
		Enabled:              getenvBool("GREGGPT_VOICE_ENABLED", false),
		CodexBin:             getenv("GREGGPT_VOICE_CODEX_BIN", "codex"),
		CodexHome:            getenv("GREGGPT_VOICE_CODEX_HOME", codexHome),
		AuthFile:             authFile,
		RealtimeModel:        strings.TrimSpace(os.Getenv("GREGGPT_VOICE_REALTIME_MODEL")),
		RealtimeVoice:        getenv("GREGGPT_VOICE_REALTIME_VOICE", "cove"),
		SilenceTimeout:       time.Duration(getenvInt("GREGGPT_VOICE_SILENCE_MILLISECONDS", 800)) * time.Millisecond,
		TranscriptTimeout:    time.Duration(getenvInt("GREGGPT_VOICE_TRANSCRIPT_TIMEOUT_SECONDS", 20)) * time.Second,
		StartupTimeout:       time.Duration(getenvInt("GREGGPT_VOICE_STARTUP_TIMEOUT_SECONDS", 30)) * time.Second,
		MaxUtterance:         time.Duration(getenvInt("GREGGPT_VOICE_MAX_UTTERANCE_SECONDS", 30)) * time.Second,
		OutputSilence:        time.Duration(getenvInt("GREGGPT_VOICE_OUTPUT_SILENCE_MILLISECONDS", 500)) * time.Millisecond,
		CodexProtocolVersion: "v3",
	}
}

type VoiceJoinRequest struct {
	GuildID        string
	VoiceChannelID string
	TextChannelID  string
	UserID         string
	Username       string
}

type DiscordVoiceController interface {
	Join(context.Context, VoiceJoinRequest) (string, error)
	Leave(context.Context, string) (string, error)
	Status(context.Context, string) string
	Close()
}

type discordVoiceManager struct {
	cfg     Config
	session *discordgo.Session
	runner  DiscordAgentRunner
	dave    *bot.Client

	opMu   sync.Mutex
	mu     sync.Mutex
	active *discordVoiceSession

	daveSessionCh chan *davesession.Session
}

const daveJoinAttempts = 2

func newDiscordVoiceManager(cfg Config, session *discordgo.Session, runner DiscordAgentRunner) DiscordVoiceController {
	return &discordVoiceManager{
		cfg:           cfg,
		session:       session,
		runner:        runner,
		daveSessionCh: make(chan *davesession.Session, 1),
	}
}

func (m *discordVoiceManager) daveClient(ctx context.Context) (*bot.Client, error) {
	if m.dave != nil {
		return m.dave, nil
	}
	client, err := disgo.New(m.cfg.Token,
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuildVoiceStates)),
		bot.WithVoiceManagerConfigOpts(
			disvoice.WithDaveSessionCreateFunc(davesession.CreateFunc(
				davesession.WithSessionHook(func(session *davesession.Session) {
					select {
					case m.daveSessionCh <- session:
					default:
						log.Printf("voice_dave_session_error err=%q", "session handoff queue is full")
					}
				}),
			)),
			disvoice.WithConnConfigOpts(
				disvoice.WithUDPConnCreateFunc(newTrackedVoiceUDPConn),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create DAVE Discord client: %w", err)
	}
	if err := client.OpenGateway(ctx); err != nil {
		client.Close(context.Background())
		return nil, fmt.Errorf("open DAVE Discord gateway: %w", err)
	}
	m.dave = client
	return client, nil
}

type trackedVoicePacketError struct {
	UserID snowflake.ID
	SSRC   uint32
	Err    error
}

func (e *trackedVoicePacketError) Error() string { return e.Err.Error() }
func (e *trackedVoicePacketError) Unwrap() error { return e.Err }

type trackedVoiceUDPConn struct {
	disvoice.UDPConn
	mu       sync.Mutex
	lastSSRC uint32
	lastUser snowflake.ID
}

func newTrackedVoiceUDPConn(daveSession godave.Session, lookup disvoice.SsrcLookupFunc, opts ...disvoice.UDPConnConfigOpt) disvoice.UDPConn {
	tracked := &trackedVoiceUDPConn{}
	tracked.UDPConn = disvoice.NewUDPConn(daveSession, func(ssrc uint32) snowflake.ID {
		userID := lookup(ssrc)
		tracked.mu.Lock()
		tracked.lastSSRC = ssrc
		tracked.lastUser = userID
		tracked.mu.Unlock()
		return userID
	}, opts...)
	return tracked
}

func (c *trackedVoiceUDPConn) ReadPacket() (*disvoice.Packet, error) {
	c.mu.Lock()
	c.lastSSRC = 0
	c.lastUser = 0
	c.mu.Unlock()
	packet, err := c.UDPConn.ReadPacket()
	if err == nil {
		return packet, nil
	}
	c.mu.Lock()
	packetErr := &trackedVoicePacketError{UserID: c.lastUser, SSRC: c.lastSSRC, Err: err}
	c.mu.Unlock()
	return nil, packetErr
}

func daveJoinAttemptTimeout(total time.Duration) time.Duration {
	if total <= 0 {
		total = 30 * time.Second
	}
	perAttempt := total / daveJoinAttempts
	if perAttempt < 5*time.Second {
		perAttempt = 5 * time.Second
	}
	return perAttempt
}

func (m *discordVoiceManager) drainDaveSessions() {
	for {
		select {
		case stale := <-m.daveSessionCh:
			_ = stale.Close()
		default:
			return
		}
	}
}

func (m *discordVoiceManager) resetDaveClient() {
	if m.dave == nil {
		return
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	m.dave.Close(closeCtx)
	closeCancel()
	m.dave = nil
	m.drainDaveSessions()
}

func closeDaveVoiceConnection(vc disvoice.Conn, daveSession *davesession.Session) {
	if vc != nil {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		vc.Close(closeCtx)
		closeCancel()
	}
	if daveSession != nil {
		_ = daveSession.Close()
	}
}

func (m *discordVoiceManager) connectDaveVoice(ctx context.Context, client *bot.Client, guildID, voiceChannelID snowflake.ID, req VoiceJoinRequest) (disvoice.Conn, *davesession.Session, error) {
	attemptTimeout := daveJoinAttemptTimeout(m.cfg.Voice.StartupTimeout)
	var lastErr error
	for attempt := 1; attempt <= daveJoinAttempts; attempt++ {
		if existing := client.VoiceManager.GetConn(guildID); existing != nil {
			closeDaveVoiceConnection(existing, nil)
		}
		m.drainDaveSessions()

		vc := client.VoiceManager.CreateConn(guildID)
		var daveSession *davesession.Session
		select {
		case daveSession = <-m.daveSessionCh:
		default:
			closeDaveVoiceConnection(vc, nil)
			lastErr = errors.New("capture Discord DAVE session: no session was created")
			continue
		}

		attemptCtx, attemptCancel := context.WithTimeout(ctx, attemptTimeout)
		openErr := vc.Open(attemptCtx, voiceChannelID, false, false)
		attemptCancel()
		if openErr == nil {
			state := daveSession.State()
			if daveSession.ShouldHoldFrames() {
				log.Printf("voice_dave_pending guild_id=%s voice_channel_id=%s protocol=%d epoch=%d attempt=%d", req.GuildID, req.VoiceChannelID, state.ProtocolVersion, state.EpochID, attempt)
			} else {
				log.Printf("voice_dave_ready guild_id=%s voice_channel_id=%s protocol=%d epoch=%d wait_ms=0 attempt=%d", req.GuildID, req.VoiceChannelID, state.ProtocolVersion, state.EpochID, attempt)
			}
			return vc, daveSession, nil
		}

		state := daveSession.State()
		stats := daveSession.Stats()
		lastErr = openErr
		closeDaveVoiceConnection(vc, daveSession)
		log.Printf("voice_dave_connect_failed guild_id=%s voice_channel_id=%s attempt=%d max_attempts=%d timeout_ms=%d ready=%t protocol=%d epoch=%d recovery_attempts=%d transport_recovery_attempts=%d commits_failed=%d welcomes_failed=%d err=%q", req.GuildID, req.VoiceChannelID, attempt, daveJoinAttempts, attemptTimeout.Milliseconds(), state.Ready, state.ProtocolVersion, state.EpochID, stats.RecoveryAttempts, stats.RecoveryAttemptsTransport, stats.CommitsFailed, stats.WelcomesFailed, openErr.Error())
		if attempt < daveJoinAttempts {
			m.resetDaveClient()
			client, lastErr = m.daveClient(ctx)
			if lastErr != nil {
				break
			}
		}
	}
	return nil, nil, fmt.Errorf("join Discord DAVE voice after %d attempts: %w", daveJoinAttempts, lastErr)
}

func (m *discordVoiceManager) Join(ctx context.Context, req VoiceJoinRequest) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	if !m.cfg.Voice.Enabled {
		return "", errors.New("voice prototype is disabled; set GREGGPT_VOICE_ENABLED=true to make /voice join available")
	}
	if strings.TrimSpace(req.GuildID) == "" {
		return "", errors.New("voice commands must be used in a Discord server")
	}
	if strings.TrimSpace(req.VoiceChannelID) == "" {
		return "", errors.New("a voice channel is required")
	}
	if _, err := exec.LookPath(m.cfg.Voice.CodexBin); err != nil {
		return "", fmt.Errorf("find Codex CLI %q: %w", m.cfg.Voice.CodexBin, err)
	}
	if err := validateChatGPTVoiceAuthFile(m.cfg.Voice.AuthFile); err != nil {
		return "", err
	}

	channel, err := m.session.Channel(req.VoiceChannelID)
	if err != nil {
		var restErr *discordgo.RESTError
		if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == 50001 {
			return "", fmt.Errorf("GregGPT cannot access <#%s>; grant Greg's Kitten View Channel, Connect, and Speak on that voice channel or its parent category", req.VoiceChannelID)
		}
		return "", fmt.Errorf("resolve Discord voice channel: %w", err)
	}
	if channel.GuildID != req.GuildID {
		return "", errors.New("selected voice channel is not in this server")
	}
	if channel.Type == discordgo.ChannelTypeGuildStageVoice {
		return "", errors.New("Stage channels are not supported by this voice prototype; select a normal voice channel")
	}
	if channel.Type != discordgo.ChannelTypeGuildVoice {
		return "", errors.New("selected channel is not a voice channel")
	}
	m.resetDaveClient()

	m.mu.Lock()
	old := m.active
	m.active = nil
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}

	voiceCtx, cancel := context.WithCancel(context.Background())
	bridge, err := startCodexRealtimeVoiceBridge(ctx, m.cfg.Voice, m.cfg.Agent.Config, m.cfg.Workspace)
	if err != nil {
		cancel()
		return "", fmt.Errorf("start realtime voice backend: %w", err)
	}

	guildID, err := snowflake.Parse(req.GuildID)
	if err != nil {
		cancel()
		bridge.Close()
		return "", fmt.Errorf("parse Discord guild id: %w", err)
	}
	voiceChannelID, err := snowflake.Parse(req.VoiceChannelID)
	if err != nil {
		cancel()
		bridge.Close()
		return "", fmt.Errorf("parse Discord voice channel id: %w", err)
	}
	client, err := m.daveClient(ctx)
	if err != nil {
		cancel()
		bridge.Close()
		return "", err
	}
	vc, daveSession, err := m.connectDaveVoice(ctx, client, guildID, voiceChannelID, req)
	if err != nil {
		cancel()
		bridge.Close()
		return "", err
	}
	voiceSession := newDiscordVoiceSession(voiceCtx, cancel, m.cfg, m.session, vc, daveSession, bridge, m.runner, req)
	voiceSession.Prepare()
	voiceSession.onTerminal = m.onSessionTerminal
	m.mu.Lock()
	m.active = voiceSession
	m.mu.Unlock()
	voiceSession.Start()

	log.Printf("voice_session_started guild_id=%s voice_channel_id=%s text_channel_id=%s user_id=%s", req.GuildID, req.VoiceChannelID, req.TextChannelID, req.UserID)
	return fmt.Sprintf("GregGPT live voice joined <#%s>. Use /voice leave to stop it.", req.VoiceChannelID), nil
}
func (m *discordVoiceManager) onSessionTerminal(session *discordVoiceSession) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	if m.active != session {
		m.mu.Unlock()
		return
	}
	m.active = nil
	m.mu.Unlock()
	session.Close()
	m.resetDaveClient()
	log.Printf("voice_session_terminated guild_id=%s voice_channel_id=%s", session.guildID, session.voiceChannelID)
}

func (m *discordVoiceManager) Leave(_ context.Context, guildID string) (string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	active := m.active
	if active != nil && guildID != "" && active.guildID != guildID {
		active = nil
	} else {
		m.active = nil
	}
	m.mu.Unlock()
	if active == nil {
		return "GregGPT live voice is already inactive.", nil
	}
	channelID := active.voiceChannelID
	active.Close()
	m.resetDaveClient()
	log.Printf("voice_session_stopped guild_id=%s voice_channel_id=%s", guildID, channelID)
	return fmt.Sprintf("GregGPT live voice left <#%s> and is disabled.", channelID), nil
}

func (m *discordVoiceManager) Status(_ context.Context, guildID string) string {
	m.mu.Lock()
	active := m.active
	m.mu.Unlock()
	if !m.cfg.Voice.Enabled {
		return "GregGPT live voice capability is disabled (GREGGPT_VOICE_ENABLED=false)."
	}
	if err := validateChatGPTVoiceAuthFile(m.cfg.Voice.AuthFile); err != nil {
		return "GregGPT live voice capability is enabled but unavailable: " + err.Error()
	}
	if active == nil || (guildID != "" && active.guildID != guildID) {
		return fmt.Sprintf("GregGPT live voice is ready but inactive (protocol=%s, voice=%s).", m.cfg.Voice.CodexProtocolVersion, m.cfg.Voice.RealtimeVoice)
	}
	status := fmt.Sprintf("GregGPT live voice is active in <#%s> (protocol=%s, voice=%s).", active.voiceChannelID, m.cfg.Voice.CodexProtocolVersion, m.cfg.Voice.RealtimeVoice)
	if lastErr := active.LastError(); lastErr != "" {
		status += " Last error: " + lastErr
	}
	return status
}

func (m *discordVoiceManager) Close() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	active := m.active
	m.active = nil
	m.mu.Unlock()
	if active != nil {
		active.Close()
	}
	m.resetDaveClient()
}

type voiceUtterance struct {
	userID   string
	username string
}

type voiceTurn struct {
	utterance  voiceUtterance
	transcript string
	queuedAt   time.Time
}

type bufferedVoiceUtterance struct {
	userID   string
	username string
	frames   int
	timer    *time.Timer
}

type discordVoiceSession struct {
	ctx               context.Context
	cancel            context.CancelFunc
	cfg               Config
	session           *discordgo.Session
	vc                disvoice.Conn
	dave              *davesession.Session
	bridge            RealtimeVoiceBridge
	runner            DiscordAgentRunner
	guildID           string
	voiceChannelID    string
	textChannelID     string
	startedByUserID   string
	startedByUsername string
	player            *discordVoicePlayer

	utteranceCh  chan voiceUtterance
	transcriptCh chan string
	turnCh       chan voiceTurn

	utterMu sync.Mutex
	utter   map[string]*bufferedVoiceUtterance

	errMu      sync.Mutex
	lastErr    string
	closeOnce  sync.Once
	wg         sync.WaitGroup
	onTerminal func(*discordVoiceSession)
}

func newDiscordVoiceSession(ctx context.Context, cancel context.CancelFunc, cfg Config, session *discordgo.Session, vc disvoice.Conn, dave *davesession.Session, bridge RealtimeVoiceBridge, runner DiscordAgentRunner, req VoiceJoinRequest) *discordVoiceSession {
	return &discordVoiceSession{
		ctx:               ctx,
		cancel:            cancel,
		cfg:               cfg,
		session:           session,
		vc:                vc,
		dave:              dave,
		bridge:            bridge,
		runner:            runner,
		guildID:           req.GuildID,
		voiceChannelID:    req.VoiceChannelID,
		textChannelID:     req.TextChannelID,
		startedByUserID:   req.UserID,
		startedByUsername: req.Username,
		player:            newDiscordVoicePlayer(ctx, cfg.Voice, vc),
		utteranceCh:       make(chan voiceUtterance, 16),
		transcriptCh:      make(chan string, 16),
		turnCh:            make(chan voiceTurn, 1),
		utter:             make(map[string]*bufferedVoiceUtterance),
	}
}

func (s *discordVoiceSession) Prepare() {
	s.vc.SetOpusFrameProvider(s.player)
}

func (s *discordVoiceSession) Start() {
	s.wg.Add(4)
	go func() {
		defer s.wg.Done()
		s.processDiscordAudio()
	}()
	go func() {
		defer s.wg.Done()
		s.processUtterances()
	}()
	go func() {
		defer s.wg.Done()
		s.processVoiceTurns()
	}()
	go func() {
		defer s.wg.Done()
		s.processRealtimeEvents()
	}()
}

func (s *discordVoiceSession) processDiscordAudio() {
	failures := 0
	successes := 0
	lastHealthLog := time.Now()
	var lastFailureUser snowflake.ID
	var lastFailureSSRC uint32
	var lastFailureErr error
	for {
		if s.dave != nil && s.dave.ShouldHoldFrames() {
			waited, err := s.dave.WaitReady(s.ctx)
			if err != nil {
				return
			}
			state := s.dave.State()
			log.Printf("voice_dave_recovered guild_id=%s voice_channel_id=%s protocol=%d epoch=%d wait_ms=%d", s.guildID, s.voiceChannelID, state.ProtocolVersion, state.EpochID, waited.Milliseconds())
		}

		packet, err := s.vc.UDP().ReadPacket()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
				return
			}
			var packetErr *trackedVoicePacketError
			if errors.As(err, &packetErr) {
				if packetErr.UserID != 0 && !s.allowedUser(packetErr.UserID.String()) {
					continue
				}
				lastFailureUser = packetErr.UserID
				lastFailureSSRC = packetErr.SSRC
			}
			failures++
			lastFailureErr = err
			if time.Since(lastHealthLog) >= 10*time.Second {
				log.Printf("voice_audio_receive_health guild_id=%s voice_channel_id=%s failures=%d successes=%d last_user_id=%s last_ssrc=%d err=%q", s.guildID, s.voiceChannelID, failures, successes, lastFailureUser.String(), lastFailureSSRC, lastFailureErr.Error())
				failures = 0
				successes = 0
				lastHealthLog = time.Now()
			}
			continue
		}
		successes++
		userID := s.vc.UserIDBySSRC(packet.SSRC)
		if userID == 0 || len(packet.Opus) == 0 {
			continue
		}
		s.bufferOpusPacket(userID.String(), packet.Opus)
	}
}

func (s *discordVoiceSession) bufferOpusPacket(userID string, opus []byte) {
	s.utterMu.Lock()
	if !s.allowedUser(userID) {
		s.utterMu.Unlock()
		return
	}
	item := s.utter[userID]
	if item == nil {
		item = &bufferedVoiceUtterance{userID: userID, username: s.displayName(userID)}
		s.utter[userID] = item
	}
	item.frames++
	if item.timer != nil {
		item.timer.Stop()
	}
	item.timer = time.AfterFunc(s.cfg.Voice.SilenceTimeout, func() { s.flushUtterance(userID) })
	maxFrames := int(s.cfg.Voice.MaxUtterance / (20 * time.Millisecond))
	shouldFlush := maxFrames > 0 && item.frames >= maxFrames
	s.utterMu.Unlock()
	if err := s.bridge.AppendOpus(s.ctx, opus, discordOpusFrameDuration); err != nil {
		s.setError("send realtime Opus: " + err.Error())
	}
	if shouldFlush {
		s.flushUtterance(userID)
	}
}

func (s *discordVoiceSession) flushUtterance(userID string) {
	s.utterMu.Lock()
	item := s.utter[userID]
	delete(s.utter, userID)
	if item != nil && item.timer != nil {
		item.timer.Stop()
	}
	s.utterMu.Unlock()
	if item == nil || item.frames == 0 {
		return
	}
	utterance := voiceUtterance{userID: item.userID, username: item.username}
	select {
	case s.utteranceCh <- utterance:
	case <-s.ctx.Done():
	default:
		s.setError("voice utterance queue is full")
	}
}

func (s *discordVoiceSession) processUtterances() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case utterance := <-s.utteranceCh:
			silence := s.cfg.Voice.SilenceTimeout
			if silence <= 0 {
				silence = 800 * time.Millisecond
			}
			for elapsed := time.Duration(0); elapsed < silence; elapsed += discordOpusFrameDuration {
				if err := s.bridge.AppendOpus(s.ctx, discordOpusSilence, discordOpusFrameDuration); err != nil {
					s.setError("send realtime silence: " + err.Error())
					break
				}
				select {
				case <-s.ctx.Done():
					return
				case <-time.After(discordOpusFrameDuration):
				}
			}

			var transcript string
			select {
			case <-s.ctx.Done():
				return
			case transcript = <-s.transcriptCh:
			case <-time.After(s.cfg.Voice.TranscriptTimeout):
				s.setError("realtime transcript timed out")
				s.logDaveHealth("transcript_timeout")
				continue
			}
			transcript = strings.TrimSpace(transcript)
			if transcript == "" {
				continue
			}
			turn := voiceTurn{utterance: utterance, transcript: transcript, queuedAt: time.Now()}
			replaced := enqueueLatestVoiceTurn(s.turnCh, turn)
			log.Printf("voice_transcript_ready guild_id=%s voice_channel_id=%s user_id=%s transcript_len=%d replaced_pending=%t", s.guildID, s.voiceChannelID, utterance.userID, len(transcript), replaced)
		}
	}
}

func (s *discordVoiceSession) logDaveHealth(reason string) {
	if s.dave == nil {
		return
	}
	state := s.dave.State()
	stats := s.dave.Stats()
	log.Printf("voice_dave_health guild_id=%s voice_channel_id=%s reason=%s ready=%t protocol=%d epoch=%d decrypt_failures=%d replay_rejections=%d commits_failed=%d welcomes_failed=%d", s.guildID, s.voiceChannelID, reason, state.Ready, state.ProtocolVersion, state.EpochID, stats.DecryptFailures, stats.RejectedReplayFrames, stats.CommitsFailed, stats.WelcomesFailed)
}

func enqueueLatestVoiceTurn(ch chan voiceTurn, turn voiceTurn) bool {
	select {
	case ch <- turn:
		return false
	default:
	}

	replaced := false
	select {
	case <-ch:
		replaced = true
	default:
	}
	select {
	case ch <- turn:
	default:
	}
	return replaced
}

func (s *discordVoiceSession) processVoiceTurns() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case turn := <-s.turnCh:
			s.runGregGPTVoiceTurn(turn)
		}
	}
}

func (s *discordVoiceSession) runGregGPTVoiceTurn(turn voiceTurn) {
	startedAt := time.Now()
	queueDelay := startedAt.Sub(turn.queuedAt)
	log.Printf("voice_turn_start guild_id=%s voice_channel_id=%s user_id=%s transcript_len=%d queue_delay_ms=%d", s.guildID, s.voiceChannelID, turn.utterance.userID, len(turn.transcript), queueDelay.Milliseconds())
	timeout := s.cfg.Agent.Config.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	response, err := s.runner.Run(ctx, DiscordAgentRequest{
		Channel:          agent.ChannelDiscord,
		Text:             turn.transcript,
		UserID:           turn.utterance.userID,
		Username:         turn.utterance.username,
		DiscordChannelID: s.textChannelID,
		MessageID:        fmt.Sprintf("voice-%d", time.Now().UnixNano()),
	})
	if err != nil {
		s.setError("GregGPT voice turn: " + err.Error())
		return
	}
	spoken := voiceSpeakableText(response.Reply)
	if spoken == "" {
		return
	}
	if err := s.bridge.AppendSpeech(s.ctx, spoken); err != nil {
		s.setError("speak GregGPT response: " + err.Error())
		return
	}
	log.Printf("voice_turn_ok guild_id=%s voice_channel_id=%s user_id=%s transcript_len=%d reply_len=%d elapsed_ms=%d", s.guildID, s.voiceChannelID, turn.utterance.userID, len(turn.transcript), len(spoken), time.Since(startedAt).Milliseconds())
}

func (s *discordVoiceSession) processRealtimeEvents() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case event, ok := <-s.bridge.Events():
			if !ok {
				s.setError("realtime voice backend stopped")
				return
			}
			switch event.Type {
			case RealtimeVoiceEventTranscriptDone:
				if strings.EqualFold(event.Role, "user") && strings.TrimSpace(event.Text) != "" {
					select {
					case s.transcriptCh <- event.Text:
					case <-s.ctx.Done():
					default:
						s.setError("realtime transcript queue is full")
					}
				}
			case RealtimeVoiceEventAudio:
				if err := s.player.Write(event.Audio); err != nil {
					s.setError("play realtime audio: " + err.Error())
				}
			case RealtimeVoiceEventError:
				s.terminate(event.Text)
				return
			case RealtimeVoiceEventClosed:
				message := "realtime voice backend closed"
				if strings.TrimSpace(event.Text) != "" {
					message = "realtime voice closed: " + event.Text
				}
				s.terminate(message)
				return
			}
		}
	}
}
func (s *discordVoiceSession) terminate(message string) {
	s.setError(message)
	s.cancel()
	if s.onTerminal != nil {
		go s.onTerminal(s)
	}
}

func (s *discordVoiceSession) allowedUser(userID string) bool {
	if strings.TrimSpace(userID) == "" {
		return false
	}
	if len(s.cfg.AllowedUsers) == 0 {
		return s.cfg.AllowAllUsers
	}
	_, ok := s.cfg.AllowedUsers[userID]
	return ok
}

func (s *discordVoiceSession) displayName(userID string) string {
	if userID == s.startedByUserID && strings.TrimSpace(s.startedByUsername) != "" {
		return s.startedByUsername
	}
	if s.session.State != nil {
		if member, err := s.session.State.Member(s.guildID, userID); err == nil && member != nil && member.User != nil {
			if strings.TrimSpace(member.Nick) != "" {
				return member.Nick
			}
			if strings.TrimSpace(member.User.GlobalName) != "" {
				return member.User.GlobalName
			}
			return member.User.Username
		}
	}
	return userID
}

func (s *discordVoiceSession) setError(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	s.errMu.Lock()
	s.lastErr = message
	s.errMu.Unlock()
	log.Printf("voice_session_error guild_id=%s voice_channel_id=%s err=%q", s.guildID, s.voiceChannelID, message)
}

func (s *discordVoiceSession) LastError() string {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.lastErr
}

func (s *discordVoiceSession) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.utterMu.Lock()
		for _, item := range s.utter {
			if item.timer != nil {
				item.timer.Stop()
			}
		}
		s.utter = make(map[string]*bufferedVoiceUtterance)
		s.utterMu.Unlock()
		s.player.Close()
		s.bridge.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.vc.Close(ctx)
		cancel()
		if s.dave != nil {
			_ = s.dave.Close()
		}
		s.wg.Wait()
	})
}

var voiceMarkdownLinkRE = regexp.MustCompile(`\[([^\]]+)\]\([^\)]+\)`)
var voiceMarkdownDecorationRE = regexp.MustCompile(`(?m)(?:^|\s)[#>*_~]+`)

func voiceSpeakableText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "```", "")
	text = strings.ReplaceAll(text, "`", "")
	text = voiceMarkdownLinkRE.ReplaceAllString(text, "$1")
	text = voiceMarkdownDecorationRE.ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ")
	const maxRunes = 6000
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "…"
	}
	return text
}
