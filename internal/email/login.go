package email

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"
)

// Login runs the OAuth 2.0 device code flow and saves the resulting token to
// cfg.TokenFile. It prints the verification URL and user code to stdout, then
// blocks polling until the user approves on another device. timeout bounds the
// whole flow (10 minutes is a sensible value for a human to notice the code).
func Login(ctx context.Context, cfg Config, timeout time.Duration) error {
	cfg = cfg.defaulted()
	ocfg := &oauth2.Config{
		ClientID: cfg.ClientID,
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: cfg.DeviceAuthURL,
			TokenURL:      cfg.TokenURL,
		},
		Scopes: authScopes,
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	da, err := ocfg.DeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("email device auth: %w", err)
	}
	fmt.Printf("Visit %s and enter code: %s\n", da.VerificationURI, da.UserCode)

	tok, err := ocfg.DeviceAccessToken(ctx, da)
	if err != nil {
		return fmt.Errorf("email device flow: %w", err)
	}
	if err := NewTokenStore(cfg.TokenFile).Save(tok); err != nil {
		return err
	}
	fmt.Printf("OK — token saved to %s\n", cfg.TokenFile)
	return nil
}
