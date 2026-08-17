package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/chewcw/netwatch/internal/app"
	"github.com/chewcw/netwatch/internal/config"
	"github.com/chewcw/netwatch/internal/docker"
	"github.com/chewcw/netwatch/internal/email"
	"github.com/chewcw/netwatch/internal/notify"
	"github.com/spf13/cobra"
)

// runMonitor implements the `run` command (shared by bare `netwatch`).
func runMonitor(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUsage, err)
	}
	cfg, err = mergeFlags(cfg, cmd)
	if err != nil {
		return err // already wrapped in ErrUsage
	}
	setupLogging(cfg)

	slog.Debug("netwatch starting",
		"targets", cfg.Targets,
		"check_interval", cfg.CheckInterval,
		"alert_after", cfg.AlertAfter,
		"min_rx_traffic", cfg.MinRxTraffic,
		"min_tx_traffic", cfg.MinTxTraffic,
		"docker_host", cfg.DockerHost,
		"notify", cfg.Notify,
		"log_level", cfg.LogLevel,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := docker.New(cfg.DockerHost)
	if err != nil {
		return fmt.Errorf("%w: cannot connect to docker: %v", ErrRuntime, err)
	}

	notifier := buildNotifier(ctx, cfg)
	defer func() {
		if c, ok := notifier.(interface{ Close() }); ok {
			c.Close()
		}
	}()

	if err := app.Run(ctx, cfg, client, notifier); err != nil {
		return fmt.Errorf("%w: %v", ErrRuntime, err)
	}
	slog.Info("netwatch stopped")
	return nil
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
