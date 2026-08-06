// Package email implements the Microsoft 365 email notification channel:
// a one-time OAuth 2.0 device-code login, refresh-token persistence with
// rotation, and sending alert emails via Microsoft Graph (delegated
// Mail.Send, acting as the signed-in user).
package email

import (
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/oauth2"
)

// TokenStore persists an *oauth2.Token as JSON at a fixed path.
type TokenStore struct {
	path string
}

func NewTokenStore(path string) *TokenStore { return &TokenStore{path: path} }

// Load reads the token file. It returns a clear error if the file is missing,
// the JSON is corrupt, or the token has no refresh token (such a token cannot
// be kept alive and is useless to this app).
func (s *TokenStore) Load() (*oauth2.Token, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("email token: read %s: %w", s.path, err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, fmt.Errorf("email token: corrupt %s: %w", s.path, err)
	}
	if tok.RefreshToken == "" {
		return nil, fmt.Errorf("email token: %s has no refresh token", s.path)
	}
	return &tok, nil
}

// Save writes the token atomically (temp file + rename) with 0600 perms.
func (s *TokenStore) Save(tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("email token: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("email token: rename %s: %w", tmp, err)
	}
	return nil
}
