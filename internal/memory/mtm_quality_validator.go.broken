package memory

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
)

// Helper function for max of two integers (moved utility functions to avoid conflicts)
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// QualityValidator assesses and validates segment quality before storage
type QualityValidator struct {
	config QualityConfig
}

// ValidationMode defines different quality validation approaches
type ValidationMode string

const (
	ValidationModePermissive ValidationMode = "permissive" // Learning phase - lenient scoring
	ValidationModeBalanced   ValidationMode = "balanced"   // Production default - balanced scoring
	ValidationModeStrict     ValidationMode = "strict"     // High-quality only - strict scoring
)

// QualityConfig holds configuration for quality validation
type QualityConfig struct {
	Mode                  ValidationMode // Validation mode
	MinQualityScore       float64        // Minimum score to accept a segment
	CoherenceWeight       float64        // Weight for topic coherence
	CompletenessWeight    float64        // Weight for information completeness
	RelevanceWeight       float64        // Weight for user relevance
	MinInteractionLength  int            // Minimum total interaction length
	MaxSummaryLength      int            // Maximum allowed summary length
	RequireUserEngagement bool           // Whether user engagement is required
}

// QualityMetrics represents comprehensive quality assessment
type QualityMetrics struct {
	OverallScore      float64 `json:"overall_score" bson:"overall_score"`
	CoherenceScore    float64 `json:"coherence_score" bson:"coherence_score"`
	CompletenessScore float64 `json:"completeness_score" bson:"completeness_score"`
	RelevanceScore    float64 `json:"relevance_score" bson:"relevance_score"`
	EngagementScore   float64 `json:"engagement_score" bson:"engagement_score"`

	// Quality indicators
	HasValidSummary    bool `json:"has_valid_summary" bson:"has_valid_summary"`
	HasUserQuestions   bool `json:"has_user_questions" bson:"has_user_questions"`
	HasDetailedAnswers bool `json:"has_detailed_answers" bson:"has_detailed_answers"`
	IsMultiTurn        bool `json:"is_multi_turn" bson:"is_multi_turn"`

	// Metadata
	TotalWordCount       int       `json:"total_word_count" bson:"total_word_count"`
	AverageMessageLength int       `json:"average_message_length" bson:"average_message_length"`
	TopicKeywords        []string  `json:"topic_keywords" bson:"topic_keywords"`
	ValidationTime       time.Time `json:"validation_time" bson:"validation_time"`

	// Issues found
	QualityIssues []string `json:"quality_issues,omitempty" bson:"quality_issues,omitempty"`
}

// ValidationResult represents the outcome of quality validation
type ValidationResult struct {
	IsValid        bool            `json:"is_valid"`
	QualityScore   float64         `json:"quality_score"`
	Metrics        *QualityMetrics `json:"metrics"`
	Recommendation string          `json:"recommendation"`
	ShouldStore    bool            `json:"should_store"`
	ShouldImprove  bool            `json:"should_improve"`
}

// NewQualityValidator creates a new quality validator
func NewQualityValidator() *QualityValidator {
	return &QualityValidator{
		config: getDefaultQualityConfig(),
	}
}

// parseValidationMode reads validation mode from environment
func parseValidationMode(key, defaultValue string) ValidationMode {
	if v := os.Getenv(key); v != "" {
		switch ValidationMode(v) {
		case ValidationModePermissive, ValidationModeBalanced, ValidationModeStrict:
			return ValidationMode(v)
		}
	}
	return ValidationMode(defaultValue)
}

// getDefaultQualityConfig returns default quality validation configuration
func getDefaultQualityConfig() QualityConfig {
	mode := parseValidationMode("QUALITY_VALIDATION_MODE", string(ValidationModeBalanced))

	// Mode-specific minimum scores
	var minScore float64
	switch mode {
	case ValidationModePermissive:
		minScore = parseFloatEnv("QUALITY_MIN_SCORE_PERMISSIVE", 0.4)
	case ValidationModeBalanced:
		minScore = parseFloatEnv("QUALITY_MIN_SCORE_BALANCED", 0.5)
	case ValidationModeStrict:
		minScore = parseFloatEnv("QUALITY_MIN_SCORE_STRICT", 0.7)
	default:
		minScore = parseFloatEnv("QUALITY_MIN_SCORE", 0.5)
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

// ValidateSegment performs comprehensive quality validation on a segment
func (qv *QualityValidator) ValidateSegment(ctx context.Context, segment *models.Segment, pages []models.DialoguePage) (*ValidationResult, error) {
	start := time.Now()

	log.Printf("INFO: Validating segment quality for %s (%d pages)", segment.SegmentID, len(pages))

	// Calculate quality metrics
	metrics, err := qv.calculateQualityMetrics(segment, pages)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate quality metrics: %w", err)
	}

	// Determine overall validation result
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
		segment.SegmentID, metrics.OverallScore, isValid, duration)

	return result, nil
}

// calculateQualityMetrics computes comprehensive quality metrics
func (qv *QualityValidator) calculateQualityMetrics(segment *models.Segment, pages []models.DialoguePage) (*QualityMetrics, error) {
	metrics := &QualityMetrics{
		ValidationTime: time.Now(),
	}

	// Basic statistics
	metrics.TotalWordCount = qv.calculateTotalWordCount(pages)
	metrics.AverageMessageLength = qv.calculateAverageMessageLength(pages)
	metrics.TopicKeywords = qv.extractTopicKeywords(segment, pages)

	// Quality indicators
	metrics.HasValidSummary = qv.hasValidSummary(segment)
	metrics.HasUserQuestions = qv.hasUserQuestions(pages)
	metrics.HasDetailedAnswers = qv.hasDetailedAnswers(pages)
	metrics.IsMultiTurn = len(pages) > 1

	// Calculate individual quality scores
	metrics.CoherenceScore = qv.calculateCoherence(segment, pages)
	metrics.CompletenessScore = qv.calculateCompleteness(segment, pages)
	metrics.RelevanceScore = qv.calculateRelevance(segment, pages)
	metrics.EngagementScore = qv.calculateEngagement(pages)

	// Calculate overall score
	metrics.OverallScore = qv.calculateOverallScore(metrics)

	// Identify quality issues
	metrics.QualityIssues = qv.identifyQualityIssues(metrics, pages)

	return metrics, nil
}

// calculateCoherence assesses topic coherence within the segment
func (qv *QualityValidator) calculateCoherence(segment *models.Segment, pages []models.DialoguePage) float64 {
	if len(pages) <= 1 {
		return 1.0 // Single page is perfectly coherent
	}

	coherenceScore := 0.8 // Base coherence

	// Check summary quality
	if segment.TopicSummary != "" {
		summaryLength := len(segment.TopicSummary)
		if summaryLength > 20 && summaryLength < 200 {
			coherenceScore += 0.1 // Good summary length
		} else if summaryLength > 500 {
			coherenceScore -= 0.2 // Too verbose
		}
	} else {
		coherenceScore -= 0.3 // No summary hurts coherence
	}

	// Check topic consistency across pages
	topicWords := qv.extractCommonWords(pages)
	if len(topicWords) > 2 {
		coherenceScore += 0.1 // Common vocabulary indicates coherence
	}

	// Check for topic drift (simplified heuristic)
	if qv.detectTopicDrift(pages) {
		coherenceScore -= 0.2
	}

	return math.Max(0.0, math.Min(1.0, coherenceScore))
}

// calculateCompleteness assesses information completeness
func (qv *QualityValidator) calculateCompleteness(segment *models.Segment, pages []models.DialoguePage) float64 {
	// Context-aware base score based on validation mode
	baseScore := qv.getContextualBaseScore(pages)

	// Apply smart penalties for problematic content
	penalties := qv.calculateSmartPenalties(segment, pages)

	completenessScore := baseScore - penalties

	// Check for question-answer pairs
	questionAnswerPairs := 0
	for _, page := range pages {
		if qv.containsQuestion(page.UserMessage) && len(page.AgentResponse) > 20 {
			questionAnswerPairs++
		}
	}

	if questionAnswerPairs > 0 {
		completenessScore += 0.2 * float64(questionAnswerPairs) / float64(len(pages))
	}

	// Check interaction depth
	totalLength := 0
	for _, page := range pages {
		totalLength += len(page.UserMessage) + len(page.AgentResponse)
	}

	if totalLength >= qv.config.MinInteractionLength {
		completenessScore += 0.2
	}

	// Check for detailed responses
	detailedResponses := 0
	for _, page := range pages {
		if len(page.AgentResponse) > 100 {
			detailedResponses++
		}
	}

	if detailedResponses > 0 {
		completenessScore += 0.1 * float64(detailedResponses) / float64(len(pages))
	}

	return math.Max(0.0, math.Min(1.0, completenessScore))
}

// calculateRelevance assesses user relevance
func (qv *QualityValidator) calculateRelevance(segment *models.Segment, pages []models.DialoguePage) float64 {
	relevanceScore := 0.6 // Base relevance

	// Check for important topics
	importantKeywords := []string{
		"help", "problem", "question", "how", "what", "why", "when", "where",
		"error", "issue", "bug", "troubleshoot", "explain", "tutorial",
		"urgent", "important", "critical", "deadline",
	}

	keywordHits := 0
	totalWords := 0

	for _, page := range pages {
		words := strings.Fields(strings.ToLower(page.UserMessage + " " + page.AgentResponse))
		totalWords += len(words)

		for _, word := range words {
			for _, keyword := range importantKeywords {
				if word == keyword {
					keywordHits++
					break
				}
			}
		}
	}

	if totalWords > 0 {
		keywordDensity := float64(keywordHits) / float64(totalWords)
		relevanceScore += keywordDensity * 0.3
	}

	// Boost for multi-turn conversations (more engaged users)
	if len(pages) > 2 {
		relevanceScore += 0.1
	}

	return math.Max(0.0, math.Min(1.0, relevanceScore))
}

// calculateEngagement assesses user engagement level
func (qv *QualityValidator) calculateEngagement(pages []models.DialoguePage) float64 {
	if len(pages) == 0 {
		return 0.0
	}

	engagementScore := 0.4 // Base engagement

	// Multi-turn bonus
	if len(pages) > 1 {
		engagementScore += 0.2
	}

	// Question complexity bonus
	complexQuestions := 0
	for _, page := range pages {
		if qv.isComplexQuestion(page.UserMessage) {
			complexQuestions++
		}
	}

	if complexQuestions > 0 {
		engagementScore += 0.2 * float64(complexQuestions) / float64(len(pages))
	}

	// Follow-up question bonus
	followUps := 0
	for i := 1; i < len(pages); i++ {
		if qv.isFollowUpQuestion(pages[i].UserMessage, pages[i-1].AgentResponse) {
			followUps++
		}
	}

	if followUps > 0 {
		engagementScore += 0.1
	}

	return math.Max(0.0, math.Min(1.0, engagementScore))
}

// calculateOverallScore combines individual scores into overall quality score
func (qv *QualityValidator) calculateOverallScore(metrics *QualityMetrics) float64 {
	engagementWeight := 1.0 - qv.config.CoherenceWeight - qv.config.CompletenessWeight - qv.config.RelevanceWeight

	overallScore := qv.config.CoherenceWeight*metrics.CoherenceScore +
		qv.config.CompletenessWeight*metrics.CompletenessScore +
		qv.config.RelevanceWeight*metrics.RelevanceScore +
		engagementWeight*metrics.EngagementScore

	return math.Max(0.0, math.Min(1.0, overallScore))
}

// Helper methods for quality assessment
func (qv *QualityValidator) calculateTotalWordCount(pages []models.DialoguePage) int {
	totalWords := 0
	for _, page := range pages {
		words := strings.Fields(page.UserMessage + " " + page.AgentResponse)
		totalWords += len(words)
	}
	return totalWords
}

func (qv *QualityValidator) calculateAverageMessageLength(pages []models.DialoguePage) int {
	if len(pages) == 0 {
		return 0
	}

	totalLength := 0
	messageCount := 0

	for _, page := range pages {
		if page.UserMessage != "" {
			totalLength += len(page.UserMessage)
			messageCount++
		}
		if page.AgentResponse != "" {
			totalLength += len(page.AgentResponse)
			messageCount++
		}
	}

	if messageCount == 0 {
		return 0
	}

	return totalLength / messageCount
}

func (qv *QualityValidator) extractTopicKeywords(segment *models.Segment, pages []models.DialoguePage) []string {
	wordCount := make(map[string]int)

	// Count words from all content
	allText := segment.TopicSummary
	for _, page := range pages {
		allText += " " + page.UserMessage + " " + page.AgentResponse
	}

	words := strings.Fields(strings.ToLower(allText))
	for _, word := range words {
		if len(word) > 3 && !qv.isStopWord(word) {
			wordCount[word]++
		}
	}

	// Get top keywords
	var keywords []string
	for word, count := range wordCount {
		if count >= 2 && len(keywords) < 5 {
			keywords = append(keywords, word)
		}
	}

	return keywords
}

func (qv *QualityValidator) hasValidSummary(segment *models.Segment) bool {
	summary := segment.TopicSummary
	return summary != "" && len(summary) > 10 && len(summary) < qv.config.MaxSummaryLength
}

func (qv *QualityValidator) hasUserQuestions(pages []models.DialoguePage) bool {
	for _, page := range pages {
		if qv.containsQuestion(page.UserMessage) {
			return true
		}
	}
	return false
}

func (qv *QualityValidator) hasDetailedAnswers(pages []models.DialoguePage) bool {
	for _, page := range pages {
		if len(page.AgentResponse) > 100 {
			return true
		}
	}
	return false
}

func (qv *QualityValidator) containsQuestion(text string) bool {
	text = strings.ToLower(text)
	questionWords := []string{"how", "what", "why", "when", "where", "who", "which", "can", "could", "would", "should"}

	for _, word := range questionWords {
		if strings.Contains(text, word) {
			return true
		}
	}

	return strings.Contains(text, "?")
}

func (qv *QualityValidator) isComplexQuestion(text string) bool {
	text = strings.ToLower(text)

	// Check for complexity indicators
	complexityIndicators := []string{
		"explain", "describe", "compare", "analyze", "evaluate",
		"implement", "design", "optimize", "troubleshoot",
		"difference between", "how do i", "what is the best way",
	}

	for _, indicator := range complexityIndicators {
		if strings.Contains(text, indicator) {
			return true
		}
	}

	// Check length as complexity indicator
	return len(text) > 50
}

func (qv *QualityValidator) isFollowUpQuestion(currentQuestion, previousAnswer string) bool {
	current := strings.ToLower(currentQuestion)

	followUpIndicators := []string{
		"also", "and", "but", "however", "what about", "how about",
		"can you also", "additionally", "furthermore", "moreover",
	}

	for _, indicator := range followUpIndicators {
		if strings.Contains(current, indicator) {
			return true
		}
	}

	return false
}

func (qv *QualityValidator) extractCommonWords(pages []models.DialoguePage) []string {
	if len(pages) <= 1 {
		return []string{}
	}

	wordCounts := make(map[string]int)

	for _, page := range pages {
		words := strings.Fields(strings.ToLower(page.UserMessage + " " + page.AgentResponse))
		uniqueWords := make(map[string]bool)

		for _, word := range words {
			if len(word) > 3 && !qv.isStopWord(word) && !uniqueWords[word] {
				wordCounts[word]++
				uniqueWords[word] = true
			}
		}
	}

	var commonWords []string
	minOccurrences := max(2, len(pages)/2) // Word must appear in at least half the pages

	for word, count := range wordCounts {
		if count >= minOccurrences {
			commonWords = append(commonWords, word)
		}
	}

	return commonWords
}

func (qv *QualityValidator) detectTopicDrift(pages []models.DialoguePage) bool {
	if len(pages) <= 2 {
		return false
	}

	// Simple topic drift detection: check if vocabulary changes significantly
	firstHalf := pages[:len(pages)/2]
	secondHalf := pages[len(pages)/2:]

	firstWords := qv.extractVocabulary(firstHalf)
	secondWords := qv.extractVocabulary(secondHalf)

	// Calculate overlap
	overlap := 0
	for word := range firstWords {
		if secondWords[word] {
			overlap++
		}
	}

	totalUnique := len(firstWords) + len(secondWords) - overlap
	if totalUnique > 0 {
		overlapRatio := float64(overlap) / float64(totalUnique)
		return overlapRatio < 0.3 // Less than 30% overlap suggests topic drift
	}

	return false
}

func (qv *QualityValidator) extractVocabulary(pages []models.DialoguePage) map[string]bool {
	vocab := make(map[string]bool)

	for _, page := range pages {
		words := strings.Fields(strings.ToLower(page.UserMessage + " " + page.AgentResponse))
		for _, word := range words {
			if len(word) > 3 && !qv.isStopWord(word) {
				vocab[word] = true
			}
		}
	}

	return vocab
}

func (qv *QualityValidator) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "and": true, "or": true, "but": true, "in": true, "on": true,
		"at": true, "to": true, "for": true, "of": true, "with": true, "by": true,
		"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
		"have": true, "has": true, "had": true, "do": true, "does": true, "did": true,
		"will": true, "would": true, "could": true, "should": true, "may": true,
		"might": true, "can": true, "a": true, "an": true, "this": true, "that": true,
		"these": true, "those": true,
	}

	return stopWords[word]
}

func (qv *QualityValidator) identifyQualityIssues(metrics *QualityMetrics, pages []models.DialoguePage) []string {
	var issues []string

	if metrics.CoherenceScore < 0.5 {
		issues = append(issues, "Low topic coherence - conversation may drift between topics")
	}

	if metrics.CompletenessScore < 0.4 {
		issues = append(issues, "Incomplete information - responses may lack detail")
	}

	if metrics.RelevanceScore < 0.5 {
		issues = append(issues, "Low relevance - conversation may not address important topics")
	}

	if metrics.EngagementScore < 0.3 {
		issues = append(issues, "Low user engagement - limited interaction depth")
	}

	if !metrics.HasValidSummary {
		issues = append(issues, "Invalid or missing topic summary")
	}

	if metrics.TotalWordCount < qv.config.MinInteractionLength {
		issues = append(issues, "Interaction too brief to be meaningful")
	}

	if !metrics.IsMultiTurn && qv.config.RequireUserEngagement {
		issues = append(issues, "Single-turn conversation lacks engagement depth")
	}

	return issues
}

func (qv *QualityValidator) generateRecommendation(metrics *QualityMetrics) string {
	if metrics.OverallScore >= 0.8 {
		return "High quality segment - excellent for long-term storage and retrieval"
	} else if metrics.OverallScore >= 0.6 {
		return "Good quality segment - suitable for storage with minor improvements possible"
	} else if metrics.OverallScore >= 0.4 {
		return "Moderate quality segment - consider improvements before storage"
	} else {
		return "Low quality segment - requires significant improvement or reconsideration"
	}
}

func (qv *QualityValidator) shouldStoreSegment(metrics *QualityMetrics) bool {
	return metrics.OverallScore >= qv.config.MinQualityScore
}

func (qv *QualityValidator) shouldImproveSegment(metrics *QualityMetrics) bool {
	return metrics.OverallScore < 0.7 && metrics.OverallScore >= qv.config.MinQualityScore
}

// getContextualBaseScore returns mode-specific base scores based on conversation context
func (qv *QualityValidator) getContextualBaseScore(pages []models.DialoguePage) float64 {
	// Empty conversations get no base score
	if len(pages) == 0 {
		return 0.0
	}

	// Single trivial interactions get minimal base score
	if len(pages) == 1 && qv.isTrivialInteraction(pages[0]) {
		switch qv.config.Mode {
		case ValidationModePermissive:
			return 0.3 // Still give some credit in learning mode
		case ValidationModeBalanced:
			return 0.1 // Minimal credit in production
		case ValidationModeStrict:
			return 0.0 // No credit for trivial interactions
		}
	}

	// Real conversations get mode-appropriate base scores
	switch qv.config.Mode {
	case ValidationModePermissive:
		return 0.5 // Your original approach - generous baseline
	case ValidationModeBalanced:
		return 0.2 // Must earn 80% of the score
	case ValidationModeStrict:
		return 0.0 // Must earn everything
	}

	return 0.2 // Default fallback
}

// calculateSmartPenalties applies intelligent penalties for low-quality content
func (qv *QualityValidator) calculateSmartPenalties(segment *models.Segment, pages []models.DialoguePage) float64 {
	penalties := 0.0

	// Severe penalty for truly empty conversations
	if len(pages) == 0 {
		penalties += 0.8
	}

	// Moderate penalty for greeting-only conversations
	if qv.isGreetingOnlyConversation(pages) {
		penalties += 0.4
	}

	// Light penalty for missing or very short topic summary
	if segment.TopicSummary == "" || len(strings.TrimSpace(segment.TopicSummary)) < 10 {
		penalties += 0.2
	}

	// Penalty for extremely short total interaction
	totalLength := qv.getTotalInteractionLength(pages)
	if totalLength < 20 {
		penalties += 0.3
	} else if totalLength < qv.config.MinInteractionLength {
		penalties += 0.1
	}

	return penalties
}

// isTrivialInteraction checks if a single interaction is trivial (greetings, very short)
func (qv *QualityValidator) isTrivialInteraction(page models.DialoguePage) bool {
	trivialPhrases := []string{"hi", "hello", "hey", "thanks", "thank you", "bye", "goodbye", "ok", "okay"}
	userMsg := strings.ToLower(strings.TrimSpace(page.UserMessage))

	// Check for exact trivial phrases
	for _, phrase := range trivialPhrases {
		if userMsg == phrase {
			return true
		}
	}

	// Check for very short interactions
	return len(userMsg) < 10 && len(strings.TrimSpace(page.AgentResponse)) < 30
}

// isGreetingOnlyConversation checks if the entire conversation is just greetings
func (qv *QualityValidator) isGreetingOnlyConversation(pages []models.DialoguePage) bool {
	if len(pages) == 0 {
		return false
	}

	// If all interactions are trivial, it's a greeting-only conversation
	for _, page := range pages {
		if !qv.isTrivialInteraction(page) {
			return false
		}
	}

	return true
}

// getTotalInteractionLength calculates total character count of the conversation
func (qv *QualityValidator) getTotalInteractionLength(pages []models.DialoguePage) int {
	totalLength := 0
	for _, page := range pages {
		totalLength += len(page.UserMessage) + len(page.AgentResponse)
	}
	return totalLength
}
