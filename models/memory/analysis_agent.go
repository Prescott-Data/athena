package models

// Question represents a question input from the user for analysis or processing.
type Question struct {
	Text    string                 `json:"text"`
	Context map[string]interface{} `json:"context"`
}

// FocusedBrief defines parameters for requesting a brief with specific focus areas.
// Used to guide the depth and scope of the AI-generated response.
type FocusedBrief struct {
	DetailLevel string  `json:"detail_level,omitempty"`
	FocusArea   *string `json:"focus_area,omitempty"`
}

// ValidationError represents a single validation error returned by the API.
// Typically used when request input fails schema validation.
type ValidationError struct {
	Loc  []interface{} `json:"loc"`
	Msg  string        `json:"msg"`
	Type string        `json:"type"`
}

// HTTPValidationError represents a structured validation error response.
// It wraps multiple ValidationError items to support detailed error reporting.
type HTTPValidationError struct {
	Detail []ValidationError `json:"detail"`
}

// Error implements error.
func (h HTTPValidationError) Error() string {
	if len(h.Detail) == 0 {
		return "validation error"
	}

	// Return the first validation error message
	return h.Detail[0].Msg
}
