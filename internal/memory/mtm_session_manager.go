package memory

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SessionManager handles intelligent segment grouping and merging
type SessionManager struct {
	db                 *mongo.Database
	continuityAnalyzer *ContinuityAnalyzer
	heatScorer         *HeatScorer
	config             SessionConfig
}

// SessionConfig holds configuration for session management
type SessionConfig struct {
	SimilarityThreshold float64       // Minimum similarity to merge segments
	MaxSegmentAge       time.Duration // Maximum age to consider for merging
	MaxSegmentsPerUser  int           // Maximum segments per user to avoid memory bloat
	MergeMinConfidence  float64       // Minimum confidence required for merging
	KeywordWeight       float64       // Weight for keyword similarity in merging decisions
}

// SegmentMergeCandidate represents a candidate for merging
type SegmentMergeCandidate struct {
	Segment          *models.Segment
	SimilarityScore  float64
	ContinuityResult *ContinuityResult
	MergeConfidence  float64
	MergeReasoning   string
}

// MergeDecision represents the result of merge analysis
type MergeDecision struct {
	ShouldMerge     bool
	TargetSegment   *models.Segment
	Confidence      float64
	Reasoning       string
	SimilarityScore float64
	ContinuityScore float64
}

// NewSessionManager creates a new session manager
func NewSessionManager(db *mongo.Database, stmStore *STMStore) *SessionManager {
	return &SessionManager{
		db:                 db,
		continuityAnalyzer: NewContinuityAnalyzer(db, stmStore),
		heatScorer:         NewHeatScorer(db),
		config:             getDefaultSessionConfig(),
	}
}

// getDefaultSessionConfig returns default session management configuration
func getDefaultSessionConfig() SessionConfig {
	return SessionConfig{
		SimilarityThreshold: parseFloatEnv("SESSION_SIMILARITY_THRESHOLD", 0.6),
		MaxSegmentAge:       time.Duration(parseIntEnv("SESSION_MAX_AGE_HOURS", 72)) * time.Hour,
		MaxSegmentsPerUser:  parseIntEnv("SESSION_MAX_SEGMENTS_PER_USER", 100),
		MergeMinConfidence:  parseFloatEnv("SESSION_MERGE_MIN_CONFIDENCE", 0.7),
		KeywordWeight:       parseFloatEnv("SESSION_KEYWORD_WEIGHT", 0.3),
	}
}

// ProcessNewSegment determines whether to merge a new segment or create it standalone
func (sm *SessionManager) ProcessNewSegment(ctx context.Context, newSegment *models.Segment, pages []models.DialoguePage) (*models.Segment, error) {
	start := time.Now()

	log.Printf("INFO: Processing new segment for user %s: %s", newSegment.UserID, newSegment.SegmentID)

	// Find potential merge candidates
	candidates, err := sm.findMergeCandidates(ctx, newSegment)
	if err != nil {
		log.Printf("WARN: Failed to find merge candidates: %v", err)
		// Proceed with standalone segment creation
		return sm.createStandaloneSegment(ctx, newSegment, pages)
	}

	if len(candidates) == 0 {
		log.Printf("INFO: No merge candidates found, creating standalone segment")
		return sm.createStandaloneSegment(ctx, newSegment, pages)
	}

	// Analyze merge decision
	mergeDecision, err := sm.analyzeMergeDecision(ctx, newSegment, candidates)
	if err != nil {
		log.Printf("WARN: Merge analysis failed: %v", err)
		return sm.createStandaloneSegment(ctx, newSegment, pages)
	}

	duration := time.Since(start)

	if mergeDecision.ShouldMerge {
		log.Printf("INFO: Merging segment %s into %s (confidence: %.3f, similarity: %.3f) - Duration: %v",
			newSegment.SegmentID, mergeDecision.TargetSegment.SegmentID,
			mergeDecision.Confidence, mergeDecision.SimilarityScore, duration)

		return sm.mergeSegments(ctx, mergeDecision.TargetSegment, newSegment, pages)
	} else {
		log.Printf("INFO: Creating standalone segment %s (best similarity: %.3f below threshold) - Duration: %v",
			newSegment.SegmentID, mergeDecision.SimilarityScore, duration)

		return sm.createStandaloneSegment(ctx, newSegment, pages)
	}
}

// findMergeCandidates finds existing segments that could potentially be merged with the new segment
func (sm *SessionManager) findMergeCandidates(ctx context.Context, newSegment *models.Segment) ([]*SegmentMergeCandidate, error) {
	collection := sm.db.Collection("segments")

	// Find recent segments for the same user
	cutoffTime := time.Now().Add(-sm.config.MaxSegmentAge)
	filter := bson.M{
		"userId":    newSegment.UserID,
		"status":    "in_mtm",
		"createdAt": bson.M{"$gte": cutoffTime},
		"segmentId": bson.M{"$ne": newSegment.SegmentID}, // Exclude self
	}

	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query candidate segments: %w", err)
	}
	defer cursor.Close(ctx)

	var existingSegments []models.Segment
	if err := cursor.All(ctx, &existingSegments); err != nil {
		return nil, fmt.Errorf("failed to decode candidate segments: %w", err)
	}

	if len(existingSegments) == 0 {
		return []*SegmentMergeCandidate{}, nil
	}

	// Analyze each candidate
	candidates := make([]*SegmentMergeCandidate, 0, len(existingSegments))
	continuityConfig := sm.continuityAnalyzer.GetDefaultConfig()

	for _, segment := range existingSegments {
		segmentCopy := segment // Create a copy to avoid pointer issues

		// Check continuity
		continuityResult, err := sm.continuityAnalyzer.AnalyzeContinuity(ctx, &segmentCopy, newSegment, continuityConfig)
		if err != nil {
			log.Printf("WARN: Continuity analysis failed for segment %s: %v", segment.SegmentID, err)
			continue
		}

		// Calculate overall merge confidence
		mergeConfidence := sm.calculateMergeConfidence(continuityResult, &segmentCopy, newSegment)

		if mergeConfidence >= sm.config.MergeMinConfidence*0.5 { // Consider candidates with at least half the required confidence
			candidate := &SegmentMergeCandidate{
				Segment:          &segmentCopy,
				SimilarityScore:  continuityResult.SemanticScore,
				ContinuityResult: continuityResult,
				MergeConfidence:  mergeConfidence,
				MergeReasoning:   continuityResult.Reasoning,
			}
			candidates = append(candidates, candidate)
		}
	}

	// Sort by merge confidence (best candidates first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].MergeConfidence > candidates[j].MergeConfidence
	})

	log.Printf("DEBUG: Found %d merge candidates for segment %s", len(candidates), newSegment.SegmentID)
	return candidates, nil
}

// calculateMergeConfidence computes overall confidence for merging two segments
func (sm *SessionManager) calculateMergeConfidence(continuityResult *ContinuityResult, existingSegment, newSegment *models.Segment) float64 {
	// Base confidence from continuity analysis
	confidence := continuityResult.Confidence

	// Boost confidence for same chain ID
	if existingSegment.ChainID == newSegment.ChainID {
		confidence += 0.2
	}

	// Boost confidence for recent segments
	timeDiff := newSegment.CreatedAt.Sub(existingSegment.CreatedAt)
	if timeDiff < time.Hour {
		confidence += 0.1
	} else if timeDiff < 6*time.Hour {
		confidence += 0.05
	}

	// Penalty for very different interaction sizes
	sizeDiff := absInt(len(existingSegment.PageIDs) - len(newSegment.PageIDs))
	if sizeDiff > 5 {
		confidence -= 0.1
	}

	// Ensure confidence stays in valid range
	if confidence > 1.0 {
		confidence = 1.0
	} else if confidence < 0.0 {
		confidence = 0.0
	}

	return confidence
}

// analyzeMergeDecision determines the best merge action for the new segment
func (sm *SessionManager) analyzeMergeDecision(ctx context.Context, newSegment *models.Segment, candidates []*SegmentMergeCandidate) (*MergeDecision, error) {
	if len(candidates) == 0 {
		return &MergeDecision{
			ShouldMerge: false,
			Reasoning:   "No candidates available",
		}, nil
	}

	// Get the best candidate
	bestCandidate := candidates[0]

	// Check if it meets our merging criteria
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

	return &MergeDecision{
		ShouldMerge:     shouldMerge,
		TargetSegment:   bestCandidate.Segment,
		Confidence:      bestCandidate.MergeConfidence,
		Reasoning:       reasoning,
		SimilarityScore: bestCandidate.SimilarityScore,
		ContinuityScore: bestCandidate.ContinuityResult.Confidence,
	}, nil
}

// mergeSegments merges the new segment into an existing target segment
func (sm *SessionManager) mergeSegments(ctx context.Context, targetSegment *models.Segment, newSegment *models.Segment, pages []models.DialoguePage) (*models.Segment, error) {
	collection := sm.db.Collection("segments")
	now := time.Now()

	// Convert pages to ObjectIDs for storage
	pageIDs := make([]primitive.ObjectID, 0, len(pages))
	for _, page := range pages {
		pageIDs = append(pageIDs, page.ID)
	}

	// Merge page IDs
	mergedPageIDs := append(targetSegment.PageIDs, pageIDs...)

	// Create combined topic summary
	combinedSummary := sm.combineSummaries(targetSegment.TopicSummary, newSegment.TopicSummary)

	// Calculate new interaction size
	newInteractionSize := targetSegment.InteractionSize + len(pages)

	// Update the target segment
	update := bson.M{
		"$set": bson.M{
			"pageIds":         mergedPageIDs,
			"topicSummary":    combinedSummary,
			"interactionSize": newInteractionSize,
			"updatedAt":       now,
		},
	}

	filter := bson.M{"segmentId": targetSegment.SegmentID}
	result, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return nil, fmt.Errorf("failed to update target segment: %w", err)
	}

	if result.MatchedCount == 0 {
		return nil, fmt.Errorf("target segment not found for update")
	}

	// Fetch the updated segment
	var updatedSegment models.Segment
	if err := collection.FindOne(ctx, filter).Decode(&updatedSegment); err != nil {
		return nil, fmt.Errorf("failed to fetch updated segment: %w", err)
	}

	// Recalculate heat score for the merged segment
	newHeatScore, heatFactors, err := sm.heatScorer.ComputeSegmentHeat(ctx, &updatedSegment)
	if err != nil {
		log.Printf("WARN: Failed to recalculate heat score for merged segment: %v", err)
	} else {
		// Update heat score
		heatUpdate := bson.M{
			"$set": bson.M{
				"heatScore":   newHeatScore,
				"heatFactors": heatFactors,
				"updatedAt":   now,
			},
		}
		collection.UpdateOne(ctx, filter, heatUpdate)
	}

	log.Printf("INFO: Successfully merged segment %s into %s (total pages: %d)",
		newSegment.SegmentID, targetSegment.SegmentID, len(mergedPageIDs))

	return &updatedSegment, nil
}

// createStandaloneSegment creates a new segment when merging is not appropriate
func (sm *SessionManager) createStandaloneSegment(ctx context.Context, segment *models.Segment, pages []models.DialoguePage) (*models.Segment, error) {
	collection := sm.db.Collection("segments")

	// Convert pages to ObjectIDs
	pageIDs := make([]primitive.ObjectID, 0, len(pages))
	for _, page := range pages {
		pageIDs = append(pageIDs, page.ID)
	}

	// Initialize segment with proper values
	segment.PageIDs = pageIDs
	segment.InteractionSize = len(pages)
	segment.AccessCount = 0
	segment.LastAccessTime = nil
	segment.HeatScore = 0.0
	segment.HeatFactors = nil
	segment.Status = "in_mtm"
	segment.Scope = "individual"
	segment.CreatedAt = time.Now()
	segment.UpdatedAt = time.Now()

	// Insert the new segment
	result, err := collection.InsertOne(ctx, segment)
	if err != nil {
		return nil, fmt.Errorf("failed to insert standalone segment: %w", err)
	}

	segment.ID = result.InsertedID.(primitive.ObjectID)

	// Calculate initial heat score
	heatScore, heatFactors, err := sm.heatScorer.ComputeSegmentHeat(ctx, segment)
	if err != nil {
		log.Printf("WARN: Failed to calculate initial heat score: %v", err)
	} else {
		// Update with heat score
		update := bson.M{
			"$set": bson.M{
				"heatScore":   heatScore,
				"heatFactors": heatFactors,
				"updatedAt":   time.Now(),
			},
		}
		collection.UpdateOne(ctx, bson.M{"_id": segment.ID}, update)
		segment.HeatScore = heatScore
		segment.HeatFactors = heatFactors
	}

	log.Printf("INFO: Created standalone segment %s with %d pages", segment.SegmentID, len(pageIDs))
	return segment, nil
}

// combineSummaries creates a merged summary from two segment summaries
func (sm *SessionManager) combineSummaries(summary1, summary2 string) string {
	if summary1 == "" {
		return summary2
	}
	if summary2 == "" {
		return summary1
	}

	// Simple combination - in production you might want LLM-based summary merging
	return fmt.Sprintf("%s; %s", summary1, summary2)
}

// CleanupOldSegments removes old segments to prevent memory bloat
func (sm *SessionManager) CleanupOldSegments(ctx context.Context, userID string) error {
	collection := sm.db.Collection("segments")

	// Count current segments for user
	filter := bson.M{"userId": userID, "status": "in_mtm"}
	count, err := collection.CountDocuments(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to count user segments: %w", err)
	}

	if count <= int64(sm.config.MaxSegmentsPerUser) {
		return nil // No cleanup needed
	}

	// Find oldest segments to remove
	excessCount := count - int64(sm.config.MaxSegmentsPerUser)

	findOptions := options.Find().SetSort(bson.D{bson.E{Key: "createdAt", Value: 1}}).SetLimit(excessCount)
	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return fmt.Errorf("failed to find old segments: %w", err)
	}
	defer cursor.Close(ctx)

	var segmentsToRemove []string
	for cursor.Next(ctx) {
		var segment models.Segment
		if err := cursor.Decode(&segment); err != nil {
			continue
		}
		segmentsToRemove = append(segmentsToRemove, segment.SegmentID)
	}

	if len(segmentsToRemove) > 0 {
		// Archive old segments instead of deleting
		archiveFilter := bson.M{"segmentId": bson.M{"$in": segmentsToRemove}}
		archiveUpdate := bson.M{"$set": bson.M{"status": "archived", "updatedAt": time.Now()}}

		result, err := collection.UpdateMany(ctx, archiveFilter, archiveUpdate)
		if err != nil {
			return fmt.Errorf("failed to archive old segments: %w", err)
		}

		log.Printf("INFO: Archived %d old segments for user %s", result.ModifiedCount, userID)
	}

	return nil
}

// Helper function for absolute value of int (avoiding name collision)
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
