package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Config struct {
	Token          string
	Workspace      string
	ConfigPath     string
	GuildID        string
	CommandTimeout time.Duration
	ReplyLimit     int
	AllowedUsers   map[string]struct{}
}

type picoclawConfig struct {
	Channels struct {
		Discord struct {
			AllowFrom []string `json:"allow_from"`
		} `json:"discord"`
	} `json:"channels"`
}

type wikiSearchResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type wikiSearchResponse struct {
	OK        bool               `json:"ok"`
	Query     string             `json:"query"`
	TotalHits int                `json:"total_hits"`
	Results   []wikiSearchResult `json:"results"`
	Source    string             `json:"source"`
}

type wikiPageResponse struct {
	OK      bool   `json:"ok"`
	Query   string `json:"query"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary"`
	Source  string `json:"source"`
}

type Service struct {
	cfg Config
	s   *discordgo.Session
}

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

func loadConfig() (Config, error) {
	token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("PICOCLAW_CHANNELS_DISCORD_TOKEN"))
	}
	if token == "" {
		return Config{}, errors.New("missing Discord bot token; set DISCORD_BOT_TOKEN or PICOCLAW_CHANNELS_DISCORD_TOKEN")
	}

	cfg := Config{
		Token:          token,
		Workspace:      getenv("DISCORD_WORKSPACE", "/root/.picoclaw/workspace"),
		ConfigPath:     getenv("DISCORD_CONFIG_PATH", "/root/.picoclaw/config.json"),
		GuildID:        strings.TrimSpace(os.Getenv("DISCORD_GUILD_ID")),
		CommandTimeout: time.Duration(getenvInt("DISCORD_COMMAND_TIMEOUT_SECONDS", 45)) * time.Second,
		ReplyLimit:     getenvInt("DISCORD_REPLY_LIMIT", 1900),
		AllowedUsers:   map[string]struct{}{},
	}
	if cfg.ReplyLimit < 200 {
		cfg.ReplyLimit = 200
	}
	allowed, err := loadAllowedUsers(cfg.ConfigPath)
	if err != nil {
		return Config{}, err
	}
	cfg.AllowedUsers = allowed
	return cfg, nil
}

func loadAllowedUsers(path string) (map[string]struct{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read discord config: %w", err)
	}
	var cfg picoclawConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse discord config: %w", err)
	}
	out := map[string]struct{}{}
	for _, id := range cfg.Channels.Discord.AllowFrom {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("startup_error: %v", err)
	}

	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		log.Fatalf("discord_session_error: %v", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds

	svc := &Service{cfg: cfg, s: session}
	session.AddHandler(svc.onInteractionCreate)

	ready := make(chan string, 1)
	session.AddHandlerOnce(func(s *discordgo.Session, r *discordgo.Ready) {
		if s.State != nil && s.State.User != nil {
			ready <- s.State.User.ID
			return
		}
		ready <- ""
	})

	if err := session.Open(); err != nil {
		log.Fatalf("discord_open_error: %v", err)
	}
	defer session.Close()

	appID := <-ready
	if appID == "" {
		log.Fatal("discord_ready_error: missing application id")
	}

	if err := svc.registerCommands(appID); err != nil {
		log.Fatalf("command_registration_error: %v", err)
	}

	log.Printf("slash commands ready app_id=%s guild_id=%s workspace=%s", appID, cfg.GuildID, cfg.Workspace)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
}

func (s *Service) registerCommands(appID string) error {
	cmds := []*discordgo.ApplicationCommand{
		tasksCommand(),
		inventoryCommand(),
		queryCommand(),
		minecraftCommand(),
		checkinCommand(),
	}
	if s.cfg.GuildID != "" {
		_, err := s.s.ApplicationCommandBulkOverwrite(appID, s.cfg.GuildID, cmds)
		return err
	}
	_, err := s.s.ApplicationCommandBulkOverwrite(appID, "", cmds)
	return err
}

func tasksCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "tasks",
		Description: "GTNH task board actions",
		Options: []*discordgo.ApplicationCommandOption{
			subcommand("board", "Show the board view"),
			subcommand("board_code", "Show the board view in a Discord code block"),
			subcommand("board_json", "Show the board JSON"),
			subcommand("in_progress_json", "Show the in-progress JSON"),
			subcommand("list", "List tasks",
				stringChoice("scope", "Filter", false, "all", "open", "done"),
				stringOption("area", "Area filter", false),
			),
			subcommand("add", "Add a task",
				stringOption("title", "Task title", true),
				stringChoice("priority", "Priority", false, "low", "med", "high"),
				stringOption("area", "Area", false),
				stringChoice("status", "Initial status", false, "todo", "doing", "paused", "done"),
				stringOption("owners", "Comma-separated owners", false),
				stringOption("paused_reason", "Paused reason", false),
				stringOption("description", "Description", false),
			),
			subcommand("move", "Move a task",
				integerOption("id", "Task id", true),
				stringChoice("status", "Status", true, "todo", "doing", "paused", "done"),
				stringOption("owners", "Comma-separated owners", false),
				stringOption("reason", "Reason", false),
			),
			subcommand("assign", "Add owners to a task",
				integerOption("id", "Task id", true),
				stringOption("owners", "Comma-separated owners", true),
			),
			subcommand("unassign", "Remove owners from a task",
				integerOption("id", "Task id", true),
				stringOption("owners", "Comma-separated owners", true),
			),
			subcommand("reassign", "Replace the owner list",
				integerOption("id", "Task id", true),
				stringOption("owners", "Comma-separated owners", true),
			),
			subcommand("pause", "Pause a task",
				integerOption("id", "Task id", true),
				stringOption("reason", "Pause reason", true),
			),
			subcommand("unpause", "Unpause a task",
				integerOption("id", "Task id", true),
			),
			subcommand("describe", "Set task description",
				integerOption("id", "Task id", true),
				stringOption("description", "Description", true),
			),
			subcommand("status_update", "Add a status update",
				integerOption("id", "Task id", true),
				stringOption("update", "Update text", true),
			),
			subcommand("status_history", "Show status history",
				integerOption("id", "Task id", true),
			),
			subcommand("done", "Mark task done",
				integerOption("id", "Task id", true),
			),
			subcommand("reopen", "Reopen task",
				integerOption("id", "Task id", true),
			),
			subcommand("show", "Show task details",
				integerOption("id", "Task id", true),
			),
			subcommand("summary", "Show task summary"),
		},
	}
}

func inventoryCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "inventory",
		Description: "GTNH inventory and chest lookup",
		Options: []*discordgo.ApplicationCommandOption{
			subcommand("status", "Show inventory index status"),
			subcommand("find", "Find an exact item",
				stringOption("item", "Exact mod:name[:damage] or mod:name", true),
				stringChoice("scope", "Scope", false, "players", "chests", "both"),
				integerOption("limit", "Result limit", false),
				stringOption("player", "Filter by player", false),
				boolOption("any_damage", "Allow any damage value"),
			),
			subcommand("find_item", "Find an item by natural language",
				stringOption("query", "Item query", true),
				stringChoice("scope", "Scope", false, "players", "chests", "both"),
				integerOption("limit", "Result limit", false),
				boolOption("oredict", "Use ore dictionary"),
			),
			subcommand("player", "Show a player inventory",
				stringOption("name", "Player name", false),
				stringOption("uuid", "Player UUID", false),
				boolOption("all", "Show nested contents"),
			),
			subcommand("chest", "Show a chest or container",
				integerOption("x", "X coordinate", true),
				integerOption("y", "Y coordinate", true),
				integerOption("z", "Z coordinate", true),
				integerOption("dim", "Dimension", false),
			),
			subcommand("refresh", "Refresh inventory index",
				stringChoice("scope", "Scope", false, "players", "chests", "all"),
			),
		},
	}
}

func queryCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "query",
		Description: "GTNH item, recipe, and wiki lookup",
		Options: []*discordgo.ApplicationCommandOption{
			subcommand("find_item", "Search for a matching item",
				stringOption("query", "Search text", true),
				boolOption("oredict", "Use ore dictionary"),
			),
			subcommand("item", "Show an exact item",
				stringOption("slug", "Exact item slug", true),
			),
			subcommand("resolve_recipes", "Resolve a recipe chain",
				stringOption("item", "Item name", true),
			),
			subcommand("search_recipes", "Search recipes",
				stringOption("query", "Recipe query", true),
			),
			subcommand("wiki_search", "Search the GTNH wiki",
				stringOption("query", "Search text", true),
			),
			subcommand("wiki_page", "Show a GTNH wiki page",
				stringOption("title", "Page title", true),
			),
		},
	}
}

func minecraftCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "minecraft",
		Description: "Minecraft chat helpers",
		Options: []*discordgo.ApplicationCommandOption{
			subcommand("online", "Show online players",
				integerOption("lines", "Lines to inspect", false),
			),
			subcommand("poll", "Poll Minecraft chat",
				integerOption("lines", "Lines to inspect", false),
			),
			subcommand("say", "Send a Minecraft chat message",
				stringOption("text", "Message text", true),
			),
		},
	}
}

func checkinCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "checkin",
		Description: "GTNH task check-in helpers",
		Options: []*discordgo.ApplicationCommandOption{
			subcommand("check", "Run the pending check-in"),
			subcommand("mark_sent", "Mark the reminder as sent"),
		},
	}
}

func subcommand(name, desc string, opts ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        name,
		Description: desc,
		Options:     opts,
	}
}

func stringOption(name, desc string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        name,
		Description: desc,
		Required:    required,
	}
}

func stringChoice(name, desc string, required bool, values ...string) *discordgo.ApplicationCommandOption {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(values))
	for _, v := range values {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: v, Value: v})
	}
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        name,
		Description: desc,
		Required:    required,
		Choices:     choices,
	}
}

func integerOption(name, desc string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionInteger,
		Name:        name,
		Description: desc,
		Required:    required,
	}
}

func boolOption(name, desc string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionBoolean,
		Name:        name,
		Description: desc,
		Required:    false,
	}
}

func (s *Service) onInteractionCreate(dg *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if !s.allowedUser(i) {
		_ = dg.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "not allowed",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.CommandTimeout)
	defer cancel()

	_ = dg.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	content, err := s.dispatch(ctx, i.ApplicationCommandData())
	if err != nil {
		if content != "" {
			content = content + "\n\nerror: " + err.Error()
		} else {
			content = "error: " + err.Error()
		}
	}
	content = truncate(content, s.cfg.ReplyLimit)
	content = wrapCodeBlock(content)

	_, _ = dg.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
}

func (s *Service) allowedUser(i *discordgo.InteractionCreate) bool {
	if len(s.cfg.AllowedUsers) == 0 {
		return true
	}
	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}
	_, ok := s.cfg.AllowedUsers[userID]
	return ok
}

func (s *Service) dispatch(ctx context.Context, data discordgo.ApplicationCommandInteractionData) (string, error) {
	switch data.Name {
	case "tasks":
		return s.dispatchTasks(ctx, data.Options)
	case "inventory":
		return s.dispatchInventory(ctx, data.Options)
	case "query":
		return s.dispatchQuery(ctx, data.Options)
	case "minecraft":
		return s.dispatchMinecraft(ctx, data.Options)
	case "checkin":
		return s.dispatchCheckin(ctx, data.Options)
	default:
		return "", fmt.Errorf("unknown command %q", data.Name)
	}
}

func (s *Service) dispatchTasks(ctx context.Context, opts []*discordgo.ApplicationCommandInteractionDataOption) (string, error) {
	sub, subOpts := firstSubcommand(opts)
	switch sub {
	case "board":
		return s.run(ctx, "sh", "gtnh_tasks", "board")
	case "board_code":
		return s.run(ctx, "sh", "gtnh_tasks", "board-code")
	case "board_json":
		return s.run(ctx, "sh", "gtnh_tasks", "board-json")
	case "in_progress_json":
		return s.run(ctx, "sh", "gtnh_tasks", "in-progress-json")
	case "list":
		args := []string{"sh", "gtnh_tasks", "list"}
		switch optString(subOpts, "scope") {
		case "all":
			args = append(args, "--all")
		case "done":
			args = append(args, "--done")
		default:
			args = append(args, "--open")
		}
		if area := optString(subOpts, "area"); area != "" {
			args = append(args, "--area", area)
		}
		return s.run(ctx, args...)
	case "add":
		args := []string{"sh", "gtnh_tasks", "add", optString(subOpts, "title")}
		if priority := optString(subOpts, "priority"); priority != "" {
			args = append(args, "--priority", priority)
		}
		if area := optString(subOpts, "area"); area != "" {
			args = append(args, "--area", area)
		}
		if status := optString(subOpts, "status"); status != "" {
			args = append(args, "--status", status)
		}
		for _, owner := range splitNames(optString(subOpts, "owners")) {
			args = append(args, "--owner", owner)
		}
		if reason := optString(subOpts, "paused_reason"); reason != "" {
			args = append(args, "--paused-reason", reason)
		}
		if desc := optString(subOpts, "description"); desc != "" {
			args = append(args, "--description", desc)
		}
		return s.run(ctx, args...)
	case "move":
		args := []string{"sh", "gtnh_tasks", "move", fmt.Sprintf("%d", optInt(subOpts, "id")), "--status", optString(subOpts, "status")}
		for _, owner := range splitNames(optString(subOpts, "owners")) {
			args = append(args, "--owner", owner)
		}
		if reason := optString(subOpts, "reason"); reason != "" {
			args = append(args, "--reason", reason)
		}
		return s.run(ctx, args...)
	case "assign":
		return s.run(ctx, append([]string{"sh", "gtnh_tasks", "assign", fmt.Sprintf("%d", optInt(subOpts, "id"))}, splitNames(optString(subOpts, "owners"))...)...)
	case "unassign":
		return s.run(ctx, append([]string{"sh", "gtnh_tasks", "unassign", fmt.Sprintf("%d", optInt(subOpts, "id"))}, splitNames(optString(subOpts, "owners"))...)...)
	case "reassign":
		return s.run(ctx, append([]string{"sh", "gtnh_tasks", "reassign", fmt.Sprintf("%d", optInt(subOpts, "id"))}, splitNames(optString(subOpts, "owners"))...)...)
	case "pause":
		return s.run(ctx, "sh", "gtnh_tasks", "pause", fmt.Sprintf("%d", optInt(subOpts, "id")), optString(subOpts, "reason"))
	case "unpause":
		return s.run(ctx, "sh", "gtnh_tasks", "unpause", fmt.Sprintf("%d", optInt(subOpts, "id")))
	case "describe":
		return s.run(ctx, "sh", "gtnh_tasks", "describe", fmt.Sprintf("%d", optInt(subOpts, "id")), optString(subOpts, "description"))
	case "status_update":
		return s.run(ctx, "sh", "gtnh_tasks", "status-update", fmt.Sprintf("%d", optInt(subOpts, "id")), optString(subOpts, "update"))
	case "status_history":
		return s.run(ctx, "sh", "gtnh_tasks", "status-history", fmt.Sprintf("%d", optInt(subOpts, "id")))
	case "done":
		return s.run(ctx, "sh", "gtnh_tasks", "done", fmt.Sprintf("%d", optInt(subOpts, "id")))
	case "reopen":
		return s.run(ctx, "sh", "gtnh_tasks", "reopen", fmt.Sprintf("%d", optInt(subOpts, "id")))
	case "show":
		return s.run(ctx, "sh", "gtnh_tasks", "show", fmt.Sprintf("%d", optInt(subOpts, "id")))
	case "summary":
		return s.run(ctx, "sh", "gtnh_tasks", "summary")
	default:
		return "", fmt.Errorf("unknown tasks subcommand %q", sub)
	}
}

func (s *Service) dispatchInventory(ctx context.Context, opts []*discordgo.ApplicationCommandInteractionDataOption) (string, error) {
	sub, subOpts := firstSubcommand(opts)
	switch sub {
	case "status":
		return s.run(ctx, "sh", "gtnh_inventory", "status")
	case "find":
		args := []string{"sh", "gtnh_inventory", "find", "--item", optString(subOpts, "item")}
		if player := optString(subOpts, "player"); player != "" {
			args = append(args, "--player", player)
		}
		if scope := optString(subOpts, "scope"); scope != "" {
			args = append(args, "--scope", scope)
		}
		if limit := optInt(subOpts, "limit"); limit > 0 {
			args = append(args, "--limit", fmt.Sprintf("%d", limit))
		}
		if optBool(subOpts, "any_damage") {
			args = append(args, "--any-damage")
		}
		return s.run(ctx, args...)
	case "find_item":
		args := []string{"sh", "gtnh_inventory", "find-item", "--query", optString(subOpts, "query")}
		if optBool(subOpts, "oredict") {
			args = append(args, "--oredict")
		}
		if scope := optString(subOpts, "scope"); scope != "" {
			args = append(args, "--scope", scope)
		}
		if limit := optInt(subOpts, "limit"); limit > 0 {
			args = append(args, "--limit", fmt.Sprintf("%d", limit))
		}
		return s.run(ctx, args...)
	case "player":
		args := []string{"sh", "gtnh_inventory", "player"}
		if name := optString(subOpts, "name"); name != "" {
			args = append(args, "--name", name)
		} else if uuid := optString(subOpts, "uuid"); uuid != "" {
			args = append(args, "--uuid", uuid)
		} else {
			return "", errors.New("either name or uuid is required")
		}
		if optBool(subOpts, "all") {
			args = append(args, "--all")
		}
		return s.run(ctx, args...)
	case "chest":
		args := []string{"sh", "gtnh_inventory", "chest", "--x", fmt.Sprintf("%d", optInt(subOpts, "x")), "--y", fmt.Sprintf("%d", optInt(subOpts, "y")), "--z", fmt.Sprintf("%d", optInt(subOpts, "z"))}
		if dim, ok := optIntMaybe(subOpts, "dim"); ok {
			args = append(args, "--dim", fmt.Sprintf("%d", dim))
		}
		return s.run(ctx, args...)
	case "refresh":
		args := []string{"sh", "gtnh_inventory", "refresh"}
		if scope := optString(subOpts, "scope"); scope != "" {
			switch scope {
			case "players":
				args = append(args, "--players")
			case "chests":
				args = append(args, "--chests")
			case "all":
				args = append(args, "--all")
			}
		}
		return s.run(ctx, args...)
	default:
		return "", fmt.Errorf("unknown inventory subcommand %q", sub)
	}
}

func (s *Service) dispatchQuery(ctx context.Context, opts []*discordgo.ApplicationCommandInteractionDataOption) (string, error) {
	sub, subOpts := firstSubcommand(opts)
	switch sub {
	case "find_item":
		args := []string{"sh", "gtnh_find_item", optString(subOpts, "query")}
		if optBool(subOpts, "oredict") {
			args = append(args, "--oredict")
		}
		return s.run(ctx, args...)
	case "item":
		return s.run(ctx, "sh", "gtnh_query", "item", optString(subOpts, "slug"))
	case "resolve_recipes":
		return s.run(ctx, "sh", "gtnh_resolve_recipes", optString(subOpts, "item"))
	case "search_recipes":
		return s.run(ctx, "sh", "gtnh_search_recipes", optString(subOpts, "query"))
	case "wiki_search":
		out, err := s.run(ctx, "sh", "gtnh_wiki_search", optString(subOpts, "query"))
		return formatWikiSearchOutput(out), err
	case "wiki_page":
		out, err := s.run(ctx, "sh", "gtnh_wiki_page", optString(subOpts, "title"))
		return formatWikiPageOutput(out), err
	default:
		return "", fmt.Errorf("unknown query subcommand %q", sub)
	}
}

func (s *Service) dispatchMinecraft(ctx context.Context, opts []*discordgo.ApplicationCommandInteractionDataOption) (string, error) {
	sub, subOpts := firstSubcommand(opts)
	switch sub {
	case "online":
		args := []string{"sh", "mc_online"}
		if lines := optInt(subOpts, "lines"); lines > 0 {
			args = append(args, fmt.Sprintf("%d", lines))
		}
		return s.run(ctx, args...)
	case "poll":
		args := []string{"sh", "mc_poll"}
		if lines := optInt(subOpts, "lines"); lines > 0 {
			args = append(args, fmt.Sprintf("%d", lines))
		}
		return s.run(ctx, args...)
	case "say":
		return s.run(ctx, "sh", "mc_say", optString(subOpts, "text"))
	default:
		return "", fmt.Errorf("unknown minecraft subcommand %q", sub)
	}
}

func (s *Service) dispatchCheckin(ctx context.Context, opts []*discordgo.ApplicationCommandInteractionDataOption) (string, error) {
	sub, _ := firstSubcommand(opts)
	switch sub {
	case "check":
		return s.run(ctx, "sh", "gtnh_task_checkin", "check")
	case "mark_sent":
		return s.run(ctx, "sh", "gtnh_task_checkin", "mark-sent")
	default:
		return "", fmt.Errorf("unknown checkin subcommand %q", sub)
	}
}

func firstSubcommand(opts []*discordgo.ApplicationCommandInteractionDataOption) (string, []*discordgo.ApplicationCommandInteractionDataOption) {
	if len(opts) == 0 {
		return "", nil
	}
	if opts[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		if len(opts[0].Options) == 0 {
			return opts[0].Name, nil
		}
		if len(opts[0].Options) > 0 {
			return opts[0].Options[0].Name, opts[0].Options[0].Options
		}
		return opts[0].Name, nil
	}
	if opts[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		return opts[0].Name, opts[0].Options
	}
	return "", opts
}

func optString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range opts {
		if opt.Name != name {
			continue
		}
		switch v := opt.Value.(type) {
		case string:
			return strings.TrimSpace(v)
		case fmt.Stringer:
			return strings.TrimSpace(v.String())
		default:
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}
	return ""
}

func optBool(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) bool {
	for _, opt := range opts {
		if opt.Name != name {
			continue
		}
		if v, ok := opt.Value.(bool); ok {
			return v
		}
	}
	return false
}

func optInt(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) int64 {
	v, _ := optIntMaybe(opts, name)
	return v
}

func optIntMaybe(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) (int64, bool) {
	for _, opt := range opts {
		if opt.Name != name {
			continue
		}
		switch v := opt.Value.(type) {
		case int64:
			return v, true
		case float64:
			return int64(v), true
		case json.Number:
			n, err := v.Int64()
			if err == nil {
				return n, true
			}
		case string:
			var n int64
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func splitNames(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func (s *Service) run(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing command")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = s.cfg.Workspace
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			return "", err
		}
		return text, err
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func truncate(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	if max < 4 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func wrapCodeBlock(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "```text\n(no output)\n```"
	}
	if strings.HasPrefix(content, "```") {
		return content
	}

	lang := "text"
	if json.Valid([]byte(content)) {
		lang = "json"
	}

	safe := strings.ReplaceAll(content, "```", "`\u200b``")
	return "```" + lang + "\n" + safe + "\n```"
}

func formatWikiSearchOutput(raw string) string {
	var resp wikiSearchResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil || !resp.OK {
		return raw
	}

	var b strings.Builder
	if resp.Query != "" {
		fmt.Fprintf(&b, "%s (%d hits)", resp.Query, resp.TotalHits)
	} else {
		fmt.Fprintf(&b, "Wiki search (%d hits)", resp.TotalHits)
	}
	if len(resp.Results) == 0 {
		b.WriteString("\nNo clickable results were returned.")
		return b.String()
	}
	count := 0
	for _, result := range resp.Results {
		title := strings.TrimSpace(result.Title)
		if title == "" {
			continue
		}
		count++
		b.WriteString("\n")
		if result.URL != "" {
			fmt.Fprintf(&b, "%d. %s - %s", count, title, result.URL)
		} else {
			fmt.Fprintf(&b, "%d. %s", count, title)
		}
	}
	return b.String()
}

func formatWikiPageOutput(raw string) string {
	var resp wikiPageResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil || !resp.OK {
		return raw
	}

	var b strings.Builder
	if resp.Title != "" {
		b.WriteString(resp.Title)
	}
	if resp.URL != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(resp.URL)
	}
	if summary := strings.TrimSpace(resp.Summary); summary != "" {
		b.WriteString("\n\n")
		b.WriteString(summary)
	}
	return b.String()
}
