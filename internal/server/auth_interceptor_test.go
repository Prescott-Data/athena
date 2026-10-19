package server

import (
	"context"
	"errors"
	"testing"

	"github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/api/middleware"
	"github.com/Prescott-Data/athena/internal/config"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// sessions: s-acme belongs to acme/alice/bot, s-globex to globex/victim/bot.
func interceptorServer() *MemoryServer {
	s := &MemoryServer{}
	s.getIDsFromSessionFunc = func(_ *MemoryServer, _ context.Context, id string) (string, string, string, error) {
		switch id {
		case "s-acme":
			return "acme", "alice", "bot", nil
		case "s-globex":
			return "globex", "victim", "bot", nil
		case "s-flaky":
			return "", "", "", errors.New("server selection timeout")
		}
		return "", "", "", mongo.ErrNoDocuments
	}
	return s
}

func call(t *testing.T, am *middleware.AuthMiddleware, md map[string]string, method string, req interface{}) (interface{}, error) {
	t.Helper()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.New(md))
	handler := func(ctx context.Context, req interface{}) (interface{}, error) { return req, nil }
	return interceptorServer().AuthUnaryInterceptor(am)(ctx, req, &grpc.UnaryServerInfo{FullMethod: method}, handler)
}

func code(err error) codes.Code { return status.Code(err) }

var keys = &config.AuthConfig{RequireAPIKey: true, APIKeys: map[string]string{"acme-key": "acme", "svc-key": config.ServiceTenant}}

func TestInterceptorRequiresCredentials(t *testing.T) {
	am := middleware.NewAuthMiddleware(keys)
	_, err := call(t, am, nil, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-acme"})
	assert.Equal(t, codes.Unauthenticated, code(err))

	_, err = call(t, am, nil, healthCheckMethod, &gen.HealthCheckRequest{})
	assert.NoError(t, err, "health checks need no credentials")
}

func TestTenantKeyIsConfinedToItsTenant(t *testing.T) {
	am := middleware.NewAuthMiddleware(keys)
	md := map[string]string{"x-api-key": "acme-key"}

	_, err := call(t, am, md, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-acme"})
	assert.NoError(t, err)
	_, err = call(t, am, md, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-globex"})
	assert.Equal(t, codes.NotFound, code(err), "another tenant's session looks like a missing one")
	_, err = call(t, am, md, "/memory.v1.MemoryService/SearchMemory", &gen.SearchMemoryRequest{SessionId: "s-globex", Query: "x"})
	assert.Equal(t, codes.NotFound, code(err))

	out, err := call(t, am, md, createSessionMethod, &gen.CreateSessionRequest{UserId: "alice"})
	require.NoError(t, err)
	assert.Equal(t, "acme", out.(*gen.CreateSessionRequest).TenantId, "tenant comes from the key")
	_, err = call(t, am, md, createSessionMethod, &gen.CreateSessionRequest{TenantId: "globex", UserId: "victim"})
	assert.Equal(t, codes.PermissionDenied, code(err))

	_, err = call(t, am, md, graphAnalyticsAdmin, &gen.TriggerGraphAnalyticsRequest{})
	assert.Equal(t, codes.PermissionDenied, code(err))
}

func TestServiceKeyMayActForAnyTenant(t *testing.T) {
	am := middleware.NewAuthMiddleware(keys)
	md := map[string]string{"x-api-key": "svc-key"}
	for _, sid := range []string{"s-acme", "s-globex"} {
		_, err := call(t, am, md, "/memory.v1.MemoryService/StoreEvent", &gen.StoreEventRequest{SessionId: sid})
		assert.NoError(t, err)
	}
	out, err := call(t, am, md, createSessionMethod, &gen.CreateSessionRequest{TenantId: "globex", UserId: "victim"})
	require.NoError(t, err)
	assert.Equal(t, "globex", out.(*gen.CreateSessionRequest).TenantId)
	_, err = call(t, am, md, graphAnalyticsAdmin, &gen.TriggerGraphAnalyticsRequest{})
	assert.NoError(t, err)
}

func TestJWTIsConfinedToItsUserAndAgent(t *testing.T) {
	am := middleware.NewAuthMiddleware(&config.AuthConfig{RequireJWT: true, JWTSecret: "s3cret"})
	tok := signedJWT(t, "s3cret", "acme", "alice", "")

	_, err := call(t, am, map[string]string{"x-jwt-token": tok}, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-acme"})
	assert.NoError(t, err)

	// The reported exploit: a token for acme/alice creating a session in globex/victim.
	_, err = call(t, am, map[string]string{"x-jwt-token": tok}, createSessionMethod, &gen.CreateSessionRequest{TenantId: "globex", UserId: "victim"})
	assert.Equal(t, codes.PermissionDenied, code(err))
	_, err = call(t, am, map[string]string{"x-jwt-token": tok}, createSessionMethod, &gen.CreateSessionRequest{TenantId: "acme", UserId: "mallory"})
	assert.Equal(t, codes.PermissionDenied, code(err))

	bob := signedJWT(t, "s3cret", "acme", "bob", "")
	_, err = call(t, am, map[string]string{"x-jwt-token": bob}, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-acme"})
	assert.Equal(t, codes.NotFound, code(err), "same tenant, other user")

	otherAgent := signedJWT(t, "s3cret", "acme", "alice", "other-bot")
	_, err = call(t, am, map[string]string{"x-jwt-token": otherAgent}, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-acme"})
	assert.Equal(t, codes.NotFound, code(err), "token bound to another agent")
}

func TestUnauthenticatedModePassesThrough(t *testing.T) {
	am := middleware.NewAuthMiddleware(&config.AuthConfig{AllowUnauthenticated: true})
	_, err := call(t, am, nil, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-globex"})
	assert.NoError(t, err)
}

func signedJWT(t *testing.T, secret, tenant, user, agent string) string {
	t.Helper()
	claims := jwt.MapClaims{"tenant_id": tenant, "user_id": user}
	if agent != "" {
		claims["agent_id"] = agent
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

func TestSessionLookupFailuresDenyAccess(t *testing.T) {
	am := middleware.NewAuthMiddleware(keys)
	md := map[string]string{"x-api-key": "acme-key"}
	_, err := call(t, am, md, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "s-flaky"})
	assert.Equal(t, codes.Unavailable, code(err), "a failed ownership check must not fall through to the handler")
	_, err = call(t, am, md, "/memory.v1.MemoryService/GetContext", &gen.GetContextRequest{SessionId: "missing"})
	assert.Equal(t, codes.NotFound, code(err))
}
