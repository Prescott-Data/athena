package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Prescott-Data/athena/internal/llm"
	// Load environment variables from .env file
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	// Manually set vars if .env autoload fails or for explicit test
	if os.Getenv("LLM_API_KEY") == "" {
		fmt.Println("LLM_API_KEY not found in env, please set it.")
		return
	}

	fmt.Println("Testing Gemini Provider...")
	factory := &llm.Factory{}
	provider, err := factory.NewProvider("gemini")
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test Completion
	fmt.Println("1. Testing Generation...")
	resp, err := provider.GenerateCompletion(ctx, llm.CompletionRequest{
		Prompt:      "Hello! Are you working?",
		MaxTokens:   50,
		Temperature: 0.7,
	})
	if err != nil {
		log.Printf("❌ Generation failed: %v", err)
	} else {
		fmt.Printf("✅ Generation success: %s\n", resp)
	}

	// Test Embedding
	fmt.Println("\n2. Testing Embedding...")
	vec, err := provider.CreateEmbedding(ctx, llm.EmbeddingRequest{
		Input: "This is a test sentence.",
	})
	if err != nil {
		log.Printf("❌ Embedding failed: %v", err)
	} else {
		fmt.Printf("✅ Embedding success. Vector length: %d\n", len(vec))
	}
}
