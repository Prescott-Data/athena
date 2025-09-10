package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// UserPersona represents a comprehensive 90-dimension user profile
type UserPersona struct {
	ID     primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UserID string             `json:"user_id" bson:"user_id"`

	// Core personality dimensions
	PsychologicalModel *PsychologicalDimensions `json:"psychological_model" bson:"psychological_model"`
	AIAlignment        *AIAlignmentDimensions   `json:"ai_alignment" bson:"ai_alignment"`
	ContentInterests   *ContentInterestTags     `json:"content_interests" bson:"content_interests"`

	// Profile metadata
	ProfileVersion    int       `json:"profile_version" bson:"profile_version"`
	LastAnalysisTime  time.Time `json:"last_analysis_time" bson:"last_analysis_time"`
	ConfidenceScore   float64   `json:"confidence_score" bson:"confidence_score"`
	TotalInteractions int       `json:"total_interactions" bson:"total_interactions"`

	// Knowledge extraction
	UserFacts          []UserFactEntry           `json:"user_facts" bson:"user_facts"`
	AssistantKnowledge []AssistantKnowledgeEntry `json:"assistant_knowledge" bson:"assistant_knowledge"`

	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// PsychologicalDimensions represents the 16 psychological model dimensions
type PsychologicalDimensions struct {
	// Big 5 Personality Traits
	Extraversion      *DimensionScore `json:"extraversion" bson:"extraversion"`
	Openness          *DimensionScore `json:"openness" bson:"openness"`
	Agreeableness     *DimensionScore `json:"agreeableness" bson:"agreeableness"`
	Conscientiousness *DimensionScore `json:"conscientiousness" bson:"conscientiousness"`
	Neuroticism       *DimensionScore `json:"neuroticism" bson:"neuroticism"`

	// Maslow's Hierarchy Needs
	PhysiologicalNeeds *DimensionScore `json:"physiological_needs" bson:"physiological_needs"`
	SecurityNeed       *DimensionScore `json:"security_need" bson:"security_need"`
	BelongingNeed      *DimensionScore `json:"belonging_need" bson:"belonging_need"`
	SelfEsteemNeed     *DimensionScore `json:"self_esteem_need" bson:"self_esteem_need"`
	CognitiveNeeds     *DimensionScore `json:"cognitive_needs" bson:"cognitive_needs"`
	AestheticNeeds     *DimensionScore `json:"aesthetic_needs" bson:"aesthetic_needs"`
	SelfActualization  *DimensionScore `json:"self_actualization" bson:"self_actualization"`

	// Additional Needs
	OrderNeed       *DimensionScore `json:"order_need" bson:"order_need"`
	AutonomyNeed    *DimensionScore `json:"autonomy_need" bson:"autonomy_need"`
	PowerNeed       *DimensionScore `json:"power_need" bson:"power_need"`
	AchievementNeed *DimensionScore `json:"achievement_need" bson:"achievement_need"`
}

// AIAlignmentDimensions represents user expectations of AI behavior
type AIAlignmentDimensions struct {
	Helpfulness           *DimensionScore `json:"helpfulness" bson:"helpfulness"`
	Honesty               *DimensionScore `json:"honesty" bson:"honesty"`
	Safety                *DimensionScore `json:"safety" bson:"safety"`
	InstructionCompliance *DimensionScore `json:"instruction_compliance" bson:"instruction_compliance"`
	Truthfulness          *DimensionScore `json:"truthfulness" bson:"truthfulness"`
	Coherence             *DimensionScore `json:"coherence" bson:"coherence"`
	ComplexityPreference  *DimensionScore `json:"complexity_preference" bson:"complexity_preference"`
	ConcisenessPreference *DimensionScore `json:"conciseness_preference" bson:"conciseness_preference"`
}

// ContentInterestTags represents the 66 content interest dimensions
type ContentInterestTags struct {
	// Core Interests (22)
	ScienceInterest      *DimensionScore `json:"science_interest" bson:"science_interest"`
	EducationInterest    *DimensionScore `json:"education_interest" bson:"education_interest"`
	PsychologyInterest   *DimensionScore `json:"psychology_interest" bson:"psychology_interest"`
	FamilyConcern        *DimensionScore `json:"family_concern" bson:"family_concern"`
	FashionInterest      *DimensionScore `json:"fashion_interest" bson:"fashion_interest"`
	ArtInterest          *DimensionScore `json:"art_interest" bson:"art_interest"`
	HealthConcern        *DimensionScore `json:"health_concern" bson:"health_concern"`
	FinancialInterest    *DimensionScore `json:"financial_interest" bson:"financial_interest"`
	SportsInterest       *DimensionScore `json:"sports_interest" bson:"sports_interest"`
	FoodInterest         *DimensionScore `json:"food_interest" bson:"food_interest"`
	TravelInterest       *DimensionScore `json:"travel_interest" bson:"travel_interest"`
	MusicInterest        *DimensionScore `json:"music_interest" bson:"music_interest"`
	LiteratureInterest   *DimensionScore `json:"literature_interest" bson:"literature_interest"`
	FilmInterest         *DimensionScore `json:"film_interest" bson:"film_interest"`
	SocialMediaActivity  *DimensionScore `json:"social_media_activity" bson:"social_media_activity"`
	TechInterest         *DimensionScore `json:"tech_interest" bson:"tech_interest"`
	EnvironmentalConcern *DimensionScore `json:"environmental_concern" bson:"environmental_concern"`
	HistoryInterest      *DimensionScore `json:"history_interest" bson:"history_interest"`
	PoliticalConcern     *DimensionScore `json:"political_concern" bson:"political_concern"`
	ReligiousInterest    *DimensionScore `json:"religious_interest" bson:"religious_interest"`
	GamingInterest       *DimensionScore `json:"gaming_interest" bson:"gaming_interest"`
	AnimalConcern        *DimensionScore `json:"animal_concern" bson:"animal_concern"`

	// Communication Style (5)
	EmotionalExpression *DimensionScore `json:"emotional_expression" bson:"emotional_expression"`
	SenseOfHumor        *DimensionScore `json:"sense_of_humor" bson:"sense_of_humor"`
	InformationDensity  *DimensionScore `json:"information_density" bson:"information_density"`
	LanguageStyle       *DimensionScore `json:"language_style" bson:"language_style"`
	PracticalityFocus   *DimensionScore `json:"practicality_focus" bson:"practicality_focus"`
}

// DimensionScore represents a scored personality dimension
type DimensionScore struct {
	Level            string    `json:"level" bson:"level"`           // "High", "Medium", "Low"
	Confidence       float64   `json:"confidence" bson:"confidence"` // 0.0 - 1.0
	Evidence         string    `json:"evidence" bson:"evidence"`     // Reasoning for the score
	LastObserved     time.Time `json:"last_observed" bson:"last_observed"`
	ObservationCount int       `json:"observation_count" bson:"observation_count"`
}

// UserFactEntry represents extracted factual knowledge about the user
type UserFactEntry struct {
	FactID      string    `json:"fact_id" bson:"fact_id"`
	Category    string    `json:"category" bson:"category"`     // "personal", "preference", "skill", etc.
	Content     string    `json:"content" bson:"content"`       // The actual fact
	Context     string    `json:"context" bson:"context"`       // Context/source of the fact
	Confidence  float64   `json:"confidence" bson:"confidence"` // How confident we are
	Source      string    `json:"source" bson:"source"`         // "conversation", "explicit", "inferred"
	ExtractedAt time.Time `json:"extracted_at" bson:"extracted_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
}

// AssistantKnowledgeEntry represents what the assistant demonstrated
type AssistantKnowledgeEntry struct {
	KnowledgeID    string    `json:"knowledge_id" bson:"knowledge_id"`
	Capability     string    `json:"capability" bson:"capability"` // What capability was shown
	Action         string    `json:"action" bson:"action"`         // What action was taken
	Context        string    `json:"context" bson:"context"`       // Context of the demonstration
	UserID         string    `json:"user_id" bson:"user_id"`       // Which user witnessed it
	DemonstratedAt time.Time `json:"demonstrated_at" bson:"demonstrated_at"`
}

// PersonalityAnalysisConfig holds configuration for personality analysis
type PersonalityAnalysisConfig struct {
	MinConfidenceThreshold      float64 // Minimum confidence to record a dimension
	DimensionUpdateThreshold    float64 // Minimum change required to update dimension
	MaxFactsPerCategory         int     // Maximum facts per category
	FactRetentionDays           int     // How long to keep facts
	RequireMultipleObservations bool    // Whether to require multiple observations before recording
}

// DimensionType represents the category of personality dimension
type DimensionType string

const (
	DimensionTypePsychological   DimensionType = "psychological"
	DimensionTypeAIAlignment     DimensionType = "ai_alignment"
	DimensionTypeContentInterest DimensionType = "content_interest"
)

// GetAllDimensionNames returns all 90 dimension names for analysis
func GetAllDimensionNames() map[DimensionType][]string {
	return map[DimensionType][]string{
		DimensionTypePsychological: {
			"extraversion", "openness", "agreeableness", "conscientiousness", "neuroticism",
			"physiological_needs", "security_need", "belonging_need", "self_esteem_need",
			"cognitive_needs", "aesthetic_needs", "self_actualization", "order_need",
			"autonomy_need", "power_need", "achievement_need",
		},
		DimensionTypeAIAlignment: {
			"helpfulness", "honesty", "safety", "instruction_compliance",
			"truthfulness", "coherence", "complexity_preference", "conciseness_preference",
		},
		DimensionTypeContentInterest: {
			"science_interest", "education_interest", "psychology_interest", "family_concern",
			"fashion_interest", "art_interest", "health_concern", "financial_interest",
			"sports_interest", "food_interest", "travel_interest", "music_interest",
			"literature_interest", "film_interest", "social_media_activity", "tech_interest",
			"environmental_concern", "history_interest", "political_concern", "religious_interest",
			"gaming_interest", "animal_concern", "emotional_expression", "sense_of_humor",
			"information_density", "language_style", "practicality_focus",
		},
	}
}

// GetDimensionDescription returns human-readable description for a dimension
func GetDimensionDescription(dimensionName string) string {
	descriptions := map[string]string{
		// Psychological Model
		"extraversion":        "Preference for social activities",
		"openness":            "Willingness to embrace new ideas and experiences",
		"agreeableness":       "Tendency to be friendly and cooperative",
		"conscientiousness":   "Responsibility and organizational ability",
		"neuroticism":         "Emotional stability and sensitivity",
		"physiological_needs": "Concern for comfort and basic needs",
		"security_need":       "Emphasis on safety and stability",
		"belonging_need":      "Desire for group affiliation",
		"self_esteem_need":    "Need for respect and recognition",
		"cognitive_needs":     "Desire for knowledge and understanding",
		"aesthetic_needs":     "Appreciation for beauty and art",
		"self_actualization":  "Pursuit of one's full potential",
		"order_need":          "Preference for cleanliness and organization",
		"autonomy_need":       "Preference for independent decision-making",
		"power_need":          "Desire to influence or control others",
		"achievement_need":    "Value placed on accomplishments",

		// AI Alignment
		"helpfulness":            "Expectation of practically useful AI responses",
		"honesty":                "Expectation of truthful AI responses",
		"safety":                 "Preference for avoiding sensitive content",
		"instruction_compliance": "Expectation of strict adherence to instructions",
		"truthfulness":           "Expectation of accurate and authentic content",
		"coherence":              "Preference for clear and logical responses",
		"complexity_preference":  "Preference for detailed information",
		"conciseness_preference": "Preference for brief responses",

		// Content Interests
		"science_interest":      "Interest in science topics",
		"education_interest":    "Concern with education and learning",
		"psychology_interest":   "Interest in psychology topics",
		"family_concern":        "Interest in family and parenting",
		"fashion_interest":      "Interest in fashion topics",
		"art_interest":          "Engagement with or interest in art",
		"health_concern":        "Concern with physical health and lifestyle",
		"financial_interest":    "Interest in finance and budgeting",
		"sports_interest":       "Interest in sports and physical activity",
		"food_interest":         "Passion for cooking and cuisine",
		"travel_interest":       "Interest in traveling and exploring",
		"music_interest":        "Interest in music appreciation or creation",
		"literature_interest":   "Interest in literature and reading",
		"film_interest":         "Interest in movies and cinema",
		"social_media_activity": "Frequency and engagement with social media",
		"tech_interest":         "Interest in technology and innovation",
		"environmental_concern": "Attention to environmental issues",
		"history_interest":      "Interest in historical knowledge",
		"political_concern":     "Interest in political and social issues",
		"religious_interest":    "Interest in religion and spirituality",
		"gaming_interest":       "Enjoyment of video games or board games",
		"animal_concern":        "Concern for animals or pets",
		"emotional_expression":  "Preference for direct vs restrained expression",
		"sense_of_humor":        "Preference for humorous communication",
		"information_density":   "Preference for detailed vs concise information",
		"language_style":        "Preference for formal vs casual tone",
		"practicality_focus":    "Preference for practical vs theoretical discussion",
	}

	if desc, exists := descriptions[dimensionName]; exists {
		return desc
	}
	return "No description available"
}
