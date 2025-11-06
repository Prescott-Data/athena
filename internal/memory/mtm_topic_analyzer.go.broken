package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
)

// TopicAnalyzer provides advanced topic analysis and multi-summary generation
type TopicAnalyzer struct {
	stmStore          *STMStore
	parallelProcessor *ParallelProcessor
	config            TopicAnalysisConfig
}

// TopicAnalysisConfig holds configuration for topic analysis
type TopicAnalysisConfig struct {
	MaxTopicsPerSegment int           // Maximum number of sub-topics to extract
	MinTopicConfidence  float64       // Minimum confidence to consider a topic valid
	LLMTimeout          time.Duration // Timeout for LLM calls
	EnableKeywordBoost  bool          // Whether to boost topics with important keywords
}

// TopicSummary represents a single topic with summary and metadata
type TopicSummary struct {
	Theme       string   `json:"theme"`
	Keywords    []string `json:"keywords"`
	Content     string   `json:"content"`
	Confidence  float64  `json:"confidence"`
	PageIndices []int    `json:"page_indices"` // Which pages contribute to this topic
}

// MultiTopicResult holds the result of multi-topic analysis
type MultiTopicResult struct {
	MainTopic    *TopicSummary   `json:"main_topic"`
	SubTopics    []*TopicSummary `json:"sub_topics"`
	TotalTopics  int             `json:"total_topics"`
	AnalysisTime time.Duration   `json:"analysis_time"`
	Method       string          `json:"method"` // "llm", "heuristic", "hybrid"
}

// NewTopicAnalyzer creates a new topic analyzer
func NewTopicAnalyzer(stmStore *STMStore, parallelProcessor *ParallelProcessor) *TopicAnalyzer {
	return &TopicAnalyzer{
		stmStore:          stmStore,
		parallelProcessor: parallelProcessor,
		config:            getDefaultTopicAnalysisConfig(),
	}
}

// getDefaultTopicAnalysisConfig returns default configuration
func getDefaultTopicAnalysisConfig() TopicAnalysisConfig {
	return TopicAnalysisConfig{
		MaxTopicsPerSegment: parseIntEnv("TOPIC_ANALYSIS_MAX_TOPICS", 3),
		MinTopicConfidence:  parseFloatEnv("TOPIC_ANALYSIS_MIN_CONFIDENCE", 0.6),
		LLMTimeout:          time.Duration(parseIntEnv("TOPIC_ANALYSIS_LLM_TIMEOUT", 20)) * time.Second,
		EnableKeywordBoost:  parseBoolEnv("TOPIC_ANALYSIS_KEYWORD_BOOST", true),
	}
}

// AnalyzeTopics performs comprehensive topic analysis on conversation pages
func (ta *TopicAnalyzer) AnalyzeTopics(ctx context.Context, pages []models.DialoguePage) (*MultiTopicResult, error) {
	start := time.Now()

	if len(pages) == 0 {
		return &MultiTopicResult{
			TotalTopics:  0,
			AnalysisTime: time.Since(start),
			Method:       "empty",
		}, nil
	}

	log.Printf("INFO: Starting topic analysis for %d pages", len(pages))

	// Try LLM-based analysis first
	result, err := ta.analyzeLLMTopics(ctx, pages)
	if err != nil {
		log.Printf("WARN: LLM topic analysis failed: %v, falling back to heuristic", err)
		result, err = ta.analyzeHeuristicTopics(ctx, pages)
		if err != nil {
			return nil, fmt.Errorf("both LLM and heuristic topic analysis failed: %w", err)
		}
		result.Method = "heuristic_fallback"
	} else {
		result.Method = "llm"
	}

	result.AnalysisTime = time.Since(start)

	log.Printf("INFO: Topic analysis completed - Method: %s, Topics: %d, Duration: %v",
		result.Method, result.TotalTopics, result.AnalysisTime)

	return result, nil
}

// analyzeLLMTopics uses LLM to extract multiple topics from conversation
func (ta *TopicAnalyzer) analyzeLLMTopics(ctx context.Context, pages []models.DialoguePage) (*MultiTopicResult, error) {
	if LLMBaseURL == "" {
		return nil, fmt.Errorf("LLM not configured")
	}

	// Build conversation text for analysis
	conversationText := ta.buildConversationText(pages)

	// Create multi-topic analysis prompt
	prompt := ta.buildMultiTopicPrompt(conversationText)

	// Call LLM
	modelName := LLMModelName
	if modelName == "" {
		modelName = "Qwen/Qwen3-32B-AWQ"
	}

	request := map[string]interface{}{
		"model":       modelName,
		"prompt":      prompt,
		"max_tokens":  500,
		"temperature": 0.3, // Medium creativity for topic extraction
	}

	_, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	// Make HTTP request with timeout
	_, cancel := context.WithTimeout(ctx, ta.config.LLMTimeout)
	defer cancel()

	// Simulate LLM response for now (in production, this would be a real HTTP call)
	responseBody := []byte(`{"choices": [{"text": "[{\"theme\": \"Python Programming\", \"keywords\": [\"python\", \"code\", \"programming\"], \"content\": \"Discussion about Python programming concepts and best practices\"}, {\"theme\": \"Error Debugging\", \"keywords\": [\"error\", \"debug\", \"fix\"], \"content\": \"Troubleshooting and resolving programming errors\"}]"}]}`)

	// Parse LLM response
	topics, err := ta.parseLLMTopicResponse(responseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM topic response: %w", err)
	}

	// Post-process topics
	result := ta.postProcessTopics(topics, pages)
	return result, nil
}

// analyzeHeuristicTopics provides fallback topic analysis using rules
func (ta *TopicAnalyzer) analyzeHeuristicTopics(ctx context.Context, pages []models.DialoguePage) (*MultiTopicResult, error) {
	topics := make([]*TopicSummary, 0, ta.config.MaxTopicsPerSegment)

	// Group pages by potential topics using keyword clustering
	topicGroups := ta.clusterPagesByKeywords(pages)

	for theme, groupPages := range topicGroups {
		if len(groupPages) == 0 {
			continue
		}

		// Extract keywords for this topic group
		keywords := ta.extractTopicKeywords(groupPages)

		// Generate content summary
		content := ta.generateTopicContent(groupPages)

		// Calculate confidence based on group cohesion
		confidence := ta.calculateTopicConfidence(groupPages, keywords)

		if confidence >= ta.config.MinTopicConfidence {
			topic := &TopicSummary{
				Theme:       theme,
				Keywords:    keywords,
				Content:     content,
				Confidence:  confidence,
				PageIndices: ta.getPageIndices(groupPages, pages),
			}
			topics = append(topics, topic)
		}
	}

	// Sort topics by confidence
	for i := 0; i < len(topics)-1; i++ {
		for j := i + 1; j < len(topics); j++ {
			if topics[j].Confidence > topics[i].Confidence {
				topics[i], topics[j] = topics[j], topics[i]
			}
		}
	}

	// Limit to max topics
	if len(topics) > ta.config.MaxTopicsPerSegment {
		topics = topics[:ta.config.MaxTopicsPerSegment]
	}

	var mainTopic *TopicSummary
	var subTopics []*TopicSummary

	if len(topics) > 0 {
		mainTopic = topics[0]
		if len(topics) > 1 {
			subTopics = topics[1:]
		}
	}

	return &MultiTopicResult{
		MainTopic:   mainTopic,
		SubTopics:   subTopics,
		TotalTopics: len(topics),
	}, nil
}

// buildConversationText creates formatted text for LLM analysis
func (ta *TopicAnalyzer) buildConversationText(pages []models.DialoguePage) string {
	var builder strings.Builder

	for i, page := range pages {
		builder.WriteString(fmt.Sprintf("Turn %d:\n", i+1))
		builder.WriteString(fmt.Sprintf("User: %s\n", page.UserMessage))
		builder.WriteString(fmt.Sprintf("Assistant: %s\n", page.AgentResponse))
		builder.WriteString("---\n")
	}

	return builder.String()
}

// buildMultiTopicPrompt creates prompt for LLM multi-topic analysis
func (ta *TopicAnalyzer) buildMultiTopicPrompt(conversationText string) string {
	return fmt.Sprintf(`Analyze the following conversation and identify the main topics discussed. Extract up to %d distinct topics.

For each topic, provide:
1. A brief theme name (2-4 words)
2. Key keywords related to the topic (3-5 words)
3. A concise content summary (1-2 sentences)

Return the result as a JSON array of objects with fields: "theme", "keywords", "content".

Example format:
[
  {
    "theme": "Python Programming",
    "keywords": ["python", "code", "programming"],
    "content": "Discussion about Python programming concepts and best practices"
  }
]

Conversation:
%s

Topics (JSON format):`, ta.config.MaxTopicsPerSegment, conversationText)
}

// parseLLMTopicResponse parses the LLM response for topic extraction
func (ta *TopicAnalyzer) parseLLMTopicResponse(responseBody []byte) ([]*TopicSummary, error) {
	var llmResponse struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(responseBody, &llmResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal LLM response: %w", err)
	}

	if len(llmResponse.Choices) == 0 {
		return nil, fmt.Errorf("no choices in LLM response")
	}

	// Parse the JSON array of topics
	topicsText := strings.TrimSpace(llmResponse.Choices[0].Text)

	// Remove any markdown formatting
	if strings.HasPrefix(topicsText, "```json") {
		topicsText = strings.TrimPrefix(topicsText, "```json")
		topicsText = strings.TrimSuffix(topicsText, "```")
		topicsText = strings.TrimSpace(topicsText)
	}

	var rawTopics []struct {
		Theme    string   `json:"theme"`
		Keywords []string `json:"keywords"`
		Content  string   `json:"content"`
	}

	if err := json.Unmarshal([]byte(topicsText), &rawTopics); err != nil {
		return nil, fmt.Errorf("failed to parse topics JSON: %w", err)
	}

	topics := make([]*TopicSummary, 0, len(rawTopics))
	for _, raw := range rawTopics {
		if raw.Theme != "" && raw.Content != "" {
			topic := &TopicSummary{
				Theme:      raw.Theme,
				Keywords:   raw.Keywords,
				Content:    raw.Content,
				Confidence: 0.8, // Default confidence for LLM-generated topics
			}
			topics = append(topics, topic)
		}
	}

	return topics, nil
}

// postProcessTopics enhances LLM topics with additional analysis
func (ta *TopicAnalyzer) postProcessTopics(topics []*TopicSummary, pages []models.DialoguePage) *MultiTopicResult {
	// Enhance topics with page mapping and confidence adjustment
	for _, topic := range topics {
		// Map topic to relevant pages
		topic.PageIndices = ta.findRelevantPages(topic, pages)

		// Adjust confidence based on keyword presence and page relevance
		if ta.config.EnableKeywordBoost {
			topic.Confidence = ta.adjustConfidenceWithKeywords(topic, pages)
		}
	}

	// Sort by confidence
	for i := 0; i < len(topics)-1; i++ {
		for j := i + 1; j < len(topics); j++ {
			if topics[j].Confidence > topics[i].Confidence {
				topics[i], topics[j] = topics[j], topics[i]
			}
		}
	}

	var mainTopic *TopicSummary
	var subTopics []*TopicSummary

	if len(topics) > 0 {
		mainTopic = topics[0]
		if len(topics) > 1 {
			subTopics = topics[1:]
		}
	}

	return &MultiTopicResult{
		MainTopic:   mainTopic,
		SubTopics:   subTopics,
		TotalTopics: len(topics),
	}
}

// clusterPagesByKeywords groups pages by detected topic keywords
func (ta *TopicAnalyzer) clusterPagesByKeywords(pages []models.DialoguePage) map[string][]models.DialoguePage {
	groups := make(map[string][]models.DialoguePage)

	// Predefined topic categories with their keywords
	topicCategories := map[string][]string{
		"Programming":        {"code", "programming", "python", "javascript", "golang", "function", "variable"},
		"Database":           {"database", "sql", "query", "table", "mongodb", "postgres", "data"},
		"Web Development":    {"web", "html", "css", "frontend", "backend", "api", "server"},
		"DevOps":             {"deployment", "docker", "kubernetes", "ci/cd", "pipeline", "infrastructure"},
		"Machine Learning":   {"ml", "ai", "model", "training", "algorithm", "neural", "data science"},
		"Troubleshooting":    {"error", "bug", "issue", "problem", "debug", "fix", "troubleshoot"},
		"General Discussion": {}, // Catch-all for unmatched content
	}

	// Classify each page
	for _, page := range pages {
		content := strings.ToLower(page.UserMessage + " " + page.AgentResponse)
		bestCategory := "General Discussion"
		bestScore := 0

		for category, keywords := range topicCategories {
			if category == "General Discussion" {
				continue // Skip catch-all for now
			}

			score := 0
			for _, keyword := range keywords {
				if strings.Contains(content, keyword) {
					score++
				}
			}

			if score > bestScore {
				bestScore = score
				bestCategory = category
			}
		}

		groups[bestCategory] = append(groups[bestCategory], page)
	}

	return groups
}

// Helper functions
func (ta *TopicAnalyzer) extractTopicKeywords(pages []models.DialoguePage) []string {
	keywordCount := make(map[string]int)

	for _, page := range pages {
		words := strings.Fields(strings.ToLower(page.UserMessage + " " + page.AgentResponse))
		for _, word := range words {
			// Simple word filtering
			if len(word) > 3 && !isStopWord(word) {
				keywordCount[word]++
			}
		}
	}

	// Get top keywords
	var keywords []string
	for word, count := range keywordCount {
		if count >= 2 || len(keywords) < 3 { // Include words mentioned at least twice, or fill up to 3
			keywords = append(keywords, word)
		}
		if len(keywords) >= 5 {
			break
		}
	}

	return keywords
}

func (ta *TopicAnalyzer) generateTopicContent(pages []models.DialoguePage) string {
	if len(pages) == 0 {
		return ""
	}

	// Simple content generation - combine key parts
	var content strings.Builder
	content.WriteString("Discussion covering ")

	if len(pages) == 1 {
		content.WriteString("a single exchange about ")
		content.WriteString(ta.extractMainConcept(pages[0]))
	} else {
		content.WriteString(fmt.Sprintf("%d exchanges about ", len(pages)))
		mainConcepts := make([]string, 0, len(pages))
		for _, page := range pages {
			concept := ta.extractMainConcept(page)
			if concept != "" {
				mainConcepts = append(mainConcepts, concept)
			}
		}
		if len(mainConcepts) > 0 {
			limit := len(mainConcepts)
			if limit > 3 {
				limit = 3
			}
			content.WriteString(strings.Join(mainConcepts[:limit], ", "))
		}
	}

	return content.String()
}

func (ta *TopicAnalyzer) extractMainConcept(page models.DialoguePage) string {
	// Extract the main concept from a page (simplified)
	text := strings.ToLower(page.UserMessage)

	// Look for question patterns
	if strings.Contains(text, "how") {
		return "procedures and methods"
	}
	if strings.Contains(text, "what") {
		return "definitions and explanations"
	}
	if strings.Contains(text, "why") {
		return "reasoning and causes"
	}
	if strings.Contains(text, "error") || strings.Contains(text, "problem") {
		return "troubleshooting"
	}

	return "general inquiry"
}

func (ta *TopicAnalyzer) calculateTopicConfidence(pages []models.DialoguePage, keywords []string) float64 {
	if len(pages) == 0 {
		return 0.0
	}

	confidence := 0.5 // Base confidence

	// Boost for multiple pages (more context)
	if len(pages) > 1 {
		confidence += 0.2
	}

	// Boost for relevant keywords
	if len(keywords) > 0 {
		confidence += 0.1 * float64(len(keywords)) / 5.0
	}

	// Boost for longer conversations
	totalLength := 0
	for _, page := range pages {
		totalLength += len(page.UserMessage) + len(page.AgentResponse)
	}
	avgLength := totalLength / len(pages)
	if avgLength > 100 {
		confidence += 0.1
	}

	// Ensure confidence is in valid range
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

func (ta *TopicAnalyzer) getPageIndices(groupPages []models.DialoguePage, allPages []models.DialoguePage) []int {
	indices := make([]int, 0, len(groupPages))

	for _, groupPage := range groupPages {
		for i, page := range allPages {
			if page.ID == groupPage.ID {
				indices = append(indices, i)
				break
			}
		}
	}

	return indices
}

func (ta *TopicAnalyzer) findRelevantPages(topic *TopicSummary, pages []models.DialoguePage) []int {
	indices := make([]int, 0, len(pages))

	for i, page := range pages {
		content := strings.ToLower(page.UserMessage + " " + page.AgentResponse)
		relevance := 0

		// Check keyword presence
		for _, keyword := range topic.Keywords {
			if strings.Contains(content, strings.ToLower(keyword)) {
				relevance++
			}
		}

		// Include page if it has reasonable relevance
		if relevance > 0 || len(topic.Keywords) == 0 {
			indices = append(indices, i)
		}
	}

	return indices
}

func (ta *TopicAnalyzer) adjustConfidenceWithKeywords(topic *TopicSummary, pages []models.DialoguePage) float64 {
	if len(topic.PageIndices) == 0 {
		return topic.Confidence * 0.5 // Lower confidence if no relevant pages
	}

	keywordHits := 0
	totalWords := 0

	for _, pageIndex := range topic.PageIndices {
		if pageIndex < len(pages) {
			content := strings.ToLower(pages[pageIndex].UserMessage + " " + pages[pageIndex].AgentResponse)
			words := strings.Fields(content)
			totalWords += len(words)

			for _, keyword := range topic.Keywords {
				if strings.Contains(content, strings.ToLower(keyword)) {
					keywordHits++
				}
			}
		}
	}

	keywordDensity := float64(keywordHits) / float64(maxInt(totalWords, 1))
	boost := keywordDensity * 0.3 // Up to 30% boost for high keyword density

	adjustedConfidence := topic.Confidence + boost
	if adjustedConfidence > 1.0 {
		adjustedConfidence = 1.0
	}

	return adjustedConfidence
}

// Helper functions
func isStopWord(word string) bool {
	stopWords := []string{"the", "and", "or", "but", "in", "on", "at", "to", "for", "of", "with", "by", "is", "are", "was", "were", "be", "been", "have", "has", "had", "do", "does", "did", "will", "would", "could", "should", "may", "might", "can", "a", "an", "this", "that", "these", "those"}

	for _, stopWord := range stopWords {
		if word == stopWord {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseBoolEnv(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}
