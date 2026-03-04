package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/http"
)

func main() {
	log.Println("=== Starting LTM Graph Analytics Verification ===")

	dbURL := os.Getenv("ARANGODB_URL")
	if dbURL == "" {
		dbURL = "http://localhost:8529"
	}
	dbUser := os.Getenv("ARANGODB_USER")
	if dbUser == "" {
		dbUser = "root"
	}
	dbPass := os.Getenv("ARANGODB_PASSWORD")
	if dbPass == "" {
		dbPass = "athena_dev"
	}
	dbName := os.Getenv("ARANGODB_DATABASE")
	if dbName == "" {
		dbName = "athena_ltm"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	conn, err := http.NewConnection(http.ConnectionConfig{
		Endpoints: []string{dbURL},
	})
	if err != nil {
		log.Fatalf("Failed to create arangodb connection: %v", err)
	}

	client, err := driver.NewClient(driver.ClientConfig{
		Connection:     conn,
		Authentication: driver.BasicAuthentication(dbUser, dbPass),
	})
	if err != nil {
		log.Fatalf("Failed to create arangodb client: %v", err)
	}

	exists, err := client.DatabaseExists(ctx, dbName)
	if err != nil {
		log.Fatalf("Failed to check database existence: %v", err)
	}
	if !exists {
		log.Fatalf("Database %s does not exist", dbName)
	}

	db, err := client.Database(ctx, dbName)
	if err != nil {
		log.Fatalf("Failed to open database %s: %v", dbName, err)
	}

	// Wait a moment for background Pregel/AQL jobs to complete after being triggered
	// by the simulation script. We'll poll up to 30 seconds.
	log.Println("Polling for community_id assignments (max 30s)...")
	success := false
	for i := 0; i < 15; i++ {
		if checkCommunitiesExist(ctx, db) {
			success = true
			break
		}
		time.Sleep(2 * time.Second)
	}

	if !success {
		log.Fatalf("FAILED: No nodes found with community_id assigned.")
	}

	log.Println("Polling for bridge entity assignments (max 10s)...")
	bridgeSuccess := false
	for i := 0; i < 5; i++ {
		if checkBridgesExist(ctx, db) {
			bridgeSuccess = true
			break
		}
		time.Sleep(2 * time.Second)
	}

	if !bridgeSuccess {
		// Bridge entities might not exist if the graph is too sparse/linear,
		// so we just warn rather than fail the entire simulation.
		log.Println("WARN: No bridge entities found. The graph topology may not be dense enough to form multiple communities.")
	}

	log.Println("=== Verification Complete ===")
}

func checkCommunitiesExist(ctx context.Context, db driver.Database) bool {
	query := `
		FOR node IN UNION(Identities, Concepts, Tools, Projects)
			FILTER HAS(node, "community_id") AND node.community_id != null
			LIMIT 1
			RETURN node
	`
	cursor, err := db.Query(ctx, query, nil)
	if err != nil {
		log.Printf("Query error checking communities: %v", err)
		return false
	}
	defer cursor.Close()

	if cursor.HasMore() {
		var doc map[string]interface{}
		_, _ = cursor.ReadDocument(ctx, &doc)
		log.Printf("SUCCESS: Found node assigned to community %v: '%s' (collection: %v)",
			doc["community_id"], doc["name"], doc["_id"])
		return true
	}

	return false
}

func checkBridgesExist(ctx context.Context, db driver.Database) bool {
	query := `
		FOR node IN UNION(Identities, Concepts, Tools, Projects)
			FILTER HAS(node, "is_bridge") AND node.is_bridge == true
			LIMIT 1
			RETURN node
	`
	cursor, err := db.Query(ctx, query, nil)
	if err != nil {
		log.Printf("Query error checking bridges: %v", err)
		return false
	}
	defer cursor.Close()

	if cursor.HasMore() {
		var doc map[string]interface{}
		_, _ = cursor.ReadDocument(ctx, &doc)
		log.Printf("SUCCESS: Found Bridge Entity connecting %v communities: '%s' (collection: %v)",
			doc["bridge_score"], doc["name"], doc["_id"])
		return true
	}

	return false
}
