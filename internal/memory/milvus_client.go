package memory

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/Prescott-Data/athena/internal/models"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

const (
	// STMCollectionName is the collection name for STM embeddings
	STMCollectionName = "stm_embeddings"
	// SegmentCollectionName is the collection name for Segment embeddings
	SegmentCollectionName = "segment_embeddings"
	// VectorFieldName is the vector field name
	VectorFieldName = "embedding_vector"
	// PageIDFieldName is the page ID field name
	PageIDFieldName = "page_id"
	// SegmentIDFieldName is the segment ID field name
	SegmentIDFieldName = "segment_id"
	// TimestampFieldName is the timestamp field name
	TimestampFieldName = "created_at"
	// TenantIDFieldName is the tenant ID field name
	TenantIDFieldName = "tenant_id"
	// UserIDFieldName is the user ID field name
	UserIDFieldName = "user_id"
	// AgentIDFieldName is the agent ID field name
	AgentIDFieldName = "agent_id"
	// DefaultVectorDimension is the default vector dimension for embedding models
	DefaultVectorDimension = 1536 // Azure OpenAI text-embedding-ada-002 output dimension
)

// VectorDimension returns the configured vector dimension from env or default.
// Supports text-embedding-ada-002 (1536) and text-embedding-3-large (3072).
var VectorDimension = getVectorDimension()

func getVectorDimension() int {
	return parseIntEnv("MILVUS_VECTOR_DIMENSION", DefaultVectorDimension)
}

// MilvusClientInterface defines the interface for Milvus client operations
type MilvusClientInterface interface {
	InsertEmbedding(ctx context.Context, tenantID, userID, agentID, pageID string, embedding *models.EmbeddingData) error
	InsertSegmentEmbedding(ctx context.Context, tenantID, userID, agentID, segmentID string, embedding *models.EmbeddingData) error
	GetEmbeddingByPageID(ctx context.Context, tenantID, userID, agentID, pageID string) (*models.EmbeddingData, error)
	SearchSimilarEmbeddings(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]string, []float32, error)
	SearchSimilarSegments(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]string, []float32, error)
	DeleteSegmentEmbedding(ctx context.Context, tenantID, userID, agentID, segmentID string) error
	Close() error
}

// MilvusClient wraps the Milvus SDK for STM operations
type MilvusClient struct {
	client client.Client
}

// Ensure MilvusClient implements MilvusClientInterface
var _ MilvusClientInterface = (*MilvusClient)(nil)

// NewMilvusClient creates a new Milvus client connection
func NewMilvusClient(host, port string) (*MilvusClient, error) {
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "19530"
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		return nil, fmt.Errorf("invalid port number: %s", port)
	}

	milvusClient, err := client.NewGrpcClient(context.Background(), fmt.Sprintf("%s:%d", host, portInt))
	if err != nil {
		return nil, fmt.Errorf("failed to create Milvus client: %w", err)
	}

	mc := &MilvusClient{
		client: milvusClient,
	}

	// Initialize collections
	if err := mc.initializeCollection(context.Background()); err != nil {
		log.Printf("WARN: Failed to initialize Milvus collection: %v", err)
	}
	if err := mc.initializeSegmentCollection(context.Background()); err != nil {
		log.Printf("WARN: Failed to initialize Segment Milvus collection: %v", err)
	}

	return mc, nil
}

// initializeCollection creates the STM collection if it doesn't exist
func (mc *MilvusClient) initializeCollection(ctx context.Context) error {
	// Check if collection exists
	exists, err := mc.client.HasCollection(ctx, STMCollectionName)
	if err != nil {
		return fmt.Errorf("failed to check collection existence: %w", err)
	}

	if exists {
		// Check if existing collection has correct dimensions
		collection, err := mc.client.DescribeCollection(ctx, STMCollectionName)
		if err != nil {
			return fmt.Errorf("failed to describe existing collection: %w", err)
		}

		// Find the vector field and check its dimension
		for _, field := range collection.Schema.Fields {
			if field.Name == VectorFieldName && field.DataType == entity.FieldTypeFloatVector {
				if dimParam, exists := field.TypeParams[entity.TypeParamDim]; exists {
					existingDim := dimParam
					expectedDim := fmt.Sprintf("%d", VectorDimension)
					if existingDim != expectedDim {
						log.Printf("WARN: Collection '%s' has dimension %s but expected %s, dropping and recreating",
							STMCollectionName, existingDim, expectedDim)
						// Drop existing collection
						if err := mc.client.DropCollection(ctx, STMCollectionName); err != nil {
							return fmt.Errorf("failed to drop existing collection: %w", err)
						}
						log.Printf("INFO: Dropped collection '%s' with incorrect dimensions", STMCollectionName)
						exists = false
						break
					}
				}
			}
		}

		if exists {
			// Collections are NOT auto-loaded: after any Milvus restart an
			// existing collection sits unloaded and every search fails with
			// "collection not loaded". Load idempotently on startup — but bounded
			// and non-fatal: a sick Milvus must degrade search, never hang boot.
			loadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := mc.client.LoadCollection(loadCtx, STMCollectionName, true); err != nil {
				log.Printf("WARN: failed to request load of collection '%s' (search may fail until loaded): %v", STMCollectionName, err)
			}
			log.Printf("INFO: Milvus collection '%s' already exists with correct dimensions", STMCollectionName)
			return nil
		}
	}

	// Create collection schema
	schema := &entity.Schema{
		CollectionName: STMCollectionName,
		Description:    "STM embeddings for conversational memory",
		Fields: []*entity.Field{
			{
				Name:       PageIDFieldName,
				DataType:   entity.FieldTypeVarChar,
				PrimaryKey: true,
				TypeParams: map[string]string{
					entity.TypeParamMaxLength: "64",
				},
			},
			{
				Name:     TenantIDFieldName,
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					entity.TypeParamMaxLength: "64",
				},
			},
			{
				Name:     UserIDFieldName,
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					entity.TypeParamMaxLength: "64",
				},
			},
			{
				Name:     AgentIDFieldName,
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					entity.TypeParamMaxLength: "64",
				},
			},
			{
				Name:     VectorFieldName,
				DataType: entity.FieldTypeFloatVector,
				TypeParams: map[string]string{
					entity.TypeParamDim: fmt.Sprintf("%d", VectorDimension),
				},
			},
			{
				Name:     TimestampFieldName,
				DataType: entity.FieldTypeInt64,
			},
		},
	}

	// Create collection
	if err := mc.client.CreateCollection(ctx, schema, 1); err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	// Create index for vector field
	idx, err := entity.NewIndexHNSW(entity.L2, 16, 200)
	if err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}

	if err := mc.client.CreateIndex(ctx, STMCollectionName, VectorFieldName, idx, false); err != nil {
		return fmt.Errorf("failed to create vector index: %w", err)
	}

	// Load collection
	if err := mc.client.LoadCollection(ctx, STMCollectionName, false); err != nil {
		return fmt.Errorf("failed to load collection: %w", err)
	}

	log.Printf("INFO: Milvus collection '%s' created and loaded successfully", STMCollectionName)
	return nil
}

// initializeSegmentCollection creates the Segment collection if it doesn't exist
func (mc *MilvusClient) initializeSegmentCollection(ctx context.Context) error {
	exists, err := mc.client.HasCollection(ctx, SegmentCollectionName)
	if err != nil {
		return fmt.Errorf("failed to check segment collection existence: %w", err)
	}
	if exists {
		collection, err := mc.client.DescribeCollection(ctx, SegmentCollectionName)
		if err != nil {
			return fmt.Errorf("failed to describe segment collection: %w", err)
		}
		for _, field := range collection.Schema.Fields {
			if field.Name == VectorFieldName && field.DataType == entity.FieldTypeFloatVector {
				if dimParam, ok := field.TypeParams[entity.TypeParamDim]; ok {
					expected := fmt.Sprintf("%d", VectorDimension)
					if dimParam != expected {
						log.Printf("WARN: Segment collection '%s' has dimension %s but expected %s, dropping and recreating", SegmentCollectionName, dimParam, expected)
						if err := mc.client.DropCollection(ctx, SegmentCollectionName); err != nil {
							return fmt.Errorf("failed to drop segment collection: %w", err)
						}
						exists = false
						break
					}
				}
			}
		}
		if exists {
			// Same restart hazard as the STM collection: request an async load
			// (bounded, non-fatal) so search works after a Milvus restart.
			loadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := mc.client.LoadCollection(loadCtx, SegmentCollectionName, true); err != nil {
				log.Printf("WARN: failed to request load of segment collection '%s' (search may fail until loaded): %v", SegmentCollectionName, err)
			}
			log.Printf("INFO: Milvus collection '%s' already exists with correct dimensions", SegmentCollectionName)
			return nil
		}
	}

	schema := &entity.Schema{
		CollectionName: SegmentCollectionName,
		Description:    "Segment embeddings for MTM retrieval",
		Fields: []*entity.Field{
			{
				Name:       SegmentIDFieldName,
				DataType:   entity.FieldTypeVarChar,
				PrimaryKey: true,
				TypeParams: map[string]string{entity.TypeParamMaxLength: "64"},
			},
			{
				Name:     TenantIDFieldName,
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					entity.TypeParamMaxLength: "64",
				},
			},
			{
				Name:     UserIDFieldName,
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					entity.TypeParamMaxLength: "64",
				},
			},
			{
				Name:     AgentIDFieldName,
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					entity.TypeParamMaxLength: "64",
				},
			},
			{
				Name:       VectorFieldName,
				DataType:   entity.FieldTypeFloatVector,
				TypeParams: map[string]string{entity.TypeParamDim: fmt.Sprintf("%d", VectorDimension)},
			},
			{Name: TimestampFieldName, DataType: entity.FieldTypeInt64},
		},
	}
	if err := mc.client.CreateCollection(ctx, schema, 1); err != nil {
		return fmt.Errorf("failed to create segment collection: %w", err)
	}
	idx, err := entity.NewIndexHNSW(entity.L2, 16, 200)
	if err != nil {
		return fmt.Errorf("failed to create segment index: %w", err)
	}
	if err := mc.client.CreateIndex(ctx, SegmentCollectionName, VectorFieldName, idx, false); err != nil {
		return fmt.Errorf("failed to create segment vector index: %w", err)
	}
	if err := mc.client.LoadCollection(ctx, SegmentCollectionName, false); err != nil {
		return fmt.Errorf("failed to load segment collection: %w", err)
	}
	log.Printf("INFO: Milvus collection '%s' created and loaded successfully", SegmentCollectionName)
	return nil
}

// InsertEmbedding stores a vector embedding in Milvus with tenant/user/agent scope
func (mc *MilvusClient) InsertEmbedding(ctx context.Context, tenantID, userID, agentID, pageID string, embedding *models.EmbeddingData) error {
	if len(embedding.Vector) != VectorDimension {
		return fmt.Errorf("vector dimension mismatch: expected %d, got %d", VectorDimension, len(embedding.Vector))
	}

	// Convert float64 to float32 for Milvus
	vectorFloat32 := make([]float32, len(embedding.Vector))
	for i, v := range embedding.Vector {
		vectorFloat32[i] = float32(v)
	}

	// Prepare data with tenant/user/agent scope
	tenantIDColumn := entity.NewColumnVarChar(TenantIDFieldName, []string{tenantID})
	userIDColumn := entity.NewColumnVarChar(UserIDFieldName, []string{userID})
	agentIDColumn := entity.NewColumnVarChar(AgentIDFieldName, []string{agentID})
	pageIDColumn := entity.NewColumnVarChar(PageIDFieldName, []string{pageID})
	vectorColumn := entity.NewColumnFloatVector(VectorFieldName, VectorDimension, [][]float32{vectorFloat32})
	timestampColumn := entity.NewColumnInt64(TimestampFieldName, []int64{embedding.CreatedAt.Unix()})

	// Insert data
	_, err := mc.client.Insert(ctx, STMCollectionName, "", tenantIDColumn, userIDColumn, agentIDColumn, pageIDColumn, vectorColumn, timestampColumn)
	if err != nil {
		return fmt.Errorf("failed to insert embedding: %w", err)
	}

	// Flush to ensure data is persisted
	if err := mc.client.Flush(ctx, STMCollectionName, false); err != nil {
		log.Printf("WARN: Failed to flush after embedding insertion: %v", err)
	}

	return nil
}

// InsertSegmentEmbedding stores a segment embedding in Milvus with tenant/user/agent scope
func (mc *MilvusClient) InsertSegmentEmbedding(ctx context.Context, tenantID, userID, agentID, segmentID string, embedding *models.EmbeddingData) error {
	if len(embedding.Vector) != VectorDimension {
		return fmt.Errorf("vector dimension mismatch: expected %d, got %d", VectorDimension, len(embedding.Vector))
	}
	vectorFloat32 := make([]float32, len(embedding.Vector))
	for i, v := range embedding.Vector {
		vectorFloat32[i] = float32(v)
	}

	// Prepare data with tenant/user/agent scope
	tenantIDColumn := entity.NewColumnVarChar(TenantIDFieldName, []string{tenantID})
	userIDColumn := entity.NewColumnVarChar(UserIDFieldName, []string{userID})
	agentIDColumn := entity.NewColumnVarChar(AgentIDFieldName, []string{agentID})
	segIDColumn := entity.NewColumnVarChar(SegmentIDFieldName, []string{segmentID})
	vectorColumn := entity.NewColumnFloatVector(VectorFieldName, VectorDimension, [][]float32{vectorFloat32})
	tsColumn := entity.NewColumnInt64(TimestampFieldName, []int64{embedding.CreatedAt.Unix()})

	if _, err := mc.client.Insert(ctx, SegmentCollectionName, "", tenantIDColumn, userIDColumn, agentIDColumn, segIDColumn, vectorColumn, tsColumn); err != nil {
		return fmt.Errorf("failed to insert segment embedding: %w", err)
	}
	if err := mc.client.Flush(ctx, SegmentCollectionName, false); err != nil {
		log.Printf("WARN: Failed to flush after segment embedding insertion: %v", err)
	}
	return nil
}

// GetEmbeddingByPageID retrieves a stored embedding by page ID from Milvus
func (mc *MilvusClient) GetEmbeddingByPageID(ctx context.Context, tenantID, userID, agentID, pageID string) (*models.EmbeddingData, error) {
	// Query with tenant/user/agent scope
	expr := fmt.Sprintf(`%s == "%s" && %s == "%s" && %s == "%s" && %s == "%s"`,
		TenantIDFieldName, tenantID,
		UserIDFieldName, userID,
		AgentIDFieldName, agentID,
		PageIDFieldName, pageID)

	results, err := mc.client.Query(
		ctx,
		STMCollectionName,
		nil,  // no partition
		expr, // expression to filter by tenant/user/agent/pageID
		[]string{VectorFieldName, TimestampFieldName}, // output fields
	)

	if err != nil {
		return nil, fmt.Errorf("failed to query embedding by pageID: %w", err)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("embedding not found for pageID: %s", pageID)
	}

	// Extract vector
	vectorField := results.GetColumn(VectorFieldName)
	if vectorField == nil {
		return nil, fmt.Errorf("vector field not found in query result")
	}

	vectorCol, ok := vectorField.(*entity.ColumnFloatVector)
	if !ok {
		return nil, fmt.Errorf("unexpected vector field type")
	}

	vectorData := vectorCol.Data()
	if len(vectorData) == 0 {
		return nil, fmt.Errorf("no vector data found for pageID: %s", pageID)
	}

	vectorFloat32 := vectorData[0]

	// Convert float32 to float64
	vector := make([]float64, len(vectorFloat32))
	for i, v := range vectorFloat32 {
		vector[i] = float64(v)
	}

	// Extract timestamp
	var createdAt int64
	timestampField := results.GetColumn(TimestampFieldName)
	if timestampField != nil {
		if timestampCol, ok := timestampField.(*entity.ColumnInt64); ok {
			if ts, err := timestampCol.ValueByIdx(0); err == nil {
				createdAt = ts
			}
		}
	}

	return &models.EmbeddingData{
		ReferenceID: pageID,
		Vector:      vector,
		Dimensions:  len(vector),
		Model:       "retrieved_from_milvus",
		CreatedAt:   time.Unix(createdAt, 0),
	}, nil
}

// SearchSimilarEmbeddings finds similar STM vectors in Milvus with tenant/user/agent scope
func (mc *MilvusClient) SearchSimilarEmbeddings(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]string, []float32, error) {
	if len(queryVector) != VectorDimension {
		return nil, nil, fmt.Errorf("query vector dimension mismatch: expected %d, got %d", VectorDimension, len(queryVector))
	}

	// Convert to float32
	queryFloat32 := make([]float32, len(queryVector))
	for i, v := range queryVector {
		queryFloat32[i] = float32(v)
	}

	// Search parameters
	sp, _ := entity.NewIndexHNSWSearchParam(200)

	// Filter expression for tenant/user/agent scope
	expr := fmt.Sprintf(`%s == "%s" && %s == "%s" && %s == "%s"`,
		TenantIDFieldName, tenantID,
		UserIDFieldName, userID,
		AgentIDFieldName, agentID)

	// Perform search
	results, err := mc.client.Search(
		ctx,
		STMCollectionName,
		nil,                       // no partition
		expr,                      // tenant/user/agent filter
		[]string{PageIDFieldName}, // output fields
		[]entity.Vector{entity.FloatVector(queryFloat32)},
		VectorFieldName,
		entity.L2, // metric type
		limit,
		sp,
	)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to search embeddings: %w", err)
	}

	if len(results) == 0 {
		return []string{}, []float32{}, nil
	}

	// Extract results
	result := results[0]
	pageIDs := make([]string, 0, result.ResultCount)
	scores := make([]float32, 0, result.ResultCount)

	// Get page IDs
	pageIDField := result.Fields.GetColumn(PageIDFieldName)
	if pageIDField != nil {
		if pageIDCol, ok := pageIDField.(*entity.ColumnVarChar); ok {
			for i := 0; i < result.ResultCount; i++ {
				value, err := pageIDCol.ValueByIdx(i)
				if err == nil {
					pageIDs = append(pageIDs, value)
				}
			}
		}
	}

	// Get scores
	for i := 0; i < result.ResultCount; i++ {
		scores = append(scores, result.Scores[i])
	}

	return pageIDs, scores, nil
}

// SearchSimilarSegments finds similar Segment vectors in Milvus with tenant/user/agent scope
func (mc *MilvusClient) SearchSimilarSegments(ctx context.Context, tenantID, userID, agentID string, queryVector []float64, limit int) ([]string, []float32, error) {
	if len(queryVector) != VectorDimension {
		return nil, nil, fmt.Errorf("query vector dimension mismatch: expected %d, got %d", VectorDimension, len(queryVector))
	}
	q := make([]float32, len(queryVector))
	for i, v := range queryVector {
		q[i] = float32(v)
	}
	sp, _ := entity.NewIndexHNSWSearchParam(200)

	// Filter expression for tenant/user scope; agent scope is optional for user-wide search
	var expr string
	if agentID != "" {
		expr = fmt.Sprintf(`%s == "%s" && %s == "%s" && %s == "%s"`,
			TenantIDFieldName, tenantID,
			UserIDFieldName, userID,
			AgentIDFieldName, agentID)
	} else {
		expr = fmt.Sprintf(`%s == "%s" && %s == "%s"`,
			TenantIDFieldName, tenantID,
			UserIDFieldName, userID)
	}

	results, err := mc.client.Search(
		ctx,
		SegmentCollectionName,
		nil,
		expr, // tenant/user/agent filter
		[]string{SegmentIDFieldName},
		[]entity.Vector{entity.FloatVector(q)},
		VectorFieldName,
		entity.L2,
		limit,
		sp,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to search segment embeddings: %w", err)
	}
	if len(results) == 0 {
		return []string{}, []float32{}, nil
	}
	res := results[0]
	ids := make([]string, 0, res.ResultCount)
	scores := make([]float32, 0, res.ResultCount)
	idField := res.Fields.GetColumn(SegmentIDFieldName)
	if idField != nil {
		if col, ok := idField.(*entity.ColumnVarChar); ok {
			for i := 0; i < res.ResultCount; i++ {
				v, err := col.ValueByIdx(i)
				if err == nil {
					ids = append(ids, v)
				}
			}
		}
	}
	for i := 0; i < res.ResultCount; i++ {
		scores = append(scores, res.Scores[i])
	}
	return ids, scores, nil
}

// DeleteSegmentEmbedding removes a segment embedding from Milvus with tenant/user/agent scope
func (mc *MilvusClient) DeleteSegmentEmbedding(ctx context.Context, tenantID, userID, agentID, segmentID string) error {
	// Filter expression for tenant/user/agent scope and segment ID
	expr := fmt.Sprintf(`%s == "%s" && %s == "%s" && %s == "%s" && %s == "%s"`,
		TenantIDFieldName, tenantID,
		UserIDFieldName, userID,
		AgentIDFieldName, agentID,
		SegmentIDFieldName, segmentID)

	if err := mc.client.Delete(ctx, SegmentCollectionName, "", expr); err != nil {
		return fmt.Errorf("failed to delete segment embedding: %w", err)
	}
	return nil
}

// Close closes the Milvus client connection
func (mc *MilvusClient) Close() error {
	return mc.client.Close()
}