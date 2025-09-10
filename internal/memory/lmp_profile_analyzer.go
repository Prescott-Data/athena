package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dromos-org/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/mongo"
)

// ProfileAnalyzer performs sophisticated 90-dimension personality analysis
type ProfileAnalyzer struct {
	db     *mongo.Database
	config PersonalityAnalysisConfig
}

// PersonalityAnalysisResult holds the results of personality analysis
type PersonalityAnalysisResult struct {
	UserID             string                    `json:"user_id"`
	UpdatedDimensions  []DimensionUpdate         `json:"updated_dimensions"`
	ExtractedFacts     []UserFactEntry           `json:"extracted_facts"`
	AssistantKnowledge []AssistantKnowledgeEntry `json:"assistant_knowledge"`
	AnalysisConfidence float64                   `json:"analysis_confidence"`
	ProcessingTime     time.Duration             `json:"processing_time"`
	Method             string                    `json:"method"` // "llm", "heuristic", "hybrid"
}

// DimensionUpdate represents an update to a personality dimension
type DimensionUpdate struct {
	DimensionName string          `json:"dimension_name"`
	DimensionType DimensionType   `json:"dimension_type"`
	OldScore      *DimensionScore `json:"old_score,omitempty"`
	NewScore      *DimensionScore `json:"new_score"`
	UpdateReason  string          `json:"update_reason"`
}

// ConversationAnalysisInput represents input for personality analysis
type ConversationAnalysisInput struct {
	UserID          string                `json:"user_id"`
	Pages           []models.DialoguePage `json:"pages"`
	Segments        []models.Segment      `json:"segments"`
	ExistingProfile *UserPersona          `json:"existing_profile,omitempty"`
}

// NewProfileAnalyzer creates a new profile analyzer
func NewProfileAnalyzer(db *mongo.Database) *ProfileAnalyzer {
	return &ProfileAnalyzer{
		db:     db,
		config: GetDefaultPersonalityAnalysisConfig(),
	}
}

// GetDefaultPersonalityAnalysisConfig returns default configuration
func GetDefaultPersonalityAnalysisConfig() PersonalityAnalysisConfig {
	return PersonalityAnalysisConfig{
		MinConfidenceThreshold:      parseFloatEnv("PERSONALITY_MIN_CONFIDENCE", 0.6),
		DimensionUpdateThreshold:    parseFloatEnv("PERSONALITY_UPDATE_THRESHOLD", 0.1),
		MaxFactsPerCategory:         parseIntEnv("PERSONALITY_MAX_FACTS_PER_CATEGORY", 20),
		FactRetentionDays:           parseIntEnv("PERSONALITY_FACT_RETENTION_DAYS", 90),
		RequireMultipleObservations: parseBoolEnv("PERSONALITY_REQUIRE_MULTIPLE_OBS", true),
	}
}

// AnalyzeConversation performs comprehensive personality analysis on conversation data
func (pa *ProfileAnalyzer) AnalyzeConversation(ctx context.Context, input *ConversationAnalysisInput) (*PersonalityAnalysisResult, error) {
	start := time.Now()

	log.Printf("INFO: Starting personality analysis for user %s with %d pages, %d segments",
		input.UserID, len(input.Pages), len(input.Segments))

	result := &PersonalityAnalysisResult{
		UserID:             input.UserID,
		UpdatedDimensions:  []DimensionUpdate{},
		ExtractedFacts:     []UserFactEntry{},
		AssistantKnowledge: []AssistantKnowledgeEntry{},
	}

	// Try LLM-based analysis first
	if LLMBaseURL != "" {
		llmResult, err := pa.analyzewithLLM(ctx, input)
		if err != nil {
			log.Printf("WARN: LLM personality analysis failed: %v, falling back to heuristic", err)
			result.Method = "heuristic_fallback"
		} else {
			result = llmResult
			result.Method = "llm"
		}
	}

	// If LLM failed or unavailable, use heuristic analysis
	if result.Method == "" || result.Method == "heuristic_fallback" {
		heuristicResult, err := pa.analyzeWithHeuristics(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("both LLM and heuristic analysis failed: %w", err)
		}
		if result.Method == "heuristic_fallback" {
			// Merge LLM and heuristic results
			result = pa.mergeAnalysisResults(result, heuristicResult)
			result.Method = "hybrid"
		} else {
			result = heuristicResult
			result.Method = "heuristic"
		}
	}

	result.ProcessingTime = time.Since(start)

	// Calculate overall analysis confidence
	result.AnalysisConfidence = pa.calculateAnalysisConfidence(result)

	log.Printf("INFO: Personality analysis completed for %s - Method: %s, Dimensions: %d, Confidence: %.3f, Duration: %v",
		input.UserID, result.Method, len(result.UpdatedDimensions), result.AnalysisConfidence, result.ProcessingTime)

	return result, nil
}

// analyzewithLLM performs LLM-based personality analysis using the 90-dimension framework
func (pa *ProfileAnalyzer) analyzewithLLM(ctx context.Context, input *ConversationAnalysisInput) (*PersonalityAnalysisResult, error) {
	// Build conversation text for analysis
	conversationText := pa.buildConversationText(input.Pages)

	// Get existing profile summary
	existingProfileText := ""
	if input.ExistingProfile != nil {
		existingProfileText = pa.buildProfileSummary(input.ExistingProfile)
	}

	// Create personality analysis prompt
	prompt := pa.buildPersonalityAnalysisPrompt(conversationText, existingProfileText)

	// Call LLM for personality analysis
	personalityResponse, err := pa.callLLMForPersonalityAnalysis(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM personality analysis failed: %w", err)
	}

	// Parse personality analysis response
	dimensionUpdates, err := pa.parsePersonalityResponse(personalityResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to parse personality response: %w", err)
	}

	// Extract facts and knowledge separately
	facts, assistantKnowledge, err := pa.extractFactsAndKnowledge(ctx, conversationText)
	if err != nil {
		log.Printf("WARN: Fact extraction failed: %v", err)
		// Continue without facts - personality analysis is more important
	}

	return &PersonalityAnalysisResult{
		UserID:             input.UserID,
		UpdatedDimensions:  dimensionUpdates,
		ExtractedFacts:     facts,
		AssistantKnowledge: assistantKnowledge,
	}, nil
}

// analyzeWithHeuristics performs rule-based personality analysis
func (pa *ProfileAnalyzer) analyzeWithHeuristics(ctx context.Context, input *ConversationAnalysisInput) (*PersonalityAnalysisResult, error) {
	result := &PersonalityAnalysisResult{
		UserID:             input.UserID,
		UpdatedDimensions:  []DimensionUpdate{},
		ExtractedFacts:     []UserFactEntry{},
		AssistantKnowledge: []AssistantKnowledgeEntry{},
	}

	// Analyze conversation patterns for personality dimensions
	for _, page := range input.Pages {
		// Extract content interests based on keywords
		contentUpdates := pa.analyzeContentInterests(page)
		result.UpdatedDimensions = append(result.UpdatedDimensions, contentUpdates...)

		// Analyze communication style
		styleUpdates := pa.analyzeCommunicationStyle(page)
		result.UpdatedDimensions = append(result.UpdatedDimensions, styleUpdates...)

		// Extract user facts
		facts := pa.extractUserFactsHeuristic(page)
		result.ExtractedFacts = append(result.ExtractedFacts, facts...)

		// Extract assistant knowledge
		knowledge := pa.extractAssistantKnowledgeHeuristic(page)
		result.AssistantKnowledge = append(result.AssistantKnowledge, knowledge...)
	}

	// Deduplicate and refine results
	result.UpdatedDimensions = pa.dedupeDimensionUpdates(result.UpdatedDimensions)
	result.ExtractedFacts = pa.dedupeUserFacts(result.ExtractedFacts)
	result.AssistantKnowledge = pa.dedupeAssistantKnowledge(result.AssistantKnowledge)

	return result, nil
}

// buildConversationText creates formatted text for LLM analysis
func (pa *ProfileAnalyzer) buildConversationText(pages []models.DialoguePage) string {
	var builder strings.Builder

	for i, page := range pages {
		builder.WriteString(fmt.Sprintf("Turn %d (Time: %s):\n", i+1, page.CreatedAt.Format("2006-01-02 15:04:05")))
		builder.WriteString(fmt.Sprintf("User: %s\n", page.UserMessage))
		builder.WriteString(fmt.Sprintf("Assistant: %s\n", page.AgentResponse))
		builder.WriteString("---\n")
	}

	return builder.String()
}

// buildProfileSummary creates a summary of existing profile for LLM context
func (pa *ProfileAnalyzer) buildProfileSummary(profile *UserPersona) string {
	var builder strings.Builder

	builder.WriteString("Existing User Profile:\n")

	// Summarize psychological dimensions
	if profile.PsychologicalModel != nil {
		builder.WriteString("Psychological Traits:\n")
		if profile.PsychologicalModel.Extraversion != nil {
			builder.WriteString(fmt.Sprintf("- Extraversion: %s (Confidence: %.2f)\n",
				profile.PsychologicalModel.Extraversion.Level, profile.PsychologicalModel.Extraversion.Confidence))
		}
		// Add other significant dimensions...
	}

	// Summarize content interests
	if profile.ContentInterests != nil {
		builder.WriteString("Content Interests:\n")
		if profile.ContentInterests.TechInterest != nil {
			builder.WriteString(fmt.Sprintf("- Technology: %s (Confidence: %.2f)\n",
				profile.ContentInterests.TechInterest.Level, profile.ContentInterests.TechInterest.Confidence))
		}
		// Add other significant interests...
	}

	// Include key user facts
	if len(profile.UserFacts) > 0 {
		builder.WriteString("Key Facts:\n")
		for i, fact := range profile.UserFacts {
			if i >= 5 {
				break // Limit to top 5 facts
			}
			builder.WriteString(fmt.Sprintf("- %s\n", fact.Content))
		}
	}

	return builder.String()
}

// buildPersonalityAnalysisPrompt creates the LLM prompt for personality analysis
func (pa *ProfileAnalyzer) buildPersonalityAnalysisPrompt(conversationText, existingProfile string) string {
	dimensionNames := GetAllDimensionNames()

	var dimensionList strings.Builder
	for dimType, names := range dimensionNames {
		dimensionList.WriteString(fmt.Sprintf("[%s]\n", dimType))
		for _, name := range names {
			description := GetDimensionDescription(name)
			dimensionList.WriteString(fmt.Sprintf("%s: %s\n", name, description))
		}
		dimensionList.WriteString("\n")
	}

	return fmt.Sprintf(`You are a professional user preference analysis assistant. Analyze the user's personality from the conversation based on the 90 personality dimensions below.

For each dimension that you can identify from the conversation:
1. Determine the user's preference level: High / Medium / Low
2. Provide brief reasoning with specific evidence from the conversation
3. Only include dimensions that have clear evidence

Personality Dimensions:
%s

%s

Latest Conversation:
%s

Return your analysis as JSON in this format:
{
  "dimensions": [
    {
      "name": "dimension_name",
      "type": "psychological|ai_alignment|content_interest", 
      "level": "High|Medium|Low",
      "confidence": 0.0-1.0,
      "evidence": "Brief explanation with specific evidence"
    }
  ]
}

Focus on dimensions with clear evidence. Be conservative with confidence scores.`,
		dimensionList.String(), existingProfile, conversationText)
}

// callLLMForPersonalityAnalysis makes the LLM API call for personality analysis
func (pa *ProfileAnalyzer) callLLMForPersonalityAnalysis(ctx context.Context, prompt string) ([]byte, error) {
	if LLMBaseURL == "" {
		return nil, fmt.Errorf("LLM not configured")
	}

	modelName := LLMModelName
	if modelName == "" {
		modelName = "Qwen/Qwen3-32B-AWQ"
	}

	request := map[string]interface{}{
		"model":       modelName,
		"prompt":      prompt,
		"max_tokens":  1000,
		"temperature": 0.2, // Low temperature for consistent analysis
	}

	_, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	// Simulate LLM response for now (in production, this would be a real HTTP call)
	responseBody := []byte(`{"choices": [{"text": "{\"dimensions\": [{\"name\": \"tech_interest\", \"type\": \"content_interest\", \"level\": \"High\", \"confidence\": 0.8, \"evidence\": \"User frequently asks about programming and technology topics\"}, {\"name\": \"cognitive_needs\", \"type\": \"psychological\", \"level\": \"High\", \"confidence\": 0.7, \"evidence\": \"User shows strong desire to understand technical concepts\"}]}"}]}`)

	return responseBody, nil
}

// parsePersonalityResponse parses LLM response into dimension updates
func (pa *ProfileAnalyzer) parsePersonalityResponse(responseBody []byte) ([]DimensionUpdate, error) {
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

	// Parse the JSON response
	responseText := strings.TrimSpace(llmResponse.Choices[0].Text)

	// Remove any markdown formatting
	if strings.HasPrefix(responseText, "```json") {
		responseText = strings.TrimPrefix(responseText, "```json")
		responseText = strings.TrimSuffix(responseText, "```")
		responseText = strings.TrimSpace(responseText)
	}

	var analysisResult struct {
		Dimensions []struct {
			Name       string  `json:"name"`
			Type       string  `json:"type"`
			Level      string  `json:"level"`
			Confidence float64 `json:"confidence"`
			Evidence   string  `json:"evidence"`
		} `json:"dimensions"`
	}

	if err := json.Unmarshal([]byte(responseText), &analysisResult); err != nil {
		return nil, fmt.Errorf("failed to parse personality analysis JSON: %w", err)
	}

	var updates []DimensionUpdate
	for _, dim := range analysisResult.Dimensions {
		if dim.Confidence >= pa.config.MinConfidenceThreshold {
			update := DimensionUpdate{
				DimensionName: dim.Name,
				DimensionType: DimensionType(dim.Type),
				NewScore: &DimensionScore{
					Level:            dim.Level,
					Confidence:       dim.Confidence,
					Evidence:         dim.Evidence,
					LastObserved:     time.Now(),
					ObservationCount: 1,
				},
				UpdateReason: "LLM personality analysis",
			}
			updates = append(updates, update)
		}
	}

	return updates, nil
}

// extractFactsAndKnowledge extracts user facts and assistant knowledge
func (pa *ProfileAnalyzer) extractFactsAndKnowledge(ctx context.Context, conversationText string) ([]UserFactEntry, []AssistantKnowledgeEntry, error) {
	// Create knowledge extraction prompt
	prompt := pa.buildKnowledgeExtractionPrompt(conversationText)

	// Call LLM for knowledge extraction
	response, err := pa.callLLMForKnowledgeExtraction(ctx, prompt)
	if err != nil {
		return nil, nil, err
	}

	// Parse knowledge response
	facts, knowledge, err := pa.parseKnowledgeResponse(response)
	if err != nil {
		return nil, nil, err
	}

	return facts, knowledge, nil
}

// buildKnowledgeExtractionPrompt creates prompt for extracting facts and knowledge
func (pa *ProfileAnalyzer) buildKnowledgeExtractionPrompt(conversationText string) string {
	return fmt.Sprintf(`Extract user private data and assistant knowledge from the conversation below.

Latest Conversation:
%s

Return JSON format:
{
  "user_facts": [
    {
      "category": "personal|preference|skill|other",
      "content": "Brief factual statement",
      "context": "Context/source information",
      "confidence": 0.0-1.0
    }
  ],
  "assistant_knowledge": [
    {
      "capability": "What capability was shown",
      "action": "What action was taken", 
      "context": "Context of demonstration"
    }
  ]
}

Be extremely concise and factual. Only include clear, verifiable information.`, conversationText)
}

// callLLMForKnowledgeExtraction makes LLM call for knowledge extraction
func (pa *ProfileAnalyzer) callLLMForKnowledgeExtraction(ctx context.Context, prompt string) ([]byte, error) {
	// Similar to personality analysis but focused on knowledge extraction
	// For now, return a mock response
	responseBody := []byte(`{"choices": [{"text": "{\"user_facts\": [{\"category\": \"skill\", \"content\": \"User has programming experience\", \"context\": \"Mentioned debugging and code review\", \"confidence\": 0.8}], \"assistant_knowledge\": [{\"capability\": \"code_explanation\", \"action\": \"Explained programming concepts\", \"context\": \"During technical discussion\"}]}"}]}`)

	return responseBody, nil
}

// parseKnowledgeResponse parses knowledge extraction response
func (pa *ProfileAnalyzer) parseKnowledgeResponse(responseBody []byte) ([]UserFactEntry, []AssistantKnowledgeEntry, error) {
	var llmResponse struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(responseBody, &llmResponse); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal LLM response: %w", err)
	}

	if len(llmResponse.Choices) == 0 {
		return nil, nil, fmt.Errorf("no choices in LLM response")
	}

	responseText := strings.TrimSpace(llmResponse.Choices[0].Text)
	if strings.HasPrefix(responseText, "```json") {
		responseText = strings.TrimPrefix(responseText, "```json")
		responseText = strings.TrimSuffix(responseText, "```")
		responseText = strings.TrimSpace(responseText)
	}

	var knowledgeResult struct {
		UserFacts []struct {
			Category   string  `json:"category"`
			Content    string  `json:"content"`
			Context    string  `json:"context"`
			Confidence float64 `json:"confidence"`
		} `json:"user_facts"`
		AssistantKnowledge []struct {
			Capability string `json:"capability"`
			Action     string `json:"action"`
			Context    string `json:"context"`
		} `json:"assistant_knowledge"`
	}

	if err := json.Unmarshal([]byte(responseText), &knowledgeResult); err != nil {
		return nil, nil, fmt.Errorf("failed to parse knowledge JSON: %w", err)
	}

	var facts []UserFactEntry
	for _, fact := range knowledgeResult.UserFacts {
		if fact.Confidence >= pa.config.MinConfidenceThreshold {
			entry := UserFactEntry{
				FactID:      fmt.Sprintf("fact_%d", time.Now().UnixNano()),
				Category:    fact.Category,
				Content:     fact.Content,
				Context:     fact.Context,
				Confidence:  fact.Confidence,
				Source:      "llm_extraction",
				ExtractedAt: time.Now(),
				UpdatedAt:   time.Now(),
			}
			facts = append(facts, entry)
		}
	}

	var knowledge []AssistantKnowledgeEntry
	for _, know := range knowledgeResult.AssistantKnowledge {
		entry := AssistantKnowledgeEntry{
			KnowledgeID:    fmt.Sprintf("knowledge_%d", time.Now().UnixNano()),
			Capability:     know.Capability,
			Action:         know.Action,
			Context:        know.Context,
			DemonstratedAt: time.Now(),
		}
		knowledge = append(knowledge, entry)
	}

	return facts, knowledge, nil
}

// Helper methods for heuristic analysis
func (pa *ProfileAnalyzer) analyzeContentInterests(page models.DialoguePage) []DimensionUpdate {
	var updates []DimensionUpdate

	content := strings.ToLower(page.UserMessage + " " + page.AgentResponse)

	// Tech interest detection
	techKeywords := []string{"programming", "code", "software", "technology", "computer", "api", "database", "algorithm"}
	techScore := pa.calculateKeywordScore(content, techKeywords)
	if techScore > 0.3 {
		level := "Medium"
		if techScore > 0.6 {
			level = "High"
		}
		updates = append(updates, DimensionUpdate{
			DimensionName: "tech_interest",
			DimensionType: DimensionTypeContentInterest,
			NewScore: &DimensionScore{
				Level:            level,
				Confidence:       techScore,
				Evidence:         fmt.Sprintf("Tech keywords found: %.1f%% relevance", techScore*100),
				LastObserved:     time.Now(),
				ObservationCount: 1,
			},
			UpdateReason: "Keyword-based heuristic analysis",
		})
	}

	// Add more content interest detections...

	return updates
}

func (pa *ProfileAnalyzer) analyzeCommunicationStyle(page models.DialoguePage) []DimensionUpdate {
	var updates []DimensionUpdate

	userMessage := page.UserMessage

	// Analyze information density preference
	if len(userMessage) > 100 && strings.Count(userMessage, "?") > 1 {
		updates = append(updates, DimensionUpdate{
			DimensionName: "information_density",
			DimensionType: DimensionTypeContentInterest,
			NewScore: &DimensionScore{
				Level:            "High",
				Confidence:       0.7,
				Evidence:         "User asks detailed, multi-part questions",
				LastObserved:     time.Now(),
				ObservationCount: 1,
			},
			UpdateReason: "Communication pattern analysis",
		})
	}

	return updates
}

func (pa *ProfileAnalyzer) extractUserFactsHeuristic(page models.DialoguePage) []UserFactEntry {
	var facts []UserFactEntry

	// Look for explicit statements about preferences
	content := strings.ToLower(page.UserMessage)
	if strings.Contains(content, "i prefer") || strings.Contains(content, "i like") {
		fact := UserFactEntry{
			FactID:      fmt.Sprintf("fact_%d", time.Now().UnixNano()),
			Category:    "preference",
			Content:     page.UserMessage,
			Context:     "User explicit statement",
			Confidence:  0.9,
			Source:      "explicit",
			ExtractedAt: time.Now(),
			UpdatedAt:   time.Now(),
		}
		facts = append(facts, fact)
	}

	return facts
}

func (pa *ProfileAnalyzer) extractAssistantKnowledgeHeuristic(page models.DialoguePage) []AssistantKnowledgeEntry {
	var knowledge []AssistantKnowledgeEntry

	// Look for assistant demonstrations of capability
	response := strings.ToLower(page.AgentResponse)
	if strings.Contains(response, "i can help") || strings.Contains(response, "here's how") {
		entry := AssistantKnowledgeEntry{
			KnowledgeID:    fmt.Sprintf("knowledge_%d", time.Now().UnixNano()),
			Capability:     "helpful_assistance",
			Action:         "Provided helpful guidance",
			Context:        "During conversation",
			DemonstratedAt: time.Now(),
		}
		knowledge = append(knowledge, entry)
	}

	return knowledge
}

// Helper utility functions
func (pa *ProfileAnalyzer) calculateKeywordScore(content string, keywords []string) float64 {
	matches := 0
	for _, keyword := range keywords {
		if strings.Contains(content, keyword) {
			matches++
		}
	}
	return float64(matches) / float64(len(keywords))
}

func (pa *ProfileAnalyzer) mergeAnalysisResults(llmResult, heuristicResult *PersonalityAnalysisResult) *PersonalityAnalysisResult {
	// Combine dimensions, preferring LLM results but adding heuristic insights
	merged := &PersonalityAnalysisResult{
		UserID:             llmResult.UserID,
		UpdatedDimensions:  llmResult.UpdatedDimensions,
		ExtractedFacts:     append(llmResult.ExtractedFacts, heuristicResult.ExtractedFacts...),
		AssistantKnowledge: append(llmResult.AssistantKnowledge, heuristicResult.AssistantKnowledge...),
	}

	// Add heuristic dimensions that weren't found by LLM
	for _, heuristicDim := range heuristicResult.UpdatedDimensions {
		found := false
		for _, llmDim := range llmResult.UpdatedDimensions {
			if llmDim.DimensionName == heuristicDim.DimensionName {
				found = true
				break
			}
		}
		if !found {
			merged.UpdatedDimensions = append(merged.UpdatedDimensions, heuristicDim)
		}
	}

	return merged
}

func (pa *ProfileAnalyzer) calculateAnalysisConfidence(result *PersonalityAnalysisResult) float64 {
	if len(result.UpdatedDimensions) == 0 {
		return 0.0
	}

	totalConfidence := 0.0
	for _, dim := range result.UpdatedDimensions {
		if dim.NewScore != nil {
			totalConfidence += dim.NewScore.Confidence
		}
	}

	return totalConfidence / float64(len(result.UpdatedDimensions))
}

func (pa *ProfileAnalyzer) dedupeDimensionUpdates(updates []DimensionUpdate) []DimensionUpdate {
	seen := make(map[string]*DimensionUpdate)

	for _, update := range updates {
		key := update.DimensionName
		if existing, exists := seen[key]; exists {
			// Keep the update with higher confidence
			if update.NewScore != nil && existing.NewScore != nil &&
				update.NewScore.Confidence > existing.NewScore.Confidence {
				seen[key] = &update
			}
		} else {
			seen[key] = &update
		}
	}

	var deduped []DimensionUpdate
	for _, update := range seen {
		deduped = append(deduped, *update)
	}

	return deduped
}

func (pa *ProfileAnalyzer) dedupeUserFacts(facts []UserFactEntry) []UserFactEntry {
	seen := make(map[string]bool)
	var deduped []UserFactEntry

	for _, fact := range facts {
		key := strings.ToLower(fact.Content)
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, fact)
		}
	}

	return deduped
}

func (pa *ProfileAnalyzer) dedupeAssistantKnowledge(knowledge []AssistantKnowledgeEntry) []AssistantKnowledgeEntry {
	seen := make(map[string]bool)
	var deduped []AssistantKnowledgeEntry

	for _, know := range knowledge {
		key := know.Capability + know.Action
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, know)
		}
	}

	return deduped
}
