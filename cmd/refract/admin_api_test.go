package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/usage"
	"github.com/ponytojas/Refract/internal/users"
)

const adminAccountsPath = "/api/admin/accounts"

func signInForAdminTest(e *accountEnv, email, password string) *browser {
	b := e.browser()
	b.expect(b.post("/api/auth/login", map[string]string{"email": email, "password": password}), http.StatusNoContent, "")
	return b
}

func TestAdminAccountActions(t *testing.T) {
	e := newAccountEnv(t, envOpts{noMail: true})
	ctx := context.Background()
	admin, err := e.people.SetPassword(ctx, "admin@example.com", "fixture admin password")
	if err != nil {
		t.Fatal(err)
	}
	operator := signInForAdminTest(e, admin.Email, "fixture admin password")
	const email = "member@example.com"
	add := adminAction{Action: "add", Email: email}
	created := operator.post(adminAccountsPath, add)
	var issued adminResult
	if err := json.Unmarshal([]byte(created.Body), &issued); created.Status != http.StatusOK || err != nil || issued.Password == "" {
		t.Fatalf("add account returned %d or failed to issue a password", created.Status)
	}
	acct, err := e.people.Lookup(ctx, email)
	if err != nil || !acct.Verified {
		t.Fatalf("account was not created and confirmed: %v", err)
	}
	member := signInForAdminTest(e, email, issued.Password)
	for _, route := range []string{"/admin", adminAccountsPath, adminAccountsPath + "/report"} {
		member.expect(member.do("GET", route, nil), http.StatusNotFound, "")
	}
	member.expect(member.post(adminAccountsPath, adminAction{Action: "remove", ID: admin.ID, Confirmed: true}), http.StatusNotFound, "")
	operator.expect(operator.post(adminAccountsPath, add), http.StatusConflict, "admin_account_exists")
	otherSession := signInForAdminTest(e, email, issued.Password)

	var accounts []users.AdminAccount
	list := operator.do("GET", adminAccountsPath, nil)
	if err := json.Unmarshal([]byte(list.Body), &accounts); list.Status != http.StatusOK || err != nil || len(accounts) != 2 ||
		!accounts[0].IsAdmin || accounts[1].IsAdmin {
		t.Fatalf("unexpected account list: %d %+v, %v", list.Status, accounts, err)
	}
	for _, action := range []string{"reset", "remove", "signout", "unlock", "unlock_all", "rename"} {
		operator.expect(operator.post(adminAccountsPath, adminAction{Action: action, ID: acct.ID, Email: email}), http.StatusBadRequest, "other")
	}

	reset := operator.post(adminAccountsPath, adminAction{Action: "reset", ID: acct.ID, Confirmed: true})
	var replacement adminResult
	if err := json.Unmarshal([]byte(reset.Body), &replacement); reset.Status != http.StatusOK || err != nil ||
		replacement.Password == "" || replacement.Password == issued.Password {
		t.Fatalf("reset returned %d or failed to issue a different password", reset.Status)
	}
	member.expect(member.do("GET", "/api/account", nil), http.StatusUnauthorized, "login_required")
	otherSession.expect(otherSession.do("GET", "/api/account", nil), http.StatusUnauthorized, "login_required")
	if _, err := e.people.Login(ctx, email, issued.Password, ""); !errors.Is(err, users.ErrBadLogin) {
		t.Fatalf("old password after reset: %v", err)
	}
	member = signInForAdminTest(e, email, replacement.Password)

	const renamed = "renamed@example.com"
	operator.expect(operator.post(adminAccountsPath, adminAction{Action: "rename", ID: acct.ID, Email: renamed, Confirmed: true}), http.StatusOK, "")
	if found, err := e.people.Lookup(ctx, renamed); err != nil || found.ID != acct.ID {
		t.Fatalf("rename changed the account ID: %v", err)
	}
	member.expect(member.do("GET", "/api/account", nil), http.StatusUnauthorized, "login_required")
	member = signInForAdminTest(e, renamed, replacement.Password)
	operator.expect(operator.post(adminAccountsPath, adminAction{Action: "signout", ID: acct.ID, Confirmed: true}), http.StatusOK, "")
	member.expect(member.do("GET", "/api/account", nil), http.StatusUnauthorized, "login_required")

	if _, err := e.people.DB.Exec(`INSERT INTO login_failures (key, count, since) VALUES (?, 10, ?), ('ip:192.0.2.1', 30, ?)`,
		"email:"+renamed, time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	operator.expect(operator.post(adminAccountsPath, adminAction{Action: "unlock", ID: acct.ID, Confirmed: true}), http.StatusOK, "")
	var remaining int
	if err := e.people.DB.QueryRow(`SELECT count(*) FROM login_failures WHERE key IN (?, 'ip:192.0.2.1')`, "email:"+renamed).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("individual unlock left %d target failures, want the address only: %v", remaining, err)
	}
	operator.expect(operator.post(adminAccountsPath, adminAction{Action: "unlock_all", Confirmed: true}), http.StatusOK, "")
	if err := e.people.DB.QueryRow(`SELECT count(*) FROM login_failures`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("global unlock left %d failures: %v", remaining, err)
	}

	member = signInForAdminTest(e, renamed, replacement.Password)
	owner := auth.WithUser(ctx, auth.User{ID: acct.ID, Email: renamed})
	p, err := e.ps.Create(owner, "Member's project")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Trash(owner, p.ID); err != nil {
		t.Fatal(err)
	}
	operator.expect(operator.post(adminAccountsPath, adminAction{Action: "remove", ID: acct.ID, Confirmed: true}), http.StatusOK, "")
	if _, err := e.people.Lookup(ctx, renamed); !errors.Is(err, users.ErrNoAccount) {
		t.Fatalf("deleted account still exists: %v", err)
	}
	if _, err := os.Stat(e.ps.Path(p.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("project in the trash survived account removal")
	}
	member.expect(member.do("GET", "/api/account", nil), http.StatusUnauthorized, "login_required")
}

func TestAdminRemovePreflightsAndReportsPartialDeletion(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "busy project leaves everything intact"
		if partial {
			name = "filesystem failure reports what was removed"
		}
		t.Run(name, func(t *testing.T) {
			e := newAccountEnv(t, envOpts{noMail: true})
			ctx := context.Background()
			admin, err := e.people.SetPassword(ctx, "admin@example.com", "fixture admin password")
			if err != nil {
				t.Fatal(err)
			}
			member, err := e.people.SetPassword(ctx, "member@example.com", "fixture member password")
			if err != nil {
				t.Fatal(err)
			}
			owner := auth.WithUser(ctx, member)
			first, err := e.ps.Create(owner, "A first project")
			if err != nil {
				t.Fatal(err)
			}
			second, err := e.ps.Create(owner, "Z second project")
			if err != nil {
				t.Fatal(err)
			}
			if partial {
				gone := filepath.Join(e.ps.Dir, ".purge-"+second.ID)
				if err := os.Mkdir(gone, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(gone, "fixture"), []byte("blocked"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				e.ps.HoldTurns = func(id string) (func(), bool) { return func() {}, id != second.ID }
			}
			operator := signInForAdminTest(e, admin.Email, "fixture admin password")
			res := operator.post(adminAccountsPath, adminAction{Action: "remove", ID: member.ID, Confirmed: true})
			if partial {
				operator.expect(res, http.StatusConflict, "account_removal_partial")
				var failure struct {
					Error struct {
						Removed int `json:"removed_projects"`
					}
				}
				if err := json.Unmarshal([]byte(res.Body), &failure); err != nil || failure.Error.Removed != 1 {
					t.Fatalf("response did not say that one project was deleted: %s (%v)", res.Body, err)
				}
				if _, err := e.ps.Get(ctx, first.ID); err == nil {
					t.Fatal("reported partial deletion did not remove the first project")
				}
			} else {
				operator.expect(res, http.StatusConflict, "admin_account_busy")
				if _, err := e.ps.Get(ctx, first.ID); err != nil {
					t.Fatalf("preflight deleted the first project: %v", err)
				}
			}
			if _, err := e.ps.Get(ctx, second.ID); err != nil {
				t.Fatalf("the second project was lost: %v", err)
			}
			if _, err := e.people.AccountByID(ctx, member.ID); err != nil {
				t.Fatalf("the account was lost: %v", err)
			}
		})
	}
}

type reportedListener struct {
	net.Listener
	addr net.Addr
}

func (l reportedListener) Addr() net.Addr { return l.addr }

func TestAdminWithoutAuthRequiresLoopbackListener(t *testing.T) {
	t.Setenv("REFRACT_ALLOWED_HOSTS", "localhost")
	for _, variable := range []string{"REFRACT_MODE", "REFRACT_PROFILE", "REFRACT_SANDBOX", "REFRACT_OUTBOUND"} {
		t.Setenv(variable, "")
	}
	for _, tc := range []struct {
		name string
		ip   net.IP
		want int
	}{{"loopback", net.IPv4(127, 0, 0, 1), http.StatusOK}, {"wildcard", net.IPv4zero, http.StatusNotFound}} {
		t.Run(tc.name, func(t *testing.T) {
			// The test itself always binds loopback. Only the address
			// reported to the startup policy changes.
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ln.Close() })
			addr := ln.Addr().(*net.TCPAddr)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := make(chan struct{})
			done := make(chan error, 1)
			data := t.TempDir()
			go func() {
				done <- run(ctx, runOptions{dataDir: data, key: make([]byte, 32), authKind: "none",
					listen: func() (net.Listener, error) {
						return reportedListener{ln, &net.TCPAddr{IP: tc.ip, Port: addr.Port}}, nil
					},
					ready: func(context.Context, net.Addr) { close(ready) },
				})
			}()
			select {
			case <-ready:
			case err := <-done:
				t.Fatalf("server stopped before readiness: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("server did not start")
			}
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("server shutdown: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("server did not stop")
				}
			})
			client := &http.Client{Timeout: 5 * time.Second}
			for _, path := range []string{"/api/me", adminAccountsPath} {
				req, _ := http.NewRequest(http.MethodGet, "http://"+addr.String()+path, nil)
				req.Header.Set("X-Refract", "1")
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				if path == "/api/me" {
					var me struct {
						IsAdmin bool `json:"is_admin"`
					}
					if err := json.NewDecoder(res.Body).Decode(&me); err != nil || me.IsAdmin != (tc.want == http.StatusOK) {
						t.Errorf("/api/me: is_admin = %v (%v)", me.IsAdmin, err)
					}
				} else if res.StatusCode != tc.want {
					t.Errorf("admin GET = %d, want %d", res.StatusCode, tc.want)
				}
				res.Body.Close()
			}
		})
	}
}

func TestAdminReportMatchesCLIAndOwner(t *testing.T) {
	e := newAccountEnv(t, envOpts{noMail: true})
	ctx := context.Background()
	t.Setenv("REFRACT_DATA_DIR", filepath.Dir(e.ps.Dir))
	t.Setenv("REFRACT_SECRET_KEY", base64.StdEncoding.EncodeToString(e.people.Key))
	t.Setenv("REFRACT_SECRET_KEY_FILE", "")
	create := func(email string) auth.User {
		t.Helper()
		u, err := e.people.SetPassword(ctx, email, "fixture report password")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin, member := create("admin@example.com"), create("member@example.com")
	operator := signInForAdminTest(e, admin.Email, "fixture report password")
	ownerBrowser := signInForAdminTest(e, member.Email, "fixture report password")
	owner := auth.WithUser(ctx, member)
	p, err := e.ps.Create(owner, "Report project")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := e.ps.StorageBytes(ctx, p.ID)
	if err != nil || initial == 0 {
		t.Fatalf("initial repository size = %d, %v", initial, err)
	}
	if err := e.ps.WriteFile(owner, p.ID, "main.tex", strings.NewReader("source")); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(e.ps.Path(p.ID), ".refract")
	if err := os.Mkdir(build, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(build, "generated.pdf"), []byte("ignored build output"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &usage.Recorder{DB: e.people.DB, Now: func() time.Time { return time.Unix(100, 0) }}
	for _, user := range []auth.User{member, admin} {
		if err := recorder.Record(ctx, usage.Entry{UserID: user.ID, Kind: "quick", Model: user.Email,
			Usage: ai.Usage{InputTokens: 12, CachedTokens: 2, OutputTokens: 5}}); err != nil {
			t.Fatal(err)
		}
	}
	r := operator.do("GET", adminAccountsPath+"/report?id="+url.QueryEscape(member.ID)+"&since=100", nil)
	var webReport accountReport
	if err := json.Unmarshal([]byte(r.Body), &webReport); r.Status != http.StatusOK || err != nil ||
		len(webReport.Projects) != 1 || webReport.Projects[0].ID != p.ID || webReport.Projects[0].Bytes != initial+6 {
		t.Fatalf("unexpected report %d %+v, %v", r.Status, webReport, err)
	}
	own := ownerBrowser.do("GET", "/api/usage?since=100", nil)
	var ownerReport accountReport
	if err := json.Unmarshal([]byte(own.Body), &ownerReport); own.Status != http.StatusOK || err != nil || len(ownerReport.Models) != 1 ||
		!reflect.DeepEqual(ownerReport.Models, webReport.Models) || ownerReport.Models[0].Model != member.Email {
		t.Fatalf("admin usage differs from account usage: %+v versus %+v (%v)", webReport.Models, ownerReport.Models, err)
	}
	var output bytes.Buffer
	if err := runUsers([]string{"report", member.Email, "100"}, &output); err != nil {
		t.Fatal(err)
	}
	var cli accountReport
	if err := json.NewDecoder(&output).Decode(&cli); err != nil || !reflect.DeepEqual(webReport, cli) {
		t.Fatalf("admin report differs from CLI: %+v versus %+v (%v)", webReport, cli, err)
	}
	if err := runUsers([]string{"admin", member.Email}, io.Discard); err != nil {
		t.Fatal(err)
	}
	ownerBrowser.expect(ownerBrowser.do("GET", adminAccountsPath, nil), http.StatusOK, "")
}

func TestOpenSignupDoesNotBootstrapAdmin(t *testing.T) {
	e := newAccountEnv(t, envOpts{noMail: true})
	ctx := context.Background()
	const email = "public@example.com"
	link, err := e.people.SignUp(ctx, email, "fixture signup password", "en")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.people.GrantAdmin(ctx, email); !errors.Is(err, users.ErrAdminEmail) {
		t.Fatalf("unconfirmed admin grant: %v", err)
	}
	if err := e.people.MarkVerified(ctx, link.To.ID); err != nil {
		t.Fatal(err)
	}
	member := signInForAdminTest(e, email, "fixture signup password")
	member.expect(member.do("GET", adminAccountsPath, nil), http.StatusNotFound, "")
}
