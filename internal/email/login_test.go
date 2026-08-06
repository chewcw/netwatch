package email

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestLoginSavesToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/devicecode", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("devicecode method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_code":"dc-1","user_code":"ABCD-EFGH","verification_uri":"https://example.test/devicelogin","expires_in":600,"interval":1}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"at-2","refresh_token":"rt-2","token_type":"Bearer","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tokenFile := filepath.Join(t.TempDir(), "token.json")
	cfg := Config{
		ClientID:      "cid-1",
		TokenFile:     tokenFile,
		DeviceAuthURL: srv.URL + "/devicecode",
		TokenURL:      srv.URL + "/token",
	}

	out := captureStdout(t, func() {
		if err := Login(context.Background(), cfg, time.Minute); err != nil {
			t.Errorf("Login: %v", err)
		}
	})

	if !strings.Contains(out, "ABCD-EFGH") || !strings.Contains(out, "devicelogin") {
		t.Errorf("stdout missing user code/URI, got:\n%s", out)
	}
	tok, err := NewTokenStore(tokenFile).Load()
	if err != nil {
		t.Fatalf("token not saved: %v", err)
	}
	if tok.AccessToken != "at-2" || tok.RefreshToken != "rt-2" {
		t.Errorf("saved token access=%q refresh=%q, want at-2/rt-2", tok.AccessToken, tok.RefreshToken)
	}
}

func TestLoginFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/devicecode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_code":"dc-1","user_code":"ABCD-EFGH","verification_uri":"https://example.test/devicelogin","expires_in":600,"interval":1}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"access_denied","error_description":"user declined"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{
		ClientID:      "cid-1",
		TokenFile:     filepath.Join(t.TempDir(), "token.json"),
		DeviceAuthURL: srv.URL + "/devicecode",
		TokenURL:      srv.URL + "/token",
	}
	if err := Login(context.Background(), cfg, time.Minute); err == nil {
		t.Fatal("expected Login to fail on access_denied")
	}
}
