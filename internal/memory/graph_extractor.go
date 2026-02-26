package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"bitbucket.org/dromos/memory-os/internal/llm"
	"github.com/prometheus/client_golang/prometheus"
)

// GraphNode represents an entity in the Knowledge Graph
type GraphNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
}

// GraphEdge represents a relationship between two nodes
type GraphEdge struct {
	From          string  `json:"from"`
	To            string  `json:"to"`
	Relation      string  `json:"relation"`
	ContextNuance string  `json:"context_nuance"`
	Confidence    float64 `json:"confidence"`
}

// GraphExtraction represents the full parsed graph from a summary
type GraphExtraction struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// ExtractGraphFromSummary uses the LLM to extract a valid graph from a text summary
func (s *STMStore) ExtractGraphFromSummary(ctx context.Context, summary string) (*GraphExtraction, error) {
	if s.llmProvider == nil {
		return nil, fmt.Errorf("llm provider is not initialized")
	}

	if !s.llmGuards.checkRateLimit(ctx, "graph_extraction_system") {
		return nil, fmt.Errorf("llm rate limit exceeded for graph extraction")
	}
	if !s.llmGuards.checkCircuitBreaker() {
		return nil, fmt.Errorf("llm circuit breaker is open for graph extraction")
	}

	// Define strict JSON Schema for the response matching ArangoDB collections
	jsonSchema := map[string]interface{}{
		"name":   "graph_extraction_schema",
		"strict": true,
		"schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodes": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id": map[string]interface{}{
								"type":        "string",
								"description": "Lowercase, snake_case ID. E.g., 'sangalo', 'openclaw'",
							},
							"label": map[string]interface{}{
								"type": "string",
								"enum": []string{"Identities", "Concepts", "Tools", "Projects"},
							},
							"name": map[string]interface{}{
								"type": "string",
							},
						},
						"required":             []string{"id", "label", "name"},
						"additionalProperties": false,
					},
				},
				"edges": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"from": map[string]interface{}{
								"type":        "string",
								"description": "The ID of the source node",
							},
							"to": map[string]interface{}{
								"type":        "string",
								"description": "The ID of the target node",
							},
							"relation": map[string]interface{}{
								"type": "string",
								"enum": []string{
									"USES",
									"WORKS_ON",
									"BUILT_FOR_CLIENT",
									"STRUGGLES_WITH",
									"EXHIBITS",
									"EXPRESSED_INTEREST",
									"RELATES_TO",
								},
							},
							"context_nuance": map[string]interface{}{
								"type":        "string",
								"description": "The exact human meaning if relation is RELATES_TO",
							},
							"confidence": map[string]interface{}{
								"type":        "number",
								"description": "Confidence score 0.0 to 1.0",
							},
						},
						"required":             []string{"from", "to", "relation", "confidence"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"nodes", "edges"},
			"additionalProperties": false,
		},
	}

	systemPrompt := `You are an expert Knowledge Graph Extraction Engine for a cognitive memory system.
Your task is to read a conversational summary and extract the core entities (Nodes) and their relationships (Edges).

OUTPUT FORMAT: You must return strictly valid JSON. Do not include markdown blocks or conversational text.

RULE 1: NODE ONTOLOGY (INHERENT NATURE)
You must classify entities based on their highest-order, objective nature, NOT how the user is currently interacting with them.
Nodes must strictly use one of these four Labels:

'Identities': Entities with agency, persona, or organization. (e.g., sangalo, joe, prescott_data, athena, openclaw). Even if the user is 'building' an AI agent, it has agency and is an Identity, not a Project.

'Tools': Concrete software, hardware, languages, or frameworks. (e.g., go, arangodb, docker, macbook_air).

'Projects': Scoped initiatives, repositories, or specific deliverables without agency. (e.g., nexus_protocol, joedowns_ai).

'Concepts': Abstract ideas, methodologies, or domains. (e.g., agentic_ai, spaced_repetition, authentication).

RULE 2: NODE IDs
Node IDs must be highly normalized, lowercase, snake_case strings. (e.g., 'sangalo', 'nexus_protocol', 'agentic_ai').

RULE 3: EDGE ONTOLOGY (PERSPECTIVE)
Edges define how the entities interact. You must strictly use one of these Relations:

'USES': Actively utilizing a Tool or Identity for a task.

'WORKS_ON': Actively building, coding, or developing a Project, Tool, or Identity.

'BUILT_FOR_CLIENT': Developing something specifically for another Identity.

'STRUGGLES_WITH': Having difficulty with a Tool, Concept, or Project.

'EXHIBITS': Displaying a personality trait or behavior.

'EXPRESSED_INTEREST': Showing curiosity or desire to learn about a Concept or Tool.

'RELATES_TO': A generic connection when no other verb fits. If you use this, you MUST fill out the 'context_nuance' field with the exact human meaning (e.g., 'is traveling to', 'is siblings with').

RULE 4: CONFIDENCE SCORING
For every Edge, assign a 'confidence' score between 0.0 and 1.0.

Return the JSON in this exact structure:
{
"nodes": [{"id": "string", "label": "string", "name": "string"}],
"edges": [{"from": "string", "to": "string", "relation": "string", "context_nuance": "string", "confidence": 0.0}]
}`

	req := llm.CompletionRequest{
		SystemPrompt: systemPrompt,
		Prompt:       summary,
		MaxTokens:    1500,
		Temperature:  0.1,
		JSONSchema:   jsonSchema,
	}

	httpCtx, cancel := context.WithTimeout(ctx, s.llmConfig.SummaryTimeout)
	defer cancel()

	timer := prometheus.NewTimer(ExtractorLLMDuration)
	responseContent, err := s.llmProvider.GenerateCompletion(httpCtx, req)
	timer.ObserveDuration()

	if err != nil {
		s.llmGuards.recordLLMResult(false)
		return nil, fmt.Errorf("llm graph extraction failed: %w", err)
	}

	var extractionResp GraphExtraction
	if err := json.Unmarshal([]byte(responseContent), &extractionResp); err != nil {
		ExtractorSchemaFailures.Inc()
		s.llmGuards.recordLLMResult(false)
		slog.Error("LLM returned invalid JSON schema",
			slog.String("error", err.Error()),
			slog.String("raw_llm_payload", responseContent),
		)
		return nil, fmt.Errorf("failed to parse structured JSON output for graph: %w", err)
	}

	s.llmGuards.recordLLMResult(true)

	slog.Info("Graph extraction successful",
		slog.Int("nodes", len(extractionResp.Nodes)),
		slog.Int("edges", len(extractionResp.Edges)))

	return &extractionResp, nil
}
