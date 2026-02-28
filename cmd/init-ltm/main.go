package main

import (
	"context"
	"log"

	"bitbucket.org/dromos/memory-os/pkg/memory"
)

func main() {
	log.Println("Initializing ArangoDB LTM Graph...")
	err := memory.InitializeLTMGraph(context.Background(), "http://localhost:8529", "root", "athena_dev")
	if err != nil {
		log.Fatalf("Failed to initialize LTM graph: %v", err)
	}
	log.Println("LTM Graph initialization complete.")
}
