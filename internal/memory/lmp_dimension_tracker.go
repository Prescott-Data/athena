package memory

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DimensionTracker monitors personality dimension changes and trends over time
type DimensionTracker struct {
	db     *mongo.Database
	config PersonalityAnalysisConfig
}

// DimensionEvolution represents how a dimension has changed over time
type DimensionEvolution struct {
	UserID        string                `json:"user_id"`
	DimensionName string                `json:"dimension_name"`
	DimensionType DimensionType         `json:"dimension_type"`
	History       []DimensionSnapshot   `json:"history"`
	CurrentScore  *DimensionScore       `json:"current_score"`
	Trend         string                `json:"trend"` // "increasing", "decreasing", "stable", "volatile"
	TrendStrength float64               `json:"trend_strength"` // 0.0-1.0
	FirstObserved time.Time             `json:"first_observed"`
	LastObserved  time.Time             `json:"last_observed"`
	Observations  int                   `json:"observations"`
}

// DimensionSnapshot represents a point-in-time snapshot of a dimension
type DimensionSnapshot struct {
	Timestamp     time.Time       `json:"timestamp" bson:"timestamp"`
	Score         *DimensionScore `json:"score" bson:"score"`
	Context       string          `json:"context" bson:"context"`
	ChangeReason  string          `json:"change_reason" bson:"change_reason"`
	ProfileVersion int            `json:"profile_version" bson:"profile_version"`
}

// DimensionAnalytics provides aggregated analytics across users and dimensions
type DimensionAnalytics struct {
	TimeRange            string                        `json:"time_range"`
	TotalUsers           int                           `json:"total_users"`
	TotalDimensions      int                           `json:"total_dimensions"`
	TopDimensions        []DimensionPopularity         `json:"top_dimensions"`
	DimensionDistribution map[DimensionType]int        `json:"dimension_distribution"`
	ConfidenceTrends     []ConfidenceTrend            `json:"confidence_trends"`
	UserEngagementTrends []UserEngagementTrend        `json:"user_engagement_trends"`
	GeneratedAt          time.Time                     `json:"generated_at"`
}

// DimensionPopularity represents how popular a dimension is across users
type DimensionPopularity struct {
	DimensionName string        `json:"dimension_name"`
	DimensionType DimensionType `json:"dimension_type"`
	UserCount     int           `json:"user_count"`
	AvgConfidence float64       `json:"avg_confidence"`
	TrendDirection string       `json:"trend_direction"`
}

// ConfidenceTrend represents confidence trends over time
type ConfidenceTrend struct {
	Date          time.Time `json:"date"`
	AvgConfidence float64   `json:"avg_confidence"`
	TotalUpdates  int       `json:"total_updates"`
}

// UserEngagementTrend represents user engagement with personality analysis
type UserEngagementTrend struct {
	Date               time.Time `json:"date"`
	ActiveUsers        int       `json:"active_users"`
	NewDimensions      int       `json:"new_dimensions"`
	DimensionUpdates   int       `json:"dimension_updates"`
	AvgDimensionsPerUser float64 `json:"avg_dimensions_per_user"`
}

// NewDimensionTracker creates a new dimension tracker
func NewDimensionTracker(db *mongo.Database) *DimensionTracker {
	return &DimensionTracker{
		db:     db,
		config: GetDefaultPersonalityAnalysisConfig(),
	}
}

// TrackDimensionChange records a dimension change for historical analysis
func (dt *DimensionTracker) TrackDimensionChange(ctx context.Context, userID, dimensionName string, dimensionType DimensionType, 
	oldScore, newScore *DimensionScore, changeReason string, profileVersion int) error {
	
	col := dt.db.Collection("dimension_history")
	
	snapshot := DimensionSnapshot{
		Timestamp:      time.Now(),
		Score:          newScore,
		Context:        fmt.Sprintf("Change from %s to %s", dt.getScoreString(oldScore), dt.getScoreString(newScore)),
		ChangeReason:   changeReason,
		ProfileVersion: profileVersion,
	}
	
	// Create or update dimension evolution record
	filter := bson.M{
		"user_id":        userID,
		"dimension_name": dimensionName,
		"dimension_type": dimensionType,
	}
	
	update := bson.M{
		"$push": bson.M{
			"history": snapshot,
		},
		"$set": bson.M{
			"current_score":  newScore,
			"last_observed":  time.Now(),
			"updated_at":     time.Now(),
		},
		"$inc": bson.M{
			"observations": 1,
		},
		"$setOnInsert": bson.M{
			"user_id":        userID,
			"dimension_name": dimensionName,
			"dimension_type": dimensionType,
			"first_observed": time.Now(),
			"created_at":     time.Now(),
		},
	}
	
	opts := options.Update().SetUpsert(true)
	_, err := col.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("failed to track dimension change: %w", err)
	}
	
	log.Printf("INFO: Tracked dimension change for user %s: %s (%s) - %s", 
		userID, dimensionName, dimensionType, changeReason)
	
	return nil
}

// GetDimensionEvolution retrieves the evolution history for a specific dimension
func (dt *DimensionTracker) GetDimensionEvolution(ctx context.Context, userID, dimensionName string, dimensionType DimensionType) (*DimensionEvolution, error) {
	col := dt.db.Collection("dimension_history")
	
	filter := bson.M{
		"user_id":        userID,
		"dimension_name": dimensionName,
		"dimension_type": dimensionType,
	}
	
	var evolution DimensionEvolution
	err := col.FindOne(ctx, filter).Decode(&evolution)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil // No evolution found
		}
		return nil, fmt.Errorf("failed to get dimension evolution: %w", err)
	}
	
	// Calculate trend
	evolution.Trend, evolution.TrendStrength = dt.calculateTrend(evolution.History)
	
	return &evolution, nil
}

// GetUserDimensionEvolutions retrieves all dimension evolutions for a user
func (dt *DimensionTracker) GetUserDimensionEvolutions(ctx context.Context, userID string) ([]DimensionEvolution, error) {
	col := dt.db.Collection("dimension_history")
	
	filter := bson.M{"user_id": userID}
	opts := options.Find().SetSort(bson.M{"last_observed": -1})
	
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to query dimension evolutions: %w", err)
	}
	defer cursor.Close(ctx)
	
	var evolutions []DimensionEvolution
	for cursor.Next(ctx) {
		var evolution DimensionEvolution
		if err := cursor.Decode(&evolution); err != nil {
			log.Printf("WARN: Failed to decode dimension evolution: %v", err)
			continue
		}
		
		// Calculate trend for each evolution
		evolution.Trend, evolution.TrendStrength = dt.calculateTrend(evolution.History)
		evolutions = append(evolutions, evolution)
	}
	
	return evolutions, nil
}

// AnalyzeDimensionTrends provides comprehensive analytics across all users and dimensions
func (dt *DimensionTracker) AnalyzeDimensionTrends(ctx context.Context, timeRange string) (*DimensionAnalytics, error) {
	// Determine time range for analysis
	startTime, err := dt.parseTimeRange(timeRange)
	if err != nil {
		return nil, fmt.Errorf("invalid time range: %w", err)
	}
	
	analytics := &DimensionAnalytics{
		TimeRange:            timeRange,
		DimensionDistribution: make(map[DimensionType]int),
		GeneratedAt:          time.Now(),
	}
	
	// Get basic statistics
	if err := dt.calculateBasicStatistics(ctx, analytics, startTime); err != nil {
		return nil, fmt.Errorf("failed to calculate basic statistics: %w", err)
	}
	
	// Get top dimensions
	if err := dt.calculateTopDimensions(ctx, analytics, startTime); err != nil {
		return nil, fmt.Errorf("failed to calculate top dimensions: %w", err)
	}
	
	// Get confidence trends
	if err := dt.calculateConfidenceTrends(ctx, analytics, startTime); err != nil {
		return nil, fmt.Errorf("failed to calculate confidence trends: %w", err)
	}
	
	// Get user engagement trends
	if err := dt.calculateUserEngagementTrends(ctx, analytics, startTime); err != nil {
		return nil, fmt.Errorf("failed to calculate user engagement trends: %w", err)
	}
	
	log.Printf("INFO: Generated dimension analytics for %s - Users: %d, Dimensions: %d", 
		timeRange, analytics.TotalUsers, analytics.TotalDimensions)
	
	return analytics, nil
}

// calculateTrend analyzes the trend in dimension history
func (dt *DimensionTracker) calculateTrend(history []DimensionSnapshot) (string, float64) {
	if len(history) < 2 {
		return "insufficient_data", 0.0
	}
	
	// Sort history by timestamp
	sort.Slice(history, func(i, j int) bool {
		return history[i].Timestamp.Before(history[j].Timestamp)
	})
	
	// Calculate confidence trend
	confidenceChanges := 0.0
	levelChanges := 0
	
	for i := 1; i < len(history); i++ {
		prev := history[i-1].Score
		curr := history[i].Score
		
		if prev != nil && curr != nil {
			confidenceChanges += curr.Confidence - prev.Confidence
			
			// Track level changes
			if curr.Level != prev.Level {
				levelChanges++
			}
		}
	}
	
	// Determine trend direction
	trend := "stable"
	strength := 0.0
	
	if len(history) > 0 {
		strength = abs(confidenceChanges) / float64(len(history)-1)
		
		if confidenceChanges > 0.1 {
			trend = "increasing"
		} else if confidenceChanges < -0.1 {
			trend = "decreasing"
		} else if levelChanges > len(history)/3 {
			trend = "volatile"
		}
	}
	
	return trend, strength
}

// calculateBasicStatistics calculates basic statistics for analytics
func (dt *DimensionTracker) calculateBasicStatistics(ctx context.Context, analytics *DimensionAnalytics, startTime time.Time) error {
	col := dt.db.Collection("dimension_history")
	
	// Count total users
	userPipeline := []bson.M{
		{"$match": bson.M{"last_observed": bson.M{"$gte": startTime}}},
		{"$group": bson.M{"_id": "$user_id"}},
		{"$count": "total"},
	}
	
	userCursor, err := col.Aggregate(ctx, userPipeline)
	if err != nil {
		return err
	}
	defer userCursor.Close(ctx)
	
	if userCursor.Next(ctx) {
		var result struct {
			Total int `bson:"total"`
		}
		if err := userCursor.Decode(&result); err == nil {
			analytics.TotalUsers = result.Total
		}
	}
	
	// Count total dimensions and distribution
	dimPipeline := []bson.M{
		{"$match": bson.M{"last_observed": bson.M{"$gte": startTime}}},
		{"$group": bson.M{
			"_id": "$dimension_type",
			"count": bson.M{"$sum": 1},
		}},
	}
	
	dimCursor, err := col.Aggregate(ctx, dimPipeline)
	if err != nil {
		return err
	}
	defer dimCursor.Close(ctx)
	
	for dimCursor.Next(ctx) {
		var result struct {
			DimensionType DimensionType `bson:"_id"`
			Count         int           `bson:"count"`
		}
		if err := dimCursor.Decode(&result); err == nil {
			analytics.DimensionDistribution[result.DimensionType] = result.Count
			analytics.TotalDimensions += result.Count
		}
	}
	
	return nil
}

// calculateTopDimensions calculates the most popular dimensions
func (dt *DimensionTracker) calculateTopDimensions(ctx context.Context, analytics *DimensionAnalytics, startTime time.Time) error {
	col := dt.db.Collection("dimension_history")
	
	pipeline := []bson.M{
		{"$match": bson.M{"last_observed": bson.M{"$gte": startTime}}},
		{"$group": bson.M{
			"_id": bson.M{
				"dimension_name": "$dimension_name",
				"dimension_type": "$dimension_type",
			},
			"user_count": bson.M{"$sum": 1},
			"avg_confidence": bson.M{"$avg": "$current_score.confidence"},
		}},
		{"$sort": bson.M{"user_count": -1}},
		{"$limit": 10},
	}
	
	cursor, err := col.Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	
	for cursor.Next(ctx) {
		var result struct {
			ID struct {
				DimensionName string        `bson:"dimension_name"`
				DimensionType DimensionType `bson:"dimension_type"`
			} `bson:"_id"`
			UserCount     int     `bson:"user_count"`
			AvgConfidence float64 `bson:"avg_confidence"`
		}
		
		if err := cursor.Decode(&result); err == nil {
			popularity := DimensionPopularity{
				DimensionName:  result.ID.DimensionName,
				DimensionType:  result.ID.DimensionType,
				UserCount:      result.UserCount,
				AvgConfidence:  result.AvgConfidence,
				TrendDirection: "stable", // TODO: Calculate actual trend
			}
			analytics.TopDimensions = append(analytics.TopDimensions, popularity)
		}
	}
	
	return nil
}

// calculateConfidenceTrends calculates confidence trends over time
func (dt *DimensionTracker) calculateConfidenceTrends(ctx context.Context, analytics *DimensionAnalytics, startTime time.Time) error {
	col := dt.db.Collection("dimension_history")
	
	// Group by day and calculate average confidence
	pipeline := []bson.M{
		{"$match": bson.M{"last_observed": bson.M{"$gte": startTime}}},
		{"$unwind": "$history"},
		{"$match": bson.M{"history.timestamp": bson.M{"$gte": startTime}}},
		{"$group": bson.M{
			"_id": bson.M{
				"year":  bson.M{"$year": "$history.timestamp"},
				"month": bson.M{"$month": "$history.timestamp"},
				"day":   bson.M{"$dayOfMonth": "$history.timestamp"},
			},
			"avg_confidence": bson.M{"$avg": "$history.score.confidence"},
			"total_updates":  bson.M{"$sum": 1},
		}},
		{"$sort": bson.M{"_id": 1}},
	}
	
	cursor, err := col.Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	
	for cursor.Next(ctx) {
		var result struct {
			ID struct {
				Year  int `bson:"year"`
				Month int `bson:"month"`
				Day   int `bson:"day"`
			} `bson:"_id"`
			AvgConfidence float64 `bson:"avg_confidence"`
			TotalUpdates  int     `bson:"total_updates"`
		}
		
		if err := cursor.Decode(&result); err == nil {
			date := time.Date(result.ID.Year, time.Month(result.ID.Month), result.ID.Day, 0, 0, 0, 0, time.UTC)
			trend := ConfidenceTrend{
				Date:          date,
				AvgConfidence: result.AvgConfidence,
				TotalUpdates:  result.TotalUpdates,
			}
			analytics.ConfidenceTrends = append(analytics.ConfidenceTrends, trend)
		}
	}
	
	return nil
}

// calculateUserEngagementTrends calculates user engagement trends
func (dt *DimensionTracker) calculateUserEngagementTrends(ctx context.Context, analytics *DimensionAnalytics, startTime time.Time) error {
	col := dt.db.Collection("dimension_history")
	
	// Group by day and calculate engagement metrics
	pipeline := []bson.M{
		{"$match": bson.M{"last_observed": bson.M{"$gte": startTime}}},
		{"$unwind": "$history"},
		{"$match": bson.M{"history.timestamp": bson.M{"$gte": startTime}}},
		{"$group": bson.M{
			"_id": bson.M{
				"year":  bson.M{"$year": "$history.timestamp"},
				"month": bson.M{"$month": "$history.timestamp"},
				"day":   bson.M{"$dayOfMonth": "$history.timestamp"},
			},
			"active_users":     bson.M{"$addToSet": "$user_id"},
			"dimension_updates": bson.M{"$sum": 1},
			"new_dimensions":   bson.M{"$sum": bson.M{"$cond": []interface{}{bson.M{"$eq": []interface{}{"$history.change_reason", "new_dimension"}}, 1, 0}}},
		}},
		{"$addFields": bson.M{
			"active_user_count": bson.M{"$size": "$active_users"},
		}},
		{"$sort": bson.M{"_id": 1}},
	}
	
	cursor, err := col.Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	
	for cursor.Next(ctx) {
		var result struct {
			ID struct {
				Year  int `bson:"year"`
				Month int `bson:"month"`
				Day   int `bson:"day"`
			} `bson:"_id"`
			ActiveUserCount   int `bson:"active_user_count"`
			DimensionUpdates  int `bson:"dimension_updates"`
			NewDimensions     int `bson:"new_dimensions"`
		}
		
		if err := cursor.Decode(&result); err == nil {
			date := time.Date(result.ID.Year, time.Month(result.ID.Month), result.ID.Day, 0, 0, 0, 0, time.UTC)
			
			avgDimensionsPerUser := 0.0
			if result.ActiveUserCount > 0 {
				avgDimensionsPerUser = float64(result.DimensionUpdates) / float64(result.ActiveUserCount)
			}
			
			trend := UserEngagementTrend{
				Date:                 date,
				ActiveUsers:          result.ActiveUserCount,
				NewDimensions:        result.NewDimensions,
				DimensionUpdates:     result.DimensionUpdates,
				AvgDimensionsPerUser: avgDimensionsPerUser,
			}
			analytics.UserEngagementTrends = append(analytics.UserEngagementTrends, trend)
		}
	}
	
	return nil
}

// CleanupOldDimensionHistory removes old dimension history beyond retention period
func (dt *DimensionTracker) CleanupOldDimensionHistory(ctx context.Context) error {
	col := dt.db.Collection("dimension_history")
	
	retentionPeriod := time.Duration(dt.config.FactRetentionDays) * 24 * time.Hour
	cutoffDate := time.Now().Add(-retentionPeriod)
	
	// Remove entire evolution records where last_observed is too old
	filter := bson.M{"last_observed": bson.M{"$lt": cutoffDate}}
	deleteResult, err := col.DeleteMany(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to cleanup old dimension history: %w", err)
	}
	
	// Clean up old snapshots from remaining records
	updateFilter := bson.M{"last_observed": bson.M{"$gte": cutoffDate}}
	update := bson.M{
		"$pull": bson.M{
			"history": bson.M{"timestamp": bson.M{"$lt": cutoffDate}},
		},
	}
	
	updateResult, err := col.UpdateMany(ctx, updateFilter, update)
	if err != nil {
		return fmt.Errorf("failed to cleanup old snapshots: %w", err)
	}
	
	log.Printf("INFO: Dimension history cleanup completed - Deleted records: %d, Updated records: %d", 
		deleteResult.DeletedCount, updateResult.ModifiedCount)
	
	return nil
}

// Utility functions
func (dt *DimensionTracker) parseTimeRange(timeRange string) (time.Time, error) {
	now := time.Now()
	
	switch timeRange {
	case "24h", "1d":
		return now.Add(-24 * time.Hour), nil
	case "7d", "1w":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d", "1m":
		return now.Add(-30 * 24 * time.Hour), nil
	case "90d", "3m":
		return now.Add(-90 * 24 * time.Hour), nil
	case "1y":
		return now.Add(-365 * 24 * time.Hour), nil
	default:
		return now.Add(-7 * 24 * time.Hour), nil // Default to 1 week
	}
}

func (dt *DimensionTracker) getScoreString(score *DimensionScore) string {
	if score == nil {
		return "nil"
	}
	return fmt.Sprintf("%s(%.2f)", score.Level, score.Confidence)
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
