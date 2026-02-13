package memory

import (
	"context"
	"math"
	"strings"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// HeatScorer provides advanced heat scoring for cognitive chains
type HeatScorer struct {
	db     *mongo.Database
	config HeatScoringConfig
}

// HeatScoringConfig holds configuration for the heat scoring algorithm
type HeatScoringConfig struct {
	Alpha   float64 // Access frequency weight
	Beta    float64 // Interaction depth weight
	Gamma   float64 // Recency weight
	Delta   float64 // User engagement weight
	Epsilon float64 // Topic importance weight
	Zeta    float64 // Cognitive depth weight

	RecencyTauHours float64 // Time decay constant
	RecencyMaxAge   float64 // Maximum age before negligible score

	EngagementMinQuestionWords int
	EngagementMaxResponseChars int
}

// NewHeatScorer creates a new heat scorer and initializes configuration from environment variables
func NewHeatScorer(db *mongo.Database) *HeatScorer {
	return &HeatScorer{
		db: db,
		config: HeatScoringConfig{
			Alpha:                      parseFloatEnv("HEAT_ALPHA", 1.0),
			Beta:                       parseFloatEnv("HEAT_BETA", 1.0),
			Gamma:                      parseFloatEnv("HEAT_GAMMA", 1.0),
			Delta:                      parseFloatEnv("HEAT_DELTA", 0.5),
			Epsilon:                    parseFloatEnv("HEAT_EPSILON", 0.3),
			Zeta:                       parseFloatEnv("HEAT_ZETA", 0.7),
			RecencyTauHours:            parseFloatEnv("RECENCY_TAU_HOURS", 24.0),
			RecencyMaxAge:              parseFloatEnv("RECENCY_MAX_AGE_HOURS", 168.0),
			EngagementMinQuestionWords: parseIntEnv("ENGAGEMENT_MIN_QUESTION_WORDS", 5),
			EngagementMaxResponseChars: parseIntEnv("ENGAGEMENT_MAX_RESPONSE_CHARS", 1000),
		},
	}
}

// ComputeSegmentHeat calculates the enhanced heat score for a cognitive chain
func (h *HeatScorer) ComputeSegmentHeat(ctx context.Context, chain *models.CognitiveChain) (float64, *models.HeatFactors, error) {
	now := time.Now()

	// Factor 1: Access Frequency
	accessFrequency := h.calculateAccessFrequency(chain)

	// Factor 2: Interaction Depth
	interactionDepth := h.calculateInteractionDepth(chain)

	// Factor 3: Recency Score
	recencyScore := h.calculateRecencyScore(chain, now)

	// Factor 4: User Engagement
	userEngagement := h.calculateUserEngagement(chain)

	// Factor 5: Topic Importance
	topicImportance := h.calculateTopicImportance(chain)

	// Factor 6: Cognitive Depth (based on thought/action events)
	cognitiveDepth := h.calculateCognitiveDepth(ctx, chain)

	// Weighted combination
	heatScore := h.config.Alpha*accessFrequency +
		h.config.Beta*interactionDepth +
		h.config.Gamma*recencyScore +
		h.config.Delta*userEngagement +
		h.config.Epsilon*topicImportance +
		h.config.Zeta*cognitiveDepth

	// Normalize by dividing by the theoretical maximum raw score.
	// This prevents tanh saturation which compressed all scores into [0.86, 1.0].
	// Max realistic factor values: accessFreq≈4.0, depth≈3.5, recency=1.0,
	// engagement=1.0, importance=1.0, cognitive=1.0
	maxRawScore := h.config.Alpha*4.0 +
		h.config.Beta*3.5 +
		h.config.Gamma*1.0 +
		h.config.Delta*1.0 +
		h.config.Epsilon*1.0 +
		h.config.Zeta*1.0
	if maxRawScore <= 0 {
		maxRawScore = 1.0 // Safety fallback
	}
	normalizedScore := math.Min(1.0, heatScore/maxRawScore)

	factors := &models.HeatFactors{
		AccessFrequency:  accessFrequency,
		InteractionDepth: interactionDepth,
		RecencyScore:     recencyScore,
		UserEngagement:   userEngagement,
		TopicImportance:  topicImportance,
		CognitiveDepth:   cognitiveDepth,
	}

	return normalizedScore, factors, nil
}

// calculateAccessFrequency computes the normalized access frequency score from the cognitive chain
func (h *HeatScorer) calculateAccessFrequency(chain *models.CognitiveChain) float64 {
	if chain.AccessCount <= 0 {
		return 0.0
	}
	// Logarithmic scaling to prevent very high access counts from dominating
	return math.Log(float64(chain.AccessCount) + 1.0)
}

// calculateInteractionDepth computes the interaction complexity score from the cognitive chain
func (h *HeatScorer) calculateInteractionDepth(chain *models.CognitiveChain) float64 {
	// Use EventCount as the primary measure of interaction depth
	if chain.EventCount <= 0 {
		return 0.0
	}
	return math.Log(float64(chain.EventCount) + 1.0)
}

// calculateRecencyScore computes the time-decay-based recency score from the cognitive chain
func (h *HeatScorer) calculateRecencyScore(chain *models.CognitiveChain, currentTime time.Time) float64 {
	var referenceTime time.Time

	// Use LastEventAt if available, otherwise StartedAt
	if !chain.LastEventAt.IsZero() {
		referenceTime = chain.LastEventAt
	} else {
		referenceTime = chain.StartedAt
	}

	hoursSince := currentTime.Sub(referenceTime).Hours()

	if hoursSince > h.config.RecencyMaxAge {
		return 0.001 // Return a minimal score if too old
	}

	// Exponential decay: e^(-t/tau)
	return math.Exp(-hoursSince / h.config.RecencyTauHours)
}

// calculateUserEngagement estimates the user engagement level from the cognitive chain
func (h *HeatScorer) calculateUserEngagement(chain *models.CognitiveChain) float64 {
	// Base engagement from the event count
	baseEngagement := math.Log(float64(chain.EventCount)+1.0) / 3.0

	// Enhance score based on summary length as a proxy for complexity
	summaryLength := len(chain.Summary)
	if summaryLength > 100 {
		baseEngagement *= 1.2
	}

	return math.Tanh(baseEngagement) // Normalize to [0, 1]
}

// calculateTopicImportance estimates the topic importance from the cognitive chain's summary
func (h *HeatScorer) calculateTopicImportance(chain *models.CognitiveChain) float64 {
	importance := 0.5 // Default moderate importance

	importantKeywords := []string{
		"urgent", "important", "critical", "deadline", "problem",
		"error", "issue", "help", "question", "decision",
	}

	for _, keyword := range importantKeywords {
		if contains(chain.Summary, keyword) {
			importance += 0.1
		}
	}

	return math.Tanh(importance) // Normalize to [0, 1]
}

// calculateCognitiveDepth assesses cognitive processing depth from events
func (h *HeatScorer) calculateCognitiveDepth(ctx context.Context, chain *models.CognitiveChain) float64 {
	// If database is not available, return a default moderate score
	if h.db == nil {
		return 0.5
	}

	// Fetch events for this chain from the database
	eventsCollection := h.db.Collection(CognitiveEventsCollection)
	filter := bson.M{"chainid": chain.ChainID}

	cursor, err := eventsCollection.Find(ctx, filter)
	if err != nil {
		// If we can't fetch events, return a default moderate score
		return 0.5
	}
	defer cursor.Close(ctx)

	var events []models.CognitiveEvent
	if err := cursor.All(ctx, &events); err != nil {
		return 0.5
	}

	if len(events) == 0 {
		return 0.5
	}

	// Count thought and action events
	thoughtCount := 0
	actionCount := 0
	for _, event := range events {
		switch event.Type {
		case models.STMEventTypeThought:
			thoughtCount++
		case models.STMEventTypeAction:
			actionCount++
		}
	}

	// Base score
	score := 0.5

	// Bonus for thoughts (indicates reasoning)
	if thoughtCount > 0 {
		thoughtRatio := float64(thoughtCount) / float64(len(events))
		score += 0.25 * thoughtRatio // Up to +0.25
	}

	// Bonus for actions (indicates tool use/execution)
	if actionCount > 0 {
		actionRatio := float64(actionCount) / float64(len(events))
		score += 0.2 * actionRatio // Up to +0.2
	}

	// Bonus for balanced cognitive processing
	if thoughtCount > 0 && actionCount > 0 {
		score += 0.15
	}

	// Cap at 1.0
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// UpdateSegmentAccess updates a cognitive chain's access tracking and recalculates its heat score
func (h *HeatScorer) UpdateSegmentAccess(ctx context.Context, chainID string) error {
	collection := h.db.Collection("cognitive_chains")
	now := time.Now()

	update := bson.M{
		"$inc": bson.M{"accessCount": 1},
		"$set": bson.M{
			"lastAccessTime": now,
			"updatedAt":      now,
		},
	}

	filter := bson.M{"chainId": chainID}
	result, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}

	if result.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}

	// Fetch the updated chain to recalculate its heat score
	var chain models.CognitiveChain
	if err := collection.FindOne(ctx, filter).Decode(&chain); err != nil {
		return err
	}

	newHeatScore, heatFactors, err := h.ComputeSegmentHeat(ctx, &chain)
	if err != nil {
		return err
	}

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

// BatchRecalculateHeat recalculates heat scores for multiple cognitive chains
func (h *HeatScorer) BatchRecalculateHeat(ctx context.Context, chainIDs []string) error {
	collection := h.db.Collection("cognitive_chains")

	for _, chainID := range chainIDs {
		var chain models.CognitiveChain
		filter := bson.M{"chainId": chainID}

		if err := collection.FindOne(ctx, filter).Decode(&chain); err != nil {
			continue // Skip if the chain is not found
		}

		heatScore, heatFactors, err := h.ComputeSegmentHeat(ctx, &chain)
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

// contains is a helper function to check for a substring (case-insensitive)
func contains(text, substr string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(substr))
}
