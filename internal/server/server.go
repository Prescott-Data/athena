package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"bitbucket.org/dromos/memory-os/api/grpc/gen"
	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/config"
	"bitbucket.org/dromos/memory-os/internal/database"
	"bitbucket.org/dromos/memory-os/internal/memory"
	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stmCache defines the interface for the short-term memory cache.
// This allows for mocking in tests.
type stmCache interface {
	AddSTMEvent(ctx context.Context, tenantID, userID, agentID string, event memory.STMEvent) error
}

// MemoryServer represents the main Memory OS server
type MemoryServer struct {
	gen.UnimplementedMemoryServiceServer // Embed for forward compatibility
	config *config.Config
	stmStore *memory.STMStore
	stmCache stmCache
	taskQueue memory.TaskQueuer
	mongoClient *mongo.Client
	redisClient cache.Interface
	// getIDsFromSessionFunc allows mocking the DB call in tests
	getIDsFromSessionFunc func(s *MemoryServer, ctx context.Context, sessionID string) (string, string, string, error)
}

// NewMemoryServer creates a new Memory OS server instance
func NewMemoryServer(cfg *config.Config) (*MemoryServer, error) {
	// Initialize database connections
	mongoClient, db, err := database.ConnectMongoDB(database.ConnectionConfig{
		URI: cfg.Database.MongoDB.URI,
		DatabaseName: cfg.Database.MongoDB.Database,
		ConnectTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	redisClient, err := cache.NewRedisClient()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Initialize memory components
	stmStore := memory.NewSTMStore(db, redisClient)
	stmCache := memory.NewSTMCache(redisClient)
	taskQueue := memory.NewTaskQueue(redisClient)

	server := &MemoryServer{
		config: cfg,
		stmStore: stmStore,
		stmCache: stmCache,
		taskQueue: taskQueue,
		mongoClient: mongoClient,
		redisClient: redisClient,
	}
	// Point the function field to the real method
	server.getIDsFromSessionFunc = (*MemoryServer).getIDsFromSession

	return server, nil
}

// Close gracefully shuts down the server and cleans up resources
func (s *MemoryServer) Close() error {
	// Close MongoDB connection
	if s.mongoClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.mongoClient.Disconnect(ctx); err != nil {
			log.Printf("Error closing MongoDB connection: %v", err)
		}
	}

	return nil
}

// GetConfig returns the server configuration
func (s *MemoryServer) GetConfig() *config.Config {
	return s.config
}

// GetSTMStore returns the STM store for worker initialization
func (s *MemoryServer) GetSTMStore() *memory.STMStore {
	return s.stmStore
}

// GetTaskQueue returns the task queue for worker initialization
func (s *MemoryServer) GetTaskQueue() memory.TaskQueuer {
	return s.taskQueue
}

// GetRedisClient returns the Redis client for worker initialization
func (s *MemoryServer) GetRedisClient() cache.Interface {
	return s.redisClient
}

// GetMongoClient returns the MongoDB client
func (s *MemoryServer) GetMongoClient() *mongo.Client {
	return s.mongoClient
}

// StoreInteraction is the main entry point for new events.
func (s *MemoryServer) StoreInteraction(ctx context.Context, req *gen.StoreInteractionRequest) (*gen.StoreInteractionResponse, error) {
	sessionID := req.SessionId
	tenantID, userID, agentID, err := s.getIDsFromSessionFunc(s, ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found or invalid: %v", err)
	}

	// Create and process the user event
	userEvent := memory.STMEvent{
		Role: "user",
		Type: models.STMEventTypeMessage,
		Content: req.UserMessage,
		Timestamp: time.Now(),
	}

	// Always save the user event to the STM cache
	if err := s.stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, userEvent); err != nil {
		log.Printf("ERROR: Failed to add user event to STM cache: %v", err)
	}

	// Conditionally trigger the worker only for user messages
	if userEvent.Role == "user" && userEvent.Type == models.STMEventTypeMessage {
		taskPayload := models.CognitiveChainCheckTask{
			ID: uuid.New().String(),
			Type: memory.TaskTypeCognitiveChainCheck,
			TenantID: tenantID,
			UserID: userID,
			AgentID: agentID,
			Timestamp: time.Now(),
		}

		payloadJSON, err := json.Marshal(taskPayload)
		if err != nil {
			log.Printf("ERROR: Failed to marshal cognitive check task payload: %v", err)
		} else {
			envelope := memory.TaskEnvelope{
				ID: taskPayload.ID,
				Type: taskPayload.Type,
				Payload: payloadJSON,
				EnqueuedAt: time.Now(),
			}

			envelopeJSON, err := json.Marshal(envelope)
			if err != nil {
				log.Printf("ERROR: Failed to marshal task envelope: %v", err)
			} else {
				scopedQueueName := memory.GenerateScopedQueueName(tenantID, userID, agentID)
				if err := s.redisClient.LPush(scopedQueueName, string(envelopeJSON)); err != nil {
					log.Printf("ERROR: Failed to enqueue cognitive check task: %v", err)
				} else {
					if err := s.redisClient.LPush(memory.GlobalWorkQueueName, scopedQueueName); err != nil {
						log.Printf("ERROR: Failed to push to global work queue: %v", err)
					}
				}
			}
		}
	}

	// Create and process the agent event if it exists
	if req.AgentResponse != "" {
		agentEvent := memory.STMEvent{
			Role: "agent",
			Type: models.STMEventTypeMessage,
			Content: req.AgentResponse,
			Timestamp: time.Now(),
		}
		if err := s.stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, agentEvent); err != nil {
			log.Printf("ERROR: Failed to add agent event to STM cache: %v", err)
		}
	}

	log.Printf("INFO: Interaction processed successfully for SessionID: %s", sessionID)

	return &gen.StoreInteractionResponse{
		Success: true,
		InteractionId: "", // The concept of a single interaction ID is now obsolete
	}, nil
}

// getIDsFromSession retrieves tenant, user, and agent IDs from a session ID.
func (s *MemoryServer) getIDsFromSession(ctx context.Context, sessionID string) (string, string, string, error) {
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("cognitive_chains")
	var chain models.CognitiveChain
	err := collection.FindOne(ctx, bson.M{"chainId": sessionID}).Decode(&chain)
	if err != nil {
		return "", "", "", err
	}
	return chain.TenantID, chain.UserID, chain.AgentID, nil
}

// GetContext retrieves the recent conversation context from the STM cache.
func (s *MemoryServer) GetContext(ctx context.Context, req *gen.GetContextRequest) (*gen.GetContextResponse, error) {
	sessionID := req.SessionId
	tenantID, userID, agentID, err := s.getIDsFromSessionFunc(s, ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found or invalid: %v", err)
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}

	// Retrieve STM events from cache
	stmCache := memory.NewSTMCache(s.redisClient)
	events, err := stmCache.GetSTMContext(ctx, tenantID, userID, agentID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to retrieve STM context: %v", err)
	}

	// STM events are stored individually (user, agent, user, agent...) via LPUSH (newest first).
	// We pair them into ConversationTurns: each user message + following agent response = 1 turn.
	// Events are in reverse chronological order (newest first), so we reverse to get chronological.
	var turns []*gen.ConversationTurn
	// Walk events in reverse (oldest first) and pair user+agent
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Role == "user" {
			turn := &gen.ConversationTurn{
				UserMessage: event.Content,
			}
			// Look for a following agent response
			if i-1 >= 0 && events[i-1].Role == "agent" {
				turn.AgentResponse = events[i-1].Content
				i-- // Skip the agent event
			}
			turns = append(turns, turn)
		}
	}

	// Apply limit
	if len(turns) > limit {
		// Return most recent turns
		turns = turns[len(turns)-limit:]
	}

	log.Printf("INFO: GetContext returned %d turns for session %s", len(turns), sessionID)

	return &gen.GetContextResponse{
		RecentTurns: turns,
	}, nil
}

// CreateSession creates a new memory session by inserting a cognitive chain record.
func (s *MemoryServer) CreateSession(ctx context.Context, req *gen.CreateSessionRequest) (*gen.CreateSessionResponse, error) {
	tenantID := req.TenantId
	userID := req.UserId
	agentID := req.AgentId

	if tenantID == "" || userID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "tenant_id and user_id are required")
	}
	if agentID == "" {
		agentID = "default"
	}

	sessionID := uuid.New().String()
	now := time.Now()

	// Create a cognitive chain record that StoreInteraction can look up
	chain := models.CognitiveChain{
		TenantID:    tenantID,
		UserID:      userID,
		AgentID:     agentID,
		ChainID:     sessionID,
		Topic:       "",
		Summary:     "",
		StartedAt:   now,
		LastEventAt: now,
		EventCount:  0,
		Status:      "active",
	}

	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("cognitive_chains")
	_, err := collection.InsertOne(ctx, chain)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create session: %v", err)
	}

	log.Printf("INFO: Created session %s for tenant=%s user=%s agent=%s", sessionID, tenantID, userID, agentID)

	return &gen.CreateSessionResponse{
		SessionId: sessionID,
	}, nil
}

// SearchMemory performs semantic search across MTM cognitive chains.
func (s *MemoryServer) SearchMemory(ctx context.Context, req *gen.SearchMemoryRequest) (*gen.SearchMemoryResponse, error) {
	sessionID := req.SessionId
	tenantID, userID, agentID, err := s.getIDsFromSessionFunc(s, ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found or invalid: %v", err)
	}

	query := req.Query
	if query == "" {
		return nil, status.Errorf(codes.InvalidArgument, "query is required")
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 5
	}

	// Create embedding for the search query
	queryEmbedding, err := s.stmStore.CreateEmbedding(ctx, query)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create query embedding: %v", err)
	}

	// Search for similar chains using the STM store's vector search
	chains, err := s.stmStore.SearchSimilarChains(ctx, tenantID, userID, agentID, queryEmbedding.Vector, limit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to search similar chains: %v", err)
	}

	// Convert to SearchResult protos
	var results []*gen.SearchResult
	for _, chain := range chains {
		result := &gen.SearchResult{
			Content:    chain.Summary,
			SourceType: "cognitive_chain",
			SourceId:   chain.ChainID,
		}
		results = append(results, result)
	}

	log.Printf("INFO: SearchMemory returned %d results for query '%s' in session %s", len(results), query, sessionID)

	return &gen.SearchMemoryResponse{
		Results: results,
	}, nil
}
