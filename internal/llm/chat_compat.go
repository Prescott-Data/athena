package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

// defaultReasoningHeadroom is added to a request's MaxTokens for reasoning models.
// They spend completion tokens on hidden reasoning before answering, so the small
// budgets Athena asks for (10–1000 tokens) would otherwise end in an empty answer.
const defaultReasoningHeadroom = 4096

// chatCompat adapts chat-completions requests to what a model accepts. Reasoning
// models (OpenAI o-series, gpt-5 and later, on OpenAI or Azure) reject max_tokens
// in favour of max_completion_tokens, accept only the default temperature and do
// not support stop sequences. The first rejection of each parameter switches the
// request shape for the provider's lifetime, as OpenAI's SDKs do; the deployment
// does not change while the process runs.
type chatCompat struct {
	mu                  sync.Mutex
	maxCompletionTokens bool
	dropTemperature     bool
	dropStop            bool
	dropEffort          bool

	// headroom is added to MaxTokens once the model is known to reason.
	headroom int
	// effort is sent as reasoning_effort to reasoning models when set
	// (for example "minimal" or "low"), to keep reasoning short.
	effort string
}

func newChatCompat() *chatCompat {
	c := &chatCompat{headroom: defaultReasoningHeadroom}
	if v, err := strconv.Atoi(os.Getenv("LLM_REASONING_TOKEN_HEADROOM")); err == nil && v >= 0 {
		c.headroom = v
	}
	c.effort = strings.TrimSpace(os.Getenv("LLM_REASONING_EFFORT"))
	// Deployments known to reason can skip the first rejected request.
	if b, _ := strconv.ParseBool(os.Getenv("LLM_REASONING_MODEL")); b {
		c.maxCompletionTokens, c.dropTemperature, c.dropStop = true, true, true
	}
	return c
}

// reasoning reports whether the model has been identified as a reasoning model.
func (c *chatCompat) reasoning() bool {
	return c.maxCompletionTokens
}

// body builds the request body for req in the shape the model accepts.
func (c *chatCompat) body(req CompletionRequest, messages []map[string]string) map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	b := map[string]interface{}{"messages": messages}
	if c.maxCompletionTokens {
		b["max_completion_tokens"] = req.MaxTokens + c.headroom
		if c.effort != "" && !c.dropEffort {
			b["reasoning_effort"] = c.effort
		}
	} else {
		b["max_tokens"] = req.MaxTokens
	}
	if !c.dropTemperature {
		b["temperature"] = req.Temperature
	}
	if len(req.Stop) > 0 && !c.dropStop {
		b["stop"] = req.Stop
	}
	if req.JSONSchema != nil {
		b["response_format"] = map[string]interface{}{
			"type":        "json_schema",
			"json_schema": req.JSONSchema,
		}
	}
	return b
}

// adapt inspects a 400 response and changes the request shape if it rejected a
// parameter this type knows how to replace. It reports whether a retry can help.
func (c *chatCompat) adapt(status int, respBody []byte) bool {
	if status != 400 {
		return false
	}
	var parsed struct {
		Error struct {
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(respBody, &parsed)
	param := parsed.Error.Param
	msg := parsed.Error.Message
	mentions := func(name string) bool {
		return param == name || strings.Contains(msg, "'"+name+"'")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case mentions("max_tokens") && !c.maxCompletionTokens:
		c.maxCompletionTokens = true
	case mentions("temperature") && !c.dropTemperature:
		c.dropTemperature = true
	case mentions("stop") && !c.dropStop:
		c.dropStop = true
	case mentions("reasoning_effort") && !c.dropEffort:
		c.dropEffort = true
	default:
		return false
	}
	return true
}

// emptyCompletionError explains an answer with no content.
func (c *chatCompat) emptyCompletionError(finishReason string) error {
	if finishReason == "length" && c.reasoning() {
		return fmt.Errorf("model returned no content (finish_reason=length): reasoning used the whole completion budget; raise LLM_REASONING_TOKEN_HEADROOM (now %d) or set LLM_REASONING_EFFORT=low", c.headroom)
	}
	return fmt.Errorf("model returned no content (finish_reason=%q)", finishReason)
}

// chatResponse is the part of a chat-completions response Athena reads.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// maxCompatRetries bounds the number of request shapes tried for one completion:
// one per parameter chatCompat can adapt.
const maxCompatRetries = 4

// chatMessages builds the message list for a completion request.
func chatMessages(req CompletionRequest) []map[string]string {
	messages := []map[string]string{}
	if req.SystemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.SystemPrompt})
	}
	return append(messages, map[string]string{"role": "user", "content": req.Prompt})
}

// postChatCompletion sends a chat-completions request, adapting its shape and
// retrying when the model rejects a parameter chatCompat can replace. extra holds
// provider-specific fields (such as "model"); setAuth adds authentication headers.
func postChatCompletion(ctx context.Context, client *http.Client, url, provider string, compat *chatCompat, req CompletionRequest, extra map[string]interface{}, setAuth func(*http.Request)) (string, error) {
	messages := chatMessages(req)
	for attempt := 0; ; attempt++ {
		body := compat.body(req, messages)
		for k, v := range extra {
			body[k] = v
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("failed to marshal request: %w", err)
		}
		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		setAuth(httpReq)

		resp, err := client.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("failed to call %s: %w", provider, err)
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read %s response: %w", provider, err)
		}
		if resp.StatusCode != http.StatusOK {
			if attempt < maxCompatRetries && compat.adapt(resp.StatusCode, respBody) {
				continue
			}
			return "", fmt.Errorf("%s API returned status %d: %s", provider, resp.StatusCode, string(respBody))
		}

		var parsed chatResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return "", fmt.Errorf("failed to decode response: %w", err)
		}
		if len(parsed.Choices) == 0 {
			return "", fmt.Errorf("no choices in response")
		}
		choice := parsed.Choices[0]
		if strings.TrimSpace(choice.Message.Content) == "" {
			return "", compat.emptyCompletionError(choice.FinishReason)
		}
		return choice.Message.Content, nil
	}
}
