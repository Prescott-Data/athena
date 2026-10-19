package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/internal/memory"
	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeSessions struct {
	record    *models.CognitiveChain
	count     int64
	lastAt    time.Time
	events    []models.CognitiveEvent
	chains    []models.CognitiveChain
	heat      map[string]float64
	topics    *memory.MultiTopicResult
	topicsIn  []models.CognitiveEvent
	erased    *models.CognitiveChain
	eraseErr  error
	chainsLim int
}

func (f *fakeSessions) SessionRecord(_ context.Context, id string) (*models.CognitiveChain, error) {
	if f.record == nil || f.record.ChainID != id {
		return nil, memory.ErrSessionNotFound
	}
	return f.record, nil
}
func (f *fakeSessions) SessionActivity(context.Context, string) (int64, time.Time, error) {
	return f.count, f.lastAt, nil
}
func (f *fakeSessions) SessionEvents(context.Context, *models.CognitiveChain) ([]models.CognitiveEvent, error) {
	return f.events, nil
}
func (f *fakeSessions) SessionChains(_ context.Context, _ *models.CognitiveChain, _ []models.CognitiveEvent, limit int) ([]models.CognitiveChain, error) {
	f.chainsLim = limit
	return f.chains, nil
}
func (f *fakeSessions) ChainHeat(_ context.Context, c *models.CognitiveChain) (float64, *models.HeatFactors, error) {
	h := f.heat[c.ChainID]
	return h, &models.HeatFactors{BaseImportance: h * 2, TimeDecay: 0.5, RecallStrength: 1}, nil
}
func (f *fakeSessions) AnalyzeEventTopics(_ context.Context, events []models.CognitiveEvent) (*memory.MultiTopicResult, error) {
	f.topicsIn = events
	return f.topics, nil
}
func (f *fakeSessions) EraseSession(_ context.Context, r *models.CognitiveChain) (*memory.SessionErasure, error) {
	f.erased = r
	if f.eraseErr != nil {
		return nil, f.eraseErr
	}
	return &memory.SessionErasure{STMEvents: 1, Events: 2, Chains: 3, Vectors: 4, Blobs: 5}, nil
}

func sessionFixture() *fakeSessions {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &fakeSessions{
		record: &models.CognitiveChain{ChainID: "s1", TenantID: "t", UserID: "u", AgentID: "a", Status: "active", StartedAt: start, Metadata: map[string]string{"origin_service": "jarviscore"}},
		count:  7,
		lastAt: start.Add(time.Hour),
	}
}

func TestSessionEndpointsRejectUnknownAndMissingSessions(t *testing.T) {
	s := &MemoryServer{sessions: sessionFixture()}
	ctx := context.Background()

	_, err := s.GetSession(ctx, &gen.GetSessionRequest{SessionId: "nope"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = s.DeleteSession(ctx, &gen.DeleteSessionRequest{SessionId: "nope"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = s.GetSegments(ctx, &gen.GetSegmentsRequest{SessionId: ""})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = s.GetHeatMetrics(ctx, &gen.GetHeatMetricsRequest{SessionId: "nope"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = s.AnalyzeTopics(ctx, &gen.AnalyzeTopicsRequest{SessionId: "nope"})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetSessionReturnsOwnerAndActivity(t *testing.T) {
	f := sessionFixture()
	resp, err := (&MemoryServer{sessions: f}).GetSession(context.Background(), &gen.GetSessionRequest{SessionId: "s1"})
	require.NoError(t, err)
	got := resp.Session
	assert.Equal(t, "s1", got.SessionId)
	assert.Equal(t, "t", got.TenantId)
	assert.Equal(t, "u", got.UserId)
	assert.Equal(t, "a", got.AgentId)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, int32(7), got.EventCount)
	assert.Equal(t, "jarviscore", got.Metadata["origin_service"])
	assert.True(t, got.CreatedAt.AsTime().Equal(f.record.StartedAt))
	assert.True(t, got.UpdatedAt.AsTime().Equal(f.lastAt))
}

func TestDeleteSessionReportsWhatWasRemoved(t *testing.T) {
	f := sessionFixture()
	resp, err := (&MemoryServer{sessions: f}).DeleteSession(context.Background(), &gen.DeleteSessionRequest{SessionId: "s1"})
	require.NoError(t, err)
	assert.Same(t, f.record, f.erased)
	assert.True(t, resp.Success)
	assert.Equal(t, []int32{1, 2, 3, 4, 5}, []int32{resp.StmEventsDeleted, resp.EventsDeleted, resp.ChainsDeleted, resp.VectorsDeleted, resp.BlobsDeleted})

	f.eraseErr = errors.New("milvus down")
	_, err = (&MemoryServer{sessions: f}).DeleteSession(context.Background(), &gen.DeleteSessionRequest{SessionId: "s1"})
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), "safe to retry")
}

func TestGetSegmentsMapsChainsAndClampsLimit(t *testing.T) {
	f := sessionFixture()
	f.chains = []models.CognitiveChain{{ChainID: "c1", Summary: "deploy plan", Topic: "deploy", QualityScore: 0.8, EventCount: 6}}
	f.heat = map[string]float64{"c1": 0.4}
	s := &MemoryServer{sessions: f}

	resp, err := s.GetSegments(context.Background(), &gen.GetSegmentsRequest{SessionId: "s1", Limit: 5000})
	require.NoError(t, err)
	assert.Equal(t, maxSegmentLimit, f.chainsLim)
	require.Len(t, resp.Segments, 1)
	seg := resp.Segments[0]
	assert.Equal(t, "c1", seg.Id)
	assert.Equal(t, "s1", seg.SessionId)
	assert.Equal(t, "deploy plan", seg.Content)
	assert.Equal(t, []string{"deploy"}, seg.Topics)
	assert.Equal(t, 0.8, seg.QualityScore)
	assert.Equal(t, 0.4, seg.Heat)
	assert.Equal(t, int32(6), seg.EventCount)
	assert.Equal(t, 0.8, seg.HeatFactors.BaseImportance)

	_, err = s.GetSegments(context.Background(), &gen.GetSegmentsRequest{SessionId: "s1"})
	require.NoError(t, err)
	assert.Equal(t, defaultSegmentLimit, f.chainsLim)
}

func TestGetHeatMetricsAveragesSegments(t *testing.T) {
	f := sessionFixture()
	f.chains = []models.CognitiveChain{{ChainID: "c1"}, {ChainID: "c2"}}
	f.heat = map[string]float64{"c1": 0.2, "c2": 0.6}

	resp, err := (&MemoryServer{sessions: f}).GetHeatMetrics(context.Background(), &gen.GetHeatMetricsRequest{SessionId: "s1"})
	require.NoError(t, err)
	m := resp.HeatMetrics
	assert.InDelta(t, 0.4, m.OverallHeat, 1e-9)
	assert.InDelta(t, 0.6, m.MaxHeat, 1e-9)
	assert.InDelta(t, 0.8, m.Breakdown.BaseImportance, 1e-9)
	assert.Equal(t, int32(2), m.SegmentCount)
	assert.Equal(t, int32(7), m.TotalInteractions)
	assert.True(t, m.LastActivity.AsTime().Equal(f.lastAt))

	f.chains = nil
	resp, err = (&MemoryServer{sessions: f}).GetHeatMetrics(context.Background(), &gen.GetHeatMetricsRequest{SessionId: "s1"})
	require.NoError(t, err)
	assert.Zero(t, resp.HeatMetrics.OverallHeat)
	assert.Zero(t, resp.HeatMetrics.SegmentCount)
}

func TestAnalyzeTopicsUsesStoredEventsOnly(t *testing.T) {
	f := sessionFixture()
	t0 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	f.events = []models.CognitiveEvent{
		{TenantID: "t", Content: "rollout", CreatedAt: t0},
		{TenantID: "", Content: "rollout", CreatedAt: t0}, // legacy unscoped copy
		{TenantID: "t", Content: "billing", CreatedAt: t0.Add(time.Minute)},
	}
	f.topics = &memory.MultiTopicResult{
		MainTopic: &memory.TopicSummary{Theme: "Rollout", Confidence: 0.9, EventIndices: []int{0}},
		SubTopics: []*memory.TopicSummary{{Theme: "Billing", Confidence: 0.7, EventIndices: []int{1}}},
	}

	resp, err := (&MemoryServer{sessions: f}).AnalyzeTopics(context.Background(), &gen.AnalyzeTopicsRequest{SessionId: "s1"})
	require.NoError(t, err)
	assert.Len(t, f.topicsIn, 2, "legacy copies are not analysed twice")
	require.Len(t, resp.Topics, 2)
	assert.Equal(t, "Rollout", resp.Topics[0].Topic)
	assert.Equal(t, int32(1), resp.Topics[0].Frequency)
	assert.True(t, resp.Topics[0].LastMentioned.AsTime().Equal(t0))
	assert.Equal(t, "Billing", resp.Topics[1].Topic)
	assert.True(t, resp.Topics[1].LastMentioned.AsTime().Equal(t0.Add(time.Minute)))
}
