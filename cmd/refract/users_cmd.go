package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/usage"
	"github.com/ponytojas/Refract/internal/users"
)

const usersUsage = `usage:
  refract users add <email>               create an account, or reset its password
  refract users rename <email> <new>      change an account's email, keeping its data
  refract users signout <email>           end all of an account's sessions
  refract users unlock <email> | --all    let an account (or everyone) sign in again after too many failed attempts
  refract users remove <email> [--yes]    delete an account and everything it owns
  refract users admin <email>            grant administrator access to a confirmed account
  refract users report <email> [since]   show model usage and project disk space as JSON
  refract users list                      list accounts`

// runUsers manages accounts on the server's own database (same
// REFRACT_DATA_DIR and encryption key as the server).
func runUsers(args []string, out io.Writer) error {
	arity := map[string][]int{"list": {1}, "add": {2}, "rename": {3}, "signout": {2}, "unlock": {2}, "remove": {2, 3}, "admin": {2}, "report": {2, 3}}
	if len(args) == 0 || !slices.Contains(arity[args[0]], len(args)) || args[0] == "remove" && len(args) == 3 && args[2] != "--yes" {
		return errors.New(usersUsage)
	}
	key, err := secretKeyFromEnv()
	if err != nil {
		return err
	}
	dataDir := cmp.Or(os.Getenv("REFRACT_DATA_DIR"), "/data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(dataDir, "refract.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	svc := &users.Service{DB: db, Key: key}
	ctx := context.Background()
	projectsDir := filepath.Join(dataDir, "projects")

	switch args[0] {
	case "list":
		all, err := svc.AdminAccounts(ctx)
		if err != nil {
			return err
		}
		for _, a := range all {
			state := "password"
			if !a.HasPassword {
				state = "no password"
			}
			fmt.Fprintf(out, "%s\t%s\n", a.Email, state)
		}
	case "rename":
		if err := svc.Rename(ctx, args[1], args[2]); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s is now %s, with all its projects and settings.\n", args[1], args[2])
	case "signout":
		acct, err := svc.Lookup(ctx, args[1])
		if errors.Is(err, users.ErrNoAccount) {
			return fmt.Errorf("there is no account %q", args[1])
		}
		if err != nil {
			return err
		}
		n, err := svc.SignOutAccount(ctx, acct.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Ended %d sessions of %s.\n", n, args[1])
	case "unlock":
		if args[1] == "--all" {
			n, err := svc.UnlockAll(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Cleared %d lockouts (accounts and addresses).\n", n)
			return nil
		}
		ok, err := svc.Unlock(ctx, args[1])
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(out, "%s wasn't locked out.\n", args[1])
			return nil
		}
		fmt.Fprintf(out, "%s can sign in again.\n", args[1])
	case "remove":
		return removeUser(ctx, svc, projectsDir, args[1], len(args) == 3, out)
	case "admin":
		if err := svc.GrantAdmin(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintf(out, "Granted administrator access to %s.\n", args[1])
	case "report":
		acct, err := svc.Lookup(ctx, args[1])
		if err != nil {
			return err
		}
		raw := "0"
		if len(args) == 3 {
			raw = args[2]
		}
		since, err := usageStart(raw)
		if err != nil {
			return err
		}
		a := &adminAPI{users: svc, projects: &projects.Service{Dir: projectsDir}, usage: &usage.Recorder{DB: db}}
		report, err := a.accountUsageAndStorage(ctx, acct.ID, since)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(report)
	case "add":
		// A generated password: nothing to type into a shell history or a
		// terminal without echo control (docker exec without -t).
		u, pw, err := svc.ProvisionPassword(ctx, args[1], false)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s can sign in with this password (change it in Settings → Account):\n\n  %s\n\n", u.Email, pw)
	}
	return nil
}

// removeUser deletes an account with its projects, those in the trash too (chats go with them),
// settings and sessions. Without confirm it only says what would go.
func removeUser(ctx context.Context, svc *users.Service, projectsDir, email string, confirm bool, out io.Writer) error {
	acct, err := svc.Lookup(ctx, email)
	if errors.Is(err, users.ErrNoAccount) {
		return fmt.Errorf("there is no account %q", email)
	}
	if err != nil {
		return err
	}
	owned, err := svc.OwnedProjects(ctx, acct.ID)
	if err != nil {
		return err
	}
	if !confirm {
		fmt.Fprintf(out, "This deletes %s and everything it owns: %d projects, their chats and history,\nits AI connections and its GitHub account.\n", acct.Email, len(owned))
		for _, p := range owned {
			name := p.Name
			if p.Trashed {
				name += " (in the trash)"
			}
			fmt.Fprintf(out, "  - %s\n", name)
		}
		return fmt.Errorf("nothing was deleted: run again with --yes to confirm")
	}
	ps := &projects.Service{DB: svc.DB, Dir: projectsDir}
	n, err := svc.Remove(ctx, acct.ID, ps.DestroyOwnedProjects)
	if err != nil {
		if n > 0 {
			fmt.Fprintf(out, "Deleted %d projects before removal stopped. The account remains.\n", n)
		}
		return err
	}
	fmt.Fprintf(out, "Deleted %s and %d projects.\n", acct.Email, n)
	return nil
}
