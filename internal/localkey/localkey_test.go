package localkey

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fake struct {
	items map[string]string
	err   error // returned by every call when set
}

func (f *fake) Get(service, account string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.items[service+"/"+account]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fake) Set(service, account, secret string) error {
	if f.err != nil {
		return f.err
	}
	if f.items == nil {
		f.items = map[string]string{}
	}
	f.items[service+"/"+account] = secret
	return nil
}

func TestAccountPerFolder(t *testing.T) {
	a, b := Account("/data/one"), Account("/data/two")
	if a == b || a != Account("/data/one") {
		t.Fatalf("accounts %q %q: want stable and distinct", a, b)
	}
	if strings.Contains(a, "one") {
		t.Fatalf("account %q exposes the path", a)
	}
}

func TestSaveLoadRoundTripAndSeparateFolders(t *testing.T) {
	s := &fake{}
	k1, k2 := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	if err := Save(s, "/a", k1); err != nil {
		t.Fatal(err)
	}
	if err := Save(s, "/b", k2); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string][]byte{"/a": k1, "/b": k2} {
		got, found, err := Load(s, dir)
		if err != nil || !found || !bytes.Equal(got, want) {
			t.Fatalf("Load(%s) = %v %v %v", dir, got, found, err)
		}
	}
}

func TestLoadMissingItem(t *testing.T) {
	key, found, err := Load(&fake{}, "/a")
	if key != nil || found || err != nil {
		t.Fatalf("got %v %v %v, want a clean miss", key, found, err)
	}
}

func TestLoadUnreachableStore(t *testing.T) {
	boom := errors.New("no D-Bus session")
	if _, _, err := Load(&fake{err: boom}, "/a"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store's error", err)
	}
}

func TestLoadInvalidItemIsAnError(t *testing.T) {
	s := &fake{}
	s.Set(Service, Account("/a"), "not-a-key")
	_, found, err := Load(s, "/a")
	if err == nil || found {
		t.Fatalf("got %v %v, want an error", found, err)
	}
	if strings.Contains(err.Error(), "not-a-key") {
		t.Fatalf("error %q leaks the stored value", err)
	}
}

func TestAccountResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip(err)
	}
	if Account(dir) != Account(link) {
		t.Fatal("one folder through a symbolic link got another account")
	}
}
