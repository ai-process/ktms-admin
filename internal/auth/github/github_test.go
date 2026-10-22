package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ai-process/ktms-admin/internal/auth"
)

// fakeGitHub is a GitHub Enterprise-shaped server: OAuth under /login and
// the REST API under /api/v3.
type fakeGitHub struct {
	srv      *httptest.Server
	emails   []map[string]any
	verifier string
}

func newGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code") != "good-code" || r.PostForm.Get("code_verifier") == "" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		g.verifier = r.PostForm.Get("code_verifier")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_x", "token_type": "bearer"})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer gho_x" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/v3/user/emails", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(g.emails)
	}))
	mux.HandleFunc("GET /api/v3/user", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"login": "octo", "avatar_url": "https://avatars.githubusercontent.com/u/1"})
	}))
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func rig(t *testing.T, g *fakeGitHub) (*auth.Handler, *http.ServeMux) {
	t.Helper()
	admins, _ := auth.ParseAllowlist("boss@x.com")
	h := auth.New(auth.Options{PublicURL: "https://admin.example.com", Admins: admins, SessionTTL: time.Hour,
		Signer:  auth.NewSigner("0123456789abcdef0123456789abcdef"),
		Methods: []auth.Method{New(Options{ClientID: "cid", ClientSecret: "cs", BaseURL: g.srv.URL})}})
	mux := http.NewServeMux()
	h.Mount(mux)
	return h, mux
}

func get(mux http.Handler, path string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func cookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestSignIn(t *testing.T) {
	g := newGitHub(t)
	cases := []struct {
		name   string
		emails []map[string]any
		code   string
		want   int
	}{
		{"primary verified", []map[string]any{{"email": "old@y.com", "primary": false, "verified": true}, {"email": "boss@x.com", "primary": true, "verified": true}}, "good-code", 302},
		{"primary unverified", []map[string]any{{"email": "boss@x.com", "primary": true, "verified": false}}, "good-code", 403},
		{"verified but not primary", []map[string]any{{"email": "boss@x.com", "primary": false, "verified": true}}, "good-code", 403},
		{"not on the allowlist", []map[string]any{{"email": "eve@y.com", "primary": true, "verified": true}}, "good-code", 403},
		{"bad code", nil, "forged", 502},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, mux := rig(t, g)
			g.emails = c.emails
			w := get(mux, "/auth/login?next=/keys", nil)
			if w.Code != 302 || w.Header().Get("Location") != "/auth/github/start?next=%2Fkeys" {
				t.Fatalf("login = %d %q", w.Code, w.Header().Get("Location"))
			}
			w = get(mux, w.Header().Get("Location"), nil)
			loc, _ := url.Parse(w.Header().Get("Location"))
			q := loc.Query()
			if loc.Path != "/login/oauth/authorize" || q.Get("redirect_uri") != "https://admin.example.com/auth/github/callback" ||
				q.Get("code_challenge_method") != "S256" || q.Get("scope") != "read:user user:email" {
				t.Fatalf("authorize URL = %s", loc)
			}
			w = get(mux, "/auth/github/callback?code="+c.code+"&state="+url.QueryEscape(q.Get("state")), cookie(w, "lpa_login"))
			if w.Code != c.want {
				t.Fatalf("callback = %d, want %d: %s", w.Code, c.want, w.Body)
			}
			if c.want != 302 {
				if cookie(w, "lpa_session") != nil {
					t.Fatal("session on a refused sign-in")
				}
				return
			}
			r := httptest.NewRequest("GET", "/", nil)
			r.AddCookie(cookie(w, "lpa_session"))
			if u, ok := h.UserFor(r); !ok || u.Email != "boss@x.com" || u.Name != "octo" || u.Method != "github" || w.Header().Get("Location") != "/keys" {
				t.Fatalf("user = %+v %v, redirect %q", u, ok, w.Header().Get("Location"))
			}
		})
	}
}

func TestCallbackChecksState(t *testing.T) {
	g := newGitHub(t)
	_, mux := rig(t, g)
	w := get(mux, "/auth/github/start?next=/", nil)
	if w := get(mux, "/auth/github/callback?code=good-code&state=forged", cookie(w, "lpa_login")); w.Code != 400 {
		t.Fatalf("forged state = %d", w.Code)
	}
	if w := get(mux, "/auth/github/callback?code=good-code&state=x", nil); w.Code != 400 {
		t.Fatalf("no flow cookie = %d", w.Code)
	}
}

func TestBaseURL(t *testing.T) {
	for base, want := range map[string][2]string{
		"":                          {"https://github.com/login/oauth/authorize", "https://api.github.com"},
		"https://github.com/":       {"https://github.com/login/oauth/authorize", "https://api.github.com"},
		"https://ghe.corp.example/": {"https://ghe.corp.example/login/oauth/authorize", "https://ghe.corp.example/api/v3"},
	} {
		m := New(Options{BaseURL: base})
		if m.cfg.Endpoint.AuthURL != want[0] || m.apiURL != want[1] {
			t.Errorf("%q: %s %s", base, m.cfg.Endpoint.AuthURL, m.apiURL)
		}
	}
}
