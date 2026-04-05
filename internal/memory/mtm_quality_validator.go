package memory

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
)

// QualityValidator assesses and validates the quality of a cognitive chain before storage
type QualityValidator struct {
	config QualityConfig
}

// ValidationMode defines different quality validation approaches
type ValidationMode string

const (
	ValidationModePermissive ValidationMode = "permissive"
	ValidationModeBalanced   ValidationMode = "balanced"
	ValidationModeStrict     ValidationMode = "strict"
)

// QualityConfig holds configuration for quality validation
type QualityConfig struct {
	Mode                  ValidationMode
	MinQualityScore       float64
	CoherenceWeight       float64
	CompletenessWeight    float64
	RelevanceWeight       float64
	MinInteractionLength  int
	MaxSummaryLength      int
	RequireUserEngagement bool
}

// QualityMetrics represents a comprehensive quality assessment of a cognitive chain
type QualityMetrics struct {
	OverallScore         float64   `json:"overall_score" bson:"overall_score"`
	CoherenceScore       float64   `json:"coherence_score" bson:"coherence_score"`
	CompletenessScore    float64   `json:"completeness_score" bson:"completeness_score"`
	RelevanceScore       float64   `json:"relevance_score" bson:"relevance_score"`
	EngagementScore      float64   `json:"engagement_score" bson:"engagement_score"`
	CognitiveDepthScore  float64   `json:"cognitive_depth_score" bson:"cognitive_depth_score"`
	HasValidSummary      bool      `json:"has_valid_summary" bson:"has_valid_summary"`
	HasUserQuestions     bool      `json:"has_user_questions" bson:"has_user_questions"`
	HasDetailedAnswers   bool      `json:"has_detailed_answers" bson:"has_detailed_answers"`
	IsMultiTurn          bool      `json:"is_multi_turn" bson:"is_multi_turn"`
	HasThoughts          bool      `json:"has_thoughts" bson:"has_thoughts"`
	HasActions           bool      `json:"has_actions" bson:"has_actions"`
	ThoughtCount         int       `json:"thought_count" bson:"thought_count"`
	ActionCount          int       `json:"action_count" bson:"action_count"`
	TotalWordCount       int       `json:"total_word_count" bson:"total_word_count"`
	AverageMessageLength int       `json:"average_message_length" bson:"average_message_length"`
	TopicKeywords        []string  `json:"topic_keywords" bson:"topic_keywords"`
	ValidationTime       time.Time `json:"validation_time" bson:"validation_time"`
	QualityIssues        []string  `json:"quality_issues,omitempty" bson:"quality_issues,omitempty"`
}

// ValidationResult represents the outcome of quality validation
type ValidationResult struct {
	IsValid        bool
	QualityScore   float64
	Metrics        *QualityMetrics
	Recommendation string
	ShouldStore    bool
	ShouldImprove  bool
}

// NewQualityValidator creates a new quality validator
func NewQualityValidator() *QualityValidator {
	return &QualityValidator{
		config: getDefaultQualityConfig(),
	}
}

// ValidateSegment performs comprehensive quality validation on a cognitive chain and its events
func (qv *QualityValidator) ValidateSegment(ctx context.Context, chain *models.CognitiveChain, events []models.CognitiveEvent) (*ValidationResult, error) {
	start := time.Now()
	log.Printf("INFO: Validating chain quality for %s (%d events)", chain.ChainID, len(events))

	metrics, err := qv.calculateQualityMetrics(chain, events)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate quality metrics: %w", err)
	}

	isValid := metrics.OverallScore >= qv.config.MinQualityScore
	recommendation := qv.generateRecommendation(metrics)

	result := &ValidationResult{
		IsValid:        isValid,
		QualityScore:   metrics.OverallScore,
		Metrics:        metrics,
		Recommendation: recommendation,
		ShouldStore:    qv.shouldStoreSegment(metrics),
		ShouldImprove:  qv.shouldImproveSegment(metrics),
	}

	duration := time.Since(start)
	log.Printf("INFO: Quality validation completed for %s - Score: %.3f, Valid: %t, Duration: %v",
		chain.ChainID, metrics.OverallScore, isValid, duration)

	return result, nil
}

// calculateQualityMetrics computes comprehensive quality metrics for a chain and its events
func (qv *QualityValidator) calculateQualityMetrics(chain *models.CognitiveChain, events []models.CognitiveEvent) (*QualityMetrics, error) {
	// Analyze cognitive event types
	thoughtCount, actionCount := qv.analyzeCognitiveEventTypes(events)

	metrics := &QualityMetrics{
		ValidationTime:       time.Now(),
		TotalWordCount:       qv.calculateTotalWordCount(events),
		AverageMessageLength: qv.calculateAverageMessageLength(events),
		TopicKeywords:        qv.extractTopicKeywords(chain, events),
		HasValidSummary:      qv.hasValidSummary(chain),
		HasUserQuestions:     qv.hasUserQuestions(events),
		HasDetailedAnswers:   qv.hasDetailedAnswers(events),
		IsMultiTurn:          len(events) > 1,
		HasThoughts:          thoughtCount > 0,
		HasActions:           actionCount > 0,
		ThoughtCount:         thoughtCount,
		ActionCount:          actionCount,
	}

	metrics.CoherenceScore = qv.calculateCoherence(chain, events)
	metrics.CompletenessScore = qv.calculateCompleteness(chain, events)
	metrics.RelevanceScore = qv.calculateRelevance(chain, events)
	metrics.EngagementScore = qv.calculateEngagement(events)
	metrics.CognitiveDepthScore = qv.calculateCognitiveDepth(events, metrics)
	metrics.OverallScore = qv.calculateOverallScore(metrics)
	metrics.QualityIssues = qv.identifyQualityIssues(metrics, events)

	return metrics, nil
}

// Helper methods for quality assessment, now using CognitiveEvent
func (qv *QualityValidator) calculateTotalWordCount(events []models.CognitiveEvent) int {
	totalWords := 0
	for _, event := range events {
		totalWords += len(strings.Fields(event.Content))
	}
	return totalWords
}

func (qv *QualityValidator) calculateAverageMessageLength(events []models.CognitiveEvent) int {
	if len(events) == 0 {
		return 0
	}
	totalLength, messageCount := 0, 0
	for _, event := range events {
		if event.Content != "" {
			totalLength += len(event.Content)
			messageCount++
		}
	}
	if messageCount == 0 {
		return 0
	}
	return totalLength / messageCount
}

func (qv *QualityValidator) extractTopicKeywords(chain *models.CognitiveChain, events []models.CognitiveEvent) []string {
	wordCount := make(map[string]int)
	allText := chain.Summary
	for _, event := range events {
		allText += " " + event.Content
	}

	words := strings.Fields(strings.ToLower(allText))
	for _, word := range words {
		if len(word) > 3 && !qv.isStopWord(word) {
			wordCount[word]++
		}
	}

	var keywords []string
	for word, count := range wordCount {
		if count >= 2 && len(keywords) < 5 {
			keywords = append(keywords, word)
		}
	}
	return keywords
}

func (qv *QualityValidator) hasValidSummary(chain *models.CognitiveChain) bool {
	summary := chain.Summary
	return summary != "" && len(summary) > 10 && len(summary) < qv.config.MaxSummaryLength
}

func (qv *QualityValidator) hasUserQuestions(events []models.CognitiveEvent) bool {
	for _, event := range events {
		if event.Role == "user" && qv.containsQuestion(event.Content) {
			return true
		}
	}
	return false
}

func (qv *QualityValidator) hasDetailedAnswers(events []models.CognitiveEvent) bool {
	for _, event := range events {
		if event.Role == "agent" && len(event.Content) > 100 {
			return true
		}
	}
	return false
}

// analyzeCognitiveEventTypes counts thought and action events
func (qv *QualityValidator) analyzeCognitiveEventTypes(events []models.CognitiveEvent) (thoughtCount, actionCount int) {
	for _, event := range events {
		switch event.Type {
		case models.STMEventTypeThought:
			thoughtCount++
		case models.STMEventTypeAction:
			actionCount++
		}
	}
	return thoughtCount, actionCount
}

// calculateCognitiveDepth assesses the depth of cognitive processing
func (qv *QualityValidator) calculateCognitiveDepth(events []models.CognitiveEvent, metrics *QualityMetrics) float64 {
	if len(events) == 0 {
		return 0.0
	}

	// Base score starts at 0.5 for any conversation
	score := 0.5

	// Bonus for presence of thought events (indicates reasoning)
	if metrics.HasThoughts {
		thoughtRatio := float64(metrics.ThoughtCount) / float64(len(events))
		score += 0.2 * thoughtRatio // Up to +0.2 for high thought density
	}

	// Bonus for presence of action events (indicates tool use/execution)
	if metrics.HasActions {
		actionRatio := float64(metrics.ActionCount) / float64(len(events))
		score += 0.15 * actionRatio // Up to +0.15 for high action density
	}

	// Bonus for balanced cognitive processing (both thoughts and actions)
	if metrics.HasThoughts && metrics.HasActions {
		score += 0.15 // Balanced reasoning and execution
	}

	// Cap score at 1.0
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// Other helper functions (calculateCoherence, calculateCompleteness, etc.) would be similarly refactored.
// This example provides the core structure and refactoring of the main validation logic.
// Due to the complexity, a full line-by-line refactoring of every helper is omitted for brevity,
// but the pattern of using `events []models.CognitiveEvent` is established.

// calculateCoherence, calculateCompleteness, calculateRelevance, calculateEngagement, etc.
// would need to be updated to iterate over `events` instead of `pages` and use `event.Content`, `event.Role`, etc.

// Quality scoring implementations
func (qv *QualityValidator) calculateCoherence(_ *models.CognitiveChain, events []models.CognitiveEvent) float64 {
	if len(events) <= 1 {
		// Single event interactions have no coherence to measure
		return 0.5
	}
	// For multi-turn, check if messages build on each other
	// Higher score for longer, more substantive conversations
	totalWords := qv.calculateTotalWordCount(events)
	if totalWords < 10 {
		return 0.3
	} else if totalWords < 30 {
		return 0.6
	}
	return 0.8
}

func (qv *QualityValidator) calculateCompleteness(_ *models.CognitiveChain, events []models.CognitiveEvent) float64 {
	// Check for question-answer pairs and detailed content
	hasQuestion := qv.hasUserQuestions(events)
	hasAnswer := qv.hasDetailedAnswers(events)

	if !hasQuestion && !hasAnswer {
		return 0.3 // Neither question nor answer
	}
	if hasQuestion && !hasAnswer {
		return 0.5 // Question without detailed answer
	}
	if hasQuestion && hasAnswer {
		return 0.9 // Complete Q&A
	}
	return 0.6 // Answer without question
}

func (qv *QualityValidator) calculateRelevance(chain *models.CognitiveChain, events []models.CognitiveEvent) float64 {
	// Check for substantive content vs trivial greetings
	totalWords := qv.calculateTotalWordCount(events)
	keywordCount := len(qv.extractTopicKeywords(chain, events))

	if totalWords < 5 {
		return 0.2 // Very brief, likely trivial
	}
	if keywordCount == 0 {
		return 0.4 // No meaningful keywords
	}
	if keywordCount < 3 {
		return 0.6
	}
	return 0.9
}

func (qv *QualityValidator) calculateEngagement(events []models.CognitiveEvent) float64 {
	// Assess multi-turn interactions and question complexity
	if len(events) <= 1 {
		return 0.3 // Single turn, low engagement
	}
	if len(events) == 2 {
		return 0.5 // Simple back-and-forth
	}
	if len(events) <= 4 {
		return 0.7 // Moderate conversation
	}
	return 0.9 // Extended conversation
}

func (qv *QualityValidator) calculateOverallScore(metrics *QualityMetrics) float64 {
	// Reserve 15% weight for cognitive depth, distribute remaining weight to engagement
	cognitiveDepthWeight := 0.15
	engagementWeight := 1.0 - qv.config.CoherenceWeight - qv.config.CompletenessWeight - qv.config.RelevanceWeight - cognitiveDepthWeight

	return qv.config.CoherenceWeight*metrics.CoherenceScore +
		qv.config.CompletenessWeight*metrics.CompletenessScore +
		qv.config.RelevanceWeight*metrics.RelevanceScore +
		engagementWeight*metrics.EngagementScore +
		cognitiveDepthWeight*metrics.CognitiveDepthScore
}

func (qv *QualityValidator) identifyQualityIssues(metrics *QualityMetrics, _ []models.CognitiveEvent) []string {
	var issues []string
	if metrics.CoherenceScore < 0.5 {
		issues = append(issues, "Low topic coherence")
	}
	if metrics.CompletenessScore < 0.4 {
		issues = append(issues, "Incomplete information")
	}
	if metrics.CognitiveDepthScore < 0.5 {
		issues = append(issues, "Low cognitive depth - limited reasoning or action")
	}
	if !metrics.HasValidSummary {
		issues = append(issues, "Invalid or missing summary")
	}
	if metrics.TotalWordCount < qv.config.MinInteractionLength {
		issues = append(issues, "Interaction too brief")
	}
	return issues
}

func (qv *QualityValidator) generateRecommendation(metrics *QualityMetrics) string {
	if metrics.OverallScore >= 0.8 {
		return "High quality, excellent for storage"
	}
	if metrics.OverallScore >= 0.6 {
		return "Good quality, suitable for storage"
	}
	if metrics.OverallScore >= 0.4 {
		return "Moderate quality, consider for improvement"
	}
	return "Low quality, requires significant improvement"
}

func (qv *QualityValidator) shouldStoreSegment(metrics *QualityMetrics) bool {
	return metrics.OverallScore >= qv.config.MinQualityScore
}

func (qv *QualityValidator) shouldImproveSegment(metrics *QualityMetrics) bool {
	return metrics.OverallScore < 0.7 && metrics.OverallScore >= qv.config.MinQualityScore
}

// Utility functions
func (qv *QualityValidator) containsQuestion(text string) bool {
	return strings.Contains(strings.ToLower(text), "?") || strings.HasPrefix(strings.ToLower(text), "what") || strings.HasPrefix(strings.ToLower(text), "how")
}

func (qv *QualityValidator) isStopWord(word string) bool {
	stopWords := map[string]bool{"the": true, "a": true, "and": true, "is": true, "in": true, "it": true, "to": true, "i": true, "you": true}
	return stopWords[word]
}

func getDefaultQualityConfig() QualityConfig {
	mode := parseValidationMode("QUALITY_VALIDATION_MODE", string(ValidationModeBalanced))
	var minScore float64
	switch mode {
	case ValidationModePermissive:
		minScore = parseFloatEnv("QUALITY_MIN_SCORE_PERMISSIVE", 0.4)
	case ValidationModeBalanced:
		minScore = parseFloatEnv("QUALITY_MIN_SCORE_BALANCED", 0.5)
	case ValidationModeStrict:
		minScore = parseFloatEnv("QUALITY_MIN_SCORE_STRICT", 0.7)
	}
	return QualityConfig{
		Mode:                  mode,
		MinQualityScore:       minScore,
		CoherenceWeight:       parseFloatEnv("QUALITY_COHERENCE_WEIGHT", 0.3),
		CompletenessWeight:    parseFloatEnv("QUALITY_COMPLETENESS_WEIGHT", 0.3),
		RelevanceWeight:       parseFloatEnv("QUALITY_RELEVANCE_WEIGHT", 0.25),
		MinInteractionLength:  parseIntEnv("QUALITY_MIN_INTERACTION_LENGTH", 50),
		MaxSummaryLength:      parseIntEnv("QUALITY_MAX_SUMMARY_LENGTH", 1000),
		RequireUserEngagement: parseBoolEnv("QUALITY_REQUIRE_USER_ENGAGEMENT", false),
	}
}

func parseValidationMode(key, defaultValue string) ValidationMode {
	if v := os.Getenv(key); v != "" {
		switch ValidationMode(v) {
		case ValidationModePermissive, ValidationModeBalanced, ValidationModeStrict:
			return ValidationMode(v)
		}
	}
	return ValidationMode(defaultValue)
}
