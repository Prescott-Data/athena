package main

import (
	"context"
	"log"
	"os"

	"bitbucket.org/dromos/athena-memos/pkg/memory"
)

func main() {
	dbURL := os.Getenv("ARANGODB_URL")
	if dbURL == "" {
		arangoHost := os.Getenv("ARANGODB_HOST")
		arangoPort := os.Getenv("ARANGODB_PORT")
		if arangoHost == "" {
			log.Fatal("ARANGODB_HOST or ARANGODB_URL must be set")
		}
		if arangoPort == "" {
			arangoPort = "8529"
		}
		dbURL = "http://" + arangoHost + ":" + arangoPort
	}

	user := os.Getenv("ARANGODB_USER")
	if user == "" {
		user = "root"
	}

	pass := os.Getenv("ARANGO_ROOT_PASSWORD")
	if pass == "" {
		pass = os.Getenv("ARANGODB_PASSWORD")
	}
	if pass == "" {
		log.Fatal("ARANGO_ROOT_PASSWORD or ARANGODB_PASSWORD env var is required")
	}

	log.Printf("Initializing ArangoDB LTM Graph at %s...", dbURL)
	err := memory.InitializeLTMGraph(context.Background(), dbURL, user, pass)
	if err != nil {
		log.Fatalf("Failed to initialize LTM graph: %v", err)
	}
	log.Println("LTM Graph initialization complete.")
}
