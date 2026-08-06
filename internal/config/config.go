package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Targets       []string
	CheckInterval time.Duration
	AlertAfter    time.Duration
	MinTraffic    uint64
	DockerHost    string
	Notify        []string
	LogLevel      string

	EmailTenantID    string
	EmailClientID    string
	EmailTo          []string
	EmailTokenFile   string
	EmailKeepAlive   time.Duration
	EmailRetryWindow time.Duration
	EmailHost        string
}

func Load() (Config, error) {
	var cfg Config

	raw := strings.TrimSpace(os.Getenv("NETWATCH_TARGETS"))
	if raw == "" {
		return cfg, fmt.Errorf("NETWATCH_TARGETS: required, comma-separated container names")
	}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.Targets = append(cfg.Targets, p)
		}
	}
	if len(cfg.Targets) == 0 {
		return cfg, fmt.Errorf("NETWATCH_TARGETS: no valid container names after parsing %q", raw)
	}

	interval, err := durationEnv("NETWATCH_CHECK_INTERVAL", 30*time.Second)
	if err != nil {
		return cfg, err
	}
	cfg.CheckInterval = interval

	alertAfter, err := durationEnv("NETWATCH_ALERT_AFTER", 3*interval)
	if err != nil {
		return cfg, err
	}
	cfg.AlertAfter = alertAfter

	mt, err := uintEnv("NETWATCH_MIN_TRAFFIC", 0)
	if err != nil {
		return cfg, err
	}
	cfg.MinTraffic = mt

	cfg.DockerHost = strEnv("NETWATCH_DOCKER_HOST", "unix:///var/run/docker.sock")

	notifyRaw := os.Getenv("NETWATCH_NOTIFY")
	var notify []string
	if notifyRaw == "" {
		notify = []string{"log"}
	} else {
		for _, p := range strings.Split(notifyRaw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				notify = append(notify, p)
			}
		}
	}
	if len(notify) == 0 {
		return cfg, fmt.Errorf("NETWATCH_NOTIFY: no channels after parsing %q", notifyRaw)
	}
	for _, ch := range notify {
		switch ch {
		case "log", "email":
		default:
			return cfg, fmt.Errorf("NETWATCH_NOTIFY: unsupported channel %q", ch)
		}
	}
	cfg.Notify = notify

	cfg.LogLevel = strEnv("NETWATCH_LOG_LEVEL", "info")

	cfg.EmailKeepAlive, err = durationEnv("NETWATCH_EMAIL_KEEPALIVE", 12*time.Hour)
	if err != nil {
		return cfg, err
	}
	cfg.EmailRetryWindow, err = durationEnv("NETWATCH_EMAIL_RETRY_WINDOW", 5*time.Minute)
	if err != nil {
		return cfg, err
	}
	cfg.EmailHost = strEnv("NETWATCH_EMAIL_HOST", "")

	emailEnabled := false
	for _, ch := range notify {
		if ch == "email" {
			emailEnabled = true
			break
		}
	}
	if emailEnabled {
		cfg.EmailTenantID = strings.TrimSpace(os.Getenv("NETWATCH_EMAIL_TENANT_ID"))
		if cfg.EmailTenantID == "" {
			return cfg, fmt.Errorf("NETWATCH_EMAIL_TENANT_ID: required when email channel is enabled")
		}
		cfg.EmailClientID = strings.TrimSpace(os.Getenv("NETWATCH_EMAIL_CLIENT_ID"))
		if cfg.EmailClientID == "" {
			return cfg, fmt.Errorf("NETWATCH_EMAIL_CLIENT_ID: required when email channel is enabled")
		}
		for _, p := range strings.Split(os.Getenv("NETWATCH_EMAIL_TO"), ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.EmailTo = append(cfg.EmailTo, p)
			}
		}
		if len(cfg.EmailTo) == 0 {
			return cfg, fmt.Errorf("NETWATCH_EMAIL_TO: required, comma-separated recipients, when email channel is enabled")
		}
		cfg.EmailTokenFile = strings.TrimSpace(os.Getenv("NETWATCH_EMAIL_TOKEN_FILE"))
		if cfg.EmailTokenFile == "" {
			return cfg, fmt.Errorf("NETWATCH_EMAIL_TOKEN_FILE: required when email channel is enabled")
		}
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return cfg, fmt.Errorf("NETWATCH_LOG_LEVEL: unsupported %q", cfg.LogLevel)
	}
	return cfg, nil
}

// LoadEmail reads only the email configuration from the environment,
// skipping all core validation (targets, notify channels, log level,
// docker). It backs the auth-login and test-email subcommands, which need
// email settings but no monitoring configuration.
func LoadEmail() (Config, error) {
	var cfg Config

	cfg.EmailTenantID = strings.TrimSpace(os.Getenv("NETWATCH_EMAIL_TENANT_ID"))
	if cfg.EmailTenantID == "" {
		return cfg, fmt.Errorf("NETWATCH_EMAIL_TENANT_ID: required")
	}
	cfg.EmailClientID = strings.TrimSpace(os.Getenv("NETWATCH_EMAIL_CLIENT_ID"))
	if cfg.EmailClientID == "" {
		return cfg, fmt.Errorf("NETWATCH_EMAIL_CLIENT_ID: required")
	}
	cfg.EmailTokenFile = strings.TrimSpace(os.Getenv("NETWATCH_EMAIL_TOKEN_FILE"))
	if cfg.EmailTokenFile == "" {
		return cfg, fmt.Errorf("NETWATCH_EMAIL_TOKEN_FILE: required")
	}
	for _, p := range strings.Split(os.Getenv("NETWATCH_EMAIL_TO"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.EmailTo = append(cfg.EmailTo, p)
		}
	}

	keepAlive, err := durationEnv("NETWATCH_EMAIL_KEEPALIVE", 12*time.Hour)
	if err != nil {
		return cfg, err
	}
	cfg.EmailKeepAlive = keepAlive

	retryWindow, err := durationEnv("NETWATCH_EMAIL_RETRY_WINDOW", 5*time.Minute)
	if err != nil {
		return cfg, err
	}
	cfg.EmailRetryWindow = retryWindow

	cfg.EmailHost = strEnv("NETWATCH_EMAIL_HOST", "")
	return cfg, nil
}

func strEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func durationEnv(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %v", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: must be positive, got %q", key, v)
	}
	return d, nil
}

func uintEnv(key string, def uint64) (uint64, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %v", key, err)
	}
	return n, nil
}
