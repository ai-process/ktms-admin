// Package config reads the service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string
	LogLevel   string

	// PublicURL is the externally visible base URL; the Google redirect URI is PublicURL + /auth/callback.
	PublicURL          string
	GoogleClientID     string
	GoogleClientSecret string
	AllowedEmails      string
	ViewerEmails       string
	TeamMemberships    string // email:team_id:role[:org_id]
	DevUserRole        string // admin | viewer | teamlead | member
	DevUserTeamID      string
	DevUserOrgID       string
	SessionSecret      string
	SessionTTL         time.Duration
	// OIDCIssuer replaces Google as the identity provider (any OIDC provider that returns verified e-mails).
	OIDCIssuer string
	// InsecureNoAuth skips login entirely. Only honoured for localhost PublicURLs.
	InsecureNoAuth bool

	ProxyAddr     string
	ProxyAdminKey string
	ProxyTLS      bool

	TokenmeterURL string

	// Notices name deprecated settings in use, for a startup warning; they
	// keep working through 1.x.
	Notices []string
}

// legacyEnv maps llm-proxy-era names to the KTMS ones that replace them.
var legacyEnv = [][2]string{
	{"LLM_PROXY_ADDR", "KTMS_ADDR"},
	{"LLM_PROXY_ADMIN_KEY", "KTMS_ADMIN_KEY"},
	{"LLM_PROXY_TLS", "KTMS_TLS"},
}

func Load() (*Config, error) {
	ttl, err := durationEnv("SESSION_TTL", 12*time.Hour)
	if err != nil {
		return nil, err
	}
	c := &Config{
		ListenAddr:         env("LISTEN_ADDR", ":8080"),
		LogLevel:           env("LOG_LEVEL", "info"),
		PublicURL:          strings.TrimRight(env("PUBLIC_URL", ""), "/"),
		GoogleClientID:     env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
		AllowedEmails:      env("ALLOWED_EMAILS", ""),
		ViewerEmails:       env("VIEWER_EMAILS", ""),
		TeamMemberships:    env("TEAM_MEMBERSHIPS", ""),
		DevUserRole:        env("DEV_USER_ROLE", ""),
		DevUserTeamID:      env("DEV_USER_TEAM_ID", ""),
		DevUserOrgID:       env("DEV_USER_ORG_ID", ""),
		SessionSecret:      env("SESSION_SECRET", ""),
		SessionTTL:         ttl,
		OIDCIssuer:         env("OIDC_ISSUER", ""),
		InsecureNoAuth:     env("INSECURE_NO_AUTH", "") == "true",
		ProxyAddr:          env("KTMS_ADDR", env("LLM_PROXY_ADDR", "")),
		ProxyAdminKey:      env("KTMS_ADMIN_KEY", env("LLM_PROXY_ADMIN_KEY", "")),
		ProxyTLS:           env("KTMS_TLS", env("LLM_PROXY_TLS", "")) == "true",
		TokenmeterURL:      strings.TrimRight(env("TOKENMETER_URL", ""), "/"),
	}
	for _, p := range legacyEnv {
		if env(p[0], "") != "" && env(p[1], "") == "" {
			c.Notices = append(c.Notices, p[0]+" is deprecated: rename it to "+p[1])
		}
	}
	return c, c.Validate()
}

func (c *Config) ProxyEnabled() bool { return c.ProxyAddr != "" }
func (c *Config) UsageEnabled() bool { return c.TokenmeterURL != "" }

func (c *Config) Validate() error {
	if !c.ProxyEnabled() && !c.UsageEnabled() {
		return errors.New("configure at least one backend: KTMS_ADDR (+ KTMS_ADMIN_KEY) and/or TOKENMETER_URL")
	}
	if c.ProxyEnabled() && c.ProxyAdminKey == "" {
		return errors.New("KTMS_ADDR is set but KTMS_ADMIN_KEY is empty")
	}
	if c.UsageEnabled() {
		u, err := url.Parse(c.TokenmeterURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("TOKENMETER_URL must be an http(s) URL, got %q", c.TokenmeterURL)
		}
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("PUBLIC_URL must be an http(s) URL, got %q", c.PublicURL)
		}
	}
	if c.InsecureNoAuth {
		if !c.isLocalPublicURL() {
			return errors.New("INSECURE_NO_AUTH is only allowed when PUBLIC_URL points at localhost or 127.0.0.1")
		}
		return nil
	}
	switch {
	case c.PublicURL == "":
		return errors.New("PUBLIC_URL is required (e.g. https://admin.example.com)")
	case c.GoogleClientID == "" || c.GoogleClientSecret == "":
		return errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required")
	case strings.TrimSpace(c.AllowedEmails) == "" && strings.TrimSpace(c.ViewerEmails) == "" && strings.TrimSpace(c.TeamMemberships) == "":
		return errors.New("ALLOWED_EMAILS, VIEWER_EMAILS and TEAM_MEMBERSHIPS are empty: nobody could sign in")
	}
	return nil
}

func (c *Config) isLocalPublicURL() bool {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// SecureCookies reports whether cookies get the Secure flag.
func (c *Config) SecureCookies() bool { return strings.HasPrefix(c.PublicURL, "https://") }

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func durationEnv(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration like 12h, got %q", key, v)
	}
	return d, nil
}
