package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chewcw/netwatch/internal/config"
	"github.com/spf13/cobra"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"usage sentinel", ErrUsage, 2},
		{"runtime sentinel", ErrRuntime, 1},
		{"wrapped usage", wrapUsage("bad"), 2},
		{"wrapped runtime", wrapRuntime("boom"), 1},
		{"unknown command prefix", errors.New(`unknown command "auth" for "netwatch"`), 2},
		{"plain error", errors.New("other"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestNewRootCommand(t *testing.T) {
	root := NewRootCommand()
	if root.Use != "netwatch" {
		t.Errorf("root.Use = %q, want netwatch", root.Use)
	}
	if root.RunE == nil {
		t.Error("root.RunE is nil — bare `netwatch` must run the monitor")
	}
	for _, want := range []string{"run", "auth-login", "test-email"} {
		sub, _, err := root.Find([]string{want})
		if err != nil || sub == nil {
			t.Errorf("missing subcommand %q (err=%v)", want, err)
		}
	}
	// Note: cobra registers the `completion` subcommand lazily inside
	// ExecuteC (command.go:1113), not at construction — its presence is
	// verified by the `--help` smoke test (Task 2 Step 7).
	// run shares the runMonitor behavior: both root and run must expose the
	// 7 core flags.
	for _, name := range []string{"targets", "check-interval", "alert-after", "min-traffic", "docker-host", "notify", "log-level"} {
		if root.Flags().Lookup(name) == nil {
			t.Errorf("root missing flag --%s", name)
		}
		if runCmd(root).Flags().Lookup(name) == nil {
			t.Errorf("run missing flag --%s", name)
		}
	}
	// auth-login / test-email must NOT accept core flags.
	for _, name := range []string{"auth-login", "test-email"} {
		sub, _, _ := root.Find([]string{name})
		if sub.Flags().Lookup("targets") != nil {
			t.Errorf("%s unexpectedly accepts --targets", name)
		}
	}
}

func runCmd(root *cobra.Command) *cobra.Command {
	sub, _, _ := root.Find([]string{"run"})
	return sub
}

func TestMergeFlagsPrecedence(t *testing.T) {
	setEnv(t, map[string]string{
		"NETWATCH_TARGETS":        "env-a,env-b",
		"NETWATCH_CHECK_INTERVAL": "60s",
		"NETWATCH_ALERT_AFTER":    "5m",
		"NETWATCH_MIN_TRAFFIC":    "100",
		"NETWATCH_DOCKER_HOST":    "unix:///tmp/env.sock",
		"NETWATCH_NOTIFY":         "log",
		"NETWATCH_LOG_LEVEL":      "warn",
	})
	envCfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	t.Run("flags win over env", func(t *testing.T) {
		root := NewRootCommand()
		args := []string{"--targets", "a,b", "--check-interval", "45s",
			"--alert-after", "2m", "--min-traffic", "7",
			"--docker-host", "tcp://host:2375", "--notify", "log,email",
			"--log-level", "debug"}
		root.SetArgs(args)
		if err := root.ParseFlags(args); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		got, err := mergeFlags(envCfg, root)
		if err != nil {
			t.Fatalf("mergeFlags: %v", err)
		}
		if len(got.Targets) != 2 || got.Targets[0] != "a" || got.Targets[1] != "b" {
			t.Errorf("Targets = %v, want [a b]", got.Targets)
		}
		if got.CheckInterval != 45*time.Second {
			t.Errorf("CheckInterval = %v, want 45s", got.CheckInterval)
		}
		if got.AlertAfter != 2*time.Minute {
			t.Errorf("AlertAfter = %v, want 2m", got.AlertAfter)
		}
		if got.MinTraffic != 7 {
			t.Errorf("MinTraffic = %d, want 7", got.MinTraffic)
		}
		if got.DockerHost != "tcp://host:2375" {
			t.Errorf("DockerHost = %q, want tcp://host:2375", got.DockerHost)
		}
		if len(got.Notify) != 2 || got.Notify[0] != "log" || got.Notify[1] != "email" {
			t.Errorf("Notify = %v, want [log email]", got.Notify)
		}
		if got.LogLevel != "debug" {
			t.Errorf("LogLevel = %q, want debug", got.LogLevel)
		}
	})

	t.Run("env wins when flag absent", func(t *testing.T) {
		root := NewRootCommand()
		if err := root.ParseFlags(nil); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		got, err := mergeFlags(envCfg, root)
		if err != nil {
			t.Fatalf("mergeFlags: %v", err)
		}
		if got.CheckInterval != 60*time.Second {
			t.Errorf("CheckInterval = %v, want env 60s", got.CheckInterval)
		}
		if got.AlertAfter != 5*time.Minute {
			t.Errorf("AlertAfter = %v, want env 5m", got.AlertAfter)
		}
		if got.MinTraffic != 100 {
			t.Errorf("MinTraffic = %d, want env 100", got.MinTraffic)
		}
		if got.LogLevel != "warn" {
			t.Errorf("LogLevel = %q, want env warn", got.LogLevel)
		}
	})

	t.Run("defaults when neither flag nor env", func(t *testing.T) {
		// setEnv only sets what's in the map; drop the interval keys by
		// clearing them explicitly.
		t.Setenv("NETWATCH_CHECK_INTERVAL", "")
		t.Setenv("NETWATCH_MIN_TRAFFIC", "")
		t.Setenv("NETWATCH_LOG_LEVEL", "")
		base, err := config.Load()
		if err != nil {
			t.Fatalf("config.Load() error = %v", err)
		}
		root := NewRootCommand()
		if err := root.ParseFlags(nil); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		got, err := mergeFlags(base, root)
		if err != nil {
			t.Fatalf("mergeFlags: %v", err)
		}
		if got.CheckInterval != 30*time.Second {
			t.Errorf("CheckInterval = %v, want default 30s", got.CheckInterval)
		}
		if got.MinTraffic != 0 {
			t.Errorf("MinTraffic = %d, want default 0", got.MinTraffic)
		}
		if got.LogLevel != "info" {
			t.Errorf("LogLevel = %q, want default info", got.LogLevel)
		}
	})
}

func TestMergeFlagsAlertAfterCascade(t *testing.T) {
	// --check-interval without --alert-after keeps the env-derived
	// alert-after (no surprise recompute from the flag).
	setEnv(t, map[string]string{
		"NETWATCH_TARGETS":        "a",
		"NETWATCH_CHECK_INTERVAL": "30s",
	})
	envCfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	root := NewRootCommand()
	args := []string{"--check-interval", "45s"}
	root.SetArgs(args)
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got, err := mergeFlags(envCfg, root)
	if err != nil {
		t.Fatalf("mergeFlags: %v", err)
	}
	if got.CheckInterval != 45*time.Second {
		t.Errorf("CheckInterval = %v, want 45s", got.CheckInterval)
	}
	if got.AlertAfter != 90*time.Second {
		t.Errorf("AlertAfter = %v, want env-derived 90s (3x 30s), got recomputed or flag default", got.AlertAfter)
	}
}

func TestMergeFlagsInvariants(t *testing.T) {
	base := func() config.Config {
		return config.Config{Targets: []string{"a"}, CheckInterval: 30 * time.Second,
			AlertAfter: 90 * time.Second, Notify: []string{"log"}}
	}
	tests := []struct {
		name    string
		args    []string
		mut     func(*config.Config)
		badCfg  config.Config
		wantErr bool
	}{
		{"empty targets flag", []string{"--targets", " , , "}, nil, config.Config{}, true},
		{"bad notify channel", []string{"--notify", "sms"}, nil, config.Config{}, true},
		{"empty notify flag", []string{"--notify", " "}, nil, config.Config{}, true},
		{"negative interval", []string{"--check-interval", "-5s"}, nil, config.Config{}, true},
		{"zero alert-after", []string{"--alert-after", "0s"}, nil, config.Config{}, true},
		{"valid flags pass", []string{"--min-traffic", "3"}, nil, config.Config{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			root := NewRootCommand()
			root.SetArgs(tt.args)
			if err := root.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			_, err := mergeFlags(cfg, root)
			if (err != nil) != tt.wantErr {
				t.Errorf("mergeFlags() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUnknownCommandExitCode(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"bogus"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() on unknown command succeeded, want error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, want unknown command message", err)
	}
	if got := ExitCode(err); got != 2 {
		t.Errorf("ExitCode = %d, want 2", got)
	}
}

func TestFlagParseErrorExitCode(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"--check-interval", "abc"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() with bad duration succeeded, want error")
	}
	if got := ExitCode(err); got != 2 {
		t.Errorf("ExitCode = %d, want 2", got)
	}
}

func wrapUsage(err string) error   { return &usageError{err} }
func wrapRuntime(err string) error { return &runtimeError{err} }

type usageError struct{ s string }

func (e *usageError) Error() string { return e.s }
func (e *usageError) Unwrap() error { return ErrUsage }

type runtimeError struct{ s string }

func (e *runtimeError) Error() string { return e.s }
func (e *runtimeError) Unwrap() error { return ErrRuntime }
