package users

import (
	"context"
	"errors"
	"testing"
	"time"
)

func countUsers(t *testing.T, s *Service) (n int) {
	t.Helper()
	if err := s.DB.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSignUpNewAddress(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p', 'old', 0, 0)`)
	l, err := s.SignUp(ctx, " Ana@Example.com ", "correct horse", "es")
	if err != nil {
		t.Fatal(err)
	}
	if l.Purpose != Verify || l.SignUp || l.Token == "" || l.To.Email != "ana@example.com" {
		t.Fatalf("link = %+v", l)
	}
	a, err := s.Lookup(ctx, "ana@example.com")
	if err != nil || a.Verified || !a.HasPassword || a.Lang != "es" {
		t.Fatalf("account = %+v, %v; want unverified with a password and es", a, err)
	}
	if _, err := s.Login(ctx, "ana@example.com", "correct horse", ""); err != nil {
		t.Errorf("login: %v", err)
	}
	var owner string
	s.DB.QueryRow(`SELECT owner FROM projects WHERE id = 'p'`).Scan(&owner)
	if owner != "" {
		t.Errorf("sign-up adopted data: owner %q", owner)
	}
}

func TestSignUpExistingAddress(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.SetPassword(ctx, "ana@example.com", "correct horse")
	before := countUsers(t, s)
	l, err := s.SignUp(ctx, "ana@example.com", "attacker pass", "en")
	if err != nil {
		t.Fatal(err)
	}
	if l.Purpose != Reset || !l.SignUp || l.Token == "" {
		t.Fatalf("link = %+v", l)
	}
	if countUsers(t, s) != before {
		t.Error("a row was added")
	}
	if _, err := s.Login(ctx, "ana@example.com", "correct horse", ""); err != nil {
		t.Errorf("old password: %v", err)
	}
	if _, err := s.Login(ctx, "ana@example.com", "attacker pass", ""); err == nil {
		t.Error("the new password signs in")
	}
}

func TestSignUpHashesEitherWay(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.SetPassword(ctx, "ana@example.com", "correct horse")
	old := pbkdf2Iter
	defer func() { pbkdf2Iter = old }()
	// Slow the hash until one takes at least 50 ms on this machine.
	for pbkdf2Iter = 100_000; ; pbkdf2Iter *= 2 {
		t0 := time.Now()
		hashPassword("x")
		if time.Since(t0) >= 50*time.Millisecond {
			break
		}
	}
	t0 := time.Now()
	if _, err := s.SignUp(ctx, "ana@example.com", "whatever pass", "en"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(t0); took < 40*time.Millisecond {
		t.Errorf("sign-up of an existing address took %v: it skipped the hash", took)
	}
}

func TestSignUpRejectsBadInputFirst(t *testing.T) {
	s, ctx := newService(t), context.Background()
	for _, c := range [][3]string{{"no-at", "correct horse", "en"}, {"a@example.com", "1234567", "en"}, {"a@example.com", "correct horse", "fr"}} {
		if _, err := s.SignUp(ctx, c[0], c[1], c[2]); err == nil {
			t.Errorf("accepted %v", c)
		}
	}
	if n := countUsers(t, s); n != 0 {
		t.Errorf("%d users created", n)
	}
}

func TestResetPassword(t *testing.T) {
	s, ctx := newService(t), context.Background()
	if _, ok, err := s.RequestReset(ctx, "nobody@example.com"); ok || err != nil || countUsers(t, s) != 0 {
		t.Fatalf("unknown address: ok=%v err=%v users=%d", ok, err, countUsers(t, s))
	}
	u, _ := s.SetPassword(ctx, "ana@example.com", "old password")
	tok1, _ := s.NewSession(ctx, u.ID)
	s.DB.Exec(`UPDATE users SET email_verified_at = 0`)
	l, ok, err := s.RequestReset(ctx, "ANA@example.com")
	if !ok || err != nil || l.Purpose != Reset || l.SignUp {
		t.Fatalf("link = %+v %v %v", l, ok, err)
	}
	if _, err := s.ResetPassword(ctx, l.Token, "short"); err == nil || errors.Is(err, ErrBadToken) {
		t.Fatalf("weak password: %v", err)
	}
	got, err := s.ResetPassword(ctx, l.Token, "new password")
	if err != nil || got.ID != u.ID {
		t.Fatalf("reset = %+v, %v", got, err)
	}
	if _, err := s.Login(ctx, "ana@example.com", "new password", ""); err != nil {
		t.Errorf("new password: %v", err)
	}
	if _, err := s.Login(ctx, "ana@example.com", "old password", ""); err == nil {
		t.Error("old password still works")
	}
	if _, err := s.Session(ctx, tok1); err == nil {
		t.Error("an old session survived")
	}
	if a, _ := s.Lookup(ctx, "ana@example.com"); !a.Verified {
		t.Error("reset didn't verify the account")
	}
	if _, err := s.ResetPassword(ctx, l.Token, "another password"); !errors.Is(err, ErrBadToken) {
		t.Errorf("token reused: %v", err)
	}
}

func TestPasswordChangeKillsResetLinks(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.SetPassword(ctx, "ana@example.com", "old password")
	l, _, _ := s.RequestReset(ctx, "ana@example.com")
	s.SetPassword(ctx, "ana@example.com", "changed password")
	if _, err := s.ResetPassword(ctx, l.Token, "attacker password"); !errors.Is(err, ErrBadToken) {
		t.Errorf("reset after a password change: %v", err)
	}
}

func TestVerifyEmail(t *testing.T) {
	s, ctx := newService(t), context.Background()
	l, _ := s.SignUp(ctx, "ana@example.com", "correct horse", "en")
	other, _ := s.Resolve(ctx, "ben@example.com")
	usable := func() {
		t.Helper()
		if _, err := s.tokenAccount(ctx, l.Token, Verify); err != nil {
			t.Fatalf("token no longer usable: %v", err)
		}
	}
	if _, err := s.VerifyEmail(ctx, l.Token, "", "", ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("no password: %v", err)
	}
	usable()
	if _, err := s.VerifyEmail(ctx, l.Token, other.ID, "", ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("signed in as another account: %v", err)
	}
	if _, err := s.VerifyEmail(ctx, l.Token, "", "wrong password", ""); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("wrong password: %v", err)
	}
	usable()
	u, err := s.VerifyEmail(ctx, l.Token, "", "correct horse", "")
	if err != nil || u.Email != "ana@example.com" {
		t.Fatalf("verify = %+v, %v", u, err)
	}
	if a, _ := s.Lookup(ctx, "ana@example.com"); !a.Verified {
		t.Error("not verified")
	}
	if _, err := s.VerifyEmail(ctx, l.Token, "", "correct horse", ""); !errors.Is(err, ErrBadToken) {
		t.Errorf("token reused: %v", err)
	}

	// Already signed in as that account: no password needed.
	l2, _ := s.SignUp(ctx, "cy@example.com", "correct horse", "en")
	cy, _ := s.Lookup(ctx, "cy@example.com")
	if _, err := s.VerifyEmail(ctx, l2.Token, cy.ID, "", ""); err != nil {
		t.Errorf("signed in as the account: %v", err)
	}
}

func TestConfirmByProvider(t *testing.T) {
	s, ctx := newService(t), context.Background()
	l, _ := s.SignUp(ctx, "ana@example.com", "attacker pass", "en")
	tok, _ := s.NewSession(ctx, l.To.ID)
	if err := s.ConfirmByProvider(ctx, l.To.ID); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Lookup(ctx, "ana@example.com")
	if a.HasPassword || !a.Verified {
		t.Errorf("account = %+v; want verified without a password", a)
	}
	if _, err := s.Session(ctx, tok); err == nil {
		t.Error("session survived")
	}
	if _, err := s.tokenAccount(ctx, l.Token, Verify); !errors.Is(err, ErrBadToken) {
		t.Errorf("link survived: %v", err)
	}

	// A verified account keeps everything.
	u, _ := s.SetPassword(ctx, "ben@example.com", "correct horse")
	tok, _ = s.NewSession(ctx, u.ID)
	if err := s.ConfirmByProvider(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, tok); err != nil {
		t.Errorf("a verified account lost its session: %v", err)
	}
	if _, err := s.Login(ctx, "ben@example.com", "correct horse", ""); err != nil {
		t.Errorf("a verified account lost its password: %v", err)
	}
}

func TestAllowMail(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.SetPassword(ctx, "ana@example.com", "correct horse")
	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		for i := 0; i < maxMailsPerEmail; i++ {
			if err := s.AllowMail(ctx, email, ""); err != nil {
				t.Fatalf("%s #%d: %v", email, i+1, err)
			}
		}
		if err := s.AllowMail(ctx, email, ""); !errors.Is(err, ErrTooManyMails) {
			t.Errorf("%s: 4th = %v", email, err)
		}
	}
	s.DB.Exec(`UPDATE login_failures SET since = since - 901`)
	if err := s.AllowMail(ctx, "ana@example.com", ""); err != nil {
		t.Errorf("after the window: %v", err)
	}
	s.AllowMail(ctx, "ana@example.com", "")
	s.AllowMail(ctx, "ana@example.com", "")
	if ok, _ := s.Unlock(ctx, "ana@example.com"); !ok {
		t.Error("unlock found nothing")
	}
	if err := s.AllowMail(ctx, "ana@example.com", ""); err != nil {
		t.Errorf("after unlock: %v", err)
	}

	for i := 0; i < maxMailsPerIP; i++ {
		if err := s.AllowMail(ctx, "x"+string(rune('a'+i))+"@example.com", "10.0.0.9"); err != nil {
			t.Fatalf("ip #%d: %v", i+1, err)
		}
	}
	if err := s.AllowMail(ctx, "other@example.com", "10.0.0.9"); !errors.Is(err, ErrTooManyMails) {
		t.Errorf("21st from one address: %v", err)
	}
}
