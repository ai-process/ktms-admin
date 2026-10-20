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
		SessionSecret:      env("SESSION_SECRET", ""),
		SessionTTL:         ttl,
		OIDCIssuer:         env("OIDC_ISSUER", ""),
		InsecureNoAuth:     env("INSECURE_NO_AUTH", "") == "true",
		ProxyAddr:          envFirst("", "KTMS_ADDR", "LLM_PROXY_ADDR"),
		ProxyAdminKey:      envFirst("", "KTMS_ADMIN_KEY", "LLM_PROXY_ADMIN_KEY"),
		ProxyTLS:           envFirst("", "KTMS_TLS", "LLM_PROXY_TLS") == "true",
		TokenmeterURL:      strings.TrimRight(env("TOKENMETER_URL", ""), "/"),
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
	case strings.TrimSpace(c.AllowedEmails) == "" && strings.TrimSpace(c.ViewerEmails) == "":
		return errors.New("ALLOWED_EMAILS is empty: nobody could sign in")
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

// envFirst returns the first non-empty variable; later keys are pre-rename names kept for old deployments.
func envFirst(def string, keys ...string) string {
	for _, k := range keys {
		if v := env(k, ""); v != "" {
			return v
		}
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
