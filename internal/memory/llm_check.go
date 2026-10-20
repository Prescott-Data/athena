package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/Prescott-Data/athena/internal/llm"
)

// CheckLLM makes one small completion and one embedding with the configured
// provider, so a misconfigured model shows up at startup and in health checks
// instead of as chains that silently never form. It also checks that embeddings
// have the dimension Milvus expects, since every vector insert fails otherwise.
func (s *STMStore) CheckLLM(ctx context.Context) error {
	if s.llmProvider == nil {
		return fmt.Errorf("no LLM provider configured (check LLM_PROVIDER and its credentials)")
	}
	answer, err := s.llmProvider.GenerateCompletion(ctx, llm.CompletionRequest{
		Prompt:    "Reply with the single word OK.",
		MaxTokens: 5,
	})
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}
	if strings.TrimSpace(answer) == "" {
		return fmt.Errorf("completion returned no content")
	}
	vector, err := s.llmProvider.CreateEmbedding(ctx, llm.EmbeddingRequest{Input: "Athena health check"})
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}
	if len(vector) != VectorDimension {
		return fmt.Errorf("embedding model %q returns %d dimensions but MILVUS_VECTOR_DIMENSION is %d; vectors cannot be stored",
			s.llmProvider.EmbeddingModel(), len(vector), VectorDimension)
	}
	return nil
}
