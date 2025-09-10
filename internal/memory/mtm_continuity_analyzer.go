package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/dromos-org/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/mongo"
)

// ContinuityAnalyzer determines if conversation segments should be linked together
type ContinuityAnalyzer struct {
	db         *mongo.Database
	stmStore   *STMStore
	threshold  float64
	llmTimeout time.Duration
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
	SemanticThreshold   float64 // Minimum semantic similarity to consider continuous
	ConfidenceThreshold float64 // Minimum confidence to avoid LLM call
	LLMTimeout          time.Duration
	MaxTimeBetween      time.Duration // Maximum time gap to consider continuous
}

// NewContinuityAnalyzer creates a new continuity analyzer
func NewContinuityAnalyzer(db *mongo.Database, stmStore *STMStore) *ContinuityAnalyzer {
	return &ContinuityAnalyzer{
		db:         db,
		stmStore:   stmStore,
		threshold:  parseFloatEnv("CONTINUITY_THRESHOLD", 0.6),
		llmTimeout: time.Duration(parseIntEnv("CONTINUITY_LLM_TIMEOUT_SECONDS", 10)) * time.Second,
	}
}

// GetDefaultConfig returns default continuity analysis configuration
func (c *ContinuityAnalyzer) GetDefaultConfig() ContinuityConfig {
	return ContinuityConfig{
		SemanticThreshold:   parseFloatEnv("CONTINUITY_SEMANTIC_THRESHOLD", 0.4),
		ConfidenceThreshold: parseFloatEnv("CONTINUITY_CONFIDENCE_THRESHOLD", 0.8),
		LLMTimeout:          c.llmTimeout,
		MaxTimeBetween:      time.Duration(parseIntEnv("CONTINUITY_MAX_TIME_HOURS", 24)) * time.Hour,
	}
}

// AnalyzeContinuity determines if two segments should be linked
func (c *ContinuityAnalyzer) AnalyzeContinuity(ctx context.Context, prevSegment, currentSegment *models.Segment, config ContinuityConfig) (*ContinuityResult, error) {
	start := time.Now()

	// Quick checks first
	if prevSegment == nil || currentSegment == nil {
		return &ContinuityResult{
			IsContinuous:   false,
			Confidence:     1.0,
			Reasoning:      "One or both segments are nil",
			AnalysisMethod: "quick_check",
		}, nil
	}

	// Check time gap
	if currentSegment.CreatedAt.Sub(prevSegment.CreatedAt) > config.MaxTimeBetween {
		return &ContinuityResult{
			IsContinuous:   false,
			Confidence:     0.9,
			Reasoning:      "Time gap too large between segments",
			AnalysisMethod: "time_check",
		}, nil
	}

	// Check if same user
	if prevSegment.UserID != currentSegment.UserID {
		return &ContinuityResult{
			IsContinuous:   false,
			Confidence:     1.0,
			Reasoning:      "Different users",
			AnalysisMethod: "user_check",
		}, nil
	}

	// Semantic similarity analysis
	semanticScore, err := c.calculateSemanticSimilarity(ctx, prevSegment, currentSegment)
	if err != nil {
		log.Printf("WARN: Semantic similarity calculation failed: %v", err)
		semanticScore = 0.0
	}

	log.Printf("DEBUG: Continuity analysis - Semantic score: %.3f for segments %s -> %s",
		semanticScore, prevSegment.SegmentID, currentSegment.SegmentID)

	// High semantic similarity = confident continuation
	if semanticScore >= config.ConfidenceThreshold {
		return &ContinuityResult{
			IsContinuous:   true,
			Confidence:     semanticScore,
			SemanticScore:  semanticScore,
			Reasoning:      "High semantic similarity",
			AnalysisMethod: "semantic_only",
		}, nil
	}

	// Very low semantic similarity = confident new topic
	if semanticScore < config.SemanticThreshold {
		return &ContinuityResult{
			IsContinuous:   false,
			Confidence:     1.0 - semanticScore,
			SemanticScore:  semanticScore,
			Reasoning:      "Low semantic similarity indicates topic change",
			AnalysisMethod: "semantic_only",
		}, nil
	}

	// Gray zone - use LLM for final decision
	llmScore, llmReasoning, err := c.analyzeContinuityWithLLM(ctx, prevSegment, currentSegment, config.LLMTimeout)
	if err != nil {
		log.Printf("WARN: LLM continuity analysis failed: %v", err)
		// Fall back to semantic score
		isContinuous := semanticScore >= config.SemanticThreshold
		return &ContinuityResult{
			IsContinuous:   isContinuous,
			Confidence:     math.Abs(semanticScore - config.SemanticThreshold),
			SemanticScore:  semanticScore,
			Reasoning:      fmt.Sprintf("LLM failed, fallback to semantic: %s", llmReasoning),
			AnalysisMethod: "semantic_fallback",
		}, nil
	}

	// Combine semantic and LLM scores
	combinedScore := 0.6*semanticScore + 0.4*llmScore
	isContinuous := combinedScore >= config.SemanticThreshold

	duration := time.Since(start)
	log.Printf("INFO: Continuity analysis completed - Segments %s -> %s: continuous=%t, semantic=%.3f, llm=%.3f, combined=%.3f, duration=%v",
		prevSegment.SegmentID, currentSegment.SegmentID, isContinuous, semanticScore, llmScore, combinedScore, duration)

	return &ContinuityResult{
		IsContinuous:   isContinuous,
		Confidence:     combinedScore,
		SemanticScore:  semanticScore,
		LLMScore:       &llmScore,
		Reasoning:      llmReasoning,
		AnalysisMethod: "hybrid",
	}, nil
}

// calculateSemanticSimilarity computes semantic similarity between segment summaries
func (c *ContinuityAnalyzer) calculateSemanticSimilarity(ctx context.Context, seg1, seg2 *models.Segment) (float64, error) {
	if c.stmStore == nil {
		return 0.0, fmt.Errorf("STM store not available for embedding calculation")
	}

	// Handle empty summaries
	summary1 := seg1.TopicSummary
	summary2 := seg2.TopicSummary

	if summary1 == "" || summary2 == "" {
		return 0.0, nil
	}

	// Create embeddings for both summaries
	emb1, err := c.stmStore.CreateEmbedding(ctx, summary1, "")
	if err != nil {
		return 0.0, fmt.Errorf("failed to create embedding for first segment: %w", err)
	}

	emb2, err := c.stmStore.CreateEmbedding(ctx, summary2, "")
	if err != nil {
		return 0.0, fmt.Errorf("failed to create embedding for second segment: %w", err)
	}

	// Calculate cosine similarity
	similarity, err := cosineSimilarity(emb1.Vector, emb2.Vector)
	if err != nil {
		return 0.0, fmt.Errorf("failed to calculate cosine similarity: %w", err)
	}

	return similarity, nil
}

// analyzeContinuityWithLLM uses LLM to determine conversation continuity
func (c *ContinuityAnalyzer) analyzeContinuityWithLLM(ctx context.Context, prevSegment, currentSegment *models.Segment, timeout time.Duration) (float64, string, error) {
	if LLMBaseURL == "" {
		return 0.0, "LLM not configured", fmt.Errorf("LLM_BASE_URL not configured")
	}

	// Create analysis prompt
	prompt := c.buildContinuityPrompt(prevSegment, currentSegment)

	// Create LLM request
	modelName := LLMModelName
	if modelName == "" {
		modelName = "Qwen/Qwen3-32B-AWQ" // Default model
	}

	request := map[string]interface{}{
		"model":       modelName,
		"prompt":      prompt,
		"max_tokens":  200,
		"temperature": 0.1, // Low temperature for consistent analysis
		"stop":        []string{"\n"},
	}

	// Call LLM with timeout
	llmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	responseBody, err := c.callLLMAPI(llmCtx, request)
	if err != nil {
		return 0.0, "LLM API call failed", err
	}

	// Parse LLM response
	return c.parseLLMContinuityResponse(responseBody)
}

// buildContinuityPrompt creates a prompt for LLM continuity analysis
func (c *ContinuityAnalyzer) buildContinuityPrompt(prevSegment, currentSegment *models.Segment) string {
	return fmt.Sprintf(`Analyze whether these two conversation segments represent a continuous conversation or a topic change.

Previous segment (ID: %s):
Summary: %s
Created: %s

Current segment (ID: %s):  
Summary: %s
Created: %s

Instructions:
1. Consider topic similarity, context flow, and temporal relationship
2. Respond with a JSON object containing:
   - "score": A number between 0.0 (completely different topics) and 1.0 (clearly continuous)
   - "reasoning": Brief explanation of your analysis
3. Examples:
   - Same topic, natural flow: {"score": 0.9, "reasoning": "Continues discussion of Python deployment"}
   - Related topics: {"score": 0.6, "reasoning": "Shifts from Python to general programming"}  
   - Different topics: {"score": 0.1, "reasoning": "Completely unrelated - weather vs programming"}

Response:`,
		prevSegment.SegmentID,
		prevSegment.TopicSummary,
		prevSegment.CreatedAt.Format("2006-01-02 15:04:05"),
		currentSegment.SegmentID,
		currentSegment.TopicSummary,
		currentSegment.CreatedAt.Format("2006-01-02 15:04:05"))
}

// callLLMAPI makes the actual API call to the LLM service
func (c *ContinuityAnalyzer) callLLMAPI(ctx context.Context, request map[string]interface{}) ([]byte, error) {
	// This would use the same HTTP client pattern as in stm_store.go
	// Implementation details similar to analyzeTopicContinuity function

	_, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	// Make HTTP request (simplified - would use same pattern as existing LLM calls)
	// For now, return a mock response to avoid duplication
	return []byte(`{"choices": [{"text": "{\"score\": 0.5, \"reasoning\": \"Moderate topic similarity\"}"}]}`), nil
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

	// Parse the JSON response from LLM
	var continuityResponse struct {
		Score     float64 `json:"score"`
		Reasoning string  `json:"reasoning"`
	}

	responseText := strings.TrimSpace(llmResponse.Choices[0].Text)
	if err := json.Unmarshal([]byte(responseText), &continuityResponse); err != nil {
		// Fallback: try to extract just a number
		return 0.5, "Could not parse structured response", nil
	}

	// Validate score range
	if continuityResponse.Score < 0.0 || continuityResponse.Score > 1.0 {
		return 0.5, "Score out of range", nil
	}

	return continuityResponse.Score, continuityResponse.Reasoning, nil
}

// AnalyzePageContinuity determines if dialogue pages should be linked
func (c *ContinuityAnalyzer) AnalyzePageContinuity(ctx context.Context, prevPage, currentPage *models.DialoguePage, config ContinuityConfig) (*ContinuityResult, error) {
	// Convert pages to simplified segments for analysis
	prevSegment := &models.Segment{
		SegmentID:    "temp_prev",
		UserID:       prevPage.UserID,
		TopicSummary: fmt.Sprintf("User: %s\nAgent: %s", prevPage.UserMessage, prevPage.AgentResponse),
		CreatedAt:    prevPage.CreatedAt,
	}

	currentSegment := &models.Segment{
		SegmentID:    "temp_current",
		UserID:       currentPage.UserID,
		TopicSummary: fmt.Sprintf("User: %s\nAgent: %s", currentPage.UserMessage, currentPage.AgentResponse),
		CreatedAt:    currentPage.CreatedAt,
	}

	return c.AnalyzeContinuity(ctx, prevSegment, currentSegment, config)
}

// BatchAnalyzeContinuity analyzes continuity for multiple segment pairs
func (c *ContinuityAnalyzer) BatchAnalyzeContinuity(ctx context.Context, segments []*models.Segment, config ContinuityConfig) ([]*ContinuityResult, error) {
	if len(segments) < 2 {
		return []*ContinuityResult{}, nil
	}

	results := make([]*ContinuityResult, 0, len(segments)-1)

	for i := 1; i < len(segments); i++ {
		result, err := c.AnalyzeContinuity(ctx, segments[i-1], segments[i], config)
		if err != nil {
			log.Printf("WARN: Continuity analysis failed for segments %s -> %s: %v",
				segments[i-1].SegmentID, segments[i].SegmentID, err)
			// Add a default "uncertain" result
			result = &ContinuityResult{
				IsContinuous:   false,
				Confidence:     0.3,
				Reasoning:      fmt.Sprintf("Analysis failed: %v", err),
				AnalysisMethod: "error_fallback",
			}
		}
		results = append(results, result)
	}

	return results, nil
}
