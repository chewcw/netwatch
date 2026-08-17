package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// authScopes are the delegated Graph scopes the app requests. offline_access
// yields the refresh token that keeps the session alive.
var authScopes = []string{"Mail.Send", "offline_access"}

const (
	graphEndpointDefault = "https://graph.microsoft.com"
	deviceAuthURLTpl     = "https://login.microsoftonline.com/%s/oauth2/v2.0/devicecode"
	tokenURLTpl          = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
)

// Config carries everything the email channel needs. Endpoint fields default
// to Microsoft's endpoints and exist so unit tests can point them at
// httptest servers.
type Config struct {
	TenantID    string
	ClientID    string
	To          []string
	TokenFile   string
	KeepAlive   time.Duration
	RetryWindow time.Duration
	Host        string

	// Endpoint overrides for tests. Empty values get Microsoft defaults.
	DeviceAuthURL string
	TokenURL      string
	GraphBaseURL  string
}

// defaulted returns a copy of cfg with empty fields filled with defaults.
func (c Config) defaulted() Config {
	if c.DeviceAuthURL == "" {
		c.DeviceAuthURL = fmt.Sprintf(deviceAuthURLTpl, c.TenantID)
	}
	if c.TokenURL == "" {
		c.TokenURL = fmt.Sprintf(tokenURLTpl, c.TenantID)
	}
	if c.GraphBaseURL == "" {
		c.GraphBaseURL = graphEndpointDefault
	}
	if c.KeepAlive <= 0 {
		c.KeepAlive = 12 * time.Hour
	}
	if c.RetryWindow <= 0 {
		c.RetryWindow = 5 * time.Minute
	}
	if c.Host == "" {
		if h, err := os.Hostname(); err == nil {
			c.Host = h
		}
	}
	return c
}

// ErrAuthDead is returned when Microsoft rejects the refresh token
// (invalid_grant): password changed, user deactivated, consent revoked, or
// Conditional Access blocked the app. The user must re-run `netwatch
// auth-login` and restart.
var ErrAuthDead = errors.New("email auth dead: re-run netwatch auth-login")

// Sentinel classification errors from a single send attempt.
var (
	errUnauthorized = errors.New("email: access token rejected (401)")
	errPermanent    = errors.New("email: request rejected by Graph (4xx)")
	errRetryable    = errors.New("email: transient send failure")
)

// persistingTokenSource refreshes access tokens via x/oauth2 and re-saves the
// rotated token after every refresh. invalid_grant on refresh marks the source
// dead (ErrAuthDead) until the operator re-authenticates.
type persistingTokenSource struct {
	mu       sync.Mutex
	src      oauth2.TokenSource
	store    *TokenStore
	clientID string
	tokenURL string
	dead     bool
}

// newTokenSource loads the stored token and wires an x/oauth2 refresh source.
// It returns an error only when the token file is missing or unusable.
func newTokenSource(ctx context.Context, cfg Config, store *TokenStore) (*persistingTokenSource, error) {
	tok, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &persistingTokenSource{
		src:      oauthConfig(cfg).TokenSource(ctx, tok),
		store:    store,
		clientID: cfg.ClientID,
		tokenURL: cfg.TokenURL,
	}, nil
}

func oauthConfig(cfg Config) *oauth2.Config {
	return &oauth2.Config{
		ClientID: cfg.ClientID,
		Endpoint: oauth2.Endpoint{TokenURL: cfg.TokenURL},
	}
}

// Token returns a valid access token, refreshing and re-persisting as needed.
func (s *persistingTokenSource) Token(ctx context.Context) (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return nil, ErrAuthDead
	}
	tok, err := s.src.Token()
	if err != nil {
		if isInvalidGrant(err) {
			s.dead = true
			return nil, ErrAuthDead
		}
		slog.Debug("email: access token refresh failed", "err", err)
		return nil, err
	}
	if err := s.store.Save(tok); err != nil {
		slog.Warn("email: could not persist refreshed token", "err", err)
	}
	slog.Debug("email: access token refreshed")
	return tok, nil
}

// ForceRefresh performs a fresh refresh-token grant regardless of access-token
// expiry. It is used when Graph rejects a still-unexpired access token (401).
func (s *persistingTokenSource) ForceRefresh(ctx context.Context) (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return nil, ErrAuthDead
	}
	cur, err := s.store.Load()
	if err != nil {
		return nil, fmt.Errorf("email: force refresh: %w", err)
	}
	// A token carrying only the refresh token is never valid, so x/oauth2
	// issues an immediate refresh grant; Microsoft rotates the refresh token
	// in the response and x/oauth2 carries it into the returned token.
	fresh := &oauth2.Token{RefreshToken: cur.RefreshToken}
	tok, err := oauthConfig(s.config()).TokenSource(ctx, fresh).Token()
	if err != nil {
		if isInvalidGrant(err) {
			s.dead = true
			return nil, ErrAuthDead
		}
		return nil, err
	}
	s.src = oauthConfig(s.config()).TokenSource(ctx, tok)
	if err := s.store.Save(tok); err != nil {
		slog.Warn("email: could not persist rotated token", "err", err)
	}
	return tok, nil
}

func (s *persistingTokenSource) config() Config {
	return Config{ClientID: s.clientID, TokenURL: s.tokenURL}
}

func isInvalidGrant(err error) bool {
	var re *oauth2.RetrieveError
	return errors.As(err, &re) && re.ErrorCode == "invalid_grant"
}

// sendMailRequest is the JSON body for POST /v1.0/me/sendMail.
type sendMailRequest struct {
	Message struct {
		Subject string `json:"subject"`
		Body    struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		} `json:"body"`
		ToRecipients []struct {
			EmailAddress struct {
				Address string `json:"address"`
			} `json:"emailAddress"`
		} `json:"toRecipients"`
	} `json:"message"`
	SaveToSentItems bool `json:"saveToSentItems"`
}

// notifier sends alert emails asynchronously. It is defined here so sendOnce
// can live next to the HTTP layer; Task 4 fills in the lifecycle (New, Notify,
// Close, keep-alive).
type notifier struct {
	cfg    Config
	http   *http.Client
	ts     *persistingTokenSource
	authOK bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// sendOnce posts one message to Graph. It classifies the response: nil on
// 2xx, errUnauthorized on 401, errPermanent on other 4xx, errRetryable on
// 429/5xx/network errors.
func (n *notifier) sendOnce(ctx context.Context, tok *oauth2.Token, subject, body string) error {
	req := sendMailRequest{}
	req.Message.Subject = subject
	req.Message.Body.ContentType = "Text"
	req.Message.Body.Content = body
	for _, to := range n.cfg.To {
		req.Message.ToRecipients = append(req.Message.ToRecipients, struct {
			EmailAddress struct {
				Address string `json:"address"`
			} `json:"emailAddress"`
		}{EmailAddress: struct {
			Address string `json:"address"`
		}{Address: to}})
	}
	req.SaveToSentItems = true

	b, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errPermanent, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.cfg.GraphBaseURL+"/v1.0/me/sendMail", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%w: %v", errRetryable, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := n.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", errRetryable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted:
		slog.Debug("email: sent", "subject", subject, "status", resp.StatusCode)
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return errUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP %d", errRetryable, resp.StatusCode)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return fmt.Errorf("%w: HTTP %d", errPermanent, resp.StatusCode)
	default:
		return fmt.Errorf("%w: HTTP %d", errRetryable, resp.StatusCode)
	}
}
