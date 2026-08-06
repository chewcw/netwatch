package cli

import (
	"fmt"
	"time"

	"github.com/chewcw/netwatch/internal/config"
	"github.com/chewcw/netwatch/internal/email"
	"github.com/spf13/cobra"
)

// runAuthLogin drives the one-time Microsoft device-code login. It loads
// email config only — no TARGETS, no notify checks, no docker.
func runAuthLogin(cmd *cobra.Command, _ []string) error {
	cfg, err := config.LoadEmail()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUsage, err)
	}
	if err := email.Login(cmd.Context(), emailCfg(cfg), 10*time.Minute); err != nil {
		return fmt.Errorf("%w: %v", ErrRuntime, err)
	}
	return nil
}

// runTestEmail sends one test message through the configured email channel.
// Like auth-login it loads email config only.
func runTestEmail(cmd *cobra.Command, _ []string) error {
	cfg, err := config.LoadEmail()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUsage, err)
	}
	if len(cfg.EmailTo) == 0 {
		return fmt.Errorf("%w: NETWATCH_EMAIL_TO: required to send a test email", ErrUsage)
	}
	if err := email.SendTest(cmd.Context(), emailCfg(cfg)); err != nil {
		return fmt.Errorf("%w: %v", ErrRuntime, err)
	}
	fmt.Println("test email sent OK")
	return nil
}
