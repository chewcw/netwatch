package email

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/chewcw/netwatch/internal/detector"
)

func TestBuildMessageAlerted(t *testing.T) {
	subj, body := buildMessage("edge1", detector.Alert{
		Target: "collector", Kind: detector.KindAlerted,
		RxDelta: 120, TxDelta: 4096, SilentRx: true, SilentTx: false,
		At: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
	})
	if subj != "[netwatch: edge1] ALERT: collector" {
		t.Errorf("subject = %q", subj)
	}
	for _, want := range []string{
		"host:    edge1", "target:  collector", "kind:    alerted",
		"rx:      120 bytes", "silent: true", "tx:      4096 bytes",
		"check the sensor-side and/or cloud-side path",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestBuildMessageSubjects(t *testing.T) {
	cases := []struct {
		kind detector.Kind
		want string
	}{
		{detector.KindAlerted, "[netwatch: h] ALERT: t"},
		{detector.KindRecovered, "[netwatch: h] RECOVERED: t"},
		{detector.KindDead, "[netwatch: h] DOWN: t"},
		{detector.KindBack, "[netwatch: h] BACK: t"},
	}
	for _, c := range cases {
		subj, _ := buildMessage("h", detector.Alert{Target: "t", Kind: c.kind})
		if subj != c.want {
			t.Errorf("kind %v subject = %q, want %q", c.kind, subj, c.want)
		}
	}
}

func TestBuildMessageExplanations(t *testing.T) {
	cases := []struct {
		kind detector.Kind
		want string
	}{
		{detector.KindRecovered, "sending data again"},
		{detector.KindDead, "not running"},
		{detector.KindBack, "running again"},
	}
	for _, c := range cases {
		_, body := buildMessage("h", detector.Alert{Target: "t", Kind: c.kind})
		if !strings.Contains(body, c.want) {
			t.Errorf("kind %v body missing %q:\n%s", c.kind, c.want, body)
		}
	}
}

func TestNotifyReturnsPromptly(t *testing.T) {
	// A Graph endpoint that hangs must not delay Notify (async send).
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/me/sendMail", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // never completes before test ends
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	n := newTestNotifier(t, srv.URL)
	start := time.Now()
	err := n.Notify(context.Background(), detector.Alert{Target: "t", Kind: detector.KindAlerted})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Notify blocked for %s, want immediate return", d)
	}
}

func TestNotifyAuthDeadSkipsSend(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/me/sendMail", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{GraphBaseURL: srv.URL, TokenFile: filepath.Join(t.TempDir(), "none.json")}
	n := New(context.Background(), cfg) // no token file → authOK=false
	if err := n.Notify(context.Background(), detector.Alert{Target: "t", Kind: detector.KindAlerted}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	n.Close()
	if calls.Load() != 0 {
		t.Errorf("send called %d times with auth dead, want 0", calls.Load())
	}
}

func TestNotify401RefreshesAndRetries(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"at-fresh","refresh_token":"rt-fresh","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/v1.0/me/sendMail", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{
		ClientID:     "cid-1",
		TokenURL:     srv.URL + "/token",
		GraphBaseURL: srv.URL,
		TokenFile:    filepath.Join(t.TempDir(), "t.json"),
		RetryWindow:  10 * time.Second,
	}
	store := NewTokenStore(cfg.TokenFile)
	if err := store.Save(&oauth2.Token{AccessToken: "stale", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	n := New(context.Background(), cfg)
	if err := n.SendTest(context.Background()); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	n.Close()
	if calls.Load() < 2 {
		t.Errorf("sendMail called %d times, want at least 2 (401 then retry)", calls.Load())
	}
}

func TestSendTestNoToken(t *testing.T) {
	cfg := Config{TokenFile: filepath.Join(t.TempDir(), "none.json"), GraphBaseURL: "http://127.0.0.1:1"}
	n := New(context.Background(), cfg)
	defer n.Close()
	if err := n.SendTest(context.Background()); err == nil {
		t.Fatal("expected error when no token")
	}
}

// newTestNotifier builds a notifier with a valid stored token and no
// keep-alive (KeepAlive large so tests are fast and deterministic).
func newTestNotifier(t *testing.T, graphURL string) *notifier {
	t.Helper()
	cfg := Config{
		ClientID:     "cid-1",
		TokenURL:     "http://127.0.0.1:1/token", // unused: token below is valid
		GraphBaseURL: graphURL,
		TokenFile:    filepath.Join(t.TempDir(), "t.json"),
		KeepAlive:    24 * time.Hour,
		RetryWindow:  5 * time.Second,
	}
	store := NewTokenStore(cfg.TokenFile)
	if err := store.Save(&oauth2.Token{
		AccessToken: "at-valid", RefreshToken: "rt-1",
		TokenType: "Bearer", Expiry: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	n := New(context.Background(), cfg)
	t.Cleanup(n.Close)
	return n
}
