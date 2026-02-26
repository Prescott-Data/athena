package memory

import (
	"context"
	"fmt"
	"log"

	"github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/http"
)

// InitializeLTMGraph connects to ArangoDB, creates the database if it doesn't exist,
// and ensures all required document and edge collections are present.
func InitializeLTMGraph(ctx context.Context, dbURL, user, pass string) error {
	// 1. Connect to ArangoDB
	conn, err := http.NewConnection(http.ConnectionConfig{
		Endpoints: []string{dbURL},
	})
	if err != nil {
		return fmt.Errorf("failed to create arangodb connection: %w", err)
	}

	client, err := driver.NewClient(driver.ClientConfig{
		Connection:     conn,
		Authentication: driver.BasicAuthentication(user, pass),
	})
	if err != nil {
		return fmt.Errorf("failed to create arangodb client: %w", err)
	}

	// 2. Check if database exists, if not, create it
	dbName := "athena_ltm"
	exists, err := client.DatabaseExists(ctx, dbName)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	var db driver.Database
	if !exists {
		log.Printf("INFO: Database %s does not exist, creating it...", dbName)
		db, err = client.CreateDatabase(ctx, dbName, nil)
		if err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}
	} else {
		log.Printf("INFO: Database %s already exists.", dbName)
		db, err = client.Database(ctx, dbName)
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
	}

	// 3. Ensure Document Collections (Nodes)
	docCollections := []string{
		"Identities", // e.g., Sangalo, Athena, OpenClaw
		"Concepts",   // e.g., Agentic AI, Spaced Repetition
		"Tools",      // e.g., Golang, Docker, ArangoDB
		"Projects",   // e.g., Nexus Protocol, joedowns.ai
	}

	for _, colName := range docCollections {
		err := ensureCollection(ctx, db, colName, driver.CollectionTypeDocument)
		if err != nil {
			return err
		}
	}

	// 4. Ensure Edge Collection (Relationships)
	edgeCollections := []string{
		"MemoryEdges", // Verbs: USES, WORKS_ON, EXHIBITS (properties: heat_score, confidence)
	}

	for _, colName := range edgeCollections {
		err := ensureCollection(ctx, db, colName, driver.CollectionTypeEdge)
		if err != nil {
			return err
		}
	}

	log.Println("SUCCESS: Successfully initialized LTM graph in ArangoDB.")
	return nil
}

func ensureCollection(ctx context.Context, db driver.Database, colName string, colType driver.CollectionType) error {
	exists, err := db.CollectionExists(ctx, colName)
	if err != nil {
		return fmt.Errorf("failed to check if collection %s exists: %w", colName, err)
	}

	if !exists {
		log.Printf("INFO: Collection %s does not exist, creating it...", colName)
		_, err = db.CreateCollection(ctx, colName, &driver.CreateCollectionOptions{
			Type: colType,
		})
		if err != nil {
			return fmt.Errorf("failed to create collection %s: %w", colName, err)
		}
	} else {
		log.Printf("INFO: Collection %s already exists.", colName)
	}

	return nil
}
