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
	dev := Config{PublicURL: "http://localhost:8080", InsecureNoAuth: true, ProxyAddr: "p:9090", ProxyAdminKey: "k"}
	if err := dev.Validate(); err != nil {
		t.Errorf("local insecure mode rejected: %v", err)
	}
	if !ok.UsageEnabled() || ok.ProxyEnabled() {
		t.Errorf("backend flags wrong")
	}
}

func TestKTMSEnvFallback(t *testing.T) {
	t.Setenv("LLM_PROXY_ADDR", "old:9090")
	if got := envFirst("", "KTMS_ADDR", "LLM_PROXY_ADDR"); got != "old:9090" {
		t.Errorf("legacy name ignored: got %q", got)
	}
	t.Setenv("KTMS_ADDR", "new:9090")
	if got := envFirst("", "KTMS_ADDR", "LLM_PROXY_ADDR"); got != "new:9090" {
		t.Errorf("KTMS_ADDR must win over LLM_PROXY_ADDR: got %q", got)
	}
	if got := envFirst("d", "KTMS_UNSET_X", "LLM_PROXY_UNSET_X"); got != "d" {
		t.Errorf("default not used: got %q", got)
	}
}
