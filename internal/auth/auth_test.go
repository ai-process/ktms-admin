package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllowlist(t *testing.T) {
	a, err := ParseAllowlist("alice@example.com, *@corp.io\n*@*.dept.org;bob*@x.com")
	if err != nil {
		t.Fatal(err)
	}
	yes := []string{"alice@example.com", "ALICE@Example.com", "anyone@corp.io", "u@a.dept.org", "u@a.b.dept.org", "bobby@x.com", "bob@x.com"}
	no := []string{"alice@evil.com", "mallory@corp.io.evil.com", "u@dept.org", "a@b@corp.io", "@corp.io", "alice@example.com.au", "corp.io", "", "carol@x.com", "xalice@example.com"}
	for _, e := range yes {
		if !a.Match(e) {
			t.Errorf("should allow %q", e)
		}
	}
	for _, e := range no {
		if a.Match(e) {
			t.Errorf("should refuse %q", e)
		}
	}
}

func TestAllowlistRejectsBarePatterns(t *testing.T) {
	for _, bad := range []string{"*", "example.com", "a@b@c"} {
		if _, err := ParseAllowlist(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	a, err := ParseAllowlist("*@*")
	if err != nil || !a.Match("any@where.net") || !a.AllowsAll() {
		t.Errorf("*@* is the explicit allow-all")
	}
	empty, _ := ParseAllowlist("")
	if empty.Match("a@b.c") {
		t.Errorf("empty list must allow nobody")
	}
}

func TestSignerRoundTripAndTamper(t *testing.T) {
	s := NewSigner("secret-one-secret-one-secret-one!")
	tok, err := s.Seal(sessionData{Email: "a@b.c", Exp: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got sessionData
	if err := s.Open(tok, &got); err != nil || got.Email != "a@b.c" {
		t.Fatalf("open: %v %+v", err, got)
	}
	if NewSigner("another-secret-another-secret!!!").Open(tok, &got) == nil {
		t.Error("wrong key accepted")
	}
	if s.Open(tok[:len(tok)-2]+"xx", &got) == nil || s.Open("garbage", &got) == nil {
		t.Error("tampered token accepted")
	}
}

func newHandler(t *testing.T) *Handler {
	t.Helper()
	admins, _ := ParseAllowlist("admin@x.com")
	viewers, _ := ParseAllowlist("*@view.com")
	return New(Options{PublicURL: "http://localhost", Admins: admins, Viewers: viewers,
		Signer: NewSigner("0123456789abcdef0123456789abcdef"), SessionTTL: time.Hour})
}

func withSession(t *testing.T, h *Handler, email string, exp time.Time) *http.Request {
	t.Helper()
	tok, _ := h.o.Signer.Seal(sessionData{Email: email, Exp: exp.Unix()})
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	return r
}

func TestSessionRolesAndExpiry(t *testing.T) {
	h := newHandler(t)
	if u, ok := h.UserFor(withSession(t, h, "admin@x.com", time.Now().Add(time.Hour))); !ok || !u.CanWrite() {
		t.Errorf("admin: %+v %v", u, ok)
	}
	if u, ok := h.UserFor(withSession(t, h, "p@view.com", time.Now().Add(time.Hour))); !ok || u.CanWrite() || u.Role != RoleViewer {
		t.Errorf("viewer: %+v %v", u, ok)
	}
	if _, ok := h.UserFor(withSession(t, h, "admin@x.com", time.Now().Add(-time.Minute))); ok {
		t.Error("expired session accepted")
	}
	if _, ok := h.UserFor(withSession(t, h, "stranger@y.com", time.Now().Add(time.Hour))); ok {
		t.Error("session for an address that left the allowlist accepted")
	}
	if _, ok := h.UserFor(httptest.NewRequest("GET", "/", nil)); ok {
		t.Error("anonymous accepted")
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{"/usage": "/usage", "//evil.com": "/", "https://evil.com": "/", "/\\evil.com": "/", "": "/"}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
