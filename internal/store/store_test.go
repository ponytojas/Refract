package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenMigratesOnceAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refract.db")
	for range 2 { // second open must be a no-op, not a re-run
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != len(migrations) {
			t.Fatalf("user_version = %d, want %d", v, len(migrations))
		}
		db.Close()
	}
}

// Many goroutines reading and writing at once: no "database is locked", and a
// query made while iterating another's rows does not block (it did with a
// single pooled connection).
func TestConcurrentAccess(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := range 20 {
		wg.Go(func() {
			for j := range 10 {
				k := fmt.Sprintf("k%d-%d", i, j)
				if err := SetSetting(ctx, db, k, "v"); err != nil {
					errs <- err
				}
				if _, err := GetSetting(ctx, db, k); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		rows, err := db.QueryContext(ctx, `SELECT key FROM settings LIMIT 3`)
		if err != nil {
			done <- err
			return
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			rows.Scan(&k)
			if _, err := GetSetting(ctx, db, k); err != nil { // nested query
				done <- err
				return
			}
		}
		done <- rows.Err()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nested query blocked")
	}
}

// at builds a database at schema version n with nothing else done to it.
func at(t *testing.T, path string, n int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:n] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, n)); err != nil {
		t.Fatal(err)
	}
	return db
}

// versionOf reads the schema version of the database file at path.
func versionOf(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRefusesNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refract.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations)+1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer Refract") {
		t.Fatalf("Open = %v, want a refusal naming a newer Refract", err)
	}
	if exists(path + ".pre-upgrade") {
		t.Fatal("a refused database was copied")
	}
}

// seed puts one row in every table a database at schema version n has. A
// migration that adds a table adds its row here: TestUpgradeKeepsData fails
// for a table left empty.
func seed(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	rows := []struct {
		since int // the migration that created the table
		sql   string
	}{
		{1, `INSERT INTO settings (key, value) VALUES ('k', 'v')`},
		{2, `INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p', 'Paper', 1, 1)`},
		{3, `INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES ('c', 'p', 'Chat', 1, 1)`},
		{3, `INSERT INTO turns (id, chat_id, message, status, created_at, updated_at) VALUES ('t', 'c', 'Hi', 'review', 1, 1)`},
		{4, `INSERT INTO users (id, email, created_at) VALUES ('u', 'u@example.com', 1)`},
		{5, `INSERT INTO sessions (id, user_id, expires_at) VALUES ('s', 'u', 1)`},
		{7, `INSERT INTO login_failures (key, count, since) VALUES ('ip:1', 1, 1)`},
	}
	for _, r := range rows {
		if r.since > n {
			continue
		}
		if _, err := db.Exec(r.sql); err != nil {
			t.Fatal(err)
		}
	}
}

// counts returns how many rows each table of the database has.
func counts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, name := range tables {
		var c int
		if err := db.QueryRow(`SELECT count(*) FROM "` + name + `"`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		got[name] = c
	}
	return got
}

// From every earlier schema version, with a row in every table, an upgrade
// reaches the latest version, loses no row, and leaves a copy of the
// database as it was.
func TestUpgradeKeepsData(t *testing.T) {
	for n := 1; n < len(migrations); n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "refract.db")
			old := at(t, path, n)
			seed(t, old, n)
			before := counts(t, old)
			old.Close()
			for table, c := range before {
				if c == 0 {
					t.Fatalf("table %s has no row at version %d: add one to seed", table, n)
				}
			}

			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var v int
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(migrations) {
				t.Fatalf("user_version = %d, %v; want %d", v, err, len(migrations))
			}
			after := counts(t, db)
			for table, c := range before {
				if after[table] != c {
					t.Errorf("table %s went from %d rows to %d", table, c, after[table])
				}
			}
			var value string
			if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'k'`).Scan(&value); err != nil || value != "v" {
				t.Fatalf("setting = %q, %v", value, err)
			}

			if got := versionOf(t, path+".pre-upgrade"); got != n {
				t.Fatalf("the copy is at version %d, want %d", got, n)
			}
			copied, err := sql.Open("sqlite", "file:"+path+".pre-upgrade?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer copied.Close()
			if got := counts(t, copied); fmt.Sprint(got) != fmt.Sprint(before) {
				t.Fatalf("the copy holds %v, want %v", got, before)
			}
		})
	}
}

// Another process may still be writing when an upgrade starts (an older
// server, while `refract users` of a newer version opens the database). The
// copy must hold every write committed before the upgrade, not only those
// from before the copy began.
func TestCopyHoldsEveryWriteBeforeTheUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refract.db")
	at(t, path, 1).Close()
	writer, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// Each row records the schema version its transaction saw.
	write := func(i int) error {
		tx, err := writer.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var v int
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, fmt.Sprint("w", i), fmt.Sprint(v)); err != nil {
			return err
		}
		return tx.Commit()
	}
	stop := make(chan struct{})
	failed := make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				failed <- nil
				return
			default:
			}
			if err := write(i); err != nil {
				failed <- err
				return
			}
			time.Sleep(time.Millisecond) // leave the upgrade room to take the lock
		}
	}()
	time.Sleep(20 * time.Millisecond) // let some writes land first
	db, err := Open(path)
	close(stop)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := <-failed; err != nil {
		t.Fatal(err)
	}

	var written, copied int
	if err := db.QueryRow(`SELECT count(*) FROM settings WHERE value = '1'`).Scan(&written); err != nil {
		t.Fatal(err)
	}
	c, err := sql.Open("sqlite", "file:"+path+".pre-upgrade?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.QueryRow(`SELECT count(*) FROM settings`).Scan(&copied); err != nil {
		t.Fatal(err)
	}
	if written == 0 || copied != written {
		t.Fatalf("%d rows were written before the upgrade and the copy holds %d", written, copied)
	}
}

func TestFreshDatabaseTakesNoSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refract.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if left, _ := filepath.Glob(path + ".pre-upgrade*"); len(left) != 0 {
		t.Fatalf("a new database left %v", left)
	}
}

func TestConcurrentOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refract.db")
	at(t, path, 1).Close()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			db, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			var v int
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(migrations) {
				errs <- fmt.Errorf("user_version = %d, %v", v, err)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := versionOf(t, path+".pre-upgrade"); got != 1 {
		t.Fatalf("the copy is at version %d, want 1", got)
	}
	if left, _ := filepath.Glob(path + ".pre-upgrade-*"); len(left) != 0 {
		t.Fatalf("temporary copies left behind: %v", left)
	}
}

// Two processes may create the database together: the server's first start
// and a `refract users` run. Both must succeed and be able to write.
func TestConcurrentOpenOfNewDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refract.db")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 4 {
		wg.Go(func() {
			db, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			if err := SetSetting(t.Context(), db, fmt.Sprint("k", i), "v"); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := versionOf(t, path); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	if left, _ := filepath.Glob(path + ".pre-upgrade*"); len(left) != 0 {
		t.Fatalf("a new database left %v", left)
	}
}

func TestFailedUpgradeChangesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refract.db")
	n := len(migrations)
	at(t, path, n).Close()
	shipped := migrations
	t.Cleanup(func() { migrations = shipped })
	migrations = append(append([]string{}, shipped...),
		`CREATE TABLE upgrade_test (x TEXT)`,
		`CREATE TABLE settings (x TEXT)`) // fails: the table exists

	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("migration %d", n+2)) {
		t.Fatalf("Open = %v, want migration %d to fail", err, n+2)
	}
	if got := versionOf(t, path); got != n {
		t.Fatalf("user_version = %d after a failed upgrade, want %d", got, n)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'upgrade_test'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("the first migration of a failed upgrade stayed: %d, %v", count, err)
	}
}
