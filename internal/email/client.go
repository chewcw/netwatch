package email

import (
	"fmt"
	"os"
	"time"
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
