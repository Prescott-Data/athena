package memory

import (
	"github.com/dromos-org/memory-os/internal/models"
)

// Import aliases for convenience
type (
	UserPersona               = models.UserPersona
	PsychologicalDimensions   = models.PsychologicalDimensions
	AIAlignmentDimensions     = models.AIAlignmentDimensions
	ContentInterestTags       = models.ContentInterestTags
	DimensionScore            = models.DimensionScore
	UserFactEntry             = models.UserFactEntry
	AssistantKnowledgeEntry   = models.AssistantKnowledgeEntry
	PersonalityAnalysisConfig = models.PersonalityAnalysisConfig
	DimensionType             = models.DimensionType
)

// Constants for dimension types
const (
	DimensionTypePsychological   = models.DimensionTypePsychological
	DimensionTypeAIAlignment     = models.DimensionTypeAIAlignment
	DimensionTypeContentInterest = models.DimensionTypeContentInterest
)

// Exported functions for convenience
var (
	GetAllDimensionNames    = models.GetAllDimensionNames
	GetDimensionDescription = models.GetDimensionDescription
)
