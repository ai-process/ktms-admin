package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
)

// fakeIdP is a minimal OIDC provider: discovery, keys and a token endpoint
// that returns whatever ID token the test armed.
type fakeIdP struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	claim map[string]any
}

func newIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
			"jwks_uri": p.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		claims := map[string]any{"iss": p.srv.URL, "aud": "client-id", "sub": "1", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
		for k, v := range p.claim {
			claims[k] = v
		}
		raw, _ := jwt.Signed(sig).Claims(claims).Serialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

type rig struct {
	h   *auth.Handler
	mux *http.ServeMux
}

func newRig(t *testing.T, idp *fakeIdP) *rig {
	t.Helper()
	admins, _ := auth.ParseAllowlist("boss@x.com")
	viewers, _ := auth.ParseAllowlist("*@view.com")
	h := auth.New(auth.Options{PublicURL: "http://localhost:8085", Admins: admins, Viewers: viewers,
		Signer: auth.NewSigner("0123456789abcdef0123456789abcdef"), SessionTTL: time.Hour,
		Methods: []auth.Method{New(Options{Issuer: idp.srv.URL, ClientID: "client-id", ClientSecret: "secret"})}})
	mux := http.NewServeMux()
	h.Mount(mux)
	return &rig{h: h, mux: mux}
}

func (g *rig) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, r)
	return w
}

// startLogin follows the sign-in page to the IdP and returns the flow cookie
// plus the state and nonce sent there.
func startLogin(t *testing.T, g *rig, next string) (cookie *http.Cookie, state, nonce string) {
	t.Helper()
	w := g.get("/auth/login?next=" + url.QueryEscape(next))
	if w.Code != 302 || w.Header().Get("Location") != "/auth/oidc/start?next="+url.QueryEscape(next) {
		t.Fatalf("login page should go straight to the only method: %d %q", w.Code, w.Header().Get("Location"))
	}
	w = g.get(w.Header().Get("Location"))
	if w.Code != 302 {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" {
		t.Fatalf("auth URL lacks PKCE/nonce: %s", loc)
	}
	if q.Get("redirect_uri") != "http://localhost:8085/auth/callback" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "lpa_login" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no flow cookie")
	}
	return cookie, q.Get("state"), q.Get("nonce")
}

func finish(g *rig, c *http.Cookie, state string) *httptest.ResponseRecorder {
	return g.get("/auth/callback?code=abc&state="+url.QueryEscape(state), c)
}

func TestFullSignInFlow(t *testing.T) {
	idp := newIdP(t)
	g := newRig(t, idp)

	cases := []struct {
		name   string
		claims map[string]any
		mutate func(state *string, nonce *string)
		want   int
		role   string
	}{
		{"admin", map[string]any{"email": "boss@x.com", "email_verified": true, "name": "Boss"}, nil, 302, auth.RoleAdmin},
		{"viewer by wildcard", map[string]any{"email": "Pat@View.com", "email_verified": true}, nil, 302, auth.RoleViewer},
		{"not allowed", map[string]any{"email": "eve@evil.com", "email_verified": true}, nil, 403, ""},
		{"unverified email", map[string]any{"email": "boss@x.com", "email_verified": false}, nil, 403, ""},
		{"wrong nonce", map[string]any{"email": "boss@x.com", "email_verified": true}, func(_, n *string) { *n = "other" }, 403, ""},
		{"wrong state", map[string]any{"email": "boss@x.com", "email_verified": true}, func(s, _ *string) { *s = "forged" }, 400, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cookie, state, nonce := startLogin(t, g, "/models")
			if c.mutate != nil {
				c.mutate(&state, &nonce)
			}
			claims := map[string]any{"nonce": nonce}
			for k, v := range c.claims {
				claims[k] = v
			}
			idp.claim = claims
			w := finish(g, cookie, state)
			if w.Code != c.want {
				t.Fatalf("callback = %d, want %d: %s", w.Code, c.want, w.Body)
			}
			var session *http.Cookie
			for _, sc := range w.Result().Cookies() {
				if sc.Name == "lpa_session" && sc.Value != "" {
					session = sc
				}
			}
			if c.want != 302 {
				if session != nil {
					t.Fatal("a session was issued on a refused sign-in")
				}
				return
			}
			if w.Header().Get("Location") != "/models" || session == nil || !session.HttpOnly {
				t.Fatalf("redirect=%q session=%v", w.Header().Get("Location"), session)
			}
			r := httptest.NewRequest("GET", "/", nil)
			r.AddCookie(session)
			if u, ok := g.h.UserFor(r); !ok || u.Role != c.role || u.Method != "oidc" {
				t.Fatalf("session user = %+v ok=%v, want role %s", u, ok, c.role)
			}
		})
	}
}

func TestCallbackWithoutFlowCookie(t *testing.T) {
	g := newRig(t, newIdP(t))
	if w := g.get("/auth/callback?code=x&state=y"); w.Code != 400 {
		t.Errorf("replayed/unsolicited callback = %d", w.Code)
	}
}

func TestFlowCookieIsSingleUse(t *testing.T) {
	idp := newIdP(t)
	g := newRig(t, idp)
	cookie, state, nonce := startLogin(t, g, "/")
	idp.claim = map[string]any{"nonce": nonce, "email": "boss@x.com", "email_verified": true}
	w := finish(g, cookie, state)
	cleared := false
	for _, c := range w.Result().Cookies() {
		cleared = cleared || (c.Name == "lpa_login" && c.MaxAge < 0)
	}
	if w.Code != 302 || !cleared {
		t.Fatalf("callback = %d, flow cookie cleared = %v", w.Code, cleared)
	}
}
