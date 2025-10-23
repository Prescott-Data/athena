package memory

import (
	"context"
	"fmt"
	"log"
	"time"

	"bitbucket.org/dromos/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Archivist groups oldest in_stm dialogue pages into a segment and promotes them to MTM
type Archivist struct {
	db             *mongo.Database
	sessionManager *SessionManager
	stmStore       *STMStore
}

func NewArchivist(db *mongo.Database) *Archivist {
	stmStore := NewSTMStore(db, nil)
	return &Archivist{
		db:             db,
		sessionManager: NewSessionManager(db, stmStore),
		stmStore:       stmStore,
	}
}

// RunOnce finds the oldest chain with in_stm pages, groups a batch into a Segment, and flips status to in_mtm
func (a *Archivist) RunOnce(ctx context.Context, maxPagesPerSegment int) error {
	if a.db == nil {
		return fmt.Errorf("database is nil")
	}

	pagesCol := a.db.Collection(DialoguePagesCollection)

	// 1) Find the oldest in_stm page to identify a chain
	var oldestPage models.DialoguePage
	findOldest := options.FindOne().SetSort(bson.D{bson.E{Key: "createdAt", Value: 1}})
	if err := pagesCol.FindOne(ctx, bson.M{"status": "in_stm"}, findOldest).Decode(&oldestPage); err != nil {
		if err == mongo.ErrNoDocuments {
			log.Println("INFO: Archivist found no in_stm pages to segment")
			return nil
		}
		return fmt.Errorf("failed to find oldest in_stm page: %w", err)
	}

	// 2) Pull up to maxPagesPerSegment pages for that chain, ordered by createdAt
	filter := bson.M{"chainId": oldestPage.ChainID, "status": "in_stm"}
	findOpts := options.Find().SetSort(bson.D{bson.E{Key: "createdAt", Value: 1}})
	if maxPagesPerSegment > 0 {
		findOpts.SetLimit(int64(maxPagesPerSegment))
	}
	cursor, err := pagesCol.Find(ctx, filter, findOpts)
	if err != nil {
		return fmt.Errorf("failed to query in_stm pages: %w", err)
	}
	defer cursor.Close(ctx)

	var pages []models.DialoguePage
	if err := cursor.All(ctx, &pages); err != nil {
		return fmt.Errorf("failed to read pages cursor: %w", err)
	}
	if len(pages) == 0 {
		log.Println("INFO: Archivist found zero pages after query; nothing to do")
		return nil
	}

	// 3) Create or merge segment using session manager
	now := time.Now()

	// Create candidate segment
	candidateSegment := &models.Segment{
		SegmentID: fmt.Sprintf("segment_%s_%d", oldestPage.ChainID, now.Unix()),
		UserID:    oldestPage.UserID,
		ChainID:   oldestPage.ChainID,
	}

	// Generate topic summary for the segment
	summary, err := a.stmStore.CreateSegmentSummary(ctx, pages)
	if err != nil {
		log.Printf("WARN: Failed to create segment summary: %v", err)
		summary = "Conversation segment"
	}
	candidateSegment.TopicSummary = summary

	// Use session manager to intelligently create or merge the segment
	finalSegment, err := a.sessionManager.ProcessNewSegment(ctx, candidateSegment, pages)
	if err != nil {
		return fmt.Errorf("failed to process segment with session manager: %w", err)
	}

	log.Printf("INFO: Archivist processed segment %s with %d pages for chain %s",
		finalSegment.SegmentID, len(finalSegment.PageIDs), oldestPage.ChainID)

	// 4) Update pages to in_mtm
	pageIDs := make([]primitive.ObjectID, 0, len(pages))
	for _, p := range pages {
		pageIDs = append(pageIDs, p.ID)
	}

	updateRes, err := pagesCol.UpdateMany(ctx, bson.M{"_id": bson.M{"$in": pageIDs}}, bson.M{"$set": bson.M{"status": "in_mtm", "updatedAt": now}})
	if err != nil {
		return fmt.Errorf("failed to update pages to in_mtm: %w", err)
	}
	log.Printf("INFO: Archivist updated %d pages to in_mtm for chain %s", updateRes.ModifiedCount, oldestPage.ChainID)

	// 5) Store segment embedding (best-effort)
	if finalSegment.TopicSummary != "" {
		if err := a.stmStore.StoreSegmentEmbedding(ctx, oldestPage.TenantID, oldestPage.UserID, oldestPage.AgentID, finalSegment.SegmentID, finalSegment.TopicSummary); err != nil {
			log.Printf("WARN: Failed to store segment embedding for %s: %v", finalSegment.SegmentID, err)
		} else {
			log.Printf("INFO: Stored embedding for segment %s", finalSegment.SegmentID)
		}
	}

	// 6) Optional cleanup of old segments for this user
	if err := a.sessionManager.CleanupOldSegments(ctx, oldestPage.UserID); err != nil {
		log.Printf("WARN: Failed to cleanup old segments for user %s: %v", oldestPage.UserID, err)
	}

	return nil
}

// cursorAllByIDs fetches full documents for provided IDs
func cursorAllByIDs(ctx context.Context, col *mongo.Collection, ids []primitive.ObjectID, out interface{}) error {
	cur, err := col.Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	return cur.All(ctx, out)
}
