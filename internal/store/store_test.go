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

func TestUpgradeKeepsData(t *testing.T) {
	for n := 1; n < len(migrations); n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "refract.db")
			old := at(t, path, n)
			if _, err := old.Exec(`INSERT INTO settings (key, value) VALUES ('k', 'v')`); err != nil {
				t.Fatal(err)
			}
			if n >= 2 {
				if _, err := old.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p', 'Paper', 1, 1)`); err != nil {
					t.Fatal(err)
				}
			}
			old.Close()

			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var v int
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(migrations) {
				t.Fatalf("user_version = %d, %v; want %d", v, err, len(migrations))
			}
			var value string
			if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'k'`).Scan(&value); err != nil || value != "v" {
				t.Fatalf("setting = %q, %v", value, err)
			}
			if n >= 2 {
				var name string
				if err := db.QueryRow(`SELECT name FROM projects WHERE id = 'p'`).Scan(&name); err != nil || name != "Paper" {
					t.Fatalf("project = %q, %v", name, err)
				}
			}
			if got := versionOf(t, path+".pre-upgrade"); got != n {
				t.Fatalf("the copy is at version %d, want %d", got, n)
			}
		})
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
