package memory

import (
	"context"
	"math"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// HeatScorer provides advanced heat scoring for cognitive chains based on Ebbinghaus Forgetting Curve
type HeatScorer struct {
	db     *mongo.Database
	config HeatScoringConfig
}

// HeatScoringConfig holds configuration for the Ebbinghaus heat scoring algorithm
type HeatScoringConfig struct {
	WeightIntrinsic  float64 // Weight for LLM-provided importance (0.0-1.0)
	WeightCognitive  float64 // Weight for cognitive depth (0.0-1.0)
	DecayTauHours    float64 // Base decay constant in hours
	RecallGrowthFactor float64 // Multiplier for RecallStrength on successful recall
}

// NewHeatScorer creates a new heat scorer and initializes configuration from environment variables
func NewHeatScorer(db *mongo.Database) *HeatScorer {
	return &HeatScorer{
		db: db,
		config: HeatScoringConfig{
			WeightIntrinsic:    parseFloatEnv("HEAT_WEIGHT_INTRINSIC", 0.8),
			WeightCognitive:    parseFloatEnv("HEAT_WEIGHT_COGNITIVE", 0.2),
			DecayTauHours:      parseFloatEnv("HEAT_DECAY_TAU_HOURS", 24.0),
			RecallGrowthFactor: parseFloatEnv("HEAT_RECALL_GROWTH", 1.5),
		},
	}
}

// ComputeSegmentHeat calculates the heat score using the Ebbinghaus Forgetting Curve formula.
// HeatScore = I_base * exp(-DeltaT / (Tau * S))
func (h *HeatScorer) ComputeSegmentHeat(ctx context.Context, chain *models.CognitiveChain) (float64, *models.HeatFactors, error) {
	now := time.Now()

	// 1. Calculate Cognitive Score if not already set (or if we want to refresh it)
	// For efficiency, we only calculate if it's 0.0, assuming 0.0 means uninitialized.
	// In a real system, we might want to be more robust.
	if chain.CognitiveScore == 0.0 {
		chain.CognitiveScore = h.calculateCognitiveScore(ctx, chain)
	}

	// 2. Calculate Base Importance (I_base)
	// I_base = min(1.0, w1 * Intrinsic + w2 * Cognitive)
	// If IntrinsicImportance is 0 (e.g., legacy data), we might default it or let it be 0.
	iBase := math.Min(1.0, (h.config.WeightIntrinsic*chain.IntrinsicImportance)+(h.config.WeightCognitive*chain.CognitiveScore))

	// 3. Calculate Time Delta (DeltaT)
	var lastAccess time.Time
	if chain.LastAccessedAt != nil {
		lastAccess = *chain.LastAccessedAt
	} else {
		// Fallback to LastEventAt or StartedAt if never accessed
		if !chain.LastEventAt.IsZero() {
			lastAccess = chain.LastEventAt
		} else {
			lastAccess = chain.StartedAt
		}
	}
	deltaHours := now.Sub(lastAccess).Hours()
	if deltaHours < 0 {
		deltaHours = 0
	}

	// 4. Ensure RecallStrength is at least 1.0
	recallStrength := chain.RecallStrength
	if recallStrength < 1.0 {
		recallStrength = 1.0
	}

	// 5. Calculate Heat Score (H_t)
	// Heat = I_base * exp(-DeltaT / (Tau * S))
	decayFactor := math.Exp(-deltaHours / (h.config.DecayTauHours * recallStrength))
	heatScore := iBase * decayFactor

	factors := &models.HeatFactors{
		BaseImportance: iBase,
		TimeDecay:      decayFactor,
		RecallStrength: recallStrength,
	}

	return heatScore, factors, nil
}

// RecordAccess updates a cognitive chain when it is retrieved/recalled.
// It implements Spaced Repetition by increasing RecallStrength and resetting the forgetting curve.
func (h *HeatScorer) RecordAccess(ctx context.Context, chain *models.CognitiveChain) error {
	now := time.Now()

	// 1. Update LastAccessedAt
	chain.LastAccessedAt = &now

	// 2. Increase RecallStrength
	// S_new = S_old * GrowthFactor
	if chain.RecallStrength < 1.0 {
		chain.RecallStrength = 1.0
	}
	chain.RecallStrength *= h.config.RecallGrowthFactor

	// 3. Recalculate Heat Score immediately to reflect the "refresh"
	// (DeltaT is now 0, so Heat should jump to I_base)
	newHeat, factors, err := h.ComputeSegmentHeat(ctx, chain)
	if err != nil {
		return err
	}
	chain.HeatScore = newHeat
	chain.HeatFactors = factors

	// 4. Persist changes to MongoDB
	collection := h.db.Collection("cognitive_chains")
	filter := bson.M{"chainId": chain.ChainID}
	update := bson.M{
		"$set": bson.M{
			"lastAccessedAt": chain.LastAccessedAt,
			"recallStrength": chain.RecallStrength,
			"heatScore":      chain.HeatScore,
			"heatFactors":    chain.HeatFactors,
			"cognitiveScore": chain.CognitiveScore, // Save this too in case it was computed
			"updatedAt":      now,
		},
	}

	_, err = collection.UpdateOne(ctx, filter, update)
	return err
}

// calculateCognitiveScore assesses cognitive processing depth from events.
// Formerly calculateCognitiveDepth.
func (h *HeatScorer) calculateCognitiveScore(ctx context.Context, chain *models.CognitiveChain) float64 {
	// If database is not available, return a default moderate score
	if h.db == nil {
		return 0.5
	}

	// Fetch events for this chain from the database
	eventsCollection := h.db.Collection(CognitiveEventsCollection)
	filter := bson.M{"chainid": chain.ChainID}

	cursor, err := eventsCollection.Find(ctx, filter)
	if err != nil {
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

// UpdateSegmentAccess is deprecated but kept for compatibility. Use RecordAccess instead.
// It adapts the old interface to the new logic.
func (h *HeatScorer) UpdateSegmentAccess(ctx context.Context, chainID string) error {
	collection := h.db.Collection("cognitive_chains")
	filter := bson.M{"chainId": chainID}

	var chain models.CognitiveChain
	if err := collection.FindOne(ctx, filter).Decode(&chain); err != nil {
		return err
	}

	return h.RecordAccess(ctx, &chain)
}

// BatchRecalculateHeat recalculates heat scores for multiple cognitive chains
func (h *HeatScorer) BatchRecalculateHeat(ctx context.Context, chainIDs []string) error {
	collection := h.db.Collection("cognitive_chains")

	for _, chainID := range chainIDs {
		var chain models.CognitiveChain
		filter := bson.M{"chainId": chainID}

		if err := collection.FindOne(ctx, filter).Decode(&chain); err != nil {
			continue
		}

		heatScore, heatFactors, err := h.ComputeSegmentHeat(ctx, &chain)
		if err != nil {
			continue
		}

		update := bson.M{
			"$set": bson.M{
				"heatScore":      heatScore,
				"heatFactors":    heatFactors,
				"cognitiveScore": chain.CognitiveScore, // Ensure this is saved
				"updatedAt":      time.Now(),
			},
		}

		collection.UpdateOne(ctx, filter, update)
	}

	return nil
}
