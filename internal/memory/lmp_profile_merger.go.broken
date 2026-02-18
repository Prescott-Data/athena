package memory

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ProfileMerger handles intelligent merging of personality profiles and updates
type ProfileMerger struct {
	db     *mongo.Database
	config PersonalityAnalysisConfig
}

// MergeResult represents the result of a profile merge operation
type MergeResult struct {
	UserID             string            `json:"user_id"`
	PreviousProfile    *UserPersona      `json:"previous_profile,omitempty"`
	UpdatedProfile     *UserPersona      `json:"updated_profile"`
	MergedDimensions   []string          `json:"merged_dimensions"`
	NewDimensions      []string          `json:"new_dimensions"`
	RemovedDimensions  []string          `json:"removed_dimensions"`
	ConfidenceChanged  bool              `json:"confidence_changed"`
	SignificantUpdates []DimensionUpdate `json:"significant_updates"`
	MergeStrategy      string            `json:"merge_strategy"`
	ProcessingTime     time.Duration     `json:"processing_time"`
}

// MergeStrategy defines how profiles should be merged
type MergeStrategy string

const (
	MergeStrategyConservative  MergeStrategy = "conservative"  // Require high confidence for updates
	MergeStrategyBalanced      MergeStrategy = "balanced"      // Balance old and new observations
	MergeStrategyAggressive    MergeStrategy = "aggressive"    // Favor recent observations
	MergeStrategyReinforcement MergeStrategy = "reinforcement" // Only strengthen existing dimensions
)

// NewProfileMerger creates a new profile merger
func NewProfileMerger(db *mongo.Database) *ProfileMerger {
	return &ProfileMerger{
		db:     db,
		config: GetDefaultPersonalityAnalysisConfig(),
	}
}

// MergeProfile merges new personality analysis results with existing profile
func (pm *ProfileMerger) MergeProfile(ctx context.Context, userID string, analysisResult *PersonalityAnalysisResult, strategy MergeStrategy) (*MergeResult, error) {
	start := time.Now()

	log.Printf("INFO: Merging profile for user %s with %d dimension updates using %s strategy",
		userID, len(analysisResult.UpdatedDimensions), strategy)

	// Get existing profile
	existingProfile, err := pm.getExistingProfile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing profile: %w", err)
	}

	// If no existing profile, create new one
	if existingProfile == nil {
		newProfile, err := pm.createNewProfile(userID, analysisResult)
		if err != nil {
			return nil, fmt.Errorf("failed to create new profile: %w", err)
		}

		// Save new profile
		if err := pm.saveProfile(ctx, newProfile); err != nil {
			return nil, fmt.Errorf("failed to save new profile: %w", err)
		}

		result := &MergeResult{
			UserID:         userID,
			UpdatedProfile: newProfile,
			NewDimensions:  pm.extractDimensionNames(analysisResult.UpdatedDimensions),
			MergeStrategy:  string(strategy),
			ProcessingTime: time.Since(start),
		}

		log.Printf("INFO: Created new profile for user %s with %d dimensions", userID, len(analysisResult.UpdatedDimensions))
		return result, nil
	}

	// Merge with existing profile
	mergeResult, err := pm.mergeWithExistingProfile(ctx, existingProfile, analysisResult, strategy)
	if err != nil {
		return nil, fmt.Errorf("failed to merge with existing profile: %w", err)
	}

	mergeResult.ProcessingTime = time.Since(start)

	// Save updated profile
	if err := pm.saveProfile(ctx, mergeResult.UpdatedProfile); err != nil {
		return nil, fmt.Errorf("failed to save updated profile: %w", err)
	}

	log.Printf("INFO: Profile merge completed for user %s - New: %d, Merged: %d, Removed: %d, Duration: %v",
		userID, len(mergeResult.NewDimensions), len(mergeResult.MergedDimensions),
		len(mergeResult.RemovedDimensions), mergeResult.ProcessingTime)

	return mergeResult, nil
}

// getExistingProfile retrieves existing user profile from database
func (pm *ProfileMerger) getExistingProfile(ctx context.Context, userID string) (*UserPersona, error) {
	col := pm.db.Collection("user_personas")

	var profile UserPersona
	err := col.FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil // No existing profile
		}
		return nil, err
	}

	return &profile, nil
}

// createNewProfile creates a new user profile from analysis results
func (pm *ProfileMerger) createNewProfile(userID string, analysisResult *PersonalityAnalysisResult) (*UserPersona, error) {
	now := time.Now()

	profile := &UserPersona{
		UserID:             userID,
		PsychologicalModel: &PsychologicalDimensions{},
		AIAlignment:        &AIAlignmentDimensions{},
		ContentInterests:   &ContentInterestTags{},
		ProfileVersion:     1,
		LastAnalysisTime:   now,
		TotalInteractions:  1,
		UserFacts:          analysisResult.ExtractedFacts,
		AssistantKnowledge: analysisResult.AssistantKnowledge,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	// Apply dimension updates
	for _, update := range analysisResult.UpdatedDimensions {
		if err := pm.applyDimensionUpdate(profile, &update); err != nil {
			log.Printf("WARN: Failed to apply dimension update %s: %v", update.DimensionName, err)
		}
	}

	// Calculate overall confidence
	profile.ConfidenceScore = pm.calculateOverallConfidence(profile)

	return profile, nil
}

// mergeWithExistingProfile merges new analysis with existing profile
func (pm *ProfileMerger) mergeWithExistingProfile(ctx context.Context, existingProfile *UserPersona, analysisResult *PersonalityAnalysisResult, strategy MergeStrategy) (*MergeResult, error) {
	// Create a copy of existing profile for updates
	updatedProfile := pm.copyProfile(existingProfile)
	updatedProfile.ProfileVersion++
	updatedProfile.LastAnalysisTime = time.Now()
	updatedProfile.TotalInteractions++
	updatedProfile.UpdatedAt = time.Now()

	result := &MergeResult{
		UserID:             existingProfile.UserID,
		PreviousProfile:    existingProfile,
		UpdatedProfile:     updatedProfile,
		MergedDimensions:   []string{},
		NewDimensions:      []string{},
		RemovedDimensions:  []string{},
		SignificantUpdates: []DimensionUpdate{},
		MergeStrategy:      string(strategy),
	}

	// Merge dimension updates
	for _, update := range analysisResult.UpdatedDimensions {
		mergeType, significant, err := pm.mergeDimensionUpdate(updatedProfile, &update, strategy)
		if err != nil {
			log.Printf("WARN: Failed to merge dimension %s: %v", update.DimensionName, err)
			continue
		}

		switch mergeType {
		case "new":
			result.NewDimensions = append(result.NewDimensions, update.DimensionName)
		case "merged":
			result.MergedDimensions = append(result.MergedDimensions, update.DimensionName)
		case "unchanged":
			// Dimension was not significantly changed
		}

		if significant {
			result.SignificantUpdates = append(result.SignificantUpdates, update)
		}
	}

	// Merge user facts
	pm.mergeUserFacts(updatedProfile, analysisResult.ExtractedFacts, strategy)

	// Merge assistant knowledge
	pm.mergeAssistantKnowledge(updatedProfile, analysisResult.AssistantKnowledge)

	// Update overall confidence
	oldConfidence := existingProfile.ConfidenceScore
	updatedProfile.ConfidenceScore = pm.calculateOverallConfidence(updatedProfile)
	result.ConfidenceChanged = math.Abs(oldConfidence-updatedProfile.ConfidenceScore) > 0.05

	// Clean up old dimensions if strategy allows
	if strategy == MergeStrategyAggressive {
		removedDims := pm.cleanupLowConfidenceDimensions(updatedProfile)
		result.RemovedDimensions = removedDims
	}

	return result, nil
}

// mergeDimensionUpdate merges a single dimension update with existing profile
func (pm *ProfileMerger) mergeDimensionUpdate(profile *UserPersona, update *DimensionUpdate, strategy MergeStrategy) (string, bool, error) {
	existingDimension := pm.getDimensionScore(profile, update.DimensionName, update.DimensionType)

	// If dimension doesn't exist, add it
	if existingDimension == nil {
		if update.NewScore.Confidence >= pm.config.MinConfidenceThreshold {
			err := pm.applyDimensionUpdate(profile, update)
			return "new", true, err
		}
		return "unchanged", false, nil
	}

	// Merge with existing dimension based on strategy
	mergedScore, significant := pm.calculateMergedScore(existingDimension, update.NewScore, strategy)

	// Only update if change is significant
	if significant {
		update.OldScore = existingDimension
		update.NewScore = mergedScore
		err := pm.applyDimensionUpdate(profile, update)
		return "merged", true, err
	}

	return "unchanged", false, nil
}

// calculateMergedScore calculates the merged score based on strategy
func (pm *ProfileMerger) calculateMergedScore(existing, new *DimensionScore, strategy MergeStrategy) (*DimensionScore, bool) {
	switch strategy {
	case MergeStrategyConservative:
		return pm.conservativeMerge(existing, new)
	case MergeStrategyBalanced:
		return pm.balancedMerge(existing, new)
	case MergeStrategyAggressive:
		return pm.aggressiveMerge(existing, new)
	case MergeStrategyReinforcement:
		return pm.reinforcementMerge(existing, new)
	default:
		return pm.balancedMerge(existing, new)
	}
}

// conservativeMerge only updates if new evidence is very strong
func (pm *ProfileMerger) conservativeMerge(existing, new *DimensionScore) (*DimensionScore, bool) {
	// Require high confidence and multiple observations for conservative updates
	if new.Confidence < 0.8 {
		return existing, false
	}

	// If levels are the same, increase confidence
	if existing.Level == new.Level {
		merged := &DimensionScore{
			Level:            existing.Level,
			Confidence:       math.Min(1.0, (existing.Confidence+new.Confidence)/2+0.1),
			Evidence:         fmt.Sprintf("%s; %s", existing.Evidence, new.Evidence),
			LastObserved:     time.Now(),
			ObservationCount: existing.ObservationCount + 1,
		}
		return merged, true
	}

	// Only change level if new confidence is much higher
	if new.Confidence > existing.Confidence+0.3 {
		merged := &DimensionScore{
			Level:            new.Level,
			Confidence:       (existing.Confidence + new.Confidence) / 2,
			Evidence:         fmt.Sprintf("Updated: %s (was: %s)", new.Evidence, existing.Evidence),
			LastObserved:     time.Now(),
			ObservationCount: existing.ObservationCount + 1,
		}
		return merged, true
	}

	return existing, false
}

// balancedMerge balances old and new observations
func (pm *ProfileMerger) balancedMerge(existing, new *DimensionScore) (*DimensionScore, bool) {
	// Weight based on observation count and confidence
	oldWeight := float64(existing.ObservationCount) * existing.Confidence
	newWeight := float64(new.ObservationCount) * new.Confidence
	totalWeight := oldWeight + newWeight

	if totalWeight == 0 {
		return existing, false
	}

	// Calculate weighted confidence
	weightedConfidence := (oldWeight*existing.Confidence + newWeight*new.Confidence) / totalWeight

	// Determine level based on weighted average
	level := existing.Level
	if new.Confidence > existing.Confidence+pm.config.DimensionUpdateThreshold {
		level = new.Level
	}

	merged := &DimensionScore{
		Level:            level,
		Confidence:       math.Min(1.0, weightedConfidence),
		Evidence:         fmt.Sprintf("%s; %s", existing.Evidence, new.Evidence),
		LastObserved:     time.Now(),
		ObservationCount: existing.ObservationCount + new.ObservationCount,
	}

	// Check if change is significant
	significant := math.Abs(merged.Confidence-existing.Confidence) > pm.config.DimensionUpdateThreshold ||
		merged.Level != existing.Level

	return merged, significant
}

// aggressiveMerge favors recent observations
func (pm *ProfileMerger) aggressiveMerge(existing, new *DimensionScore) (*DimensionScore, bool) {
	// Give more weight to recent observations
	recentWeight := 0.7
	existingWeight := 0.3

	mergedConfidence := existingWeight*existing.Confidence + recentWeight*new.Confidence

	// Prefer new level if confidence is reasonable
	level := new.Level
	if new.Confidence < 0.4 {
		level = existing.Level
	}

	merged := &DimensionScore{
		Level:            level,
		Confidence:       math.Min(1.0, mergedConfidence),
		Evidence:         fmt.Sprintf("Recent: %s (Previous: %s)", new.Evidence, existing.Evidence),
		LastObserved:     time.Now(),
		ObservationCount: existing.ObservationCount + 1,
	}

	return merged, true // Always consider aggressive merges significant
}

// reinforcementMerge only strengthens existing dimensions
func (pm *ProfileMerger) reinforcementMerge(existing, new *DimensionScore) (*DimensionScore, bool) {
	// Only update if new observation reinforces existing level
	if existing.Level != new.Level {
		return existing, false
	}

	// Strengthen confidence for matching observations
	merged := &DimensionScore{
		Level:            existing.Level,
		Confidence:       math.Min(1.0, existing.Confidence+0.1),
		Evidence:         fmt.Sprintf("%s; Reinforced: %s", existing.Evidence, new.Evidence),
		LastObserved:     time.Now(),
		ObservationCount: existing.ObservationCount + 1,
	}

	return merged, true
}

// getDimensionScore retrieves existing dimension score from profile
func (pm *ProfileMerger) getDimensionScore(profile *UserPersona, dimensionName string, dimensionType DimensionType) *DimensionScore {
	switch dimensionType {
	case DimensionTypePsychological:
		return pm.getPsychologicalDimension(profile.PsychologicalModel, dimensionName)
	case DimensionTypeAIAlignment:
		return pm.getAIAlignmentDimension(profile.AIAlignment, dimensionName)
	case DimensionTypeContentInterest:
		return pm.getContentInterestDimension(profile.ContentInterests, dimensionName)
	}
	return nil
}

// Helper methods to get specific dimension scores
func (pm *ProfileMerger) getPsychologicalDimension(psych *PsychologicalDimensions, name string) *DimensionScore {
	if psych == nil {
		return nil
	}

	switch name {
	case "extraversion":
		return psych.Extraversion
	case "openness":
		return psych.Openness
	case "agreeableness":
		return psych.Agreeableness
	case "conscientiousness":
		return psych.Conscientiousness
	case "neuroticism":
		return psych.Neuroticism
	case "physiological_needs":
		return psych.PhysiologicalNeeds
	case "security_need":
		return psych.SecurityNeed
	case "belonging_need":
		return psych.BelongingNeed
	case "self_esteem_need":
		return psych.SelfEsteemNeed
	case "cognitive_needs":
		return psych.CognitiveNeeds
	case "aesthetic_needs":
		return psych.AestheticNeeds
	case "self_actualization":
		return psych.SelfActualization
	case "order_need":
		return psych.OrderNeed
	case "autonomy_need":
		return psych.AutonomyNeed
	case "power_need":
		return psych.PowerNeed
	case "achievement_need":
		return psych.AchievementNeed
	}
	return nil
}

func (pm *ProfileMerger) getAIAlignmentDimension(ai *AIAlignmentDimensions, name string) *DimensionScore {
	if ai == nil {
		return nil
	}

	switch name {
	case "helpfulness":
		return ai.Helpfulness
	case "honesty":
		return ai.Honesty
	case "safety":
		return ai.Safety
	case "instruction_compliance":
		return ai.InstructionCompliance
	case "truthfulness":
		return ai.Truthfulness
	case "coherence":
		return ai.Coherence
	case "complexity_preference":
		return ai.ComplexityPreference
	case "conciseness_preference":
		return ai.ConcisenessPreference
	}
	return nil
}

func (pm *ProfileMerger) getContentInterestDimension(content *ContentInterestTags, name string) *DimensionScore {
	if content == nil {
		return nil
	}

	switch name {
	case "science_interest":
		return content.ScienceInterest
	case "education_interest":
		return content.EducationInterest
	case "psychology_interest":
		return content.PsychologyInterest
	case "family_concern":
		return content.FamilyConcern
	case "fashion_interest":
		return content.FashionInterest
	case "art_interest":
		return content.ArtInterest
	case "health_concern":
		return content.HealthConcern
	case "financial_interest":
		return content.FinancialInterest
	case "sports_interest":
		return content.SportsInterest
	case "food_interest":
		return content.FoodInterest
	case "travel_interest":
		return content.TravelInterest
	case "music_interest":
		return content.MusicInterest
	case "literature_interest":
		return content.LiteratureInterest
	case "film_interest":
		return content.FilmInterest
	case "social_media_activity":
		return content.SocialMediaActivity
	case "tech_interest":
		return content.TechInterest
	case "environmental_concern":
		return content.EnvironmentalConcern
	case "history_interest":
		return content.HistoryInterest
	case "political_concern":
		return content.PoliticalConcern
	case "religious_interest":
		return content.ReligiousInterest
	case "gaming_interest":
		return content.GamingInterest
	case "animal_concern":
		return content.AnimalConcern
	case "emotional_expression":
		return content.EmotionalExpression
	case "sense_of_humor":
		return content.SenseOfHumor
	case "information_density":
		return content.InformationDensity
	case "language_style":
		return content.LanguageStyle
	case "practicality_focus":
		return content.PracticalityFocus
	}
	return nil
}

// applyDimensionUpdate applies a dimension update to the profile
func (pm *ProfileMerger) applyDimensionUpdate(profile *UserPersona, update *DimensionUpdate) error {
	switch update.DimensionType {
	case DimensionTypePsychological:
		return pm.applyPsychologicalUpdate(profile.PsychologicalModel, update.DimensionName, update.NewScore)
	case DimensionTypeAIAlignment:
		return pm.applyAIAlignmentUpdate(profile.AIAlignment, update.DimensionName, update.NewScore)
	case DimensionTypeContentInterest:
		return pm.applyContentInterestUpdate(profile.ContentInterests, update.DimensionName, update.NewScore)
	default:
		return fmt.Errorf("unknown dimension type: %s", update.DimensionType)
	}
}

// Helper methods to apply specific dimension updates
func (pm *ProfileMerger) applyPsychologicalUpdate(psych *PsychologicalDimensions, name string, score *DimensionScore) error {
	switch name {
	case "extraversion":
		psych.Extraversion = score
	case "openness":
		psych.Openness = score
	case "agreeableness":
		psych.Agreeableness = score
	case "conscientiousness":
		psych.Conscientiousness = score
	case "neuroticism":
		psych.Neuroticism = score
	case "physiological_needs":
		psych.PhysiologicalNeeds = score
	case "security_need":
		psych.SecurityNeed = score
	case "belonging_need":
		psych.BelongingNeed = score
	case "self_esteem_need":
		psych.SelfEsteemNeed = score
	case "cognitive_needs":
		psych.CognitiveNeeds = score
	case "aesthetic_needs":
		psych.AestheticNeeds = score
	case "self_actualization":
		psych.SelfActualization = score
	case "order_need":
		psych.OrderNeed = score
	case "autonomy_need":
		psych.AutonomyNeed = score
	case "power_need":
		psych.PowerNeed = score
	case "achievement_need":
		psych.AchievementNeed = score
	default:
		return fmt.Errorf("unknown psychological dimension: %s", name)
	}
	return nil
}

func (pm *ProfileMerger) applyAIAlignmentUpdate(ai *AIAlignmentDimensions, name string, score *DimensionScore) error {
	switch name {
	case "helpfulness":
		ai.Helpfulness = score
	case "honesty":
		ai.Honesty = score
	case "safety":
		ai.Safety = score
	case "instruction_compliance":
		ai.InstructionCompliance = score
	case "truthfulness":
		ai.Truthfulness = score
	case "coherence":
		ai.Coherence = score
	case "complexity_preference":
		ai.ComplexityPreference = score
	case "conciseness_preference":
		ai.ConcisenessPreference = score
	default:
		return fmt.Errorf("unknown AI alignment dimension: %s", name)
	}
	return nil
}

func (pm *ProfileMerger) applyContentInterestUpdate(content *ContentInterestTags, name string, score *DimensionScore) error {
	switch name {
	case "science_interest":
		content.ScienceInterest = score
	case "education_interest":
		content.EducationInterest = score
	case "psychology_interest":
		content.PsychologyInterest = score
	case "family_concern":
		content.FamilyConcern = score
	case "fashion_interest":
		content.FashionInterest = score
	case "art_interest":
		content.ArtInterest = score
	case "health_concern":
		content.HealthConcern = score
	case "financial_interest":
		content.FinancialInterest = score
	case "sports_interest":
		content.SportsInterest = score
	case "food_interest":
		content.FoodInterest = score
	case "travel_interest":
		content.TravelInterest = score
	case "music_interest":
		content.MusicInterest = score
	case "literature_interest":
		content.LiteratureInterest = score
	case "film_interest":
		content.FilmInterest = score
	case "social_media_activity":
		content.SocialMediaActivity = score
	case "tech_interest":
		content.TechInterest = score
	case "environmental_concern":
		content.EnvironmentalConcern = score
	case "history_interest":
		content.HistoryInterest = score
	case "political_concern":
		content.PoliticalConcern = score
	case "religious_interest":
		content.ReligiousInterest = score
	case "gaming_interest":
		content.GamingInterest = score
	case "animal_concern":
		content.AnimalConcern = score
	case "emotional_expression":
		content.EmotionalExpression = score
	case "sense_of_humor":
		content.SenseOfHumor = score
	case "information_density":
		content.InformationDensity = score
	case "language_style":
		content.LanguageStyle = score
	case "practicality_focus":
		content.PracticalityFocus = score
	default:
		return fmt.Errorf("unknown content interest dimension: %s", name)
	}
	return nil
}

// mergeUserFacts merges new user facts with existing ones
func (pm *ProfileMerger) mergeUserFacts(profile *UserPersona, newFacts []UserFactEntry, strategy MergeStrategy) {
	// Remove old facts that exceed retention period
	retentionCutoff := time.Now().AddDate(0, 0, -pm.config.FactRetentionDays)
	var validFacts []UserFactEntry

	for _, fact := range profile.UserFacts {
		if fact.ExtractedAt.After(retentionCutoff) {
			validFacts = append(validFacts, fact)
		}
	}

	// Add new facts, checking for duplicates
	factMap := make(map[string]*UserFactEntry)
	for i, fact := range validFacts {
		factMap[fact.Content] = &validFacts[i]
	}

	for _, newFact := range newFacts {
		if existingFact, exists := factMap[newFact.Content]; exists {
			// Update confidence if new fact has higher confidence
			if newFact.Confidence > existingFact.Confidence {
				existingFact.Confidence = newFact.Confidence
				existingFact.UpdatedAt = time.Now()
			}
		} else {
			// Add new fact if we haven't exceeded category limits
			categoryCount := pm.countFactsByCategory(validFacts, newFact.Category)
			if categoryCount < pm.config.MaxFactsPerCategory {
				validFacts = append(validFacts, newFact)
				factMap[newFact.Content] = &newFact
			}
		}
	}

	profile.UserFacts = validFacts
}

// mergeAssistantKnowledge merges new assistant knowledge with existing
func (pm *ProfileMerger) mergeAssistantKnowledge(profile *UserPersona, newKnowledge []AssistantKnowledgeEntry) {
	// Simple append for now - in production might want deduplication
	profile.AssistantKnowledge = append(profile.AssistantKnowledge, newKnowledge...)

	// Keep only recent knowledge (last 100 entries)
	if len(profile.AssistantKnowledge) > 100 {
		profile.AssistantKnowledge = profile.AssistantKnowledge[len(profile.AssistantKnowledge)-100:]
	}
}

// Utility functions
func (pm *ProfileMerger) copyProfile(original *UserPersona) *UserPersona {
	// Create a deep copy of the profile
	copied := *original

	// Deep copy nested structures
	if original.PsychologicalModel != nil {
		copied.PsychologicalModel = &(*original.PsychologicalModel)
	}
	if original.AIAlignment != nil {
		copied.AIAlignment = &(*original.AIAlignment)
	}
	if original.ContentInterests != nil {
		copied.ContentInterests = &(*original.ContentInterests)
	}

	// Copy slices
	copied.UserFacts = make([]UserFactEntry, len(original.UserFacts))
	copy(copied.UserFacts, original.UserFacts)

	copied.AssistantKnowledge = make([]AssistantKnowledgeEntry, len(original.AssistantKnowledge))
	copy(copied.AssistantKnowledge, original.AssistantKnowledge)

	return &copied
}

func (pm *ProfileMerger) calculateOverallConfidence(profile *UserPersona) float64 {
	totalConfidence := 0.0
	dimensionCount := 0

	// Count all non-nil dimensions and sum their confidence
	if profile.PsychologicalModel != nil {
		totalConfidence += pm.sumPsychologicalConfidence(profile.PsychologicalModel, &dimensionCount)
	}
	if profile.AIAlignment != nil {
		totalConfidence += pm.sumAIAlignmentConfidence(profile.AIAlignment, &dimensionCount)
	}
	if profile.ContentInterests != nil {
		totalConfidence += pm.sumContentInterestConfidence(profile.ContentInterests, &dimensionCount)
	}

	if dimensionCount == 0 {
		return 0.0
	}

	return totalConfidence / float64(dimensionCount)
}

func (pm *ProfileMerger) sumPsychologicalConfidence(psych *PsychologicalDimensions, count *int) float64 {
	total := 0.0

	dimensions := []*DimensionScore{
		psych.Extraversion, psych.Openness, psych.Agreeableness, psych.Conscientiousness, psych.Neuroticism,
		psych.PhysiologicalNeeds, psych.SecurityNeed, psych.BelongingNeed, psych.SelfEsteemNeed,
		psych.CognitiveNeeds, psych.AestheticNeeds, psych.SelfActualization, psych.OrderNeed,
		psych.AutonomyNeed, psych.PowerNeed, psych.AchievementNeed,
	}

	for _, dim := range dimensions {
		if dim != nil {
			total += dim.Confidence
			*count++
		}
	}

	return total
}

func (pm *ProfileMerger) sumAIAlignmentConfidence(ai *AIAlignmentDimensions, count *int) float64 {
	total := 0.0

	dimensions := []*DimensionScore{
		ai.Helpfulness, ai.Honesty, ai.Safety, ai.InstructionCompliance,
		ai.Truthfulness, ai.Coherence, ai.ComplexityPreference, ai.ConcisenessPreference,
	}

	for _, dim := range dimensions {
		if dim != nil {
			total += dim.Confidence
			*count++
		}
	}

	return total
}

func (pm *ProfileMerger) sumContentInterestConfidence(content *ContentInterestTags, count *int) float64 {
	total := 0.0

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
			total += dim.Confidence
			*count++
		}
	}

	return total
}

func (pm *ProfileMerger) extractDimensionNames(updates []DimensionUpdate) []string {
	var names []string
	for _, update := range updates {
		names = append(names, update.DimensionName)
	}
	return names
}

func (pm *ProfileMerger) countFactsByCategory(facts []UserFactEntry, category string) int {
	count := 0
	for _, fact := range facts {
		if fact.Category == category {
			count++
		}
	}
	return count
}

func (pm *ProfileMerger) cleanupLowConfidenceDimensions(profile *UserPersona) []string {
	var removed []string
	minConfidence := 0.3 // Remove dimensions with very low confidence

	// Clean up psychological dimensions
	if profile.PsychologicalModel != nil {
		removed = append(removed, pm.cleanupPsychologicalDimensions(profile.PsychologicalModel, minConfidence)...)
	}

	// Clean up AI alignment dimensions
	if profile.AIAlignment != nil {
		removed = append(removed, pm.cleanupAIAlignmentDimensions(profile.AIAlignment, minConfidence)...)
	}

	// Clean up content interest dimensions
	if profile.ContentInterests != nil {
		removed = append(removed, pm.cleanupContentInterestDimensions(profile.ContentInterests, minConfidence)...)
	}

	return removed
}

func (pm *ProfileMerger) cleanupPsychologicalDimensions(psych *PsychologicalDimensions, minConfidence float64) []string {
	var removed []string

	if psych.Extraversion != nil && psych.Extraversion.Confidence < minConfidence {
		psych.Extraversion = nil
		removed = append(removed, "extraversion")
	}
	// Add cleanup for other psychological dimensions...

	return removed
}

func (pm *ProfileMerger) cleanupAIAlignmentDimensions(ai *AIAlignmentDimensions, minConfidence float64) []string {
	var removed []string

	if ai.Helpfulness != nil && ai.Helpfulness.Confidence < minConfidence {
		ai.Helpfulness = nil
		removed = append(removed, "helpfulness")
	}
	// Add cleanup for other AI alignment dimensions...

	return removed
}

func (pm *ProfileMerger) cleanupContentInterestDimensions(content *ContentInterestTags, minConfidence float64) []string {
	var removed []string

	if content.TechInterest != nil && content.TechInterest.Confidence < minConfidence {
		content.TechInterest = nil
		removed = append(removed, "tech_interest")
	}
	// Add cleanup for other content interest dimensions...

	return removed
}

// saveProfile saves the profile to database
func (pm *ProfileMerger) saveProfile(ctx context.Context, profile *UserPersona) error {
	col := pm.db.Collection("user_personas")

	opts := options.Replace().SetUpsert(true)
	filter := bson.M{"user_id": profile.UserID}

	_, err := col.ReplaceOne(ctx, filter, profile, opts)
	return err
}
