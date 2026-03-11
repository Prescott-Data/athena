package testbasic

import (
	"context"
	"fmt"
	"log"
	"time"

	"bitbucket.org/dromos/athena-memos/internal/config"
	"bitbucket.org/dromos/athena-memos/internal/database"
)

func testConnectivity() {
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
	mongoClient, db, err := database.ConnectMongoDB(database.ConnectionConfig{
		URI:            cfg.Database.MongoDB.URI,
		DatabaseName:   cfg.Database.MongoDB.Database,
		ConnectTimeout: 30 * time.Second,
	})
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		fmt.Printf("✅ SUCCESS\n")

		// Test basic operations
		ctx := context.Background()
		collections, err := db.ListCollectionNames(ctx, nil)
		if err != nil {
			fmt.Printf("   ⚠️  Warning: Could not check collections: %v\n", err)
		} else if len(collections) > 0 {
			fmt.Printf("   📊 Found %d collections in database\n", len(collections))
		} else {
			fmt.Printf("   📊 Database is empty, collections need to be initialized\n")
		}

		// Cleanup MongoDB connection
		if err := mongoClient.Disconnect(ctx); err != nil {
			fmt.Printf("   ⚠️  Warning: Could not disconnect MongoDB: %v\n", err)
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
