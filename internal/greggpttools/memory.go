package greggpttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"greggpt-gtnh/internal/filelock"
)

const memoryVersion = 1

type MemoryScope string

const (
	MemoryScopeGlobal  MemoryScope = "global"
	MemoryScopeChannel MemoryScope = "channel"
	MemoryScopeUser    MemoryScope = "user"
)

type MemoryEntry struct {
	ID        string      `json:"id"`
	Scope     MemoryScope `json:"scope"`
	Channel   string      `json:"channel,omitempty"`
	User      string      `json:"user,omitempty"`
	Key       string      `json:"key"`
	Value     string      `json:"value"`
	Tags      []string    `json:"tags,omitempty"`
	Source    string      `json:"source,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	ExpiresAt *time.Time  `json:"expires_at,omitempty"`
}

type MemoryStore struct {
	path       string
	defaultTTL time.Duration
	now        func() time.Time
	mu         sync.Mutex
}

type MemorySelector struct {
	Scopes  []MemoryScope
	Channel string
	User    string
	Query   string
	Tags    []string
	Limit   int
}

type MemoryDocument struct {
	Version int           `json:"version"`
	Items   []MemoryEntry `json:"items"`
}

func NewMemoryStore(path string, defaultTTL time.Duration) *MemoryStore {
	return &MemoryStore{
		path:       path,
		defaultTTL: defaultTTL,
		now:        time.Now,
	}
}

func (s *MemoryStore) Remember(entry MemoryEntry, ttlSeconds *int) (MemoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := filelock.Acquire(context.Background(), s.lockPath())
	if err != nil {
		return MemoryEntry{}, err
	}
	defer lock.Release()

	doc, err := s.loadLocked()
	if err != nil {
		return MemoryEntry{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	entry.Scope = normalizeMemoryScope(entry.Scope)
	entry.Channel = strings.TrimSpace(entry.Channel)
	entry.User = strings.TrimSpace(entry.User)
	entry.Key = strings.TrimSpace(entry.Key)
	entry.Value = strings.TrimSpace(entry.Value)
	entry.Source = strings.TrimSpace(entry.Source)
	entry.Tags = normalizeTags(entry.Tags)
	if entry.Key == "" {
		return MemoryEntry{}, fmt.Errorf("memory key is required")
	}
	if entry.Value == "" {
		return MemoryEntry{}, fmt.Errorf("memory value is required")
	}
	switch entry.Scope {
	case MemoryScopeGlobal:
		entry.Channel = ""
		entry.User = ""
	case MemoryScopeChannel:
		if entry.Channel == "" {
			return MemoryEntry{}, fmt.Errorf("channel memory requires channel")
		}
		entry.User = ""
	case MemoryScopeUser:
		if entry.User == "" {
			return MemoryEntry{}, fmt.Errorf("user memory requires user")
		}
		entry.Channel = ""
	default:
		return MemoryEntry{}, fmt.Errorf("unsupported memory scope %q", entry.Scope)
	}
	entry.ID = strings.TrimSpace(entry.ID)
	if entry.ID == "" {
		entry.ID = nextMemoryID(doc.Items, now)
	}
	entry.CreatedAt = now
	entry.UpdatedAt = now
	if ttlSeconds != nil {
		if *ttlSeconds > 0 {
			expires := now.Add(time.Duration(*ttlSeconds) * time.Second)
			entry.ExpiresAt = &expires
		}
	} else if s.defaultTTL > 0 {
		expires := now.Add(s.defaultTTL)
		entry.ExpiresAt = &expires
	}

	for i, existing := range doc.Items {
		if memorySameSlot(existing, entry) {
			entry.ID = existing.ID
			entry.CreatedAt = existing.CreatedAt
			doc.Items[i] = entry
			return entry, s.saveLocked(doc)
		}
	}
	doc.Items = append(doc.Items, entry)
	return entry, s.saveLocked(doc)
}

func (s *MemoryStore) List(selector MemorySelector) ([]MemoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := filelock.Acquire(context.Background(), s.lockPath())
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	items := make([]MemoryEntry, 0, len(doc.Items))
	changed := false
	for _, item := range doc.Items {
		if item.expired(now) {
			changed = true
			continue
		}
		if memoryMatches(item, selector) {
			items = append(items, item)
		}
	}
	if changed {
		doc.Items = filterUnexpired(doc.Items, now)
		if err := s.saveLocked(doc); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	limit := selector.Limit
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	return append([]MemoryEntry(nil), items[:limit]...), nil
}

func (s *MemoryStore) Forget(id, reason string) (MemoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := filelock.Acquire(context.Background(), s.lockPath())
	if err != nil {
		return MemoryEntry{}, err
	}
	defer lock.Release()

	id = strings.TrimSpace(id)
	if id == "" {
		return MemoryEntry{}, fmt.Errorf("memory id is required")
	}
	if strings.TrimSpace(reason) == "" {
		return MemoryEntry{}, fmt.Errorf("forget reason is required")
	}
	doc, err := s.loadLocked()
	if err != nil {
		return MemoryEntry{}, err
	}
	for i, item := range doc.Items {
		if item.ID != id {
			continue
		}
		doc.Items = append(doc.Items[:i], doc.Items[i+1:]...)
		if err := s.saveLocked(doc); err != nil {
			return MemoryEntry{}, err
		}
		return item, nil
	}
	return MemoryEntry{}, fmt.Errorf("memory id %q not found", id)
}

func (s *MemoryStore) lockPath() string {
	return s.path + ".lock"
}

func (s *MemoryStore) loadLocked() (MemoryDocument, error) {
	doc := MemoryDocument{Version: memoryVersion}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doc, nil
		}
		return doc, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, fmt.Errorf("parse memory store %s: %w", s.path, err)
	}
	if doc.Version == 0 {
		doc.Version = memoryVersion
	}
	return doc, nil
}

func (s *MemoryStore) saveLocked(doc MemoryDocument) error {
	doc.Version = memoryVersion
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".greggpt_memory_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func normalizeMemoryScope(scope MemoryScope) MemoryScope {
	switch MemoryScope(strings.ToLower(strings.TrimSpace(string(scope)))) {
	case MemoryScopeChannel:
		return MemoryScopeChannel
	case MemoryScopeUser:
		return MemoryScopeUser
	default:
		return MemoryScopeGlobal
	}
}

func normalizeTags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func nextMemoryID(items []MemoryEntry, now time.Time) string {
	prefix := now.Format("20060102T150405Z")
	return fmt.Sprintf("mem_%s_%03d", prefix, len(items)+1)
}

func memorySameSlot(a, b MemoryEntry) bool {
	return a.Scope == b.Scope &&
		a.Channel == b.Channel &&
		a.User == b.User &&
		strings.EqualFold(a.Key, b.Key)
}

func (e MemoryEntry) expired(now time.Time) bool {
	return e.ExpiresAt != nil && !e.ExpiresAt.After(now)
}

func filterUnexpired(items []MemoryEntry, now time.Time) []MemoryEntry {
	out := items[:0]
	for _, item := range items {
		if !item.expired(now) {
			out = append(out, item)
		}
	}
	return out
}

func memoryMatches(item MemoryEntry, selector MemorySelector) bool {
	if len(selector.Scopes) > 0 && !memoryScopeSelected(item.Scope, selector.Scopes) {
		return false
	}
	if item.Scope == MemoryScopeChannel && strings.TrimSpace(selector.Channel) != "" && item.Channel != strings.TrimSpace(selector.Channel) {
		return false
	}
	if item.Scope == MemoryScopeUser && strings.TrimSpace(selector.User) != "" && item.User != strings.TrimSpace(selector.User) {
		return false
	}
	query := strings.ToLower(strings.TrimSpace(selector.Query))
	if query != "" && !strings.Contains(strings.ToLower(item.Key+"\n"+item.Value+"\n"+strings.Join(item.Tags, "\n")), query) {
		return false
	}
	for _, tag := range normalizeTags(selector.Tags) {
		if !hasString(item.Tags, tag) {
			return false
		}
	}
	return true
}

func memoryScopeSelected(scope MemoryScope, selected []MemoryScope) bool {
	for _, candidate := range selected {
		if normalizeMemoryScope(candidate) == scope {
			return true
		}
	}
	return false
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
