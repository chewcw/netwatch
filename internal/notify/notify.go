package notify

import (
	"context"
	"log/slog"

	"github.com/chewcw/netwatch/internal/detector"
)

type Notifier interface {
	Notify(ctx context.Context, a detector.Alert) error
}

// Log writes each alert as a structured log line. Future implementations
// (email, Telegram, webhook) implement the same interface.
type logNotifier struct{}

func Log() Notifier { return logNotifier{} }

func (logNotifier) Notify(_ context.Context, a detector.Alert) error {
	slog.Info("alert",
		"target", a.Target,
		"kind", a.Kind.String(),
		"rx_silent", a.SilentRx,
		"tx_silent", a.SilentTx,
		"rx_delta", a.RxDelta,
		"tx_delta", a.TxDelta,
	)
	return nil
}
