package main

import (
	"context"
	"log"
	"time"

	gw "bitbucket.org/dromos/memory-os/api/grpc/gen"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	tenantID = "test-tenant"
	userID   = "test-user-sim"
	agentID  = "athena-core"
)

func main() {
	log.Println("=== Starting Athena Memory OS Simulation ===")

	// 1. Connect to gRPC server
	conn, err := grpc.NewClient("localhost:9090", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to memory server: %v", err)
	}
	defer conn.Close()
	client := gw.NewMemoryServiceClient(conn)
	ctx := context.Background()

	// 2. Connect to local MongoDB to force a "cold" chain
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://admin:admin123@localhost:27017"))
	if err != nil {
		log.Fatalf("Failed to connect to local MongoDB: %v", err)
	}
	defer mongoClient.Disconnect(ctx)

	scenarios := []func(context.Context, gw.MemoryServiceClient, *mongo.Client){
		runScenario1, runScenario2, runScenario3,
		runScenario4, runScenario5, runScenario6,
		runScenario7, runScenario8, runScenario9,
		runScenario10, runRecallScenario, runColdArchivalScenario,
		runBlobScenario, runCommunityDetectionScenario,
	}

	for i, scn := range scenarios {
		log.Printf("\n--- Executing Stage %d ---", i+1)
		scn(ctx, client, mongoClient)
		time.Sleep(1 * time.Second) // Small buffer between scenarios
	}

	log.Println("\n=== Simulation Complete ===")
	log.Println("Now wait roughly 1 minute for the Memory OS background Promoter to process these chains.")
	log.Println("Check the server logs to spot 'Successfully promoted chain to LTM Graph' and 'Archiving cold chain' messages.")
}

func createSession(ctx context.Context, c gw.MemoryServiceClient) string {
	res, err := c.CreateSession(ctx, &gw.CreateSessionRequest{
		TenantId: tenantID,
		UserId:   userID,
		AgentId:  agentID,
		Metadata: map[string]string{"source": "simulation"},
	})
	if err != nil {
		log.Fatalf("CreateSession failed: %v", err)
	}
	return res.SessionId
}

func runScenario1(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 1: Simple Human to Agent Chat")
	sid := createSession(ctx, c)
	_, err := c.StoreInteraction(ctx, &gw.StoreInteractionRequest{
		SessionId:     sid,
		UserMessage:   "Hello Athena, are you online?",
		AgentResponse: "Yes I am. How can I help you today?",
	})
	if err != nil {
		log.Fatalf("S1 failed: %v", err)
	}
}

func runScenario2(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 2: Detailed Context/Persona")
	sid := createSession(ctx, c)
	_, err := c.StoreInteraction(ctx, &gw.StoreInteractionRequest{
		SessionId:     sid,
		UserMessage:   "I am the Lead Engineer for Project 'Helios'. We are building a massively scalable real-time analytics platform using Go, Kafka, and Redis. My primary constraint is sub-millisecond latency.",
		AgentResponse: "Understood. I will remember that you lead Project Helios and require sub-millisecond latency using Go, Kafka, and Redis.",
	})
	if err != nil {
		log.Fatalf("S2 failed: %v", err)
	}
}

func runScenario3(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 3: Architecture Debate (High Density)")
	sid := createSession(ctx, c)
	_, err := c.StoreInteraction(ctx, &gw.StoreInteractionRequest{
		SessionId:     sid,
		UserMessage:   "I am debating whether to use PostgreSQL or ArangoDB for our knowledge graph. I leaning towards ArangoDB because we need native graph traversals and AQL looks powerful.",
		AgentResponse: "ArangoDB is an excellent choice for native graph traversals and multi-model flexibility. AQL simplifies complex path queries.",
	})
	if err != nil {
		log.Fatalf("S3 failed: %v", err)
	}
}

func runScenario4(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 4: System Log (Observation)")
	sid := createSession(ctx, c)
	_, err := c.StoreEvent(ctx, &gw.StoreEventRequest{
		SessionId: sid,
		Type:      "observation",
		Role:      "system",
		Content:   "Database connection pool initialized. Active workers: 4.",
	})
	if err != nil {
		log.Fatalf("S4 failed: %v", err)
	}
}

func runScenario5(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 5: System Error (Observation)")
	sid := createSession(ctx, c)
	_, err := c.StoreEvent(ctx, &gw.StoreEventRequest{
		SessionId: sid,
		Type:      "observation",
		Role:      "system",
		Content:   "FATAL ERROR: Redis cluster partitioned. Idempotency locks failing.",
		Metadata:  map[string]string{"severity": "high"},
	})
	if err != nil {
		log.Fatalf("S5 failed: %v", err)
	}
}

func runScenario6(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 6: Agent Thought")
	sid := createSession(ctx, c)
	_, err := c.StoreEvent(ctx, &gw.StoreEventRequest{
		SessionId: sid,
		Type:      "thought",
		Role:      "agent",
		Content:   "The user is asking about quantum mechanics. I don't have enough context. I will delegate this to the SearchAgent.",
	})
	if err != nil {
		log.Fatalf("S6 failed: %v", err)
	}
}

func runScenario7(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 7: Agent Action")
	sid := createSession(ctx, c)
	_, err := c.StoreEvent(ctx, &gw.StoreEventRequest{
		SessionId: sid,
		Type:      "action",
		Role:      "agent",
		Content:   "Tool Call: Trigger search_web(query='Quantum Entanglement latest research')",
	})
	if err != nil {
		log.Fatalf("S7 failed: %v", err)
	}
}

func runScenario8(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 8: Multi-Agent Debate")
	sid := createSession(ctx, c)
	c.StoreEvent(ctx, &gw.StoreEventRequest{SessionId: sid, Type: "observation", Role: "agent-researcher", Content: "Found new paper on spooky action at a distance."})
	c.StoreEvent(ctx, &gw.StoreEventRequest{SessionId: sid, Type: "thought", Role: "agent-synthesizer", Content: "This paper contradicts previous assumptions. I must note this."})
	_, err := c.StoreEvent(ctx, &gw.StoreEventRequest{SessionId: sid, Type: "message", Role: "agent-synthesizer", Content: "I reviewed what the Researcher found."})
	if err != nil {
		log.Fatalf("S8 failed: %v", err)
	}
}

func runScenario9(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 9: Mixed Flow")
	sid := createSession(ctx, c)
	c.StoreInteraction(ctx, &gw.StoreInteractionRequest{SessionId: sid, UserMessage: "Deploy the app.", AgentResponse: ""})
	c.StoreEvent(ctx, &gw.StoreEventRequest{SessionId: sid, Type: "thought", Role: "agent", Content: "I need to run docker-compose."})
	c.StoreEvent(ctx, &gw.StoreEventRequest{SessionId: sid, Type: "action", Role: "agent", Content: "Running docker-compose up"})
	c.StoreEvent(ctx, &gw.StoreEventRequest{SessionId: sid, Type: "observation", Role: "system", Content: "Containers started successfully."})
	_, err := c.StoreInteraction(ctx, &gw.StoreInteractionRequest{SessionId: sid, UserMessage: "", AgentResponse: "The app is deployed!"})
	if err != nil {
		log.Fatalf("S9 failed: %v", err)
	}
}

func runScenario10(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Scenario 10: The LTM 'Escape Hatch' (Weird Ontology)")
	sid := createSession(ctx, c)
	// Trying to force the LLM to hallucinate edges or use RELATES_TO
	_, err := c.StoreInteraction(ctx, &gw.StoreInteractionRequest{
		SessionId:     sid,
		UserMessage:   "My toaster's name is George. George believes that the moon is made of cheddar. The toaster communicates with the spaceship via telepathy.",
		AgentResponse: "George the toaster telepathically talks to the spaceship about cheddar moon. Got it.",
	})
	if err != nil {
		log.Fatalf("S10 failed: %v", err)
	}
}

func runRecallScenario(ctx context.Context, c gw.MemoryServiceClient, _ *mongo.Client) {
	log.Println("Recall Scenario: Validating SearchMemory increases Review Count/Heat")
	sid := createSession(ctx, c) // New session just to trigger the search
	_, err := c.SearchMemory(ctx, &gw.SearchMemoryRequest{
		SessionId: sid,
		Query:     "Project Helios",
		Limit:     3,
	})
	if err != nil {
		log.Fatalf("Recall test failed: %v", err)
	}
	log.Println("  Search executed. Background workers should boost recall strength.")
}

func runColdArchivalScenario(ctx context.Context, c gw.MemoryServiceClient, db *mongo.Client) {
	log.Println("Archival Scenario: Creating a cold chain and faking its timestamp")
	sid := createSession(ctx, c)
	_, err := c.StoreInteraction(ctx, &gw.StoreInteractionRequest{
		SessionId:     sid,
		UserMessage:   "What's the weather like?",
		AgentResponse: "It's sunny.",
	})
	if err != nil {
		log.Fatalf("StoreInteraction failed: %v", err)
	}

	// Fastforward its internal timestamp to 8 days ago to trigger the MTM_ARCHIVE_SCAN_DAYS
	eightDaysAgo := time.Now().Add(-8 * 24 * time.Hour)
	time.Sleep(2 * time.Second) // wait for mongo persistence
	col := db.Database("memory_os").Collection("cognitive_chains")
	_, err = col.UpdateOne(ctx, bson.M{"chainId": sid}, bson.M{
		"$set": bson.M{
			"lastEventAt":    eightDaysAgo,
			"lastAccessedAt": eightDaysAgo,
		},
	})
	if err != nil {
		log.Fatalf("Failed to manipulate mongodb chain date: %v", err)
	}
	log.Println("  Timestamp falsified to 8 days ago. It should be swept by the archiver next run.")
}

func runBlobScenario(ctx context.Context, c gw.MemoryServiceClient, mongoClient *mongo.Client) {
	sessionID := createSession(ctx, c)
	log.Printf("[Scenario 13] Blob Storage Claim Check")
	log.Printf("Session ID: %s", sessionID)

	// Create a dummy 5MB payload
	payload := make([]byte, 5*1024*1024)
	for i := range payload {
		payload[i] = 'Z'
	}

	_, err := c.StoreEvent(ctx, &gw.StoreEventRequest{
		SessionId: sessionID,
		Role:      "system",
		Type:      "observation",
		MimeType:  "application/octet-stream",
		Payload:   payload,
	})
	if err != nil {
		log.Fatalf("StoreEvent (Blob) failed: %v", err)
	}
	log.Printf("Successfully pushed 5MB blob observation for session %s", sessionID)

	// Let's force this chain to go cold so we can test Blob Deletion as well.
	collection := mongoClient.Database("memory_os").Collection("cognitive_chains")
	filter := bson.M{"chainId": sessionID}
	update := bson.M{
		"$set": bson.M{
			"lastEventAt":    time.Now().Add(-10 * 24 * time.Hour),
			"lastAccessedAt": time.Now().Add(-10 * 24 * time.Hour),
		},
	}
	_, err = collection.UpdateOne(ctx, filter, update)
	if err != nil {
		log.Fatalf("Failed to artificially age blob chain %s: %v", sessionID, err)
	}
	log.Printf("Artificially aged blob chain %s by 10 days to trigger Archival / Blob cleanup.", sessionID)

	time.Sleep(2 * time.Second)
}

func runCommunityDetectionScenario(ctx context.Context, c gw.MemoryServiceClient, mongoClient *mongo.Client) {
	log.Printf("[Scenario 14] Triggering Graph Analytics (Community Detection & Bridge Entities)")

	// We call the new TriggerGraphAnalytics endpoint
	res, err := c.TriggerGraphAnalytics(ctx, &gw.TriggerGraphAnalyticsRequest{})
	if err != nil {
		log.Fatalf("TriggerGraphAnalytics failed: %v", err)
	}

	if res.Success {
		log.Printf("Successfully triggered graph analytics: %s", res.Message)
	} else {
		log.Printf("Graph analytics trigger returned false success without error: %s", res.Message)
	}

	// We don't block and wait here since the Pregel job runs asynchronously in ArangoDB
	// The verification script will handle polling/asserting.
}
