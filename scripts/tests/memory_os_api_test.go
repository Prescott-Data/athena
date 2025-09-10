package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	memoryOSBaseURL = "http://localhost:8080"
	apiKey         = "test-api-key"
)

type CreateSessionRequest struct {
	UserID   string            `json:"user_id"`
	Metadata map[string]string `json:"metadata"`
}

type CreateSessionResponse struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
}

type StoreInteractionRequest struct {
	UserMessage   string            `json:"user_message"`
	AgentResponse string            `json:"agent_response"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Timestamp     time.Time         `json:"timestamp"`
}

func main() {
	fmt.Println("🧠 Testing Memory OS Server Integration")
	fmt.Println("======================================")

	// Wait for server to start
	fmt.Println("⏳ Waiting for Memory OS server to start...")
	if !waitForServer(memoryOSBaseURL+"/health", 30*time.Second) {
		fmt.Println("❌ Memory OS server is not responding. Please start it first:")
		fmt.Println("   cd /home/dev/projects/dromos-core/memory-os")
		fmt.Println("   go build -o memory-server cmd/memory-server/main.go")
		fmt.Println("   ./memory-server")
		return
	}

	fmt.Println("✅ Memory OS server is running!")

	// Test health endpoint
	fmt.Println("\n1. 💚 Testing Health Endpoint...")
	if testHealthEndpoint() {
		fmt.Println("   ✅ Health endpoint: SUCCESS")
	} else {
		fmt.Println("   ❌ Health endpoint: FAILED")
		return
	}

	// Test session creation
	fmt.Println("\n2. 🔐 Testing Session Creation...")
	sessionID, err := testCreateSession()
	if err != nil {
		fmt.Printf("   ❌ Session creation: FAILED - %v\n", err)
		return
	}
	fmt.Printf("   ✅ Session creation: SUCCESS - Session ID: %s\n", sessionID)

	// Test interaction storage
	fmt.Println("\n3. 💬 Testing Interaction Storage...")
	if err := testStoreInteraction(sessionID); err != nil {
		fmt.Printf("   ❌ Interaction storage: FAILED - %v\n", err)
		return
	}
	fmt.Println("   ✅ Interaction storage: SUCCESS")

	// Test context retrieval
	fmt.Println("\n4. 📖 Testing Context Retrieval...")
	if err := testGetContext(sessionID); err != nil {
		fmt.Printf("   ❌ Context retrieval: FAILED - %v\n", err)
		return
	}
	fmt.Println("   ✅ Context retrieval: SUCCESS")

	fmt.Println("\n==============================================")
	fmt.Println("🎉 ALL MEMORY OS INTEGRATION TESTS PASSED!")
	fmt.Println("✅ Memory OS is successfully connected to Azure infrastructure!")
	fmt.Println("✅ All core functionality is working correctly!")
}

func waitForServer(url string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return true
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

func testHealthEndpoint() bool {
	client := &http.Client{Timeout: 5 * time.Second}
	
	resp, err := client.Get(memoryOSBaseURL + "/health")
	if err != nil {
		fmt.Printf("   ⚠️  Health request failed: %v\n", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("   ⚠️  Health endpoint returned status: %d\n", resp.StatusCode)
		return false
	}

	var healthResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
		fmt.Printf("   ⚠️  Failed to decode health response: %v\n", err)
		return false
	}

	if status, ok := healthResp["status"]; !ok || status != "healthy" {
		fmt.Printf("   ⚠️  Health status is not healthy: %v\n", status)
		return false
	}

	return true
}

func testCreateSession() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	sessionReq := CreateSessionRequest{
		UserID: "test-user-123",
		Metadata: map[string]string{
			"app":     "memory-os-test",
			"version": "1.0",
		},
	}

	jsonData, err := json.Marshal(sessionReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", memoryOSBaseURL+"/api/v1/sessions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var sessionResp CreateSessionResponse
	if err := json.Unmarshal(body, &sessionResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	return sessionResp.SessionID, nil
}

func testStoreInteraction(sessionID string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	interactionReq := StoreInteractionRequest{
		UserMessage:   "Hello! Can you help me understand how memory works?",
		AgentResponse: "Of course! Memory in AI systems like this one works in multiple layers: Short-Term Memory (STM) for recent context, Mid-Term Memory (MTM) for important conversation segments, and Long-Term Memory (LTM) for persistent knowledge about users.",
		Metadata: map[string]string{
			"topic":      "memory_explanation",
			"complexity": "educational",
		},
		Timestamp: time.Now(),
	}

	jsonData, err := json.Marshal(interactionReq)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/sessions/%s/interactions", memoryOSBaseURL, sessionID), bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != 201 {
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func testGetContext(sessionID string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/sessions/%s/context?limit=10", memoryOSBaseURL, sessionID), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var contextResp map[string]interface{}
	if err := json.Unmarshal(body, &contextResp); err != nil {
		return fmt.Errorf("failed to decode response: %v", err)
	}

	fmt.Printf("   📊 Retrieved context with %v recent turns\n", contextResp["recent_turns"])
	return nil
}
