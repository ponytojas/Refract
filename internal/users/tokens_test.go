package users

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"
)

func TestTokensAreSingleUseHashedAndPurposed(t *testing.T) {
	s, ctx := newService(t), context.Background()
	now := time.Unix(1_800_000_000, 0)
	s.Now = func() time.Time { return now }
	u, _ := s.Resolve(ctx, "ana@example.com")

	mint := func(p Purpose) string {
		tx, _ := s.DB.BeginTx(ctx, nil)
		defer tx.Rollback()
		tok, err := s.issue(ctx, tx, u.ID, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return tok
	}
	redeem := func(tok string, p Purpose) error {
		tx, _ := s.DB.BeginTx(ctx, nil)
		defer tx.Rollback()
		if _, err := s.redeem(ctx, tx, tok, p); err != nil {
			return err
		}
		return tx.Commit()
	}

	tok := mint(Reset)
	var stored string
	if err := s.DB.QueryRow(`SELECT hash FROM account_tokens WHERE purpose = 'reset'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(stored) || stored == tok {
		t.Errorf("stored %q is not a digest of the token", stored)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(tok) {
		t.Errorf("token %q is not 32 bytes of base64url", tok)
	}
	if err := redeem(tok, Verify); !errors.Is(err, ErrBadToken) {
		t.Errorf("a reset token redeemed as verify: %v", err)
	}
	if err := redeem(tok, Reset); err != nil {
		t.Fatal(err)
	}
	if err := redeem(tok, Reset); !errors.Is(err, ErrBadToken) {
		t.Errorf("second redeem: %v", err)
	}
	if err := redeem("nope", Reset); !errors.Is(err, ErrBadToken) {
		t.Errorf("unknown token: %v", err)
	}

	for p, ttl := range map[Purpose]time.Duration{Reset: ResetTTL, Verify: VerifyTTL} {
		tok := mint(p)
		now = now.Add(ttl - time.Second)
		if _, err := s.tokenAccount(ctx, tok, p); err != nil {
			t.Errorf("%s valid at TTL-1s: %v", p, err)
		}
		now = now.Add(time.Second)
		if err := redeem(tok, p); !errors.Is(err, ErrBadToken) {
			t.Errorf("%s at TTL: %v", p, err)
		}
	}

	first := mint(Verify)
	second := mint(Verify)
	if err := redeem(first, Verify); !errors.Is(err, ErrBadToken) {
		t.Errorf("an older link still works: %v", err)
	}
	if err := redeem(second, Verify); err != nil {
		t.Errorf("the newest link: %v", err)
	}
}
