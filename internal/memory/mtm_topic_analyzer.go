package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
)

// TopicAnalyzer provides advanced topic analysis and multi-summary generation
type TopicAnalyzer struct {
	stmStore          *STMStore
	parallelProcessor *ParallelProcessor
	config            TopicAnalysisConfig
	HTTPClient        *http.Client
}

// TopicAnalysisConfig holds configuration for topic analysis
type TopicAnalysisConfig struct {
	MaxTopicsPerSegment int
	MinTopicConfidence  float64
	LLMTimeout          time.Duration
	EnableKeywordBoost  bool
}

// TopicSummary represents a single topic with summary and metadata
type TopicSummary struct {
	Theme        string   `json:"theme"`
	Keywords     []string `json:"keywords"`
	Content      string   `json:"content"`
	Entities     []string `json:"entities"`
	Confidence   float64  `json:"confidence"`
	EventIndices []int    `json:"event_indices"`
}

// MultiTopicResult holds the result of multi-topic analysis
type MultiTopicResult struct {
	MainTopic    *TopicSummary
	SubTopics    []*TopicSummary
	AllEntities  []string
	TotalTopics  int
	AnalysisTime time.Duration
	Method       string
}

// NewTopicAnalyzer creates a new topic analyzer
func NewTopicAnalyzer(stmStore *STMStore, parallelProcessor *ParallelProcessor) *TopicAnalyzer {
	return &TopicAnalyzer{
		stmStore:          stmStore,
		parallelProcessor: parallelProcessor,
		config:            getDefaultTopicAnalysisConfig(),
		HTTPClient:        &http.Client{},
	}
}

// getDefaultTopicAnalysisConfig returns default configuration from environment variables
func getDefaultTopicAnalysisConfig() TopicAnalysisConfig {
	maxTopics := parseIntEnv("TOPIC_ANALYSIS_MAX_TOPICS", 3)
	minConfidence := parseFloatEnv("TOPIC_ANALYSIS_MIN_CONFIDENCE", 0.6)
	llmTimeout := time.Duration(parseIntEnv("TOPIC_ANALYSIS_LLM_TIMEOUT", 20)) * time.Second
	enableKeywordBoost := parseBoolEnv("TOPIC_ANALYSIS_KEYWORD_BOOST", true)

	return TopicAnalysisConfig{
		MaxTopicsPerSegment: maxTopics,
		MinTopicConfidence:  minConfidence,
		LLMTimeout:          llmTimeout,
		EnableKeywordBoost:  enableKeywordBoost,
	}
}

// AnalyzeTopics performs comprehensive topic analysis on cognitive events
func (ta *TopicAnalyzer) AnalyzeTopics(ctx context.Context, events []models.CognitiveEvent) (*MultiTopicResult, error) {
	start := time.Now()
	if len(events) == 0 {
		return &MultiTopicResult{TotalTopics: 0, AnalysisTime: time.Since(start), Method: "empty"}, nil
	}

	log.Printf("INFO: Starting topic analysis for %d events", len(events))
	result, err := ta.analyzeLLMTopics(ctx, events)
	if err != nil {
		log.Printf("WARN: LLM topic analysis failed: %v, falling back to heuristic", err)
		result, err = ta.analyzeHeuristicTopics(ctx, events)
		if err != nil {
			return nil, fmt.Errorf("both LLM and heuristic topic analysis failed: %w", err)
		}
		result.Method = "heuristic_fallback"
	} else {
		result.Method = "llm"
	}

	result.AnalysisTime = time.Since(start)
	log.Printf("INFO: Topic analysis completed - Method: %s, Topics: %d, Duration: %v", result.Method, result.TotalTopics, result.AnalysisTime)
	return result, nil
}

// analyzeLLMTopics uses an LLM to extract multiple topics from the conversation
func (ta *TopicAnalyzer) analyzeLLMTopics(ctx context.Context, events []models.CognitiveEvent) (*MultiTopicResult, error) {
	llmBaseURL := os.Getenv("LLM_BASE_URL")
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
	if llmBaseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("LLM not configured")
	}

	conversationText := ta.buildConversationText(events)
	prompt := ta.buildMultiTopicPrompt(conversationText)

	request := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  1000,
		"temperature": 0.3,
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	httpCtx, cancel := context.WithTimeout(ctx, ta.config.LLMTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", llmBaseURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := ta.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make LLM API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LLM API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read LLM response body: %w", err)
	}

	topics, err := ta.parseLLMTopicResponse(responseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM topic response: %w", err)
	}

	return ta.postProcessTopics(topics, events), nil
}

// buildConversationText creates a rich, formatted text log from cognitive events
func (ta *TopicAnalyzer) buildConversationText(events []models.CognitiveEvent) string {
	var builder strings.Builder
	for i, event := range events {
		builder.WriteString(fmt.Sprintf("Turn %d:\n", i+1))
		builder.WriteString(fmt.Sprintf("%s: [%s] %s\n", event.Role, event.Type, event.Content))
		builder.WriteString("---\n")
	}
	return builder.String()
}

// analyzeHeuristicTopics provides a fallback topic analysis using rules
func (ta *TopicAnalyzer) analyzeHeuristicTopics(_ context.Context, _ []models.CognitiveEvent) (*MultiTopicResult, error) {
	// This function would need to be adapted to work with CognitiveEvent
	// For now, we'll return a simple result.
	return &MultiTopicResult{TotalTopics: 0, Method: "heuristic_stub"}, nil
}

// buildMultiTopicPrompt creates a prompt for LLM multi-topic analysis
func (ta *TopicAnalyzer) buildMultiTopicPrompt(conversationText string) string {
	return fmt.Sprintf(`Analyze the following conversation and identify the main topics. Extract up to %d distinct topics.

For each topic, provide:
1. A brief theme name (2-4 words).
2. Key keywords (3-5 words).
3. A concise content summary (1-2 sentences).
4. Specific technical entities mentioned (Tools, Languages, Frameworks, Libraries).

Return the result as a JSON array of objects. Each object must have the following fields: "theme", "keywords", "content", and "entities".

Example of the JSON format:
[
  {
    "theme": "Data Processing with Python",
    "keywords": ["data", "python", "pandas"],
    "content": "The user is asking about using Python and Pandas for data processing.",
    "entities": ["Python", "Docker"]
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
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &llmResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal LLM response: %w", err)
	}
	if len(llmResponse.Choices) == 0 {
		return nil, fmt.Errorf("no choices in LLM response")
	}

	topicsText := strings.TrimSpace(llmResponse.Choices[0].Message.Content)
	if strings.HasPrefix(topicsText, "```json") {
		topicsText = strings.TrimPrefix(topicsText, "```json")
		topicsText = strings.TrimSuffix(topicsText, "```")
		topicsText = strings.TrimSpace(topicsText)
	}

	var rawTopics []struct {
		Theme    string   `json:"theme"`
		Keywords []string `json:"keywords"`
		Content  string   `json:"content"`
		Entities []string `json:"entities"`
	}
	if err := json.Unmarshal([]byte(topicsText), &rawTopics); err != nil {
		return nil, fmt.Errorf("failed to parse topics JSON: %w", err)
	}

	topics := make([]*TopicSummary, 0, len(rawTopics))
	for _, raw := range rawTopics {
		if raw.Theme != "" && raw.Content != "" {
			topics = append(topics, &TopicSummary{
				Theme:      raw.Theme,
				Keywords:   raw.Keywords,
				Content:    raw.Content,
				Entities:   raw.Entities,
				Confidence: 0.8, // Default confidence for LLM-generated topics
			})
		}
	}
	return topics, nil
}

// postProcessTopics enhances LLM topics with additional analysis
func (ta *TopicAnalyzer) postProcessTopics(topics []*TopicSummary, events []models.CognitiveEvent) *MultiTopicResult {
	entitySet := make(map[string]struct{})

	for _, topic := range topics {
		topic.EventIndices = ta.findRelevantEvents(topic, events)
		if ta.config.EnableKeywordBoost {
			topic.Confidence = ta.adjustConfidenceWithKeywords(topic, events)
		}
		for _, entity := range topic.Entities {
			entitySet[entity] = struct{}{}
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

	allEntities := make([]string, 0, len(entitySet))
	for entity := range entitySet {
		allEntities = append(allEntities, entity)
	}

	return &MultiTopicResult{
		MainTopic:   mainTopic,
		SubTopics:   subTopics,
		AllEntities: allEntities,
		TotalTopics: len(topics),
	}
}

// findRelevantEvents maps a topic to the cognitive events that are relevant to it
func (ta *TopicAnalyzer) findRelevantEvents(topic *TopicSummary, events []models.CognitiveEvent) []int {
	indices := make([]int, 0, len(events))
	for i, event := range events {
		content := strings.ToLower(event.Content)
		relevance := 0
		for _, keyword := range topic.Keywords {
			if strings.Contains(content, strings.ToLower(keyword)) {
				relevance++
			}
		}
		if relevance > 0 || len(topic.Keywords) == 0 {
			indices = append(indices, i)
		}
	}
	return indices
}

// adjustConfidenceWithKeywords adjusts a topic's confidence based on keyword density
func (ta *TopicAnalyzer) adjustConfidenceWithKeywords(topic *TopicSummary, events []models.CognitiveEvent) float64 {
	if len(topic.EventIndices) == 0 {
		return topic.Confidence * 0.5
	}

	keywordHits, totalWords := 0, 0
	for _, eventIndex := range topic.EventIndices {
		if eventIndex < len(events) {
			content := strings.ToLower(events[eventIndex].Content)
			totalWords += len(strings.Fields(content))
			for _, keyword := range topic.Keywords {
				if strings.Contains(content, strings.ToLower(keyword)) {
					keywordHits++
				}
			}
		}
	}

	keywordDensity := float64(keywordHits) / float64(maxInt(totalWords, 1))
	boost := keywordDensity * 0.3
	adjustedConfidence := topic.Confidence + boost
	if adjustedConfidence > 1.0 {
		return 1.0
	}
	return adjustedConfidence
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
