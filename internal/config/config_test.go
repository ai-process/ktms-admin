package config

import "testing"

func TestValidate(t *testing.T) {
	ok := Config{PublicURL: "https://a.example.com", GoogleClientID: "i", GoogleClientSecret: "s", AllowedEmails: "*@x.com", TokenmeterURL: "http://tm:8080"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(c *Config){
		"no backend":         func(c *Config) { c.TokenmeterURL = "" },
		"proxy without key":  func(c *Config) { c.ProxyAddr = "p:9090" },
		"bad tokenmeter url": func(c *Config) { c.TokenmeterURL = "tm:8080" },
		"no public url":      func(c *Config) { c.PublicURL = "" },
		"no google creds":    func(c *Config) { c.GoogleClientSecret = "" },
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
		"TOKENMETER_URL", "SESSION_TTL", "OIDC_ISSUER", "VIEWER_EMAILS", "TEAM_MEMBERSHIPS"} {
		t.Setenv(k, "")
	}
	t.Setenv("PUBLIC_URL", "https://a.example.com")
	t.Setenv("GOOGLE_CLIENT_ID", "i")
	t.Setenv("GOOGLE_CLIENT_SECRET", "s")
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
