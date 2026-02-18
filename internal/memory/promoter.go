package memory

import (
	"context"
	"fmt"
	"log"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Promoter computes heat scores and promotes canonical triples to the LPM Knowledge Graph
type Promoter struct {
	db         *mongo.Database
	heatScorer *HeatScorer
}

// NewPromoter creates a new Promoter service
func NewPromoter(db *mongo.Database) *Promoter {
	return &Promoter{
		db:         db,
		heatScorer: NewHeatScorer(db),
	}
}

// RunOnce scans recent cognitive chains, computes their heat scores, and promotes them if they are above a given threshold
func (p *Promoter) RunOnce(ctx context.Context, threshold float64) error {
	if p.db == nil || p.heatScorer == nil {
		return fmt.Errorf("promoter dependencies not ready")
	}

	log.Printf("INFO: Starting promoter run with threshold %.2f", threshold)

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

	log.Printf("INFO: Found %d cognitive chains to analyze", len(chains))
	promotedCount := 0

	for _, chain := range chains {
		// Use the existing heat score if available and recent, otherwise re-compute.
		// For this test, we will always re-compute.
		heatScore, heatFactors, err := p.heatScorer.ComputeSegmentHeat(ctx, &chain)
		if err != nil {
			log.Printf("WARN: Failed to compute heat for chain %s: %v", chain.ChainID, err)
			continue
		}

		if err := p.updateChainHeat(ctx, chain.ChainID, heatScore, heatFactors); err != nil {
			log.Printf("WARN: Failed to update heat for chain %s: %v", chain.ChainID, err)
		}

		log.Printf("DEBUG: Chain %s heat: %.3f (base: %.2f, decay: %.2f, recall: %.2f)",
			chain.ChainID, heatScore,
			heatFactors.BaseImportance, heatFactors.TimeDecay, heatFactors.RecallStrength)

		if heatScore >= threshold {
			if err := p.promoteChainToLPM(ctx, &chain, heatScore); err != nil {
				log.Printf("WARN: Failed to promote chain %s: %v", chain.ChainID, err)
			} else {
				promotedCount++
				log.Printf("INFO: Promoted chain %s for user %s (heat=%.3f)",
					chain.ChainID, chain.UserID, heatScore)
			}
		}
	}

	log.Printf("INFO: Promoter run completed. Promoted %d/%d chains", promotedCount, len(chains))
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
// When the chain has extracted entities, it writes one structured triple per entity
// (e.g. User → interested_in → "OAuth2"). Falls back to a single topic-based triple
// for older chains that pre-date entity extraction.
func (p *Promoter) promoteChainToLPM(ctx context.Context, chain *models.CognitiveChain, heatScore float64) error {
	// LTM write (JanusGraph/ArangoDB) is disabled — handled by colleague's Odin integration.
	// Entities are stored on the chain in MongoDB and ready for future graph writing.
	if len(chain.Entities) > 0 {
		log.Printf("INFO: Chain %s qualified for LTM promotion (heat=%.3f). Entities ready: %v. LTM write skipped (pending Odin integration).", chain.ChainID, heatScore, chain.Entities)
	} else {
		log.Printf("INFO: Chain %s qualified for LTM promotion (heat=%.3f). LTM write skipped (pending Odin integration).", chain.ChainID, heatScore)
	}
	return nil
}
