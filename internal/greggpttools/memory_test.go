package greggpttools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreLoadSaveTTLAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "greggpt_memory.json")
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore(path, time.Hour)
	store.now = func() time.Time { return now }

	entry, err := store.Remember(MemoryEntry{
		Scope:  MemoryScopeUser,
		User:   "Snow",
		Key:    "preferred_voltage",
		Value:  "HV",
		Tags:   []string{"GTNH", "planning"},
		Source: "unit test",
	}, nil)
	if err != nil {
		t.Fatalf("Remember returned error: %v", err)
	}
	if entry.ID == "" || entry.ExpiresAt == nil {
		t.Fatalf("entry missing id or default expiry: %+v", entry)
	}

	reloaded := NewMemoryStore(path, 0)
	reloaded.now = func() time.Time { return now.Add(30 * time.Minute) }
	items, err := reloaded.List(MemorySelector{User: "Snow"})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 1 || items[0].Value != "HV" {
		t.Fatalf("unexpected loaded items: %+v", items)
	}

	reloaded.now = func() time.Time { return now.Add(2 * time.Hour) }
	items, err = reloaded.List(MemorySelector{User: "Snow"})
	if err != nil {
		t.Fatalf("List after expiry returned error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected expired item to be pruned, got %+v", items)
	}

	permanent := NewMemoryStore(path, 0)
	permanent.now = func() time.Time { return now }
	ttl := 0
	entry, err = permanent.Remember(MemoryEntry{
		Scope: MemoryScopeGlobal,
		Key:   "base",
		Value: "steam age",
	}, &ttl)
	if err != nil {
		t.Fatalf("Remember permanent returned error: %v", err)
	}
	if entry.ExpiresAt != nil {
		t.Fatalf("ttl=0 should not expire: %+v", entry)
	}
	forgotten, err := permanent.Forget(entry.ID, "test cleanup")
	if err != nil {
		t.Fatalf("Forget returned error: %v", err)
	}
	if forgotten.ID != entry.ID {
		t.Fatalf("forgotten = %+v, want id %q", forgotten, entry.ID)
	}
	items, err = permanent.List(MemorySelector{})
	if err != nil {
		t.Fatalf("List after forget returned error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items after forget, got %+v", items)
	}
}

func TestMemoryToolsRememberSearchListForget(t *testing.T) {
	workspace := t.TempDir()
	cfg := DefaultConfig()
	cfg.Workspace = workspace
	cfg.MemoryEnabled = true
	cfg.MemoryPath = "state/greggpt_memory.json"
	registry := testRegistry(t, cfg)

	result, err := registry.Execute(context.Background(), "memory_remember", json.RawMessage(`{
		"scope":"channel",
		"channel":"discord",
		"key":"base goal",
		"value":"build the EBF",
		"tags":["goal"],
		"source":"unit test",
		"ttl_seconds":0
	}`))
	if err != nil {
		t.Fatalf("memory_remember returned error: %v", err)
	}
	if !result.OK || !strings.Contains(result.Stdout, `"base goal"`) {
		t.Fatalf("unexpected remember result: %+v", result)
	}

	result, err = registry.Execute(context.Background(), "memory_search", json.RawMessage(`{"query":"EBF","channel":"discord"}`))
	if err != nil {
		t.Fatalf("memory_search returned error: %v", err)
	}
	if !strings.Contains(result.Stdout, `"count":1`) {
		t.Fatalf("unexpected search result: %s", result.Stdout)
	}

	var payload struct {
		Items []MemoryEntry `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("parse search stdout: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected one item, got %+v", payload.Items)
	}

	result, err = registry.ExecuteMap(context.Background(), "memory_forget", map[string]any{
		"id":     payload.Items[0].ID,
		"reason": "unit test",
	})
	if err != nil {
		t.Fatalf("memory_forget returned error: %v", err)
	}
	if !strings.Contains(result.Stdout, `"reason":"unit test"`) {
		t.Fatalf("unexpected forget result: %s", result.Stdout)
	}
}

func TestMemoryStoreSerializesMultipleInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "greggpt_memory.json")
	stores := []*MemoryStore{NewMemoryStore(path, 0), NewMemoryStore(path, 0)}
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	for _, store := range stores {
		store.now = func() time.Time { return now }
	}

	const writes = 20
	errs := make(chan error, writes)
	var wg sync.WaitGroup
	for i := 0; i < writes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := stores[i%len(stores)].Remember(MemoryEntry{
				Scope: MemoryScopeGlobal,
				Key:   fmt.Sprintf("key-%02d", i),
				Value: fmt.Sprintf("value-%02d", i),
			}, nil)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Remember() error = %v", err)
		}
	}

	items, err := NewMemoryStore(path, 0).List(MemorySelector{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != writes {
		t.Fatalf("memory item count = %d, want %d", len(items), writes)
	}
	ids := map[string]struct{}{}
	for _, item := range items {
		if _, exists := ids[item.ID]; exists {
			t.Fatalf("duplicate memory id %q", item.ID)
		}
		ids[item.ID] = struct{}{}
	}
}
