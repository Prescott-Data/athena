package memory

import (
	"testing"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
)

// TestHeatScorerTopicImportanceKeywords ensures keywords increase topic importance and overall heat score.
func TestHeatScorerTopicImportanceKeywords(t *testing.T) {
	scorer := NewHeatScorer(nil) // db not needed for pure scoring

	base := &models.CognitiveChain{
		ChainID:     "base",
		UserID:      "user1",
		Summary:     "General discussion about project tasks and planning.",
		AccessCount: 5,
		EventCount:  4,
		StartedAt:   time.Now().Add(-1 * time.Hour),
		LastEventAt: time.Now().Add(-30 * time.Minute),
	}
	keywords := &models.CognitiveChain{
		ChainID:     "kw",
		UserID:      "user1",
		Summary:     "Urgent critical deadline issue impacting release planning.",
		AccessCount: 5,
		EventCount:  4,
		StartedAt:   base.StartedAt,
		LastEventAt: base.LastEventAt,
	}

	baseScore, baseFactors, err := scorer.ComputeSegmentHeat(nil, base)
	if err != nil {
		t.Fatalf("unexpected error computing base score: %v", err)
	}
	kwScore, kwFactors, err := scorer.ComputeSegmentHeat(nil, keywords)
	if err != nil {
		t.Fatalf("unexpected error computing keyword score: %v", err)
	}

	if kwFactors.TopicImportance <= baseFactors.TopicImportance {
		t.Fatalf("expected topic importance to increase with keywords; base=%.3f kw=%.3f", baseFactors.TopicImportance, kwFactors.TopicImportance)
	}
	if kwScore <= baseScore {
		t.Fatalf("expected overall heat score to increase with keywords; base=%.3f kw=%.3f", baseScore, kwScore)
	}
}
