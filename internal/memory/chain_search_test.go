package memory

import (
	"math"
	"testing"

	"github.com/Prescott-Data/athena/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSquaredL2ToCosine(t *testing.T) {
	a := normalizeVector([]float64{1, 2, 3})
	b := normalizeVector([]float64{3, -1, 2})
	var dot, d float64
	for i := range a {
		dot += a[i] * b[i]
		d += (a[i] - b[i]) * (a[i] - b[i])
	}
	assert.InDelta(t, dot, float64(squaredL2ToCosine(float32(d))), 1e-6)
	assert.Equal(t, float32(1), squaredL2ToCosine(0), "identical vectors")
	assert.Equal(t, float32(-1), squaredL2ToCosine(4), "opposite vectors")
	assert.Equal(t, float32(-1), squaredL2ToCosine(4.1), "clamped")
}

func TestNormalizeVectorMakesUnitLength(t *testing.T) {
	v := normalizeVector([]float64{3, 4})
	assert.InDelta(t, 1, math.Hypot(v[0], v[1]), 1e-12)
	assert.Equal(t, []float64{0, 0}, normalizeVector([]float64{0, 0}))
}

func TestRankChainsBySimilarity(t *testing.T) {
	query := []float64{1, 0, 0}
	chains := []*models.CognitiveChain{
		{ChainID: "orthogonal"}, {ChainID: "close"}, {ChainID: "exact"}, {ChainID: "no-vector"}, {ChainID: "opposite"},
	}
	vectors := map[string][]float32{
		"exact":      {2, 0, 0}, // stored vectors need not be unit length
		"close":      {0.8, 0.6, 0},
		"orthogonal": {0, 1, 0},
		"opposite":   {-1, 0, 0},
	}

	ranked := rankChainsBySimilarity(chains, vectors, query, 0.0001, 10)
	ids := []string{}
	for _, c := range ranked {
		ids = append(ids, c.ChainID)
	}
	assert.Equal(t, []string{"exact", "close"}, ids, "most similar first; missing vectors and scores below the threshold dropped")
	assert.InDelta(t, 1.0, ranked[0].SimilarityScore, 1e-6)
	assert.InDelta(t, 0.8, ranked[1].SimilarityScore, 1e-6)

	ranked = rankChainsBySimilarity(chains, vectors, query, 0.5, 1)
	require.Len(t, ranked, 1)
	assert.Equal(t, "exact", ranked[0].ChainID)

	all := rankChainsBySimilarity(chains, vectors, query, 0, 10)
	assert.Len(t, all, 4, "no threshold keeps every chain that has a vector")
	assert.Equal(t, "opposite", all[3].ChainID)
}

func TestRankChainsIgnoresVectorsOfAnotherDimension(t *testing.T) {
	chains := []*models.CognitiveChain{{ChainID: "a"}}
	ranked := rankChainsBySimilarity(chains, map[string][]float32{"a": {1, 0}}, []float64{1, 0, 0}, 0, 5)
	assert.Empty(t, ranked)
}
