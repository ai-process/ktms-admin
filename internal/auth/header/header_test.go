package header

import (
	"net/http/httptest"
	"testing"
)

func TestIdentify(t *testing.T) {
	m, err := New(Options{TrustedProxies: "10.0.0.0/8, 192.168.1.5 ::1", NameHeader: "X-Forwarded-User"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		remote string
		emails []string
		want   string
	}{
		{"trusted proxy", "10.1.2.3:5555", []string{"Boss@x.com"}, "Boss@x.com"},
		{"single trusted host", "192.168.1.5:1", []string{"a@x.com"}, "a@x.com"},
		{"ipv6 loopback", "[::1]:1", []string{"a@x.com"}, "a@x.com"},
		{"ipv4-mapped ipv6", "[::ffff:10.0.0.1]:1", []string{"a@x.com"}, "a@x.com"},
		{"untrusted client forges header", "203.0.113.9:1", []string{"boss@x.com"}, ""},
		{"neighbour of a single host", "192.168.1.6:1", []string{"a@x.com"}, ""},
		{"no header", "10.1.2.3:1", nil, ""},
		{"header sent twice", "10.1.2.3:1", []string{"a@x.com", "boss@x.com"}, ""},
		{"list in one header", "10.1.2.3:1", []string{"a@x.com,boss@x.com"}, ""},
		{"not an e-mail", "10.1.2.3:1", []string{"boss"}, ""},
		{"blank", "10.1.2.3:1", []string{"  "}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.remote
			for _, e := range c.emails {
				r.Header.Add("X-Forwarded-Email", e)
			}
			r.Header.Set("X-Forwarded-User", "Boss")
			id, ok := m.Identify(r)
			if got := map[bool]string{true: id.Email}[ok]; got != c.want {
				t.Fatalf("Identify = %q %v, want %q", id.Email, ok, c.want)
			}
			if ok && id.Name != "Boss" {
				t.Errorf("name = %q", id.Name)
			}
		})
	}
}

func TestCustomHeaderAndConfigErrors(t *testing.T) {
	m, err := New(Options{TrustedProxies: "127.0.0.1", EmailHeader: "Remote-Email"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-Email", "a@x.com")
	if _, ok := m.Identify(r); ok {
		t.Error("the default header was trusted although another one is configured")
	}
	r.Header.Set("Remote-Email", "a@x.com")
	if id, ok := m.Identify(r); !ok || id.Email != "a@x.com" {
		t.Errorf("custom header: %+v %v", id, ok)
	}
	for _, bad := range []string{"", "  ", "10.0.0.0/33", "proxy.local"} {
		if _, err := New(Options{TrustedProxies: bad}); err == nil {
			t.Errorf("TRUSTED_PROXY_CIDRS=%q accepted", bad)
		}
	}
}
