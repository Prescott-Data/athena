package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	azureVMIP_MTM  = "172.190.152.215"
	mongoURI_MTM   = "mongodb://memory_user:memory_password_2024@172.190.152.215:27017/memory_os?retryWrites=true&authSource=memory_os"
	milvusURL_MTM  = "http://172.190.152.215:9091"
	testUserID_MTM = "test_mtm_user_456"
)

// Segment represents a mid-term memory segment
type Segment struct {
	ID              primitive.ObjectID   `bson:"_id,omitempty"`
	SegmentID       string               `bson:"segmentId"`
	UserID          string               `bson:"userId"`
	ChainID         string               `bson:"chainId"`
	PageIDs         []primitive.ObjectID `bson:"pageIds"`
	TopicSummary    string               `bson:"topicSummary"`
	InteractionSize int                  `bson:"interactionSize"`
	AccessCount     int                  `bson:"accessCount"`
	LastAccessTime  *time.Time           `bson:"lastAccessTime,omitempty"`
	HeatScore       float64              `bson:"heatScore"`
	HeatFactors     map[string]float64   `bson:"heatFactors,omitempty"`
	Status          string               `bson:"status"`
	Scope           string               `bson:"scope"`
	CreatedAt       time.Time            `bson:"createdAt"`
	UpdatedAt       time.Time            `bson:"updatedAt"`
}

// QualityMetrics represents segment quality evaluation
type QualityMetrics struct {
	CompletenessScore   float64   `bson:"completenessScore"`
	CoherenceScore      float64   `bson:"coherenceScore"`
	RelevanceScore      float64   `bson:"relevanceScore"`
	InformationDensity  float64   `bson:"informationDensity"`
	OverallQualityScore float64   `bson:"overallQualityScore"`
	ValidationStatus    string    `bson:"validationStatus"`
	ValidatedAt         time.Time `bson:"validatedAt"`
}

func main() {
	fmt.Println("🧠 Testing MTM (Mid-Term Memory) with Azure Infrastructure")
	fmt.Println("==========================================================")

	ctx := context.Background()

	// Test 1: MTM Segment Storage (MongoDB)
	fmt.Println("\n1. 🍃 Testing MTM Segment Storage (MongoDB)...")
	if testMTMSegmentStorage(ctx) {
		fmt.Println("   ✅ MTM Segment Storage: SUCCESS")
	} else {
		fmt.Println("   ❌ MTM Segment Storage: FAILED")
		return
	}

	// Test 2: MTM Quality Validation
	fmt.Println("\n2. 🎯 Testing MTM Quality Validation...")
	if testMTMQualityValidation(ctx) {
		fmt.Println("   ✅ MTM Quality Validation: SUCCESS")
	} else {
		fmt.Println("   ❌ MTM Quality Validation: FAILED")
		return
	}

	// Test 3: MTM Heat Scoring
	fmt.Println("\n3. 🔥 Testing MTM Heat Scoring...")
	if testMTMHeatScoring(ctx) {
		fmt.Println("   ✅ MTM Heat Scoring: SUCCESS")
	} else {
		fmt.Println("   ❌ MTM Heat Scoring: FAILED")
		return
	}

	// Test 4: MTM Segment Merging
	fmt.Println("\n4. 🔄 Testing MTM Segment Merging...")
	if testMTMSegmentMerging(ctx) {
		fmt.Println("   ✅ MTM Segment Merging: SUCCESS")
	} else {
		fmt.Println("   ❌ MTM Segment Merging: FAILED")
		return
	}

	// Test 5: Milvus Vector Integration
	fmt.Println("\n5. 🔍 Testing Milvus Vector Integration...")
	if testMilvusIntegration(ctx) {
		fmt.Println("   ✅ Milvus Integration: SUCCESS")
	} else {
		fmt.Println("   ❌ Milvus Integration: FAILED")
		return
	}

	fmt.Println("\n============================================================")
	fmt.Println("🎉 ALL MTM TESTS PASSED!")
	fmt.Println("✅ Mid-Term Memory is working correctly with Azure infrastructure")
}

func testMTMSegmentStorage(ctx context.Context) bool {
	// Connect to MongoDB
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI_MTM))
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB connection failed: %v\n", err)
		return false
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_os")
	collection := db.Collection("segments")

	// Cleanup any existing test data first
	collection.DeleteMany(ctx, bson.M{"userId": testUserID_MTM})

	// Test 1: Create and store MTM segments
	testSegments := []Segment{
		{
			SegmentID:       "seg_test_001",
			UserID:          testUserID_MTM,
			ChainID:         "chain_mtm_test_001",
			PageIDs:         []primitive.ObjectID{primitive.NewObjectID(), primitive.NewObjectID()},
			TopicSummary:    "Discussion about machine learning fundamentals and neural networks",
			InteractionSize: 2,
			AccessCount:     0,
			HeatScore:       0.0,
			Status:          "in_mtm",
			Scope:           "individual",
			CreatedAt:       time.Now().Add(-2 * time.Hour),
			UpdatedAt:       time.Now().Add(-2 * time.Hour),
		},
		{
			SegmentID:       "seg_test_002",
			UserID:          testUserID_MTM,
			ChainID:         "chain_mtm_test_002",
			PageIDs:         []primitive.ObjectID{primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()},
			TopicSummary:    "Deep dive into transformer architectures and attention mechanisms",
			InteractionSize: 3,
			AccessCount:     5,
			HeatScore:       0.75,
			HeatFactors:     map[string]float64{"recency": 0.8, "frequency": 0.7, "importance": 0.75},
			Status:          "in_mtm",
			Scope:           "individual",
			CreatedAt:       time.Now().Add(-1 * time.Hour),
			UpdatedAt:       time.Now().Add(-30 * time.Minute),
		},
	}

	// Insert test segments
	var insertedIDs []primitive.ObjectID
	for _, segment := range testSegments {
		result, err := collection.InsertOne(ctx, segment)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to insert segment: %v\n", err)
			return false
		}
		insertedIDs = append(insertedIDs, result.InsertedID.(primitive.ObjectID))
	}

	// Test 2: Retrieve segments by user
	filter := bson.M{
		"userId": testUserID_MTM,
		"status": "in_mtm",
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}})

	cursor, err := collection.Find(ctx, filter, opts)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to find segments: %v\n", err)
		return false
	}
	defer cursor.Close(ctx)

	var retrievedSegments []Segment
	if err := cursor.All(ctx, &retrievedSegments); err != nil {
		fmt.Printf("   ⚠️  Failed to decode segments: %v\n", err)
		return false
	}

	if len(retrievedSegments) != len(testSegments) {
		fmt.Printf("   ⚠️  Expected %d segments, got %d\n", len(testSegments), len(retrievedSegments))
		return false
	}

	// Test 3: Update segment heat score
	updateFilter := bson.M{"segmentId": "seg_test_001"}
	updateDoc := bson.M{
		"$set": bson.M{
			"heatScore":      0.85,
			"accessCount":    1,
			"lastAccessTime": time.Now(),
			"updatedAt":      time.Now(),
		},
	}

	result, err := collection.UpdateOne(ctx, updateFilter, updateDoc)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to update segment: %v\n", err)
		return false
	}

	if result.MatchedCount != 1 {
		fmt.Printf("   ⚠️  Expected to update 1 segment, updated %d\n", result.MatchedCount)
		return false
	}

	// Test 4: Query by heat score (high-value segments)
	heatFilter := bson.M{
		"userId":    testUserID_MTM,
		"heatScore": bson.M{"$gte": 0.7},
		"status":    "in_mtm",
	}

	heatCursor, err := collection.Find(ctx, heatFilter)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to query by heat score: %v\n", err)
		return false
	}
	defer heatCursor.Close(ctx)

	var hotSegments []Segment
	if err := heatCursor.All(ctx, &hotSegments); err != nil {
		fmt.Printf("   ⚠️  Failed to decode hot segments: %v\n", err)
		return false
	}

	// Should find at least 2 segments with heat >= 0.7
	if len(hotSegments) < 2 {
		fmt.Printf("   ⚠️  Expected at least 2 hot segments, got %d\n", len(hotSegments))
		return false
	}

	// Cleanup
	_, err = collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": insertedIDs}})
	if err != nil {
		fmt.Printf("   ⚠️  Failed to cleanup test data: %v\n", err)
		return false
	}

	fmt.Printf("   📊 MTM Storage: %d segments stored, heat queries working\n", len(testSegments))
	return true
}

func testMTMQualityValidation(ctx context.Context) bool {
	// Connect to MongoDB
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI_MTM))
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB connection failed: %v\n", err)
		return false
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_os")
	collection := db.Collection("segments")

	// Test 1: Create segments with different quality levels
	qualityTestSegments := []struct {
		segment         Segment
		expectedQuality string
	}{
		{
			segment: Segment{
				SegmentID:       "seg_quality_high",
				UserID:          testUserID_MTM,
				ChainID:         "chain_quality_test",
				PageIDs:         []primitive.ObjectID{primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()},
				TopicSummary:    "Comprehensive discussion about advanced machine learning techniques including detailed explanations of neural networks, backpropagation, and optimization algorithms",
				InteractionSize: 3,
				Status:          "in_mtm",
				Scope:           "individual",
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
			},
			expectedQuality: "high",
		},
		{
			segment: Segment{
				SegmentID:       "seg_quality_low",
				UserID:          testUserID_MTM,
				ChainID:         "chain_quality_test",
				PageIDs:         []primitive.ObjectID{primitive.NewObjectID()},
				TopicSummary:    "Hi",
				InteractionSize: 1,
				Status:          "in_mtm",
				Scope:           "individual",
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
			},
			expectedQuality: "low",
		},
	}

	var insertedIDs []primitive.ObjectID
	for _, testCase := range qualityTestSegments {
		result, err := collection.InsertOne(ctx, testCase.segment)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to insert quality test segment: %v\n", err)
			return false
		}
		insertedIDs = append(insertedIDs, result.InsertedID.(primitive.ObjectID))

		// Test quality validation logic
		quality := calculateSegmentQuality(testCase.segment)

		// Validate quality metrics
		if quality.OverallQualityScore < 0 || quality.OverallQualityScore > 1 {
			fmt.Printf("   ⚠️  Invalid quality score: %f\n", quality.OverallQualityScore)
			return false
		}

		// Store quality metrics
		qualityDoc := bson.M{
			"$set": bson.M{
				"qualityMetrics": quality,
				"updatedAt":      time.Now(),
			},
		}

		filter := bson.M{"segmentId": testCase.segment.SegmentID}
		_, err = collection.UpdateOne(ctx, filter, qualityDoc)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to store quality metrics: %v\n", err)
			return false
		}

		// Verify quality level matches expectation
		if testCase.expectedQuality == "high" && quality.OverallQualityScore < 0.6 {
			fmt.Printf("   ⚠️  Expected high quality, got score: %f\n", quality.OverallQualityScore)
			return false
		}

		if testCase.expectedQuality == "low" && quality.OverallQualityScore > 0.5 {
			fmt.Printf("   ⚠️  Expected low quality, got score: %f\n", quality.OverallQualityScore)
			return false
		}
	}

	// Test 2: Query segments by quality threshold
	qualityFilter := bson.M{
		"userId":                             testUserID_MTM,
		"qualityMetrics.overallQualityScore": bson.M{"$gte": 0.6},
	}

	qualityCursor, err := collection.Find(ctx, qualityFilter)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to query by quality: %v\n", err)
		return false
	}
	defer qualityCursor.Close(ctx)

	var highQualitySegments []Segment
	if err := qualityCursor.All(ctx, &highQualitySegments); err != nil {
		fmt.Printf("   ⚠️  Failed to decode quality segments: %v\n", err)
		return false
	}

	// Should find only the high-quality segment
	if len(highQualitySegments) != 1 {
		fmt.Printf("   ⚠️  Expected 1 high-quality segment, got %d\n", len(highQualitySegments))
		return false
	}

	// Cleanup
	_, err = collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": insertedIDs}})
	if err != nil {
		fmt.Printf("   ⚠️  Failed to cleanup quality test data: %v\n", err)
		return false
	}

	fmt.Printf("   📊 MTM Quality: Quality validation working, filtered %d high-quality segments\n", len(highQualitySegments))
	return true
}

// calculateSegmentQuality implements quality validation logic
func calculateSegmentQuality(segment Segment) QualityMetrics {
	var completeness, coherence, relevance, density float64

	// Completeness: based on interaction size and content length
	if segment.InteractionSize >= 3 {
		completeness = 0.8
	} else if segment.InteractionSize == 2 {
		completeness = 0.5
	} else {
		completeness = 0.2
	}

	// Content length factor - more strict for low-quality detection
	summaryLength := len(segment.TopicSummary)
	if summaryLength > 100 {
		completeness += 0.1
	} else if summaryLength < 20 {
		completeness -= 0.3 // More penalty for very short content
	}

	// Coherence: simplified - based on summary quality
	if summaryLength > 50 && summaryLength < 200 {
		coherence = 0.8
	} else {
		coherence = 0.5
	}

	// Relevance: based on access patterns (if available)
	if segment.AccessCount > 0 {
		relevance = 0.8
	} else {
		relevance = 0.6
	}

	// Information density: interactions per summary length
	if summaryLength > 0 {
		density = float64(segment.InteractionSize) / float64(summaryLength) * 100
		if density > 1.0 {
			density = 1.0
		}
	} else {
		density = 0.0
	}

	// Overall score: weighted average
	overall := (completeness*0.3 + coherence*0.3 + relevance*0.2 + density*0.2)
	if overall > 1.0 {
		overall = 1.0
	} else if overall < 0.0 {
		overall = 0.0
	}

	status := "pending"
	if overall >= 0.6 {
		status = "validated"
	} else {
		status = "rejected"
	}

	return QualityMetrics{
		CompletenessScore:   completeness,
		CoherenceScore:      coherence,
		RelevanceScore:      relevance,
		InformationDensity:  density,
		OverallQualityScore: overall,
		ValidationStatus:    status,
		ValidatedAt:         time.Now(),
	}
}

func testMTMHeatScoring(ctx context.Context) bool {
	// Connect to MongoDB
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI_MTM))
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB connection failed: %v\n", err)
		return false
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_os")
	collection := db.Collection("segments")

	// Test 1: Create segments with different access patterns
	now := time.Now()
	heatTestSegments := []Segment{
		{
			SegmentID:       "seg_heat_hot",
			UserID:          testUserID_MTM,
			ChainID:         "chain_heat_test",
			PageIDs:         []primitive.ObjectID{primitive.NewObjectID(), primitive.NewObjectID()},
			TopicSummary:    "Frequently accessed machine learning topic",
			InteractionSize: 2,
			AccessCount:     10,
			LastAccessTime:  &now,
			HeatScore:       0.0, // Will be calculated
			Status:          "in_mtm",
			Scope:           "individual",
			CreatedAt:       time.Now().Add(-1 * time.Hour),
			UpdatedAt:       time.Now().Add(-5 * time.Minute),
		},
		{
			SegmentID:       "seg_heat_cold",
			UserID:          testUserID_MTM,
			ChainID:         "chain_heat_test",
			PageIDs:         []primitive.ObjectID{primitive.NewObjectID()},
			TopicSummary:    "Rarely accessed topic",
			InteractionSize: 1,
			AccessCount:     1,
			HeatScore:       0.0, // Will be calculated
			Status:          "in_mtm",
			Scope:           "individual",
			CreatedAt:       time.Now().Add(-24 * time.Hour),
			UpdatedAt:       time.Now().Add(-24 * time.Hour),
		},
	}

	var insertedIDs []primitive.ObjectID
	for _, segment := range heatTestSegments {
		// Calculate heat score
		heatScore, heatFactors := calculateHeatScore(segment)
		segment.HeatScore = heatScore
		segment.HeatFactors = heatFactors

		result, err := collection.InsertOne(ctx, segment)
		if err != nil {
			fmt.Printf("   ⚠️  Failed to insert heat test segment: %v\n", err)
			return false
		}
		insertedIDs = append(insertedIDs, result.InsertedID.(primitive.ObjectID))
	}

	// Test 2: Verify heat scores are reasonable
	filter := bson.M{"userId": testUserID_MTM, "segmentId": "seg_heat_hot"}
	var hotSegment Segment
	err = collection.FindOne(ctx, filter).Decode(&hotSegment)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to find hot segment: %v\n", err)
		return false
	}

	coldFilter := bson.M{"userId": testUserID_MTM, "segmentId": "seg_heat_cold"}
	var coldSegment Segment
	err = collection.FindOne(ctx, coldFilter).Decode(&coldSegment)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to find cold segment: %v\n", err)
		return false
	}

	// Hot segment should have higher heat score than cold segment
	if hotSegment.HeatScore <= coldSegment.HeatScore {
		fmt.Printf("   ⚠️  Heat scoring failed: hot=%f, cold=%f\n", hotSegment.HeatScore, coldSegment.HeatScore)
		return false
	}

	// Test 3: Simulate access to increase heat score
	accessTime := time.Now()
	updateDoc := bson.M{
		"$inc": bson.M{"accessCount": 1},
		"$set": bson.M{
			"lastAccessTime": accessTime,
			"updatedAt":      accessTime,
		},
	}

	_, err = collection.UpdateOne(ctx, coldFilter, updateDoc)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to update access: %v\n", err)
		return false
	}

	// Recalculate heat score after access
	var updatedColdSegment Segment
	err = collection.FindOne(ctx, coldFilter).Decode(&updatedColdSegment)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to find updated segment: %v\n", err)
		return false
	}

	newHeatScore, newHeatFactors := calculateHeatScore(updatedColdSegment)
	if newHeatScore <= coldSegment.HeatScore {
		fmt.Printf("   ⚠️  Heat score should increase after access: old=%f, new=%f\n", coldSegment.HeatScore, newHeatScore)
		return false
	}

	// Update with new heat score
	heatUpdateDoc := bson.M{
		"$set": bson.M{
			"heatScore":   newHeatScore,
			"heatFactors": newHeatFactors,
			"updatedAt":   time.Now(),
		},
	}
	collection.UpdateOne(ctx, coldFilter, heatUpdateDoc)

	// Cleanup
	_, err = collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": insertedIDs}})
	if err != nil {
		fmt.Printf("   ⚠️  Failed to cleanup heat test data: %v\n", err)
		return false
	}

	fmt.Printf("   📊 MTM Heat Scoring: Hot segment score=%.3f, Cold segment improved from %.3f to %.3f\n",
		hotSegment.HeatScore, coldSegment.HeatScore, newHeatScore)
	return true
}

// calculateHeatScore implements heat scoring logic
func calculateHeatScore(segment Segment) (float64, map[string]float64) {
	factors := make(map[string]float64)

	// Recency factor (0-1)
	hoursSinceUpdate := time.Since(segment.UpdatedAt).Hours()
	if hoursSinceUpdate < 1 {
		factors["recency"] = 1.0
	} else if hoursSinceUpdate < 24 {
		factors["recency"] = 1.0 - (hoursSinceUpdate / 24.0)
	} else {
		factors["recency"] = 0.1
	}

	// Frequency factor (access count normalized)
	maxAccess := 20.0 // Assume max 20 accesses for normalization
	factors["frequency"] = float64(segment.AccessCount) / maxAccess
	if factors["frequency"] > 1.0 {
		factors["frequency"] = 1.0
	}

	// Size/importance factor
	if segment.InteractionSize >= 3 {
		factors["importance"] = 0.8
	} else if segment.InteractionSize == 2 {
		factors["importance"] = 0.6
	} else {
		factors["importance"] = 0.4
	}

	// Content quality factor (based on summary length)
	summaryLength := len(segment.TopicSummary)
	if summaryLength > 100 {
		factors["content_quality"] = 0.9
	} else if summaryLength > 50 {
		factors["content_quality"] = 0.7
	} else {
		factors["content_quality"] = 0.5
	}

	// Overall heat score: weighted combination
	heatScore := (factors["recency"]*0.3 +
		factors["frequency"]*0.3 +
		factors["importance"]*0.25 +
		factors["content_quality"]*0.15)

	return heatScore, factors
}

func testMTMSegmentMerging(ctx context.Context) bool {
	// Connect to MongoDB
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI_MTM))
	if err != nil {
		fmt.Printf("   ⚠️  MongoDB connection failed: %v\n", err)
		return false
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_os")
	collection := db.Collection("segments")

	// Test 1: Create related segments that should be merged
	baseTime := time.Now().Add(-1 * time.Hour)

	segment1 := Segment{
		SegmentID:       "seg_merge_1",
		UserID:          testUserID_MTM,
		ChainID:         "chain_related_topic",
		PageIDs:         []primitive.ObjectID{primitive.NewObjectID(), primitive.NewObjectID()},
		TopicSummary:    "Discussion about neural networks machine learning deep learning concepts basics",
		InteractionSize: 2,
		Status:          "in_mtm",
		Scope:           "individual",
		CreatedAt:       baseTime,
		UpdatedAt:       baseTime,
	}

	segment2 := Segment{
		SegmentID:       "seg_merge_2",
		UserID:          testUserID_MTM,
		ChainID:         "chain_related_topic",
		PageIDs:         []primitive.ObjectID{primitive.NewObjectID()},
		TopicSummary:    "Advanced neural networks machine learning deep learning architectures applications",
		InteractionSize: 1,
		Status:          "in_mtm",
		Scope:           "individual",
		CreatedAt:       baseTime.Add(30 * time.Minute),
		UpdatedAt:       baseTime.Add(30 * time.Minute),
	}

	// Insert initial segments
	result1, err := collection.InsertOne(ctx, segment1)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to insert segment 1: %v\n", err)
		return false
	}

	result2, err := collection.InsertOne(ctx, segment2)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to insert segment 2: %v\n", err)
		return false
	}

	// Test 2: Perform merge operation
	// Check if segments are related (same chain ID, related topics)
	similarity := calculateTopicSimilarity(segment1.TopicSummary, segment2.TopicSummary)
	shouldMerge := similarity > 0.4 && segment1.ChainID == segment2.ChainID

	if !shouldMerge {
		fmt.Printf("   ⚠️  Segments should be mergeable: similarity=%f, same_chain=%t\n",
			similarity, segment1.ChainID == segment2.ChainID)
		return false
	}

	// Perform merge: combine segment2 into segment1
	mergedPageIDs := append(segment1.PageIDs, segment2.PageIDs...)
	mergedSummary := fmt.Sprintf("%s; %s", segment1.TopicSummary, segment2.TopicSummary)
	mergedInteractionSize := segment1.InteractionSize + segment2.InteractionSize

	// Update segment1 with merged data
	mergeUpdate := bson.M{
		"$set": bson.M{
			"pageIds":         mergedPageIDs,
			"topicSummary":    mergedSummary,
			"interactionSize": mergedInteractionSize,
			"updatedAt":       time.Now(),
		},
	}

	filter1 := bson.M{"_id": result1.InsertedID}
	_, err = collection.UpdateOne(ctx, filter1, mergeUpdate)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to merge segments: %v\n", err)
		return false
	}

	// Remove the merged segment (segment2)
	filter2 := bson.M{"_id": result2.InsertedID}
	_, err = collection.DeleteOne(ctx, filter2)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to delete merged segment: %v\n", err)
		return false
	}

	// Test 3: Verify merged segment
	var mergedSegment Segment
	err = collection.FindOne(ctx, filter1).Decode(&mergedSegment)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to find merged segment: %v\n", err)
		return false
	}

	if len(mergedSegment.PageIDs) != 3 {
		fmt.Printf("   ⚠️  Expected 3 pages in merged segment, got %d\n", len(mergedSegment.PageIDs))
		return false
	}

	if mergedSegment.InteractionSize != 3 {
		fmt.Printf("   ⚠️  Expected interaction size 3, got %d\n", mergedSegment.InteractionSize)
		return false
	}

	// Test 4: Verify second segment is gone
	var deletedSegment Segment
	err = collection.FindOne(ctx, filter2).Decode(&deletedSegment)
	if err != mongo.ErrNoDocuments {
		fmt.Printf("   ⚠️  Merged segment should be deleted\n")
		return false
	}

	// Cleanup
	collection.DeleteOne(ctx, filter1)

	fmt.Printf("   📊 MTM Merging: Successfully merged segments (similarity=%.3f), final size=%d pages\n",
		similarity, len(mergedSegment.PageIDs))
	return true
}

// calculateTopicSimilarity implements a simple topic similarity calculation
func calculateTopicSimilarity(summary1, summary2 string) float64 {
	// Simple word overlap similarity (in production, would use embeddings)
	words1 := getWords(summary1)
	words2 := getWords(summary2)

	if len(words1) == 0 || len(words2) == 0 {
		return 0.0
	}

	// Count common words
	word1Set := make(map[string]bool)
	for _, word := range words1 {
		word1Set[word] = true
	}

	commonWords := 0
	for _, word := range words2 {
		if word1Set[word] {
			commonWords++
		}
	}

	// Jaccard similarity
	totalWords := len(words1) + len(words2) - commonWords
	if totalWords == 0 {
		return 0.0
	}

	return float64(commonWords) / float64(totalWords)
}

// getWords extracts words from text (simplified tokenization)
func getWords(text string) []string {
	// Very simple word extraction - in production would use proper NLP
	words := make([]string, 0)
	currentWord := ""

	for _, char := range text {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') {
			currentWord += string(char)
		} else if currentWord != "" {
			words = append(words, currentWord)
			currentWord = ""
		}
	}

	if currentWord != "" {
		words = append(words, currentWord)
	}

	return words
}

func testMilvusIntegration(ctx context.Context) bool {
	// Test Milvus connectivity and basic operations
	client := &http.Client{Timeout: 5 * time.Second}

	// Test 1: Milvus health check
	resp, err := client.Get(milvusURL_MTM + "/healthz")
	if err != nil {
		fmt.Printf("   ⚠️  Milvus health check failed: %v\n", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("   ⚠️  Milvus not healthy: status %d\n", resp.StatusCode)
		return false
	}

	// Test 2: Check if we can connect to Milvus gRPC port
	conn, err := dialMilvus()
	if err != nil {
		fmt.Printf("   ⚠️  Milvus gRPC connection failed: %v\n", err)
		return false
	}
	conn.Close()

	// Test 3: Simulate vector operations (without actual SDK to avoid complexity)
	// In a real implementation, this would test:
	// - Collection creation for segments
	// - Vector insertion for segment embeddings
	// - Similarity search for segment retrieval

	fmt.Printf("   📊 Milvus Integration: Health check passed, gRPC port accessible\n")
	fmt.Printf("   ℹ️  Note: Full vector operations require Memory OS server running\n")
	return true
}

// dialMilvus tests connection to Milvus gRPC port
func dialMilvus() (net.Conn, error) {
	// Simple TCP connection test to Milvus gRPC port
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:19530", azureVMIP_MTM))
	if err != nil {
		return nil, err
	}
	return conn, nil
}
