package main

import (
	"context"
	"log"

	"github.com/Prescott-Data/athena/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	ctx := context.Background()

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://admin:admin123@localhost:27017"))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Disconnect(ctx)

	col := mongoClient.Database("memory_os").Collection("cognitive_chains")

	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		log.Fatalf("Find failed: %v", err)
	}
	defer cursor.Close(ctx)

	var chains []models.CognitiveChain
	if err := cursor.All(ctx, &chains); err != nil {
		log.Fatalf("Cursor.All failed: %v", err)
	}

	for _, c := range chains {
		log.Printf("Chain: %s | StartedAt: %v | LastEventAt: %v | LastAccessedAt: %v | Heat: %v",
			c.ChainID, c.StartedAt, c.LastEventAt, c.LastAccessedAt, c.HeatScore)
	}
}
