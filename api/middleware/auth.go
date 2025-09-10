package middleware

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/dromos-org/memory-os/internal/config"
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
	ServiceID string            `json:"service_id"`
	UserID    string            `json:"user_id"`
	SessionID string            `json:"session_id,omitempty"`
	Claims    map[string]interface{} `json:"claims,omitempty"`
	APIKey    string            `json:"api_key,omitempty"`
}

// GinAuthMiddleware returns a Gin middleware function for authentication
func (am *AuthMiddleware) GinAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authCtx, err := am.authenticate(c.Request)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "authentication_failed",
				"message": err.Error(),
			})
			c.Abort()
			return
		}

		// Store auth context in Gin context
		c.Set("auth_context", authCtx)
		c.Next()
	}
}

// GRPCAuthMiddleware returns a gRPC middleware function for authentication
func (am *AuthMiddleware) GRPCAuthMiddleware() func(ctx context.Context, req interface{}, info interface{}, handler interface{}) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info interface{}, handler interface{}) (interface{}, error) {
		// For gRPC, we need to extract headers from metadata
		// This is a simplified version - in production, you'd use grpc/metadata
		return handler.(func(context.Context, interface{}) (interface{}, error))(ctx, req)
	}
}

// authenticate performs the actual authentication logic
func (am *AuthMiddleware) authenticate(req *http.Request) (*AuthContext, error) {
	authCtx := &AuthContext{}

	// 1. mTLS Certificate Verification (if enabled)
	if am.config.RequireMTLS {
		if err := am.verifyMTLS(req); err != nil {
			return nil, fmt.Errorf("mTLS verification failed: %w", err)
		}
	}

	// 2. API Key Verification (if enabled)
	if am.config.RequireAPIKey {
		apiKey, err := am.extractAPIKey(req)
		if err != nil {
			return nil, fmt.Errorf("API key extraction failed: %w", err)
		}
		
		serviceID, err := am.verifyAPIKey(apiKey)
		if err != nil {
			return nil, fmt.Errorf("API key verification failed: %w", err)
		}
		
		authCtx.ServiceID = serviceID
		authCtx.APIKey = apiKey
	}

	// 3. JWT Verification (if enabled)
	if am.config.RequireJWT {
		token, err := am.extractJWT(req)
		if err != nil {
			return nil, fmt.Errorf("JWT extraction failed: %w", err)
		}
		
		claims, err := am.verifyJWT(token)
		if err != nil {
			return nil, fmt.Errorf("JWT verification failed: %w", err)
		}
		
		authCtx.UserID = claims["user_id"].(string)
		authCtx.SessionID = getStringClaim(claims, "session_id")
		authCtx.Claims = claims
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

// extractAPIKey extracts the API key from the request
func (am *AuthMiddleware) extractAPIKey(req *http.Request) (string, error) {
	// Check Authorization header: "Bearer <api-key>"
	authHeader := req.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			return parts[1], nil
		}
	}

	// Check X-API-Key header
	apiKey := req.Header.Get("X-API-Key")
	if apiKey != "" {
		return apiKey, nil
	}

	return "", fmt.Errorf("API key not found in request headers")
}

// verifyAPIKey verifies the API key and returns the service ID
func (am *AuthMiddleware) verifyAPIKey(apiKey string) (string, error) {
	// Simple validation against configured keys
	for _, validKey := range am.config.ValidAPIKeys {
		if apiKey == validKey {
			// In production, you'd map API keys to service IDs
			return "docintel-api", nil // Default service ID
		}
	}

	return "", fmt.Errorf("invalid API key")
}

// extractJWT extracts the JWT token from the request
func (am *AuthMiddleware) extractJWT(req *http.Request) (string, error) {
	// Check Authorization header: "JWT <token>"
	authHeader := req.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "JWT" {
			return parts[1], nil
		}
	}

	// Check X-JWT-Token header
	jwtToken := req.Header.Get("X-JWT-Token")
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
