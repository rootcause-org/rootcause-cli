package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestLoginPKCE drives the full loopback flow against a stub authorization server: the stub /authorize
// validates the PKCE challenge + redirects back to the CLI's loopback callback with a code; the stub
// /token validates that the presented code_verifier hashes to that challenge before issuing tokens. The
// "browser" is a plain HTTP GET that follows the redirect into the CLI's own callback server.
func TestLoginPKCE(t *testing.T) {
	var gotChallenge string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			t.Errorf("authorize missing S256 challenge: %v", q)
		}
		if q.Get("client_id") != CLIClientID {
			t.Errorf("authorize client_id = %q", q.Get("client_id"))
		}
		gotChallenge = q.Get("code_challenge")
		// Redirect back to the loopback redirect_uri with a code + the same state (the CSRF echo).
		redir := q.Get("redirect_uri") + "?code=auth_code_1&state=" + url.QueryEscape(q.Get("state"))
		http.Redirect(w, r, redir, http.StatusFound)
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("grant_type") != "authorization_code" || r.PostFormValue("code") != "auth_code_1" {
			t.Errorf("unexpected token request: %v", r.PostForm)
		}
		// Verify the PKCE proof: S256(verifier) must equal the challenge sent at /authorize.
		sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != gotChallenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"rcoa_ok","refresh_token":"rcor_ok","token_type":"Bearer","expires_in":3600}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// The "browser": follow the authorize URL (it 302s into the loopback callback).
	opener := func(authURL string) error {
		resp, err := http.Get(authURL)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}

	toks, err := NewClient(srv.URL).LoginPKCE(context.Background(), opener, io.Discard)
	if err != nil {
		t.Fatalf("LoginPKCE: %v", err)
	}
	if toks.AccessToken != "rcoa_ok" || toks.RefreshToken != "rcor_ok" || toks.ExpiresIn != 3600 {
		t.Errorf("tokens = %+v", toks)
	}
}

// TestLoginPKCEStateMismatch: a callback whose state doesn't match is rejected (CSRF guard).
func TestLoginPKCEStateMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		redir := r.URL.Query().Get("redirect_uri") + "?code=x&state=WRONG"
		http.Redirect(w, r, redir, http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	opener := func(authURL string) error {
		resp, err := http.Get(authURL)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	_, err := NewClient(srv.URL).LoginPKCE(context.Background(), opener, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("expected a state-mismatch error, got %v", err)
	}
}

// TestRefreshInvalidGrant: a dead refresh token surfaces as an invalid_grant *Error (IsInvalidGrant).
func TestRefreshInvalidGrant(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := NewClient(srv.URL).Refresh(context.Background(), "rcor_dead")
	if !IsInvalidGrant(err) {
		t.Fatalf("expected IsInvalidGrant, got %v", err)
	}
}

// TestPKCEChallenge: the challenge is the base64url-S256 of the verifier (the proof the server checks).
func TestPKCEChallenge(t *testing.T) {
	pk, err := newPKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(pk.verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); pk.challenge != want {
		t.Errorf("challenge = %q, want %q", pk.challenge, want)
	}
}
