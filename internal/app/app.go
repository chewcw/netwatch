package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/chewcw/go-monitoring/internal/config"
	"github.com/chewcw/go-monitoring/internal/detector"
	"github.com/chewcw/go-monitoring/internal/docker"
	"github.com/chewcw/go-monitoring/internal/notify"
)

// Run runs the monitoring loop until ctx is cancelled. stats and n are
// injected for testability. Returns nil on clean shutdown.
func Run(ctx context.Context, cfg config.Config, stats docker.StatsClient, n notify.Notifier) error {
	ticks := thresholdTicks(cfg.AlertAfter, cfg.CheckInterval)
	dets := make(map[string]*detector.Detector, len(cfg.Targets))
	for _, name := range cfg.Targets {
		dets[name] = detector.New(name, ticks, cfg.MinTraffic)
	}
	if err := warnUnknownTargets(ctx, stats, cfg.Targets); err != nil {
		slog.Warn("startup target check failed", "err", err)
	}

	ticker := time.NewTicker(cfg.CheckInterval)
	defer ticker.Stop()

	cycle(ctx, stats, dets, n, cfg.CheckInterval) // immediate first cycle
	for {
		select {
		case <-ticker.C:
			cycle(ctx, stats, dets, n, cfg.CheckInterval)
		case <-ctx.Done():
			return nil
		}
	}
}

func cycle(ctx context.Context, stats docker.StatsClient, dets map[string]*detector.Detector, n notify.Notifier, callTimeout time.Duration) {
	for name, d := range dets {
		// Per-call timeout: a hung daemon must degrade to a logged skip,
		// never freeze the ticker loop.
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		rx, tx, err := stats.GetStats(callCtx, name)
		cancel()
		switch {
		case errors.Is(err, docker.ErrNotFound), errors.Is(err, docker.ErrStopped):
			emit(ctx, n, d.FeedDead())
		case err != nil:
			slog.Warn("stats fetch failed (not counted as silence)", "target", name, "err", err)
		default:
			emit(ctx, n, d.Feed(rx, tx))
		}
	}
}

func emit(ctx context.Context, n notify.Notifier, alerts []detector.Alert) {
	for _, a := range alerts {
		notifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := n.Notify(notifyCtx, a); err != nil {
			slog.Warn("notify failed", "target", a.Target, "kind", a.Kind.String(), "err", err)
		}
		cancel()
	}
}

func thresholdTicks(alertAfter, checkInterval time.Duration) int {
	if checkInterval <= 0 || alertAfter <= 0 {
		return 1
	}
	ticks := int((alertAfter + checkInterval - 1) / checkInterval)
	if ticks < 1 {
		return 1
	}
	return ticks
}

func warnUnknownTargets(ctx context.Context, stats docker.StatsClient, targets []string) error {
	lister, ok := stats.(interface {
		ListContainers(ctx context.Context, all bool) ([]string, error)
	})
	if !ok {
		return nil // client does not support listing; skip check
	}
	names, err := lister.ListContainers(ctx, true)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	for _, t := range targets {
		if !known[t] {
			slog.Warn("configured target not found on daemon (still watching)", "target", t)
		}
	}
	return nil
}
