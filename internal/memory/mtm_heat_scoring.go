package memory

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/dromos-org/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Heat scoring configuration with environment variable support
var (
	// Heat scoring weights (configurable via environment)
	HeatAlpha   = parseFloatEnv("HEAT_ALPHA", 1.0)   // Weight for access frequency
	HeatBeta    = parseFloatEnv("HEAT_BETA", 1.0)    // Weight for interaction depth
	HeatGamma   = parseFloatEnv("HEAT_GAMMA", 1.0)   // Weight for recency
	HeatDelta   = parseFloatEnv("HEAT_DELTA", 0.5)   // Weight for user engagement
	HeatEpsilon = parseFloatEnv("HEAT_EPSILON", 0.3) // Weight for topic importance

	// Recency decay parameters
	RecencyTauHours = parseFloatEnv("RECENCY_TAU_HOURS", 24.0)      // Time decay constant (hours)
	RecencyMaxAge   = parseFloatEnv("RECENCY_MAX_AGE_HOURS", 168.0) // Max age before score becomes negligible (1 week)

	// User engagement thresholds
	EngagementMinQuestionWords = parseIntEnv("ENGAGEMENT_MIN_QUESTION_WORDS", 5)
	EngagementMaxResponseChars = parseIntEnv("ENGAGEMENT_MAX_RESPONSE_CHARS", 1000)
)

// HeatScorer provides advanced heat scoring for segments
type HeatScorer struct {
	db     *mongo.Database
	config HeatScoringConfig
}

// HeatScoringConfig holds configuration for heat scoring algorithm
type HeatScoringConfig struct {
	Alpha   float64 // Access frequency weight
	Beta    float64 // Interaction depth weight
	Gamma   float64 // Recency weight
	Delta   float64 // User engagement weight
	Epsilon float64 // Topic importance weight

	RecencyTauHours float64 // Time decay constant
	RecencyMaxAge   float64 // Maximum age before negligible score
}

// NewHeatScorer creates a new heat scorer with default or environment configuration
func NewHeatScorer(db *mongo.Database) *HeatScorer {
	return &HeatScorer{
		db: db,
		config: HeatScoringConfig{
			Alpha:           HeatAlpha,
			Beta:            HeatBeta,
			Gamma:           HeatGamma,
			Delta:           HeatDelta,
			Epsilon:         HeatEpsilon,
			RecencyTauHours: RecencyTauHours,
			RecencyMaxAge:   RecencyMaxAge,
		},
	}
}

// ComputeSegmentHeat calculates enhanced heat score for a segment
func (h *HeatScorer) ComputeSegmentHeat(ctx context.Context, segment *models.Segment) (float64, *models.HeatFactors, error) {
	now := time.Now()

	// Factor 1: Access Frequency (N_visit)
	accessFrequency := h.calculateAccessFrequency(segment)

	// Factor 2: Interaction Depth (L_interaction)
	interactionDepth := h.calculateInteractionDepth(ctx, segment)

	// Factor 3: Recency Score (R_recency)
	recencyScore := h.calculateRecencyScore(segment, now)

	// Factor 4: User Engagement
	userEngagement := h.calculateUserEngagement(ctx, segment)

	// Factor 5: Topic Importance
	topicImportance := h.calculateTopicImportance(segment)

	// Weighted combination
	heatScore := h.config.Alpha*accessFrequency +
		h.config.Beta*interactionDepth +
		h.config.Gamma*recencyScore +
		h.config.Delta*userEngagement +
		h.config.Epsilon*topicImportance

	// Normalize using tanh to keep score in [0,1] range
	normalizedScore := math.Tanh(heatScore)

	factors := &models.HeatFactors{
		AccessFrequency:  accessFrequency,
		InteractionDepth: interactionDepth,
		RecencyScore:     recencyScore,
		UserEngagement:   userEngagement,
		TopicImportance:  topicImportance,
	}

	return normalizedScore, factors, nil
}

// calculateAccessFrequency computes normalized access frequency score
func (h *HeatScorer) calculateAccessFrequency(segment *models.Segment) float64 {
	if segment.AccessCount <= 0 {
		return 0.0
	}

	// Logarithmic scaling to prevent very high access counts from dominating
	return math.Log(float64(segment.AccessCount) + 1.0)
}

// calculateInteractionDepth computes interaction complexity score
func (h *HeatScorer) calculateInteractionDepth(ctx context.Context, segment *models.Segment) float64 {
	// Base score from number of pages
	pageCount := len(segment.PageIDs)
	if pageCount == 0 {
		return 0.0
	}

	// Enhanced score based on actual interaction complexity
	if segment.InteractionSize > 0 {
		// Use stored interaction size if available
		return math.Log(float64(segment.InteractionSize) + 1.0)
	}

	// Fallback: use page count as proxy for interaction depth
	return math.Log(float64(pageCount) + 1.0)
}

// calculateRecencyScore computes time-decay based recency score
func (h *HeatScorer) calculateRecencyScore(segment *models.Segment, currentTime time.Time) float64 {
	var referenceTime time.Time

	// Use last access time if available, otherwise creation time
	if segment.LastAccessTime != nil {
		referenceTime = *segment.LastAccessTime
	} else {
		referenceTime = segment.CreatedAt
	}

	// Calculate hours since reference time
	hoursSince := currentTime.Sub(referenceTime).Hours()

	// If too old, return minimal score
	if hoursSince > h.config.RecencyMaxAge {
		return 0.001
	}

	// Exponential decay: e^(-t/tau)
	return math.Exp(-hoursSince / h.config.RecencyTauHours)
}

// calculateUserEngagement estimates user engagement level
func (h *HeatScorer) calculateUserEngagement(ctx context.Context, segment *models.Segment) float64 {
	// This is a simplified implementation - in production you might analyze:
	// - Question complexity and length
	// - Response detail and helpfulness
	// - Follow-up questions
	// - User satisfaction indicators

	if len(segment.PageIDs) == 0 {
		return 0.0
	}

	// Base engagement from interaction count
	baseEngagement := math.Log(float64(len(segment.PageIDs))+1.0) / 3.0

	// Could be enhanced with NLP analysis of the actual page content
	// For now, use a simple heuristic based on segment summary length
	summaryLength := len(segment.TopicSummary)
	if summaryLength > 100 {
		baseEngagement *= 1.2 // Longer summaries suggest more complex interactions
	}

	return math.Tanh(baseEngagement) // Normalize to [0,1]
}

// calculateTopicImportance estimates topic importance
func (h *HeatScorer) calculateTopicImportance(segment *models.Segment) float64 {
	// This could be enhanced with:
	// - Entity extraction and importance scoring
	// - Topic classification
	// - User priority keywords
	// - Urgency indicators

	importance := 0.5 // Default moderate importance

	// Simple heuristic: check for important keywords in summary
	summary := segment.TopicSummary
	importantKeywords := []string{
		"urgent", "important", "critical", "deadline", "problem",
		"error", "issue", "help", "question", "decision",
	}

	for _, keyword := range importantKeywords {
		if contains(summary, keyword) {
			importance += 0.1
		}
	}

	return math.Tanh(importance) // Normalize to [0,1]
}

// UpdateSegmentAccess updates segment access tracking and recalculates heat
func (h *HeatScorer) UpdateSegmentAccess(ctx context.Context, segmentID string) error {
	collection := h.db.Collection("segments")
	now := time.Now()

	// Increment access count and update last access time
	update := bson.M{
		"$inc": bson.M{"accessCount": 1},
		"$set": bson.M{
			"lastAccessTime": now,
			"updatedAt":      now,
		},
	}

	filter := bson.M{"segmentId": segmentID}
	result, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}

	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}

	// Fetch updated segment and recalculate heat
	var segment models.Segment
	if err := collection.FindOne(ctx, filter).Decode(&segment); err != nil {
		return err
	}

	// Recalculate heat score
	newHeatScore, heatFactors, err := h.ComputeSegmentHeat(ctx, &segment)
	if err != nil {
		return err
	}

	// Update heat score in database
	heatUpdate := bson.M{
		"$set": bson.M{
			"heatScore":   newHeatScore,
			"heatFactors": heatFactors,
			"updatedAt":   now,
		},
	}

	_, err = collection.UpdateOne(ctx, filter, heatUpdate)
	return err
}

// BatchRecalculateHeat recalculates heat scores for multiple segments
func (h *HeatScorer) BatchRecalculateHeat(ctx context.Context, segmentIDs []string) error {
	collection := h.db.Collection("segments")

	for _, segmentID := range segmentIDs {
		var segment models.Segment
		filter := bson.M{"segmentId": segmentID}

		if err := collection.FindOne(ctx, filter).Decode(&segment); err != nil {
			continue // Skip missing segments
		}

		heatScore, heatFactors, err := h.ComputeSegmentHeat(ctx, &segment)
		if err != nil {
			continue // Skip on calculation error
		}

		update := bson.M{
			"$set": bson.M{
				"heatScore":   heatScore,
				"heatFactors": heatFactors,
				"updatedAt":   time.Now(),
			},
		}

		collection.UpdateOne(ctx, filter, update)
	}

	return nil
}

// Helper function to check if string contains substring (case-insensitive)
func contains(text, substr string) bool {
	// Simple case-insensitive contains check
	// In production, you might want to use more sophisticated text analysis
	textLower := strings.ToLower(text)
	substrLower := strings.ToLower(substr)
	return strings.Contains(textLower, substrLower)
}
