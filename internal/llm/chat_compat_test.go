package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reasoningDeployment mimics an Azure gpt-5 deployment: it rejects max_tokens,
// non-default temperature and stop exactly as the service does, and spends 64
// completion tokens on reasoning before it answers.
type reasoningDeployment struct {
	mu       sync.Mutex
	bodies   []map[string]interface{}
	headers  []http.Header
	rejectRE bool // reject reasoning_effort
}

func (d *reasoningDeployment) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	d.mu.Lock()
	d.bodies = append(d.bodies, body)
	d.headers = append(d.headers, r.Header.Clone())
	d.mu.Unlock()

	reject := func(param, msg string) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]string{
			"message": msg, "type": "invalid_request_error", "param": param, "code": "unsupported_parameter",
		}})
	}
	switch {
	case body["max_tokens"] != nil:
		reject("max_tokens", "Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.")
		return
	case body["temperature"] != nil && body["temperature"] != 1.0:
		reject("temperature", "Unsupported value: 'temperature' does not support 0.3 with this model. Only the default (1) value is supported.")
		return
	case body["stop"] != nil:
		reject("stop", "Unsupported parameter: 'stop' is not supported with this model.")
		return
	case d.rejectRE && body["reasoning_effort"] != nil:
		reject("reasoning_effort", "Unrecognized request argument supplied: reasoning_effort")
		return
	}
	content, finish := "OK", "stop"
	if budget, _ := body["max_completion_tokens"].(float64); budget < 64+5 {
		content, finish = "", "length"
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{
		{"message": map[string]string{"content": content}, "finish_reason": finish},
	}})
}

func classicDeployment(t *testing.T, seen *[]map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*seen = append(*seen, body)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{
			{"message": map[string]string{"content": "classic"}, "finish_reason": "stop"},
		}})
	}))
}

func request() CompletionRequest {
	return CompletionRequest{SystemPrompt: "be brief", Prompt: "Say OK", MaxTokens: 10, Temperature: 0.3, Stop: []string{"\n"}}
}

func TestAzureAdaptsToReasoningDeployment(t *testing.T) {
	d := &reasoningDeployment{}
	srv := httptest.NewServer(d)
	defer srv.Close()
	p := NewAzureProvider(srv.URL, "key", srv.URL, "")

	out, err := p.GenerateCompletion(context.Background(), request())
	require.NoError(t, err)
	assert.Equal(t, "OK", out)
	require.Len(t, d.bodies, 4, "max_tokens, temperature and stop are each rejected once")

	final := d.bodies[3]
	assert.Nil(t, final["max_tokens"])
	assert.Nil(t, final["temperature"])
	assert.Nil(t, final["stop"])
	assert.Equal(t, float64(10+defaultReasoningHeadroom), final["max_completion_tokens"])
	assert.Equal(t, "key", d.headers[3].Get("api-key"))
	msgs := final["messages"].([]interface{})
	assert.Equal(t, "system", msgs[0].(map[string]interface{})["role"])

	// The adaptation is remembered: the next completion is a single request.
	_, err = p.GenerateCompletion(context.Background(), request())
	require.NoError(t, err)
	assert.Len(t, d.bodies, 5)
}

func TestReasoningBudgetExhaustedIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	t.Setenv("LLM_REASONING_TOKEN_HEADROOM", "0")
	d := &reasoningDeployment{}
	srv := httptest.NewServer(d)
	defer srv.Close()
	p := NewAzureProvider(srv.URL, "key", srv.URL, "")

	_, err := p.GenerateCompletion(context.Background(), request())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finish_reason=length")
	assert.Contains(t, err.Error(), "LLM_REASONING_TOKEN_HEADROOM")
}

func TestKnownReasoningModelSkipsRejectedRequests(t *testing.T) {
	t.Setenv("LLM_REASONING_MODEL", "true")
	t.Setenv("LLM_REASONING_EFFORT", "low")
	d := &reasoningDeployment{}
	srv := httptest.NewServer(d)
	defer srv.Close()
	p := NewAzureProvider(srv.URL, "key", srv.URL, "")

	_, err := p.GenerateCompletion(context.Background(), request())
	require.NoError(t, err)
	require.Len(t, d.bodies, 1)
	assert.Equal(t, "low", d.bodies[0]["reasoning_effort"])
}

func TestRejectedReasoningEffortIsDropped(t *testing.T) {
	t.Setenv("LLM_REASONING_MODEL", "true")
	t.Setenv("LLM_REASONING_EFFORT", "minimal")
	d := &reasoningDeployment{rejectRE: true}
	srv := httptest.NewServer(d)
	defer srv.Close()
	p := NewAzureProvider(srv.URL, "key", srv.URL, "")

	_, err := p.GenerateCompletion(context.Background(), request())
	require.NoError(t, err)
	require.Len(t, d.bodies, 2)
	assert.Nil(t, d.bodies[1]["reasoning_effort"])
}

func TestClassicDeploymentKeepsItsParameters(t *testing.T) {
	var seen []map[string]interface{}
	srv := classicDeployment(t, &seen)
	defer srv.Close()
	p := NewAzureProvider(srv.URL, "key", srv.URL, "")

	out, err := p.GenerateCompletion(context.Background(), request())
	require.NoError(t, err)
	assert.Equal(t, "classic", out)
	require.Len(t, seen, 1)
	assert.Equal(t, float64(10), seen[0]["max_tokens"])
	assert.Equal(t, 0.3, seen[0]["temperature"])
	assert.NotNil(t, seen[0]["stop"])
	assert.Nil(t, seen[0]["max_completion_tokens"])
}

func TestUnrelatedBadRequestIsNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"The response was filtered due to the prompt triggering content management policy.","code":"content_filter"}}`))
	}))
	defer srv.Close()
	p := NewAzureProvider(srv.URL, "key", srv.URL, "")

	_, err := p.GenerateCompletion(context.Background(), request())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
	assert.Equal(t, 1, calls)
}

func TestOpenAIProviderAdaptsToReasoningModels(t *testing.T) {
	d := &reasoningDeployment{}
	srv := httptest.NewServer(d)
	defer srv.Close()
	p := NewOpenAIProvider(srv.URL, "sk-test", "gpt-5", "")

	out, err := p.GenerateCompletion(context.Background(), request())
	require.NoError(t, err)
	assert.Equal(t, "OK", out)
	last := d.bodies[len(d.bodies)-1]
	assert.Equal(t, "gpt-5", last["model"])
	assert.True(t, strings.HasSuffix(d.headers[0].Get("Authorization"), "sk-test"))
}
