package password

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
)

func TestHashAndVerify(t *testing.T) {
	h := Hash("correct horse battery")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") || checkHash(h) != nil {
		t.Fatalf("hash = %q", h)
	}
	if !Verify(h, "correct horse battery") || Verify(h, "correct horse batterY") || Verify(h, "") {
		t.Fatal("argon2id verify")
	}
	if Hash("x") == Hash("x") {
		t.Fatal("hashes are not salted")
	}
	b, _ := bcrypt.GenerateFromPassword([]byte("from htpasswd"), bcrypt.MinCost)
	if checkHash(string(b)) != nil || !Verify(string(b), "from htpasswd") || Verify(string(b), "nope") {
		t.Fatal("bcrypt verify")
	}
	for _, bad := range []string{"", "plaintext", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$a2V5", "$argon2id$v=19$m=4194304,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$!!$a2V5", "$1$md5crypt$x"} {
		if checkHash(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseUsers(t *testing.T) {
	a, b := Hash("first password"), Hash("second password")
	users := map[string]string{}
	err := ParseUsers("# admins\nBoss@X.com:"+a+"\n  ops@x.com:"+b+" ; ", users)
	if err != nil || users["boss@x.com"] != a || users["ops@x.com"] != b || len(users) != 2 {
		t.Fatalf("users = %v, %v", users, err)
	}
	for _, bad := range []string{"boss@x.com", "boss@x.com:plain", "boss:" + a, "boss@x.com:" + a + " BOSS@x.com:" + b} {
		err := ParseUsers(bad, map[string]string{})
		if err == nil {
			t.Errorf("%q accepted", bad)
		} else if strings.Contains(err.Error(), "plain") || strings.Contains(err.Error(), a) {
			t.Errorf("error leaks the secret: %v", err)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newLimiter(2, 4*time.Second)
	l.now = func() time.Time { return now }
	l.fail("k")
	l.fail("k")
	if l.wait("k") != 0 {
		t.Fatal("free attempts were limited")
	}
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		l.fail("k")
		if got := l.wait("k"); got != want {
			t.Fatalf("failure %d: wait %v, want %v", i+3, got, want)
		}
	}
	now = now.Add(5 * time.Second)
	if l.wait("k") != 0 || l.wait("other") != 0 {
		t.Fatal("the wait did not end")
	}
	l.reset("k")
	l.fail("k")
	if l.wait("k") != 0 {
		t.Fatal("reset kept the strikes")
	}
}

type rig struct {
	h   *auth.Handler
	mux *http.ServeMux
	m   *Method
}

func newRig(t *testing.T) *rig {
	t.Helper()
	m, err := New(map[string]string{"boss@x.com": Hash("boss password!"), "stranger@y.com": Hash("stranger password")})
	if err != nil {
		t.Fatal(err)
	}
	admins, _ := auth.ParseAllowlist("boss@x.com")
	h := auth.New(auth.Options{PublicURL: "https://admin.example.com", Admins: admins, Methods: []auth.Method{m},
		Signer: auth.NewSigner("0123456789abcdef0123456789abcdef"), SessionTTL: time.Hour})
	mux := http.NewServeMux()
	h.Mount(mux)
	return &rig{h: h, mux: mux, m: m}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// form loads the sign-in page and returns its token and flow cookie.
func (g *rig) form(t *testing.T) (string, *http.Cookie) {
	t.Helper()
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, httptest.NewRequest("GET", "/auth/login?next=/keys", nil))
	m := csrfField.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil || !strings.Contains(w.Body.String(), `name="next" value="/keys"`) {
		t.Fatalf("sign-in page = %d\n%s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "lpa_login" {
			return m[1], c
		}
	}
	t.Fatal("no flow cookie")
	return "", nil
}

func (g *rig) post(token string, c *http.Cookie, email, pw string) *httptest.ResponseRecorder {
	body := url.Values{"csrf": {token}, "next": {"/keys"}, "email": {email}, "password": {pw}}.Encode()
	r := httptest.NewRequest("POST", "/auth/password", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "198.51.100.7:1234"
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, r)
	return w
}

func (g *rig) try(t *testing.T, email, pw string) *httptest.ResponseRecorder {
	t.Helper()
	tok, c := g.form(t)
	return g.post(tok, c, email, pw)
}

func sessionOf(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "lpa_session" && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestSignIn(t *testing.T) {
	g := newRig(t)
	w := g.try(t, " Boss@X.com ", "boss password!")
	s := sessionOf(w)
	if w.Code != 302 || w.Header().Get("Location") != "/keys" || s == nil {
		t.Fatalf("sign-in = %d %q", w.Code, w.Header().Get("Location"))
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(s)
	if u, ok := g.h.UserFor(r); !ok || u.Email != "boss@x.com" || u.Method != "password" || !u.IsSuperAdmin() {
		t.Fatalf("user = %+v %v", u, ok)
	}

	cases := []struct {
		name, email, pw string
		code            int
		says            string
	}{
		{"wrong password", "boss@x.com", "guess", 401, "Wrong e-mail or password."},
		{"unknown e-mail", "nobody@x.com", "boss password!", 401, "Wrong e-mail or password."},
		{"right password, not on the allowlist", "stranger@y.com", "stranger password", 403, "stranger@y.com is not allowed"},
		{"blank", "boss@x.com", "", 400, "Enter your e-mail and password."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := g.try(t, c.email, c.pw)
			if w.Code != c.code || sessionOf(w) != nil || !strings.Contains(w.Body.String(), c.says) {
				t.Fatalf("= %d\n%s", w.Code, w.Body)
			}
			if c.code == 401 && csrfField.FindString(w.Body.String()) == "" {
				t.Error("a failed attempt should show the form again")
			}
		})
	}
}

func TestFormNeedsItsCookie(t *testing.T) {
	g := newRig(t)
	tok, c := g.form(t)
	other, _ := g.form(t)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"cross-site post without the cookie": g.post(tok, nil, "boss@x.com", "boss password!"),
		"token from another form":            g.post(other, c, "boss@x.com", "boss password!"),
	} {
		if w.Code != 400 || sessionOf(w) != nil {
			t.Errorf("%s = %d", name, w.Code)
		}
	}
}

func TestBackoff(t *testing.T) {
	g := newRig(t)
	for i := 0; i < 5; i++ {
		if w := g.try(t, "boss@x.com", "guess"); w.Code != 401 {
			t.Fatalf("attempt %d = %d", i+1, w.Code)
		}
	}
	if w := g.try(t, "boss@x.com", "guess"); w.Code != 401 {
		t.Fatalf("sixth attempt = %d", w.Code)
	}
	w := g.try(t, "boss@x.com", "boss password!")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || sessionOf(w) != nil {
		t.Fatalf("attempt while backing off = %d", w.Code)
	}
}
