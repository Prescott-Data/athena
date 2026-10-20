package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ErrSessionNotFound is returned when no session record exists for an ID.
var ErrSessionNotFound = errors.New("session not found")

// erasedSessionTTL is how long a deleted session's tombstone is kept. It only has to
// outlive chain formations that were already running when the session was deleted.
const erasedSessionTTL = 24 * time.Hour

func erasedSessionKey(sessionID string) string {
	return "session_erased:v1:" + sessionID
}

// SessionRecord returns the record CreateSession wrote for a session: the
// cognitive_chains document whose chainId is the session ID. MTM chain IDs are
// rejected, so a segment ID passed by mistake cannot be treated as a session.
func (s *STMStore) SessionRecord(ctx context.Context, sessionID string) (*models.CognitiveChain, error) {
	if sessionID == "" {
		return nil, ErrSessionNotFound
	}
	var record models.CognitiveChain
	opts := options.FindOne().SetSort(bson.D{{Key: "startedAt", Value: 1}})
	err := s.db.Collection(CognitiveChainsCollection).FindOne(ctx, sessionRecordFilter(sessionID), opts).Decode(&record)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load session %s: %w", sessionID, err)
	}
	return &record, nil
}

// sessionRecordFilter matches a session record: marked as one, or, for records
// written before the marker existed, a chain document without content of its own.
func sessionRecordFilter(sessionID string) bson.M {
	return bson.M{
		"chainId": sessionID,
		"$or": bson.A{
			bson.M{"kind": models.ChainKindSession},
			bson.M{"kind": bson.M{"$exists": false}, "summary": "", "sessionIds": bson.M{"$exists": false}},
		},
	}
}

// SessionEvents returns every cognitive event stored through a session, oldest first.
//
// Events written since sessionId was introduced carry it directly. Older events are
// found through the durable copy written on arrival (chainId == session ID), and the
// copies older releases inserted at chain formation (no tenant, no session) are
// matched to those originals by timestamp and content.
func (s *STMStore) SessionEvents(ctx context.Context, record *models.CognitiveChain) ([]models.CognitiveEvent, error) {
	sessionID := record.ChainID
	col := s.db.Collection(CognitiveEventsCollection)
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}})

	cursor, err := col.Find(ctx, bson.M{"$or": bson.A{
		bson.M{"sessionId": sessionID},
		bson.M{"chainId": sessionID},
	}}, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to query session events: %w", err)
	}
	var events []models.CognitiveEvent
	if err := cursor.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("failed to decode session events: %w", err)
	}

	legacy, err := s.legacyFormationCopies(ctx, events)
	if err != nil {
		return nil, err
	}
	events = append(events, legacy...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })
	return events, nil
}

// legacyFormationCopies finds the unscoped duplicates older releases inserted when
// they summarised a session's events into a chain.
func (s *STMStore) legacyFormationCopies(ctx context.Context, originals []models.CognitiveEvent) ([]models.CognitiveEvent, error) {
	if len(originals) == 0 {
		return nil, nil
	}
	known := make(map[primitive.ObjectID]struct{}, len(originals))
	match := make(bson.A, 0, len(originals))
	for _, e := range originals {
		known[e.ID] = struct{}{}
		match = append(match, bson.M{"createdAt": e.CreatedAt, "content": e.Content, "role": e.Role})
	}
	cursor, err := s.db.Collection(CognitiveEventsCollection).Find(ctx, bson.M{
		"tenantId": "",
		"$or":      match,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query legacy event copies: %w", err)
	}
	var found []models.CognitiveEvent
	if err := cursor.All(ctx, &found); err != nil {
		return nil, fmt.Errorf("failed to decode legacy event copies: %w", err)
	}
	copies := found[:0]
	for _, e := range found {
		if _, dup := known[e.ID]; !dup {
			copies = append(copies, e)
		}
	}
	return copies, nil
}

// SessionChains returns the MTM chains summarised from a session's events, newest
// first. A chain formed from several sessions is returned for each of them.
//
// Chains record the sessions they came from (sessionIds). Chains formed by older
// releases do not, and their events were copied without a chain ID, so they are
// matched by time instead: STM is shared by every session of a tenant, user and
// agent, and a chain's startedAt and lastEventAt are the times of its first and last
// events, so a chain in the session's scope whose span covers one of the session's
// events was formed from it. A chain merged by an older release spans from its
// target's start to the merged chain's end, so it can also cover another session's
// events; erasure then removes it too, keeping its source events. For an erasure,
// removing one derived summary too many is preferred over leaving content behind.
func (s *STMStore) SessionChains(ctx context.Context, record *models.CognitiveChain, events []models.CognitiveEvent, limit int) ([]models.CognitiveChain, error) {
	sessionID := record.ChainID
	col := s.db.Collection(CognitiveChainsCollection)
	opts := options.Find().SetSort(bson.D{{Key: "lastEventAt", Value: -1}})

	cursor, err := col.Find(ctx, bson.M{
		"chainId": bson.M{"$ne": sessionID},
		"$or": bson.A{
			bson.M{"chainId": bson.M{"$in": chainIDsOf(sessionID, events)}},
			bson.M{"sessionIds": sessionID},
		},
	}, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to query session chains: %w", err)
	}
	var chains []models.CognitiveChain
	if err := cursor.All(ctx, &chains); err != nil {
		return nil, fmt.Errorf("failed to decode session chains: %w", err)
	}

	legacy, err := s.legacySessionChains(ctx, record, events)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(chains))
	for _, c := range chains {
		seen[c.ChainID] = struct{}{}
	}
	for _, c := range legacy {
		if _, dup := seen[c.ChainID]; !dup {
			seen[c.ChainID] = struct{}{}
			chains = append(chains, c)
		}
	}
	sort.SliceStable(chains, func(i, j int) bool { return chains[i].LastEventAt.After(chains[j].LastEventAt) })
	if limit > 0 && len(chains) > limit {
		chains = chains[:limit]
	}
	return chains, nil
}

// legacySessionChains finds chains formed by older releases from a session's events.
func (s *STMStore) legacySessionChains(ctx context.Context, record *models.CognitiveChain, events []models.CognitiveEvent) ([]models.CognitiveChain, error) {
	var times []time.Time
	for _, e := range events {
		if e.SessionID == "" && !e.CreatedAt.IsZero() {
			times = append(times, e.CreatedAt)
		}
	}
	if len(times) == 0 {
		return nil, nil
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

	cursor, err := s.db.Collection(CognitiveChainsCollection).Find(ctx, bson.M{
		"tenantId":    record.TenantID,
		"userId":      record.UserID,
		"agentId":     record.AgentID,
		"chainId":     bson.M{"$ne": record.ChainID},
		"summary":     bson.M{"$ne": ""},
		"startedAt":   bson.M{"$lte": times[len(times)-1]},
		"lastEventAt": bson.M{"$gte": times[0]},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query legacy session chains: %w", err)
	}
	var candidates []models.CognitiveChain
	if err := cursor.All(ctx, &candidates); err != nil {
		return nil, fmt.Errorf("failed to decode legacy session chains: %w", err)
	}
	var matched []models.CognitiveChain
	for _, c := range candidates {
		// First event time at or after the chain's start; the chain matches if it
		// is no later than the chain's last event.
		i := sort.Search(len(times), func(i int) bool { return !times[i].Before(c.StartedAt) })
		if i < len(times) && !times[i].After(c.LastEventAt) {
			matched = append(matched, c)
		}
	}
	return matched, nil
}

// ChainHeat computes a chain's current heat. Heat decays with time, so the score
// stored on the chain is only as fresh as its last recalculation.
func (s *STMStore) ChainHeat(ctx context.Context, chain *models.CognitiveChain) (float64, *models.HeatFactors, error) {
	return NewHeatScorer(s.db).ComputeSegmentHeat(ctx, chain)
}

// AnalyzeEventTopics runs the topic analyser over a set of events.
func (s *STMStore) AnalyzeEventTopics(ctx context.Context, events []models.CognitiveEvent) (*MultiTopicResult, error) {
	if s.topicAnalyzer == nil {
		return nil, fmt.Errorf("topic analyzer is not configured")
	}
	return s.topicAnalyzer.AnalyzeTopics(ctx, events)
}

// SessionErasure reports what EraseSession removed.
type SessionErasure struct {
	STMEvents int
	Events    int
	Chains    int
	Vectors   int
	Blobs     int
	// GraphEntries counts LTM graph nodes and edges removed because no remaining
	// chain supports them.
	GraphEntries int
}

// EraseSession deletes everything Athena holds for a session: its STM entries, its
// cognitive events and their blob payloads, every MTM chain summarised from them
// (with the chains' and events' vectors) and the session record. A chain formed
// from several sessions is deleted as a whole, because its summary contains this
// session's content; the other sessions keep their own events.
//
// With a GraphEraser, knowledge the LTM graph extracted from those chains is removed
// too, unless another remaining chain also supports it.
func (s *STMStore) EraseSession(ctx context.Context, record *models.CognitiveChain) (*SessionErasure, error) {
	sessionID := record.ChainID

	// Tombstone first, so a chain formation that is already running drops this
	// session's events instead of summarising them after they are deleted.
	if s.redis != nil {
		if err := s.redis.SetEX(erasedSessionKey(sessionID), "1", erasedSessionTTL); err != nil {
			return nil, fmt.Errorf("failed to mark session %s as erased: %w", sessionID, err)
		}
	}
	report, err := s.eraseSession(ctx, record)
	if err != nil && s.redis != nil {
		// The session is still there; let its memory keep forming until a retry.
		if delErr := s.redis.Delete(erasedSessionKey(sessionID)); delErr != nil {
			slog.Error("Failed to clear erasure tombstone", slog.String("session_id", sessionID), slog.String("error", delErr.Error()))
		}
	}
	return report, err
}

func (s *STMStore) eraseSession(ctx context.Context, record *models.CognitiveChain) (*SessionErasure, error) {
	sessionID := record.ChainID
	report := &SessionErasure{}

	events, err := s.SessionEvents(ctx, record)
	if err != nil {
		return nil, err
	}

	removed, err := s.removeSessionFromSTM(record, events)
	if err != nil {
		return nil, err
	}
	report.STMEvents = removed

	chains, err := s.SessionChains(ctx, record, events, 0)
	if err != nil {
		return nil, err
	}

	// Vectors and blobs live outside MongoDB. They are removed first and any failure
	// stops the erasure before the session record goes, so the call can be retried.
	if s.milvus != nil {
		vectorIDs := make([]string, 0, len(chains)+len(events))
		for _, chain := range chains {
			vectorIDs = append(vectorIDs, chain.ChainID)
		}
		for _, e := range events {
			vectorIDs = append(vectorIDs, e.ID.Hex())
		}
		if err := s.milvus.DeleteSegmentEmbeddingsByID(ctx, vectorIDs); err != nil {
			return nil, fmt.Errorf("failed to delete session vectors: %w", err)
		}
		if err := s.milvus.DeleteEventEmbeddingsByID(ctx, eventIDs(events)); err != nil {
			return nil, fmt.Errorf("failed to delete session event vectors: %w", err)
		}
		report.Vectors = len(vectorIDs)
	}

	if s.blobStore != nil {
		for _, e := range events {
			if e.BlobURI == "" {
				continue
			}
			if err := s.blobStore.Delete(ctx, e.BlobURI); err != nil {
				return nil, fmt.Errorf("failed to delete blob payload %s: %w", e.BlobURI, err)
			}
			report.Blobs++
		}
	}

	if len(chains) > 0 {
		ids := make([]string, 0, len(chains))
		for _, chain := range chains {
			ids = append(ids, chain.ChainID)
		}
		// Mark before the graph step (see ChainErasingField).
		if _, err := s.db.Collection(CognitiveChainsCollection).UpdateMany(ctx,
			bson.M{"chainId": bson.M{"$in": ids}}, bson.M{"$set": bson.M{ChainErasingField: true}}); err != nil {
			return nil, fmt.Errorf("failed to mark session chains for erasure: %w", err)
		}
	}
	if s.graphEraser != nil && len(chains) > 0 {
		ids := make([]string, 0, len(chains))
		for _, chain := range chains {
			ids = append(ids, chain.ChainID)
		}
		n, err := s.graphEraser.EraseChains(ctx, record.TenantID, record.UserID, ids)
		if err != nil {
			return nil, fmt.Errorf("failed to erase session knowledge from the LTM graph: %w", err)
		}
		report.GraphEntries = n
	}

	// Chains go before the events they are found through, so a retry after a
	// partial failure still finds them; the session record goes last.
	if len(chains) > 0 {
		chainIDs := make(bson.A, 0, len(chains))
		for _, chain := range chains {
			chainIDs = append(chainIDs, chain.ChainID)
		}
		if _, err := s.db.Collection(CognitiveChainsCollection).DeleteMany(ctx, bson.M{"chainId": bson.M{"$in": chainIDs}}); err != nil {
			return nil, fmt.Errorf("failed to delete session chains: %w", err)
		}
	}
	report.Chains = len(chains)

	if len(events) > 0 {
		ids := make(bson.A, 0, len(events))
		for _, e := range events {
			ids = append(ids, e.ID)
		}
		res, err := s.db.Collection(CognitiveEventsCollection).DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
		if err != nil {
			return nil, fmt.Errorf("failed to delete session events: %w", err)
		}
		report.Events = int(res.DeletedCount)
	}

	if _, err := s.db.Collection(CognitiveChainsCollection).DeleteMany(ctx, sessionRecordFilter(sessionID)); err != nil {
		return nil, fmt.Errorf("failed to delete session record: %w", err)
	}

	slog.Info("Session erased",
		slog.String("session_id", sessionID),
		slog.Int("stm_events", report.STMEvents),
		slog.Int("events", report.Events),
		slog.Int("chains", report.Chains),
		slog.Int("vectors", report.Vectors),
		slog.Int("blobs", report.Blobs))
	return report, nil
}

// removeSessionFromSTM removes a session's entries from its scope's STM list. The
// list is shared by every session of the same tenant, user and agent, so entries
// are removed one by one rather than by deleting the key.
func (s *STMStore) removeSessionFromSTM(record *models.CognitiveChain, events []models.CognitiveEvent) (int, error) {
	if s.redis == nil {
		return 0, nil
	}
	key := GenerateSTMKey(record.TenantID, record.UserID, record.AgentID)
	entries, err := s.redis.LRange(key, 0, -1)
	if err != nil {
		return 0, fmt.Errorf("failed to read STM for session %s: %w", record.ChainID, err)
	}

	type stamp struct {
		at      int64
		content string
	}
	stored := make(map[stamp]struct{}, len(events))
	ids := make(map[string]struct{}, len(events))
	for _, e := range events {
		stored[stamp{e.CreatedAt.UnixMilli(), e.Content}] = struct{}{}
		ids[e.ID.Hex()] = struct{}{}
	}

	removed := 0
	for _, raw := range entries {
		var e STMEvent
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		_, byID := ids[e.EventID]
		_, byStamp := stored[stamp{e.Timestamp.UnixMilli(), e.Content}]
		if e.SessionID != record.ChainID && e.ChainID != record.ChainID && !byID && !byStamp {
			continue
		}
		if err := s.redis.LRem(key, 1, raw); err != nil {
			return removed, fmt.Errorf("failed to remove STM entry for session %s: %w", record.ChainID, err)
		}
		removed++
	}
	return removed, nil
}

// dropErasedSessionEvents removes events whose session has been deleted.
func (s *STMStore) dropErasedSessionEvents(events []models.CognitiveEvent) []models.CognitiveEvent {
	if s.redis == nil {
		return events
	}
	erased := make(map[string]bool)
	kept := events[:0]
	for _, e := range events {
		if e.SessionID != "" {
			gone, seen := erased[e.SessionID]
			if !seen {
				exists, err := s.redis.Exists(erasedSessionKey(e.SessionID))
				gone = err == nil && exists
				erased[e.SessionID] = gone
			}
			if gone {
				continue
			}
		}
		kept = append(kept, e)
	}
	return kept
}

// promoteEventsToChain moves a chain's events from STM status onto the chain. Events
// that already have a durable copy are updated in place; the rest are inserted.
func (s *STMStore) promoteEventsToChain(ctx context.Context, chainID string, events []models.CognitiveEvent) error {
	col := s.db.Collection(CognitiveEventsCollection)
	var updates []mongo.WriteModel
	var inserts []interface{}
	for i := range events {
		events[i].Status = "in_mtm"
		if events[i].ID.IsZero() {
			events[i].ID = primitive.NewObjectID()
			inserts = append(inserts, events[i])
			continue
		}
		updates = append(updates, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": events[i].ID}).
			SetUpdate(bson.M{"$set": bson.M{
				"chainId":    chainID,
				"status":     "in_mtm",
				"eventIndex": events[i].EventIndex,
			}}))
	}
	if len(updates) > 0 {
		if _, err := col.BulkWrite(ctx, updates, options.BulkWrite().SetOrdered(false)); err != nil {
			return fmt.Errorf("failed to promote stored events: %w", err)
		}
	}
	if len(inserts) > 0 {
		if _, err := col.InsertMany(ctx, inserts); err != nil {
			return fmt.Errorf("failed to insert events: %w", err)
		}
	}
	return nil
}

// chainHasErasedSession reports whether any session the chain was formed from has
// been deleted.
func (s *STMStore) chainHasErasedSession(chain *models.CognitiveChain) bool {
	if s.redis == nil || chain == nil {
		return false
	}
	for _, id := range chain.SessionIDs {
		if exists, err := s.redis.Exists(erasedSessionKey(id)); err == nil && exists {
			return true
		}
	}
	return false
}

// discardChain deletes a chain, its events from deleted sessions and its vectors.
// It is used when a session is deleted while one of its chains is being formed.
func (s *STMStore) discardChain(ctx context.Context, chain *models.CognitiveChain) {
	vectorIDs := []string{chain.ChainID}
	cursor, err := s.db.Collection(CognitiveEventsCollection).Find(ctx, bson.M{"chainId": chain.ChainID})
	if err == nil {
		var events []models.CognitiveEvent
		if cursor.All(ctx, &events) == nil {
			var erased bson.A
			for _, e := range events {
				vectorIDs = append(vectorIDs, e.ID.Hex())
				if exists, err := s.redis.Exists(erasedSessionKey(e.SessionID)); err == nil && exists {
					erased = append(erased, e.ID)
				}
			}
			if len(erased) > 0 {
				if _, err := s.db.Collection(CognitiveEventsCollection).DeleteMany(ctx, bson.M{"_id": bson.M{"$in": erased}}); err != nil {
					slog.Error("Failed to delete events of an erased session", slog.String("chain_id", chain.ChainID), slog.String("error", err.Error()))
				}
			}
		}
	}
	if s.milvus != nil {
		if err := s.milvus.DeleteSegmentEmbeddingsByID(ctx, vectorIDs); err != nil {
			slog.Error("Failed to delete vectors of a discarded chain", slog.String("chain_id", chain.ChainID), slog.String("error", err.Error()))
		}
		if err := s.milvus.DeleteEventEmbeddingsByID(ctx, vectorIDs[1:]); err != nil {
			slog.Error("Failed to delete event vectors of a discarded chain", slog.String("chain_id", chain.ChainID), slog.String("error", err.Error()))
		}
	}
	if _, err := s.db.Collection(CognitiveChainsCollection).DeleteOne(ctx, bson.M{"chainId": chain.ChainID}); err != nil {
		slog.Error("Failed to delete a chain of an erased session", slog.String("chain_id", chain.ChainID), slog.String("error", err.Error()))
	}
	slog.Info("Discarded chain formed from a deleted session", slog.String("chain_id", chain.ChainID))
}

func sessionIDsOf(events []models.CognitiveEvent) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		if _, dup := seen[e.SessionID]; !dup {
			seen[e.SessionID] = struct{}{}
			ids = append(ids, e.SessionID)
		}
	}
	return ids
}

// eventIDs returns the hex IDs of events, which are their vector IDs.
func eventIDs(events []models.CognitiveEvent) []string {
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID.Hex())
	}
	return ids
}

func firstSessionID(events []models.CognitiveEvent) string {
	for _, e := range events {
		if e.SessionID != "" {
			return e.SessionID
		}
	}
	return ""
}

// chainIDsOf returns the MTM chains the events were summarised into, excluding the
// session record itself.
func chainIDsOf(sessionID string, events []models.CognitiveEvent) []string {
	seen := make(map[string]struct{})
	ids := []string{}
	for _, e := range events {
		if e.ChainID == "" || e.ChainID == sessionID {
			continue
		}
		if _, dup := seen[e.ChainID]; !dup {
			seen[e.ChainID] = struct{}{}
			ids = append(ids, e.ChainID)
		}
	}
	return ids
}

// SessionActivity returns how many events were stored through a session and when
// the latest one arrived (zero time if none).
func (s *STMStore) SessionActivity(ctx context.Context, sessionID string) (int64, time.Time, error) {
	col := s.db.Collection(CognitiveEventsCollection)
	filter := bson.M{"$or": bson.A{bson.M{"sessionId": sessionID}, bson.M{"chainId": sessionID}}}
	count, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to count session events: %w", err)
	}
	if count == 0 {
		return 0, time.Time{}, nil
	}
	var latest models.CognitiveEvent
	opts := options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: -1}})
	if err := col.FindOne(ctx, filter, opts).Decode(&latest); err != nil {
		return count, time.Time{}, fmt.Errorf("failed to load latest session event: %w", err)
	}
	return count, latest.CreatedAt, nil
}

// StoredEvents drops the unscoped copies older releases made at chain formation,
// leaving one document per event a client stored.
func StoredEvents(events []models.CognitiveEvent) []models.CognitiveEvent {
	kept := make([]models.CognitiveEvent, 0, len(events))
	for _, e := range events {
		if e.TenantID != "" {
			kept = append(kept, e)
		}
	}
	return kept
}
