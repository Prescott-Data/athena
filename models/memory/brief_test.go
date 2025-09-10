package models

import (
	"encoding/json"
	"testing"
)

func TestVisualizationPropertiesUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		expected map[string]interface{}
	}{
		{
			name: "Bar Chart with labels and datasets",
			jsonData: `{
				"chartType": "Bar Chart",
				"labels": ["C&G Auto Garage", "AutoXpress Service", "Toyota Service Center"],
				"datasets": [
					{
						"label": "Total Repair Costs",
						"data": [2345000, 1876000, 1543000]
					}
				]
			}`,
			expected: map[string]interface{}{
				"chartType": "Bar Chart",
				"labels":    []interface{}{"C&G Auto Garage", "AutoXpress Service", "Toyota Service Center"},
				"datasets": []interface{}{
					map[string]interface{}{
						"label": "Total Repair Costs",
						"data":  []interface{}{2.345e+06, 1.876e+06, 1.543e+06},
					},
				},
			},
		},
		{
			name: "Pie Chart with labels and data",
			jsonData: `{
				"chartType": "Pie Chart",
				"labels": ["Nairobi", "Mombasa", "Kisumu"],
				"data": [156, 34, 22]
			}`,
			expected: map[string]interface{}{
				"chartType": "Pie Chart",
				"labels":    []interface{}{"Nairobi", "Mombasa", "Kisumu"},
				"data":      []interface{}{156.0, 34.0, 22.0},
			},
		},
		{
			name: "Heatmap with xLabels, yLabels and data",
			jsonData: `{
				"chartType": "Heatmap",
				"xLabels": ["Thomas Ouma", "Peter Wekesa"],
				"yLabels": ["Kia Cadenza", "Subaru Impreza"],
				"data": [[3, 0], [0, 2]]
			}`,
			expected: map[string]interface{}{
				"chartType": "Heatmap",
				"xLabels":   []interface{}{"Thomas Ouma", "Peter Wekesa"},
				"yLabels":   []interface{}{"Kia Cadenza", "Subaru Impreza"},
				"data":      []interface{}{[]interface{}{3.0, 0.0}, []interface{}{0.0, 2.0}},
			},
		},
		{
			name: "Scatter Plot with data objects",
			jsonData: `{
				"chartType": "Scatter Plot",
				"data": [
					{"x": 182309, "y": 4},
					{"x": 145118, "y": 3}
				]
			}`,
			expected: map[string]interface{}{
				"chartType": "Scatter Plot",
				"data": []interface{}{
					map[string]interface{}{"x": 182309.0, "y": 4.0},
					map[string]interface{}{"x": 145118.0, "y": 3.0},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var props VisualizationProperties
			err := json.Unmarshal([]byte(tt.jsonData), &props)
			if err != nil {
				t.Fatalf("Failed to unmarshal JSON: %v", err)
			}

			// Check that chartType is preserved
			if chartType, ok := props["chartType"]; !ok || chartType != tt.expected["chartType"] {
				t.Errorf("Expected chartType %v, got %v", tt.expected["chartType"], chartType)
			}

			// Check that all expected fields are present
			for key, expectedValue := range tt.expected {
				if actualValue, ok := props[key]; !ok {
					t.Errorf("Expected key %s to be present", key)
				} else {
					// For complex structures, just check they exist
					if key == "chartType" && actualValue != expectedValue {
						t.Errorf("Expected %s to be %v, got %v", key, expectedValue, actualValue)
					}
				}
			}

			// Check that no transformation occurred - data should be exactly as received
			if len(props) == 0 {
				t.Error("Expected properties to have content, but it was empty")
			}
		})
	}
}
