package llm

import "testing"

func TestVertexBaseURL(t *testing.T) {
	tests := []struct {
		location string
		want     string
	}{
		{"global", "https://aiplatform.googleapis.com/v1/projects/my-proj/locations/global/publishers/google/models"},
		{"us-central1", "https://us-central1-aiplatform.googleapis.com/v1/projects/my-proj/locations/us-central1/publishers/google/models"},
	}
	for _, tt := range tests {
		if got := vertexBaseURL("my-proj", tt.location); got != tt.want {
			t.Errorf("vertexBaseURL(%q) = %q, want %q", tt.location, got, tt.want)
		}
	}
}

func TestSupportsThinkingBudget(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"gemini-1.5-pro", false},
		{"gemini-2.5-flash", true},
		{"gemini-3-flash-preview", true},
	}
	for _, tt := range tests {
		if got := supportsThinkingBudget(tt.model); got != tt.want {
			t.Errorf("supportsThinkingBudget(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestMapSchemaTypesForGemini_StripsStrictnessKeys(t *testing.T) {
	in := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"strict":               true,
		"properties": map[string]interface{}{
			"summary": map[string]interface{}{
				"type":                 "string",
				"additionalProperties": false,
			},
		},
	}
	out := mapSchemaTypesForGemini(in)

	if _, ok := out["additionalProperties"]; ok {
		t.Error("additionalProperties should be stripped at the top level")
	}
	if _, ok := out["strict"]; ok {
		t.Error("strict should be stripped")
	}
	if out["type"] != "OBJECT" {
		t.Errorf("type = %v, want OBJECT", out["type"])
	}
	props := out["properties"].(map[string]interface{})
	sub := props["summary"].(map[string]interface{})
	if _, ok := sub["additionalProperties"]; ok {
		t.Error("additionalProperties should be stripped recursively")
	}
	if sub["type"] != "STRING" {
		t.Errorf("nested type = %v, want STRING", sub["type"])
	}
}
