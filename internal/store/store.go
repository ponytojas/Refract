// Package store owns the SQLite database.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

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
	if err := migrate(db, path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate brings the database to the latest schema, or refuses it. A database
// newer than this binary was written by a later Refract, and serving it would
// mean guessing at its schema. Before an upgrade a copy is kept beside the
// database. The whole upgrade is one write transaction, so the database is
// only ever at its old version or the latest one, and two processes opening
// it together (the server and `refract users`) upgrade it once.
func migrate(db *sql.DB, path string) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return newer(version)
	}
	if version == len(migrations) {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	// Read again: holding the write lock, nobody else can change it.
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return newer(version)
	}
	if version == len(migrations) {
		return nil // another process upgraded it while this one waited
	}
	if version > 0 {
		if err := snapshot(db, path); err != nil {
			return fmt.Errorf("copying the database before upgrading it: %w", err)
		}
	}
	for i := version; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}

func newer(version int) error {
	return fmt.Errorf("the database is at schema version %d and this Refract knows up to %d: a newer Refract wrote it. Run that version or a later one, or restore a backup from before the upgrade", version, len(migrations))
}

// snapshot writes a consistent copy of the database to path.pre-upgrade. The
// caller holds the write lock and has changed nothing yet, so the copy, read
// on another connection, is exactly what the upgrade starts from: no write
// can land between the two. It is written under a name of its own and then
// renamed, so the copy from an earlier upgrade is replaced whole or not at all.
func snapshot(db *sql.DB, path string) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".pre-upgrade-*")
	if err != nil {
		return err
	}
	f.Close()                 // VACUUM INTO accepts an empty file as its target
	defer os.Remove(f.Name()) // no-op once renamed
	if _, err := db.Exec(`VACUUM INTO ?`, f.Name()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path+".pre-upgrade")
}
