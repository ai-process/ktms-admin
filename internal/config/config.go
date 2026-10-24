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

	// PublicURL is the externally visible base URL; the OIDC redirect URI is PublicURL + /auth/callback.
	PublicURL string
	// OIDC is the unnamed OpenID Connect provider, Google unless OIDC_ISSUER says otherwise.
	OIDC OIDCProvider
	// OIDCProviders are the named ones from OIDC_PROVIDERS.
	OIDCProviders      []OIDCProvider
	GitHubClientID     string
	GitHubClientSecret string
	GitHubURL          string
	AllowedEmails      string
	ViewerEmails       string
	TeamMemberships    string // email:team_id:role[:org_id]
	DevUserRole        string // admin | viewer | teamlead | member
	DevUserTeamID      string
	DevUserOrgID       string
	SessionSecret      string
	SessionTTL         time.Duration
	// TrustedProxyCIDRs turns on the header method: an access proxy at these
	// addresses names the user in AuthHeaderEmail.
	TrustedProxyCIDRs string
	AuthHeaderEmail   string
	AuthHeaderName    string
	// AdminUsers and AdminUsersFile turn on password sign-in: email:hash entries.
	AdminUsers     string
	AdminUsersFile string
	// CFAccessTeamDomain and CFAccessAUD turn on Cloudflare Access.
	CFAccessTeamDomain string
	CFAccessAUD        string
	// InsecureNoAuth skips login entirely. Only honoured for localhost PublicURLs.
	InsecureNoAuth bool

	ProxyAddr     string
	ProxyAdminKey string
	// ProxyReadKey, a key with the admin:read scope, carries every read and
	// every viewer's call, so those cannot change Keystone. Optional.
	ProxyReadKey string
	ProxyTLS     bool

	TokenmeterURL string
	// TokenmeterToken is a light-tokenmeter bearer token with the read scope.
	TokenmeterToken string

	// Notices name deprecated settings in use, for a startup warning; they
	// keep working through 1.x.
	Notices []string
}

// OIDCProvider is one OpenID Connect provider; Name is empty for the unnamed one.
type OIDCProvider struct {
	Name, Label, Issuer, ClientID, ClientSecret string
}

func (p OIDCProvider) Enabled() bool { return p.ClientID != "" && p.ClientSecret != "" }

// envPrefix is where a named provider's settings live: OIDC_<NAME>_.
func (p OIDCProvider) envPrefix() string {
	return "OIDC_" + strings.ToUpper(strings.ReplaceAll(p.Name, "-", "_")) + "_"
}

// legacyEnv maps older names to the ones that replace them.
var legacyEnv = [][2]string{
	{"GOOGLE_CLIENT_ID", "OIDC_CLIENT_ID"},
	{"GOOGLE_CLIENT_SECRET", "OIDC_CLIENT_SECRET"},
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
		ListenAddr: env("LISTEN_ADDR", ":8080"),
		LogLevel:   env("LOG_LEVEL", "info"),
		PublicURL:  strings.TrimRight(env("PUBLIC_URL", ""), "/"),
		OIDC: OIDCProvider{
			Label: env("OIDC_NAME", ""), Issuer: env("OIDC_ISSUER", ""),
			ClientID:     env("OIDC_CLIENT_ID", env("GOOGLE_CLIENT_ID", "")),
			ClientSecret: env("OIDC_CLIENT_SECRET", env("GOOGLE_CLIENT_SECRET", "")),
		},
		GitHubClientID:     env("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret: env("GITHUB_CLIENT_SECRET", ""),
		GitHubURL:          env("GITHUB_URL", ""),
		AllowedEmails:      env("ALLOWED_EMAILS", ""),
		ViewerEmails:       env("VIEWER_EMAILS", ""),
		TeamMemberships:    env("TEAM_MEMBERSHIPS", ""),
		DevUserRole:        env("DEV_USER_ROLE", ""),
		DevUserTeamID:      env("DEV_USER_TEAM_ID", ""),
		DevUserOrgID:       env("DEV_USER_ORG_ID", ""),
		SessionSecret:      env("SESSION_SECRET", ""),
		SessionTTL:         ttl,
		TrustedProxyCIDRs:  env("TRUSTED_PROXY_CIDRS", ""),
		AuthHeaderEmail:    env("AUTH_HEADER_EMAIL", ""),
		AuthHeaderName:     env("AUTH_HEADER_NAME", ""),
		AdminUsers:         env("ADMIN_USERS", ""),
		AdminUsersFile:     env("ADMIN_USERS_FILE", ""),
		CFAccessTeamDomain: env("CF_ACCESS_TEAM_DOMAIN", ""),
		CFAccessAUD:        env("CF_ACCESS_AUD", ""),
		InsecureNoAuth:     env("INSECURE_NO_AUTH", "") == "true",
		ProxyAddr:          env("KTMS_ADDR", env("LLM_PROXY_ADDR", "")),
		ProxyAdminKey:      env("KTMS_ADMIN_KEY", env("LLM_PROXY_ADMIN_KEY", "")),
		ProxyReadKey:       env("KTMS_READ_KEY", ""),
		ProxyTLS:           env("KTMS_TLS", env("LLM_PROXY_TLS", "")) == "true",
		TokenmeterURL:      strings.TrimRight(env("TOKENMETER_URL", ""), "/"),
		// Trimmed: a secret written with echo ends in a newline, which no header may carry.
		TokenmeterToken: strings.TrimSpace(env("TOKENMETER_TOKEN", "")),
	}
	for _, name := range strings.FieldsFunc(env("OIDC_PROVIDERS", ""), func(r rune) bool { return r == ',' || r == ' ' }) {
		p := OIDCProvider{Name: strings.ToLower(name)}
		pre := p.envPrefix()
		p.Label, p.Issuer = env(pre+"NAME", ""), env(pre+"ISSUER", "")
		p.ClientID, p.ClientSecret = env(pre+"CLIENT_ID", ""), env(pre+"CLIENT_SECRET", "")
		c.OIDCProviders = append(c.OIDCProviders, p)
	}
	for _, p := range legacyEnv {
		switch old, cur := env(p[0], ""), env(p[1], ""); {
		case old != "" && cur == "":
			c.Notices = append(c.Notices, p[0]+" is deprecated: rename it to "+p[1])
		case old != "" && old != cur:
			c.Notices = append(c.Notices, p[0]+" is ignored because "+p[1]+" is set: remove it")
		}
	}
	return c, c.Validate()
}

func (c *Config) GitHubEnabled() bool   { return c.GitHubClientID != "" && c.GitHubClientSecret != "" }
func (c *Config) PasswordEnabled() bool { return c.AdminUsers != "" || c.AdminUsersFile != "" }
func (c *Config) HeaderEnabled() bool   { return c.TrustedProxyCIDRs != "" }
func (c *Config) CFAccessEnabled() bool { return c.CFAccessTeamDomain != "" && c.CFAccessAUD != "" }

func (c *Config) ProxyEnabled() bool { return c.ProxyAddr != "" }
func (c *Config) UsageEnabled() bool { return c.TokenmeterURL != "" }

func (c *Config) Validate() error {
	if !c.ProxyEnabled() && !c.UsageEnabled() {
		return errors.New("configure at least one backend: KTMS_ADDR (+ KTMS_ADMIN_KEY) and/or TOKENMETER_URL (LLM_PROXY_* names still work)")
	}
	if c.ProxyEnabled() && c.ProxyAdminKey == "" {
		return errors.New("KTMS_ADDR (or LLM_PROXY_ADDR) is set but KTMS_ADMIN_KEY (or LLM_PROXY_ADMIN_KEY) is empty")
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
	case (c.OIDC.ClientID == "") != (c.OIDC.ClientSecret == ""):
		return errors.New("set both OIDC_CLIENT_ID and OIDC_CLIENT_SECRET (or the GOOGLE_* names), or neither")
	case c.OIDC.Issuer != "" && !c.OIDC.Enabled():
		return errors.New("OIDC_ISSUER is set but OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are not")
	case (c.GitHubClientID == "") != (c.GitHubClientSecret == ""):
		return errors.New("set both GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET, or neither")
	case (c.CFAccessTeamDomain == "") != (c.CFAccessAUD == ""):
		return errors.New("set both CF_ACCESS_TEAM_DOMAIN and CF_ACCESS_AUD, or neither")
	case strings.TrimSpace(c.AllowedEmails) == "" && strings.TrimSpace(c.ViewerEmails) == "" && strings.TrimSpace(c.TeamMemberships) == "":
		return errors.New("ALLOWED_EMAILS, VIEWER_EMAILS and TEAM_MEMBERSHIPS are empty: nobody could sign in")
	}
	seen := map[string]bool{}
	for _, p := range c.OIDCProviders {
		pre := p.envPrefix()
		switch {
		case !validName(p.Name):
			return fmt.Errorf("OIDC_PROVIDERS: %q must be lower-case letters, digits and dashes", p.Name)
		case seen[p.Name]:
			return fmt.Errorf("OIDC_PROVIDERS lists %q twice", p.Name)
		case p.Issuer == "" || !p.Enabled():
			return fmt.Errorf("OIDC_PROVIDERS names %q: set %sISSUER, %sCLIENT_ID and %sCLIENT_SECRET", p.Name, pre, pre, pre)
		}
		seen[p.Name] = true
	}
	if !c.OIDC.Enabled() && len(c.OIDCProviders) == 0 && !c.GitHubEnabled() && !c.PasswordEnabled() && !c.HeaderEnabled() && !c.CFAccessEnabled() {
		return errors.New("configure a sign-in method: ADMIN_USERS, OIDC_CLIENT_ID + OIDC_CLIENT_SECRET (Google by default), " +
			"GITHUB_CLIENT_ID + GITHUB_CLIENT_SECRET, TRUSTED_PROXY_CIDRS, or CF_ACCESS_TEAM_DOMAIN + CF_ACCESS_AUD")
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

// validName keeps a provider name safe in a URL path and an environment variable.
func validName(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
