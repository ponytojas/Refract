// Package store owns the SQLite database.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// migrations are applied in order; the schema version is PRAGMA user_version.
// Append only: never edit or reorder an entry that has shipped.
var migrations = []string{
	`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE projects (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		main_file  TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE chats (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		title      TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE turns (
		id          TEXT PRIMARY KEY,
		chat_id     TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
		message     TEXT NOT NULL,            -- what the user asked (with attached context)
		reply       TEXT NOT NULL DEFAULT '', -- the assistant's final text
		activity    TEXT NOT NULL DEFAULT '[]', -- JSON: compact tool log
		status      TEXT NOT NULL,            -- running | review | accepted | rejected | failed | stopped
		error       TEXT NOT NULL DEFAULT '', -- JSON ai.Error when failed
		base        TEXT NOT NULL DEFAULT '', -- commit the turn started from
		files       TEXT NOT NULL DEFAULT '[]', -- JSON: changed files still pending review
		commits     TEXT NOT NULL DEFAULT '[]', -- JSON: commits created by accepting
		created_at  INTEGER NOT NULL,
		updated_at  INTEGER NOT NULL
	);
	CREATE INDEX turns_chat ON turns(chat_id, created_at)`,
	// Users: whoever the auth layer lets in (Cloudflare Access email, or the
	// local user). Projects belong to one; '' marks data from before users,
	// adopted by the first one.
	`CREATE TABLE users (
		id         TEXT PRIMARY KEY,
		email      TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	);
	ALTER TABLE projects ADD COLUMN owner TEXT NOT NULL DEFAULT '';
	CREATE INDEX projects_owner ON projects(owner, updated_at)`,
	`ALTER TABLE users ADD COLUMN password TEXT NOT NULL DEFAULT '';
	CREATE TABLE sessions (
		id         TEXT PRIMARY KEY, -- sha256 of the cookie's token
		user_id    TEXT NOT NULL,
		expires_at INTEGER NOT NULL
	);
	CREATE INDEX sessions_user ON sessions(user_id)`,
	// Each chat remembers the model it last used; turns keep the model's reasoning.
	`ALTER TABLE chats ADD COLUMN model TEXT NOT NULL DEFAULT '';
	ALTER TABLE chats ADD COLUMN effort TEXT NOT NULL DEFAULT '';
	ALTER TABLE turns ADD COLUMN reasoning TEXT NOT NULL DEFAULT ''`,
	// Failed sign-ins, per "email:…" or "ip:…", within the current window.
	`CREATE TABLE login_failures (
		key   TEXT PRIMARY KEY,
		count INTEGER NOT NULL,
		since INTEGER NOT NULL -- first failure of the window
	);
	CREATE INDEX login_failures_since ON login_failures(since)`,
}

// Open opens (creating if needed) the database at path and applies pending migrations.
func Open(path string) (*sql.DB, error) {
	// WAL: readers never block the writer or each other. busy_timeout makes a
	// second writer wait instead of failing, and _txlock=immediate takes the
	// write lock when a transaction starts, so two transactions can't deadlock
	// upgrading from read to write. Pragmas apply to every pooled connection.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
