package llm

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
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
		if apiKey == "" {
			return nil, fmt.Errorf("azure provider: set LLM_API_KEY or AZURE_OPENAI_API_KEY")
		}
		endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
		apiVersion := os.Getenv("AZURE_OPENAI_API_VERSION")
		chatDeployment := firstNonEmpty(os.Getenv("AZURE_OPENAI_DEPLOYMENT"), model)
		embeddingDeployment := firstNonEmpty(os.Getenv("AZURE_OPENAI_EMBEDDING_DEPLOYMENT"), embeddingModel)

		chatURL, err := azureDeploymentURL(firstNonEmpty(baseURL, endpoint), chatDeployment, "chat/completions", apiVersion)
		if err != nil {
			return nil, fmt.Errorf("azure provider: chat: %w (set LLM_BASE_URL to a deployment chat-completions URL, or AZURE_OPENAI_ENDPOINT with AZURE_OPENAI_DEPLOYMENT or LLM_MODEL_NAME)", err)
		}
		embedURL, err := azureDeploymentURL(firstNonEmpty(embeddingURL, endpoint), embeddingDeployment, "embeddings", apiVersion)
		if err != nil {
			return nil, fmt.Errorf("azure provider: embeddings: %w (set EMBEDDING_BASE_URL to a deployment embeddings URL, or AZURE_OPENAI_ENDPOINT with AZURE_OPENAI_EMBEDDING_DEPLOYMENT or EMBEDDING_MODEL_NAME)", err)
		}
		return NewAzureProvider(chatURL, apiKey, embedURL, embeddingModel), nil
	case "openai":
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("openai provider: set LLM_API_KEY or OPENAI_API_KEY")
		}
		return NewOpenAIProvider(baseURL, apiKey, model, embeddingModel), nil
	case "gemini":
		if useADC, _ := strconv.ParseBool(os.Getenv("GEMINI_USE_ADC")); useADC {
			return NewGeminiVertexProvider(context.Background(),
				os.Getenv("GCP_PROJECT_ID"), os.Getenv("VERTEX_LOCATION"), model, embeddingModel)
		}
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("gemini provider: set LLM_API_KEY or GEMINI_API_KEY (or GEMINI_USE_ADC=true for Vertex AI)")
		}
		return NewGeminiProvider(apiKey, model, embeddingModel), nil
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}

// defaultAzureAPIVersion supports max_completion_tokens, which reasoning
// deployments require.
const defaultAzureAPIVersion = "2024-10-21"

// azureDeploymentURL returns the URL of an Azure OpenAI deployment operation. A base
// that already names a deployment or the operation (for example the v1 API's
// /openai/v1/chat/completions) is used as given; a resource endpoint is combined with
// the deployment name, the operation and the API version.
func azureDeploymentURL(base, deployment, operation, apiVersion string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("no endpoint configured")
	}
	if u, err := url.Parse(base); err == nil {
		path := strings.TrimRight(u.Path, "/")
		if strings.Contains(path, "/openai/deployments/") || strings.HasSuffix(path, "/"+operation) {
			return base, nil
		}
	}
	if deployment == "" {
		return "", fmt.Errorf("%s is a resource endpoint but no deployment name is set", base)
	}
	if apiVersion == "" {
		apiVersion = defaultAzureAPIVersion
	}
	return fmt.Sprintf("%s/openai/deployments/%s/%s?api-version=%s",
		strings.TrimRight(base, "/"), url.PathEscape(deployment), operation, url.QueryEscape(apiVersion)), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
