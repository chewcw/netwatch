package notify

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/chewcw/netwatch/internal/detector"
)

func TestMulti(t *testing.T) {
	var calls []string
	fake := func(name string, err error) *fakeNotifier {
		return &fakeNotifier{name: name, err: err, log: &calls}
	}
	mu := Multi(fake("a", nil), fake("b", errors.New("boom")), fake("c", nil))
	err := mu.Notify(context.Background(), detector.Alert{Target: "t", Kind: detector.KindAlerted})
	if err == nil || err.Error() != "boom" {
		t.Errorf("Multi err = %v, want boom", err)
	}
	if len(calls) != 3 {
		t.Errorf("called %v, want all 3 notifiers", calls)
	}
}

type fakeNotifier struct {
	name string
	err  error
	log  *[]string
}

func (f *fakeNotifier) Notify(_ context.Context, _ detector.Alert) error {
	*f.log = append(*f.log, f.name)
	return f.err
}

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
