package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chewcw/go-monitoring/internal/config"
	"github.com/chewcw/go-monitoring/internal/detector"
	"github.com/chewcw/go-monitoring/internal/docker"
)

type fakeStats struct {
	seq []statsResult // consumed one per GetStats call
}

type statsResult struct {
	rx, tx uint64
	err    error
}

func (f *fakeStats) GetStats(_ context.Context, _ string) (uint64, uint64, error) {
	if len(f.seq) == 0 {
		return 0, 0, errors.New("no more samples")
	}
	r := f.seq[0]
	f.seq = f.seq[1:]
	return r.rx, r.tx, r.err
}

type fakeNotifier struct {
	mu     sync.Mutex
	alerts []detector.Alert
}

func (f *fakeNotifier) Notify(_ context.Context, a detector.Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts = append(f.alerts, a)
	return nil
}

// snapshot returns a copy of the alerts recorded so far, so callers can
// read them without racing the Run goroutine that writes via Notify.
func (f *fakeNotifier) snapshot() []detector.Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]detector.Alert, len(f.alerts))
	copy(out, f.alerts)
	return out
}

func TestThresholdTicks(t *testing.T) {
	cases := []struct {
		after, interval time.Duration
		want            int
	}{
		{3 * time.Second, 1 * time.Second, 3},
		{1 * time.Second, 2 * time.Second, 1}, // ceil 0.5 -> 1
		{3 * time.Second, 2 * time.Second, 2}, // ceil 1.5 -> 2
		{0, 1 * time.Second, 1},               // min 1
	}
	for _, tc := range cases {
		if got := thresholdTicks(tc.after, tc.interval); got != tc.want {
			t.Errorf("thresholdTicks(%v,%v) = %d, want %d", tc.after, tc.interval, got, tc.want)
		}
	}
}

func TestRunEndToEndSequence(t *testing.T) {
	cfg := config.Config{
		Targets:       []string{"c"},
		CheckInterval: 10 * time.Millisecond,
		AlertAfter:    25 * time.Millisecond, // ceil(25/10)=3 ticks
		MinTraffic:    0,
	}
	// seed, active, silent, silent, silent(->alert), active, active(->recover)
	stats := &fakeStats{seq: []statsResult{
		{rx: 100, tx: 100}, // seed
		{rx: 200, tx: 200}, // active
		{rx: 200, tx: 200}, // silent 1
		{rx: 200, tx: 200}, // silent 2
		{rx: 200, tx: 200}, // silent 3 -> Alerted
		{rx: 300, tx: 300}, // active
		{rx: 400, tx: 400}, // active -> Recovered
	}}
	n := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, stats, n) }()

	waitFor(t, func() bool { return len(n.snapshot()) >= 2 }, 2*time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run error = %v", err)
	}
	alerts := n.snapshot()
	if len(alerts) != 2 {
		t.Fatalf("alerts = %v, want 2", alerts)
	}
	if alerts[0].Kind != detector.KindAlerted || alerts[1].Kind != detector.KindRecovered {
		t.Fatalf("kinds = %v, want [Alerted Recovered]", []detector.Kind{alerts[0].Kind, alerts[1].Kind})
	}
}

func TestRunTransientErrorIsNotSilence(t *testing.T) {
	cfg := config.Config{
		Targets:       []string{"c"},
		CheckInterval: 10 * time.Millisecond,
		AlertAfter:    30 * time.Millisecond,
	}
	// 100 transient errors in a row must NOT produce an alert
	seq := []statsResult{{rx: 100, tx: 100}}
	for i := 0; i < 100; i++ {
		seq = append(seq, statsResult{err: errors.New("daemon hiccup")})
	}
	stats := &fakeStats{seq: seq}
	n := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, stats, n) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done
	if len(n.snapshot()) != 0 {
		t.Fatalf("alerts = %v, want none (transient errors)", n.snapshot())
	}
}

func TestRunDeadAndBack(t *testing.T) {
	cfg := config.Config{
		Targets:       []string{"c"},
		CheckInterval: 10 * time.Millisecond,
		AlertAfter:    25 * time.Millisecond,
	}
	// seed, three not-found/stopped ticks (->Dead), alive (->Back)
	stats := &fakeStats{seq: []statsResult{
		{rx: 100, tx: 100},
		{err: docker.ErrNotFound},
		{err: docker.ErrStopped},
		{err: docker.ErrStopped},
		{rx: 110, tx: 110},
	}}
	n := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, stats, n) }()
	waitFor(t, func() bool { return len(n.snapshot()) >= 2 }, 2*time.Second)
	cancel()
	<-done
	alerts := n.snapshot()
	if len(alerts) != 2 || alerts[0].Kind != detector.KindDead || alerts[1].Kind != detector.KindBack {
		t.Fatalf("alerts = %v, want [Dead Back]", alerts)
	}
}

func TestRunHungDaemonDoesNotStall(t *testing.T) {
	cfg := config.Config{
		Targets:       []string{"c"},
		CheckInterval: 30 * time.Millisecond,
		AlertAfter:    90 * time.Millisecond,
	}
	stats := &hungStats{}
	n := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, stats, n) }()

	// Run must stay responsive across several timeout-bound cycles.
	select {
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(n.snapshot()) != 0 {
		t.Fatalf("alerts = %v, want none", n.snapshot())
	}
}

// hungStats blocks every call until its context is done, simulating a
// daemon that accepts the connection but never answers.
type hungStats struct{}

func (hungStats) GetStats(ctx context.Context, _ string) (uint64, uint64, error) {
	<-ctx.Done()
	return 0, 0, ctx.Err()
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
