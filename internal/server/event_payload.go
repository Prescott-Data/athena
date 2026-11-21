package server

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/internal/memory"
	"github.com/Prescott-Data/athena/internal/storage"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxPayloadRead is the most payload bytes one GetEventPayload call returns, and
// what it returns when the request names no length.
const maxPayloadRead = 1 << 20

// GetEventPayload returns a byte range of the payload stored with an event, so a
// caller can read an artifact back a page at a time without bucket credentials.
// An event that does not exist, belongs to another tenant, user or agent than the
// session, or carries no payload is reported as not found alike.
func (s *MemoryServer) GetEventPayload(ctx context.Context, req *gen.GetEventPayloadRequest) (*gen.GetEventPayloadResponse, error) {
	sessionID := req.SessionId
	tenantID, userID, agentID, err := s.getIDsFromSessionFunc(s, ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found or invalid: %v", err)
	}
	if req.Offset < 0 || req.Length < 0 {
		return nil, status.Error(codes.InvalidArgument, "offset and length must not be negative")
	}
	if s.blobStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "blob storage is not configured")
	}

	notFound := status.Errorf(codes.NotFound, "event %s has no payload in session %s", req.EventId, sessionID)

	eventID, err := primitive.ObjectIDFromHex(req.EventId)
	if err != nil {
		return nil, notFound
	}
	load := s.loadCognitiveEvent
	if load == nil {
		if s.stmStore == nil {
			return nil, status.Error(codes.FailedPrecondition, "memory store is not configured")
		}
		load = s.stmStore.CognitiveEvent
	}
	event, err := load(ctx, eventID)
	if errors.Is(err, memory.ErrEventNotFound) {
		return nil, notFound
	}
	if err != nil {
		slog.Error("Failed to load event for payload read", slog.String("session_id", sessionID), slog.String("event_id", req.EventId), slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to load event: %v", err)
	}
	if event.TenantID != tenantID || event.UserID != userID || event.AgentID != agentID || event.BlobURI == "" {
		return nil, notFound
	}

	total, err := s.blobStore.Size(ctx, event.BlobURI)
	if err != nil {
		return nil, s.payloadReadError(err, notFound, sessionID, req.EventId)
	}

	resp := &gen.GetEventPayloadResponse{
		TotalBytes: total,
		Offset:     req.Offset,
		MimeType:   event.BlobMimeType,
	}
	if req.Offset >= total {
		return resp, nil
	}
	length := req.Length
	if length == 0 || length > maxPayloadRead {
		length = maxPayloadRead
	}
	if remaining := total - req.Offset; length > remaining {
		length = remaining
	}

	body, err := s.blobStore.DownloadRange(ctx, event.BlobURI, req.Offset, length)
	if err != nil {
		return nil, s.payloadReadError(err, notFound, sessionID, req.EventId)
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, length))
	if err != nil {
		return nil, s.payloadReadError(err, notFound, sessionID, req.EventId)
	}
	memory.BlobStorageOps.WithLabelValues("download", "configured", "success").Inc()

	resp.Data = data
	return resp, nil
}

// payloadReadError maps a blob store failure to a gRPC status. An object that is
// gone (its chain was archived) reads as an event without a payload.
func (s *MemoryServer) payloadReadError(err error, notFound error, sessionID, eventID string) error {
	if errors.Is(err, storage.ErrBlobNotFound) {
		return notFound
	}
	memory.BlobStorageOps.WithLabelValues("download", "configured", "error").Inc()
	slog.Error("Failed to read payload from blob storage", slog.String("session_id", sessionID), slog.String("event_id", eventID), slog.String("error", err.Error()))
	return status.Errorf(codes.Internal, "failed to read payload: %v", err)
}
