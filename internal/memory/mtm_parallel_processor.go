package memory

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"
)

// ParallelProcessor handles concurrent LLM operations for better performance
type ParallelProcessor struct {
	maxWorkers int
	semaphore  chan struct{}
	stmStore   *STMStore
}

// ProcessingTask represents a generic LLM processing task
type ProcessingTask struct {
	ID       string
	TaskType string
	Execute  func() (interface{}, error)
}

// ProcessingResult holds the result of a parallel processing task
type ProcessingResult struct {
	TaskID   string
	Result   interface{}
	Error    error
	Duration time.Duration
}

// ChainAnalysisTask represents analysis tasks for cognitive chains
type ChainAnalysisTask struct {
	Chain    *models.CognitiveChain
	Events   []models.CognitiveEvent
	TaskType string // "summary", "quality", "keywords", "full"
}

// ChainAnalysisResult holds results from chain analysis
type ChainAnalysisResult struct {
	ChainID         string
	Summary         string
	QualityScore    float64
	ContinuityScore float64
	Keywords        []string
	Error           error
	Duration        time.Duration
}

// NewParallelProcessor creates a new parallel processor
func NewParallelProcessor(maxWorkers int, stmStore *STMStore) *ParallelProcessor {
	if maxWorkers <= 0 {
		maxWorkers = parseIntEnv("PARALLEL_PROCESSOR_MAX_WORKERS", 3)
	}
	return &ParallelProcessor{
		maxWorkers: maxWorkers,
		semaphore:  make(chan struct{}, maxWorkers),
		stmStore:   stmStore,
	}
}

// ProcessTasks executes multiple tasks in parallel with controlled concurrency
func (pp *ParallelProcessor) ProcessTasks(ctx context.Context, tasks []*ProcessingTask) ([]*ProcessingResult, error) {
	if len(tasks) == 0 {
		return []*ProcessingResult{}, nil
	}

	start := time.Now()
	results := make([]*ProcessingResult, len(tasks))
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(index int, t *ProcessingTask) {
			defer wg.Done()
			pp.semaphore <- struct{}{}
			defer func() { <-pp.semaphore }()

			taskStart := time.Now()
			result, err := t.Execute()
			duration := time.Since(taskStart)

			results[index] = &ProcessingResult{
				TaskID:   t.ID,
				Result:   result,
				Error:    err,
				Duration: duration,
			}
		}(i, task)
	}

	wg.Wait()
	totalDuration := time.Since(start)
	log.Printf("INFO: Parallel processing completed - Tasks: %d, Duration: %v, Avg per task: %v",
		len(tasks), totalDuration, totalDuration/time.Duration(len(tasks)))

	return results, nil
}

// ProcessChainAnalysis performs parallel analysis on multiple cognitive chains
func (pp *ParallelProcessor) ProcessChainAnalysis(ctx context.Context, tasks []*ChainAnalysisTask) ([]*ChainAnalysisResult, error) {
	if len(tasks) == 0 {
		return []*ChainAnalysisResult{}, nil
	}

	start := time.Now()
	results := make([]*ChainAnalysisResult, len(tasks))
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(index int, t *ChainAnalysisTask) {
			defer wg.Done()
			pp.semaphore <- struct{}{}
			defer func() { <-pp.semaphore }()
			results[index] = pp.analyzeChain(ctx, t)
		}(i, task)
	}

	wg.Wait()
	totalDuration := time.Since(start)
	log.Printf("INFO: Parallel chain analysis completed - Chains: %d, Duration: %v", len(tasks), totalDuration)
	return results, nil
}

// analyzeChain performs comprehensive analysis on a single cognitive chain
func (pp *ParallelProcessor) analyzeChain(ctx context.Context, task *ChainAnalysisTask) *ChainAnalysisResult {
	start := time.Now()
	result := &ChainAnalysisResult{
		ChainID: task.Chain.ChainID,
	}

	switch task.TaskType {
	case "summary":
		summary, err := pp.generateSummary(ctx, task.Events)
		if err != nil {
			result.Error = err
		} else {
			result.Summary = summary
		}
	case "quality":
		score, err := pp.assessQuality(ctx, task.Chain, task.Events)
		if err != nil {
			result.Error = err
		} else {
			result.QualityScore = score
		}
	case "keywords":
		keywords, err := pp.extractKeywords(ctx, task.Events)
		if err != nil {
			result.Error = err
		} else {
			result.Keywords = keywords
		}
	case "full":
		// Perform analysis efficiently
		analysisResult, err := pp.stmStore.topicAnalyzer.AnalyzeTopics(ctx, task.Events)
		if err == nil && analysisResult.MainTopic != nil {
			result.Summary = analysisResult.MainTopic.Content
			result.Keywords = analysisResult.MainTopic.Keywords
		}
		if score, err := pp.assessQuality(ctx, task.Chain, task.Events); err == nil {
			result.QualityScore = score
		}
	}

	result.Duration = time.Since(start)
	return result
}

// generateSummary creates a summary for the given cognitive events using the TopicAnalyzer
func (pp *ParallelProcessor) generateSummary(ctx context.Context, events []models.CognitiveEvent) (string, error) {
	if pp.stmStore == nil || pp.stmStore.topicAnalyzer == nil {
		return "", fmt.Errorf("topic analyzer not available")
	}
	analysisResult, err := pp.stmStore.topicAnalyzer.AnalyzeTopics(ctx, events)
	if err != nil {
		return "", err
	}
	if analysisResult.MainTopic != nil && analysisResult.MainTopic.Content != "" {
		return analysisResult.MainTopic.Content, nil
	}
	return "Conversation summary.", nil
}

// assessQuality evaluates the quality of a cognitive chain using the QualityValidator
func (pp *ParallelProcessor) assessQuality(ctx context.Context, chain *models.CognitiveChain, events []models.CognitiveEvent) (float64, error) {
	if pp.stmStore == nil || pp.stmStore.qualityValidator == nil {
		return 0, fmt.Errorf("quality validator not available")
	}
	validationResult, err := pp.stmStore.qualityValidator.ValidateSegment(ctx, chain, events)
	if err != nil {
		return 0, err
	}
	return validationResult.QualityScore, nil
}

// extractKeywords extracts relevant keywords from cognitive events using the TopicAnalyzer
func (pp *ParallelProcessor) extractKeywords(ctx context.Context, events []models.CognitiveEvent) ([]string, error) {
	if pp.stmStore == nil || pp.stmStore.topicAnalyzer == nil {
		return nil, fmt.Errorf("topic analyzer not available")
	}
	analysisResult, err := pp.stmStore.topicAnalyzer.AnalyzeTopics(ctx, events)
	if err != nil {
		return nil, err
	}
	if analysisResult.MainTopic != nil && len(analysisResult.MainTopic.Keywords) > 0 {
		return analysisResult.MainTopic.Keywords, nil
	}
	return []string{}, nil
}

// ProcessUserProfileAnalysis performs parallel user profile and knowledge extraction from cognitive chains
func (pp *ParallelProcessor) ProcessUserProfileAnalysis(ctx context.Context, chains []*models.CognitiveChain) (*ProfileAnalysisResult, error) {
	if len(chains) == 0 {
		return &ProfileAnalysisResult{}, nil
	}

	start := time.Now()
	var wg sync.WaitGroup
	profileChan := make(chan string, 1)
	knowledgeChan := make(chan []string, 1)
	errorChan := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		pp.semaphore <- struct{}{}
		defer func() { <-pp.semaphore }()
		profile, err := pp.analyzeUserProfile(ctx, chains)
		if err != nil {
			errorChan <- err
			return
		}
		profileChan <- profile
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		pp.semaphore <- struct{}{}
		defer func() { <-pp.semaphore }()
		knowledge, err := pp.extractUserKnowledge(ctx, chains)
		if err != nil {
			errorChan <- err
			return
		}
		knowledgeChan <- knowledge
	}()

	wg.Wait()
	close(profileChan)
	close(knowledgeChan)
	close(errorChan)

	for err := range errorChan {
		if err != nil {
			return nil, err
		}
	}

	var profile string
	var knowledge []string
	if p := <-profileChan; p != "" {
		profile = p
	}
	if k := <-knowledgeChan; len(k) > 0 {
		knowledge = k
	}

	duration := time.Since(start)
	log.Printf("INFO: Parallel profile analysis completed - Chains: %d, Duration: %v", len(chains), duration)

	return &ProfileAnalysisResult{
		UserProfile:        profile,
		ExtractedKnowledge: knowledge,
		AnalyzedSegments:   len(chains),
		Duration:           duration,
	}, nil
}

// ProfileAnalysisResult holds results from parallel profile analysis
type ProfileAnalysisResult struct {
	UserProfile        string
	ExtractedKnowledge []string
	AnalyzedSegments   int
	Duration           time.Duration
}

// analyzeUserProfile analyzes user characteristics from cognitive chains
func (pp *ParallelProcessor) analyzeUserProfile(_ context.Context, chains []*models.CognitiveChain) (string, error) {
	var interests, skills []string
	for _, chain := range chains {
		summary := strings.ToLower(chain.Summary)
		if strings.Contains(summary, "python") || strings.Contains(summary, "programming") {
			interests = append(interests, "programming")
		}
		if strings.Contains(summary, "machine learning") || strings.Contains(summary, "ai") {
			interests = append(interests, "machine learning")
		}
		if strings.Contains(summary, "database") || strings.Contains(summary, "sql") {
			interests = append(interests, "databases")
		}
		if strings.Contains(summary, "advanced") || strings.Contains(summary, "expert") {
			skills = append(skills, "advanced")
		} else if strings.Contains(summary, "beginner") || strings.Contains(summary, "learning") {
			skills = append(skills, "beginner")
		}
	}

	profile := fmt.Sprintf("User shows interest in: %v. Skill level appears to be: %v. Based on %d conversation chains.",
		uniqueStrings(interests), uniqueStrings(skills), len(chains))
	return profile, nil
}

// extractUserKnowledge extracts factual knowledge from cognitive chains
func (pp *ParallelProcessor) extractUserKnowledge(_ context.Context, chains []*models.CognitiveChain) ([]string, error) {
	var knowledge []string
	for _, chain := range chains {
		if strings.Contains(chain.Summary, "uses") {
			knowledge = append(knowledge, fmt.Sprintf("User uses: %s", chain.Summary))
		}
		if strings.Contains(chain.Summary, "prefers") {
			knowledge = append(knowledge, fmt.Sprintf("User prefers: %s", chain.Summary))
		}
		if strings.Contains(chain.Summary, "works with") {
			knowledge = append(knowledge, fmt.Sprintf("User works with: %s", chain.Summary))
		}
	}
	return uniqueStrings(knowledge), nil
}

// uniqueStrings removes duplicate strings from a slice
func uniqueStrings(s []string) []string {
	keys := make(map[string]bool)
	var list []string
	for _, entry := range s {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
