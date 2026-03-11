package memory

import (
	"context"
	"math"
	"time"

	"bitbucket.org/dromos/athena-memos/internal/models"
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
	WeightIntrinsic    float64 // Weight for LLM-provided importance (0.0-1.0)
	WeightDensity      float64 // Weight for contextual density (0.0-1.0)
	DecayTauHours      float64 // Base decay constant in hours
	RecallGrowthFactor float64 // Multiplier for RecallStrength on successful recall
	CooldownHours      float64 // Minimum hours between recall strength boosts
}

// NewHeatScorer creates a new heat scorer and initializes configuration from environment variables
func NewHeatScorer(db *mongo.Database) *HeatScorer {
	return &HeatScorer{
		db: db,
		config: HeatScoringConfig{
			WeightIntrinsic:    parseFloatEnv("HEAT_WEIGHT_INTRINSIC", 0.7),
			WeightDensity:      parseFloatEnv("HEAT_WEIGHT_DENSITY", 0.3),
			DecayTauHours:      parseFloatEnv("HEAT_DECAY_TAU_HOURS", 24.0),
			RecallGrowthFactor: parseFloatEnv("HEAT_RECALL_GROWTH", 1.5),
			CooldownHours:      parseFloatEnv("HEAT_COOLDOWN_HOURS", 12.0),
		},
	}
}

// ComputeSegmentHeat calculates the heat score using the Ebbinghaus Forgetting Curve formula.
// HeatScore = I_base * exp(-DeltaT / (Tau * S))
func (h *HeatScorer) ComputeSegmentHeat(ctx context.Context, chain *models.CognitiveChain) (float64, *models.HeatFactors, error) {
	now := time.Now()

	// 1. Calculate Density Score if not already set (or if we want to refresh it)
	// For efficiency, we only calculate if it's 0.0, assuming 0.0 means uninitialized.
	if chain.DensityScore == 0.0 {
		// Fetch events to calculate density
		events, err := h.fetchEventsForChain(ctx, chain.ChainID)
		if err == nil {
			chain.DensityScore = h.CalculateDensity(chain, events)
		} else {
			// If failed to fetch, default to 0.0 or keep as is?
			// CalculateDensity with nil events might be safe if implemented defensively
			chain.DensityScore = h.CalculateDensity(chain, nil)
		}
	}

	// 2. Calculate Base Importance (I_base)
	// I_base = min(1.0, w1 * Intrinsic + w2 * Density)
	iBase := math.Min(1.0, (h.config.WeightIntrinsic*chain.IntrinsicImportance)+(h.config.WeightDensity*chain.DensityScore))

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
	// Apply the logic (Cooldown, Growth, Recalculation)
	if err := h.ApplyAccessUpdate(ctx, chain); err != nil {
		return err
	}

	// Persist changes to MongoDB
	if h.db != nil {
		collection := h.db.Collection("cognitive_chains")
		filter := bson.M{"chainId": chain.ChainID}
		update := bson.M{
			"$set": bson.M{
				"lastAccessedAt": chain.LastAccessedAt,
				"recallStrength": chain.RecallStrength,
				"heatScore":      chain.HeatScore,
				"heatFactors":    chain.HeatFactors,
				"densityScore":   chain.DensityScore,
				"updatedAt":      time.Now(),
			},
		}
		_, err := collection.UpdateOne(ctx, filter, update)
		return err
	}
	return nil
}

// ApplyAccessUpdate applies the spaced repetition and cooldown logic to the chain object.
// It does NOT persist to the database.
func (h *HeatScorer) ApplyAccessUpdate(ctx context.Context, chain *models.CognitiveChain) error {
	now := time.Now()

	// Initialize RecallStrength if needed
	if chain.RecallStrength < 1.0 {
		chain.RecallStrength = 1.0
	}

	// Determine if RecallStrength should grow (Spaced Repetition Cooldown)
	shouldGrow := true
	if chain.LastAccessedAt != nil {
		elapsed := now.Sub(*chain.LastAccessedAt).Hours()
		if elapsed < h.config.CooldownHours {
			shouldGrow = false
		}
	}

	// 1. Update LastAccessedAt
	chain.LastAccessedAt = &now

	// 2. Increase RecallStrength only if cooldown passed
	if shouldGrow {
		chain.RecallStrength *= h.config.RecallGrowthFactor
	}

	// 3. Recalculate Heat Score immediately to reflect the "refresh"
	// (DeltaT is now 0, so Heat should jump to I_base)
	newHeat, factors, err := h.ComputeSegmentHeat(ctx, chain)
	if err != nil {
		return err
	}
	chain.HeatScore = newHeat
	chain.HeatFactors = factors

	return nil
}

// CalculateDensity computes the Contextual Density score for a chain.
func (h *HeatScorer) CalculateDensity(chain *models.CognitiveChain, events []models.CognitiveEvent) float64 {
	density := 0.0

	// 1. Entity Presence
	if len(chain.Entities) > 0 {
		density += 0.15
	}

	hasThoughtOrAction := false
	hasWorkflowMetadata := false
	uniqueOriginServices := make(map[string]bool)

	for _, event := range events {
		// 2. Event Types
		if !hasThoughtOrAction && (event.Type == models.STMEventTypeThought || event.Type == models.STMEventTypeAction) {
			density += 0.20
			hasThoughtOrAction = true
		}

		// 3. Workflow Metadata
		if !hasWorkflowMetadata && event.Type == models.STMEventTypeObservation {
			if event.Metadata != nil {
				_, hasWf := event.Metadata["workflow_id"]
				_, hasExec := event.Metadata["execution_id"]
				_, hasBlob := event.Metadata["blob_url"]
				if hasWf || hasExec || hasBlob {
					density += 0.25
					hasWorkflowMetadata = true
				}
			}
		}

		// 4. Origin Service Tracking
		if event.Metadata != nil {
			if svc, ok := event.Metadata["origin_service"]; ok {
				if svcStr, valid := svc.(string); valid && svcStr != "" {
					uniqueOriginServices[svcStr] = true
				}
			}
		}
	}

	// 5. Multi-Service Context
	if len(uniqueOriginServices) > 1 {
		density += 0.20
	}

	// Cap at 1.0
	return math.Min(1.0, density)
}

// fetchEventsForChain retrieves events for a given chain ID
func (h *HeatScorer) fetchEventsForChain(ctx context.Context, chainID string) ([]models.CognitiveEvent, error) {
	if h.db == nil {
		return nil, mongo.ErrNoDocuments
	}

	eventsCollection := h.db.Collection(CognitiveEventsCollection)
	filter := bson.M{"chainId": chainID}

	cursor, err := eventsCollection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var events []models.CognitiveEvent
	if err := cursor.All(ctx, &events); err != nil {
		return nil, err
	}
	return events, nil
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
				"densityScore":   chain.DensityScore, // Ensure this is saved
				"updatedAt":      time.Now(),
			},
		}

		collection.UpdateOne(ctx, filter, update)
	}

	return nil
}
