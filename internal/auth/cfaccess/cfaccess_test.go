package cfaccess

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeTeam serves a Cloudflare Access certs endpoint and signs tokens for it.
type fakeTeam struct {
	srv *httptest.Server
	key *rsa.PrivateKey
}

func newTeam(t *testing.T) *fakeTeam {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTeam{key: key}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cdn-cgi/access/certs" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTeam) token(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	base := map[string]any{"iss": f.srv.URL, "aud": []string{"app-aud"}, "sub": "1", "type": "app",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "boss@x.com"}
	for k, v := range claims {
		if v == nil {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(sig).Claims(base).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestIdentify(t *testing.T) {
	team := newTeam(t)
	m, err := New(Options{TeamDomain: team.srv.URL, Audience: "app-aud"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"valid", team.token(t, team.key, nil), "boss@x.com"},
		{"no token", "", ""},
		{"garbage", "not.a.jwt", ""},
		{"forged signature", team.token(t, other, nil), ""},
		{"another application", team.token(t, team.key, map[string]any{"aud": []string{"other-aud"}}), ""},
		{"another team", team.token(t, team.key, map[string]any{"iss": "https://evil.cloudflareaccess.com"}), ""},
		{"expired", team.token(t, team.key, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}), ""},
		{"service token without e-mail", team.token(t, team.key, map[string]any{"email": nil, "common_name": "ci"}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if c.token != "" {
				r.Header.Set("Cf-Access-Jwt-Assertion", c.token)
			}
			id, ok := m.Identify(r)
			if got := map[bool]string{true: id.Email}[ok]; got != c.want {
				t.Fatalf("Identify = %q %v, want %q", id.Email, ok, c.want)
			}
		})
	}
}

func TestIssuer(t *testing.T) {
	for in, want := range map[string]string{
		"myteam":                               "https://myteam.cloudflareaccess.com",
		"myteam.cloudflareaccess.com":          "https://myteam.cloudflareaccess.com",
		"https://myteam.cloudflareaccess.com/": "https://myteam.cloudflareaccess.com",
	} {
		if got := Issuer(in); got != want {
			t.Errorf("Issuer(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := New(Options{TeamDomain: "myteam"}); err == nil {
		t.Error("missing AUD accepted")
	}
}
