package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeButton signs in whoever /auth/<id>/as?email= names, so the core can be
// tested without an identity provider.
type fakeButton struct{ id string }

func (f fakeButton) ID() string    { return f.id }
func (f fakeButton) Label() string { return "Use " + f.id }
func (f fakeButton) StartURL(next string) string {
	return "/auth/" + f.id + "/start?next=" + url.QueryEscape(next)
}
func (f fakeButton) Mount(mux *http.ServeMux, host Host) {
	mux.HandleFunc("GET /auth/"+f.id+"/as", func(w http.ResponseWriter, r *http.Request) {
		host.SignIn(w, r, f.id, Identity{Email: r.URL.Query().Get("email")}, r.URL.Query().Get("next"))
	})
}

func coreMux(t *testing.T, methods ...Method) (*Handler, *http.ServeMux) {
	t.Helper()
	admins, _ := ParseAllowlist("boss@x.com")
	h := New(Options{PublicURL: "https://admin.example.com", Admins: admins, Methods: methods,
		Signer: NewSigner("0123456789abcdef0123456789abcdef"), SessionTTL: time.Hour})
	mux := http.NewServeMux()
	h.Mount(mux)
	return h, mux
}

func serve(mux http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestLoginPageListsMethods(t *testing.T) {
	_, mux := coreMux(t, fakeButton{"a"}, fakeButton{"b"})
	w := serve(mux, "/auth/login?next=/usage")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `href="/auth/a/start?next=%2Fusage"`) || !strings.Contains(body, "Use b") {
		t.Fatalf("login page = %d\n%s", w.Code, body)
	}
}

func TestLoginGoesStraightToTheOnlyButton(t *testing.T) {
	_, mux := coreMux(t, fakeButton{"a"})
	w := serve(mux, "/auth/login?next=//evil.com")
	if w.Code != 302 || w.Header().Get("Location") != "/auth/a/start?next=%2F" {
		t.Fatalf("login = %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestLoginWithoutMethods(t *testing.T) {
	_, mux := coreMux(t)
	if w := serve(mux, "/auth/login"); w.Code != 500 {
		t.Fatalf("login without methods = %d", w.Code)
	}
}

func TestSignInAppliesAllowlist(t *testing.T) {
	h, mux := coreMux(t, fakeButton{"a"}, fakeButton{"b"})
	w := serve(mux, "/auth/a/as?email="+url.QueryEscape("<b>eve</b>@evil.com"))
	if w.Code != 403 || cookieNamed(w, sessionCookie) != nil || strings.Contains(w.Body.String(), "<b>") {
		t.Fatalf("refused sign-in = %d, body %s", w.Code, w.Body)
	}
	w = serve(mux, "/auth/b/as?email=boss@x.com&next=/models")
	session := cookieNamed(w, sessionCookie)
	if w.Code != 302 || w.Header().Get("Location") != "/models" || session == nil {
		t.Fatalf("sign-in = %d %q", w.Code, w.Header().Get("Location"))
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(session)
	if u, ok := h.UserFor(r); !ok || u.Method != "b" || !u.IsSuperAdmin() {
		t.Fatalf("user = %+v %v", u, ok)
	}
	if w := serve(mux, "/auth/login?next=/keys", session); w.Code != 302 || w.Header().Get("Location") != "/keys" {
		t.Fatalf("signed-in login = %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestFlowCookie(t *testing.T) {
	h, _ := coreMux(t)
	type v struct{ S string }
	save := func(method string) *http.Cookie {
		w := httptest.NewRecorder()
		if err := h.SaveFlow(w, method, v{"x"}); err != nil {
			t.Fatal(err)
		}
		return cookieNamed(w, flowCookie)
	}
	take := func(c *http.Cookie, method string) (v, bool) {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(c)
		var got v
		return got, h.TakeFlow(httptest.NewRecorder(), r, method, &got)
	}
	if got, ok := take(save("a"), "a"); !ok || got.S != "x" {
		t.Fatalf("round trip = %+v %v", got, ok)
	}
	if _, ok := take(save("a"), "b"); ok {
		t.Fatal("another method's flow accepted")
	}
	c := save("a")
	c.Value = "x" + c.Value
	if _, ok := take(c, "a"); ok {
		t.Fatal("tampered flow accepted")
	}
	expired, _ := h.o.Signer.Seal(flowEnvelope{Method: "a", Exp: time.Now().Add(-time.Second).Unix(), Value: []byte(`{"S":"x"}`)})
	if _, ok := take(&http.Cookie{Name: flowCookie, Value: expired}, "a"); ok {
		t.Fatal("expired flow accepted")
	}
}
