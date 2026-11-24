package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ponytojas/Refract/internal/siwc"
)

// runLogin is `refract login`: sign in with ChatGPT on this machine (the
// redirect must reach 127.0.0.1) and write a credential file to upload to the server.
func runLogin(args []string) error {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(cfgDir, "refract")
	fl := flag.NewFlagSet("login", flag.ExitOnError)
	out := fl.String("out", filepath.Join(dir, "credentials.json"), "credential file to write")
	fresh := fl.Bool("new", false, "register a new account instead of reusing the saved one")
	noBrowser := fl.Bool("no-browser", false, "print the sign-in URL instead of opening a browser")
	fl.Parse(args)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	hostID, err := loadOrCreate(filepath.Join(dir, "host-id"), siwc.NewHostID)
	if err != nil {
		return err
	}
	var saved *siwc.Credentials
	if !*fresh {
		if raw, err := os.ReadFile(*out); err == nil {
			saved = new(siwc.Credentials)
			if err := json.Unmarshal(raw, saved); err != nil || saved.ClientID == "" {
				return fmt.Errorf("%s is not a credential file (use -new or another -out)", *out)
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c, err := siwc.Login(ctx, siwc.LoginOptions{
		HostID: hostID,
		Saved:  saved,
		OAuth:  siwc.NewOAuth(ctx),
		OpenBrowser: func(u string) error {
			if *noBrowser {
				return errors.New("-no-browser")
			}
			return openBrowser(u)
		},
		OnURL: func(u string, openErr error) {
			if openErr != nil {
				fmt.Println("Open this address in your browser to continue with ChatGPT:\n\n  " + u + "\n")
				return
			}
			fmt.Println("Continue with ChatGPT in your browser.")
		},
		// Keep the issued registration if the exchange fails, so running login
		// again reuses it. Never overwrite an existing credential file with it.
		OnRegistration: func(clientID string) error {
			if _, err := os.Stat(*out); err == nil {
				return nil
			}
			raw, _ := json.MarshalIndent(siwc.Credentials{ClientID: clientID}, "", "  ")
			return writeFileAtomic(*out, raw)
		},
	})
	if err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(c, "", "  ")
	if err := writeFileAtomic(*out, raw); err != nil {
		return err
	}

	fmt.Printf("Signed in as %s\n  subject: %s\n  credentials: %s\n\n", c.Email, c.Subject, *out)
	if !c.PlanGranted() {
		fmt.Println("ChatGPT plan usage was NOT granted, so Refract cannot make AI requests.\nRun `refract login` again and allow plan usage.")
		return nil
	}
	fmt.Printf("Next:\n  1. Set REFRACT_ALLOWED_SUBJECT=%s on the server.\n  2. Upload %s in Refract (Settings → ChatGPT).\n\nThe file holds live tokens: don't commit or share it.\n", c.Subject, *out)
	return nil
}

func loadOrCreate(path string, gen func() string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	v := gen()
	return v, writeFileAtomic(path, []byte(v+"\n"))
}

// writeFileAtomic writes an owner-only file via rename, so a crash never leaves a half-written token file.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the launcher, which exits once the browser has the URL
	return nil
}
