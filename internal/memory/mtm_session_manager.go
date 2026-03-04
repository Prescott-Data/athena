package memory

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SessionManager handles intelligent chain grouping and merging
type SessionManager struct {
	db                 *mongo.Database
	stmStore           *STMStore
	continuityAnalyzer *ContinuityAnalyzer
	heatScorer         *HeatScorer
	config             SessionConfig
}

// SessionConfig holds configuration for session management
type SessionConfig struct {
	SimilarityThreshold float64       // Minimum similarity to merge chains
	MaxChainAge         time.Duration // Maximum age to consider for merging
	MaxChainsPerUser    int           // Maximum chains per user to avoid memory bloat
	MergeMinConfidence  float64       // Minimum confidence required for merging
	KeywordWeight       float64       // Weight for keyword similarity in merging decisions
	VectorSearchLimit   int           // Number of candidates to fetch from vector search
}

// ChainMergeCandidate represents a candidate for merging
type ChainMergeCandidate struct {
	Chain            *models.CognitiveChain
	SimilarityScore  float64
	ContinuityResult *ContinuityResult
	MergeConfidence  float64
	MergeReasoning   string
}

// MergeDecision represents the result of merge analysis
type MergeDecision struct {
	ShouldMerge     bool
	TargetChain     *models.CognitiveChain
	Confidence      float64
	Reasoning       string
	SimilarityScore float64
	ContinuityScore float64
}

// NewSessionManager creates a new session manager
func NewSessionManager(db *mongo.Database, stmStore *STMStore) *SessionManager {
	return &SessionManager{
		db:                 db,
		stmStore:           stmStore,
		continuityAnalyzer: NewContinuityAnalyzer(db, stmStore),
		heatScorer:         NewHeatScorer(db),
		config:             getDefaultSessionConfig(),
	}
}

// getDefaultSessionConfig returns default session management configuration
func getDefaultSessionConfig() SessionConfig {
	similarityThreshold := parseFloatEnv("SESSION_SIMILARITY_THRESHOLD", 0.6)
	maxAgeHours := time.Duration(parseIntEnv("SESSION_MAX_AGE_HOURS", 72)) * time.Hour
	maxChains := parseIntEnv("SESSION_MAX_CHAINS_PER_USER", 100)
	minConfidence := parseFloatEnv("SESSION_MERGE_MIN_CONFIDENCE", 0.7)
	keywordWeight := parseFloatEnv("SESSION_KEYWORD_WEIGHT", 0.3)
	vectorSearchLimit := parseIntEnv("SESSION_VECTOR_SEARCH_LIMIT", 5)

	return SessionConfig{
		SimilarityThreshold: similarityThreshold,
		MaxChainAge:         maxAgeHours,
		MaxChainsPerUser:    maxChains,
		MergeMinConfidence:  minConfidence,
		KeywordWeight:       keywordWeight,
		VectorSearchLimit:   vectorSearchLimit,
	}
}

// ProcessNewChain determines whether to merge a new chain or create it standalone
func (sm *SessionManager) ProcessNewChain(ctx context.Context, newChain *models.CognitiveChain, events []models.CognitiveEvent) (*models.CognitiveChain, error) {
	start := time.Now()

	log.Printf("INFO: Processing new chain for user %s: %s", newChain.UserID, newChain.ChainID)

	candidates, err := sm.findMergeCandidates(ctx, newChain)
	if err != nil {
		log.Printf("WARN: Failed to find merge candidates: %v", err)
		return sm.createStandaloneChain(ctx, newChain, events)
	}

	if len(candidates) == 0 {
		log.Printf("INFO: No merge candidates found, creating standalone chain")
		return sm.createStandaloneChain(ctx, newChain, events)
	}

	mergeDecision, err := sm.analyzeMergeDecision(ctx, newChain, candidates)
	if err != nil {
		log.Printf("WARN: Merge analysis failed: %v", err)
		return sm.createStandaloneChain(ctx, newChain, events)
	}

	duration := time.Since(start)

	if mergeDecision.ShouldMerge {
		log.Printf("INFO: Merging chain %s into %s (confidence: %.3f, similarity: %.3f) - Duration: %v",
			newChain.ChainID, mergeDecision.TargetChain.ChainID,
			mergeDecision.Confidence, mergeDecision.SimilarityScore, duration)

		return sm.mergeChains(ctx, mergeDecision.TargetChain, newChain, events)
	}

	log.Printf("INFO: Creating standalone chain %s (best similarity: %.3f below threshold) - Duration: %v",
		newChain.ChainID, mergeDecision.SimilarityScore, duration)

	return sm.createStandaloneChain(ctx, newChain, events)
}

// findRecentCandidates finds potential merge candidates based on recent activity.
func (sm *SessionManager) findRecentCandidates(ctx context.Context, newChain *models.CognitiveChain) ([]models.CognitiveChain, error) {
	collection := sm.db.Collection(CognitiveChainsCollection)

	cutoffTime := time.Now().Add(-sm.config.MaxChainAge)
	filter := bson.M{
		"userId":    newChain.UserID,
		"status":    "active",
		"startedAt": bson.M{"$gte": cutoffTime},
		"chainId":   bson.M{"$ne": newChain.ChainID},
	}

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent candidate chains: %w", err)
	}
	defer cursor.Close(ctx)

	var existingChains []models.CognitiveChain
	if err := cursor.All(ctx, &existingChains); err != nil {
		return nil, fmt.Errorf("failed to decode recent candidate chains: %w", err)
	}

	return existingChains, nil
}

// findVectorCandidates finds potential merge candidates using semantic vector search.
func (sm *SessionManager) findVectorCandidates(ctx context.Context, newChain *models.CognitiveChain) ([]models.CognitiveChain, error) {
	if sm.stmStore == nil || sm.stmStore.milvus == nil {
		log.Println("INFO: Milvus not configured, skipping vector search for candidates.")
		return nil, nil
	}

	// 1. Create embedding for the new chain's summary
	embedding, err := sm.stmStore.CreateEmbedding(ctx, newChain.Summary)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding for vector search: %w", err)
	}

	// 2. Search for similar chains using STMStore's SearchSimilarChains method
	// which handles both vector search and MongoDB retrieval
	similarChains, err := sm.stmStore.SearchSimilarChains(ctx, newChain.TenantID, newChain.UserID, newChain.AgentID, embedding.Vector, sm.config.VectorSearchLimit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to perform vector search for similar chains: %w", err)
	}

	if len(similarChains) == 0 {
		return nil, nil
	}

	// 3. Convert from pointer slice to value slice and filter to ensure active status
	vectorChains := make([]models.CognitiveChain, 0, len(similarChains))
	for _, chain := range similarChains {
		if chain != nil && chain.Status == "active" && chain.ChainID != newChain.ChainID {
			vectorChains = append(vectorChains, *chain)
		}
	}

	return vectorChains, nil
}

// findMergeCandidates finds existing chains that could potentially be merged with the new chain.
// It combines results from recent time-based search and semantic vector search.
func (sm *SessionManager) findMergeCandidates(ctx context.Context, newChain *models.CognitiveChain) ([]*ChainMergeCandidate, error) {
	// 1. Find candidates from recent activity
	recentChains, err := sm.findRecentCandidates(ctx, newChain)
	if err != nil {
		log.Printf("WARN: Failed to find recent merge candidates: %v", err)
		// Non-fatal, vector search can still proceed
	}

	// 2. Find candidates from vector search
	vectorChains, err := sm.findVectorCandidates(ctx, newChain)
	if err != nil {
		log.Printf("WARN: Failed to find vector merge candidates: %v", err)
		// Non-fatal, recent search might have results
	}

	// 3. Combine and deduplicate chains
	allChains := make(map[string]models.CognitiveChain)
	for _, chain := range recentChains {
		allChains[chain.ChainID] = chain
	}
	for _, chain := range vectorChains {
		allChains[chain.ChainID] = chain
	}

	if len(allChains) == 0 {
		return []*ChainMergeCandidate{}, nil
	}

	// 4. Analyze continuity and calculate confidence for each unique candidate
	candidates := make([]*ChainMergeCandidate, 0, len(allChains))
	continuityConfig := sm.continuityAnalyzer.GetDefaultConfig()

	for _, chain := range allChains {
		// Don't compare a chain with itself if it somehow got into the list
		if chain.ChainID == newChain.ChainID {
			continue
		}

		chainCopy := chain // Avoid pointer issues in loop

		continuityResult, err := sm.continuityAnalyzer.AnalyzeContinuity(ctx, &chainCopy, newChain, continuityConfig)
		if err != nil {
			log.Printf("WARN: Continuity analysis failed for chain %s: %v", chain.ChainID, err)
			continue
		}

		mergeConfidence := sm.calculateMergeConfidence(continuityResult, &chainCopy, newChain)

		// Looser confidence for initial candidacy, stricter check happens in analyzeMergeDecision
		if mergeConfidence >= sm.config.MergeMinConfidence*0.5 {
			candidate := &ChainMergeCandidate{
				Chain:            &chainCopy,
				SimilarityScore:  continuityResult.SemanticScore,
				ContinuityResult: continuityResult,
				MergeConfidence:  mergeConfidence,
				MergeReasoning:   continuityResult.Reasoning,
			}
			candidates = append(candidates, candidate)
		}
	}

	// 5. Sort candidates by merge confidence
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].MergeConfidence > candidates[j].MergeConfidence
	})

	log.Printf("DEBUG: Found %d unique merge candidates for chain %s", len(candidates), newChain.ChainID)
	return candidates, nil
}

// calculateMergeConfidence computes overall confidence for merging two chains
func (sm *SessionManager) calculateMergeConfidence(continuityResult *ContinuityResult, existingChain, newChain *models.CognitiveChain) float64 {
	confidence := continuityResult.Confidence

	if existingChain.ChainID == newChain.ChainID {
		confidence += 0.2
	}

	timeDiff := newChain.StartedAt.Sub(existingChain.StartedAt)
	if timeDiff < time.Hour {
		confidence += 0.1
	} else if timeDiff < 6*time.Hour {
		confidence += 0.05
	}

	sizeDiff := absInt(existingChain.EventCount - newChain.EventCount)
	if sizeDiff > 10 { // Increased threshold for event counts
		confidence -= 0.1
	}

	if confidence > 1.0 {
		confidence = 1.0
	} else if confidence < 0.0 {
		confidence = 0.0
	}

	return confidence
}

// analyzeMergeDecision determines the best merge action for the new chain
func (sm *SessionManager) analyzeMergeDecision(_ context.Context, _ *models.CognitiveChain, candidates []*ChainMergeCandidate) (*MergeDecision, error) {
	if len(candidates) == 0 {
		return &MergeDecision{ShouldMerge: false, Reasoning: "No candidates available"}, nil
	}

	bestCandidate := candidates[0]

	shouldMerge := bestCandidate.MergeConfidence >= sm.config.MergeMinConfidence &&
		bestCandidate.SimilarityScore >= sm.config.SimilarityThreshold

	var reasoning string
	if shouldMerge {
		reasoning = fmt.Sprintf("High confidence merge: %s (confidence: %.3f, similarity: %.3f)",
			bestCandidate.MergeReasoning, bestCandidate.MergeConfidence, bestCandidate.SimilarityScore)
	} else {
		if bestCandidate.MergeConfidence < sm.config.MergeMinConfidence {
			reasoning = fmt.Sprintf("Confidence too low: %.3f < %.3f",
				bestCandidate.MergeConfidence, sm.config.MergeMinConfidence)
		} else {
			reasoning = fmt.Sprintf("Similarity too low: %.3f < %.3f",
				bestCandidate.SimilarityScore, sm.config.SimilarityThreshold)
		}
	}

	continuityScore := 0.0
	if bestCandidate.ContinuityResult != nil {
		continuityScore = bestCandidate.ContinuityResult.Confidence
	}

	return &MergeDecision{
		ShouldMerge:     shouldMerge,
		TargetChain:     bestCandidate.Chain,
		Confidence:      bestCandidate.MergeConfidence,
		Reasoning:       reasoning,
		SimilarityScore: bestCandidate.SimilarityScore,
		ContinuityScore: continuityScore,
	}, nil
}

// mergeChains merges the new chain into an existing target chain
func (sm *SessionManager) mergeChains(ctx context.Context, targetChain *models.CognitiveChain, newChain *models.CognitiveChain, events []models.CognitiveEvent) (*models.CognitiveChain, error) {
	chainsCollection := sm.db.Collection(CognitiveChainsCollection)
	eventsCollection := sm.db.Collection(CognitiveEventsCollection)
	now := time.Now()

	// 1. Update events from the new chain to point to the target chain
	eventIDs := make([]interface{}, len(events))
	for i, e := range events {
		eventIDs[i] = e.ID
	}

	_, err := eventsCollection.UpdateMany(ctx,
		bson.M{"chainId": newChain.ChainID},
		bson.M{"$set": bson.M{"chainId": targetChain.ChainID, "updatedAt": now}},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to re-assign events to target chain: %w", err)
	}

	// 2. Merge entities from both chains
	mergedEntities := sm.mergeEntities(targetChain.Entities, newChain.Entities)

	// 3. Update the target chain
	combinedSummary := sm.combineSummaries(targetChain.Summary, newChain.Summary)

	// Inherit the topic from the new chain if the target chain has none
	mergedTopic := targetChain.Topic
	if mergedTopic == "" && newChain.Topic != "" {
		mergedTopic = newChain.Topic
	}

	update := bson.M{
		"$set": bson.M{
			"summary":     combinedSummary,
			"topic":       mergedTopic,
			"eventCount":  targetChain.EventCount + newChain.EventCount,
			"startedAt":   targetChain.StartedAt, // typically the older one
			"lastEventAt": newChain.LastEventAt,  // typically the newer one
			"entities":    mergedEntities,
			"updatedAt":   now,
		},
	}

	filter := bson.M{"chainId": targetChain.ChainID}
	_, err = chainsCollection.UpdateOne(ctx, filter, update)
	if err != nil {
		return nil, fmt.Errorf("failed to update target chain: %w", err)
	}

	// 3. Delete the old, now-merged chain document
	_, err = chainsCollection.DeleteOne(ctx, bson.M{"chainId": newChain.ChainID})
	if err != nil {
		log.Printf("WARN: Failed to delete merged chain document %s: %v", newChain.ChainID, err)
	}

	// 4. Fetch the updated chain
	var updatedChain models.CognitiveChain
	if err := chainsCollection.FindOne(ctx, filter).Decode(&updatedChain); err != nil {
		return nil, fmt.Errorf("failed to fetch updated chain: %w", err)
	}

	// 5. Recalculate heat score for the merged chain
	newHeatScore, heatFactors, err := sm.heatScorer.ComputeSegmentHeat(ctx, &updatedChain)
	if err != nil {
		log.Printf("WARN: Failed to recalculate heat score for merged chain: %v", err)
	} else {
		heatUpdate := bson.M{"$set": bson.M{"heatScore": newHeatScore, "heatFactors": heatFactors, "updatedAt": now}}
		chainsCollection.UpdateOne(ctx, filter, heatUpdate)
	}

	log.Printf("INFO: Successfully merged chain %s into %s (total events: %d)",
		newChain.ChainID, targetChain.ChainID, updatedChain.EventCount)

	return &updatedChain, nil
}

// createStandaloneChain creates a new chain when merging is not appropriate
func (sm *SessionManager) createStandaloneChain(ctx context.Context, chain *models.CognitiveChain, events []models.CognitiveEvent) (*models.CognitiveChain, error) {
	chainsCollection := sm.db.Collection(CognitiveChainsCollection)

	// Initialize chain with proper values
	chain.EventCount = len(events)
	chain.RecallStrength = 1.0
	chain.IntrinsicImportance = 0.5
	chain.LastAccessedAt = nil
	chain.HeatScore = 0.0
	chain.HeatFactors = nil
	chain.Status = "active"
	// Timestamps should be set before calling this function

	// Insert the new chain document
	_, err := chainsCollection.InsertOne(ctx, chain)
	if err != nil {
		return nil, fmt.Errorf("failed to insert standalone chain: %w", err)
	}

	// Calculate initial heat score
	heatScore, heatFactors, err := sm.heatScorer.ComputeSegmentHeat(ctx, chain)
	if err != nil {
		log.Printf("WARN: Failed to calculate initial heat score: %v", err)
	} else {
		// Update with heat score
		update := bson.M{"$set": bson.M{"heatScore": heatScore, "heatFactors": heatFactors, "updatedAt": time.Now()}}
		_, err = chainsCollection.UpdateOne(ctx, bson.M{"_id": chain.ID}, update)
		if err != nil {
			log.Printf("WARN: Failed to update chain with initial heat score: %v", err)
		}
		chain.HeatScore = heatScore
		chain.HeatFactors = heatFactors
	}

	log.Printf("INFO: Created standalone chain %s with %d events", chain.ChainID, len(events))
	return chain, nil
}

// combineSummaries creates a merged summary from two chain summaries
func (sm *SessionManager) combineSummaries(summary1, summary2 string) string {
	if summary1 == "" {
		return summary2
	}
	if summary2 == "" {
		return summary1
	}
	// In the future, this could use an LLM for a more coherent summary.
	return fmt.Sprintf("%s; %s", summary1, summary2)
}

// mergeEntities combines entities from two chains, removing duplicates
func (sm *SessionManager) mergeEntities(entities1, entities2 []string) []string {
	entitySet := make(map[string]struct{})

	// Add entities from both chains to a set to deduplicate
	for _, entity := range entities1 {
		if entity != "" {
			entitySet[entity] = struct{}{}
		}
	}
	for _, entity := range entities2 {
		if entity != "" {
			entitySet[entity] = struct{}{}
		}
	}

	// Convert back to slice
	merged := make([]string, 0, len(entitySet))
	for entity := range entitySet {
		merged = append(merged, entity)
	}

	return merged
}

// CleanupOldChains archives old chains to prevent memory bloat
func (sm *SessionManager) CleanupOldChains(ctx context.Context, userID string) error {
	collection := sm.db.Collection(CognitiveChainsCollection)

	filter := bson.M{"userId": userID, "status": "active"}
	count, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to count user chains: %w", err)
	}

	if count <= int64(sm.config.MaxChainsPerUser) {
		return nil // No cleanup needed
	}

	excessCount := count - int64(sm.config.MaxChainsPerUser)
	findOptions := options.Find().SetSort(bson.D{bson.E{Key: "startedAt", Value: 1}}).SetLimit(excessCount)
	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return fmt.Errorf("failed to find old chains: %w", err)
	}
	defer cursor.Close(ctx)

	var chainsToArchive []string
	for cursor.Next(ctx) {
		var chain models.CognitiveChain
		if err := cursor.Decode(&chain); err != nil {
			continue
		}
		chainsToArchive = append(chainsToArchive, chain.ChainID)
	}

	if len(chainsToArchive) > 0 {
		archiveFilter := bson.M{"chainId": bson.M{"$in": chainsToArchive}}
		archiveUpdate := bson.M{"$set": bson.M{"status": "archived", "updatedAt": time.Now()}}

		result, err := collection.UpdateMany(ctx, archiveFilter, archiveUpdate)
		if err != nil {
			return fmt.Errorf("failed to archive old chains: %w", err)
		}
		log.Printf("INFO: Archived %d old chains for user %s", result.ModifiedCount, userID)
	}

	return nil
}

// Helper function for absolute value of int
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
