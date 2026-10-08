package rex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"github.com/google/uuid"
)

// Store holds Rex's product state: bots, conversations, turns, routines and
// settings. Execution state (runs, messages, events) stays in agent-runtime's
// own database, and long-term memory stays in the memory database.
type Store struct{ db *sql.DB }

var ErrNotFound = errors.New("not found")

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrations are append-only. Each entry runs once inside a transaction and
// records its version, so an interrupted upgrade never half-applies.
var migrations = []string{
	`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at DATETIME NOT NULL);
	CREATE TABLE bots (id TEXT PRIMARY KEY, name TEXT NOT NULL, emoji TEXT NOT NULL DEFAULT '', color TEXT NOT NULL DEFAULT '',
		instructions TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
		capabilities TEXT NOT NULL DEFAULT '{}', approval_mode TEXT NOT NULL, workspace_dir TEXT NOT NULL,
		memory_mode TEXT NOT NULL DEFAULT 'private', skills TEXT NOT NULL DEFAULT '[]', mcp_connections TEXT NOT NULL DEFAULT '[]',
		position INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
	CREATE TABLE conversations (id TEXT PRIMARY KEY, bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE, title TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'chat', active_run_id TEXT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
	CREATE INDEX idx_conversations_bot ON conversations(bot_id, updated_at DESC);
	CREATE TABLE conversation_runs (conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE, run_id TEXT NOT NULL UNIQUE,
		position INTEGER NOT NULL, created_at DATETIME NOT NULL, PRIMARY KEY (conversation_id, run_id));
	CREATE TABLE turns (id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE, bot_id TEXT NOT NULL,
		run_id TEXT NOT NULL DEFAULT '', user_text TEXT NOT NULL, assistant_text TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
		started_at DATETIME NOT NULL, settled_at DATETIME, memory_bank TEXT NOT NULL DEFAULT '', memory_status TEXT NOT NULL DEFAULT 'pending',
		memory_error TEXT NOT NULL DEFAULT '', memory_facts INTEGER NOT NULL DEFAULT 0);
	CREATE INDEX idx_turns_conversation ON turns(conversation_id, started_at);
	CREATE INDEX idx_turns_run ON turns(run_id, started_at);
	CREATE INDEX idx_turns_memory ON turns(memory_status, settled_at);
	CREATE TABLE memory_notes (id TEXT PRIMARY KEY, bank TEXT NOT NULL, content TEXT NOT NULL, replaces TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '', facts INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL);
	CREATE TABLE routines (id TEXT PRIMARY KEY, bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE, title TEXT NOT NULL, prompt TEXT NOT NULL,
		schedule_kind TEXT NOT NULL, interval_minutes INTEGER NOT NULL DEFAULT 0, daily_time TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1, conversation_id TEXT NOT NULL DEFAULT '', next_run_at DATETIME, last_run_at DATETIME,
		consecutive_failures INTEGER NOT NULL DEFAULT 0, paused_reason TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
	CREATE TABLE routine_runs (id TEXT PRIMARY KEY, routine_id TEXT NOT NULL REFERENCES routines(id) ON DELETE CASCADE, scheduled_for DATETIME NOT NULL,
		attempt INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL, finished_at DATETIME, UNIQUE(routine_id, scheduled_for, attempt));
	CREATE INDEX idx_routine_runs_routine ON routine_runs(routine_id, created_at DESC);
	CREATE TABLE mcp_connections (id TEXT PRIMARY KEY, name TEXT NOT NULL, url TEXT NOT NULL, auth TEXT NOT NULL DEFAULT 'none',
		tools TEXT NOT NULL DEFAULT '[]', status TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);`,
	`ALTER TABLE routines ADD COLUMN retry_slot DATETIME;
	ALTER TABLE routines ADD COLUMN retry_attempt INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE routines ADD COLUMN retry_at DATETIME;`,
	`ALTER TABLE turns ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;`,
	`CREATE TABLE memory_superseded (bank TEXT NOT NULL, fact_id TEXT NOT NULL, by_fact_id TEXT NOT NULL, by_document TEXT NOT NULL,
		created_at DATETIME NOT NULL, PRIMARY KEY (bank, fact_id));
	CREATE INDEX idx_memory_superseded_by ON memory_superseded(bank, by_document);`,
	`CREATE TABLE browser_sessions (session TEXT PRIMARY KEY, bot_id TEXT NOT NULL, kind TEXT NOT NULL, created_at DATETIME NOT NULL);
	CREATE INDEX idx_browser_sessions_bot ON browser_sessions(bot_id);`,
	`ALTER TABLE turns ADD COLUMN start_sequence INTEGER NOT NULL DEFAULT 0;`,
	// Each bot has one ongoing conversation. A turn's section marks where an
	// earlier conversation or a fresh start begins; context_start is where
	// the bot's carried-over context begins after "Start fresh".
	`ALTER TABLE turns ADD COLUMN section TEXT NOT NULL DEFAULT '';
	ALTER TABLE conversations ADD COLUMN context_start DATETIME;`,
	// A bot in setup interviews the user and configures itself; the main
	// bot (role "main") is Rex itself, which delegates to the others.
	`ALTER TABLE bots ADD COLUMN setup INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE bots ADD COLUMN role TEXT NOT NULL DEFAULT '';`,
	// Turns Rex delivers itself: a task the main bot handed to another bot
	// (origin "rex", delegated_by is the main bot's conversation) or a
	// report back to the main bot (origin "report:<bot name>").
	`ALTER TABLE turns ADD COLUMN origin TEXT NOT NULL DEFAULT '';
	ALTER TABLE turns ADD COLUMN delegated_by TEXT NOT NULL DEFAULT '';`,
	// The memories recalled for a turn, as JSON, so the conversation can
	// show what the bot remembered when it answered.
	`ALTER TABLE turns ADD COLUMN recalled TEXT NOT NULL DEFAULT '';`,
	// Calendar schedules as cron expressions. A missed slot now waits for
	// the user to run or skip it; misses recorded before that are closed.
	`ALTER TABLE routines ADD COLUMN cron TEXT NOT NULL DEFAULT '';
	UPDATE routine_runs SET status='skipped' WHERE status='missed';`,
	// New instructions a bot proposed for itself, waiting for the user.
	`CREATE TABLE instruction_proposals (id TEXT PRIMARY KEY, bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
		instructions TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL);`,
	// Non-secret OAuth settings of an MCP connection (tokens are in the Keychain).
	`ALTER TABLE mcp_connections ADD COLUMN oauth TEXT NOT NULL DEFAULT '';`,
	// A bot's saved logins; passwords and 2FA keys are in the Keychain.
	`CREATE TABLE bot_logins (id TEXT PRIMARY KEY, bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE, site TEXT NOT NULL,
		username TEXT NOT NULL, has_2fa INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, last_used_at DATETIME);`,
	// A fingerprint of the bot's setup (instructions, skills, tools, model)
	// when the conversation's run started, to notice when it changed.
	`ALTER TABLE conversations ADD COLUMN run_fingerprint TEXT NOT NULL DEFAULT '';`,
	// Bots move to the softer face palette (botColors); each old colour
	// maps to its softer counterpart.
	`UPDATE bots SET color='#8b93e8' WHERE lower(color)='#5b6cff';
UPDATE bots SET color='#e0a35e' WHERE lower(color)='#d08a2a';
UPDATE bots SET color='#e8876f' WHERE lower(color)='#e2652f';
UPDATE bots SET color='#62b89c' WHERE lower(color)='#1f9e74';
UPDATE bots SET color='#6aa9de' WHERE lower(color)='#2b8fd6';
UPDATE bots SET color='#a592dc' WHERE lower(color)='#7b5cd6';
UPDATE bots SET color='#e48aa8' WHERE lower(color)='#d23f6e';
UPDATE bots SET color='#86b46e' WHERE lower(color)='#3b8f3f';
UPDATE bots SET color='#58adbb' WHERE lower(color)='#0f8299';
UPDATE bots SET color='#c58ad6' WHERE lower(color)='#a13ad6';
UPDATE bots SET color='#c9a77c' WHERE lower(color)='#c84f2c';
UPDATE bots SET color='#8e9bb0' WHERE lower(color)='#4b5a70';
UPDATE bots SET color='#e0a35e' WHERE lower(color)='#e0821f';
UPDATE bots SET color='#e48aa8' WHERE lower(color)='#c2417a';
UPDATE bots SET color='#c9a77c' WHERE lower(color)='#b8862b';
UPDATE bots SET color='#8e9bb0' WHERE lower(color)='#6b7280';`,
	// Retrospectives: a bot looks back at recent tasks and suggests an
	// improvement (see retro.go). Proposals record where they came from,
	// and answered ones are kept so later look-backs know the outcome.
	`ALTER TABLE bots ADD COLUMN retro_off INTEGER NOT NULL DEFAULT 0;
ALTER TABLE instruction_proposals ADD COLUMN source TEXT NOT NULL DEFAULT 'bot';
ALTER TABLE instruction_proposals ADD COLUMN reason TEXT NOT NULL DEFAULT '';
CREATE TABLE retrospectives (id TEXT PRIMARY KEY, bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
	trigger TEXT NOT NULL, status TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL, started_at DATETIME, finished_at DATETIME);
CREATE INDEX retrospectives_bot ON retrospectives(bot_id, created_at);
CREATE TABLE proposal_history (id TEXT PRIMARY KEY, bot_id TEXT NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
	source TEXT NOT NULL, summary TEXT NOT NULL, outcome TEXT NOT NULL, created_at DATETIME NOT NULL, answered_at DATETIME NOT NULL);`,
	// The skill catalog (catalog.go): GitHub sources, their indexed skills,
	// and proposals to install one.
	`CREATE TABLE skill_sources (id TEXT PRIMARY KEY, repo TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, builtin INTEGER NOT NULL DEFAULT 0,
	note TEXT NOT NULL DEFAULT '', stars INTEGER NOT NULL DEFAULT 0, license TEXT NOT NULL DEFAULT '', commit_sha TEXT NOT NULL DEFAULT '',
	skill_count INTEGER NOT NULL DEFAULT 0, indexed_at DATETIME, error TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '');
CREATE TABLE catalog_skills (id TEXT PRIMARY KEY, source_id TEXT NOT NULL REFERENCES skill_sources(id) ON DELETE CASCADE, dir TEXT NOT NULL,
	name TEXT NOT NULL, description TEXT NOT NULL, license TEXT NOT NULL DEFAULT '', has_scripts INTEGER NOT NULL DEFAULT 0,
	files TEXT NOT NULL DEFAULT '[]', commit_sha TEXT NOT NULL);
ALTER TABLE instruction_proposals ADD COLUMN kind TEXT NOT NULL DEFAULT 'instructions';
ALTER TABLE instruction_proposals ADD COLUMN catalog_id TEXT NOT NULL DEFAULT '';`,
	// Edited messages: what a run said from an edited message on is left
	// out when a later run carries the conversation forward.
	`CREATE TABLE context_cuts (conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE, run_id TEXT NOT NULL,
	after DATETIME NOT NULL, PRIMARY KEY (conversation_id, run_id));`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for index := current; index < len(migrations); index++ {
		version := index + 1
		err := withTx(ctx, s.db, func(tx *sql.Tx) error {
			for _, statement := range strings.Split(migrations[index], ";\n") {
				if strings.TrimSpace(statement) == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, time.Now().UTC())
			return err
		})
		if err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
	}
	return nil
}

// --- settings ---

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, time.Now().UTC())
	return err
}

// --- bots ---

const botColumns = `id,name,emoji,color,instructions,provider,model,capabilities,approval_mode,workspace_dir,memory_mode,skills,mcp_connections,position,created_at,updated_at,setup,role,retro_off`

func scanBot(row interface{ Scan(...any) error }) (*Bot, error) {
	var bot Bot
	var capabilities, skills, connections string
	if err := row.Scan(&bot.ID, &bot.Name, &bot.Emoji, &bot.Color, &bot.Instructions, &bot.Provider, &bot.Model, &capabilities, &bot.ApprovalMode, &bot.WorkspaceDir, &bot.MemoryMode, &skills, &connections, &bot.Position, &bot.CreatedAt, &bot.UpdatedAt, &bot.Setup, &bot.Role, &bot.RetroOff); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(capabilities), &bot.Capabilities)
	_ = json.Unmarshal([]byte(skills), &bot.Skills)
	_ = json.Unmarshal([]byte(connections), &bot.MCPConnections)
	if bot.Skills == nil {
		bot.Skills = []string{}
	}
	if bot.MCPConnections == nil {
		bot.MCPConnections = []string{}
	}
	return &bot, nil
}

func (s *Store) ListBots(ctx context.Context) ([]Bot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+botColumns+` FROM bots ORDER BY position, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Bot{}
	for rows.Next() {
		bot, err := scanBot(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *bot)
	}
	return items, rows.Err()
}

func (s *Store) GetBot(ctx context.Context, id string) (*Bot, error) {
	bot, err := scanBot(s.db.QueryRowContext(ctx, `SELECT `+botColumns+` FROM bots WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return bot, err
}

func (s *Store) SaveBot(ctx context.Context, bot *Bot) error {
	now := time.Now().UTC()
	if bot.CreatedAt.IsZero() {
		bot.CreatedAt = now
	}
	bot.UpdatedAt = now
	capabilities, _ := json.Marshal(bot.Capabilities)
	skills, _ := json.Marshal(nonNil(bot.Skills))
	connections, _ := json.Marshal(nonNil(bot.MCPConnections))
	_, err := s.db.ExecContext(ctx, `INSERT INTO bots(`+botColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,emoji=excluded.emoji,color=excluded.color,instructions=excluded.instructions,
		provider=excluded.provider,model=excluded.model,capabilities=excluded.capabilities,approval_mode=excluded.approval_mode,
		workspace_dir=excluded.workspace_dir,memory_mode=excluded.memory_mode,skills=excluded.skills,mcp_connections=excluded.mcp_connections,
		position=excluded.position,updated_at=excluded.updated_at,setup=excluded.setup,role=excluded.role,retro_off=excluded.retro_off`,
		bot.ID, bot.Name, bot.Emoji, bot.Color, bot.Instructions, bot.Provider, bot.Model, string(capabilities), bot.ApprovalMode, bot.WorkspaceDir, bot.MemoryMode, string(skills), string(connections), bot.Position, bot.CreatedAt, bot.UpdatedAt, bot.Setup, bot.Role, bot.RetroOff)
	return err
}

func (s *Store) DeleteBot(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM bots WHERE id=?`, id)
	return err
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// --- conversations ---

type Conversation struct {
	ID          string    `json:"id"`
	BotID       string    `json:"bot_id"`
	Title       string    `json:"title"`
	Kind        string    `json:"kind"`
	ActiveRunID *string   `json:"active_run_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// ContextStart is set by "Start fresh": earlier turns stay visible but
	// are no longer carried into the bot's context.
	ContextStart *time.Time `json:"context_start,omitempty"`
	// RunFingerprint is the bot's setup when the active run started.
	RunFingerprint string `json:"-"`
}

const conversationColumns = `id,bot_id,title,kind,active_run_id,created_at,updated_at,context_start,run_fingerprint`

func scanConversation(row interface{ Scan(...any) error }) (*Conversation, error) {
	var item Conversation
	err := row.Scan(&item.ID, &item.BotID, &item.Title, &item.Kind, &item.ActiveRunID, &item.CreatedAt, &item.UpdatedAt, &item.ContextStart, &item.RunFingerprint)
	return &item, err
}

func (s *Store) CreateConversation(ctx context.Context, botID, title, kind string) (*Conversation, error) {
	now := time.Now().UTC()
	if kind == "" {
		kind = "chat"
	}
	item := &Conversation{ID: uuid.NewString(), BotID: botID, Title: strings.TrimSpace(title), Kind: kind, CreatedAt: now, UpdatedAt: now}
	if item.Title == "" {
		item.Title = "New conversation"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations(`+conversationColumns+`) VALUES(?,?,?,?,?,?,?,?,?)`, item.ID, item.BotID, item.Title, item.Kind, nil, now, now, nil, "")
	return item, err
}

func (s *Store) ListConversations(ctx context.Context, botID string) ([]Conversation, error) {
	query := `SELECT ` + conversationColumns + ` FROM conversations`
	var args []any
	if botID != "" {
		query += ` WHERE bot_id=?`
		args = append(args, botID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		item, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	item, err := scanConversation(s.db.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM conversations WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (s *Store) RenameConversation(ctx context.Context, id, title string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET title=?, updated_at=? WHERE id=?`, strings.TrimSpace(title), time.Now().UTC(), id)
	return err
}

func (s *Store) DeleteConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id=?`, id)
	return err
}

func (s *Store) TouchConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, time.Now().UTC(), id)
	return err
}

func (s *Store) AttachRun(ctx context.Context, conversationID, runID string) error {
	return withTx(ctx, s.db, func(tx *sql.Tx) error {
		var position int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM conversation_runs WHERE conversation_id=?`, conversationID).Scan(&position); err != nil {
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_runs(conversation_id,run_id,position,created_at) VALUES(?,?,?,?)`, conversationID, runID, position, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE conversations SET active_run_id=?,updated_at=? WHERE id=?`, runID, now, conversationID)
		return err
	})
}

func (s *Store) ConversationRunIDs(ctx context.Context, id string) ([]string, error) {
	return s.strings(ctx, `SELECT run_id FROM conversation_runs WHERE conversation_id=? ORDER BY position`, id)
}

func (s *Store) ConversationForRun(ctx context.Context, runID string) (*Conversation, error) {
	item, err := scanConversation(s.db.QueryRowContext(ctx, `SELECT c.id,c.bot_id,c.title,c.kind,c.active_run_id,c.created_at,c.updated_at,c.context_start,c.run_fingerprint FROM conversations c JOIN conversation_runs r ON r.conversation_id=c.id WHERE r.run_id=?`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

// ActiveRunIDs lists runs the host should watch: the newest run of every
// conversation. Their terminal state is checked against the runtime.
func (s *Store) ActiveRunIDs(ctx context.Context) ([]string, error) {
	return s.strings(ctx, `SELECT active_run_id FROM conversations WHERE active_run_id IS NOT NULL`)
}

func (s *Store) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// --- turns ---

// Turn is one user message and the bot's answer to it. Turns are the source
// documents that memory retains, so each has a stable identity.
type Turn struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	BotID          string     `json:"bot_id"`
	RunID          string     `json:"run_id"`
	UserText       string     `json:"user_text"`
	AssistantText  string     `json:"assistant_text"`
	Status         string     `json:"status"`
	StartedAt      time.Time  `json:"started_at"`
	SettledAt      *time.Time `json:"settled_at,omitempty"`
	MemoryBank     string     `json:"memory_bank"`
	MemoryStatus   string     `json:"memory_status"`
	MemoryError    string     `json:"memory_error,omitempty"`
	MemoryFacts    int        `json:"memory_facts"`
	// StartSequence is the run's last event sequence before this turn, so
	// the timeline shows exactly the events that belong to the turn.
	StartSequence int64 `json:"start_sequence"`
	// Section starts a new part of the bot's conversation: the title of an
	// earlier conversation merged into it, or "Fresh start".
	Section string `json:"section,omitempty"`
	// Origin is empty for the user's own messages; see the migration.
	Origin      string `json:"origin,omitempty"`
	DelegatedBy string `json:"delegated_by,omitempty"`
	// Recalled lists the memories given to the bot for this turn.
	Recalled []RecalledMemory `json:"recalled,omitempty"`
}

// RecalledMemory is one memory a bot was given, and the bank it came from.
type RecalledMemory struct {
	Text string `json:"text"`
	Bank string `json:"bank"`
}

const turnColumns = `id,conversation_id,bot_id,run_id,user_text,assistant_text,status,started_at,settled_at,memory_bank,memory_status,memory_error,memory_facts,start_sequence,section,origin,delegated_by,recalled`

func scanTurn(row interface{ Scan(...any) error }) (*Turn, error) {
	var item Turn
	var recalled string
	err := row.Scan(&item.ID, &item.ConversationID, &item.BotID, &item.RunID, &item.UserText, &item.AssistantText, &item.Status, &item.StartedAt, &item.SettledAt, &item.MemoryBank, &item.MemoryStatus, &item.MemoryError, &item.MemoryFacts, &item.StartSequence, &item.Section, &item.Origin, &item.DelegatedBy, &recalled)
	if recalled != "" {
		_ = json.Unmarshal([]byte(recalled), &item.Recalled)
	}
	return &item, err
}

func (s *Store) CreateTurn(ctx context.Context, turn *Turn) error {
	if turn.ID == "" {
		turn.ID = uuid.NewString()
	}
	if turn.StartedAt.IsZero() {
		turn.StartedAt = time.Now().UTC()
	}
	if turn.Status == "" {
		turn.Status = "working"
	}
	if turn.MemoryStatus == "" {
		turn.MemoryStatus = "pending"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO turns(`+turnColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, turn.ID, turn.ConversationID, turn.BotID, turn.RunID, turn.UserText, turn.AssistantText, turn.Status, turn.StartedAt, turn.SettledAt, turn.MemoryBank, turn.MemoryStatus, turn.MemoryError, turn.MemoryFacts, turn.StartSequence, turn.Section, turn.Origin, turn.DelegatedBy, "")
	return err
}

func (s *Store) SetTurnRun(ctx context.Context, turnID, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE turns SET run_id=? WHERE id=?`, runID, turnID)
	return err
}

func (s *Store) DeleteTurn(ctx context.Context, turnID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM turns WHERE id=?`, turnID)
	return err
}

// AddContextCut leaves a run's transcript from after on out of the
// conversation's carried-forward context. The earliest cut of a run wins.
func (s *Store) AddContextCut(ctx context.Context, conversationID, runID string, after time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO context_cuts (conversation_id, run_id, after) VALUES (?, ?, ?)
		ON CONFLICT (conversation_id, run_id) DO UPDATE SET after=min(after, excluded.after)`, conversationID, runID, after.UTC())
	return err
}

// ContextCuts returns the cuts of a conversation, by run.
func (s *Store) ContextCuts(ctx context.Context, conversationID string) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, after FROM context_cuts WHERE conversation_id=?`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cuts := map[string]time.Time{}
	for rows.Next() {
		var runID string
		var after time.Time
		if err := rows.Scan(&runID, &after); err != nil {
			return nil, err
		}
		cuts[runID] = after
	}
	return cuts, rows.Err()
}

func (s *Store) GetTurn(ctx context.Context, id string) (*Turn, error) {
	item, err := scanTurn(s.db.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

// OpenTurnForRun returns the newest unsettled turn of a run.
func (s *Store) OpenTurnForRun(ctx context.Context, runID string) (*Turn, error) {
	item, err := scanTurn(s.db.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE run_id=? AND status='working' ORDER BY started_at DESC LIMIT 1`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

// LatestTurnForTarget returns the newest turn of a conversation; the target
// context endpoint uses its text as the memory recall query.
func (s *Store) LatestTurnForConversation(ctx context.Context, conversationID string) (*Turn, error) {
	item, err := scanTurn(s.db.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM turns WHERE conversation_id=? ORDER BY started_at DESC LIMIT 1`, conversationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func (s *Store) SettleTurn(ctx context.Context, turnID, status, assistantText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE turns SET status=?, assistant_text=?, settled_at=? WHERE id=? AND status='working'`, status, assistantText, time.Now().UTC(), turnID)
	return err
}

// SetTurnRecalled records the memories the bot was given for a turn.
func (s *Store) SetTurnRecalled(ctx context.Context, turnID string, recalled []RecalledMemory) error {
	payload, err := json.Marshal(recalled)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE turns SET recalled=? WHERE id=?`, string(payload), turnID)
	return err
}

func (s *Store) SetTurnMemory(ctx context.Context, turnID, status, message string, facts int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE turns SET memory_status=?, memory_error=?, memory_facts=? WHERE id=?`, status, message, facts, turnID)
	return err
}

func (s *Store) TurnsPendingMemory(ctx context.Context, limit int) ([]Turn, error) {
	return s.turns(ctx, `SELECT `+turnColumns+` FROM turns WHERE memory_status='pending' AND status IN ('settled','cancelled','failed') ORDER BY settled_at LIMIT ?`, limit)
}

func (s *Store) TurnsForBank(ctx context.Context, bank string, limit int) ([]Turn, error) {
	return s.turns(ctx, `SELECT `+turnColumns+` FROM turns WHERE memory_bank=? AND memory_status IN ('retained','forgotten','failed','pending') AND status='settled' ORDER BY started_at DESC LIMIT ?`, bank, limit)
}

func (s *Store) TurnsForConversation(ctx context.Context, conversationID string) ([]Turn, error) {
	return s.turns(ctx, `SELECT `+turnColumns+` FROM turns WHERE conversation_id=? ORDER BY started_at`, conversationID)
}

func (s *Store) turns(ctx context.Context, query string, args ...any) ([]Turn, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Turn{}
	for rows.Next() {
		item, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// RetryTurn moves a turn to a new run once. It reports false when the turn
// was already retried.
func (s *Store) RetryTurn(ctx context.Context, turnID, runID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE turns SET run_id=?, attempts=attempts+1, start_sequence=0 WHERE id=? AND attempts=0 AND status='working'`, runID, turnID)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

func (s *Store) TurnAttempts(ctx context.Context, turnID string) (int, error) {
	var attempts int
	err := s.db.QueryRowContext(ctx, `SELECT attempts FROM turns WHERE id=?`, turnID).Scan(&attempts)
	return attempts, err
}

// WorkingTurns lists turns still marked as running; used for crash recovery.
func (s *Store) WorkingTurns(ctx context.Context) ([]Turn, error) {
	return s.turns(ctx, `SELECT `+turnColumns+` FROM turns WHERE status='working' ORDER BY started_at`)
}

// --- memory notes ---

type MemoryNote struct {
	ID        string    `json:"id"`
	Bank      string    `json:"bank"`
	Content   string    `json:"content"`
	Replaces  string    `json:"replaces,omitempty"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Facts     int       `json:"facts"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateMemoryNote(ctx context.Context, note *MemoryNote) error {
	note.ID = uuid.NewString()
	note.CreatedAt = time.Now().UTC()
	note.Status = "pending"
	_, err := s.db.ExecContext(ctx, `INSERT INTO memory_notes(id,bank,content,replaces,status,error,facts,created_at) VALUES(?,?,?,?,?,?,?,?)`, note.ID, note.Bank, note.Content, note.Replaces, note.Status, "", 0, note.CreatedAt)
	return err
}

// RequeueMemorySources marks every learned source to be learned again, used
// when memory is rebuilt for a new embedding model.
func (s *Store) RequeueMemorySources(ctx context.Context) error {
	return withTx(ctx, s.db, func(tx *sql.Tx) error {
		for _, statement := range []string{
			`UPDATE turns SET memory_status='pending', memory_error='', memory_facts=0 WHERE status='settled' AND memory_status IN ('retained','failed')`,
			`UPDATE memory_notes SET status='pending', error='', facts=0 WHERE status IN ('retained','failed')`,
			`DELETE FROM memory_superseded`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) SetMemoryNoteStatus(ctx context.Context, id, status, message string, facts int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memory_notes SET status=?, error=?, facts=? WHERE id=?`, status, message, facts, id)
	return err
}

func (s *Store) MemoryNotes(ctx context.Context, bank, status string) ([]MemoryNote, error) {
	query := `SELECT id,bank,content,replaces,status,error,facts,created_at FROM memory_notes WHERE 1=1`
	var args []any
	if bank != "" {
		query += ` AND bank=?`
		args = append(args, bank)
	}
	if status != "" {
		query += ` AND status=?`
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MemoryNote{}
	for rows.Next() {
		var item MemoryNote
		if err := rows.Scan(&item.ID, &item.Bank, &item.Content, &item.Replaces, &item.Status, &item.Error, &item.Facts, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
