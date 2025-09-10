package memory

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dromos-org/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/mongo"
)

// LMPOrchestrator coordinates all Long-Term Personal Memory operations
type LMPOrchestrator struct {
	db               *mongo.Database
	profileAnalyzer  *ProfileAnalyzer
	profileMerger    *ProfileMerger
	dimensionTracker *DimensionTracker
	config           PersonalityAnalysisConfig
}

// PersonalityProcessingResult represents the complete result of personality processing
type PersonalityProcessingResult struct {
	UserID         string                     `json:"user_id"`
	AnalysisResult *PersonalityAnalysisResult `json:"analysis_result"`
	MergeResult    *MergeResult               `json:"merge_result"`
	UpdatedProfile *UserPersona               `json:"updated_profile"`
	ProcessingTime time.Duration              `json:"processing_time"`
	Method         string                     `json:"method"`
	Success        bool                       `json:"success"`
	ErrorMessage   string                     `json:"error_message,omitempty"`
}

// NewLMPOrchestrator creates a new LMP orchestrator
func NewLMPOrchestrator(db *mongo.Database) *LMPOrchestrator {
	return &LMPOrchestrator{
		db:               db,
		profileAnalyzer:  NewProfileAnalyzer(db),
		profileMerger:    NewProfileMerger(db),
		dimensionTracker: NewDimensionTracker(db),
		config:           GetDefaultPersonalityAnalysisConfig(),
	}
}

// ProcessPersonalityFromSegments processes personality analysis from conversation segments
func (lmp *LMPOrchestrator) ProcessPersonalityFromSegments(ctx context.Context, userID string, segments []models.Segment) (*PersonalityProcessingResult, error) {
	start := time.Now()

	log.Printf("INFO: Starting LMP personality processing for user %s with %d segments", userID, len(segments))

	result := &PersonalityProcessingResult{
		UserID: userID,
	}

	// Convert segments to dialogue pages for analysis
	pages, err := lmp.getDialoguePagesFromSegments(ctx, segments)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Failed to get dialogue pages: %v", err)
		return result, err
	}

	// Get existing profile for context
	existingProfile, err := lmp.profileMerger.getExistingProfile(ctx, userID)
	if err != nil {
		log.Printf("WARN: Failed to get existing profile for %s: %v", userID, err)
		// Continue without existing profile
	}

	// Prepare analysis input
	analysisInput := &ConversationAnalysisInput{
		UserID:          userID,
		Pages:           pages,
		Segments:        segments,
		ExistingProfile: existingProfile,
	}

	// Perform personality analysis
	analysisResult, err := lmp.profileAnalyzer.AnalyzeConversation(ctx, analysisInput)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Personality analysis failed: %v", err)
		return result, err
	}
	result.AnalysisResult = analysisResult

	// Merge with existing profile using balanced strategy
	mergeStrategy := MergeStrategyBalanced
	if existingProfile == nil {
		// Use conservative strategy for new profiles
		mergeStrategy = MergeStrategyConservative
	}

	mergeResult, err := lmp.profileMerger.MergeProfile(ctx, userID, analysisResult, mergeStrategy)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Profile merge failed: %v", err)
		return result, err
	}
	result.MergeResult = mergeResult
	result.UpdatedProfile = mergeResult.UpdatedProfile

	// Track dimension changes
	if err := lmp.trackDimensionChanges(ctx, mergeResult); err != nil {
		log.Printf("WARN: Failed to track dimension changes for %s: %v", userID, err)
		// Don't fail the entire operation for tracking issues
	}

	result.ProcessingTime = time.Since(start)
	result.Method = analysisResult.Method
	result.Success = true

	log.Printf("INFO: LMP personality processing completed for %s - Method: %s, Dimensions: %d, Duration: %v",
		userID, result.Method, len(analysisResult.UpdatedDimensions), result.ProcessingTime)

	return result, nil
}

// ProcessUserPersonality processes personality analysis from dialogue pages and segments
func (lmp *LMPOrchestrator) ProcessUserPersonality(ctx context.Context, userID string, pages []models.DialoguePage, segments []models.Segment) (*PersonalityProcessingResult, error) {
	// If we have segments, process them
	if len(segments) > 0 {
		return lmp.ProcessPersonalityFromSegments(ctx, userID, segments)
	}

	// Otherwise, process pages
	return lmp.ProcessPersonalityFromPages(ctx, userID, pages)
}

// ProcessPersonalityFromPages processes personality analysis directly from dialogue pages
func (lmp *LMPOrchestrator) ProcessPersonalityFromPages(ctx context.Context, userID string, pages []models.DialoguePage) (*PersonalityProcessingResult, error) {
	start := time.Now()

	log.Printf("INFO: Starting LMP personality processing for user %s with %d pages", userID, len(pages))

	result := &PersonalityProcessingResult{
		UserID: userID,
	}

	// Get existing profile for context
	existingProfile, err := lmp.profileMerger.getExistingProfile(ctx, userID)
	if err != nil {
		log.Printf("WARN: Failed to get existing profile for %s: %v", userID, err)
		// Continue without existing profile
	}

	// Prepare analysis input
	analysisInput := &ConversationAnalysisInput{
		UserID:          userID,
		Pages:           pages,
		Segments:        []models.Segment{}, // No segments in this case
		ExistingProfile: existingProfile,
	}

	// Perform personality analysis
	analysisResult, err := lmp.profileAnalyzer.AnalyzeConversation(ctx, analysisInput)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Personality analysis failed: %v", err)
		return result, err
	}
	result.AnalysisResult = analysisResult

	// Merge with existing profile
	mergeStrategy := MergeStrategyBalanced
	if existingProfile == nil {
		mergeStrategy = MergeStrategyConservative
	}

	mergeResult, err := lmp.profileMerger.MergeProfile(ctx, userID, analysisResult, mergeStrategy)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Profile merge failed: %v", err)
		return result, err
	}
	result.MergeResult = mergeResult
	result.UpdatedProfile = mergeResult.UpdatedProfile

	// Track dimension changes
	if err := lmp.trackDimensionChanges(ctx, mergeResult); err != nil {
		log.Printf("WARN: Failed to track dimension changes for %s: %v", userID, err)
	}

	result.ProcessingTime = time.Since(start)
	result.Method = analysisResult.Method
	result.Success = true

	log.Printf("INFO: LMP personality processing completed for %s - Method: %s, Dimensions: %d, Duration: %v",
		userID, result.Method, len(analysisResult.UpdatedDimensions), result.ProcessingTime)

	return result, nil
}

// GetUserPersona retrieves the complete user persona
func (lmp *LMPOrchestrator) GetUserPersona(ctx context.Context, userID string) (*UserPersona, error) {
	return lmp.profileMerger.getExistingProfile(ctx, userID)
}

// GetUserDimensionEvolutions retrieves dimension evolution history for a user
func (lmp *LMPOrchestrator) GetUserDimensionEvolutions(ctx context.Context, userID string) ([]DimensionEvolution, error) {
	return lmp.dimensionTracker.GetUserDimensionEvolutions(ctx, userID)
}

// GetDimensionAnalytics generates analytics across all users and dimensions
func (lmp *LMPOrchestrator) GetDimensionAnalytics(ctx context.Context, timeRange string) (*DimensionAnalytics, error) {
	return lmp.dimensionTracker.AnalyzeDimensionTrends(ctx, timeRange)
}

// UpdatePersonalityWithStrategy allows manual profile updates with specific merge strategy
func (lmp *LMPOrchestrator) UpdatePersonalityWithStrategy(ctx context.Context, userID string, analysisResult *PersonalityAnalysisResult, strategy MergeStrategy) (*PersonalityProcessingResult, error) {
	start := time.Now()

	result := &PersonalityProcessingResult{
		UserID:         userID,
		AnalysisResult: analysisResult,
	}

	mergeResult, err := lmp.profileMerger.MergeProfile(ctx, userID, analysisResult, strategy)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Profile merge failed: %v", err)
		return result, err
	}

	result.MergeResult = mergeResult
	result.UpdatedProfile = mergeResult.UpdatedProfile
	result.ProcessingTime = time.Since(start)
	result.Method = "manual_update"
	result.Success = true

	// Track dimension changes
	if err := lmp.trackDimensionChanges(ctx, mergeResult); err != nil {
		log.Printf("WARN: Failed to track dimension changes for %s: %v", userID, err)
	}

	return result, nil
}

// CleanupOldData performs maintenance on personality data
func (lmp *LMPOrchestrator) CleanupOldData(ctx context.Context) error {
	log.Printf("INFO: Starting LMP data cleanup")

	// Cleanup old dimension history
	if err := lmp.dimensionTracker.CleanupOldDimensionHistory(ctx); err != nil {
		log.Printf("WARN: Failed to cleanup dimension history: %v", err)
		// Continue with other cleanup tasks
	}

	// TODO: Add cleanup for old user facts, assistant knowledge, etc.

	log.Printf("INFO: LMP data cleanup completed")
	return nil
}

// ValidateUserProfile validates the consistency and quality of a user profile
func (lmp *LMPOrchestrator) ValidateUserProfile(ctx context.Context, userID string) (*ProfileValidationResult, error) {
	profile, err := lmp.GetUserPersona(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}

	if profile == nil {
		return &ProfileValidationResult{
			UserID:  userID,
			IsValid: false,
			Issues:  []string{"No profile found"},
		}, nil
	}

	return lmp.validateProfile(profile), nil
}

// ProfileValidationResult represents the result of profile validation
type ProfileValidationResult struct {
	UserID       string    `json:"user_id"`
	IsValid      bool      `json:"is_valid"`
	Issues       []string  `json:"issues"`
	Warnings     []string  `json:"warnings"`
	Suggestions  []string  `json:"suggestions"`
	OverallScore float64   `json:"overall_score"`
	ValidatedAt  time.Time `json:"validated_at"`
}

// Helper methods

// getDialoguePagesFromSegments retrieves dialogue pages associated with segments
func (lmp *LMPOrchestrator) getDialoguePagesFromSegments(ctx context.Context, segments []models.Segment) ([]models.DialoguePage, error) {
	if len(segments) == 0 {
		return []models.DialoguePage{}, nil
	}

	// TODO: Implement actual page retrieval from segments
	// For now, return empty slice - in production this would query MongoDB
	// to get all pages referenced by the segments' PageIDs

	log.Printf("INFO: Would retrieve dialogue pages for %d segments", len(segments))
	return []models.DialoguePage{}, nil
}

// trackDimensionChanges records dimension changes for historical tracking
func (lmp *LMPOrchestrator) trackDimensionChanges(ctx context.Context, mergeResult *MergeResult) error {
	for _, update := range mergeResult.SignificantUpdates {
		err := lmp.dimensionTracker.TrackDimensionChange(
			ctx,
			mergeResult.UserID,
			update.DimensionName,
			update.DimensionType,
			update.OldScore,
			update.NewScore,
			update.UpdateReason,
			mergeResult.UpdatedProfile.ProfileVersion,
		)
		if err != nil {
			log.Printf("WARN: Failed to track dimension change %s for user %s: %v",
				update.DimensionName, mergeResult.UserID, err)
		}
	}
	return nil
}

// validateProfile validates the consistency and quality of a profile
func (lmp *LMPOrchestrator) validateProfile(profile *UserPersona) *ProfileValidationResult {
	result := &ProfileValidationResult{
		UserID:      profile.UserID,
		IsValid:     true,
		Issues:      []string{},
		Warnings:    []string{},
		Suggestions: []string{},
		ValidatedAt: time.Now(),
	}

	// Check basic profile completeness
	if profile.ProfileVersion == 0 {
		result.Issues = append(result.Issues, "Profile version is 0")
		result.IsValid = false
	}

	if profile.ConfidenceScore < 0.3 {
		result.Warnings = append(result.Warnings, "Overall confidence score is low")
	}

	if profile.TotalInteractions < 3 {
		result.Warnings = append(result.Warnings, "Profile based on few interactions")
	}

	// Check dimension consistency
	dimensionCount := 0
	totalConfidence := 0.0

	if profile.PsychologicalModel != nil {
		count, confidence := lmp.validatePsychologicalDimensions(profile.PsychologicalModel, result)
		dimensionCount += count
		totalConfidence += confidence
	}

	if profile.AIAlignment != nil {
		count, confidence := lmp.validateAIAlignmentDimensions(profile.AIAlignment, result)
		dimensionCount += count
		totalConfidence += confidence
	}

	if profile.ContentInterests != nil {
		count, confidence := lmp.validateContentInterestDimensions(profile.ContentInterests, result)
		dimensionCount += count
		totalConfidence += confidence
	}

	// Calculate overall score
	if dimensionCount > 0 {
		result.OverallScore = totalConfidence / float64(dimensionCount)
	}

	// Add suggestions based on analysis
	if dimensionCount < 5 {
		result.Suggestions = append(result.Suggestions, "Profile could benefit from more conversation data")
	}

	if result.OverallScore > 0.8 {
		result.Suggestions = append(result.Suggestions, "High-quality profile - good for personalization")
	}

	return result
}

func (lmp *LMPOrchestrator) validatePsychologicalDimensions(psych *PsychologicalDimensions, result *ProfileValidationResult) (int, float64) {
	count := 0
	totalConfidence := 0.0

	dimensions := []*DimensionScore{
		psych.Extraversion, psych.Openness, psych.Agreeableness, psych.Conscientiousness, psych.Neuroticism,
		psych.PhysiologicalNeeds, psych.SecurityNeed, psych.BelongingNeed, psych.SelfEsteemNeed,
		psych.CognitiveNeeds, psych.AestheticNeeds, psych.SelfActualization, psych.OrderNeed,
		psych.AutonomyNeed, psych.PowerNeed, psych.AchievementNeed,
	}

	for _, dim := range dimensions {
		if dim != nil {
			count++
			totalConfidence += dim.Confidence

			if dim.Confidence < 0.3 {
				result.Warnings = append(result.Warnings, "Low confidence psychological dimension detected")
			}
		}
	}

	return count, totalConfidence
}

func (lmp *LMPOrchestrator) validateAIAlignmentDimensions(ai *AIAlignmentDimensions, result *ProfileValidationResult) (int, float64) {
	count := 0
	totalConfidence := 0.0

	dimensions := []*DimensionScore{
		ai.Helpfulness, ai.Honesty, ai.Safety, ai.InstructionCompliance,
		ai.Truthfulness, ai.Coherence, ai.ComplexityPreference, ai.ConcisenessPreference,
	}

	for _, dim := range dimensions {
		if dim != nil {
			count++
			totalConfidence += dim.Confidence

			if dim.Confidence < 0.3 {
				result.Warnings = append(result.Warnings, "Low confidence AI alignment dimension detected")
			}
		}
	}

	return count, totalConfidence
}

func (lmp *LMPOrchestrator) validateContentInterestDimensions(content *ContentInterestTags, result *ProfileValidationResult) (int, float64) {
	count := 0
	totalConfidence := 0.0

	dimensions := []*DimensionScore{
		content.ScienceInterest, content.EducationInterest, content.PsychologyInterest, content.FamilyConcern,
		content.FashionInterest, content.ArtInterest, content.HealthConcern, content.FinancialInterest,
		content.SportsInterest, content.FoodInterest, content.TravelInterest, content.MusicInterest,
		content.LiteratureInterest, content.FilmInterest, content.SocialMediaActivity, content.TechInterest,
		content.EnvironmentalConcern, content.HistoryInterest, content.PoliticalConcern, content.ReligiousInterest,
		content.GamingInterest, content.AnimalConcern, content.EmotionalExpression, content.SenseOfHumor,
		content.InformationDensity, content.LanguageStyle, content.PracticalityFocus,
	}

	for _, dim := range dimensions {
		if dim != nil {
			count++
			totalConfidence += dim.Confidence

			if dim.Confidence < 0.3 {
				result.Warnings = append(result.Warnings, "Low confidence content interest dimension detected")
			}
		}
	}

	return count, totalConfidence
}

// RunPeriodicMaintenance performs periodic maintenance tasks
func (lmp *LMPOrchestrator) RunPeriodicMaintenance(ctx context.Context) error {
	log.Printf("INFO: Starting LMP periodic maintenance")

	// Cleanup old data
	if err := lmp.CleanupOldData(ctx); err != nil {
		log.Printf("WARN: Maintenance cleanup failed: %v", err)
	}

	// TODO: Add other maintenance tasks like:
	// - Profile quality analysis
	// - Dimension trend analysis
	// - User engagement metrics update

	log.Printf("INFO: LMP periodic maintenance completed")
	return nil
}
