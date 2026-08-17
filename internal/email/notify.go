package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/chewcw/netwatch/internal/detector"
)

// New builds the email notifier. If the token file is missing or unusable it
// logs loudly and returns a notifier with authOK=false: Notify then skips
// sends (the chained log channel still delivers) and the app keeps running.
// When a token is present, a keep-alive goroutine refreshes it every
// cfg.KeepAlive (first refresh immediate), which defeats the 90-day
// inactivity revocation and detects token death within KeepAlive hours.
func New(ctx context.Context, cfg Config) *notifier {
	cfg = cfg.defaulted()
	n := &notifier{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
	n.ctx, n.cancel = context.WithCancel(ctx)
	store := NewTokenStore(cfg.TokenFile)
	ts, err := newTokenSource(n.ctx, cfg, store)
	if err != nil {
		slog.Error("email: no usable token — email notifications disabled until `netwatch auth-login` is run", "err", err)
		return n
	}
	n.ts = ts
	n.authOK = true
	n.startKeepAlive()
	return n
}

// Notify enqueues an asynchronous send and returns immediately, so the
// monitoring loop never blocks on Graph. If the passed ctx is already
// cancelled (shutting down) or no token is available, the alert is dropped —
// the chained log channel is the fallback.
func (n *notifier) Notify(ctx context.Context, a detector.Alert) error {
	if ctx.Err() != nil || !n.authOK {
		slog.Debug("email: notify skipped", "target", a.Target, "kind", a.Kind.String(), "ctx_err", ctx.Err(), "auth_ok", n.authOK)
		return nil
	}
	slog.Debug("email: notify queued", "target", a.Target, "kind", a.Kind.String())
	subj, body := buildMessage(n.cfg.Host, a)
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		if err := n.sendWithRetry(n.ctx, subj, body); err != nil {
			if errors.Is(err, ErrAuthDead) {
				slog.Warn("email skipped (auth dead)", "target", a.Target, "kind", a.Kind.String())
			} else {
				slog.Error("email send failed", "target", a.Target, "kind", a.Kind.String(), "err", err)
			}
		}
	}()
	return nil
}

// Close stops the keep-alive goroutine and waits for in-flight sends.
func (n *notifier) Close() {
	n.cancel()
	n.wg.Wait()
}

// SendTest sends a test message synchronously (with the same retry policy as
// alerts). It fails fast when no token is available.
func (n *notifier) SendTest(ctx context.Context) error {
	if !n.authOK {
		return errors.New("email: no token — run `netwatch auth-login` first")
	}
	subj := fmt.Sprintf("[netwatch: %s] TEST email", n.cfg.Host)
	body := "This is a test message from netwatch.\nIf you received this, the email notification channel is working."
	return n.sendWithRetry(ctx, subj, body)
}

// SendTest is the package-level helper used by the test-email subcommand.
func SendTest(ctx context.Context, cfg Config) error {
	n := New(ctx, cfg)
	defer n.Close()
	return n.SendTest(ctx)
}

// sendWithRetry performs one send attempt per loop iteration, retrying
// retryable failures (429/5xx/network, and a 401 after one force-refresh)
// with exponential backoff until the retry window expires.
func (n *notifier) sendWithRetry(ctx context.Context, subj, body string) error {
	deadline := time.Now().Add(n.cfg.RetryWindow)
	backoff := 1 * time.Second
	refreshed := false
	for {
		lastErr := n.trySend(ctx, subj, body, &refreshed)
		if lastErr == nil {
			return nil
		}
		if errors.Is(lastErr, ErrAuthDead) || errors.Is(lastErr, errPermanent) {
			return lastErr
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("email: giving up after %s: %w", n.cfg.RetryWindow, lastErr)
		}
		slog.Debug("email: send attempt failed, retrying", "subject", subj, "err", lastErr, "backoff", backoff)
		if !sleepCtx(ctx, backoff) {
			return ctx.Err()
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// trySend performs one send attempt, force-refreshing once when Graph
// rejects the access token with 401.
func (n *notifier) trySend(ctx context.Context, subj, body string, refreshed *bool) error {
	tok, err := n.ts.Token(ctx)
	if err != nil {
		return err // ErrAuthDead or a transient refresh failure
	}
	err = n.sendOnce(ctx, tok, subj, body)
	if err == nil {
		return nil
	}
	if errors.Is(err, errUnauthorized) && !*refreshed {
		*refreshed = true
		tok, rerr := n.ts.ForceRefresh(ctx)
		if rerr != nil {
			return rerr
		}
		return n.sendOnce(ctx, tok, subj, body)
	}
	return err
}

// startKeepAlive refreshes the token immediately, then on every KeepAlive
// interval. A successful refresh keeps the token out of the 90-day
// inactivity window; invalid_grant is logged loudly once.
func (n *notifier) startKeepAlive() {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		if _, err := n.ts.Token(n.ctx); err != nil {
			n.logAuthError(err)
		}
		t := time.NewTicker(n.cfg.KeepAlive)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if _, err := n.ts.Token(n.ctx); err != nil {
					n.logAuthError(err)
				}
			case <-n.ctx.Done():
				return
			}
		}
	}()
}

func (n *notifier) logAuthError(err error) {
	if errors.Is(err, ErrAuthDead) {
		slog.Error("email: token rejected by Microsoft — re-run `netwatch auth-login` and restart the container", "err", err)
	} else {
		slog.Warn("email: token refresh failed (will retry)", "err", err)
	}
}

// buildMessage renders the plain-text subject and body for an alert.
func buildMessage(host string, a detector.Alert) (subject, body string) {
	action := "NOTICE"
	explanation := ""
	switch a.Kind {
	case detector.KindAlerted:
		action = "ALERT"
		explanation = "The collector is not sending and/or not receiving data — check the sensor-side and/or cloud-side path."
	case detector.KindRecovered:
		action = "RECOVERED"
		explanation = "The collector is sending data again."
	case detector.KindDead:
		action = "DOWN"
		explanation = "The container is not running — check the Docker daemon and container restart policy."
	case detector.KindBack:
		action = "BACK"
		explanation = "The container is running again."
	}
	subject = fmt.Sprintf("[netwatch: %s] %s: %s", host, action, a.Target)
	body = fmt.Sprintf(
		"host:    %s\ntarget:  %s\nkind:    %s\ntime:    %s\nrx:      %d bytes since last check (silent: %t)\ntx:      %d bytes since last check (silent: %t)\n\n%s",
		host, a.Target, strings.ToLower(a.Kind.String()), a.At.UTC().Format(time.RFC3339),
		a.RxDelta, a.SilentRx, a.TxDelta, a.SilentTx, explanation)
	return subject, body
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
