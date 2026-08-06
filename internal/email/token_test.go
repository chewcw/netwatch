package email

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	s := NewTokenStore(path)
	want := &oauth2.Token{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != "at-1" || got.RefreshToken != "rt-1" {
		t.Errorf("got access=%q refresh=%q, want at-1/rt-1", got.AccessToken, got.RefreshToken)
	}
}

func TestTokenStoreMissingFile(t *testing.T) {
	s := NewTokenStore(filepath.Join(t.TempDir(), "nope.json"))
	if _, err := s.Load(); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestTokenStoreCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTokenStore(path).Load(); err == nil {
		t.Fatal("expected error for corrupt file")
	}
}

func TestTokenStoreRequiresRefreshToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	s := NewTokenStore(path)
	if err := s.Save(&oauth2.Token{AccessToken: "at-only"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("expected error for token without refresh token")
	}
}

func TestTokenStorePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	if err := NewTokenStore(path).Save(&oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("perms = %o, want 600", got)
	}
}

func TestTokenStoreNoTempLeftBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	if err := NewTokenStore(path).Save(&oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf(".tmp file left behind: %v", err)
	}
}
