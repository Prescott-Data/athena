package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
)

// TestContinuityAnalyzerLowSemantic verifies that two semantically unrelated chains are marked discontinuous.
func TestContinuityAnalyzerLowSemantic(t *testing.T) {
	prev := &models.CognitiveChain{
		ChainID:     "chain_prev",
		UserID:      "user1",
		Summary:     "Discussing quantum mechanics and particle spin experiments.",
		StartedAt:   time.Now().Add(-2 * time.Hour),
		LastEventAt: time.Now().Add(-90 * time.Minute),
		EventCount:  5,
	}
	current := &models.CognitiveChain{
		ChainID:    "chain_curr",
		UserID:     "user1",
		Summary:    "Ordering lunch sandwiches and choosing beverage options.",
		StartedAt:  time.Now(),
		EventCount: 3,
	}

	analyzer := NewContinuityAnalyzer(nil, nil) // nil stmStore forces semantic similarity failure path
	config := analyzer.GetDefaultConfig()

	res, err := analyzer.AnalyzeContinuity(context.Background(), prev, current, config)
	if err != nil {
		// Should not error; semantic fallback should engage
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsContinuous {
		t.Fatalf("expected chains to be discontinuous; got continuous with confidence %.3f", res.Confidence)
	}
	if res.SemanticScore > config.SemanticThreshold {
		// We forced semantic score path to be 0.0 (stmStore nil), ensure fallback semantics respected
		t.Fatalf("expected low semantic score; got %.3f (threshold %.3f)", res.SemanticScore, config.SemanticThreshold)
	}
}
