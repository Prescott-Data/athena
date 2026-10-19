package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzureDeploymentURL(t *testing.T) {
	cases := []struct {
		name, base, deployment, op, version, want string
		wantErr                                   bool
	}{
		{"full deployment URL used as given", "https://r.openai.azure.com/openai/deployments/gpt-5/chat/completions?api-version=2024-08-01-preview", "", "chat/completions", "", "https://r.openai.azure.com/openai/deployments/gpt-5/chat/completions?api-version=2024-08-01-preview", false},
		{"v1 API URL used as given", "https://r.openai.azure.com/openai/v1/chat/completions", "", "chat/completions", "", "https://r.openai.azure.com/openai/v1/chat/completions", false},
		{"resource endpoint built", "https://r.openai.azure.com/", "gpt-5", "chat/completions", "", "https://r.openai.azure.com/openai/deployments/gpt-5/chat/completions?api-version=" + defaultAzureAPIVersion, false},
		{"embeddings with explicit version", "https://r.openai.azure.com", "text-embedding-3-small", "embeddings", "2023-05-15", "https://r.openai.azure.com/openai/deployments/text-embedding-3-small/embeddings?api-version=2023-05-15", false},
		{"resource endpoint without deployment", "https://r.openai.azure.com", "", "chat/completions", "", "", true},
		{"nothing configured", "", "gpt-5", "chat/completions", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := azureDeploymentURL(tc.base, tc.deployment, tc.op, tc.version)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAzureProviderFromResourceEndpoint(t *testing.T) {
	for _, k := range []string{"LLM_API_KEY", "LLM_BASE_URL", "EMBEDDING_BASE_URL", "LLM_MODEL_NAME", "AZURE_OPENAI_EMBEDDING_DEPLOYMENT", "AZURE_OPENAI_API_VERSION"} {
		t.Setenv(k, "")
	}
	t.Setenv("AZURE_OPENAI_API_KEY", "key")
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://r.openai.azure.com")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT", "gpt-5")
	t.Setenv("EMBEDDING_MODEL_NAME", "text-embedding-3-small")

	p, err := (&Factory{}).NewProvider("azure")
	require.NoError(t, err)
	az := p.(*AzureProvider)
	assert.Equal(t, "https://r.openai.azure.com/openai/deployments/gpt-5/chat/completions?api-version="+defaultAzureAPIVersion, az.BaseURL)
	assert.Equal(t, "https://r.openai.azure.com/openai/deployments/text-embedding-3-small/embeddings?api-version="+defaultAzureAPIVersion, az.EmbeddingURL)

	t.Setenv("AZURE_OPENAI_DEPLOYMENT", "")
	_, err = (&Factory{}).NewProvider("azure")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_OPENAI_DEPLOYMENT")
}
