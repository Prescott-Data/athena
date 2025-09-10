package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	azureVMIP         = "172.190.152.215"
	redisPort         = "6379"
	mongoPort         = "27017"
	milvusPort        = "19530"
	milvusHealthPort  = "9091"
	redisPassword     = "dromos_redis_2024"
	mongoURI          = "mongodb://memory_user:memory_password_2024@172.190.152.215:27017/memory_os?retryWrites=true&authSource=memory_os"
)

func main() {
	fmt.Println("🧠 Memory OS Azure Infrastructure Integration Test")
	fmt.Println("=======================================================")
	fmt.Printf("Testing Azure VM: %s\n\n", azureVMIP)

	allPassed := true

	// Test 1: Network connectivity
	fmt.Println("1. 🌐 Testing Network Connectivity...")
	if testNetworkConnectivity() {
		fmt.Println("   ✅ Network connectivity: SUCCESS")
	} else {
		fmt.Println("   ❌ Network connectivity: FAILED")
		allPassed = false
	}

	// Test 2: Redis connectivity
	fmt.Println("\n2. 🔴 Testing Redis Connectivity...")
	if testRedisConnectivity() {
		fmt.Println("   ✅ Redis connectivity: SUCCESS")
	} else {
		fmt.Println("   ❌ Redis connectivity: FAILED")
		allPassed = false
	}

	// Test 3: MongoDB connectivity
	fmt.Println("\n3. 🍃 Testing MongoDB Connectivity...")
	if testMongoDBConnectivity() {
		fmt.Println("   ✅ MongoDB connectivity: SUCCESS")
	} else {
		fmt.Println("   ❌ MongoDB connectivity: FAILED")
		allPassed = false
	}

	// Test 4: Milvus connectivity
	fmt.Println("\n4. 🔍 Testing Milvus Connectivity...")
	if testMilvusConnectivity() {
		fmt.Println("   ✅ Milvus connectivity: SUCCESS")
	} else {
		fmt.Println("   ❌ Milvus connectivity: FAILED")
		allPassed = false
	}

	// Summary
	fmt.Println("\n=======================================================")
	if allPassed {
		fmt.Println("🎉 ALL TESTS PASSED! Memory OS is ready for Azure integration!")
		fmt.Println("\n📋 Next Steps:")
		fmt.Println("   1. Build Memory OS: go build -o memory-server cmd/memory-server/main.go")
		fmt.Println("   2. Set environment variables for Azure endpoints")
		fmt.Println("   3. Start Memory OS server: ./memory-server")
		fmt.Println("   4. Test Memory OS API endpoints")
	} else {
		fmt.Println("❌ SOME TESTS FAILED! Please check the Azure infrastructure.")
		fmt.Println("   Review the failing services and ensure they are running properly.")
	}
}

func testNetworkConnectivity() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%s", azureVMIP, "22"), 5*time.Second)
	if err != nil {
		fmt.Printf("   ⚠️  SSH port not accessible: %v\n", err)
		return false
	}
	conn.Close()
	return true
}

func testRedisConnectivity() bool {
	ctx := context.Background()
	
	// Create Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%s", azureVMIP, redisPort),
		Password:     redisPassword,
		DB:           0,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	defer rdb.Close()

	// Test ping
	pong, err := rdb.Ping(ctx).Result()
	if err != nil {
		fmt.Printf("   ⚠️  Redis ping failed: %v\n", err)
		return false
	}
	
	if pong != "PONG" {
		fmt.Printf("   ⚠️  Redis ping returned unexpected response: %s\n", pong)
		return false
	}

	// Test basic operations
	err = rdb.Set(ctx, "test_key", "test_value", time.Minute).Err()
	if err != nil {
		fmt.Printf("   ⚠️  Redis SET operation failed: %v\n", err)
		return false
	}

	val, err := rdb.Get(ctx, "test_key").Result()
	if err != nil {
		fmt.Printf("   ⚠️  Redis GET operation failed: %v\n", err)
		return false
	}

	if val != "test_value" {
		fmt.Printf("   ⚠️  Redis GET returned unexpected value: %s\n", val)
		return false
	}

	// Cleanup
	rdb.Del(ctx, "test_key")
	
	fmt.Println("   📊 Redis operations (SET/GET) working correctly")
	return true
}

func testMongoDBConnectivity() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create MongoDB client
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB connection failed: %v\n", err)
		return false
	}
	defer client.Disconnect(ctx)

	// Test ping
	err = client.Ping(ctx, nil)
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB ping failed: %v\n", err)
		return false
	}

	// Test database access
	db := client.Database("memory_os")
	collections, err := db.ListCollectionNames(ctx, map[string]interface{}{})
	if err != nil {
		fmt.Printf("   ⚠️  Failed to list collections: %v\n", err)
		return false
	}

	fmt.Printf("   📊 MongoDB database 'memory_os' accessible (%d collections)\n", len(collections))
	
	// Test basic operations
	collection := db.Collection("test_collection")
	testDoc := map[string]interface{}{"test": "document", "timestamp": time.Now()}
	
	insertResult, err := collection.InsertOne(ctx, testDoc)
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB insert operation failed: %v\n", err)
		return false
	}

	// Cleanup
	collection.DeleteOne(ctx, map[string]interface{}{"_id": insertResult.InsertedID})
	
	fmt.Println("   📊 MongoDB operations (INSERT/DELETE) working correctly")
	return true
}

func testMilvusConnectivity() bool {
	// Test Milvus health endpoint
	client := &http.Client{Timeout: 5 * time.Second}
	
	resp, err := client.Get(fmt.Sprintf("http://%s:%s/healthz", azureVMIP, milvusHealthPort))
	if err != nil {
		fmt.Printf("   ⚠️  Milvus health check failed: %v\n", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("   ⚠️  Milvus health check returned status: %d\n", resp.StatusCode)
		return false
	}

	fmt.Println("   📊 Milvus health endpoint responding correctly")

	// Test TCP connectivity to Milvus gRPC port
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%s", azureVMIP, milvusPort), 5*time.Second)
	if err != nil {
		fmt.Printf("   ⚠️  Milvus gRPC port not accessible: %v\n", err)
		return false
	}
	conn.Close()

	fmt.Println("   📊 Milvus gRPC port accessible")
	return true
}
