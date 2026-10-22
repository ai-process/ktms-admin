package password

import (
	"fmt"
	"os"
	"strings"
)

// ParseUsers reads "email:hash" entries separated by whitespace, newlines or
// semicolons; lines starting with # are comments. Hashes contain commas and
// dollar signs, so neither separates entries. Unlike the allowlists, a bad
// entry stops the start: it is a credential, and a typo should be loud.
func ParseUsers(raw string, into map[string]string) error {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, e := range strings.FieldsFunc(line, func(r rune) bool { return r == ';' || r == ' ' || r == '\t' || r == '\r' }) {
			email, h, ok := strings.Cut(e, ":")
			email = strings.ToLower(strings.TrimSpace(email))
			if !ok || !strings.Contains(email, "@") {
				return fmt.Errorf("entry %q: want email:hash", redact(e))
			}
			if err := checkHash(h); err != nil {
				return fmt.Errorf("%s: %w", email, err)
			}
			if _, dup := into[email]; dup {
				return fmt.Errorf("%s is listed twice", email)
			}
			into[email] = h
		}
	}
	return nil
}

// LoadUsers merges ADMIN_USERS and the file ADMIN_USERS_FILE names.
func LoadUsers(inline, file string) (map[string]string, error) {
	users := map[string]string{}
	if err := ParseUsers(inline, users); err != nil {
		return nil, fmt.Errorf("ADMIN_USERS: %w", err)
	}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("ADMIN_USERS_FILE: %w", err)
		}
		if err := ParseUsers(string(raw), users); err != nil {
			return nil, fmt.Errorf("ADMIN_USERS_FILE: %w", err)
		}
	}
	return users, nil
}

// redact keeps an error from echoing a hash, or a password pasted by mistake.
func redact(e string) string {
	if email, _, ok := strings.Cut(e, ":"); ok {
		return email + ":…"
	}
	if len(e) > 3 {
		return e[:3] + "…"
	}
	return e
}
