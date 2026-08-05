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
	Notify        string
	LogLevel      string
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
	cfg.Notify = strEnv("NETWATCH_NOTIFY", "log")
	cfg.LogLevel = strEnv("NETWATCH_LOG_LEVEL", "info")

	switch cfg.Notify {
	case "log":
	default:
		return cfg, fmt.Errorf("NETWATCH_NOTIFY: unsupported %q (only \"log\" for now)", cfg.Notify)
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return cfg, fmt.Errorf("NETWATCH_LOG_LEVEL: unsupported %q", cfg.LogLevel)
	}
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
