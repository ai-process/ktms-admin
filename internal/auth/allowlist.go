package auth

import (
	"fmt"
	"strings"
)

// Allowlist matches e-mail addresses against patterns such as
// "alice@example.com", "*@example.com" or "*@*.example.org". "*" matches any
// run of characters inside the local part or the domain, never across the "@".
type Allowlist struct {
	patterns []pattern
}

type pattern struct{ local, domain string }

// ParseAllowlist reads comma- or whitespace-separated patterns. Every pattern
// needs exactly one "@" so a stray "*" can never allow everyone by accident;
// "*@*" is the explicit way to admit any verified Google account.
func ParseAllowlist(list string) (*Allowlist, error) {
	a := &Allowlist{}
	for _, raw := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';' }) {
		p := strings.ToLower(strings.TrimSpace(raw))
		if p == "" {
			continue
		}
		if strings.Count(p, "@") != 1 {
			return nil, fmt.Errorf("invalid allowlist pattern %q: must contain exactly one @ (use *@example.com)", raw)
		}
		i := strings.Index(p, "@")
		a.patterns = append(a.patterns, pattern{local: p[:i], domain: p[i+1:]})
	}
	return a, nil
}

func (a *Allowlist) Len() int { return len(a.patterns) }

// Match reports whether the address is covered by any pattern.
func (a *Allowlist) Match(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if strings.Count(email, "@") != 1 {
		return false
	}
	i := strings.Index(email, "@")
	local, domain := email[:i], email[i+1:]
	if local == "" || domain == "" {
		return false
	}
	for _, p := range a.patterns {
		if glob(p.local, local) && glob(p.domain, domain) {
			return true
		}
	}
	return false
}

// glob matches s against pat where "*" stands for any (possibly empty) run.
func glob(pat, s string) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(s) {
		switch {
		case px < len(pat) && pat[px] == '*':
			star, mark = px, sx
			px++
		case px < len(pat) && pat[px] == s[sx]:
			px++
			sx++
		case star >= 0:
			mark++
			sx = mark
			px = star + 1
		default:
			return false
		}
	}
	for px < len(pat) && pat[px] == '*' {
		px++
	}
	return px == len(pat)
}

// AllowsAll reports whether a "*@*" pattern admits every address.
func (a *Allowlist) AllowsAll() bool {
	for _, p := range a.patterns {
		if p.local == "*" && p.domain == "*" {
			return true
		}
	}
	return false
}
