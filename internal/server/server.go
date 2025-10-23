package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	gen "bitbucket.org/dromos/memory-os/api/grpc/gen/api/grpc"
	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/config"
	"bitbucket.org/dromos/memory-os/internal/database"
	"bitbucket.org/dromos/memory-os/internal/memory"
	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// MemoryServer represents the main Memory OS server
type MemoryServer struct {
	gen.UnimplementedMemoryServiceServer // Embed for forward compatibility
	config                               *config.Config
	stmStore                             *memory.STMStore
	mongoClient                          *mongo.Client
	// Add other memory components as needed
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

	// Initialize STM Store (Milvus is optional and handled inside NewSTMStore)
	stmStore := memory.NewSTMStore(db, redisClient)

	server := &MemoryServer{
		config:      cfg,
		stmStore:    stmStore,
		mongoClient: mongoClient,
	}

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

// gRPC Method Implementations (Placeholders)

func (s *MemoryServer) CreateSession(ctx context.Context, req *gen.CreateSessionRequest) (*gen.CreateSessionResponse, error) {
	// Extract tenant and user ID from the request or context
	// For now, we'll use the request values directly.
	// In a real scenario, these should be validated against auth context.
	tenantID := req.TenantId
	userID := req.UserId
	agentID := req.AgentId

	if tenantID == "" || userID == "" || agentID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "tenant_id, user_id, and agent_id are required")
	}

	// Generate a new unique chain ID
	chainID := fmt.Sprintf("chain_%s_%d", userID, time.Now().UnixNano())

	// Create a new DialogueChain document
	newChain := &models.DialogueChain{
		TenantID:   tenantID,
		UserID:     userID,
		AgentID:    agentID,
		ChainID:    chainID,
		Topic:      "New Session",
		Summary:    "Session initiated via CreateSession API.",
		StartedAt:  time.Now(),
		LastTurnAt: time.Now(),
		TurnCount:  0, // Starts with 0 turns
		Status:     "active",
	}

	// Insert into MongoDB
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("dialogue_chains")
	_, err := collection.InsertOne(ctx, newChain)
	if err != nil {
		log.Printf("ERROR: Failed to create new dialogue chain: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to create session")
	}

	log.Printf("INFO: New session created - TenantID: %s, UserID: %s, SessionID: %s", tenantID, userID, chainID)

	// Return the new session ID
	return &gen.CreateSessionResponse{
		SessionId: chainID,
		// You can populate other fields like CreatedAt if they are added to the proto definition
	}, nil
}

func (s *MemoryServer) GetSession(ctx context.Context, req *gen.GetSessionRequest) (*gen.GetSessionResponse, error) {
	sessionID := req.SessionId
	if sessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	// Query the dialogue_chains collection for the session
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("dialogue_chains")
	var chain models.DialogueChain
	err := collection.FindOne(ctx, bson.M{"chainId": sessionID}).Decode(&chain)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, status.Errorf(codes.NotFound, "session not found")
		}
		log.Printf("ERROR: Failed to retrieve session %s: %v", sessionID, err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve session")
	}

	// Transform the database model to the gRPC response model
	session := &gen.Session{
		SessionId: chain.ChainID,
		UserId:    chain.UserID,
		CreatedAt: timestamppb.New(chain.StartedAt),
		UpdatedAt: timestamppb.New(chain.LastTurnAt),
		Metadata:  make(map[string]string), // Metadata is not stored on the chain model, returning empty for now.
	}

	return &gen.GetSessionResponse{Session: session}, nil
}

func (s *MemoryServer) DeleteSession(ctx context.Context, req *gen.DeleteSessionRequest) (*gen.DeleteSessionResponse, error) {
	sessionID := req.SessionId
	if sessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	// Update the session status to "archived" instead of deleting
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("dialogue_chains")
	result, err := collection.UpdateOne(
		ctx,
		bson.M{"chainId": sessionID},
		bson.M{"$set": bson.M{"status": "archived"}},
	)

	if err != nil {
		log.Printf("ERROR: Failed to update session %s to archived: %v", sessionID, err)
		return nil, status.Errorf(codes.Internal, "failed to delete session")
	}

	if result.MatchedCount == 0 {
		return nil, status.Errorf(codes.NotFound, "session not found")
	}

	log.Printf("INFO: Session archived - SessionID: %s", sessionID)

	return &gen.DeleteSessionResponse{Success: true}, nil
}

func (s *MemoryServer) StoreInteraction(ctx context.Context, req *gen.StoreInteractionRequest) (*gen.StoreInteractionResponse, error) {
	// Extract identifiers from the request.
	// In a real implementation, tenantId, userId, and agentId would be extracted
	// from the authenticated context rather than the request body for security.
	sessionID := req.SessionId
	userMessage := req.UserMessage
	agentResponse := req.AgentResponse

	// For now, we'll derive tenant, user, and agent IDs from a placeholder in the context
	// or a lookup based on the sessionID. Let's assume a helper function for this.
	// This part is crucial for multi-tenancy and security.
	tenantID, userID, agentID, err := s.getIDsFromSession(ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found or invalid: %v", err)
	}

	// 1. Determine the Dialogue Chain
	chainID, err := s.stmStore.DetermineDialogueChain(ctx, tenantID, userID, agentID, userMessage, agentResponse)
	if err != nil {
		log.Printf("ERROR: Failed to determine dialogue chain: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to process dialogue chain")
	}

	// 2. Create the Dialogue Page to be stored
	metadata := make(map[string]interface{})
	for k, v := range req.Metadata {
		metadata[k] = v
	}
	dialoguePage := &models.DialoguePage{
		TenantID:      tenantID,
		UserID:        userID,
		AgentID:       agentID,
		ChainID:       chainID,
		UserMessage:   userMessage,
		AgentResponse: agentResponse,
		Status:        "in_stm", // Mark as active in short-term memory
		Metadata:      metadata,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// 3. Store the Dialogue Page in MongoDB
	pageID, err := s.stmStore.StoreDialoguePageWithContext(ctx, dialoguePage, tenantID, userID, agentID)
	if err != nil {
		log.Printf("ERROR: Failed to store dialogue page: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to save interaction")
	}

	// 4. Create and Store the Embedding (asynchronously)
	go func() {
		embeddingCtx := context.Background()
		embedding, err := s.stmStore.CreateEmbedding(embeddingCtx, userMessage, agentResponse)
		if err != nil {
			log.Printf("ERROR: Failed to create embedding for page %s: %v", pageID.Hex(), err)
			return
		}

		err = s.stmStore.StoreEmbedding(embeddingCtx, tenantID, userID, agentID, pageID.Hex(), embedding)
		if err != nil {
			log.Printf("ERROR: Failed to store embedding for page %s: %v", pageID.Hex(), err)
		}
	}()

	log.Printf("INFO: Interaction stored successfully - SessionID: %s, PageID: %s", sessionID, pageID.Hex())

	return &gen.StoreInteractionResponse{
		Success:       true,
		InteractionId: pageID.Hex(),
	}, nil
}

// Helper function to retrieve tenant, user, and agent IDs from a session ID.
// This is a placeholder and should be replaced with a proper implementation.
func (s *MemoryServer) getIDsFromSession(ctx context.Context, sessionID string) (string, string, string, error) {
	collection := s.mongoClient.Database(s.config.Database.MongoDB.Database).Collection("dialogue_chains")
	var chain models.DialogueChain
	err := collection.FindOne(ctx, bson.M{"chainId": sessionID}).Decode(&chain)
	if err != nil {
		return "", "", "", err
	}
	return chain.TenantID, chain.UserID, chain.AgentID, nil
}


func (s *MemoryServer) GetContext(ctx context.Context, req *gen.GetContextRequest) (*gen.GetContextResponse, error) {
	sessionID := req.SessionId
	limit := int(req.Limit)

	if sessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	// Default limit if not provided or invalid
	if limit <= 0 {
		limit = 10
	}

	// Get tenancy info from the session
	_, userID, _, err := s.getIDsFromSession(ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found: %v", err)
	}

	// Retrieve recent dialogue pages from the STM store
	pages, err := s.stmStore.GetRecentConversationContext(ctx, userID, limit)
	if err != nil {
		log.Printf("ERROR: Failed to get recent conversation context: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve context")
	}

	// Transform the database models to the gRPC response models
	recentTurns := make([]*gen.ConversationTurn, 0, len(pages))
	for _, page := range pages {
		recentTurns = append(recentTurns, &gen.ConversationTurn{
			UserMessage:   page.UserMessage,
			AgentResponse: page.AgentResponse,
			Timestamp:     timestamppb.New(page.CreatedAt),
		})
	}

	log.Printf("INFO: Retrieved %d turns for session %s", len(recentTurns), sessionID)

	return &gen.GetContextResponse{
		RecentTurns: recentTurns,
	}, nil
}

func (s *MemoryServer) SearchMemory(ctx context.Context, req *gen.SearchMemoryRequest) (*gen.SearchMemoryResponse, error) {
	sessionID := req.SessionId
	query := req.Query
	limit := int(req.Limit)

	if sessionID == "" || query == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id and query are required")
	}

	if limit <= 0 {
		limit = 5 // Default limit
	}

	// Get tenancy info from the session
	tenantID, userID, agentID, err := s.getIDsFromSession(ctx, sessionID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "session not found: %v", err)
	}

	// Perform semantic search using the STM store
	pages, err := s.stmStore.RetrieveFromSTMStore(ctx, tenantID, userID, agentID, query, limit)
	if err != nil {
		log.Printf("ERROR: Failed to search memory for session %s: %v", sessionID, err)
		return nil, status.Errorf(codes.Internal, "failed to search memory")
	}

	// Transform the results into the gRPC response format
	results := make([]*gen.SearchResult, 0, len(pages))
	for _, page := range pages {
		results = append(results, &gen.SearchResult{
			Content:    fmt.Sprintf("User: %s\nAgent: %s", page.UserMessage, page.AgentResponse),
			SourceType: "dialogue_page",
			SourceId:   page.ID.Hex(),
			Timestamp:  timestamppb.New(page.CreatedAt),
			// SimilarityScore is not available in the current STMStore response, so it's omitted.
		})
	}

	log.Printf("INFO: Memory search completed for session %s, found %d results", sessionID, len(results))

	return &gen.SearchMemoryResponse{Results: results}, nil
}

func (s *MemoryServer) AnalyzeTopics(ctx context.Context, req *gen.AnalyzeTopicsRequest) (*gen.AnalyzeTopicsResponse, error) {
	sessionID := req.SessionId
	if sessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	// Retrieve and analyze topics from the STM store
	topicResult, err := s.stmStore.GetTopicsBySessionID(ctx, sessionID)
	if err != nil {
		log.Printf("ERROR: Failed to analyze topics for session %s: %v", sessionID, err)
		return nil, status.Errorf(codes.Internal, "failed to analyze topics")
	}

	// Transform the results into the gRPC response format
	responseTopics := make([]*gen.TopicSummary, 0, topicResult.TotalTopics)
	if topicResult.MainTopic != nil {
		responseTopics = append(responseTopics, &gen.TopicSummary{
			Topic:      topicResult.MainTopic.Theme,
			Confidence: topicResult.MainTopic.Confidence,
		})
	}
	for _, subTopic := range topicResult.SubTopics {
		responseTopics = append(responseTopics, &gen.TopicSummary{
			Topic:      subTopic.Theme,
			Confidence: subTopic.Confidence,
		})
	}

	log.Printf("INFO: Analyzed topics for session %s, found %d topics", sessionID, len(responseTopics))

	return &gen.AnalyzeTopicsResponse{Topics: responseTopics}, nil
}

func (s *MemoryServer) GetHeatMetrics(ctx context.Context, req *gen.GetHeatMetricsRequest) (*gen.GetHeatMetricsResponse, error) {
	sessionID := req.SessionId
	if sessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	// Retrieve heat metrics from the STM store
	metrics, err := s.stmStore.GetHeatMetricsBySessionID(ctx, sessionID)
	if err != nil {
		log.Printf("ERROR: Failed to get heat metrics for session %s: %v", sessionID, err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve heat metrics")
	}

	// Transform the database models to the gRPC response models
	responseMetrics := &gen.HeatMetrics{
		OverallHeat:       metrics.OverallHeat,
		TotalInteractions: int32(metrics.TotalInteractions),
		LastActivity:      timestamppb.New(metrics.LastActivity),
	}

	if metrics.Breakdown != nil {
		responseMetrics.Breakdown = &gen.HeatFactors{
			AccessFrequency:  metrics.Breakdown.AccessFrequency,
			InteractionDepth: metrics.Breakdown.InteractionDepth,
			RecencyScore:     metrics.Breakdown.RecencyScore,
			UserEngagement:   metrics.Breakdown.UserEngagement,
			TopicImportance:  metrics.Breakdown.TopicImportance,
		}
	}

	log.Printf("INFO: Retrieved heat metrics for session %s", sessionID)

	return &gen.GetHeatMetricsResponse{HeatMetrics: responseMetrics}, nil
}

func (s *MemoryServer) GetSegments(ctx context.Context, req *gen.GetSegmentsRequest) (*gen.GetSegmentsResponse, error) {
	sessionID := req.SessionId
	limit := int(req.Limit)

	if sessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	if limit <= 0 {
		limit = 50 // Default limit
	}

	// Retrieve segments from the STM store
	segments, err := s.stmStore.GetSegmentsBySessionID(ctx, sessionID, limit)
	if err != nil {
		log.Printf("ERROR: Failed to get segments for session %s: %v", sessionID, err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve segments")
	}

	// Transform the database models to the gRPC response models
	responseSegments := make([]*gen.Segment, 0, len(segments))
	for _, seg := range segments {
		genSeg := &gen.Segment{
			Id:           seg.SegmentID,
			SessionId:    seg.ChainID,
			Content:      seg.TopicSummary,
			QualityScore: seg.HeatScore,
			CreatedAt:    timestamppb.New(seg.CreatedAt),
		}
		// Note: Topics and HeatFactors are not yet implemented in this example.
		// They would be populated here if available in the models.Segment struct.
		responseSegments = append(responseSegments, genSeg)
	}

	log.Printf("INFO: Retrieved %d segments for session %s", len(responseSegments), sessionID)

	return &gen.GetSegmentsResponse{Segments: responseSegments}, nil
}

func (s *MemoryServer) HealthCheck(ctx context.Context, req *gen.HealthCheckRequest) (*gen.HealthCheckResponse, error) {
	// A simple health check implementation
	return &gen.HealthCheckResponse{Status: "healthy"}, nil
}
