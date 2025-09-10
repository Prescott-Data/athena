package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dromos-org/memory-os/internal/config"
	"github.com/dromos-org/memory-os/internal/database"
)

func main() {
	fmt.Println("🧠 Memory OS Azure Infrastructure Connectivity Test")
	fmt.Println("=================================================")

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	fmt.Printf("Testing connectivity to Azure infrastructure at %s\n\n", cfg.Database.Redis.Host)

	// Test Redis connection
	fmt.Print("🔴 Testing Redis connection... ")
	redisClient, err := database.ConnectRedis(&database.RedisConfig{
		Host:     cfg.Database.Redis.Host,
		Port:     cfg.Database.Redis.Port,
		Password: cfg.Database.Redis.Password,
		DB:       cfg.Database.Redis.DB,
	})
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		fmt.Printf("✅ SUCCESS\n")
		redisClient.Close()
	}

	// Test MongoDB connection
	fmt.Print("🍃 Testing MongoDB connection... ")
	mongoClient, err := database.ConnectMongoDB(&database.ConnectionConfig{
		URI:         cfg.Database.MongoDB.URI,
		Database:    cfg.Database.MongoDB.Database,
		MaxPoolSize: 10,
		Timeout:     30 * time.Second,
	})
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		fmt.Printf("✅ SUCCESS\n")

		// Test basic operations
		ctx := context.Background()
		exists, err := database.CollectionExists(ctx, mongoClient, "sessions")
		if err != nil {
			fmt.Printf("   ⚠️  Warning: Could not check collections: %v\n", err)
		} else if exists {
			fmt.Printf("   📊 Memory OS collections found\n")
		} else {
			fmt.Printf("   📊 Collections need to be initialized\n")
		}
	}

	// Test Milvus connection
	fmt.Print("🔍 Testing Milvus connection... ")
	milvusClient, err := database.ConnectMilvus(&database.MilvusConfig{
		Host: cfg.Database.Milvus.Host,
		Port: cfg.Database.Milvus.Port,
	})
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		fmt.Printf("✅ SUCCESS\n")
		milvusClient.Close()
	}

	fmt.Println("\n🎉 Connectivity test completed!")
	fmt.Println("If all tests passed, Memory OS is ready to connect to your Azure infrastructure.")
}
