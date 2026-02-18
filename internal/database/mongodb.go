package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoClient holds the connection to the MongoDB server
var MongoClient *mongo.Client

// DB represents the specific MongoDB database we're working with
var DB *mongo.Database

// ConnectionConfig holds MongoDB connection configuration
type ConnectionConfig struct {
	URI            string
	DatabaseName   string
	ConnectTimeout time.Duration
}

// InitMongoDB initializes the MongoDB client and establishes a connection.
func InitMongoDB() {
	config := getConnectionConfig()

	client, db, err := ConnectMongoDB(config)
	if err != nil {
		log.Fatalf("Failed to initialize MongoDB: %v", err)
	}

	// Set global variables
	MongoClient = client
	DB = db

	// Create tenant-aware indexes
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := CreateTenantIndexes(ctx, db); err != nil {
		log.Printf("WARN: Failed to create tenant indexes: %v", err)
		// Don't fail initialization for index creation issues
	}

	log.Println("✅ MongoDB connection established")
}

// getConnectionConfig reads configuration from environment variables
func getConnectionConfig() ConnectionConfig {
	config, err := loadConnectionConfig()
	if err != nil {
		log.Fatal(err.Error())
	}
	return config
}

// loadConnectionConfig loads configuration without calling log.Fatal (testable version)
func loadConnectionConfig() (ConnectionConfig, error) {
	// Try to load .env file - ignore errors if file doesn't exist (e.g., during tests)
	_ = godotenv.Load("../../.env.dev")

	uri := os.Getenv("MONGO_URI")
	dbName := os.Getenv("MONGO_DB")

	if uri == "" || dbName == "" {
		return ConnectionConfig{}, fmt.Errorf("missing MONGO_URI or MONGO_DB env variable")
	}

	return ConnectionConfig{
		URI:            uri,
		DatabaseName:   dbName,
		ConnectTimeout: 10 * time.Second,
	}, nil
}

// ConnectMongoDB creates and tests a MongoDB connection with the given configuration
func ConnectMongoDB(config ConnectionConfig) (*mongo.Client, *mongo.Database, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.ConnectTimeout)
	defer cancel()

	clientOpts := options.Client().ApplyURI(config.URI)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Test the connection
	if err := client.Ping(ctx, nil); err != nil {
		// Clean up the client if ping fails
		if disconnectErr := client.Disconnect(ctx); disconnectErr != nil {
			log.Printf("Failed to disconnect MongoDB client after ping failure: %v", disconnectErr)
		}
		return nil, nil, fmt.Errorf("MongoDB ping failed: %w", err)
	}

	db := client.Database(config.DatabaseName)
	return client, db, nil
}

// HealthCheck performs a health check on the MongoDB connection
func HealthCheck() error {
	if MongoClient == nil {
		return fmt.Errorf("MongoDB client is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return MongoClient.Ping(ctx, nil)
}

// GetDatabase returns the current database instance
func GetDatabase() *mongo.Database {
	return DB
}

// GetClient returns the current MongoDB client instance
func GetClient() *mongo.Client {
	return MongoClient
}

// CreateTenantIndexes creates compound indexes for tenant/user/agent queries
func CreateTenantIndexes(ctx context.Context, db *mongo.Database) error {
	// Dialogue Pages collection indexes
	pagesCollection := db.Collection("dialogue_pages")
	pageIndexes := []mongo.IndexModel{
		{
			Keys: map[string]interface{}{
				"tenantId":  1,
				"userId":    1,
				"agentId":   1,
				"chainId":   1,
				"turnIndex": -1,
			},
		},
		{
			Keys: map[string]interface{}{
				"tenantId":  1,
				"userId":    1,
				"agentId":   1,
				"createdAt": -1,
			},
		},
		{
			Keys: map[string]interface{}{
				"tenantId": 1,
				"userId":   1,
				"agentId":  1,
				"status":   1,
			},
		},
	}

	// Segments collection indexes
	segmentsCollection := db.Collection("segments")
	segmentIndexes := []mongo.IndexModel{
		{
			Keys: map[string]interface{}{
				"tenantId":  1,
				"userId":    1,
				"agentId":   1,
				"segmentId": 1,
			},
		},
		{
			Keys: map[string]interface{}{
				"tenantId":  1,
				"userId":    1,
				"agentId":   1,
				"createdAt": -1,
			},
		},
		{
			Keys: map[string]interface{}{
				"tenantId": 1,
				"userId":   1,
				"agentId":  1,
				"status":   1,
			},
		},
		{
			Keys: map[string]interface{}{
				"tenantId":  1,
				"userId":    1,
				"agentId":   1,
				"heatScore": -1,
			},
		},
	}

	// Cognitive Chains collection indexes
	chainsCollection := db.Collection("cognitive_chains")
	chainIndexes := []mongo.IndexModel{
		{
			Keys: map[string]interface{}{
				"tenantId": 1,
				"userId":   1,
				"agentId":  1,
				"chainId":  1,
			},
		},
		{
			Keys: map[string]interface{}{
				"tenantId":  1,
				"userId":    1,
				"agentId":   1,
				"status":    1,
				"startedAt": -1,
			},
		},
		{
			Keys: map[string]interface{}{
				"metadata.origin_service": 1,
			},
		},
		{
			Keys: map[string]interface{}{
				"metadata.context_type": 1,
			},
		},
	}

	// Create indexes for dialogue pages
	for _, index := range pageIndexes {
		if _, err := pagesCollection.Indexes().CreateOne(ctx, index); err != nil {
			log.Printf("WARN: Failed to create dialogue pages index: %v", err)
		}
	}

	// Create indexes for segments
	for _, index := range segmentIndexes {
		if _, err := segmentsCollection.Indexes().CreateOne(ctx, index); err != nil {
			log.Printf("WARN: Failed to create segments index: %v", err)
		}
	}

	// Create indexes for dialogue chains
	for _, index := range chainIndexes {
		if _, err := chainsCollection.Indexes().CreateOne(ctx, index); err != nil {
			log.Printf("WARN: Failed to create dialogue chains index: %v", err)
		}
	}

	log.Println("✅ Tenant-aware MongoDB indexes created")
	return nil
}

// DisconnectMongoDB gracefully disconnects from MongoDB
func DisconnectMongoDB() error {
	if MongoClient == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := MongoClient.Disconnect(ctx)
	if err != nil {
		return fmt.Errorf("failed to disconnect from MongoDB: %w", err)
	}

	// Clear global variables
	MongoClient = nil
	DB = nil

	log.Println("✅ MongoDB connection closed")
	return nil
}
