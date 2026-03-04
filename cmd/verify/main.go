package main

import (
	"context"
	"log"

	"github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/http"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	ctx := context.Background()

	// 1. Check MongoDB for Archival status
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://admin:admin123@localhost:27017"))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Disconnect(ctx)

	col := mongoClient.Database("memory_os").Collection("cognitive_chains")
	archivedCount, err := col.CountDocuments(ctx, bson.M{"status": "archived"})
	if err != nil {
		log.Printf("Failed to count archived chains: %v", err)
	}
	log.Printf("MongoDB: Found %d archived chains.", archivedCount)

	// 2. Check ArangoDB for LTM Graph entities and relations
	conn, err := http.NewConnection(http.ConnectionConfig{Endpoints: []string{"http://localhost:8529"}})
	if err != nil {
		log.Fatalf("ArangoDB connection failed: %v", err)
	}

	client, err := driver.NewClient(driver.ClientConfig{
		Connection:     conn,
		Authentication: driver.BasicAuthentication("root", "athena_dev"),
	})
	if err != nil {
		log.Fatalf("ArangoDB client failed: %v", err)
	}

	db, err := client.Database(ctx, "athena_ltm")
	if err != nil {
		log.Fatalf("Failed to open athena_ltm db: %v", err)
	}

	collections := []string{"Concepts", "Projects", "Tools", "MemoryEdges", "Identities"}
	for _, colName := range collections {
		c, err := db.Collection(ctx, colName)
		if err != nil {
			log.Printf("Collection %s Error: %v", colName, err)
			continue
		}
		count, err := c.Count(ctx)
		if err != nil {
			log.Printf("Collection %s Count Error: %v", colName, err)
			continue
		}
		log.Printf("ArangoDB [%s]: %d documents", colName, count)

		// Print contents of MemoryEdges
		if colName == "MemoryEdges" && count > 0 {
			cursor, err := db.Query(ctx, "FOR doc IN MemoryEdges RETURN doc", nil)
			if err == nil {
				defer cursor.Close()
				var edge map[string]interface{}
				for {
					_, err := cursor.ReadDocument(ctx, &edge)
					if driver.IsNoMoreDocuments(err) {
						break
					}
					log.Printf("  Edge: %v -> [%v] -> %v (Nuance: %v)", edge["_from"], edge["relation"], edge["_to"], edge["context_nuance"])
				}
			}
		}
	}
}
