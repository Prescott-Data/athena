package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/internal/cache"
	"github.com/Prescott-Data/athena/internal/config"
	"github.com/Prescott-Data/athena/internal/database"
	"github.com/Prescott-Data/athena/internal/memory"
	"github.com/Prescott-Data/athena/internal/models"
	"github.com/Prescott-Data/athena/internal/storage"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// stmCache defines the interface for the short-term memory cache.
// This allows for mocking in tests.
type stmCache interface {
	AddSTMEvent(ctx context.Context, tenantID, userID, agentID string, event memory.STMEvent) error
}

// MemoryServer represents the main Memory OS server
type MemoryServer struct {
	gen.UnimplementedMemoryServiceServer // Embed for forward compatibility
	config                               *config.Config
	stmStore                             *memory.STMStore
	stmCache                             stmCache
	taskQueue                            memory.TaskQueuer
	mongoClient                          *mongo.Client
	redisClient                          cache.Interface
	blobStore                            storage.BlobStore
	promoter                             *memory.Promoter
	ltmReader                            *memory.LTMReader
	// getIDsFromSessionFunc allows mocking the DB call in tests
	getIDsFromSessionFunc func(s *MemoryServer, ctx context.Context, sessionID string) (string, string, string, error)
}

// SetPromoter injects the background Promoter so it can be manually triggered by the API
func (s *MemoryServer) SetPromoter(p *memory.Promoter) {
	s.promoter = p
}

// SetLTMReader injects the LTMReader so SearchMemory can include graph results from ArangoDB
func (s *MemoryServer) SetLTMReader(r *memory.LTMReader) {
	s.ltmReader = r
}

// NewMemoryServer creates a new Memory OS server instance
func NewMemoryServer(cfg *config.Config) (*MemoryServer, error) {
	// Initialize database connections
	mongoClient, db, err := database.ConnectMongoDB(database.ConnectionConfig{
		URI:            cfg.Database.MongoDB.URI,
		DatabaseName:   cfg.Database.MongoDB.Database,
		ConnectTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	redisClient, err := cache.NewRedisClient()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	blobStore, err := storage.NewBlobStoreFromEnv()
	if err != nil {
		slog.Warn("Failed to initialize blob storage", slog.String("error", err.Error()))
	}

	// Initialize memory components
	stmStore := memory.NewSTMStore(db, redisClient, blobStore)
	stmCache := memory.NewSTMCache(redisClient)
	taskQueue := memory.NewTaskQueue(redisClient)

	server := &MemoryServer{
		config:      cfg,
		stmStore:    stmStore,
		stmCache:    stmCache,
		taskQueue:   taskQueue,
		mongoClient: mongoClient,
		redisClient: redisClient,
		blobStore:   blobStore,
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
			slog.Error("Error closing MongoDB connection", slog.String("error", err.Error()))
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

// stmEventToCognitiveEvent converts an STMEvent to a CognitiveEvent for MongoDB persistence.
func stmEventToCognitiveEvent(tenantID, userID, agentID, chainID string, event memory.STMEvent) *models.CognitiveEvent {
	metadata := make(map[string]interface{})
	for k, v := range event.Metadata {
		metadata[k] = v
	}
	return &models.CognitiveEvent{
		TenantID:     tenantID,
		UserID:       userID,
		AgentID:      agentID,
		ChainID:      chainID,
		Role:         event.Role,
		Content:      event.Content,
		BlobURI:      event.BlobURI,
		BlobMimeType: event.BlobMimeType,
		Status:       "in_stm",
		Metadata:     metadata,
		CreatedAt:    event.Timestamp,
	}
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
		Role:      "user",
		Type:      models.STMEventTypeMessage,
		Content:   req.UserMessage,
		Timestamp: time.Now(),
		ChainID:   sessionID,
	}

	// Always save the user event to the STM cache
	if err := s.stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, userEvent); err != nil {
		slog.Error("Failed to add user event to STM cache", slog.String("error", err.Error()))
	}

	// Persist user event to MongoDB immediately for durability (P5)
	cogEvent := stmEventToCognitiveEvent(tenantID, userID, agentID, sessionID, userEvent)
	if s.stmStore != nil {
		if _, err := s.stmStore.StoreCognitiveEvent(ctx, cogEvent); err != nil {
			slog.Warn("Failed to persist user event to MongoDB", slog.String("session_id", sessionID), slog.String("error", err.Error()))
		}
	}

	// Conditionally trigger the worker only for user messages
	if userEvent.Role == "user" && userEvent.Type == models.STMEventTypeMessage {
		taskPayload := models.CognitiveChainCheckTask{
			ID:        uuid.New().String(),
			Type:      memory.TaskTypeCognitiveChainCheck,
			TenantID:  tenantID,
			UserID:    userID,
			AgentID:   agentID,
			Timestamp: time.Now(),
		}

		payloadJSON, err := json.Marshal(taskPayload)
		if err != nil {
			slog.Error("Failed to marshal cognitive check task payload", slog.String("error", err.Error()))
		} else {
			envelope := memory.TaskEnvelope{
				ID:         taskPayload.ID,
				Type:       taskPayload.Type,
				Payload:    payloadJSON,
				EnqueuedAt: time.Now(),
			}

			envelopeJSON, err := json.Marshal(envelope)
			if err != nil {
				slog.Error("Failed to marshal task envelope", slog.String("error", err.Error()))
			} else {
				scopedQueueName := memory.GenerateScopedQueueName(tenantID, userID, agentID)
				if err := s.redisClient.LPush(scopedQueueName, string(envelopeJSON)); err != nil {
					slog.Error("Failed to enqueue cognitive check task", slog.String("error", err.Error()))
				} else {
					if err := s.redisClient.LPush(memory.GlobalWorkQueueName, scopedQueueName); err != nil {
						slog.Error("Failed to push to global work queue", slog.String("error", err.Error()))
					}
				}
			}
		}
	}

	// Create and process the agent event if it exists
	if req.AgentResponse != "" {
		agentEvent := memory.STMEvent{
			Role:      "agent",
			Type:      models.STMEventTypeMessage,
			Content:   req.AgentResponse,
			Timestamp: time.Now(),
			ChainID:   sessionID,
		}
		if err := s.stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, agentEvent); err != nil {
			slog.Error("Failed to add agent event to STM cache", slog.String("error", err.Error()))
		}

		// Persist agent event to MongoDB immediately for durability (P5)
		agentCogEvent := stmEventToCognitiveEvent(tenantID, userID, agentID, sessionID, agentEvent)
		if s.stmStore != nil {
			if _, err := s.stmStore.StoreCognitiveEvent(ctx, agentCogEvent); err != nil {
				slog.Warn("Failed to persist agent event to MongoDB", slog.String("session_id", sessionID), slog.String("error", err.Error()))
			}
		}
	}

	slog.Info("Interaction processed successfully", slog.String("session_id", sessionID))

	return &gen.StoreInteractionResponse{
		Success:       true,
		InteractionId: "", // The concept of a single interaction ID is now obsolete
	}, nil
}

// StoreEvent stores a single cognitive event with explicit type control.
// Services use this for automation logs (observation), agent scratch pads (thought),
// tool calls (action), and any event that isn't a simple user↔agent message pair.
func (s *MemoryServer) StoreEvent(ctx context.Context, req *gen.StoreEventRequest) (*gen.StoreEventResponse, error) {
	sessionID := req.SessionId
	tenantID, userID, agentID, err := s.getIDsFromSessionFunc(s, ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found or invalid: %v", err)
	}

	eventType := models.STMEventType(req.Type)
	switch eventType {
	case models.STMEventTypeMessage, models.STMEventTypeThought,
		models.STMEventTypeAction, models.STMEventTypeObservation:
	default:
		return nil, status.Errorf(codes.InvalidArgument,
			"invalid event type: %s (must be message, thought, action, or observation)", req.Type)
	}

	role := req.Role
	if role == "" {
		role = "system"
	}

	blobURI := ""
	blobMimeType := ""

	if len(req.Payload) > 0 {
		if s.blobStore == nil {
			return nil, status.Errorf(codes.FailedPrecondition, "blob storage is not configured but binary payload was provided")
		}
		key := fmt.Sprintf("%s/%s/%s_%d", tenantID, sessionID, time.Now().Format("20060102150405"), time.Now().UnixNano())
		uri, err := s.blobStore.Upload(ctx, key, bytes.NewReader(req.Payload), req.MimeType)
		if err != nil {
			memory.BlobStorageOps.WithLabelValues("upload", "configured", "error").Inc()
			slog.Error("Failed to upload payload to blob storage", slog.String("error", err.Error()))
			return nil, status.Errorf(codes.Internal, "failed to upload payload: %v", err)
		}
		memory.BlobStorageOps.WithLabelValues("upload", "configured", "success").Inc()
		memory.BlobPayloadBytes.WithLabelValues("configured").Observe(float64(len(req.Payload)))
		blobURI = uri
		blobMimeType = req.MimeType

		if req.Content == "" {
			req.Content = fmt.Sprintf("System Observation: Uploaded a %d byte payload (type: %s)", len(req.Payload), req.MimeType)
		}
	}

	event := memory.STMEvent{
		Role:         role,
		Type:         eventType,
		Content:      req.Content,
		BlobURI:      blobURI,
		BlobMimeType: blobMimeType,
		Timestamp:    time.Now(),
		Metadata:     req.Metadata,
	}

	if err := s.stmCache.AddSTMEvent(ctx, tenantID, userID, agentID, event); err != nil {
		slog.Error("Failed to add event to STM cache", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to store event: %v", err)
	}

	// Persist event to MongoDB immediately for durability (P5)
	cogEvent := stmEventToCognitiveEvent(tenantID, userID, agentID, sessionID, event)
	if _, err := s.stmStore.StoreCognitiveEvent(ctx, cogEvent); err != nil {
		slog.Warn("Failed to persist event to MongoDB", slog.String("session_id", sessionID), slog.String("error", err.Error()))
	}

	s.enqueueChainCheck(tenantID, userID, agentID)

	slog.Info("Event stored", slog.String("type", req.Type), slog.String("role", role), slog.String("session_id", sessionID))

	return &gen.StoreEventResponse{
		Success: true,
	}, nil
}

// enqueueChainCheck pushes a cognitive chain check task to the worker queue.
func (s *MemoryServer) enqueueChainCheck(tenantID, userID, agentID string) {
	taskPayload := models.CognitiveChainCheckTask{
		ID:        uuid.New().String(),
		Type:      memory.TaskTypeCognitiveChainCheck,
		TenantID:  tenantID,
		UserID:    userID,
		AgentID:   agentID,
		Timestamp: time.Now(),
	}
	payloadJSON, err := json.Marshal(taskPayload)
	if err != nil {
		log.Printf("ERROR: Failed to marshal chain check task: %v", err)
		return
	}
	envelope := memory.TaskEnvelope{
		ID:         taskPayload.ID,
		Type:       taskPayload.Type,
		Payload:    payloadJSON,
		EnqueuedAt: time.Now(),
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		log.Printf("ERROR: Failed to marshal task envelope: %v", err)
		return
	}
	scopedQueueName := memory.GenerateScopedQueueName(tenantID, userID, agentID)
	if err := s.redisClient.LPush(scopedQueueName, string(envelopeJSON)); err != nil {
		log.Printf("ERROR: Failed to enqueue chain check: %v", err)
		return
	}
	if err := s.redisClient.LPush(memory.GlobalWorkQueueName, scopedQueueName); err != nil {
		log.Printf("ERROR: Failed to push to global work queue: %v", err)
	}
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

	// Return ALL STM events in chronological order (oldest first).
	// This includes messages, thoughts, actions, and observations.
	var stmEvents []*gen.STMEvent
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		stmEvents = append(stmEvents, &gen.STMEvent{
			Role:      e.Role,
			Type:      string(e.Type),
			Content:   e.Content,
			Timestamp: timestamppb.New(e.Timestamp),
			Metadata:  e.Metadata,
		})
	}

	// Apply limit — keep most recent events
	if len(stmEvents) > limit {
		stmEvents = stmEvents[len(stmEvents)-limit:]
	}

	// Retrieve MTM cognitive chains.
	// Phase 2 (per-message): query provided — semantic vector search ranked by similarity.
	// Phase 1 (session init): no query — recency sort from MongoDB.
	var pages []*gen.DialoguePage
	if req.Query != "" {
		queryEmbedding, err := s.stmStore.CreateEmbedding(ctx, req.Query)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create query embedding: %v", err)
		}
		chains, err := s.stmStore.SearchSimilarChains(ctx, tenantID, userID, agentID, queryEmbedding.Vector, limit, nil)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to search similar chains: %v", err)
		}
		for _, chain := range chains {
			if chain.Summary == "" {
				continue // skip session placeholder chains
			}
			pages = append(pages, &gen.DialoguePage{
				Id:        chain.ChainID,
				SessionId: chain.ChainID,
				Topic:     chain.Topic,
				Summary:   chain.Summary,
				Timestamp: timestamppb.New(chain.LastEventAt),
			})
		}
	} else {
		pages = s.getMTMByRecency(ctx, tenantID, userID, limit)
	}

	log.Printf("INFO: GetContext returned %d STM events + %d MTM chains for session %s (query=%q)",
		len(stmEvents), len(pages), sessionID, req.Query)

	return &gen.GetContextResponse{
		StmEvents:     stmEvents,
		RelevantPages: pages,
		Ltpm:          &gen.LTPMContext{Status: "not_implemented"},
	}, nil
}

// getMTMByRecency retrieves active MTM chains sorted by most recent activity.
// Chains with an empty summary (session placeholder chains) are excluded.
func (s *MemoryServer) getMTMByRecency(ctx context.Context, tenantID, userID string, limit int) []*gen.DialoguePage {
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("cognitive_chains")
	chainFilter := bson.M{
		"userId":   userID,
		"tenantId": tenantID,
		"status":   "active",
		"summary":  bson.M{"$ne": ""},
	}
	chainOpts := options.Find().SetSort(bson.D{{Key: "lastEventAt", Value: -1}}).SetLimit(int64(limit))

	var pages []*gen.DialoguePage
	cursor, err := collection.Find(ctx, chainFilter, chainOpts)
	if err != nil {
		log.Printf("WARN: getMTMByRecency query failed: %v", err)
		return pages
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var chain models.CognitiveChain
		if err := cursor.Decode(&chain); err != nil {
			continue
		}
		pages = append(pages, &gen.DialoguePage{
			Id:        chain.ChainID,
			SessionId: chain.ChainID,
			Topic:     chain.Topic,
			Summary:   chain.Summary,
			Timestamp: timestamppb.New(chain.LastEventAt),
		})
	}
	return pages
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
		Metadata:    req.Metadata,
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
	tenantID, userID, _, err := s.getIDsFromSessionFunc(s, ctx, sessionID)
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

	// Search for similar chains using the STM store's vector search.
	// Pass empty agentID so Milvus searches across all agents for this user (user-scoped, not session-scoped).
	chains, err := s.stmStore.SearchSimilarChains(ctx, tenantID, userID, "", queryEmbedding.Vector, limit, req.Filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to search similar chains: %v", err)
	}

	// Convert to SearchResult protos
	var results []*gen.SearchResult
	for _, chain := range chains {
		result := &gen.SearchResult{
			Content:         chain.Summary,
			SimilarityScore: float64(chain.SimilarityScore),
			SourceType:      "cognitive_chain",
			SourceId:        chain.ChainID,
			Timestamp:       timestamppb.New(chain.LastEventAt),
		}
		results = append(results, result)
	}

	// Augment with LTM graph results if the reader is available
	if s.ltmReader != nil {
		ltmExtraction, ltmErr := s.ltmReader.SearchByQuery(ctx, tenantID, query)
		if ltmErr != nil {
			slog.Warn("LTM SearchByQuery failed, returning MTM results only", slog.String("error", ltmErr.Error()))
		} else {
			for _, node := range ltmExtraction.Nodes {
				results = append(results, &gen.SearchResult{
					Content:    fmt.Sprintf("%s [%s]", node.Name, node.Label),
					SourceType: "ltm_node",
					SourceId:   node.ID,
				})
			}
		}
	}

	log.Printf("INFO: SearchMemory returned %d results for query '%s' in session %s", len(results), query, sessionID)

	return &gen.SearchMemoryResponse{
		Results: results,
	}, nil
}

// TriggerGraphAnalytics manually kicks off the background graph analytics jobs (Community Detection and Bridge Entities)
func (s *MemoryServer) TriggerGraphAnalytics(ctx context.Context, req *gen.TriggerGraphAnalyticsRequest) (*gen.TriggerGraphAnalyticsResponse, error) {
	log.Printf("INFO: Received request to trigger graph analytics")

	if s.promoter == nil {
		return nil, status.Errorf(codes.Internal, "graph analytics engine is not initialized or available (promoter is nil)")
	}

	// Because this can be a long-running job, we execute it in a separate goroutine
	// and immediately return success to the caller to avoid gRPC timeouts.
	go func() {
		// Use a detached background context since the gRPC request context will be cancelled
		// as soon as we return the response.
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		err := s.promoter.TriggerAnalytics(bgCtx)
		if err != nil {
			log.Printf("ERROR: Graph analytics job failed: %v", err)
		} else {
			log.Printf("INFO: Graph analytics job completed successfully")
		}
	}()

	return &gen.TriggerGraphAnalyticsResponse{
		Success: true,
		Message: "Graph analytics job (Community Detection and Bridge Entities) has been triggered in the background.",
	}, nil
}
