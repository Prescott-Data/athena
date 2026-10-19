package middleware

import (
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/config"
	jwt "github.com/golang-jwt/jwt/v5"
)

func headers(h map[string]string) func(string) string {
	return func(k string) string { return h[k] }
}

func token(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAPIKeyIdentity(t *testing.T) {
	am := NewAuthMiddleware(&config.AuthConfig{RequireAPIKey: true, APIKeys: map[string]string{"tenant-key": "acme", "svc-key": config.ServiceTenant}})

	a, err := am.AuthenticateHeaders(headers(map[string]string{"X-API-Key": "tenant-key"}))
	if err != nil || a.TenantID != "acme" || a.Service {
		t.Fatalf("tenant key: %+v %v", a, err)
	}
	a, err = am.AuthenticateHeaders(headers(map[string]string{"X-API-Key": "svc-key"}))
	if err != nil || !a.Service || a.TenantID != "" {
		t.Fatalf("service key: %+v %v", a, err)
	}
	if _, err := am.AuthenticateHeaders(headers(map[string]string{"X-API-Key": "wrong"})); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := am.AuthenticateHeaders(headers(nil)); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestJWTIdentity(t *testing.T) {
	am := NewAuthMiddleware(&config.AuthConfig{RequireJWT: true, JWTSecret: "s3cret"})
	good := token(t, "s3cret", jwt.MapClaims{"tenant_id": "acme", "user_id": "alice", "agent_id": "bot", "exp": time.Now().Add(time.Hour).Unix()})

	a, err := am.AuthenticateHeaders(headers(map[string]string{"X-JWT-Token": good}))
	if err != nil || a.TenantID != "acme" || a.UserID != "alice" || a.AgentID != "bot" || a.Service {
		t.Fatalf("jwt: %+v %v", a, err)
	}
	if _, err := am.AuthenticateHeaders(headers(map[string]string{"Authorization": "JWT " + good})); err != nil {
		t.Fatalf("Authorization: JWT header rejected: %v", err)
	}
	forged := token(t, "your-secret-key", jwt.MapClaims{"tenant_id": "acme", "user_id": "alice"})
	if _, err := am.AuthenticateHeaders(headers(map[string]string{"X-JWT-Token": forged})); err == nil {
		t.Fatal("token signed with another secret accepted")
	}
	expired := token(t, "s3cret", jwt.MapClaims{"tenant_id": "acme", "user_id": "alice", "exp": time.Now().Add(-time.Hour).Unix()})
	if _, err := am.AuthenticateHeaders(headers(map[string]string{"X-JWT-Token": expired})); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestAPIKeyAndJWTMustAgree(t *testing.T) {
	am := NewAuthMiddleware(&config.AuthConfig{RequireAPIKey: true, RequireJWT: true, JWTSecret: "s3cret",
		APIKeys: map[string]string{"acme-key": "acme", "svc-key": config.ServiceTenant}})
	globex := token(t, "s3cret", jwt.MapClaims{"tenant_id": "globex", "user_id": "bob"})

	if _, err := am.AuthenticateHeaders(headers(map[string]string{"X-API-Key": "acme-key", "X-JWT-Token": globex})); err == nil {
		t.Fatal("JWT for another tenant than the key's accepted")
	}
	a, err := am.AuthenticateHeaders(headers(map[string]string{"X-API-Key": "svc-key", "X-JWT-Token": globex}))
	if err != nil || a.Service || a.TenantID != "globex" || a.UserID != "bob" {
		t.Fatalf("service key with JWT must act as the token's user: %+v %v", a, err)
	}
}
