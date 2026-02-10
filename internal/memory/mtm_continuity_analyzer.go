package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

// ContinuityAnalyzer determines if conversation chains should be linked together
type ContinuityAnalyzer struct {
	db         *mongo.Database
	stmStore   *STMStore
	HTTPClient *http.Client
}

// ContinuityResult represents the result of continuity analysis
type ContinuityResult struct {
	IsContinuous   bool     `json:"is_continuous"`
	Confidence     float64  `json:"confidence"`
	SemanticScore  float64  `json:"semantic_score"`
	LLMScore       *float64 `json:"llm_score,omitempty"`
	Reasoning      string   `json:"reasoning"`
	AnalysisMethod string   `json:"analysis_method"` // "semantic_only", "llm_assisted", "hybrid"
}

// ContinuityConfig holds configuration for continuity analysis
type ContinuityConfig struct {
	SemanticThreshold   float64
	ConfidenceThreshold float64
	LLMTimeout          time.Duration
	MaxTimeBetween      time.Duration
}

// NewContinuityAnalyzer creates a new continuity analyzer
func NewContinuityAnalyzer(db *mongo.Database, stmStore *STMStore) *ContinuityAnalyzer {
	return &ContinuityAnalyzer{
		db:         db,
		stmStore:   stmStore,
		HTTPClient: &http.Client{},
	}
}

// GetDefaultConfig returns default continuity analysis configuration from environment variables
func (c *ContinuityAnalyzer) GetDefaultConfig() ContinuityConfig {
	llmTimeout := time.Duration(parseIntEnv("CONTINUITY_LLM_TIMEOUT_SECONDS", 10)) * time.Second
	maxTimeBetween := time.Duration(parseIntEnv("CONTINUITY_MAX_TIME_HOURS", 24)) * time.Hour

	return ContinuityConfig{
		SemanticThreshold:   parseFloatEnv("CONTINUITY_SEMANTIC_THRESHOLD", 0.4),
		ConfidenceThreshold: parseFloatEnv("CONTINUITY_CONFIDENCE_THRESHOLD", 0.8),
		LLMTimeout:          llmTimeout,
		MaxTimeBetween:      maxTimeBetween,
	}
}

// AnalyzeContinuity determines if two cognitive chains should be linked
func (c *ContinuityAnalyzer) AnalyzeContinuity(ctx context.Context, prevChain, currentChain *models.CognitiveChain, config ContinuityConfig) (*ContinuityResult, error) {
	start := time.Now()

	if prevChain == nil || currentChain == nil {
		return &ContinuityResult{IsContinuous: false, Confidence: 1.0, Reasoning: "One or both chains are nil", AnalysisMethod: "quick_check"}, nil
	}

	if currentChain.StartedAt.Sub(prevChain.LastEventAt) > config.MaxTimeBetween {
		return &ContinuityResult{IsContinuous: false, Confidence: 0.9, Reasoning: "Time gap too large between chains", AnalysisMethod: "time_check"}, nil
	}

	if prevChain.UserID != currentChain.UserID {
		return &ContinuityResult{IsContinuous: false, Confidence: 1.0, Reasoning: "Different users", AnalysisMethod: "user_check"}, nil
	}

	semanticScore, err := c.calculateSemanticSimilarity(ctx, prevChain, currentChain)
	if err != nil {
		log.Printf("WARN: Semantic similarity calculation failed: %v", err)
		semanticScore = 0.0
	}

	log.Printf("DEBUG: Continuity analysis - Semantic score: %.3f for chains %s -> %s", semanticScore, prevChain.ChainID, currentChain.ChainID)

	if semanticScore >= config.ConfidenceThreshold {
		return &ContinuityResult{IsContinuous: true, Confidence: semanticScore, SemanticScore: semanticScore, Reasoning: "High semantic similarity", AnalysisMethod: "semantic_only"}, nil
	}

	if semanticScore < config.SemanticThreshold {
		return &ContinuityResult{IsContinuous: false, Confidence: 1.0 - semanticScore, SemanticScore: semanticScore, Reasoning: "Low semantic similarity indicates topic change", AnalysisMethod: "semantic_only"}, nil
	}

	llmScore, llmReasoning, err := c.analyzeContinuityWithLLM(ctx, prevChain, currentChain, config.LLMTimeout)
	if err != nil {
		log.Printf("WARN: LLM continuity analysis failed: %v", err)
		isContinuous := semanticScore >= config.SemanticThreshold
		return &ContinuityResult{IsContinuous: isContinuous, Confidence: math.Abs(semanticScore - config.SemanticThreshold), SemanticScore: semanticScore, Reasoning: fmt.Sprintf("LLM failed, fallback to semantic: %s", llmReasoning), AnalysisMethod: "semantic_fallback"}, nil
	}

	combinedScore := 0.6*semanticScore + 0.4*llmScore
	isContinuous := combinedScore >= config.SemanticThreshold

	duration := time.Since(start)
	log.Printf("INFO: Continuity analysis completed - Chains %s -> %s: continuous=%t, semantic=%.3f, llm=%.3f, combined=%.3f, duration=%v", prevChain.ChainID, currentChain.ChainID, isContinuous, semanticScore, llmScore, combinedScore, duration)

	return &ContinuityResult{IsContinuous: isContinuous, Confidence: combinedScore, SemanticScore: semanticScore, LLMScore: &llmScore, Reasoning: llmReasoning, AnalysisMethod: "hybrid"}, nil
}

// calculateSemanticSimilarity computes semantic similarity between cognitive chain summaries
func (c *ContinuityAnalyzer) calculateSemanticSimilarity(ctx context.Context, chain1, chain2 *models.CognitiveChain) (float64, error) {
	if c.stmStore == nil {
		return 0.0, fmt.Errorf("STM store not available for embedding calculation")
	}

	if chain1.Summary == "" || chain2.Summary == "" {
		return 0.0, nil
	}

	emb1, err := c.stmStore.CreateEmbedding(ctx, chain1.Summary)
	if err != nil {
		return 0.0, fmt.Errorf("failed to create embedding for first chain: %w", err)
	}

	emb2, err := c.stmStore.CreateEmbedding(ctx, chain2.Summary)
	if err != nil {
		return 0.0, fmt.Errorf("failed to create embedding for second chain: %w", err)
	}

	similarity, err := cosineSimilarity(emb1.Vector, emb2.Vector)
	if err != nil {
		return 0.0, fmt.Errorf("failed to calculate cosine similarity: %w", err)
	}

	return similarity, nil
}

// analyzeContinuityWithLLM uses an LLM to determine conversation continuity
func (c *ContinuityAnalyzer) analyzeContinuityWithLLM(ctx context.Context, prevChain, currentChain *models.CognitiveChain, timeout time.Duration) (float64, string, error) {
	llmBaseURL := os.Getenv("LLM_BASE_URL")
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")

	if llmBaseURL == "" {
		return 0.0, "LLM not configured", fmt.Errorf("LLM_BASE_URL not configured")
	}
	if apiKey == "" {
		return 0.0, "API key not configured", fmt.Errorf("AZURE_OPENAI_API_KEY not configured")
	}

	prompt := c.buildContinuityPrompt(prevChain, currentChain)

	request := map[string]interface{}{
		"prompt":      prompt,
		"max_tokens":  200,
		"temperature": 0.1,
		"stop":        []string{"\n"},
	}

	llmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	responseBody, err := c.callLLMAPI(llmCtx, llmBaseURL, apiKey, request)
	if err != nil {
		return 0.0, "LLM API call failed", err
	}

	return c.parseLLMContinuityResponse(responseBody)
}

// buildContinuityPrompt creates a prompt for LLM continuity analysis
func (c *ContinuityAnalyzer) buildContinuityPrompt(prevChain, currentChain *models.CognitiveChain) string {
	return fmt.Sprintf(`Analyze whether these two conversation chains represent a continuous conversation or a topic change.

Previous chain (ID: %s):
Summary: %s
Ended: %s

Current chain (ID: %s):
Summary: %s
Started: %s

Instructions:
1. Consider topic similarity, context flow, and the temporal relationship.
2. Respond with a JSON object containing:
   - "score": A number between 0.0 (completely different topics) and 1.0 (clearly continuous).
   - "reasoning": A brief explanation of your analysis.

Response:`,
		prevChain.ChainID,
		prevChain.Summary,
		prevChain.LastEventAt.Format("2006-01-02 15:04:05"),
		currentChain.ChainID,
		currentChain.Summary,
		currentChain.StartedAt.Format("2006-01-02 15:04:05"))
}

// callLLMAPI makes the actual API call to the LLM service
func (c *ContinuityAnalyzer) callLLMAPI(ctx context.Context, url, apiKey string, request map[string]interface{}) ([]byte, error) {
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make LLM API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LLM API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}

// parseLLMContinuityResponse parses the LLM response for continuity analysis
func (c *ContinuityAnalyzer) parseLLMContinuityResponse(responseBody []byte) (float64, string, error) {
	var llmResponse struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(responseBody, &llmResponse); err != nil {
		return 0.0, "Failed to parse LLM response", fmt.Errorf("failed to unmarshal LLM response: %w", err)
	}

	if len(llmResponse.Choices) == 0 {
		return 0.0, "No LLM choices returned", fmt.Errorf("no choices in LLM response")
	}

	var continuityResponse struct {
		Score     float64 `json:"score"`
		Reasoning string  `json:"reasoning"`
	}

	responseText := strings.TrimSpace(llmResponse.Choices[0].Text)
	if err := json.Unmarshal([]byte(responseText), &continuityResponse); err != nil {
		return 0.5, "Could not parse structured response", nil
	}

	if continuityResponse.Score < 0.0 || continuityResponse.Score > 1.0 {
		return 0.5, "Score out of range", nil
	}

	return continuityResponse.Score, continuityResponse.Reasoning, nil
}

// BatchAnalyzeContinuity analyzes continuity for multiple cognitive chain pairs
func (c *ContinuityAnalyzer) BatchAnalyzeContinuity(ctx context.Context, chains []*models.CognitiveChain, config ContinuityConfig) ([]*ContinuityResult, error) {
	if len(chains) < 2 {
		return []*ContinuityResult{}, nil
	}

	results := make([]*ContinuityResult, 0, len(chains)-1)

	for i := 1; i < len(chains); i++ {
		result, err := c.AnalyzeContinuity(ctx, chains[i-1], chains[i], config)
		if err != nil {
			log.Printf("WARN: Continuity analysis failed for chains %s -> %s: %v", chains[i-1].ChainID, chains[i].ChainID, err)
			result = &ContinuityResult{IsContinuous: false, Confidence: 0.3, Reasoning: fmt.Sprintf("Analysis failed: %v", err), AnalysisMethod: "error_fallback"}
		}
		results = append(results, result)
	}

	return results, nil
}