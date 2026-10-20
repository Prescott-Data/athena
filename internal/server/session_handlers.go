package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/internal/memory"
	"github.com/Prescott-Data/athena/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultSegmentLimit = 10
	maxSegmentLimit     = 100
	// maxTopicEvents bounds how much of a session is sent to the topic analyser.
	maxTopicEvents = 200
)

// sessionStore is the part of the STM store the session endpoints use.
type sessionStore interface {
	SessionRecord(ctx context.Context, sessionID string) (*models.CognitiveChain, error)
	SessionActivity(ctx context.Context, sessionID string) (int64, time.Time, error)
	SessionEvents(ctx context.Context, record *models.CognitiveChain) ([]models.CognitiveEvent, error)
	SessionChains(ctx context.Context, record *models.CognitiveChain, events []models.CognitiveEvent, limit int) ([]models.CognitiveChain, error)
	ChainHeat(ctx context.Context, chain *models.CognitiveChain) (float64, *models.HeatFactors, error)
	AnalyzeEventTopics(ctx context.Context, events []models.CognitiveEvent) (*memory.MultiTopicResult, error)
	EraseSession(ctx context.Context, record *models.CognitiveChain) (*memory.SessionErasure, error)
	EraseScope(ctx context.Context, tenantID, userID, agentID string) (*memory.ScopeErasure, error)
}

func (s *MemoryServer) sessionStore() sessionStore {
	if s.sessions != nil {
		return s.sessions
	}
	if s.stmStore != nil {
		return s.stmStore
	}
	return nil
}

// loadSession resolves a session ID to its record, mapping lookup failures to gRPC codes.
func (s *MemoryServer) loadSession(ctx context.Context, sessionID string) (sessionStore, *models.CognitiveChain, error) {
	if sessionID == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	store := s.sessionStore()
	if store == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "memory store is not configured")
	}
	record, err := store.SessionRecord(ctx, sessionID)
	if errors.Is(err, memory.ErrSessionNotFound) {
		return nil, nil, status.Errorf(codes.NotFound, "session %s not found", sessionID)
	}
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "failed to load session: %v", err)
	}
	return store, record, nil
}

// GetSession returns a session's owner, metadata and activity.
func (s *MemoryServer) GetSession(ctx context.Context, req *gen.GetSessionRequest) (*gen.GetSessionResponse, error) {
	store, record, err := s.loadSession(ctx, req.SessionId)
	if err != nil {
		return nil, err
	}
	count, lastAt, err := store.SessionActivity(ctx, record.ChainID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load session activity: %v", err)
	}
	updated := record.StartedAt
	if lastAt.After(updated) {
		updated = lastAt
	}
	return &gen.GetSessionResponse{Session: &gen.Session{
		SessionId:  record.ChainID,
		TenantId:   record.TenantID,
		UserId:     record.UserID,
		AgentId:    record.AgentID,
		Status:     record.Status,
		Metadata:   record.Metadata,
		CreatedAt:  timestamppb.New(record.StartedAt),
		UpdatedAt:  timestamppb.New(updated),
		EventCount: int32(count),
	}}, nil
}

// DeleteSession erases everything Athena holds for a session. See
// memory.STMStore.EraseSession for what is removed.
func (s *MemoryServer) DeleteSession(ctx context.Context, req *gen.DeleteSessionRequest) (*gen.DeleteSessionResponse, error) {
	store, record, err := s.loadSession(ctx, req.SessionId)
	if err != nil {
		return nil, err
	}
	report, err := store.EraseSession(ctx, record)
	if err != nil {
		slog.Error("Failed to delete session", slog.String("session_id", record.ChainID), slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to delete session (safe to retry): %v", err)
	}
	return &gen.DeleteSessionResponse{
		Success:           true,
		StmEventsDeleted:  int32(report.STMEvents),
		EventsDeleted:     int32(report.Events),
		ChainsDeleted:     int32(report.Chains),
		VectorsDeleted:    int32(report.Vectors),
		BlobsDeleted:      int32(report.Blobs),
		LtmEntriesDeleted: int32(report.GraphEntries),
	}, nil
}

// EraseMemory erases a tenant's user, a user's agent, or a whole tenant. See
// memory.STMStore.EraseScope.
func (s *MemoryServer) EraseMemory(ctx context.Context, req *gen.EraseMemoryRequest) (*gen.EraseMemoryResponse, error) {
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.AgentId != "" && req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id needs user_id")
	}
	store := s.sessionStore()
	if store == nil {
		return nil, status.Error(codes.FailedPrecondition, "memory store is not configured")
	}
	report, err := store.EraseScope(ctx, req.TenantId, req.UserId, req.AgentId)
	if err != nil {
		slog.Error("Failed to erase memory", slog.String("tenant_id", req.TenantId), slog.String("user_id", req.UserId),
			slog.String("agent_id", req.AgentId), slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to erase memory (safe to retry): %v", err)
	}
	return &gen.EraseMemoryResponse{
		Success:           true,
		SessionsDeleted:   int32(report.Sessions),
		StmEventsDeleted:  int32(report.STMEvents),
		EventsDeleted:     int32(report.Events),
		ChainsDeleted:     int32(report.Chains),
		VectorsDeleted:    int32(report.Vectors),
		BlobsDeleted:      int32(report.Blobs),
		LtmEntriesDeleted: int32(report.GraphEntries),
	}, nil
}

// GetSegments returns the MTM chains summarised from a session's events, newest first.
func (s *MemoryServer) GetSegments(ctx context.Context, req *gen.GetSegmentsRequest) (*gen.GetSegmentsResponse, error) {
	store, record, err := s.loadSession(ctx, req.SessionId)
	if err != nil {
		return nil, err
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = defaultSegmentLimit
	}
	if limit > maxSegmentLimit {
		limit = maxSegmentLimit
	}
	events, err := store.SessionEvents(ctx, record)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load session events: %v", err)
	}
	chains, err := store.SessionChains(ctx, record, events, limit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load session segments: %v", err)
	}
	segments := make([]*gen.Segment, 0, len(chains))
	for i := range chains {
		chain := &chains[i]
		heat, factors, err := store.ChainHeat(ctx, chain)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to compute heat for segment %s: %v", chain.ChainID, err)
		}
		var topics []string
		if chain.Topic != "" {
			topics = []string{chain.Topic}
		}
		segments = append(segments, &gen.Segment{
			Id:           chain.ChainID,
			SessionId:    record.ChainID,
			Content:      chain.Summary,
			Topics:       topics,
			HeatFactors:  heatFactorsProto(factors),
			QualityScore: chain.QualityScore,
			CreatedAt:    timestamppb.New(chain.StartedAt),
			Heat:         heat,
			EventCount:   int32(chain.EventCount),
			LastEventAt:  timestamppb.New(chain.LastEventAt),
		})
	}
	return &gen.GetSegmentsResponse{Segments: segments}, nil
}

// GetHeatMetrics aggregates the current heat of a session's segments.
func (s *MemoryServer) GetHeatMetrics(ctx context.Context, req *gen.GetHeatMetricsRequest) (*gen.GetHeatMetricsResponse, error) {
	store, record, err := s.loadSession(ctx, req.SessionId)
	if err != nil {
		return nil, err
	}
	count, lastAt, err := store.SessionActivity(ctx, record.ChainID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load session activity: %v", err)
	}
	events, err := store.SessionEvents(ctx, record)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load session events: %v", err)
	}
	chains, err := store.SessionChains(ctx, record, events, 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load session segments: %v", err)
	}

	metrics := &gen.HeatMetrics{
		TotalInteractions: int32(count),
		SegmentCount:      int32(len(chains)),
		Breakdown:         &gen.HeatFactors{},
	}
	if !lastAt.IsZero() {
		metrics.LastActivity = timestamppb.New(lastAt)
	}
	for i := range chains {
		heat, factors, err := store.ChainHeat(ctx, &chains[i])
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to compute heat for segment %s: %v", chains[i].ChainID, err)
		}
		metrics.OverallHeat += heat
		if heat > metrics.MaxHeat {
			metrics.MaxHeat = heat
		}
		if factors != nil {
			metrics.Breakdown.BaseImportance += factors.BaseImportance
			metrics.Breakdown.TimeDecay += factors.TimeDecay
			metrics.Breakdown.RecallStrength += factors.RecallStrength
		}
	}
	if n := float64(len(chains)); n > 0 {
		metrics.OverallHeat /= n
		metrics.Breakdown.BaseImportance /= n
		metrics.Breakdown.TimeDecay /= n
		metrics.Breakdown.RecallStrength /= n
	}
	return &gen.GetHeatMetricsResponse{HeatMetrics: metrics}, nil
}

// AnalyzeTopics runs the topic analyser over the session's most recent events.
func (s *MemoryServer) AnalyzeTopics(ctx context.Context, req *gen.AnalyzeTopicsRequest) (*gen.AnalyzeTopicsResponse, error) {
	store, record, err := s.loadSession(ctx, req.SessionId)
	if err != nil {
		return nil, err
	}
	events, err := store.SessionEvents(ctx, record)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load session events: %v", err)
	}
	events = memory.StoredEvents(events)
	if len(events) > maxTopicEvents {
		events = events[len(events)-maxTopicEvents:]
	}
	if len(events) == 0 {
		return &gen.AnalyzeTopicsResponse{}, nil
	}
	result, err := store.AnalyzeEventTopics(ctx, events)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "topic analysis failed: %v", err)
	}
	var found []*memory.TopicSummary
	if result.MainTopic != nil {
		found = append(found, result.MainTopic)
	}
	found = append(found, result.SubTopics...)

	topics := make([]*gen.TopicSummary, 0, len(found))
	for _, t := range found {
		summary := &gen.TopicSummary{
			Topic:      t.Theme,
			Confidence: t.Confidence,
			Frequency:  int32(len(t.EventIndices)),
		}
		var last time.Time
		for _, i := range t.EventIndices {
			if i >= 0 && i < len(events) && events[i].CreatedAt.After(last) {
				last = events[i].CreatedAt
			}
		}
		if !last.IsZero() {
			summary.LastMentioned = timestamppb.New(last)
		}
		topics = append(topics, summary)
	}
	return &gen.AnalyzeTopicsResponse{Topics: topics}, nil
}

func heatFactorsProto(f *models.HeatFactors) *gen.HeatFactors {
	if f == nil {
		return nil
	}
	return &gen.HeatFactors{
		BaseImportance: f.BaseImportance,
		TimeDecay:      f.TimeDecay,
		RecallStrength: f.RecallStrength,
	}
}
