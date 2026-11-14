package projects

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/auth"
)

func fixed(q Quota) func(context.Context, string) (Quota, error) {
	return func(context.Context, string) (Quota, error) { return q, nil }
}

func sizeOf(t *testing.T, s *Service, id string) int64 {
	t.Helper()
	var n int64
	if err := s.DB.QueryRow(`SELECT size_bytes FROM projects WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func write(t *testing.T, s *Service, id, name string, n int) error {
	t.Helper()
	return s.WriteFile(ownerCtx, id, name, bytes.NewReader([]byte(strings.Repeat("x", n))))
}

func TestQuotaProjects(t *testing.T) {
	s := newService(t)
	s.QuotaFor = fixed(Quota{MaxProjects: 2})
	for range 2 {
		if _, err := s.Create(ownerCtx, "p"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Create(ownerCtx, "p")
	if !errors.Is(err, ErrOverLimit) || !strings.Contains(err.Error(), "2 projects") {
		t.Fatalf("third create: %v", err)
	}
	other := auth.WithUser(context.Background(), auth.User{ID: "other"})
	if _, err := s.Create(other, "p"); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaIsPerAccount(t *testing.T) {
	s := newService(t)
	s.QuotaFor = func(_ context.Context, owner string) (Quota, error) {
		if owner == "owner" {
			return Quota{MaxProjects: 1}, nil
		}
		return Quota{MaxProjects: 3}, nil
	}
	if _, err := s.Create(ownerCtx, "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ownerCtx, "p"); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("second create: %v", err)
	}
	other := auth.WithUser(context.Background(), auth.User{ID: "other"})
	for range 3 {
		if _, err := s.Create(other, "p"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuotaStorage(t *testing.T) {
	s := newService(t)
	s.QuotaFor = fixed(Quota{MaxBytes: 1 << 20})
	p, err := s.Create(ownerCtx, "p")
	if err != nil {
		t.Fatal(err)
	}
	id := p.ID
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := write(t, s, id, name, 700<<10); err != nil {
			t.Fatal(err)
		}
	}
	err = write(t, s, id, "c.bin", 10)
	if !errors.Is(err, ErrOverLimit) || !strings.Contains(err.Error(), "storage is full") {
		t.Fatalf("third write: %v", err)
	}
	if err := s.Trash(ownerCtx, id); err != nil {
		t.Fatal(err)
	}
	// The trash still counts; purging frees it.
	if err := s.Purge(ownerCtx, id); err != nil {
		t.Fatal(err)
	}
	q, err := s.Create(ownerCtx, "q")
	if err != nil {
		t.Fatal(err)
	}
	if err := write(t, s, q.ID, "a.txt", 10); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaCountsTrash(t *testing.T) {
	s := newService(t)
	s.QuotaFor = fixed(Quota{MaxProjects: 1})
	p, err := s.Create(ownerCtx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ownerCtx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ownerCtx, "q"); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("create with a project in the trash: %v", err)
	}
}

func TestQuotaCountsHistoryAndWorktrees(t *testing.T) {
	s := newService(t)
	s.QuotaFor = fixed(Quota{MaxBytes: 1 << 30})
	res, err := s.Import(ownerCtx, "p", ZipSource(makeZip(t,
		entry{name: "main.tex", body: doc},
		entry{name: "big.bin", body: randomString(300 << 10)},
	)))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Project.ID
	imported := sizeOf(t, s, id)
	if imported < 300<<10 {
		t.Fatalf("size after import = %d", imported)
	}
	if _, _, err := s.StartTurn(ownerCtx, id, "t1"); err != nil {
		t.Fatal(err)
	}
	started := sizeOf(t, s, id)
	if started < imported+300<<10 {
		t.Fatalf("size after StartTurn = %d, imported = %d", started, imported)
	}
	if err := s.EndTurn(ownerCtx, id, "t1"); err != nil {
		t.Fatal(err)
	}
	if ended := sizeOf(t, s, id); ended >= started {
		t.Fatalf("size after EndTurn = %d, started = %d", ended, started)
	}
}

func randomString(n int) string {
	b := make([]byte, n)
	x := uint32(12345)
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte('a' + x>>24%26)
	}
	return string(b)
}

func TestQuotaOverCountIsCorrected(t *testing.T) {
	s := newService(t)
	s.QuotaFor = fixed(Quota{MaxBytes: 1 << 30})
	p, err := s.Create(ownerCtx, "p")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := write(t, s, p.ID, "f.txt", 200<<10); err != nil {
			t.Fatal(err)
		}
	}
	if n := sizeOf(t, s, p.ID); n < 600<<10 {
		t.Fatalf("size before checkpoint = %d", n)
	}
	if _, err := s.Checkpoint(ownerCtx, p.ID, "save"); err != nil {
		t.Fatal(err)
	}
	// One 200 KB file, one compressed copy in history (repeated bytes compress well).
	if n := sizeOf(t, s, p.ID); n >= 500<<10 {
		t.Fatalf("size after checkpoint = %d", n)
	}
}

func TestQuotaNeverBlocksShrinking(t *testing.T) {
	s := newService(t)
	s.QuotaFor = fixed(Quota{})
	p, err := s.Create(ownerCtx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := write(t, s, p.ID, "a.txt", 100<<10); err != nil {
		t.Fatal(err)
	}
	s.grew(ownerCtx, p.ID, 1<<20)
	s.QuotaFor = fixed(Quota{MaxBytes: 1 << 10, MaxProjects: 1})
	if err := write(t, s, p.ID, "b.txt", 1); !errors.Is(err, ErrOverLimit) {
		t.Fatalf("write over the limit: %v", err)
	}
	if _, _, err := s.ReadFile(ownerCtx, p.ID, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ownerCtx, p.ID, "a.txt"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := s.Export(ownerCtx, p.ID, &buf); err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ownerCtx, p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNoQuotaNoWork(t *testing.T) {
	s := newService(t)
	p, err := s.Create(ownerCtx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := write(t, s, p.ID, "a.txt", 1000); err != nil {
		t.Fatal(err)
	}
	if n := sizeOf(t, s, p.ID); n != 0 {
		t.Fatalf("size = %d, want 0", n)
	}
}

func TestMeasureAll(t *testing.T) {
	s := newService(t)
	s.QuotaFor = fixed(Quota{})
	var ids []string
	for range 2 {
		p, err := s.Create(ownerCtx, "p")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	if _, err := s.DB.Exec(`UPDATE projects SET size_bytes = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s.MeasureAll(ownerCtx); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if sizeOf(t, s, id) == 0 {
			t.Fatalf("%s not measured", id)
		}
	}
}
