package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"greggpt-gtnh/internal/agent"
)

func main() {
	channel := agent.Channel(os.Getenv("GREGGPT_SMOKE_CHANNEL"))
	if channel == "" {
		channel = agent.ChannelDiscord
	}
	message := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if message == "" {
		message = "Use your tools to find a GTNH item matching copper, then answer with one matching display name."
	}
	cfg := agent.Config{
		Model:           getenv(agent.EnvModel, agent.DefaultModel),
		ReasoningEffort: getenv(agent.EnvReasoningEffort, agent.DefaultReasoningEffort),
		Workspace:       getenv(agent.EnvWorkspace, agent.DefaultWorkspace),
		AuthFile:        getenv(agent.EnvAuthFile, agent.DefaultAuthFile),
		Timeout:         time.Duration(getenvInt(agent.EnvAgentTimeout, 90)) * time.Second,
		MaxToolCalls:    getenvInt(agent.EnvMaxToolCalls, agent.DefaultMaxToolCalls),
	}
	runner, err := agent.NewDefaultRunner(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	reply, err := runner.Run(ctx, agent.Request{
		Channel: channel,
		User:    "greggpt-smoke",
		Message: message,
		Context: map[string]string{
			"smoke_test": "true",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(reply)
}

func getenv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var out int
	if _, err := fmt.Sscanf(raw, "%d", &out); err != nil || out <= 0 {
		return fallback
	}
	return out
}
