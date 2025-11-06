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

// SegmentAnalysisTask represents analysis tasks for segments
type SegmentAnalysisTask struct {
	Segment  *models.Segment
	Pages    []models.DialoguePage
	TaskType string // "summary", "continuity", "quality"
}

// SegmentAnalysisResult holds results from segment analysis
type SegmentAnalysisResult struct {
	SegmentID       string
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

	// Process tasks with controlled concurrency
	for i, task := range tasks {
		wg.Add(1)
		go func(index int, t *ProcessingTask) {
			defer wg.Done()

			// Acquire semaphore
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

	// Wait for all tasks to complete
	wg.Wait()

	totalDuration := time.Since(start)
	log.Printf("INFO: Parallel processing completed - Tasks: %d, Duration: %v, Avg per task: %v",
		len(tasks), totalDuration, totalDuration/time.Duration(len(tasks)))

	return results, nil
}

// ProcessSegmentAnalysis performs parallel analysis on multiple segments
func (pp *ParallelProcessor) ProcessSegmentAnalysis(ctx context.Context, tasks []*SegmentAnalysisTask) ([]*SegmentAnalysisResult, error) {
	if len(tasks) == 0 {
		return []*SegmentAnalysisResult{}, nil
	}

	start := time.Now()
	results := make([]*SegmentAnalysisResult, len(tasks))
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(index int, t *SegmentAnalysisTask) {
			defer wg.Done()

			// Acquire semaphore for controlled concurrency
			pp.semaphore <- struct{}{}
			defer func() { <-pp.semaphore }()

			result := pp.analyzeSegment(ctx, t)
			results[index] = result
		}(i, task)
	}

	wg.Wait()

	totalDuration := time.Since(start)
	log.Printf("INFO: Parallel segment analysis completed - Segments: %d, Duration: %v",
		len(tasks), totalDuration)

	return results, nil
}

// analyzeSegment performs comprehensive analysis on a single segment
func (pp *ParallelProcessor) analyzeSegment(ctx context.Context, task *SegmentAnalysisTask) *SegmentAnalysisResult {
	start := time.Now()
	result := &SegmentAnalysisResult{
		SegmentID: task.Segment.SegmentID,
	}

	switch task.TaskType {
	case "summary":
		summary, err := pp.generateSummary(ctx, task.Pages)
		if err != nil {
			result.Error = err
		} else {
			result.Summary = summary
		}

	case "quality":
		score, err := pp.assessQuality(ctx, task.Segment, task.Pages)
		if err != nil {
			result.Error = err
		} else {
			result.QualityScore = score
		}

	case "keywords":
		keywords, err := pp.extractKeywords(ctx, task.Segment)
		if err != nil {
			result.Error = err
		} else {
			result.Keywords = keywords
		}

	case "full":
		// Perform all analyses
		if summary, err := pp.generateSummary(ctx, task.Pages); err == nil {
			result.Summary = summary
		}

		if score, err := pp.assessQuality(ctx, task.Segment, task.Pages); err == nil {
			result.QualityScore = score
		}

		if keywords, err := pp.extractKeywords(ctx, task.Segment); err == nil {
			result.Keywords = keywords
		}
	}

	result.Duration = time.Since(start)
	return result
}

// generateSummary creates a summary for the given pages
func (pp *ParallelProcessor) generateSummary(ctx context.Context, pages []models.DialoguePage) (string, error) {
	if pp.stmStore == nil {
		return "", fmt.Errorf("STM store not available")
	}

	return pp.stmStore.CreateSegmentSummary(ctx, pages)
}

// assessQuality evaluates the quality of a segment
func (pp *ParallelProcessor) assessQuality(ctx context.Context, segment *models.Segment, pages []models.DialoguePage) (float64, error) {
	// Quality assessment based on multiple factors
	var qualityScore float64 = 0.5 // Default moderate quality

	// Factor 1: Information completeness
	if len(pages) > 0 {
		avgMessageLength := 0
		for _, page := range pages {
			avgMessageLength += len(page.UserMessage) + len(page.AgentResponse)
		}
		avgMessageLength /= len(pages)

		// Longer messages generally indicate more detailed conversations
		if avgMessageLength > 200 {
			qualityScore += 0.2
		} else if avgMessageLength < 50 {
			qualityScore -= 0.1
		}
	}

	// Factor 2: Topic coherence (based on summary quality)
	if segment.TopicSummary != "" {
		summaryLength := len(segment.TopicSummary)
		if summaryLength > 100 && summaryLength < 500 {
			qualityScore += 0.1 // Good summary length
		} else if summaryLength < 20 {
			qualityScore -= 0.2 // Too short, likely not meaningful
		}
	}

	// Factor 3: Interaction depth
	interactionDepth := len(pages)
	if interactionDepth >= 3 {
		qualityScore += 0.1 // Multi-turn conversations are generally higher quality
	} else if interactionDepth == 1 {
		qualityScore -= 0.1 // Single exchanges might be less valuable
	}

	// Factor 4: Time span (conversations over longer periods might be more substantial)
	if len(pages) > 1 {
		timeSpan := pages[len(pages)-1].CreatedAt.Sub(pages[0].CreatedAt)
		if timeSpan > 5*time.Minute && timeSpan < 2*time.Hour {
			qualityScore += 0.1 // Good conversation duration
		}
	}

	// Normalize to [0, 1] range
	if qualityScore > 1.0 {
		qualityScore = 1.0
	} else if qualityScore < 0.0 {
		qualityScore = 0.0
	}

	return qualityScore, nil
}

// extractKeywords extracts relevant keywords from a segment
func (pp *ParallelProcessor) extractKeywords(ctx context.Context, segment *models.Segment) ([]string, error) {
	if segment.TopicSummary == "" {
		return []string{}, nil
	}

	// Simple keyword extraction (in production, you might use NLP libraries)
	summary := strings.ToLower(segment.TopicSummary)

	// Common technical and conversation keywords
	importantKeywords := []string{
		"error", "problem", "issue", "bug", "fix", "solution",
		"python", "golang", "javascript", "database", "api",
		"deployment", "server", "configuration", "authentication",
		"urgent", "critical", "important", "help", "question",
		"tutorial", "guide", "example", "documentation",
		"performance", "optimization", "security", "backup",
	}

	var foundKeywords []string
	for _, keyword := range importantKeywords {
		if strings.Contains(summary, keyword) {
			foundKeywords = append(foundKeywords, keyword)
		}
	}

	return foundKeywords, nil
}

// ProcessUserProfileAnalysis performs parallel user profile and knowledge extraction
func (pp *ParallelProcessor) ProcessUserProfileAnalysis(ctx context.Context, segments []*models.Segment) (*ProfileAnalysisResult, error) {
	if len(segments) == 0 {
		return &ProfileAnalysisResult{}, nil
	}

	start := time.Now()
	var wg sync.WaitGroup

	// Results channels
	profileChan := make(chan string, 1)
	knowledgeChan := make(chan []string, 1)
	errorChan := make(chan error, 2)

	// Parallel profile analysis
	wg.Add(1)
	go func() {
		defer wg.Done()
		pp.semaphore <- struct{}{}
		defer func() { <-pp.semaphore }()

		profile, err := pp.analyzeUserProfile(ctx, segments)
		if err != nil {
			errorChan <- err
			return
		}
		profileChan <- profile
	}()

	// Parallel knowledge extraction
	wg.Add(1)
	go func() {
		defer wg.Done()
		pp.semaphore <- struct{}{}
		defer func() { <-pp.semaphore }()

		knowledge, err := pp.extractUserKnowledge(ctx, segments)
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

	// Check for errors
	for err := range errorChan {
		if err != nil {
			return nil, err
		}
	}

	// Collect results
	var profile string
	var knowledge []string

	if p := <-profileChan; p != "" {
		profile = p
	}

	if k := <-knowledgeChan; len(k) > 0 {
		knowledge = k
	}

	duration := time.Since(start)
	log.Printf("INFO: Parallel profile analysis completed - Segments: %d, Duration: %v", len(segments), duration)

	return &ProfileAnalysisResult{
		UserProfile:        profile,
		ExtractedKnowledge: knowledge,
		AnalyzedSegments:   len(segments),
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

// analyzeUserProfile analyzes user characteristics from segments
func (pp *ParallelProcessor) analyzeUserProfile(ctx context.Context, segments []*models.Segment) (string, error) {
	// Simplified profile analysis - in production this would use LLM
	var interests []string
	var skills []string

	for _, segment := range segments {
		summary := strings.ToLower(segment.TopicSummary)

		// Detect interests
		if strings.Contains(summary, "python") || strings.Contains(summary, "programming") {
			interests = append(interests, "programming")
		}
		if strings.Contains(summary, "machine learning") || strings.Contains(summary, "ai") {
			interests = append(interests, "machine learning")
		}
		if strings.Contains(summary, "database") || strings.Contains(summary, "sql") {
			interests = append(interests, "databases")
		}

		// Detect skill level
		if strings.Contains(summary, "advanced") || strings.Contains(summary, "expert") {
			skills = append(skills, "advanced")
		} else if strings.Contains(summary, "beginner") || strings.Contains(summary, "learning") {
			skills = append(skills, "beginner")
		}
	}

	// Remove duplicates
	interests = uniqueStrings(interests)
	skills = uniqueStrings(skills)

	profile := fmt.Sprintf("User shows interest in: %v. Skill level appears to be: %v. Based on %d conversation segments.",
		interests, skills, len(segments))

	return profile, nil
}

// extractUserKnowledge extracts factual knowledge from segments
func (pp *ParallelProcessor) extractUserKnowledge(ctx context.Context, segments []*models.Segment) ([]string, error) {
	var knowledge []string

	for _, segment := range segments {
		// Extract factual statements from summaries
		if strings.Contains(segment.TopicSummary, "uses") {
			knowledge = append(knowledge, fmt.Sprintf("User uses: %s", segment.TopicSummary))
		}
		if strings.Contains(segment.TopicSummary, "prefers") {
			knowledge = append(knowledge, fmt.Sprintf("User prefers: %s", segment.TopicSummary))
		}
		if strings.Contains(segment.TopicSummary, "works with") {
			knowledge = append(knowledge, fmt.Sprintf("User works with: %s", segment.TopicSummary))
		}
	}

	return uniqueStrings(knowledge), nil
}

// uniqueStrings removes duplicate strings from a slice
func uniqueStrings(strings []string) []string {
	keys := make(map[string]bool)
	var unique []string

	for _, str := range strings {
		if !keys[str] {
			keys[str] = true
			unique = append(unique, str)
		}
	}

	return unique
}
