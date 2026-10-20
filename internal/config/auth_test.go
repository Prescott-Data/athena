package config

import (
	"strings"
	"testing"
)

func TestParseAPIKeys(t *testing.T) {
	keys, err := parseAPIKeys("k1=acme, k2=*,", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"k1": "acme", "k2": ServiceTenant, "legacy": ServiceTenant}
	if len(keys) != len(want) {
		t.Fatalf("got %v", keys)
	}
	for k, v := range want {
		if keys[k] != v {
			t.Errorf("%s: got %q want %q", k, keys[k], v)
		}
	}
	if _, err := parseAPIKeys("nokey", ""); err == nil {
		t.Error("an entry without a tenant must be rejected")
	}
	if _, err := parseAPIKeys("=acme", ""); err == nil {
		t.Error("an empty key must be rejected")
	}
}

func validConfig() *Config {
	c := &Config{}
	c.Server.Port, c.Server.GRPCPort = 8080, 9090
	c.Database.MongoDB.URI = "mongodb://x"
	c.Auth.RequireAPIKey = true
	c.Auth.APIKeys = map[string]string{"k": "acme"}
	return c
}

func TestValidateAuth(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	c := validConfig()
	c.Auth.RequireAPIKey = false
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "MEMORY_OS_ALLOW_UNAUTHENTICATED") {
		t.Errorf("no authentication must be refused unless explicitly allowed, got %v", err)
	}
	c.Auth.AllowUnauthenticated = true
	if err := c.Validate(); err != nil {
		t.Errorf("explicitly allowed unauthenticated mode rejected: %v", err)
	}

	c = validConfig()
	c.Auth.APIKeys = map[string]string{}
	if err := c.Validate(); err == nil {
		t.Error("API key auth without keys must be refused")
	}

	c = validConfig()
	c.Auth.RequireJWT = true
	if err := c.Validate(); err == nil {
		t.Error("JWT auth without a secret must be refused")
	}
	c.Auth.JWTSecret = placeholderJWTSecret
	if err := c.Validate(); err == nil {
		t.Error("the published placeholder secret must be refused")
	}
	c.Auth.JWTSecret = "a-real-secret"
	if err := c.Validate(); err != nil {
		t.Errorf("JWT with a secret rejected: %v", err)
	}
}
