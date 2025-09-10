package memory

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/dromos-org/memory-os/internal/kg/janus"
	"github.com/dromos-org/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Promoter computes heat scores and promotes canonical triples to LPM KG
type Promoter struct {
	db         *mongo.Database
	janus      *janus.Client
	heatScorer *HeatScorer
}

func NewPromoter(db *mongo.Database) *Promoter {
	endpoint := os.Getenv("JANUS_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://janusgraph:8182"
	}
	return &Promoter{
		db:         db,
		janus:      janus.New(endpoint),
		heatScorer: NewHeatScorer(db),
	}
}

// RunOnce scans recent segments, computes enhanced heat scores, and promotes if above threshold
func (p *Promoter) RunOnce(ctx context.Context, threshold float64) error {
	if p.db == nil || p.janus == nil || p.heatScorer == nil {
		return fmt.Errorf("promoter dependencies not ready")
	}

	log.Printf("INFO: Starting promoter run with threshold %.2f", threshold)

	col := p.db.Collection("segments")
	cur, err := col.Find(ctx, bson.M{"status": "in_mtm"})
	if err != nil {
		return fmt.Errorf("failed to query segments: %w", err)
	}
	defer cur.Close(ctx)

	var segments []models.Segment
	if err := cur.All(ctx, &segments); err != nil {
		return fmt.Errorf("failed to decode segments: %w", err)
	}

	log.Printf("INFO: Found %d segments to analyze", len(segments))
	promotedCount := 0

	for _, segment := range segments {
		// Compute enhanced heat score
		heatScore, heatFactors, err := p.heatScorer.ComputeSegmentHeat(ctx, &segment)
		if err != nil {
			log.Printf("WARN: Failed to compute heat for segment %s: %v", segment.SegmentID, err)
			continue
		}

		// Update segment with new heat score
		if err := p.updateSegmentHeat(ctx, segment.SegmentID, heatScore, heatFactors); err != nil {
			log.Printf("WARN: Failed to update heat for segment %s: %v", segment.SegmentID, err)
		}

		log.Printf("DEBUG: Segment %s heat: %.3f (access: %.2f, depth: %.2f, recency: %.2f, engagement: %.2f, importance: %.2f)",
			segment.SegmentID, heatScore,
			heatFactors.AccessFrequency, heatFactors.InteractionDepth, heatFactors.RecencyScore,
			heatFactors.UserEngagement, heatFactors.TopicImportance)

		if heatScore >= threshold {
			// Promote to LPM with enhanced triple extraction
			if err := p.promoteSegmentToLPM(ctx, &segment, heatScore); err != nil {
				log.Printf("WARN: Failed to promote segment %s: %v", segment.SegmentID, err)
			} else {
				promotedCount++
				log.Printf("INFO: Promoted segment %s for user %s (heat=%.3f)",
					segment.SegmentID, segment.UserID, heatScore)
			}
		}
	}

	log.Printf("INFO: Promoter run completed. Promoted %d/%d segments", promotedCount, len(segments))
	return nil
}

// updateSegmentHeat updates a segment's heat score and factors in the database
func (p *Promoter) updateSegmentHeat(ctx context.Context, segmentID string, heatScore float64, heatFactors *models.HeatFactors) error {
	col := p.db.Collection("segments")
	filter := bson.M{"segmentId": segmentID}
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

// promoteSegmentToLPM promotes a hot segment to long-term personal memory
func (p *Promoter) promoteSegmentToLPM(ctx context.Context, segment *models.Segment, heatScore float64) error {
	// Enhanced triple extraction could be implemented here
	// For now, keeping the existing simple approach
	triples := []janus.Triple{{
		Subject:   segment.UserID,
		Predicate: "interested_in_topic",
		Object:    segment.TopicSummary,
	}}

	conf := math.Min(0.99, heatScore)

	// Ensure user exists in the knowledge graph
	if err := p.janus.CreateUserNode(ctx, segment.TenantID, segment.UserID); err != nil {
		return fmt.Errorf("failed to ensure user exists: %w", err)
	}

	// Write triples to LPM with segment provenance
	for _, triple := range triples {
		if err := p.janus.AddUserPersonalityTriple(ctx, segment.TenantID, segment.UserID, triple.Predicate, triple.Object, conf); err != nil {
			return fmt.Errorf("failed to write user triple (%s -> %s): %w", triple.Predicate, triple.Object, err)
		}
	}

	return nil
}
