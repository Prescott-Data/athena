package server

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Prescott-Data/athena/api/middleware"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	healthCheckMethod   = "/memory.v1.MemoryService/HealthCheck"
	createSessionMethod = "/memory.v1.MemoryService/CreateSession"
	graphAnalyticsAdmin = "/memory.v1.MemoryService/TriggerGraphAnalytics"
)

// sessionBound is implemented by every request that acts on an existing session.
type sessionBound interface {
	GetSessionId() string
}

// AuthUnaryInterceptor authenticates every gRPC call, whether it arrives directly
// on the gRPC port or through the REST gateway (which forwards the auth headers as
// metadata), and authorises it against the tenant, user and agent of the session
// it touches. It is the single place tenant isolation is enforced.
func (s *MemoryServer) AuthUnaryInterceptor(am *middleware.AuthMiddleware) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if info.FullMethod == healthCheckMethod || !am.Enabled() {
			return handler(ctx, req)
		}

		md, _ := metadata.FromIncomingContext(ctx)
		caller, err := am.AuthenticateHeaders(func(name string) string {
			if v := md.Get(name); len(v) > 0 {
				return v[0]
			}
			return ""
		})
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
		}
		ctx = middleware.WithAuthContext(ctx, caller)

		if err := s.authorize(ctx, caller, info.FullMethod, req); err != nil {
			slog.Warn("Request denied",
				slog.String("method", info.FullMethod),
				slog.String("caller_tenant", caller.TenantID),
				slog.String("caller_user", caller.UserID),
				slog.String("error", err.Error()))
			return nil, err
		}
		return handler(ctx, req)
	}
}

// authorize decides whether caller may make the request. A service caller may act
// for any tenant. Any other caller is confined to its tenant (and to its user and
// agent when its credentials name them).
func (s *MemoryServer) authorize(ctx context.Context, caller *middleware.AuthContext, method string, req interface{}) error {
	if caller.Service {
		return nil
	}
	if caller.TenantID == "" {
		return status.Error(codes.PermissionDenied, "credentials do not identify a tenant")
	}

	if r, ok := req.(sessionBound); ok {
		tenantID, userID, agentID, err := s.getIDsFromSessionFunc(s, ctx, r.GetSessionId())
		if errors.Is(err, mongo.ErrNoDocuments) {
			return status.Errorf(codes.NotFound, "session not found or invalid")
		}
		if err != nil {
			// Fail closed: the handler would look the session up again and, if
			// that succeeded, act on it unchecked.
			return status.Errorf(codes.Unavailable, "could not verify session ownership: %v", err)
		}
		if tenantID != caller.TenantID ||
			(caller.UserID != "" && userID != caller.UserID) ||
			(caller.AgentID != "" && agentID != caller.AgentID) {
			// Not found rather than forbidden, so session IDs of other tenants
			// cannot be probed.
			return status.Errorf(codes.NotFound, "session not found or invalid")
		}
		return nil
	}

	if m, ok := req.(proto.Message); ok && hasScopeFields(m) {
		// Requests that name a scope (CreateSession, EraseMemory, ...) act on the
		// caller's own: identity comes from the credentials, and a request naming
		// someone else is refused. A JWT confines the request to its user (and
		// agent), so a user cannot address a whole tenant.
		if err := bindScopeField(m, "tenant_id", caller.TenantID); err != nil {
			return err
		}
		if err := bindScopeField(m, "user_id", caller.UserID); err != nil {
			return err
		}
		return bindScopeField(m, "agent_id", caller.AgentID)
	}

	if method == graphAnalyticsAdmin {
		return status.Error(codes.PermissionDenied, "administrative call requires a service API key")
	}
	return status.Errorf(codes.PermissionDenied, "method %s is not available to tenant credentials", method)
}

// hasScopeFields reports whether a request message names a tenant.
func hasScopeFields(m proto.Message) bool {
	fd := m.ProtoReflect().Descriptor().Fields().ByName("tenant_id")
	return fd != nil && fd.Kind() == protoreflect.StringKind
}

// bindScopeField binds a request's scope field to the caller's identity: an empty
// field takes the identity, a different value is refused. An empty identity (for
// example the user of a tenant key) leaves the field as the request gives it.
func bindScopeField(m proto.Message, name, identity string) error {
	if identity == "" {
		return nil
	}
	msg := m.ProtoReflect()
	fd := msg.Descriptor().Fields().ByName(protoreflect.Name(name))
	if fd == nil || fd.Kind() != protoreflect.StringKind {
		return nil
	}
	current := msg.Get(fd).String()
	if current != "" && current != identity {
		return status.Errorf(codes.PermissionDenied, "%s in the request does not match the credentials", name)
	}
	msg.Set(fd, protoreflect.ValueOfString(identity))
	return nil
}
