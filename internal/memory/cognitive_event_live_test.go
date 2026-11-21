package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// An event is found again by the id StoreCognitiveEvent returned, with the payload's
// address and the scope a payload read checks. Set ATHENA_TEST_MONGO_URI to run.
func TestACognitiveEventIsLoadedByItsID(t *testing.T) {
	db := isolationDB(t)
	store := &STMStore{db: db}
	ctx := context.Background()

	id, err := store.StoreCognitiveEvent(ctx, &models.CognitiveEvent{
		TenantID: "acme", UserID: "alice", AgentID: "researcher", SessionID: "s1",
		Role: "agent", Type: models.STMEventType("observation"), Content: "results attached",
		BlobURI: "s3://bucket/acme/s1/object", BlobMimeType: "text/plain",
		Status: "in_stm", CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	event, err := store.CognitiveEvent(ctx, id)
	require.NoError(t, err)
	require.Equal(t, id, event.ID)
	require.Equal(t, []string{"acme", "alice", "researcher"}, []string{event.TenantID, event.UserID, event.AgentID})
	require.Equal(t, "s3://bucket/acme/s1/object", event.BlobURI)
	require.Equal(t, "text/plain", event.BlobMimeType)

	_, err = store.CognitiveEvent(ctx, primitive.NewObjectID())
	require.ErrorIs(t, err, ErrEventNotFound)
}
