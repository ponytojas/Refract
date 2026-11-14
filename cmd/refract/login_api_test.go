package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/users"
)

func newPeople(t *testing.T) *users.Service {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &users.Service{DB: db, Key: make([]byte, 32)}
}

// Every row of the table in the plan: who a provider may sign in as.
func TestAdmitOIDC(t *testing.T) {
	ctx, people := context.Background(), newPeople(t)
	a := &loginAPI{users: people, allowed: []string{"@lab.org"}}

	// No account: only what the operator allowed, created verified.
	u, refusal, err := a.admitOIDC(ctx, "new@lab.org", false)
	if err != nil || refusal != "" || u.ID == "" {
		t.Fatalf("allowed new account: %+v %q %v", u, refusal, err)
	}
	if acct, _ := people.Lookup(ctx, "new@lab.org"); !acct.Verified || acct.HasPassword {
		t.Errorf("created account = %+v", acct)
	}
	if _, refusal, _ := a.admitOIDC(ctx, "stranger@example.com", true); refusal != "not_allowed" {
		t.Errorf("not allowed: %q", refusal)
	}
	if exists, _ := people.Exists(ctx, "stranger@example.com"); exists {
		t.Error("a refused sign-in created an account")
	}

	// An account with a password, verified: the provider has to vouch.
	people.SetPassword(ctx, "ana@example.com", "correct horse")
	if _, refusal, _ := a.admitOIDC(ctx, "ana@example.com", false); refusal != "unverified_email" {
		t.Errorf("password account, claim absent: %q", refusal)
	}
	if u, refusal, err := a.admitOIDC(ctx, "ana@example.com", true); err != nil || refusal != "" || u.Email != "ana@example.com" {
		t.Errorf("password account, claim true: %+v %q %v", u, refusal, err)
	}
	if _, err := people.Login(ctx, "ana@example.com", "correct horse", ""); err != nil {
		t.Errorf("a verified account lost its password: %v", err)
	}

	// An account without a password (made by a provider before): as today.
	for _, verified := range []bool{false, true} {
		if _, refusal, err := a.admitOIDC(ctx, "new@lab.org", verified); err != nil || refusal != "" {
			t.Errorf("provider-only account, verified=%v: %q %v", verified, refusal, err)
		}
	}

	// A sign-up nobody confirmed: the provider's word takes it over.
	l, _ := people.SignUp(ctx, "victim@example.com", "attacker pass", "en")
	session, _ := people.NewSession(ctx, l.To.ID)
	if _, refusal, _ := a.admitOIDC(ctx, "victim@example.com", false); refusal != "unverified_email" {
		t.Errorf("unconfirmed account, claim absent: %q", refusal)
	}
	if acct, _ := people.Lookup(ctx, "victim@example.com"); acct.Verified || !acct.HasPassword {
		t.Fatalf("a refused sign-in changed the account: %+v", acct)
	}
	if _, refusal, err := a.admitOIDC(ctx, "victim@example.com", true); err != nil || refusal != "" {
		t.Fatalf("unconfirmed account, claim true: %q %v", refusal, err)
	}
	acct, _ := people.Lookup(ctx, "victim@example.com")
	if !acct.Verified || acct.HasPassword {
		t.Errorf("account = %+v; want verified without a password", acct)
	}
	if _, err := people.Session(ctx, session); err == nil {
		t.Error("the attacker's session survived")
	}
	if _, err := people.Login(ctx, "victim@example.com", "attacker pass", ""); err == nil {
		t.Error("the attacker's password survived")
	}
}

func TestAuthModeAlias(t *testing.T) {
	for in, want := range map[string]string{"": "cloudflare", "password": "builtin", "builtin": "builtin", "none": "none", "x": "x"} {
		if got := authMode(in); got != want {
			t.Errorf("authMode(%q) = %q, want %q", in, got, want)
		}
	}
	people := newPeople(t)
	if _, login, err := authMiddleware(people, "builtin"); err != nil || login == nil {
		t.Errorf("builtin: login=%v err=%v", login, err)
	}
	if _, _, err := authMiddleware(people, "wat"); err == nil || !strings.Contains(err.Error(), "builtin") {
		t.Errorf("unknown mode: %v", err)
	}
}

func TestPublicBase(t *testing.T) {
	for in, want := range map[string]string{"https://r.example.com/": "https://r.example.com", "http://localhost:8080": "http://localhost:8080"} {
		if got, err := publicBase(in); err != nil || got != want {
			t.Errorf("publicBase(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "r.example.com", "ftp://x", "https://"} {
		if _, err := publicBase(bad); err == nil {
			t.Errorf("publicBase(%q) accepted", bad)
		}
	}
}
