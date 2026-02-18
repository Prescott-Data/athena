package llm

import (
	"fmt"
	"os"
)

// Factory creates LLM providers based on configuration
type Factory struct{}

// NewProvider creates a new LLM provider based on the type
func (f *Factory) NewProvider(providerType string) (Provider, error) {
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL_NAME")
	embeddingModel := os.Getenv("EMBEDDING_MODEL_NAME")
	baseURL := os.Getenv("LLM_BASE_URL")
	embeddingURL := os.Getenv("EMBEDDING_BASE_URL")

	switch providerType {
	case "azure":
		// For Azure, we need specific environment variables
		if apiKey == "" {
			apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
		}
		if baseURL == "" {
			baseURL = os.Getenv("AZURE_OPENAI_ENDPOINT")
		}
		return NewAzureProvider(baseURL, apiKey, embeddingURL, embeddingModel), nil
	case "openai":
		return NewOpenAIProvider(baseURL, apiKey, model, embeddingModel), nil
	case "gemini":
		return NewGeminiProvider(apiKey, model, embeddingModel), nil
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}
