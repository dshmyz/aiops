package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
)

var migrations = []string{"001_copilot.sql", "002_action_plan_audit_execution.sql", "003_audit_events_created_at_index.sql", "004_assistant_conversations.sql", "005_scheduled_tasks.sql", "006_audit_events_trace_id.sql", "007_assistant_feedback.sql", "008_knowledge_documents.sql", "009_environment_aliases.sql", "010_aiops_skills.sql", "011_mcp_servers.sql", "012_alerts.sql", "013_execution_verification.sql", "014_runbooks.sql"}

const defaultSQLiteDSN = "file:copilot-local.db?cache=shared&_foreign_keys=on"

// Open creates a MySQL database handle. Callers own the returned handle and
// must close it when the service stops.
func Open(dsn string) (*sql.DB, error) {
	return OpenWithDriver("mysql", dsn)
}

// OpenWithDriver creates a SQL database handle for the configured runtime
// store. The default remains MySQL; SQLite is intended for local development
// and deterministic tests.
func OpenWithDriver(driver, dsn string) (*sql.DB, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "mysql":
		if strings.TrimSpace(dsn) == "" {
			return nil, errors.New("MySQL DSN is required")
		}
		return sql.Open("mysql", dsn)
	case "sqlite", "sqlite3":
		if strings.TrimSpace(dsn) == "" {
			dsn = defaultSQLiteDSN
		}
		return sql.Open("sqlite3", dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

// ApplyMigrationsForDriver applies the schema for the configured runtime store.
func ApplyMigrationsForDriver(driver string, db *sql.DB) error {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "mysql":
		return ApplyMigrations(db)
	case "sqlite", "sqlite3":
		return ApplySQLiteMigrations(db)
	default:
		return fmt.Errorf("unsupported database driver %q", driver)
	}
}

// ApplySQLiteMigrations applies the copilot schema to an in-process SQLite
// database. It exists for deterministic local tests; production remains MySQL.
//
// The migration list is idempotent: `CREATE TABLE/INDEX IF NOT EXISTS` skips
// already-existing objects. Columns added after the initial schema (trace_id,
// conversation fields) are backfilled via PRAGMA table_info checks + ALTER
// TABLE, because SQLite does not support `ADD COLUMN IF NOT EXISTS`.
func ApplySQLiteMigrations(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	for _, statement := range sqliteMigrations {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("apply sqlite migration: %w", err)
		}
	}
	// Backfill columns added after the initial schema on legacy SQLite
	// databases. Each ALTER is conditional so running migrations on an
	// already-up-to-date database is a no-op.
	for _, bf := range sqliteColumnBackfills {
		exists, err := sqliteColumnExistsInDB(db, bf.table, bf.column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", bf.table, bf.column, bf.decl)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("backfill %s.%s: %w", bf.table, bf.column, err)
		}
	}
	// Indexes that depend on backfilled columns. Must run AFTER the ALTER
	// backfill so the column exists when the index is created.
	dependencyIndexes := []string{
		`CREATE INDEX IF NOT EXISTS copilot_audit_events_trace_id_idx ON copilot_audit_events (trace_id)`,
	}
	for _, stmt := range dependencyIndexes {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create dependency index: %w", err)
		}
	}
	return nil
}

// sqliteColumnBackfills lists columns added after the initial schema, used to
// backfill legacy SQLite databases that pre-date them. Each entry declares
// the SQLite column type/default; see ApplySQLiteMigrations for the conditional
// ALTER logic.
var sqliteColumnBackfills = []struct {
	table  string
	column string
	decl   string
}{
	// E-phase: audit trace correlation.
	{"copilot_audit_events", "trace_id", "TEXT NULL"},
	// Multi-turn conversation feature.
	{"copilot_assistant_conversations", "last_message_preview", "TEXT NOT NULL DEFAULT ''"},
	{"copilot_assistant_turns", "parent_turn_id", "TEXT NULL"},
	{"copilot_assistant_turns", "response_type", "TEXT NULL"},
	{"copilot_assistant_turns", "response_payload", "TEXT NULL"},
	// 结果准 #5: 执行后验证持久化。
	{"tool_executions", "verification", "TEXT NULL"},
	// 结果准 #5: dry-run 预览持久化。
	{"action_plans", "dry_run", "TEXT NULL"},
}

// sqliteColumnExistsInDB reports whether a column exists on a SQLite table.
// Used by ApplySQLiteMigrations to conditionally ALTER legacy schemas.
func sqliteColumnExistsInDB(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false, fmt.Errorf("table_info %q: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("scan table_info: %w", err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate table_info: %w", err)
	}
	return false, nil
}

// ApplyMigrations applies each copilot schema migration once. The migration
// ledger permits additive migrations after a database has already received the
// initial schema, while keeping repeated application safe.
func ApplyMigrations(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS copilot_schema_migrations (name VARCHAR(255) NOT NULL PRIMARY KEY, applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)) ENGINE=InnoDB`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	for _, name := range migrations {
		var applied bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM copilot_schema_migrations WHERE name = ?)`, name).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %q: %w", name, err)
		}
		if applied {
			continue
		}
		path, err := findMigration(name)
		if err != nil {
			return err
		}
		migration, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", path, err)
		}
		for _, statement := range strings.Split(string(migration), ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if _, err := db.Exec(statement); err != nil && !isAlreadyAppliedDDL(err) {
				return fmt.Errorf("apply migration %q: %w", path, err)
			}
		}
		if _, err := db.Exec(`INSERT INTO copilot_schema_migrations (name) VALUES (?)`, name); err != nil {
			return fmt.Errorf("record migration %q: %w", name, err)
		}
	}

	return nil
}

// Docker's MySQL init hook can apply the SQL files before this Go migration
// ledger exists. Accept only named duplicate-DDL errors so that first Go-run
// records those already-applied migration files; all other failures remain
// fatal and visible.
func isAlreadyAppliedDDL(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1060, 1061, 1826: // duplicate column, key, or foreign-key name
		return true
	default:
		return false
	}
}

func findMigration(name string) (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	for {
		candidate := filepath.Join(directory, "migrations", name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}

	return "", fmt.Errorf("migration %q not found; run from the repository or mount its migrations directory", name)
}

var sqliteMigrations = []string{
	`CREATE TABLE IF NOT EXISTS action_plans (
		id TEXT NOT NULL PRIMARY KEY,
		request_id TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		tool_name TEXT NOT NULL,
		input_json TEXT NOT NULL,
		input_hash TEXT NOT NULL,
		risk_level TEXT NOT NULL,
		status TEXT NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		confirmation_token_hash TEXT NULL,
		confirmed_by TEXT NULL,
		confirmed_at DATETIME NULL,
		expires_at DATETIME NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS action_plans_status_expires_at_idx ON action_plans (status, expires_at)`,
	`CREATE TABLE IF NOT EXISTS tool_executions (
		id TEXT NOT NULL PRIMARY KEY,
		action_plan_id TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		status TEXT NOT NULL,
		result_summary TEXT NULL,
		error_summary TEXT NULL,
		started_at DATETIME NULL,
		completed_at DATETIME NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE (idempotency_key),
		FOREIGN KEY (action_plan_id) REFERENCES action_plans (id)
	)`,
	`CREATE INDEX IF NOT EXISTS tool_executions_action_plan_id_idx ON tool_executions (action_plan_id)`,
	`CREATE TABLE IF NOT EXISTS copilot_audit_events (
		id TEXT NOT NULL PRIMARY KEY,
		action_plan_id TEXT NULL,
		tool_execution_id TEXT NULL,
		request_id TEXT NOT NULL,
		actor_subject TEXT NOT NULL,
		tool_name TEXT NULL,
		action TEXT NOT NULL,
		decision TEXT NOT NULL DEFAULT '',
		trace_id TEXT NULL,
		metadata TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (action_plan_id) REFERENCES action_plans (id),
		FOREIGN KEY (tool_execution_id) REFERENCES tool_executions (id)
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_audit_events_request_id_idx ON copilot_audit_events (request_id)`,
	`CREATE INDEX IF NOT EXISTS copilot_audit_events_action_plan_id_idx ON copilot_audit_events (action_plan_id)`,
	`CREATE INDEX IF NOT EXISTS copilot_audit_events_tool_execution_id_idx ON copilot_audit_events (tool_execution_id)`,
	`CREATE INDEX IF NOT EXISTS copilot_audit_events_created_at_idx ON copilot_audit_events (created_at)`,
	`CREATE INDEX IF NOT EXISTS copilot_audit_events_actor_subject_idx ON copilot_audit_events (actor_subject)`,
	// trace_id index is created after the ALTER TABLE backfill below, so
	// legacy databases without the column do not fail on `CREATE INDEX ON
	// (trace_id)`.
	`CREATE TABLE IF NOT EXISTS copilot_assistant_conversations (
		id TEXT NOT NULL PRIMARY KEY,
		subject TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		last_message_preview TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		archived_at DATETIME NULL
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_assistant_conversations_subject_last_active_idx ON copilot_assistant_conversations (subject, last_active_at)`,
	`CREATE TABLE IF NOT EXISTS copilot_assistant_turns (
		id TEXT NOT NULL PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		parent_turn_id TEXT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		response_type TEXT NULL,
		response_payload TEXT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (conversation_id) REFERENCES copilot_assistant_conversations (id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_assistant_turns_conversation_created_idx ON copilot_assistant_turns (conversation_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS copilot_scheduled_tasks (
		id TEXT NOT NULL PRIMARY KEY,
		name TEXT NOT NULL,
		subject TEXT NOT NULL,
		capability_name TEXT NOT NULL,
		input TEXT NOT NULL,
		schedule_kind TEXT NOT NULL,
		preset TEXT NULL,
		cron_expr TEXT NULL,
		timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
		enabled INTEGER NOT NULL DEFAULT 1,
		last_run_at DATETIME NULL,
		last_status TEXT NULL,
		next_run_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_scheduled_tasks_enabled_next_run_idx ON copilot_scheduled_tasks (enabled, next_run_at)`,
	`CREATE INDEX IF NOT EXISTS copilot_scheduled_tasks_subject_idx ON copilot_scheduled_tasks (subject)`,
	`CREATE TABLE IF NOT EXISTS copilot_scheduled_task_runs (
		id TEXT NOT NULL PRIMARY KEY,
		task_id TEXT NOT NULL,
		started_at DATETIME NOT NULL,
		finished_at DATETIME NOT NULL,
		status TEXT NOT NULL,
		result_summary TEXT NULL,
		result_data TEXT NULL,
		error TEXT NULL,
		audit_event_id TEXT NULL,
		FOREIGN KEY (task_id) REFERENCES copilot_scheduled_tasks (id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_scheduled_task_runs_task_started_idx ON copilot_scheduled_task_runs (task_id, started_at)`,
	`CREATE TABLE IF NOT EXISTS copilot_assistant_feedback (
		id TEXT NOT NULL PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		turn_id TEXT NOT NULL,
		subject TEXT NOT NULL,
		rating INTEGER NOT NULL DEFAULT 0,
		correction TEXT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_assistant_feedback_subject_idx ON copilot_assistant_feedback (subject, created_at)`,
	`CREATE TABLE IF NOT EXISTS copilot_knowledge_documents (
		id TEXT NOT NULL PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		embedding TEXT NULL,
		source TEXT NOT NULL DEFAULT 'manual',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS copilot_inspection_reports (
		id TEXT NOT NULL PRIMARY KEY,
		period TEXT NOT NULL,
		window_start DATETIME NOT NULL,
		window_end DATETIME NOT NULL,
		generated_at DATETIME NOT NULL,
		total_tasks INTEGER NOT NULL DEFAULT 0,
		succeeded_tasks INTEGER NOT NULL DEFAULT 0,
		failed_tasks INTEGER NOT NULL DEFAULT 0,
		task_summaries TEXT NULL,
		html_content TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_inspection_reports_generated_at_idx ON copilot_inspection_reports (generated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS copilot_environment_aliases (
		id TEXT NOT NULL PRIMARY KEY,
		environment TEXT NOT NULL,
		alias TEXT NOT NULL,
		display_name TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS copilot_environment_aliases_env_alias_idx ON copilot_environment_aliases (environment, alias)`,
	`CREATE INDEX IF NOT EXISTS copilot_environment_aliases_alias_idx ON copilot_environment_aliases (alias)`,
	`CREATE TABLE IF NOT EXISTS copilot_aiops_skills (
		id TEXT NOT NULL PRIMARY KEY,
		slug TEXT NOT NULL,
		name TEXT NOT NULL,
		category TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		applicable_actions TEXT NOT NULL DEFAULT '[]',
		tool_dependencies TEXT NOT NULL DEFAULT '[]',
		content TEXT NOT NULL,
		output_contract TEXT NOT NULL DEFAULT '',
		risk_level TEXT NOT NULL DEFAULT 'read_only',
		is_builtin INTEGER NOT NULL DEFAULT 0,
		is_enabled INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS copilot_aiops_skills_slug_idx ON copilot_aiops_skills (slug)`,
	`CREATE INDEX IF NOT EXISTS copilot_aiops_skills_category_idx ON copilot_aiops_skills (category)`,
	`CREATE TABLE IF NOT EXISTS copilot_mcp_servers (
		id TEXT NOT NULL PRIMARY KEY,
		name TEXT NOT NULL,
		command TEXT NOT NULL DEFAULT '',
		args TEXT NOT NULL DEFAULT '[]',
		env TEXT NOT NULL DEFAULT '{}',
		url TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS copilot_mcp_servers_name_idx ON copilot_mcp_servers (name)`,
	`CREATE TABLE IF NOT EXISTS copilot_alerts (
		id TEXT NOT NULL PRIMARY KEY,
		external_id TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		description TEXT NULL,
		severity TEXT NOT NULL DEFAULT 'warning',
		status TEXT NOT NULL DEFAULT 'firing',
		environment TEXT NOT NULL DEFAULT '',
		domain TEXT NOT NULL DEFAULT '',
		resource_type TEXT NOT NULL DEFAULT '',
		resource_name TEXT NOT NULL DEFAULT '',
		labels TEXT NOT NULL DEFAULT '{}',
		raw TEXT NULL,
		fired_at DATETIME NOT NULL,
		resolved_at DATETIME NULL,
		received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS copilot_alerts_identity_idx ON copilot_alerts (source, external_id)`,
	`CREATE INDEX IF NOT EXISTS copilot_alerts_status_severity_idx ON copilot_alerts (status, severity)`,
	`CREATE INDEX IF NOT EXISTS copilot_alerts_environment_status_idx ON copilot_alerts (environment, status)`,
	`CREATE INDEX IF NOT EXISTS copilot_alerts_updated_at_idx ON copilot_alerts (updated_at)`,
	`CREATE TABLE IF NOT EXISTS copilot_runbooks (
		id TEXT NOT NULL PRIMARY KEY,
		slug TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		intent_pattern TEXT NOT NULL DEFAULT '[]',
		tool_sequence TEXT NOT NULL DEFAULT '[]',
		default_strategy TEXT NULL,
		risk_level TEXT NOT NULL DEFAULT 'medium',
		is_builtin INTEGER NOT NULL DEFAULT 0,
		is_enabled INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS copilot_runbooks_slug_idx ON copilot_runbooks (slug)`,
}
