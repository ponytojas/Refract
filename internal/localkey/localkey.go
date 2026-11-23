// Package localkey keeps the secret key of `refract local` in the operating
// system's credential store: macOS Keychain, the Secret Service on Linux
// (GNOME Keyring, KWallet) and Windows Credential Manager.
package localkey

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/ponytojas/Refract/internal/store"
)

// Service is the name every item is stored under; the account tells data
// folders apart.
const Service = "Refract"

// ErrNotFound means the store works but holds no item for the account.
var ErrNotFound = errors.New("no item in the credential store")

// ErrInvalid means the stored item is not a valid secret key.
var ErrInvalid = errors.New("invalid secret key in the credential store")

// Store is a credential store. Get returns ErrNotFound for a missing item and
// any other error when the store can't be reached.
type Store interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
}

// Account is the item name for a data folder: a hash of its absolute path,
// with symbolic links resolved so one folder reached through two paths keeps
// one key, and two folders keep two keys; the path itself isn't exposed.
func Account(dataDir string) string {
	if real, err := filepath.EvalSymlinks(dataDir); err == nil {
		dataDir = real
	}
	sum := sha256.Sum256([]byte(dataDir))
	return "data-" + hex.EncodeToString(sum[:8])
}

// Load reads the key of dataDir. found is false, with a nil error, when the
// store has no item. A stored value that isn't a valid key is an error, so it
// is never replaced by accident.
func Load(s Store, dataDir string) (key []byte, found bool, err error) {
	v, err := s.Get(Service, Account(dataDir))
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	key, err = store.ParseKey(strings.TrimSpace(v))
	if err != nil {
		return nil, false, fmt.Errorf("%w: item %q; delete it to let Refract make a new one (stored passwords and API keys then become unreadable)", ErrInvalid, Account(dataDir))
	}
	return key, true, nil
}

// Save stores key for dataDir.
func Save(s Store, dataDir string, key []byte) error {
	return s.Set(Service, Account(dataDir), encode(key))
}

// callTimeout bounds a call to the system store: a locked Linux keyring can
// wait for a prompt nobody sees.
const callTimeout = 10 * time.Second

type system struct{}

// System is the operating system's credential store, through
// github.com/zalando/go-keyring (no cgo).
func System() Store { return system{} }

func (system) Get(service, account string) (string, error) {
	return withTimeout(func() (string, error) {
		v, err := keyring.Get(service, account)
		if errors.Is(err, keyring.ErrNotFound) {
			err = ErrNotFound
		}
		return v, err
	})
}

func (system) Set(service, account, secret string) error {
	_, err := withTimeout(func() (string, error) { return "", keyring.Set(service, account, secret) })
	return err
}

func withTimeout(f func() (string, error)) (string, error) {
	type result struct {
		v   string
		err error
	}
	ch := make(chan result, 1)
	go func() { v, err := f(); ch <- result{v, err} }()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(callTimeout):
		return "", errors.New("the credential store did not answer")
	}
}

func encode(key []byte) string { return base64.StdEncoding.EncodeToString(key) }
