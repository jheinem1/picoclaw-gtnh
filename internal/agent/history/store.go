package history

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

const defaultLimit = 50

const sqliteBusyTimeoutMillis = 5000

type Store struct {
	db *sql.DB
}

type Message struct {
	ID                int64
	Source            string
	ChannelID         string
	ChannelName       string
	AuthorID          string
	AuthorName        string
	Content           string
	Timestamp         time.Time
	ExternalMessageID string
	IsBot             bool
}

type Query struct {
	Source      string
	ChannelID   string
	UserID      string
	Text        string
	Limit       int
	IncludeBots bool
}

type RecallItem struct {
	Message Message
	Score   float64
	Reason  string
}

func NewStore(path string) (*Store, error) {
	return Open(path)
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("history database path is required")
	}
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if err := ensureDir(dir); err != nil {
				return nil, err
			}
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open history database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.configure(context.Background(), path); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure(ctx context.Context, path string) error {
	for _, stmt := range []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", sqliteBusyTimeoutMillis),
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("configure history database: %w", err)
		}
	}
	if path != ":memory:" {
		var mode string
		if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
			return fmt.Errorf("enable history WAL mode: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("enable history WAL mode: database returned %q", mode)
		}
		if _, err := s.db.ExecContext(ctx, "PRAGMA synchronous = NORMAL"); err != nil {
			return fmt.Errorf("configure history synchronous mode: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) AppendMessage(ctx context.Context, msg Message) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("history store is nil")
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin append message: %w", err)
	}
	defer rollback(tx)

	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO messages (
			source, channel_id, channel_name, author_id, author_name, content,
			timestamp_unix_nano, external_message_id, is_bot
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.Source, msg.ChannelID, msg.ChannelName, msg.AuthorID, msg.AuthorName,
		msg.Content, msg.Timestamp.UTC().UnixNano(), msg.ExternalMessageID, boolInt(msg.IsBot),
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read insert result: %w", err)
	}
	if affected == 0 {
		return tx.Commit()
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read message id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO message_fts(rowid, content, author_name, channel_name)
		VALUES (?, ?, ?, ?)`,
		id, msg.Content, msg.AuthorName, msg.ChannelName,
	); err != nil {
		return fmt.Errorf("index message: %w", err)
	}
	return tx.Commit()
}

func (s *Store) Recent(ctx context.Context, q Query) ([]Message, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("history store is nil")
	}
	where, args := filters(q)
	limit := normalizedLimit(q.Limit)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, channel_id, channel_name, author_id, author_name, content,
			timestamp_unix_nano, external_message_id, is_bot
		FROM (
			SELECT id, source, channel_id, channel_name, author_id, author_name, content,
				timestamp_unix_nano, external_message_id, is_bot
			FROM messages
			`+where+`
			ORDER BY timestamp_unix_nano DESC, id DESC
			LIMIT ?
		)
		ORDER BY timestamp_unix_nano ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("query recent messages: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *Store) Recall(ctx context.Context, q Query) ([]RecallItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("history store is nil")
	}
	match := ftsQuery(q.Text)
	if match == "" {
		return []RecallItem{}, nil
	}
	where, args := filters(q)
	limit := normalizedLimit(q.Limit)
	args = append([]any{match}, args...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.source, m.channel_id, m.channel_name, m.author_id, m.author_name,
			m.content, m.timestamp_unix_nano, m.external_message_id, m.is_bot,
			bm25(message_fts) AS rank
		FROM message_fts
		JOIN messages AS m ON m.id = message_fts.rowid
		`+whereForRecall(where)+`
		ORDER BY rank ASC, m.timestamp_unix_nano DESC, m.id DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("query recall messages: %w", err)
	}
	defer rows.Close()

	var items []RecallItem
	for rows.Next() {
		var msg Message
		var timestampUnixNano int64
		var isBot int
		var rank float64
		if err := rows.Scan(
			&msg.ID, &msg.Source, &msg.ChannelID, &msg.ChannelName, &msg.AuthorID,
			&msg.AuthorName, &msg.Content, &timestampUnixNano, &msg.ExternalMessageID,
			&isBot, &rank,
		); err != nil {
			return nil, fmt.Errorf("scan recall message: %w", err)
		}
		msg.Timestamp = time.Unix(0, timestampUnixNano).UTC()
		msg.IsBot = isBot != 0
		items = append(items, RecallItem{
			Message: msg,
			Score:   -rank,
			Reason:  "fts",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recall messages: %w", err)
	}
	return items, nil
}

func (s *Store) init(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin history migration: %w", err)
	}
	defer rollback(tx)

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at_unix_nano INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			channel_name TEXT NOT NULL,
			author_id TEXT NOT NULL,
			author_name TEXT NOT NULL,
			content TEXT NOT NULL,
			timestamp_unix_nano INTEGER NOT NULL,
			external_message_id TEXT NOT NULL DEFAULT '',
			is_bot INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS messages_source_external_id_unique
			ON messages(source, external_message_id)
			WHERE external_message_id <> ''`,
		`CREATE INDEX IF NOT EXISTS messages_recent_idx
			ON messages(source, channel_id, author_id, is_bot, timestamp_unix_nano, id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS message_fts
			USING fts5(content, author_name, channel_name)`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at_unix_nano)
			VALUES (1, strftime('%s','now') * 1000000000)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply history migration: %w", err)
		}
	}
	return tx.Commit()
}

func filters(q Query) (string, []any) {
	var clauses []string
	var args []any
	if q.Source != "" {
		clauses = append(clauses, "source = ?")
		args = append(args, q.Source)
	}
	if q.ChannelID != "" {
		clauses = append(clauses, "channel_id = ?")
		args = append(args, q.ChannelID)
	}
	if q.UserID != "" {
		clauses = append(clauses, "author_id = ?")
		args = append(args, q.UserID)
	}
	if !q.IncludeBots {
		clauses = append(clauses, "is_bot = 0")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func whereForRecall(where string) string {
	if where == "" {
		return "WHERE message_fts MATCH ?"
	}
	return "WHERE message_fts MATCH ? AND " + strings.TrimPrefix(where, "WHERE ")
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var messages []Message
	for rows.Next() {
		var msg Message
		var timestampUnixNano int64
		var isBot int
		if err := rows.Scan(
			&msg.ID, &msg.Source, &msg.ChannelID, &msg.ChannelName, &msg.AuthorID,
			&msg.AuthorName, &msg.Content, &timestampUnixNano, &msg.ExternalMessageID,
			&isBot,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msg.Timestamp = time.Unix(0, timestampUnixNano).UTC()
		msg.IsBot = isBot != 0
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return messages, nil
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	return limit
}

func ftsQuery(text string) string {
	var terms []string
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	}) {
		if field == "" {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(field, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " ")
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}

func ensureDir(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create history database directory: %w", err)
	}
	return nil
}
