package history

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenInitializesSchema(t *testing.T) {
	store := openTestStore(t)
	defer closeStore(t, store)

	var migrationCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = 1`).Scan(&migrationCount); err != nil {
		t.Fatalf("schema_migrations query error = %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count = %d, want 1", migrationCount)
	}

	if err := store.AppendMessage(context.Background(), testMessage("discord", "general", "u1", "hello world", 0)); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	recall, err := store.Recall(context.Background(), Query{Text: "hello", Limit: 1})
	if err != nil {
		t.Fatalf("Recall() error = %v", err)
	}
	if len(recall) != 1 {
		t.Fatalf("len(Recall()) = %d, want 1", len(recall))
	}
}

func TestOpenConfiguresWALAndBusyTimeout(t *testing.T) {
	store := openTestStore(t)
	defer closeStore(t, store)

	var journalMode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode query error = %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var busyTimeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout query error = %v", err)
	}
	if busyTimeout != sqliteBusyTimeoutMillis {
		t.Fatalf("busy_timeout = %d, want %d", busyTimeout, sqliteBusyTimeoutMillis)
	}
}

func TestMultipleStoresWriteConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	defer closeStore(t, first)
	second, err := Open(path)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	defer closeStore(t, second)

	stores := []*Store{first, second}
	const writes = 40
	errs := make(chan error, writes)
	var wg sync.WaitGroup
	for i := 0; i < writes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := testMessage("discord", "general", "u1", fmt.Sprintf("message %d", i), i)
			msg.ExternalMessageID = fmt.Sprintf("message-%d", i)
			errs <- stores[i%len(stores)].AppendMessage(context.Background(), msg)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	got, err := first.Recent(context.Background(), Query{IncludeBots: true, Limit: writes})
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(got) != writes {
		t.Fatalf("message count = %d, want %d", len(got), writes)
	}
}

func TestRecentReturnsChronologicalOutput(t *testing.T) {
	store := openTestStore(t)
	defer closeStore(t, store)
	ctx := context.Background()

	for _, msg := range []Message{
		testMessage("discord", "general", "u1", "third", 3),
		testMessage("discord", "general", "u1", "first", 1),
		testMessage("discord", "general", "u1", "second", 2),
	} {
		if err := store.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	got, err := store.Recent(ctx, Query{Limit: 2})
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if contents(got) != "second,third" {
		t.Fatalf("Recent contents = %q, want chronological latest two", contents(got))
	}
}

func TestAppendMessageDedupesExternalMessageIDPerSource(t *testing.T) {
	store := openTestStore(t)
	defer closeStore(t, store)
	ctx := context.Background()

	first := testMessage("discord", "general", "u1", "first copy", 1)
	first.ExternalMessageID = "msg-1"
	duplicate := testMessage("discord", "general", "u1", "second copy", 2)
	duplicate.ExternalMessageID = "msg-1"
	otherSource := testMessage("minecraft", "general", "u1", "other source", 3)
	otherSource.ExternalMessageID = "msg-1"
	noExternalA := testMessage("discord", "general", "u1", "no external a", 4)
	noExternalB := testMessage("discord", "general", "u1", "no external b", 5)

	for _, msg := range []Message{first, duplicate, otherSource, noExternalA, noExternalB} {
		if err := store.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	got, err := store.Recent(ctx, Query{IncludeBots: true, Limit: 10})
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if contents(got) != "first copy,other source,no external a,no external b" {
		t.Fatalf("Recent contents = %q, want deduped external id per source", contents(got))
	}
}

func TestRecentFiltersBySourceChannelAndUser(t *testing.T) {
	store := openTestStore(t)
	defer closeStore(t, store)
	ctx := context.Background()

	for _, msg := range []Message{
		testMessage("discord", "general", "u1", "match", 1),
		testMessage("discord", "general", "u2", "wrong user", 2),
		testMessage("discord", "trade", "u1", "wrong channel", 3),
		testMessage("minecraft", "general", "u1", "wrong source", 4),
	} {
		if err := store.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	got, err := store.Recent(ctx, Query{Source: "discord", ChannelID: "general", UserID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if contents(got) != "match" {
		t.Fatalf("Recent contents = %q, want filtered match", contents(got))
	}
}

func TestRecallRanksFTSMatchesAndHonorsLimit(t *testing.T) {
	store := openTestStore(t)
	defer closeStore(t, store)
	ctx := context.Background()

	for _, msg := range []Message{
		testMessage("discord", "general", "u1", "tungsten ore processing", 1),
		testMessage("discord", "general", "u1", "tungsten tungsten tungsten coil", 2),
		testMessage("discord", "general", "u1", "steam age bronze", 3),
	} {
		if err := store.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	got, err := store.Recall(ctx, Query{Text: "tungsten", Limit: 1})
	if err != nil {
		t.Fatalf("Recall() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Recall()) = %d, want 1", len(got))
	}
	if got[0].Message.Content != "tungsten tungsten tungsten coil" {
		t.Fatalf("top recall = %q, want strongest FTS match", got[0].Message.Content)
	}
	if got[0].Reason != "fts" {
		t.Fatalf("Reason = %q, want fts", got[0].Reason)
	}
}

func TestRecallFiltersAndExcludesBotsByDefault(t *testing.T) {
	store := openTestStore(t)
	defer closeStore(t, store)
	ctx := context.Background()

	bot := testMessage("discord", "general", "bot", "platinum dust recipe", 1)
	bot.IsBot = true
	user := testMessage("discord", "trade", "u1", "platinum wire trade", 2)
	match := testMessage("discord", "general", "u1", "platinum cable", 3)
	for _, msg := range []Message{bot, user, match} {
		if err := store.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	got, err := store.Recall(ctx, Query{Source: "discord", ChannelID: "general", Text: "platinum", Limit: 10})
	if err != nil {
		t.Fatalf("Recall() error = %v", err)
	}
	if len(got) != 1 || got[0].Message.Content != "platinum cable" {
		t.Fatalf("Recall filtered result = %#v, want only user message in channel", got)
	}

	withBots, err := store.Recall(ctx, Query{Source: "discord", ChannelID: "general", Text: "platinum", Limit: 10, IncludeBots: true})
	if err != nil {
		t.Fatalf("Recall() with bots error = %v", err)
	}
	if len(withBots) != 2 {
		t.Fatalf("len(Recall() with bots) = %d, want 2", len(withBots))
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return store
}

func closeStore(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func testMessage(source, channelID, authorID, content string, offset int) Message {
	return Message{
		Source:      source,
		ChannelID:   channelID,
		ChannelName: channelID + "-name",
		AuthorID:    authorID,
		AuthorName:  authorID + "-name",
		Content:     content,
		Timestamp:   time.Date(2026, 4, 30, 12, 0, offset, 0, time.UTC),
	}
}

func contents(messages []Message) string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Content)
	}
	return join(out)
}

func join(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, part := range parts[1:] {
		out += "," + part
	}
	return out
}
