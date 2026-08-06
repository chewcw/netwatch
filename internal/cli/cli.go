package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chewcw/netwatch/internal/config"
	"github.com/spf13/cobra"
)

// ErrUsage marks errors caused by invalid invocation or configuration;
// ExitCode maps them to 2.
var ErrUsage = errors.New("usage error")

// ErrRuntime marks runtime failures (docker, app, email); ExitCode maps
// them to 1.
var ErrRuntime = errors.New("runtime error")

// version is injected at build time via
//
//	-ldflags "-X github.com/chewcw/netwatch/internal/cli.version=v1.2.3"
//
// and exposed through cobra's built-in --version flag. Default: dev.
var version = "dev"

// NewRootCommand builds the netwatch command tree. The root command and the
// "run" subcommand share runMonitor, so bare `netwatch` and `netwatch run`
// behave identically (bare is what the Dockerfile ENTRYPOINT invokes).
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "netwatch",
		Short:         "Monitor Docker container network traffic and alert on silence",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          runMonitor,
	}
	// Flag errors (bad --check-interval, unknown --foo) are usage errors.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w: %v", ErrUsage, err)
	})
	registerRunFlags(root)

	run := &cobra.Command{
		Use:   "run",
		Short: "Run the monitoring loop (default when no subcommand is given)",
		RunE:  runMonitor,
	}
	registerRunFlags(run)
	root.AddCommand(run)

	root.AddCommand(&cobra.Command{
		Use:   "auth-login",
		Short: "Complete a one-time Microsoft device-code login for email alerts",
		RunE:  runAuthLogin,
	})
	root.AddCommand(&cobra.Command{
		Use:   "test-email",
		Short: "Send a test email through the configured channel",
		RunE:  runTestEmail,
	})
	return root
}

// registerRunFlags adds the 7 core flags to a command. They are registered
// on both root and run (not persistent) so auth-login/test-email stay
// flag-free while bare `netwatch` and `netwatch run` accept them.
func registerRunFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringSlice("targets", nil, "comma-separated container names to monitor (overrides NETWATCH_TARGETS)")
	f.Duration("check-interval", 0, "poll interval; default 30s (overrides NETWATCH_CHECK_INTERVAL)")
	f.Duration("alert-after", 0, "silence duration before alerting; default 3x check-interval (overrides NETWATCH_ALERT_AFTER)")
	f.Uint64("min-traffic", 0, "bytes/tick below which a tick counts as silent; default 0 (overrides NETWATCH_MIN_TRAFFIC)")
	f.String("docker-host", "", "docker daemon endpoint; default unix:///var/run/docker.sock (overrides NETWATCH_DOCKER_HOST)")
	f.StringSlice("notify", nil, "comma-separated channels: log,email; default log (overrides NETWATCH_NOTIFY)")
	f.String("log-level", "", "log level: debug,info,warn,error; default info (overrides NETWATCH_LOG_LEVEL)")
}

// mergeFlags applies explicitly-passed flags over the env-loaded config:
// flag > env > default. Flag-registered defaults (0 / nil / empty) are
// cosmetic help text only — only Changed() gates an override, and the
// authoritative defaults come from config.Load().
func mergeFlags(cfg config.Config, cmd *cobra.Command) (config.Config, error) {
	f := cmd.Flags()
	if f.Changed("targets") {
		v, _ := f.GetStringSlice("targets")
		cfg.Targets = splitCsv(v)
	}
	if f.Changed("check-interval") {
		v, _ := f.GetDuration("check-interval")
		cfg.CheckInterval = v
	}
	if f.Changed("alert-after") {
		v, _ := f.GetDuration("alert-after")
		cfg.AlertAfter = v
	}
	if f.Changed("min-traffic") {
		v, _ := f.GetUint64("min-traffic")
		cfg.MinTraffic = v
	}
	if f.Changed("docker-host") {
		v, _ := f.GetString("docker-host")
		cfg.DockerHost = v
	}
	if f.Changed("notify") {
		v, _ := f.GetStringSlice("notify")
		cfg.Notify = splitCsv(v)
	}
	if f.Changed("log-level") {
		v, _ := f.GetString("log-level")
		cfg.LogLevel = v
	}

	// Invariant revalidation for what flags can break. Env-only invariants
	// (email tenant/client required when email enabled) are untouched.
	if len(cfg.Targets) == 0 {
		return cfg, fmt.Errorf("%w: targets: empty after flag merge", ErrUsage)
	}
	if len(cfg.Notify) == 0 {
		return cfg, fmt.Errorf("%w: notify: no channels after flag merge", ErrUsage)
	}
	for _, ch := range cfg.Notify {
		if ch != "log" && ch != "email" {
			return cfg, fmt.Errorf("%w: notify: unsupported channel %q", ErrUsage, ch)
		}
	}
	if cfg.CheckInterval <= 0 {
		return cfg, fmt.Errorf("%w: check-interval: must be positive, got %v", ErrUsage, cfg.CheckInterval)
	}
	if cfg.AlertAfter <= 0 {
		return cfg, fmt.Errorf("%w: alert-after: must be positive, got %v", ErrUsage, cfg.AlertAfter)
	}
	return cfg, nil
}

func splitCsv(in []string) []string {
	var out []string
	for _, p := range in {
		for _, q := range strings.Split(p, ",") {
			if q = strings.TrimSpace(q); q != "" {
				out = append(out, q)
			}
		}
	}
	return out
}

// ExitCode maps an Execute() error to the process exit code, preserving the
// pre-cobra contract: 0 clean, 2 config/usage, 1 runtime.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, ErrUsage) || strings.HasPrefix(err.Error(), "unknown command ") {
		return 2
	}
	return 1
}
