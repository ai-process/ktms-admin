// Package header trusts an access proxy in front of the console (oauth2-proxy,
// Authelia, Authentik, Pomerium, Tailscale) to name the user in a header.
package header

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/rs/zerolog/log"

	"github.com/ai-process/llm-proxy-admin/internal/auth"
)

const methodID = "header"

type Options struct {
	// TrustedProxies are the addresses the proxy connects from. A header from
	// anywhere else is ignored, because any client can send one.
	TrustedProxies string
	// EmailHeader names the user's e-mail; empty means X-Forwarded-Email.
	EmailHeader string
	// NameHeader optionally names their display name.
	NameHeader string
}

type Method struct {
	trusted     []netip.Prefix
	emailHeader string
	nameHeader  string
	warned      atomic.Bool
}

func New(o Options) (*Method, error) {
	trusted, err := ParsePrefixes(o.TrustedProxies)
	if err != nil {
		return nil, err
	}
	if len(trusted) == 0 {
		return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS is empty: the header method needs the proxy's addresses")
	}
	m := &Method{trusted: trusted, emailHeader: o.EmailHeader, nameHeader: o.NameHeader}
	if m.emailHeader == "" {
		m.emailHeader = "X-Forwarded-Email"
	}
	return m, nil
}

// ParsePrefixes reads comma- or space-separated CIDRs; a bare address means just that host.
func ParsePrefixes(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS: %q is neither a CIDR nor an address", f)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

func (m *Method) ID() string                      { return methodID }
func (m *Method) Mount(*http.ServeMux, auth.Host) {}
func (m *Method) Identify(r *http.Request) (auth.Identity, bool) {
	emails := r.Header.Values(m.emailHeader)
	if len(emails) == 0 {
		return auth.Identity{}, false
	}
	if !m.fromProxy(r) {
		// Logged once: it is either a misconfigured TRUSTED_PROXY_CIDRS or a
		// forgery attempt, and a client could repeat it on every request.
		if m.warned.CompareAndSwap(false, true) {
			log.Warn().Str("remote", r.RemoteAddr).Str("header", m.emailHeader).
				Msg("identity header from an address outside TRUSTED_PROXY_CIDRS ignored (logged once)")
		}
		return auth.Identity{}, false
	}
	email := strings.TrimSpace(emails[0])
	if len(emails) != 1 || !plausibleEmail(email) {
		return auth.Identity{}, false
	}
	id := auth.Identity{Email: email}
	if m.nameHeader != "" {
		id.Name = strings.TrimSpace(r.Header.Get(m.nameHeader))
	}
	return id, true
}

func (m *Method) fromProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range m.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// plausibleEmail refuses lists and blanks a proxy might pass through.
func plausibleEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, ", ;\t")
}
