package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chewcw/netwatch/internal/app"
	"github.com/chewcw/netwatch/internal/config"
	"github.com/chewcw/netwatch/internal/docker"
	"github.com/chewcw/netwatch/internal/email"
	"github.com/chewcw/netwatch/internal/notify"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	setupLogging(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch command() {
	case "auth-login":
		if err := email.Login(ctx, emailCfg(cfg), 10*time.Minute); err != nil {
			slog.Error("auth-login failed", "err", err)
			os.Exit(1)
		}
		return
	case "test-email":
		if err := email.SendTest(ctx, emailCfg(cfg)); err != nil {
			slog.Error("test-email failed", "err", err)
			os.Exit(1)
		}
		fmt.Println("test email sent OK")
		return
	}

	client, err := docker.New(cfg.DockerHost)
	if err != nil {
		slog.Error("cannot connect to docker", "err", err)
		os.Exit(1)
	}

	notifier := buildNotifier(ctx, cfg)
	defer func() {
		if c, ok := notifier.(interface{ Close() }); ok {
			c.Close()
		}
	}()

	if err := app.Run(ctx, cfg, client, notifier); err != nil {
		slog.Error("app exited with error", "err", err)
		os.Exit(1)
	}
	slog.Info("netwatch stopped")
}

// command returns the subcommand; the default (no args or "run") is the
// monitoring loop.
func command() string {
	if len(os.Args) < 2 || os.Args[1] == "run" {
		return "run"
	}
	switch os.Args[1] {
	case "auth-login", "test-email":
		return os.Args[1]
	}
	fmt.Fprintf(os.Stderr, "unknown command %q (expected run, auth-login, test-email)\n", os.Args[1])
	os.Exit(2)
	return ""
}

// buildNotifier assembles the notifier chain from the configured channels.
// The log channel is always first; email (if enabled) is appended and its
// sends are async, so it never blocks the monitoring loop.
func buildNotifier(ctx context.Context, cfg config.Config) notify.Notifier {
	ns := []notify.Notifier{notify.Log()}
	for _, ch := range cfg.Notify {
		if ch == "email" {
			ns = append(ns, email.New(ctx, emailCfg(cfg)))
		}
	}
	return notify.Multi(ns...)
}

func emailCfg(cfg config.Config) email.Config {
	return email.Config{
		TenantID:    cfg.EmailTenantID,
		ClientID:    cfg.EmailClientID,
		To:          cfg.EmailTo,
		TokenFile:   cfg.EmailTokenFile,
		KeepAlive:   cfg.EmailKeepAlive,
		RetryWindow: cfg.EmailRetryWindow,
		Host:        cfg.EmailHost,
	}
}

func setupLogging(cfg config.Config) {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
