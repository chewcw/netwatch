package notify

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/chewcw/netwatch/internal/detector"
)

func TestLogNotifier(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	n := Log()
	err := n.Notify(context.Background(), detector.Alert{
		Target: "sensor-collector", Kind: detector.KindAlerted,
		SilentRx: true, SilentTx: true,
	})
	if err != nil {
		t.Fatalf("Notify error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "kind=alerted") || !strings.Contains(out, "sensor-collector") {
		t.Errorf("log output missing fields: %q", out)
	}
}
