package config

import (
	"strings"
	"testing"
	"time"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k := range kv {
		t.Setenv(k, kv[k])
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, map[string]string{"NETWATCH_TARGETS": "a"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CheckInterval != 30*time.Second {
		t.Errorf("CheckInterval = %v, want 30s", cfg.CheckInterval)
	}
	if cfg.AlertAfter != 90*time.Second {
		t.Errorf("AlertAfter = %v, want 90s", cfg.AlertAfter)
	}
	if cfg.MinTraffic != 0 {
		t.Errorf("MinTraffic = %d, want 0", cfg.MinTraffic)
	}
	if cfg.DockerHost != "unix:///var/run/docker.sock" {
		t.Errorf("DockerHost = %q", cfg.DockerHost)
	}
	if cfg.Notify != "log" || cfg.LogLevel != "info" {
		t.Errorf("Notify=%q LogLevel=%q", cfg.Notify, cfg.LogLevel)
	}
}

func TestLoadOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"NETWATCH_TARGETS":        "a, b, ,c",
		"NETWATCH_CHECK_INTERVAL": "15s",
		"NETWATCH_ALERT_AFTER":    "1m",
		"NETWATCH_MIN_TRAFFIC":    "512",
		"NETWATCH_DOCKER_HOST":    "tcp://127.0.0.1:2375",
		"NETWATCH_LOG_LEVEL":      "debug",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Targets) != 3 || cfg.Targets[0] != "a" || cfg.Targets[1] != "b" || cfg.Targets[2] != "c" {
		t.Errorf("Targets = %v", cfg.Targets)
	}
	if cfg.CheckInterval != 15*time.Second || cfg.AlertAfter != time.Minute {
		t.Errorf("intervals = %v / %v", cfg.CheckInterval, cfg.AlertAfter)
	}
	if cfg.MinTraffic != 512 || cfg.DockerHost != "tcp://127.0.0.1:2375" || cfg.LogLevel != "debug" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no targets", map[string]string{}, "NETWATCH_TARGETS"},
		{"only commas", map[string]string{"NETWATCH_TARGETS": ", ,"}, "NETWATCH_TARGETS"},
		{"bad interval", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_CHECK_INTERVAL": "fast"}, "NETWATCH_CHECK_INTERVAL"},
		{"zero interval", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_CHECK_INTERVAL": "0s"}, "NETWATCH_CHECK_INTERVAL"},
		{"negative traffic", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_MIN_TRAFFIC": "-5"}, "NETWATCH_MIN_TRAFFIC"},
		{"bad level", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_LOG_LEVEL": "loud"}, "NETWATCH_LOG_LEVEL"},
		{"bad notify", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_NOTIFY": "pigeon"}, "NETWATCH_NOTIFY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want failure mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
