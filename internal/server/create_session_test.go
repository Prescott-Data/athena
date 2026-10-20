package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	gen "github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/internal/config"
)

func TestCreateSessionReturnsItsCreationTime(t *testing.T) {
	uri := os.Getenv("ATHENA_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set ATHENA_TEST_MONGO_URI to run CreateSession against MongoDB")
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	dbName := "athena_create_session_test"
	t.Cleanup(func() {
		_ = client.Database(dbName).Drop(ctx)
		_ = client.Disconnect(ctx)
	})
	cfg := &config.Config{}
	cfg.Database.MongoDB.Database = dbName
	s := &MemoryServer{config: cfg, mongoClient: client}

	before := time.Now().Add(-time.Second)
	resp, err := s.CreateSession(ctx, &gen.CreateSessionRequest{TenantId: "acme", UserId: "u1", AgentId: "a1"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.SessionId)
	require.NotNil(t, resp.CreatedAt, "CreateSession must report when the session was created")
	require.True(t, resp.CreatedAt.AsTime().After(before))
}
