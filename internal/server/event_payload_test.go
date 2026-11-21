package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/api/middleware"
	"github.com/Prescott-Data/athena/internal/memory"
	"github.com/Prescott-Data/athena/internal/models"
	"github.com/Prescott-Data/athena/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// memoryBlobStore keeps blobs in memory and records the ranges read from it.
type memoryBlobStore struct {
	blobs  map[string][]byte
	ranges [][2]int64
	err    error
}

func (b *memoryBlobStore) Upload(_ context.Context, key string, data io.Reader, _ string) (string, error) {
	raw, err := io.ReadAll(data)
	if err != nil {
		return "", err
	}
	if b.blobs == nil {
		b.blobs = map[string][]byte{}
	}
	uri := "mem://bucket/" + key
	b.blobs[uri] = raw
	return uri, nil
}
func (b *memoryBlobStore) Download(_ context.Context, uri string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.blobs[uri])), nil
}
func (b *memoryBlobStore) Size(_ context.Context, uri string) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}
	raw, ok := b.blobs[uri]
	if !ok {
		return 0, storage.ErrBlobNotFound
	}
	return int64(len(raw)), nil
}
func (b *memoryBlobStore) DownloadRange(_ context.Context, uri string, offset, length int64) (io.ReadCloser, error) {
	raw, ok := b.blobs[uri]
	if !ok {
		return nil, storage.ErrBlobNotFound
	}
	if offset < 0 || offset >= int64(len(raw)) || length <= 0 {
		return nil, fmt.Errorf("range %d+%d is outside the %d byte blob", offset, length, len(raw))
	}
	b.ranges = append(b.ranges, [2]int64{offset, length})
	end := offset + length
	if end > int64(len(raw)) {
		end = int64(len(raw))
	}
	return io.NopCloser(bytes.NewReader(raw[offset:end])), nil
}
func (b *memoryBlobStore) Delete(_ context.Context, uri string) error {
	delete(b.blobs, uri)
	return nil
}

// payloadServer serves session "s1" of t/u/a with an in-memory blob store and
// event collection.
func payloadServer() (*MemoryServer, *memoryBlobStore, map[primitive.ObjectID]*models.CognitiveEvent) {
	blobs := &memoryBlobStore{}
	events := map[primitive.ObjectID]*models.CognitiveEvent{}
	mockCache := new(MockSTMCache)
	mockCache.On("AddSTMEvent", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockRedis := new(MockRedis)
	mockRedis.On("LPush", mock.Anything, mock.Anything).Return(nil)
	s := &MemoryServer{
		stmCache:    mockCache,
		redisClient: mockRedis,
		blobStore:   blobs,
		persistCognitiveEvent: func(_ context.Context, e *models.CognitiveEvent) (primitive.ObjectID, error) {
			e.ID = primitive.NewObjectID()
			events[e.ID] = e
			return e.ID, nil
		},
		loadCognitiveEvent: func(_ context.Context, id primitive.ObjectID) (*models.CognitiveEvent, error) {
			e, ok := events[id]
			if !ok {
				return nil, memory.ErrEventNotFound
			}
			return e, nil
		},
	}
	s.getIDsFromSessionFunc = func(_ *MemoryServer, _ context.Context, id string) (string, string, string, error) {
		if id != "s1" {
			return "", "", "", errors.New("mongo: no documents in result")
		}
		return "t", "u", "a", nil
	}
	return s, blobs, events
}

// storePayload stores an observation with a payload through StoreEvent.
func storePayload(t *testing.T, s *MemoryServer, payload []byte) string {
	t.Helper()
	resp, err := s.StoreEvent(context.Background(), &gen.StoreEventRequest{
		SessionId: "s1", Role: "agent", Type: "observation", Payload: payload, MimeType: "text/plain",
	})
	require.NoError(t, err)
	return resp.EventId
}

func TestGetEventPayloadReadsRanges(t *testing.T) {
	s, blobs, _ := payloadServer()
	ctx := context.Background()
	payload := []byte("0123456789abcdefghij")
	eventID := storePayload(t, s, payload)

	whole, err := s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID})
	require.NoError(t, err)
	assert.Equal(t, payload, whole.Data)
	assert.Equal(t, int64(20), whole.TotalBytes)
	assert.Equal(t, int64(0), whole.Offset)
	assert.Equal(t, "text/plain", whole.MimeType)

	middle, err := s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID, Offset: 5, Length: 7})
	require.NoError(t, err)
	assert.Equal(t, []byte("56789ab"), middle.Data)
	assert.Equal(t, int64(20), middle.TotalBytes)
	assert.Equal(t, int64(5), middle.Offset)

	// A range that runs past the end is cut at the end.
	tail, err := s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID, Offset: 15, Length: 100})
	require.NoError(t, err)
	assert.Equal(t, []byte("fghij"), tail.Data)

	// Only the bytes asked for are fetched from the store.
	assert.Equal(t, [][2]int64{{0, 20}, {5, 7}, {15, 5}}, blobs.ranges)
}

func TestGetEventPayloadCapsLengthAtOneMiB(t *testing.T) {
	s, blobs, _ := payloadServer()
	ctx := context.Background()
	payload := bytes.Repeat([]byte("x"), maxPayloadRead+10)
	eventID := storePayload(t, s, payload)

	for _, length := range []int64{0, 5 << 20} {
		resp, err := s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID, Length: length})
		require.NoError(t, err)
		assert.Len(t, resp.Data, maxPayloadRead)
		assert.Equal(t, int64(maxPayloadRead+10), resp.TotalBytes)
	}
	rest, err := s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID, Offset: maxPayloadRead})
	require.NoError(t, err)
	assert.Len(t, rest.Data, 10)
	assert.Equal(t, [2]int64{maxPayloadRead, 10}, blobs.ranges[len(blobs.ranges)-1])
}

func TestGetEventPayloadPastTheEndIsEmpty(t *testing.T) {
	s, blobs, _ := payloadServer()
	eventID := storePayload(t, s, []byte("abc"))

	for _, offset := range []int64{3, 4000} {
		resp, err := s.GetEventPayload(context.Background(), &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID, Offset: offset})
		require.NoError(t, err)
		assert.Empty(t, resp.Data)
		assert.Equal(t, int64(3), resp.TotalBytes)
		assert.Equal(t, offset, resp.Offset)
		assert.Equal(t, "text/plain", resp.MimeType)
	}
	assert.Empty(t, blobs.ranges, "nothing is fetched for a read past the end")
}

func TestGetEventPayloadNotFoundCases(t *testing.T) {
	s, blobs, events := payloadServer()
	ctx := context.Background()
	withPayload := storePayload(t, s, []byte("abc"))

	// An event stored without a payload.
	plain, err := s.StoreEvent(ctx, &gen.StoreEventRequest{SessionId: "s1", Type: "observation", Content: "no payload"})
	require.NoError(t, err)

	// Events with a payload that belong to another tenant, user or agent.
	foreign := map[string]*models.CognitiveEvent{
		"tenant": {TenantID: "other", UserID: "u", AgentID: "a"},
		"user":   {TenantID: "t", UserID: "other", AgentID: "a"},
		"agent":  {TenantID: "t", UserID: "u", AgentID: "other"},
	}
	blobs.blobs["mem://bucket/foreign"] = []byte("secret")
	cases := map[string]string{
		"no payload":    plain.EventId,
		"unknown event": primitive.NewObjectID().Hex(),
		"malformed id":  "not-an-object-id",
		"empty id":      "",
	}
	for name, e := range foreign {
		e.ID = primitive.NewObjectID()
		e.BlobURI = "mem://bucket/foreign"
		events[e.ID] = e
		cases["another "+name] = e.ID.Hex()
	}
	for name, eventID := range cases {
		resp, err := s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID})
		assert.Nil(t, resp, name)
		assert.Equal(t, codes.NotFound, status.Code(err), name)
	}

	_, err = s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "nope", EventId: withPayload})
	assert.Equal(t, codes.NotFound, status.Code(err))

	// The object is gone, as after its chain was archived.
	blobs.blobs = map[string][]byte{}
	_, err = s.GetEventPayload(ctx, &gen.GetEventPayloadRequest{SessionId: "s1", EventId: withPayload})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetEventPayloadRejectsNegativeRange(t *testing.T) {
	s, _, _ := payloadServer()
	eventID := storePayload(t, s, []byte("abc"))
	for _, req := range []*gen.GetEventPayloadRequest{
		{SessionId: "s1", EventId: eventID, Offset: -1},
		{SessionId: "s1", EventId: eventID, Length: -1},
	} {
		_, err := s.GetEventPayload(context.Background(), req)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

func TestGetEventPayloadWithoutBlobStore(t *testing.T) {
	s, _, _ := payloadServer()
	eventID := storePayload(t, s, []byte("abc"))
	s.blobStore = nil

	_, err := s.GetEventPayload(context.Background(), &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetEventPayloadReportsStoreFailure(t *testing.T) {
	s, blobs, _ := payloadServer()
	eventID := storePayload(t, s, []byte("abc"))
	blobs.err = errors.New("storage down")

	_, err := s.GetEventPayload(context.Background(), &gen.GetEventPayloadRequest{SessionId: "s1", EventId: eventID})
	assert.Equal(t, codes.Internal, status.Code(err))
}

// The interceptor guards the route like every other session route: another
// tenant's session reads as not found.
func TestGetEventPayloadIsConfinedToTheCallersSessions(t *testing.T) {
	am := middleware.NewAuthMiddleware(keys)
	const method = "/memory.v1.MemoryService/GetEventPayload"
	id := primitive.NewObjectID().Hex()

	_, err := call(t, am, map[string]string{"x-api-key": "acme-key"}, method, &gen.GetEventPayloadRequest{SessionId: "s-acme", EventId: id})
	assert.NoError(t, err)
	_, err = call(t, am, map[string]string{"x-api-key": "acme-key"}, method, &gen.GetEventPayloadRequest{SessionId: "s-globex", EventId: id})
	assert.Equal(t, codes.NotFound, code(err))
	_, err = call(t, am, nil, method, &gen.GetEventPayloadRequest{SessionId: "s-acme", EventId: id})
	assert.Equal(t, codes.Unauthenticated, code(err))
}
