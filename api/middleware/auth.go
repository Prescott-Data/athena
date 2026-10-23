package middleware

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Prescott-Data/athena/internal/config"
	"github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v5"
)

// Context keys for request context values
type CtxKey string

const (
	CtxTenantID CtxKey = "tenant_id"
	CtxUserID   CtxKey = "user_id"
	CtxAgentID  CtxKey = "agent_id"
)

// AuthMiddleware handles authentication for the Memory OS
type AuthMiddleware struct {
	config *config.AuthConfig
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(cfg *config.AuthConfig) *AuthMiddleware {
	return &AuthMiddleware{
		config: cfg,
	}
}

// AuthContext holds authentication information
type AuthContext struct {
	TenantID  string                 `json:"tenant_id"`
	UserID    string                 `json:"user_id"`
	AgentID   string                 `json:"agent_id,omitempty"`
	ServiceID string                 `json:"service_id"`
	SessionID string                 `json:"session_id,omitempty"`
	Claims    map[string]interface{} `json:"claims,omitempty"`
	APIKey    string                 `json:"api_key,omitempty"`
	// Service is true for a trusted backend authenticated by a service API key and
	// no JWT: it may act for any tenant, and the tenant comes from the request.
	Service bool `json:"service,omitempty"`
}

type authContextKey struct{}

// WithAuthContext returns ctx carrying the caller's authentication context.
func WithAuthContext(ctx context.Context, a *AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, a)
}

// AuthContextFrom returns the caller's authentication context, or nil when the
// server runs without authentication.
func AuthContextFrom(ctx context.Context) *AuthContext {
	a, _ := ctx.Value(authContextKey{}).(*AuthContext)
	return a
}

// Enabled reports whether requests must authenticate.
func (am *AuthMiddleware) Enabled() bool {
	return am.config.RequireAPIKey || am.config.RequireJWT
}

// GinAuthMiddleware returns a Gin middleware function for authentication
func (am *AuthMiddleware) GinAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Liveness is open. Metrics are aggregated across tenants, so they need a
		// service key unless MEMORY_OS_METRICS_PUBLIC opts out or authentication is off.
		metrics := c.Request.URL.Path == "/metrics"
		if c.Request.URL.Path == "/health" || (metrics && (am.config.MetricsPublic || !am.Enabled())) {
			c.Next()
			return
		}

		authCtx, err := am.authenticate(c.Request)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "authentication_failed",
				"message": err.Error(),
			})
			c.Abort()
			return
		}

		if metrics && !authCtx.Service {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "forbidden",
				"message": "metrics require a service key",
			})
			c.Abort()
			return
		}

		// Store auth context in Gin context
		c.Set("auth_context", authCtx)

		// Store tenant/user/agent in request context for service layer
		ctx := context.WithValue(c.Request.Context(), CtxTenantID, authCtx.TenantID)
		ctx = context.WithValue(ctx, CtxUserID, authCtx.UserID)
		ctx = context.WithValue(ctx, CtxAgentID, authCtx.AgentID)

		// Update request with new context
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// authenticate performs the actual authentication logic
func (am *AuthMiddleware) authenticate(req *http.Request) (*AuthContext, error) {
	// mTLS Certificate Verification (if enabled)
	if am.config.RequireMTLS {
		if err := am.verifyMTLS(req); err != nil {
			return nil, fmt.Errorf("mTLS verification failed: %w", err)
		}
	}
	return am.AuthenticateHeaders(req.Header.Get)
}

// AuthenticateHeaders authenticates a request from its headers (HTTP headers or
// gRPC metadata) and returns the caller's identity.
//
// An API key is bound to one tenant, or is a service key that may act for any
// tenant. A JWT names its tenant and user (and optionally agent); with both, the
// JWT's tenant must be the key's.
func (am *AuthMiddleware) AuthenticateHeaders(header func(string) string) (*AuthContext, error) {
	authCtx := &AuthContext{}

	// API Key Verification (if enabled)
	if am.config.RequireAPIKey {
		apiKey, err := extractAPIKey(header)
		if err != nil {
			return nil, fmt.Errorf("API key extraction failed: %w", err)
		}

		tenantID, err := am.verifyAPIKey(apiKey)
		if err != nil {
			return nil, fmt.Errorf("API key verification failed: %w", err)
		}

		authCtx.ServiceID = "memory-os"
		authCtx.APIKey = apiKey
		if tenantID == config.ServiceTenant {
			authCtx.Service = true
		} else {
			authCtx.TenantID = tenantID
		}
	}

	// JWT Verification (if enabled)
	if am.config.RequireJWT {
		token, err := extractJWT(header)
		if err != nil {
			return nil, fmt.Errorf("JWT extraction failed: %w", err)
		}

		claims, err := am.verifyJWT(token)
		if err != nil {
			return nil, fmt.Errorf("JWT verification failed: %w", err)
		}

		// Extract and validate required tenant_id and user_id from JWT
		tenantID, ok := claims["tenant_id"].(string)
		if !ok || tenantID == "" {
			return nil, fmt.Errorf("missing or invalid tenant_id in JWT claims")
		}

		userID, ok := claims["user_id"].(string)
		if !ok || userID == "" {
			return nil, fmt.Errorf("missing or invalid user_id in JWT claims")
		}

		if authCtx.TenantID != "" && authCtx.TenantID != tenantID {
			return nil, fmt.Errorf("JWT tenant_id does not match the API key's tenant")
		}
		authCtx.TenantID = tenantID
		authCtx.UserID = userID
		authCtx.AgentID = getStringClaim(claims, "agent_id")
		authCtx.SessionID = getStringClaim(claims, "session_id")
		authCtx.Claims = claims
		authCtx.Service = false
	}

	return authCtx, nil
}

// verifyMTLS verifies the client certificate for mutual TLS
func (am *AuthMiddleware) verifyMTLS(req *http.Request) error {
	if req.TLS == nil {
		return fmt.Errorf("TLS connection required")
	}

	if len(req.TLS.PeerCertificates) == 0 {
		return fmt.Errorf("client certificate required")
	}

	// Load CA certificate for verification
	if am.config.ClientCACertFile != "" {
		// In production, you'd load the CA cert and verify the client cert
		// For now, we'll just check that a certificate is present
		clientCert := req.TLS.PeerCertificates[0]
		if clientCert.Subject.CommonName == "" {
			return fmt.Errorf("invalid client certificate: missing common name")
		}
	}

	return nil
}

// extractAPIKey extracts the API key from the request headers
func extractAPIKey(header func(string) string) (string, error) {
	// Check Authorization header: "Bearer <api-key>"
	authHeader := header("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			return parts[1], nil
		}
	}

	// Check X-API-Key header
	apiKey := header("X-API-Key")
	if apiKey != "" {
		return apiKey, nil
	}

	return "", fmt.Errorf("API key not found in request headers")
}

// verifyAPIKey checks the API key and returns the tenant it may act for, or
// config.ServiceTenant for a service key.
func (am *AuthMiddleware) verifyAPIKey(apiKey string) (string, error) {
	for key, tenant := range am.config.APIKeys {
		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(key)) == 1 {
			return tenant, nil
		}
	}
	return "", fmt.Errorf("invalid API key")
}

// extractJWT extracts the JWT token from the request headers
func extractJWT(header func(string) string) (string, error) {
	// Check Authorization header: "JWT <token>"
	authHeader := header("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "JWT" {
			return parts[1], nil
		}
	}

	// Check X-JWT-Token header
	jwtToken := header("X-JWT-Token")
	if jwtToken != "" {
		return jwtToken, nil
	}

	return "", fmt.Errorf("JWT token not found in request headers")
}

// verifyJWT verifies the JWT token and returns the claims
func (am *AuthMiddleware) verifyJWT(tokenString string) (map[string]interface{}, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Validate the signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(am.config.JWTSecret), nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Validate required claims
	if claims["tenant_id"] == nil {
		return nil, fmt.Errorf("missing tenant_id in token")
	}
	if claims["user_id"] == nil {
		return nil, fmt.Errorf("missing user_id in token")
	}

	// Check expiration
	if exp, ok := claims["exp"].(float64); ok {
		if time.Unix(int64(exp), 0).Before(time.Now()) {
			return nil, fmt.Errorf("token expired")
		}
	}

	return claims, nil
}

// getStringClaim safely extracts a string claim from JWT claims
func getStringClaim(claims map[string]interface{}, key string) string {
	if value, ok := claims[key].(string); ok {
		return value
	}
	return ""
}

// GetAuthContext extracts the authentication context from a Gin context
func GetAuthContext(c *gin.Context) (*AuthContext, error) {
	authCtx, exists := c.Get("auth_context")
	if !exists {
		return nil, fmt.Errorf("authentication context not found")
	}

	ctx, ok := authCtx.(*AuthContext)
	if !ok {
		return nil, fmt.Errorf("invalid authentication context type")
	}

	return ctx, nil
}

// Context utility functions for extracting tenant/user/agent from request context

// TenantIDFromContext extracts the tenant ID from the request context
func TenantIDFromContext(ctx context.Context) (string, error) {
	tenantID, ok := ctx.Value(CtxTenantID).(string)
	if !ok || tenantID == "" {
		return "", fmt.Errorf("tenant_id not found in request context")
	}
	return tenantID, nil
}

// UserIDFromContext extracts the user ID from the request context
func UserIDFromContext(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(CtxUserID).(string)
	if !ok || userID == "" {
		return "", fmt.Errorf("user_id not found in request context")
	}
	return userID, nil
}

// AgentIDFromContext extracts the agent ID from the request context
func AgentIDFromContext(ctx context.Context) (string, error) {
	agentID, ok := ctx.Value(CtxAgentID).(string)
	if !ok {
		return "", nil // Agent ID is optional
	}
	return agentID, nil
}

// ValidateTenantUserMatch validates that client-provided tenant/user IDs match authenticated values
func ValidateTenantUserMatch(ctx context.Context, clientTenantID, clientUserID string) error {
	authTenantID, err := TenantIDFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get authenticated tenant_id: %w", err)
	}

	authUserID, err := UserIDFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get authenticated user_id: %w", err)
	}

	// If client provided tenant_id, it must match authenticated value
	if clientTenantID != "" && clientTenantID != authTenantID {
		return fmt.Errorf("client tenant_id (%s) does not match authenticated tenant_id (%s)",
			clientTenantID, authTenantID)
	}

	// If client provided user_id, it must match authenticated value
	if clientUserID != "" && clientUserID != authUserID {
		return fmt.Errorf("client user_id (%s) does not match authenticated user_id (%s)",
			clientUserID, authUserID)
	}

	return nil
}

// MTLSConfig creates a TLS configuration for mutual TLS
func MTLSConfig(certFile, keyFile, caCertFile string) (*tls.Config, error) {
	// Load server certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	// Load CA certificate for client verification
	var clientCAs *x509.CertPool
	if caCertFile != "" {
		caCert, err := os.ReadFile(caCertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		clientCAs = x509.NewCertPool()
		if !clientCAs.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		MinVersion:   tls.VersionTLS12,
	}

	return tlsConfig, nil
}
