package main

import (
	"context"
	"log"
	"os"

	"bitbucket.org/dromos/memory-os/pkg/memory"
)

func main() {
	dbURL := os.Getenv("ARANGODB_URL")
	if dbURL == "" {
		dbURL = "http://localhost:8529"
	}

	user := os.Getenv("ARANGODB_USER")
	if user == "" {
		user = "root"
	}

	pass := os.Getenv("ARANGODB_PASSWORD")
	if pass == "" {
		log.Fatal("ARANGODB_PASSWORD env var is required")
	}

	log.Printf("Initializing ArangoDB LTM Graph at %s...", dbURL)
	err := memory.InitializeLTMGraph(context.Background(), dbURL, user, pass)
	if err != nil {
		log.Fatalf("Failed to initialize LTM graph: %v", err)
	}
	log.Println("LTM Graph initialization complete.")
}
