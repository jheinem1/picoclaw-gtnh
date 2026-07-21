package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"greggpt-gtnh/internal/questplanner"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	planner, err := questplanner.LoadWorkspace(workspaceDir())
	if err != nil {
		return err
	}
	switch args[0] {
	case "recommend":
		fs := flag.NewFlagSet("recommend", flag.ContinueOnError)
		user := fs.String("user", "", "requesting player")
		message := fs.String("message", "", "original request")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return printJSON(planner.Recommend(*user, *message))
	case "plan":
		fs := flag.NewFlagSet("plan", flag.ContinueOnError)
		user := fs.String("user", "", "requesting player")
		message := fs.String("message", "", "original request")
		limit := fs.Int("limit", 5, "maximum recommendations")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *limit < 1 || *limit > 20 {
			return errors.New("--limit must be between 1 and 20")
		}
		return printJSON(planner.Plan(*user, *message, *limit))
	case "explain":
		fs := flag.NewFlagSet("explain", flag.ContinueOnError)
		id := fs.String("id", "", "quest id")
		user := fs.String("user", "", "requesting player")
		message := fs.String("message", "", "original request")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*id) == "" {
			return errors.New("--id is required")
		}
		explanation, err := planner.ExplainQuest(*id, *user, *message)
		if err != nil {
			return err
		}
		return printJSON(explanation)
	default:
		return usageError()
	}
}

func workspaceDir() string {
	if value := strings.TrimSpace(os.Getenv("GTNH_WORKSPACE")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("GREGGPT_WORKSPACE")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("GTNH_QUEST_INDEX_FILE")); value != "" {
		return filepath.Dir(filepath.Dir(value))
	}
	if wd, err := os.Getwd(); err == nil && filepath.Base(wd) == "workspace" {
		return wd
	}
	return "workspace"
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usageError() error {
	return errors.New("usage: gtnh_quest_query recommend|plan|explain [options]")
}
