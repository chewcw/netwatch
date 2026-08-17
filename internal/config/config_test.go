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
	if cfg.MinRxTraffic != 0 {
		t.Errorf("MinRxTraffic = %d, want 0", cfg.MinRxTraffic)
	}
	if cfg.MinTxTraffic != 0 {
		t.Errorf("MinTxTraffic = %d, want 0", cfg.MinTxTraffic)
	}
	if cfg.DockerHost != "unix:///var/run/docker.sock" {
		t.Errorf("DockerHost = %q", cfg.DockerHost)
	}
	if len(cfg.Notify) != 1 || cfg.Notify[0] != "log" || cfg.LogLevel != "info" {
		t.Errorf("Notify=%v LogLevel=%q", cfg.Notify, cfg.LogLevel)
	}
}

func TestLoadOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"NETWATCH_TARGETS":        "a, b, ,c",
		"NETWATCH_CHECK_INTERVAL": "15s",
		"NETWATCH_ALERT_AFTER":    "1m",
		"NETWATCH_MIN_TRAFFIC_RX": "512",
		"NETWATCH_MIN_TRAFFIC_TX": "128",
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
	if cfg.MinRxTraffic != 512 || cfg.MinTxTraffic != 128 || cfg.DockerHost != "tcp://127.0.0.1:2375" || cfg.LogLevel != "debug" {
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
		{"negative rx traffic", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_MIN_TRAFFIC_RX": "-5"}, "NETWATCH_MIN_TRAFFIC_RX"},
		{"negative tx traffic", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_MIN_TRAFFIC_TX": "-5"}, "NETWATCH_MIN_TRAFFIC_TX"},
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

func TestLoadEmailChannel(t *testing.T) {
	env := map[string]string{
		"NETWATCH_TARGETS":            "a",
		"NETWATCH_NOTIFY":             "email",
		"NETWATCH_EMAIL_TENANT_ID":    "tenant-1",
		"NETWATCH_EMAIL_CLIENT_ID":    "client-1",
		"NETWATCH_EMAIL_TO":           "me@example.com, ops@example.com",
		"NETWATCH_EMAIL_TOKEN_FILE":   "/data/t.json",
		"NETWATCH_EMAIL_KEEPALIVE":    "6h",
		"NETWATCH_EMAIL_RETRY_WINDOW": "2m",
		"NETWATCH_EMAIL_HOST":         "edge-pc-7",
	}
	setEnv(t, env)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Notify) != 1 || cfg.Notify[0] != "email" {
		t.Errorf("Notify = %v, want [email]", cfg.Notify)
	}
	if cfg.EmailTenantID != "tenant-1" || cfg.EmailClientID != "client-1" {
		t.Errorf("tenant/client = %q/%q", cfg.EmailTenantID, cfg.EmailClientID)
	}
	if len(cfg.EmailTo) != 2 || cfg.EmailTo[0] != "me@example.com" || cfg.EmailTo[1] != "ops@example.com" {
		t.Errorf("EmailTo = %v", cfg.EmailTo)
	}
	if cfg.EmailTokenFile != "/data/t.json" || cfg.EmailKeepAlive != 6*time.Hour || cfg.EmailRetryWindow != 2*time.Minute || cfg.EmailHost != "edge-pc-7" {
		t.Errorf("email cfg = %+v", cfg)
	}
}

func TestLoadEmailDefaults(t *testing.T) {
	env := map[string]string{
		"NETWATCH_TARGETS":          "a",
		"NETWATCH_NOTIFY":           "log, email",
		"NETWATCH_EMAIL_TENANT_ID":  "t",
		"NETWATCH_EMAIL_CLIENT_ID":  "c",
		"NETWATCH_EMAIL_TO":         "me@example.com",
		"NETWATCH_EMAIL_TOKEN_FILE": "/data/t.json",
	}
	setEnv(t, env)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Notify) != 2 || cfg.Notify[0] != "log" || cfg.Notify[1] != "email" {
		t.Errorf("Notify = %v, want [log email]", cfg.Notify)
	}
	if cfg.EmailKeepAlive != 12*time.Hour || cfg.EmailRetryWindow != 5*time.Minute {
		t.Errorf("defaults = keepalive %s retry %s, want 12h0m0s/5m0s", cfg.EmailKeepAlive, cfg.EmailRetryWindow)
	}
}

func TestLoadEmailMissingRequired(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no tenant", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_NOTIFY": "email",
			"NETWATCH_EMAIL_CLIENT_ID": "c", "NETWATCH_EMAIL_TO": "m@e.com", "NETWATCH_EMAIL_TOKEN_FILE": "/d/t.json"}, "NETWATCH_EMAIL_TENANT_ID"},
		{"no client", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_NOTIFY": "email",
			"NETWATCH_EMAIL_TENANT_ID": "t", "NETWATCH_EMAIL_TO": "m@e.com", "NETWATCH_EMAIL_TOKEN_FILE": "/d/t.json"}, "NETWATCH_EMAIL_CLIENT_ID"},
		{"no to", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_NOTIFY": "email",
			"NETWATCH_EMAIL_TENANT_ID": "t", "NETWATCH_EMAIL_CLIENT_ID": "c", "NETWATCH_EMAIL_TOKEN_FILE": "/d/t.json"}, "NETWATCH_EMAIL_TO"},
		{"no token file", map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_NOTIFY": "email",
			"NETWATCH_EMAIL_TENANT_ID": "t", "NETWATCH_EMAIL_CLIENT_ID": "c", "NETWATCH_EMAIL_TO": "m@e.com"}, "NETWATCH_EMAIL_TOKEN_FILE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setEnv(t, c.env)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want substring %q", err, c.want)
			}
		})
	}
}

func TestLoadNotifyEmptyList(t *testing.T) {
	setEnv(t, map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_NOTIFY": " , "})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "NETWATCH_NOTIFY") {
		t.Errorf("err = %v, want NETWATCH_NOTIFY error", err)
	}
}

func TestLoadNotifyBadChannel(t *testing.T) {
	setEnv(t, map[string]string{"NETWATCH_TARGETS": "a", "NETWATCH_NOTIFY": "pigeon"})
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "pigeon") {
		t.Errorf("err = %v, want unsupported channel error", err)
	}
}

func TestLoadEmail(t *testing.T) {
	setEnv(t, map[string]string{
		"NETWATCH_EMAIL_TENANT_ID":  "t-id",
		"NETWATCH_EMAIL_CLIENT_ID":  "c-id",
		"NETWATCH_EMAIL_TOKEN_FILE": "/data/token.json",
		"NETWATCH_EMAIL_TO":         "a@example.com, b@example.com",
		"NETWATCH_EMAIL_KEEPALIVE":  "2h",
	})
	cfg, err := LoadEmail()
	if err != nil {
		t.Fatalf("LoadEmail() error = %v", err)
	}
	if cfg.EmailTenantID != "t-id" {
		t.Errorf("EmailTenantID = %q, want t-id", cfg.EmailTenantID)
	}
	if cfg.EmailClientID != "c-id" {
		t.Errorf("EmailClientID = %q, want c-id", cfg.EmailClientID)
	}
	if cfg.EmailTokenFile != "/data/token.json" {
		t.Errorf("EmailTokenFile = %q, want /data/token.json", cfg.EmailTokenFile)
	}
	if len(cfg.EmailTo) != 2 || cfg.EmailTo[0] != "a@example.com" || cfg.EmailTo[1] != "b@example.com" {
		t.Errorf("EmailTo = %v, want [a@example.com b@example.com]", cfg.EmailTo)
	}
	if cfg.EmailKeepAlive != 2*time.Hour {
		t.Errorf("EmailKeepAlive = %v, want 2h", cfg.EmailKeepAlive)
	}
}

func TestLoadEmailIgnoresCoreEnv(t *testing.T) {
	// TARGETS and NOTIFY must be irrelevant to LoadEmail: auth-login /
	// test-email work without them.
	setEnv(t, map[string]string{
		"NETWATCH_EMAIL_TENANT_ID":  "t-id",
		"NETWATCH_EMAIL_CLIENT_ID":  "c-id",
		"NETWATCH_EMAIL_TOKEN_FILE": "/data/token.json",
	})
	cfg, err := LoadEmail()
	if err != nil {
		t.Fatalf("LoadEmail() error = %v", err)
	}
	if len(cfg.Targets) != 0 || len(cfg.Notify) != 0 {
		t.Errorf("LoadEmail populated core fields: Targets=%v Notify=%v", cfg.Targets, cfg.Notify)
	}
}

func TestLoadEmailSubDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"NETWATCH_EMAIL_TENANT_ID":  "t-id",
		"NETWATCH_EMAIL_CLIENT_ID":  "c-id",
		"NETWATCH_EMAIL_TOKEN_FILE": "/data/token.json",
	})
	cfg, err := LoadEmail()
	if err != nil {
		t.Fatalf("LoadEmail() error = %v", err)
	}
	if cfg.EmailKeepAlive != 12*time.Hour {
		t.Errorf("EmailKeepAlive = %v, want default 12h", cfg.EmailKeepAlive)
	}
	if cfg.EmailRetryWindow != 5*time.Minute {
		t.Errorf("EmailRetryWindow = %v, want default 5m", cfg.EmailRetryWindow)
	}
	if cfg.EmailHost != "" {
		t.Errorf("EmailHost = %q, want empty default", cfg.EmailHost)
	}
}

func TestLoadEmailRequired(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"tenant":    {"NETWATCH_EMAIL_CLIENT_ID": "c-id", "NETWATCH_EMAIL_TOKEN_FILE": "/data/token.json"},
		"client":    {"NETWATCH_EMAIL_TENANT_ID": "t-id", "NETWATCH_EMAIL_TOKEN_FILE": "/data/token.json"},
		"tokenfile": {"NETWATCH_EMAIL_TENANT_ID": "t-id", "NETWATCH_EMAIL_CLIENT_ID": "c-id"},
	} {
		t.Run(name, func(t *testing.T) {
			setEnv(t, kv)
			if _, err := LoadEmail(); err == nil {
				t.Fatalf("LoadEmail() succeeded, want error for missing %s", name)
			}
		})
	}
}
