package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeTokenServer serves a token endpoint that returns tokBody (optionally
// wrapped in a status code).
func fakeTokenServer(t *testing.T, status int, tokBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, tokBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestTokenSourceRefreshAndPersist(t *testing.T) {
	// First call fails with invalid_grant; verify dead classification.
	srv := fakeTokenServer(t, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"AADSTS70008: expired"}`)
	store := NewTokenStore(filepath.Join(t.TempDir(), "t.json"))
	if err := store.Save(&oauth2.Token{AccessToken: "stale", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ClientID: "cid-1", TokenURL: srv.URL + "/token"}
	ts, err := newTokenSource(context.Background(), cfg, store)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	if _, err := ts.Token(context.Background()); !errors.Is(err, ErrAuthDead) {
		t.Fatalf("Token() err = %v, want ErrAuthDead", err)
	}
	if !ts.dead {
		t.Fatal("expected source to be marked dead after invalid_grant")
	}
}

func TestTokenSourcePersistsRotatedToken(t *testing.T) {
	srv := fakeTokenServer(t, http.StatusOK, `{"access_token":"at-new","refresh_token":"rt-new","token_type":"Bearer","expires_in":3600}`)
	store := NewTokenStore(filepath.Join(t.TempDir(), "t.json"))
	if err := store.Save(&oauth2.Token{AccessToken: "stale", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ClientID: "cid-1", TokenURL: srv.URL + "/token"}
	ts, err := newTokenSource(context.Background(), cfg, store)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if tok.AccessToken != "at-new" {
		t.Errorf("access token = %q, want at-new", tok.AccessToken)
	}
	// Rotation must be persisted back to the store.
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if saved.RefreshToken != "rt-new" {
		t.Errorf("stored refresh token = %q, want rt-new", saved.RefreshToken)
	}
}

func TestForceRefresh(t *testing.T) {
	srv := fakeTokenServer(t, http.StatusOK, `{"access_token":"at-forced","refresh_token":"rt-forced","token_type":"Bearer","expires_in":3600}`)
	store := NewTokenStore(filepath.Join(t.TempDir(), "t.json"))
	// A still-valid access token must be ignored by ForceRefresh (it uses
	// only the refresh token, so the grant happens regardless of expiry).
	if err := store.Save(&oauth2.Token{AccessToken: "valid", RefreshToken: "rt-1", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ClientID: "cid-1", TokenURL: srv.URL + "/token"}
	ts, err := newTokenSource(context.Background(), cfg, store)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}
	tok, err := ts.ForceRefresh(context.Background())
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if tok.AccessToken != "at-forced" {
		t.Errorf("access token = %q, want at-forced", tok.AccessToken)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.RefreshToken != "rt-forced" {
		t.Errorf("stored refresh token = %q, want rt-forced", saved.RefreshToken)
	}
}

func TestIsInvalidGrant(t *testing.T) {
	if isInvalidGrant(&oauth2.RetrieveError{ErrorCode: "invalid_grant"}) != true {
		t.Error("expected invalid_grant to classify true")
	}
	if isInvalidGrant(&oauth2.RetrieveError{ErrorCode: "temporarily_unavailable"}) != false {
		t.Error("expected other codes to classify false")
	}
	if isInvalidGrant(errors.New("boom")) != false {
		t.Error("expected non-RetrieveError to classify false")
	}
}

// fakeGraphServer returns status once, then okStatus for subsequent calls.
func fakeGraphServer(t *testing.T, status, okStatus int) *httptest.Server {
	t.Helper()
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/me/sendMail", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(okStatus)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSendOnceClassification(t *testing.T) {
	srv := fakeGraphServer(t, http.StatusTooManyRequests, http.StatusOK)
	n := &notifier{cfg: Config{GraphBaseURL: srv.URL}, http: srv.Client()}
	tok := &oauth2.Token{AccessToken: "at"}

	err := n.sendOnce(context.Background(), tok, "s", "b")
	if !errors.Is(err, errRetryable) {
		t.Errorf("429 err = %v, want errRetryable", err)
	}
	err = n.sendOnce(context.Background(), tok, "s", "b")
	if err != nil {
		t.Errorf("ok err = %v, want nil", err)
	}
}

func TestSendOnceUnauthorized(t *testing.T) {
	srv := fakeGraphServer(t, http.StatusUnauthorized, http.StatusOK)
	n := &notifier{cfg: Config{GraphBaseURL: srv.URL}, http: srv.Client()}
	err := n.sendOnce(context.Background(), &oauth2.Token{AccessToken: "at"}, "s", "b")
	if !errors.Is(err, errUnauthorized) {
		t.Errorf("401 err = %v, want errUnauthorized", err)
	}
}

func TestSendOncePermanent(t *testing.T) {
	srv := fakeGraphServer(t, http.StatusBadRequest, http.StatusOK)
	n := &notifier{cfg: Config{GraphBaseURL: srv.URL}, http: srv.Client()}
	err := n.sendOnce(context.Background(), &oauth2.Token{AccessToken: "at"}, "s", "b")
	if !errors.Is(err, errPermanent) {
		t.Errorf("400 err = %v, want errPermanent", err)
	}
}

func TestSendMailRequestBody(t *testing.T) {
	var got sendMailRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/me/sendMail", func(w http.ResponseWriter, r *http.Request) {
		if gotAuth := r.Header.Get("Authorization"); gotAuth != "Bearer at-9" {
			t.Errorf("Authorization = %q, want Bearer at-9", gotAuth)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("bad request JSON: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	n := &notifier{cfg: Config{GraphBaseURL: srv.URL, To: []string{"a@example.com"}}, http: srv.Client()}
	err := n.sendOnce(context.Background(), &oauth2.Token{AccessToken: "at-9"}, "subject-1", "body-1")
	if err != nil {
		t.Fatalf("sendOnce: %v", err)
	}
	if got.Message.Subject != "subject-1" {
		t.Errorf("subject = %q, want subject-1", got.Message.Subject)
	}
	if got.Message.Body.ContentType != "Text" || got.Message.Body.Content != "body-1" {
		t.Errorf("body = %+v, want Text/body-1", got.Message.Body)
	}
	if len(got.Message.ToRecipients) != 1 || got.Message.ToRecipients[0].EmailAddress.Address != "a@example.com" {
		t.Errorf("toRecipients = %+v, want a@example.com", got.Message.ToRecipients)
	}
	if !got.SaveToSentItems {
		t.Error("saveToSentItems = false, want true")
	}
}
