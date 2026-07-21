package greggpttools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func memoryTools(store *MemoryStore, timeout time.Duration) []Tool {
	return []Tool{
		nativeTool("memory_search", GroupMemory, "Search shared persistent memory across all collaborators. User and channel fields are indexing filters, not visibility boundaries.", timeout, object(
			optional("query", stringSpec("Text to search in memory key, value, and tags.")),
			optional("scope", enumStringSpec("Restrict to one memory scope.", memoryScopeEnum, nil)),
			optional("channel", stringSpec("Restrict channel-scoped memory to this channel.")),
			optional("user", stringSpec("Restrict user-scoped memory to this user.")),
			optional("tags", stringArraySpec("Require all tags.")),
			optional("limit", intSpec("Maximum memories to return.", 1, 50, 10)),
		), func(_ context.Context, a Arguments) (Result, error) {
			selector := memorySelectorFromArgs(a)
			selector.Query = stringArg(a, "query")
			if selector.Limit == 0 {
				selector.Limit = 10
			}
			items, err := store.List(selector)
			if err != nil {
				return Result{}, err
			}
			return memoryResult("memory_search", map[string]any{"items": items, "count": len(items)})
		}),
		nativeTool("memory_list", GroupMemory, "List shared persistent memory across all collaborators, optionally filtered by index scope, channel, user, or tag.", timeout, object(
			optional("scope", enumStringSpec("Restrict to one memory scope.", memoryScopeEnum, nil)),
			optional("channel", stringSpec("Restrict channel-scoped memory to this channel.")),
			optional("user", stringSpec("Restrict user-scoped memory to this user.")),
			optional("tags", stringArraySpec("Require all tags.")),
			optional("limit", intSpec("Maximum memories to return.", 1, 50, 20)),
		), func(_ context.Context, a Arguments) (Result, error) {
			selector := memorySelectorFromArgs(a)
			if selector.Limit == 0 {
				selector.Limit = 20
			}
			items, err := store.List(selector)
			if err != nil {
				return Result{}, err
			}
			return memoryResult("memory_list", map[string]any{"items": items, "count": len(items)})
		}),
		nativeTool("memory_remember", GroupMemory, "Proactively persist a stable, non-sensitive fact that will help future collaborative requests. User-indexed memories remain readable in every context.", timeout, object(
			required("scope", enumStringSpec("Index category: user for facts about one collaborator, channel for channel-specific conventions, or global for shared facts.", memoryScopeEnum, nil)),
			required("key", stringSpec("Short stable label for this memory.")),
			required("value", stringSpec("Memory text to store.")),
			optional("channel", stringSpec("Required for channel scope; the channel this fact is indexed under.")),
			optional("user", stringSpec("Required for user scope; the collaborator this fact is about.")),
			optional("tags", stringArraySpec("Audit/search tags.")),
			optional("source", stringSpec("Why this memory was stored, such as user request or task context.")),
			optional("ttl_seconds", intSpec("Time to live in seconds. Omit for default TTL; use 0 for no expiry.", 0, 315360000, nil)),
		), func(_ context.Context, a Arguments) (Result, error) {
			ttl := optionalIntArg(a, "ttl_seconds")
			entry, err := store.Remember(MemoryEntry{
				Scope:   MemoryScope(stringArg(a, "scope")),
				Channel: stringArg(a, "channel"),
				User:    stringArg(a, "user"),
				Key:     stringArg(a, "key"),
				Value:   stringArg(a, "value"),
				Tags:    stringSliceArg(a, "tags"),
				Source:  stringArg(a, "source"),
			}, ttl)
			if err != nil {
				return Result{}, err
			}
			return memoryResult("memory_remember", map[string]any{"remembered": entry})
		}),
		nativeTool("memory_forget", GroupMemory, "Delete one stale or unwanted shared memory by id. Requires a short reason for audit-friendly tool output.", timeout, object(
			required("id", stringSpec("Memory id returned by memory_search or memory_list.")),
			required("reason", stringSpec("Short reason for deleting this memory.")),
		), func(_ context.Context, a Arguments) (Result, error) {
			forgotten, err := store.Forget(stringArg(a, "id"), stringArg(a, "reason"))
			if err != nil {
				return Result{}, err
			}
			return memoryResult("memory_forget", map[string]any{
				"forgotten": forgotten,
				"reason":    stringArg(a, "reason"),
			})
		}),
	}
}

func memorySelectorFromArgs(a Arguments) MemorySelector {
	var scopes []MemoryScope
	if scope := stringArg(a, "scope"); scope != "" {
		scopes = []MemoryScope{MemoryScope(scope)}
	}
	return MemorySelector{
		Scopes:  scopes,
		Channel: stringArg(a, "channel"),
		User:    stringArg(a, "user"),
		Tags:    stringSliceArg(a, "tags"),
		Limit:   intArg(a, "limit", 0),
	}
}

func memoryResult(name string, payload any) (Result, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: name, OK: true, Stdout: string(raw)}, nil
}

func optionalIntArg(args Arguments, name string) *int {
	value, ok := args[name]
	if !ok || value == nil {
		return nil
	}
	n, ok := intValue(value)
	if !ok {
		return nil
	}
	return &n
}

func FormatMemoriesForInjection(items []MemoryEntry, maxItems, maxBytes int) string {
	if maxItems <= 0 || maxBytes <= 0 || len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("memory:\n")
	count := 0
	for _, item := range items {
		if count >= maxItems {
			break
		}
		line := fmt.Sprintf("- [%s] %s key=%q value=%q", item.ID, item.Scope, item.Key, item.Value)
		if item.Channel != "" {
			line += fmt.Sprintf(" channel=%q", item.Channel)
		}
		if item.User != "" {
			line += fmt.Sprintf(" user=%q", item.User)
		}
		if len(item.Tags) > 0 {
			line += fmt.Sprintf(" tags=%q", strings.Join(item.Tags, ","))
		}
		line += "\n"
		if b.Len()+len(line) > maxBytes {
			break
		}
		b.WriteString(line)
		count++
	}
	return strings.TrimRight(b.String(), "\n")
}
