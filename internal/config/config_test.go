package config

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := Config{PublicURL: "https://a.example.com", OIDC: OIDCProvider{ClientID: "i", ClientSecret: "s"}, AllowedEmails: "*@x.com", TokenmeterURL: "http://tm:8080"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(c *Config){
		"no backend":         func(c *Config) { c.TokenmeterURL = "" },
		"proxy without key":  func(c *Config) { c.ProxyAddr = "p:9090" },
		"bad tokenmeter url": func(c *Config) { c.TokenmeterURL = "tm:8080" },
		"no public url":      func(c *Config) { c.PublicURL = "" },
		"no google creds":    func(c *Config) { c.OIDC.ClientSecret = "" },
		"nobody allowed":     func(c *Config) { c.AllowedEmails = " " },
		"insecure on public": func(c *Config) { c.InsecureNoAuth = true },
	}
	for name, mutate := range cases {
		c := ok
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	for name, mutate := range map[string]func(c *Config){
		"google id only":           func(c *Config) { c.OIDC.ClientSecret = ""; c.TrustedProxyCIDRs = "10.0.0.0/8" },
		"cloudflare team only":     func(c *Config) { c.CFAccessTeamDomain = "team" },
		"cloudflare audience only": func(c *Config) { c.CFAccessAUD = "aud" },
	} {
		c := ok
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	for name, mutate := range map[string]func(c *Config){
		"header only": func(c *Config) { c.OIDC.ClientID, c.OIDC.ClientSecret, c.TrustedProxyCIDRs = "", "", "10.0.0.0/8" },
		"cloudflare only": func(c *Config) {
			c.OIDC.ClientID, c.OIDC.ClientSecret, c.CFAccessTeamDomain, c.CFAccessAUD = "", "", "team", "aud"
		},
	} {
		c := ok
		mutate(&c)
		if err := c.Validate(); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	teamsOnly := ok
	teamsOnly.AllowedEmails, teamsOnly.TeamMemberships = "", "lead@x.com:team-1:teamlead"
	if err := teamsOnly.Validate(); err != nil {
		t.Errorf("TEAM_MEMBERSHIPS alone rejected: %v", err)
	}
	dev := Config{PublicURL: "http://localhost:8080", InsecureNoAuth: true, ProxyAddr: "p:9090", ProxyAdminKey: "k"}
	if err := dev.Validate(); err != nil {
		t.Errorf("local insecure mode rejected: %v", err)
	}
	if !ok.UsageEnabled() || ok.ProxyEnabled() {
		t.Errorf("backend flags wrong")
	}
}

func TestLegacyProxyEnvStillWorks(t *testing.T) {
	for _, k := range []string{"KTMS_ADDR", "KTMS_ADMIN_KEY", "KTMS_TLS", "LLM_PROXY_TLS", "INSECURE_NO_AUTH",
		"TOKENMETER_URL", "SESSION_TTL", "OIDC_ISSUER", "VIEWER_EMAILS", "TEAM_MEMBERSHIPS", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "OIDC_PROVIDERS"} {
		t.Setenv(k, "")
	}
	t.Setenv("PUBLIC_URL", "https://a.example.com")
	t.Setenv("OIDC_CLIENT_ID", "i")
	t.Setenv("OIDC_CLIENT_SECRET", "s")
	t.Setenv("ALLOWED_EMAILS", "*@x.com")
	t.Setenv("LLM_PROXY_ADDR", "old:9090")
	t.Setenv("LLM_PROXY_ADMIN_KEY", "k")
	c, err := Load()
	if err != nil || c.ProxyAddr != "old:9090" || c.ProxyAdminKey != "k" || len(c.Notices) != 2 {
		t.Fatalf("legacy names: %+v, %v", c, err)
	}
	t.Setenv("KTMS_ADDR", "new:9090")
	t.Setenv("KTMS_ADMIN_KEY", "k2")
	if c, err := Load(); err != nil || c.ProxyAddr != "new:9090" || c.ProxyAdminKey != "k2" || len(c.Notices) != 2 {
		t.Fatalf("new names win and stale old ones are reported: %+v, %v", c, err)
	}
	t.Setenv("LLM_PROXY_ADDR", "")
	t.Setenv("LLM_PROXY_ADMIN_KEY", "")
	if c, err := Load(); err != nil || len(c.Notices) != 0 {
		t.Fatalf("new names alone: %+v, %v", c, err)
	}
}

func TestSignInMethodSettings(t *testing.T) {
	for _, k := range []string{"KTMS_ADDR", "KTMS_ADMIN_KEY", "LLM_PROXY_ADDR", "LLM_PROXY_ADMIN_KEY", "INSECURE_NO_AUTH",
		"OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_ISSUER", "OIDC_NAME", "VIEWER_EMAILS", "TEAM_MEMBERSHIPS"} {
		t.Setenv(k, "")
	}
	t.Setenv("PUBLIC_URL", "https://a.example.com")
	t.Setenv("TOKENMETER_URL", "http://tm:8080")
	t.Setenv("ALLOWED_EMAILS", "*@x.com")
	t.Setenv("GOOGLE_CLIENT_ID", "gid")
	t.Setenv("GOOGLE_CLIENT_SECRET", "gs")
	t.Setenv("OIDC_PROVIDERS", "Keycloak, corp-sso")
	t.Setenv("OIDC_KEYCLOAK_ISSUER", "https://kc.example.com/realms/x")
	t.Setenv("OIDC_KEYCLOAK_CLIENT_ID", "kc")
	t.Setenv("OIDC_KEYCLOAK_CLIENT_SECRET", "kcs")
	t.Setenv("OIDC_CORP_SSO_ISSUER", "https://sso.corp")
	t.Setenv("OIDC_CORP_SSO_CLIENT_ID", "c")
	t.Setenv("OIDC_CORP_SSO_CLIENT_SECRET", "cs")
	t.Setenv("OIDC_CORP_SSO_NAME", "Corp SSO")
	c, err := Load()
	if err != nil || c.OIDC.ClientID != "gid" || len(c.Notices) != 2 || len(c.OIDCProviders) != 2 ||
		c.OIDCProviders[0].Name != "keycloak" || c.OIDCProviders[1].Label != "Corp SSO" || c.OIDCProviders[1].ClientSecret != "cs" {
		t.Fatalf("config = %+v, %v", c, err)
	}
	t.Setenv("OIDC_CORP_SSO_CLIENT_SECRET", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OIDC_CORP_SSO_CLIENT_SECRET") {
		t.Fatalf("incomplete provider: %v", err)
	}
	t.Setenv("OIDC_PROVIDERS", "bad_name")
	if _, err := Load(); err == nil {
		t.Fatal("provider name with an underscore accepted")
	}
	t.Setenv("OIDC_PROVIDERS", "")
	t.Setenv("GITHUB_CLIENT_ID", "gh")
	if _, err := Load(); err == nil {
		t.Fatal("GitHub without a secret accepted")
	}
	t.Setenv("GITHUB_CLIENT_SECRET", "ghs")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	if c, err := Load(); err != nil || !c.GitHubEnabled() || c.OIDC.Enabled() {
		t.Fatalf("GitHub alone: %+v, %v", c, err)
	}
}

func TestTokenmeterTokenIsTrimmed(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://a.example.com")
	t.Setenv("OIDC_CLIENT_ID", "i")
	t.Setenv("OIDC_CLIENT_SECRET", "s")
	t.Setenv("ALLOWED_EMAILS", "*@x.com")
	t.Setenv("TOKENMETER_URL", "http://tm:8080")
	t.Setenv("TOKENMETER_TOKEN", "  tok\n") // a secret written with echo
	c, err := Load()
	if err != nil || c.TokenmeterToken != "tok" {
		t.Fatalf("TOKENMETER_TOKEN %q, %v; want it trimmed", c.TokenmeterToken, err)
	}
}
