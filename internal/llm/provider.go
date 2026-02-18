package llm

import (
	"context"
)

// CompletionRequest represents a request for text generation
type CompletionRequest struct {
	Prompt      string
	MaxTokens   int
	Temperature float64
	Stop        []string
}

// EmbeddingRequest represents a request for vector embedding
type EmbeddingRequest struct {
	Input string
}

// Provider defines the interface for interacting with different LLM backends
type Provider interface {
	// GenerateCompletion generates text based on a prompt
	GenerateCompletion(ctx context.Context, req CompletionRequest) (string, error)

	// CreateEmbedding creates a vector embedding for the given text
	CreateEmbedding(ctx context.Context, req EmbeddingRequest) ([]float64, error)
}
