package llm

import (
	"fmt"
	"os"
)

// Factory creates LLM providers based on configuration
type Factory struct{}

// NewProviderFromEnv creates a provider from LLM_PROVIDER (default "azure").
func (f *Factory) NewProviderFromEnv() (Provider, error) {
	providerType := os.Getenv("LLM_PROVIDER")
	if providerType == "" {
		providerType = "azure" // backward compatibility
	}
	return f.NewProvider(providerType)
}

// NewProvider creates a new LLM provider based on the type
func (f *Factory) NewProvider(providerType string) (Provider, error) {
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL_NAME")
	embeddingModel := os.Getenv("EMBEDDING_MODEL_NAME")
	baseURL := os.Getenv("LLM_BASE_URL")
	embeddingURL := os.Getenv("EMBEDDING_BASE_URL")

	switch providerType {
	case "azure":
		if apiKey == "" {
			apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
		}
		if baseURL == "" {
			baseURL = os.Getenv("AZURE_OPENAI_ENDPOINT")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("azure provider: set LLM_API_KEY or AZURE_OPENAI_API_KEY")
		}
		if baseURL == "" {
			return nil, fmt.Errorf("azure provider: set LLM_BASE_URL (deployment completions URL) or AZURE_OPENAI_ENDPOINT")
		}
		if embeddingURL == "" {
			return nil, fmt.Errorf("azure provider: set EMBEDDING_BASE_URL (deployment embeddings URL)")
		}
		return NewAzureProvider(baseURL, apiKey, embeddingURL, embeddingModel), nil
	case "openai":
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("openai provider: set LLM_API_KEY or OPENAI_API_KEY")
		}
		return NewOpenAIProvider(baseURL, apiKey, model, embeddingModel), nil
	case "gemini":
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("gemini provider: set LLM_API_KEY or GEMINI_API_KEY")
		}
		return NewGeminiProvider(apiKey, model, embeddingModel), nil
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}
