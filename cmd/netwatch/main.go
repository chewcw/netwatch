package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/chewcw/netwatch/internal/app"
	"github.com/chewcw/netwatch/internal/config"
	"github.com/chewcw/netwatch/internal/docker"
	"github.com/chewcw/netwatch/internal/notify"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

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

	client, err := docker.New(cfg.DockerHost)
	if err != nil {
		slog.Error("cannot connect to docker", "err", err)
		os.Exit(1)
	}

	notifier := notify.Log()
	if cfg.Notify != "log" {
		slog.Error("unsupported notifier", "notify", cfg.Notify)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, client, notifier); err != nil {
		slog.Error("app exited with error", "err", err)
		os.Exit(1)
	}
	slog.Info("netwatch stopped")
}
