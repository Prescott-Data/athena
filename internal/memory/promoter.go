package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Prescott-Data/athena/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// GraphExtractor defines the interface for extracting knowledge graph triples from text.
type GraphExtractor interface {
	ExtractGraphFromSummary(ctx context.Context, summary string) (*GraphExtraction, error)
}

// GraphWriter defines the interface for upserting extracted graph data to a storage backend.
type GraphWriter interface {
	WriteExtractionToGraph(ctx context.Context, extraction *GraphExtraction, heatScore float64) error
}

// Promoter computes heat scores and promotes canonical triples to the LPM Knowledge Graph
type Promoter struct {
	db         *mongo.Database
	heatScorer *HeatScorer
	extractor  GraphExtractor
	writer     GraphWriter
}

// NewPromoter creates a new Promoter service
func NewPromoter(db *mongo.Database, extractor GraphExtractor, writer GraphWriter) *Promoter {
	return &Promoter{
		db:         db,
		heatScorer: NewHeatScorer(db),
		extractor:  extractor,
		writer:     writer,
	}
}

// RunOnce scans recent cognitive chains, computes their heat scores, and promotes them if they are above a given threshold
func (p *Promoter) RunOnce(ctx context.Context, threshold float64) error {
	if p.db == nil || p.heatScorer == nil {
		return fmt.Errorf("promoter dependencies not ready")
	}

	slog.Info("Starting promoter run", slog.Float64("threshold", threshold))

	col := p.db.Collection("cognitive_chains")
	// In a real scenario, you might filter for chains updated since the last run.
	cur, err := col.Find(ctx, bson.M{"status": "active", "eventCount": bson.M{"$gt": 0}})
	if err != nil {
		return fmt.Errorf("failed to query cognitive_chains: %w", err)
	}
	defer cur.Close(ctx)

	var chains []models.CognitiveChain
	if err := cur.All(ctx, &chains); err != nil {
		return fmt.Errorf("failed to decode cognitive chains: %w", err)
	}

	slog.Info("Found cognitive chains to analyze", slog.Int("count", len(chains)))
	promotedCount := 0

	for _, chain := range chains {
		// Use the existing heat score if available and recent, otherwise re-compute.
		// For this test, we will always re-compute.
		heatScore, heatFactors, err := p.heatScorer.ComputeSegmentHeat(ctx, &chain)
		if err != nil {
			slog.Warn("Failed to compute heat for chain", slog.String("chain_id", chain.ChainID), slog.String("error", err.Error()))
			continue
		}

		if err := p.updateChainHeat(ctx, chain.ChainID, heatScore, heatFactors); err != nil {
			slog.Warn("Failed to update heat for chain", slog.String("chain_id", chain.ChainID), slog.String("error", err.Error()))
		}

		PromoterChainsEvaluated.Inc()
		HeatScoreDistribution.Observe(heatScore)

		slog.Debug("Evaluating cognitive chain",
			slog.String("chain_id", chain.ChainID),
			slog.Float64("calculated_heat_score", heatScore),
			slog.Float64("required_threshold", threshold),
			slog.Float64("base_importance", heatFactors.BaseImportance),
			slog.Float64("time_decay", heatFactors.TimeDecay),
			slog.Float64("recall_strength", heatFactors.RecallStrength))

		if heatScore >= threshold {
			if err := p.promoteChainToLPM(ctx, &chain, heatScore); err != nil {
				slog.Error("Failed to process promoted chain into LTM", slog.String("chain_id", chain.ChainID), slog.String("error", err.Error()))
			} else {
				promotedCount++
				slog.Info("Chain promoted to LTM", slog.String("chain_id", chain.ChainID), slog.Float64("heat_score", heatScore), slog.Int("message_count", chain.EventCount))
			}
		} else {
			slog.Info("Chain rejected by promoter (insufficient heat)", slog.String("chain_id", chain.ChainID), slog.Float64("heat_score", heatScore))
		}
	}

	slog.Info("Promoter run completed", slog.Int("promoted_count", promotedCount), slog.Int("total_chains", len(chains)))
	return nil
}

// updateChainHeat updates a cognitive chain's heat score and factors in the database
func (p *Promoter) updateChainHeat(ctx context.Context, chainID string, heatScore float64, heatFactors *models.HeatFactors) error {
	col := p.db.Collection("cognitive_chains")
	filter := bson.M{"chainId": chainID}
	update := bson.M{
		"$set": bson.M{
			"heatScore":   heatScore,
			"heatFactors": heatFactors,
			"updatedAt":   time.Now(),
		},
	}
	_, err := col.UpdateOne(ctx, filter, update)
	return err
}

// promoteChainToLPM promotes a "hot" cognitive chain to the Long-Term Personal Memory (LPM).
func (p *Promoter) promoteChainToLPM(ctx context.Context, chain *models.CognitiveChain, heatScore float64) error {
	if p.extractor == nil || p.writer == nil {
		slog.Warn("LTM dependencies not wired, skipping promotion", slog.String("chain_id", chain.ChainID), slog.Float64("heat_score", heatScore))
		return nil
	}

	if chain.Summary == "" {
		slog.Warn("Chain has no summary to extract graph from, skipping promotion", slog.String("chain_id", chain.ChainID))
		return nil
	}

	slog.Info("Extracting graph for LTM promotion", slog.String("chain_id", chain.ChainID), slog.Float64("heat_score", heatScore))

	// 1. Extract the Graph Triples from the MTM Summary using the LLM
	extraction, err := p.extractor.ExtractGraphFromSummary(ctx, chain.Summary)
	if err != nil {
		return fmt.Errorf("failed to extract graph from chain %s: %w", chain.ChainID, err)
	}

	slog.Info("Graph extraction completed", slog.String("chain_id", chain.ChainID), slog.Int("nodes", len(extraction.Nodes)), slog.Int("edges", len(extraction.Edges)))

	// 2. Upsert the Triples into the ArangoDB LTM Graph
	err = p.writer.WriteExtractionToGraph(ctx, extraction, heatScore)
	if err != nil {
		return fmt.Errorf("failed to write extraction to LTM graph for chain %s: %w", chain.ChainID, err)
	}

	PromoterChainsPromoted.Inc()
	slog.Info("Successfully promoted chain to LTM Graph", slog.String("chain_id", chain.ChainID))
	return nil
}

// TriggerAnalytics manually triggers a background graph analytics job (Community Detection).
func (p *Promoter) TriggerAnalytics(ctx context.Context) error {
	if p.writer == nil {
		return fmt.Errorf("promoter writer is not configured")
	}

	// The writer is stored as a GraphWriter interface.
	// We must type assert it to *LTMWriter to access the analytics functions.
	ltmWriter, ok := p.writer.(*LTMWriter)
	if !ok {
		return fmt.Errorf("underlying writer does not support graph analytics (not an LTMWriter)")
	}

	slog.Info("Manually triggering graph analytics job...")
	return ltmWriter.TriggerCommunityDetection(ctx)
}
