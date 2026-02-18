package database

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	// "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestMain(m *testing.M) {
	// Load .env.dev from the project root (two levels up from internal/database/)
	err := godotenv.Load("../../.env.dev")
	if err != nil {
		log.Fatalf("FATAL: Could not find .env.dev file at project root. Error: %v", err)
	} else {
		log.Println("INFO: Loaded .env.dev file for testing")
	}

	// Run all the tests in the package
	os.Exit(m.Run())
}

// TestMongoDBConnection tests the MongoDB connection functionality
func TestMongoDBConnection(t *testing.T) {
	// // Setup test container
	// mongoContainer := setupMongoContainer(t)
	// defer func() {
	// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
	// 		t.Errorf("Failed to terminate MongoDB container: %v", err)
	// 	}
	// }()

	// // Get connection string from container
	// connectionString, err := mongoContainer.ConnectionString(context.Background())
	// if err != nil {
	// 	t.Fatalf("Failed to get MongoDB connection string: %v", err)
	// }
	connectionString := os.Getenv("MONGO_URI")
	dbName := os.Getenv("MONGO_DB")

	// Set environment variables for test
	os.Setenv("MONGO_URI", connectionString)
	os.Setenv("MONGO_DB", dbName)

	// Clean up global variables before test
	MongoClient = nil
	DB = nil

	t.Run("TestInitMongoDB_Success", func(t *testing.T) {
		// Test successful initialization
		InitMongoDB()

		// Verify global variables are set
		if MongoClient == nil {
			t.Error("MongoClient should not be nil after successful initialization")
		}

		if DB == nil {
			t.Error("DB should not be nil after successful initialization")
		}

		// Verify database name matches environment variable
		if DB.Name() != dbName {
			t.Errorf("Expected database name '%s', got '%s'", dbName, DB.Name())
		}

		// Test connection by pinging
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := MongoClient.Ping(ctx, nil)
		if err != nil {
			t.Errorf("Failed to ping MongoDB after initialization: %v", err)
		}
	})

	t.Run("TestMongoDBOperations", func(t *testing.T) {
		if MongoClient == nil || DB == nil {
			t.Skip("MongoDB not initialized, skipping operations test")
		}

		// Test basic database operations
		collection := DB.Collection("test_collection")

		// Test insert
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		testDoc := map[string]interface{}{
			"test_field": "test_value",
			"created_at": time.Now(),
		}

		insertResult, err := collection.InsertOne(ctx, testDoc)
		if err != nil {
			// If we don't have write permissions, skip the rest of the test
			t.Skipf("Skipping operations test: no write permissions to database: %v", err)
			return
		}

		if insertResult.InsertedID == nil {
			t.Error("InsertedID should not be nil")
		}

		// Test find
		var result map[string]interface{}
		err = collection.FindOne(ctx, map[string]interface{}{"test_field": "test_value"}).Decode(&result)
		if err != nil {
			t.Errorf("Failed to find test document: %v", err)
		} else {
			if result["test_field"] != "test_value" {
				t.Errorf("Expected test_field to be 'test_value', got '%v'", result["test_field"])
			}
		}

		// Clean up test document
		_, err = collection.DeleteOne(ctx, map[string]interface{}{"test_field": "test_value"})
		if err != nil {
			t.Logf("Warning: Failed to delete test document: %v", err)
		}
	})
}

func TestConnectMongoDB_Success(t *testing.T) {
	// // Setup test container
	// mongoContainer := setupMongoContainer(t)
	// defer func() {
	// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
	// 		t.Errorf("Failed to terminate MongoDB container: %v", err)
	// 	}
	// }()

	// // Get connection string from container
	// connectionString, err := mongoContainer.ConnectionString(context.Background())
	// if err != nil {
	// 	t.Fatalf("Failed to get MongoDB connection string: %v", err)
	// }
	connectionString := os.Getenv("MONGO_URI")
	dbName := os.Getenv("MONGO_DB")

	config := ConnectionConfig{
		URI:            connectionString,
		DatabaseName:   dbName,
		ConnectTimeout: 10 * time.Second,
	}

	client, db, err := ConnectMongoDB(config)
	if err != nil {
		t.Fatalf("ConnectMongoDB() failed: %v", err)
	}
	defer client.Disconnect(context.Background())

	if client == nil {
		t.Error("Expected client to be non-nil")
	}

	if db == nil {
		t.Error("Expected database to be non-nil")
	}

	if db.Name() != dbName {
		t.Errorf("Expected database name '%s', got '%s'", dbName, db.Name())
	}
}

func TestConnectMongoDB_InvalidURI(t *testing.T) {
	config := ConnectionConfig{
		URI:            "invalid://uri",
		DatabaseName:   "test_db",
		ConnectTimeout: 5 * time.Second,
	}

	client, db, err := ConnectMongoDB(config)
	if err == nil {
		t.Error("Expected ConnectMongoDB to fail with invalid URI")
	}

	if client != nil {
		t.Error("Expected client to be nil on failure")
	}

	if db != nil {
		t.Error("Expected database to be nil on failure")
	}
}

func TestConnectMongoDB_ConnectionTimeout(t *testing.T) {
	config := ConnectionConfig{
		URI:            "mongodb://non-existent-host:27017",
		DatabaseName:   "test_db",
		ConnectTimeout: 1 * time.Second, // Short timeout
	}

	client, db, err := ConnectMongoDB(config)
	if err == nil {
		t.Error("Expected ConnectMongoDB to fail with connection timeout")
	}

	if client != nil {
		t.Error("Expected client to be nil on failure")
	}

	if db != nil {
		t.Error("Expected database to be nil on failure")
	}
}

func TestHealthCheck(t *testing.T) {
	t.Run("HealthCheck_NotInitialized", func(t *testing.T) {
		// Clean up global variables
		originalClient := MongoClient
		MongoClient = nil
		defer func() { MongoClient = originalClient }()

		err := HealthCheck()
		if err == nil {
			t.Error("Expected HealthCheck to fail when client is not initialized")
		}

		expectedMsg := "MongoDB client is not initialized"
		if err.Error() != expectedMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedMsg, err.Error())
		}
	})

	t.Run("HealthCheck_Success", func(t *testing.T) {
		// // Setup test container
		// mongoContainer := setupMongoContainer(t)
		// defer func() {
		// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
		// 		t.Errorf("Failed to terminate MongoDB container: %v", err)
		// 	}
		// }()

		// // Get connection string from container
		// connectionString, err := mongoContainer.ConnectionString(context.Background())
		// if err != nil {
		// 	t.Fatalf("Failed to get MongoDB connection string: %v", err)
		// }
		connectionString := os.Getenv("MONGO_URI")
		dbName := os.Getenv("MONGO_DB")

		config := ConnectionConfig{
			URI:            connectionString,
			DatabaseName:   dbName,
			ConnectTimeout: 10 * time.Second,
		}

		client, _, err := ConnectMongoDB(config)
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer client.Disconnect(context.Background())

		// Set global client for health check
		originalClient := MongoClient
		MongoClient = client
		defer func() { MongoClient = originalClient }()

		err = HealthCheck()
		if err != nil {
			t.Errorf("HealthCheck failed: %v", err)
		}
	})
}

func TestGetDatabase(t *testing.T) {
	originalDB := DB
	testDB := &mongo.Database{}
	DB = testDB
	defer func() { DB = originalDB }()

	result := GetDatabase()
	if result != testDB {
		t.Error("GetDatabase did not return the expected database instance")
	}
}

func TestGetClient(t *testing.T) {
	originalClient := MongoClient
	testClient := &mongo.Client{}
	MongoClient = testClient
	defer func() { MongoClient = originalClient }()

	result := GetClient()
	if result != testClient {
		t.Error("GetClient did not return the expected client instance")
	}
}

func TestDisconnectMongoDB(t *testing.T) {
	t.Run("DisconnectMongoDB_NotInitialized", func(t *testing.T) {
		// Clean up global variables
		originalClient := MongoClient
		originalDB := DB
		MongoClient = nil
		DB = nil
		defer func() {
			MongoClient = originalClient
			DB = originalDB
		}()

		err := DisconnectMongoDB()
		if err != nil {
			t.Errorf("DisconnectMongoDB should not fail when not initialized: %v", err)
		}
	})

	t.Run("DisconnectMongoDB_Success", func(t *testing.T) {
		// // Setup test container
		// mongoContainer := setupMongoContainer(t)
		// defer func() {
		// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
		// 		t.Errorf("Failed to terminate MongoDB container: %v", err)
		// 	}
		// }()

		// // Get connection string from container
		// connectionString, err := mongoContainer.ConnectionString(context.Background())
		// if err != nil {
		// 	t.Fatalf("Failed to get MongoDB connection string: %v", err)
		// }
		connectionString := os.Getenv("MONGO_URI")
		dbName := os.Getenv("MONGO_DB")

		config := ConnectionConfig{
			URI:            connectionString,
			DatabaseName:   dbName,
			ConnectTimeout: 30 * time.Second,
		}

		client, db, err := ConnectMongoDB(config)
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}

		// Set global variables
		originalClient := MongoClient
		originalDB := DB
		MongoClient = client
		DB = db
		defer func() {
			MongoClient = originalClient
			DB = originalDB
		}()

		err = DisconnectMongoDB()
		if err != nil {
			t.Errorf("DisconnectMongoDB failed: %v", err)
		}

		if MongoClient != nil {
			t.Error("MongoClient should be nil after disconnect")
		}

		if DB != nil {
			t.Error("DB should be nil after disconnect")
		}
	})
}

func TestLoadConnectionConfig(t *testing.T) {
	// Save original environment variables
	originalURI := os.Getenv("MONGO_URI")
	originalDB := os.Getenv("MONGO_DB")

	// Restore environment variables after test
	defer func() {
		os.Setenv("MONGO_URI", originalURI)
		os.Setenv("MONGO_DB", originalDB)
	}()

	tests := []struct {
		name        string
		mongoURI    string
		mongoDB     string
		shouldError bool
	}{
		{
			name:        "Missing MONGO_URI",
			mongoURI:    "",
			mongoDB:     "test_db",
			shouldError: true,
		},
		{
			name:        "Missing MONGO_DB",
			mongoURI:    "mongodb://localhost:27017",
			mongoDB:     "",
			shouldError: true,
		},
		{
			name:        "Missing both variables",
			mongoURI:    "",
			mongoDB:     "",
			shouldError: true,
		},
		{
			name:        "Both variables present",
			mongoURI:    "mongodb://localhost:27017",
			mongoDB:     "test_db",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables for this test
			os.Setenv("MONGO_URI", tt.mongoURI)
			os.Setenv("MONGO_DB", tt.mongoDB)

			config, err := loadConnectionConfig()

			if tt.shouldError {
				if err == nil {
					t.Error("Expected loadConnectionConfig to return an error")
				}
				expectedMsg := "missing MONGO_URI or MONGO_DB env variable"
				if err.Error() != expectedMsg {
					t.Errorf("Expected error message '%s', got '%s'", expectedMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				if config.URI != tt.mongoURI {
					t.Errorf("Expected URI '%s', got '%s'", tt.mongoURI, config.URI)
				}
				if config.DatabaseName != tt.mongoDB {
					t.Errorf("Expected DatabaseName '%s', got '%s'", tt.mongoDB, config.DatabaseName)
				}
				if config.ConnectTimeout != 10*time.Second {
					t.Errorf("Expected ConnectTimeout 10s, got %v", config.ConnectTimeout)
				}
			}
		})
	}
}

func TestConnectionConfig(t *testing.T) {
	config := ConnectionConfig{
		URI:            "mongodb://test:27017",
		DatabaseName:   "test_db",
		ConnectTimeout: 5 * time.Second,
	}

	if config.URI != "mongodb://test:27017" {
		t.Errorf("Expected URI 'mongodb://test:27017', got '%s'", config.URI)
	}

	if config.DatabaseName != "test_db" {
		t.Errorf("Expected DatabaseName 'test_db', got '%s'", config.DatabaseName)
	}

	if config.ConnectTimeout != 5*time.Second {
		t.Errorf("Expected ConnectTimeout 5s, got %v", config.ConnectTimeout)
	}
}

func TestMongoDBHealthCheck(t *testing.T) {
	// // Setup test container
	// mongoContainer := setupMongoContainer(t)
	// defer func() {
	// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
	// 		t.Errorf("Failed to terminate MongoDB container: %v", err)
	// 	}
	// }()

	// // Get connection string from container
	// connectionString, err := mongoContainer.ConnectionString(context.Background())
	// if err != nil {
	// 	t.Fatalf("Failed to get MongoDB connection string: %v", err)
	// }
	connectionString := os.Getenv("MONGO_URI")

	// Create direct connection for testing
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientOpts := options.Client().ApplyURI(connectionString)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		t.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)

	dbName := os.Getenv("MONGO_DB")

	t.Run("TestPingSuccess", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := client.Ping(ctx, nil)
		if err != nil {
			t.Errorf("Failed to ping MongoDB: %v", err)
		}
	})

	t.Run("TestDatabaseAccess", func(t *testing.T) {
		db := client.Database(dbName)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Test database access by listing collections
		_, err := db.ListCollectionNames(ctx, map[string]interface{}{})
		if err != nil {
			t.Skipf("Skipping database access test: no permissions: %v", err)
		}
	})

	t.Run("TestCollectionOperations", func(t *testing.T) {
		db := client.Database(dbName)
		collection := db.Collection("health_test")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Test collection creation and operations
		testDoc := map[string]interface{}{
			"health_check": true,
			"timestamp":    time.Now(),
		}

		// Insert
		_, err := collection.InsertOne(ctx, testDoc)
		if err != nil {
			t.Skipf("Skipping collection operations test: no write permissions: %v", err)
			return
		}

		// Count
		count, err := collection.CountDocuments(ctx, map[string]interface{}{})
		if err != nil {
			t.Errorf("Failed to count documents: %v", err)
		} else if count == 0 {
			t.Error("Expected at least 1 document after insert")
		}

		// Clean up
		_, err = collection.DeleteMany(ctx, map[string]interface{}{})
		if err != nil {
			t.Logf("Warning: Failed to clean up test documents: %v", err)
		}
	})
}

func TestMongoDBIndexOperations(t *testing.T) {
	// // Setup test container
	// mongoContainer := setupMongoContainer(t)
	// defer func() {
	// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
	// 		t.Errorf("Failed to terminate MongoDB container: %v", err)
	// 	}
	// }()

	// // Get connection string from container
	// connectionString, err := mongoContainer.ConnectionString(context.Background())
	// if err != nil {
	// 	t.Fatalf("Failed to get MongoDB connection string: %v", err)
	// }
	connectionString := os.Getenv("MONGO_URI")

	// Create direct connection for testing
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientOpts := options.Client().ApplyURI(connectionString)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		t.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)

	dbName := os.Getenv("MONGO_DB")
	db := client.Database(dbName)
	collection := db.Collection("test_indexes")

	t.Run("TestCreateIndex", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Create an index
		indexModel := mongo.IndexModel{
			Keys: map[string]interface{}{
				"test_field": 1,
			},
		}

		_, err := collection.Indexes().CreateOne(ctx, indexModel)
		if err != nil {
			t.Skipf("Skipping index creation test: no permissions: %v", err)
		}
	})

	t.Run("TestListIndexes", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cursor, err := collection.Indexes().List(ctx)
		if err != nil {
			t.Skipf("Skipping list indexes test: no permissions: %v", err)
			return
		}

		if cursor != nil {
			defer cursor.Close(ctx)

			var indexes []map[string]interface{}
			if err := cursor.All(ctx, &indexes); err != nil {
				t.Errorf("Failed to decode indexes: %v", err)
				return
			}

			// Should have at least the default _id index and our test index
			if len(indexes) < 2 {
				t.Errorf("Expected at least 2 indexes, got %d", len(indexes))
			}
		}
	})
}

func TestMongoDBConnectionPool(t *testing.T) {
	// // Setup test container
	// mongoContainer := setupMongoContainer(t)
	// defer func() {
	// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
	// 		t.Errorf("Failed to terminate MongoDB container: %v", err)
	// 	}
	// }()

	// // Get connection string from container
	// connectionString, err := mongoContainer.ConnectionString(context.Background())
	// if err != nil {
	// 	t.Fatalf("Failed to get MongoDB connection string: %v", err)
	// }
	connectionString := os.Getenv("MONGO_URI")

	t.Run("TestConcurrentConnections", func(t *testing.T) {
		// Create client with connection pool settings
		clientOpts := options.Client().
			ApplyURI(connectionString).
			SetMaxPoolSize(10).
			SetMinPoolSize(2)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client, err := mongo.Connect(ctx, clientOpts)
		if err != nil {
			t.Fatalf("Failed to connect to MongoDB: %v", err)
		}
		defer client.Disconnect(ctx)

		dbName := os.Getenv("MONGO_DB")
		db := client.Database(dbName)
		collection := db.Collection("test_pool")

		// Clean up the collection before the test
		_, err = collection.DeleteMany(ctx, map[string]interface{}{})
		if err != nil {
			t.Skipf("Skipping concurrent connections test: no write permissions: %v", err)
			return
		}

		// Test concurrent operations
		done := make(chan bool, 5)
		for i := 0; i < 5; i++ {
			go func(id int) {
				defer func() { done <- true }()

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				testDoc := map[string]interface{}{
					"worker_id": id,
					"timestamp": time.Now(),
				}

				_, err := collection.InsertOne(ctx, testDoc)
				if err != nil {
					t.Errorf("Worker %d failed to insert document: %v", id, err)
				}
			}(i)
		}

		// Wait for all workers to complete
		for i := 0; i < 5; i++ {
			<-done
		}

		// Verify all documents were inserted
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		count, err := collection.CountDocuments(ctx, map[string]interface{}{})
		if err != nil {
			t.Errorf("Failed to count documents: %v", err)
		} else if count != 5 {
			t.Errorf("Expected 5 documents, got %d", count)
		}
	})
}

// // setupMongoContainer creates a MongoDB test container
// func setupMongoContainer(t *testing.T) *mongodb.MongoDBContainer {
// 	ctx := context.Background()

// 	mongoContainer, err := mongodb.Run(ctx,
// 		"mongo:6",
// 		mongodb.WithUsername("test"),
// 		mongodb.WithPassword("test"),
// 	)
// 	if err != nil {
// 		t.Fatalf("Failed to start MongoDB container: %v", err)
// 	}

// 	return mongoContainer
// }

// BenchmarkMongoDBConnection benchmarks the MongoDB connection performance
func BenchmarkMongoDBConnection(b *testing.B) {
	// // Setup test container
	// mongoContainer := setupMongoContainer(&testing.T{})
	// defer func() {
	// 	if err := mongoContainer.Terminate(context.Background()); err != nil {
	// 		b.Errorf("Failed to terminate MongoDB container: %v", err)
	// 	}
	// }()

	// // Get connection string from container
	// connectionString, err := mongoContainer.ConnectionString(context.Background())
	// if err != nil {
	// 	b.Fatalf("Failed to get MongoDB connection string: %v", err)
	// }
	connectionString := os.Getenv("MONGO_URI")

	// Create client
	clientOpts := options.Client().ApplyURI(connectionString)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		b.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)

	dbName := os.Getenv("MONGO_DB")
	db := client.Database(dbName)
	collection := db.Collection("benchmark_collection")

	b.ResetTimer()

	b.Run("InsertDocument", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			testDoc := map[string]interface{}{
				"benchmark_id": i,
				"data":         "benchmark_data",
				"timestamp":    time.Now(),
			}
			_, err := collection.InsertOne(ctx, testDoc)
			cancel()
			if err != nil {
				b.Errorf("Failed to insert document: %v", err)
			}
		}
	})

	b.Run("FindDocument", func(b *testing.B) {
		// Insert a test document first
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		testDoc := map[string]interface{}{
			"benchmark_find_id": "test",
			"data":              "find_benchmark_data",
		}
		_, err := collection.InsertOne(ctx, testDoc)
		cancel()
		if err != nil {
			b.Fatalf("Failed to insert test document: %v", err)
		}

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			var result map[string]interface{}
			err := collection.FindOne(ctx, map[string]interface{}{"benchmark_find_id": "test"}).Decode(&result)
			cancel()
			if err != nil {
				b.Errorf("Failed to find document: %v", err)
			}
		}
	})

	b.Run("PingDatabase", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			err := client.Ping(ctx, nil)
			cancel()
			if err != nil {
				b.Errorf("Failed to ping database: %v", err)
			}
		}
	})
}
