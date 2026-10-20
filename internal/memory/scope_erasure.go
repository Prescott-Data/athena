package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Prescott-Data/athena/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// eraseBatch is how many chains or events one erasure step handles at a time.
const eraseBatch = 500

// ScopeErasure reports what EraseScope removed.
type ScopeErasure struct {
	Sessions     int
	STMEvents    int
	Events       int
	Chains       int
	Vectors      int
	Blobs        int
	GraphEntries int
}

// SetGraphEraser lets erasure remove what the LTM graph learned from erased chains.
// Without one, graph knowledge is left in place.
func (s *STMStore) SetGraphEraser(g GraphEraser) {
	s.graphEraser = g
}

// EraseScope deletes everything Athena holds for a tenant's user, a user's agent, or
// (with userID empty) a whole tenant: sessions, STM, events and their blobs, chains,
// vectors and, with a GraphEraser, the LTM graph. It is meant for deletion requests
// and offboarding. The scope is closed to writes and new sessions while it runs
// (CheckWritable), and a final sweep catches a write that was already in flight.
//
// External stores are cleared before MongoDB and session records go last, so a
// failed erasure can be retried and finds what is left.
func (s *STMStore) EraseScope(ctx context.Context, tenantID, userID, agentID string) (*ScopeErasure, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if agentID != "" && userID == "" {
		return nil, fmt.Errorf("agent_id needs user_id")
	}
	scope := bson.M{"tenantId": tenantID}
	if userID != "" {
		scope["userId"] = userID
	}
	if agentID != "" {
		scope["agentId"] = agentID
	}
	report := &ScopeErasure{}
	chains := s.db.Collection(CognitiveChainsCollection)

	// 0. Close the scope to writes and new sessions until the erasure is done, so
	// nothing written meanwhile survives it. Reopened however the erasure ends.
	if s.redis != nil {
		key := erasingScopeKey(tenantID, userID, agentID)
		if err := s.redis.SetEX(key, "1", erasingScopeTTL); err != nil {
			return nil, fmt.Errorf("failed to close the scope to writes: %w", err)
		}
		defer func() {
			if err := s.redis.Delete(key); err != nil {
				slog.Error("Failed to reopen an erased scope to writes", slog.String("tenant_id", tenantID), slog.String("error", err.Error()))
			}
		}()
	}
	events := s.db.Collection(CognitiveEventsCollection)

	// 1. Tombstone the scope's sessions, so chain formations already running drop
	// their events instead of writing new chains after the erasure.
	sessions, err := s.scopeSessions(ctx, scope)
	if err != nil {
		return nil, err
	}
	if s.redis != nil {
		for _, id := range sessions {
			if err := s.redis.SetEX(erasedSessionKey(id), "1", erasedSessionTTL); err != nil {
				return nil, fmt.Errorf("failed to mark session %s as erased: %w", id, err)
			}
		}
	}

	// 2. STM lists and pending work of every user and agent in the scope.
	// The scope's users and agents are found once: the final sweep (step 8) runs
	// after their records are gone.
	var pairs []userAgent
	if s.redis != nil {
		var err error
		if pairs, err = s.scopeUserAgents(ctx, scope); err != nil {
			return nil, err
		}
	}
	eraseSTM := func() error {
		if s.redis != nil {
			for _, p := range pairs {
				key := GenerateSTMKey(tenantID, p.user, p.agent)
				if n, err := s.redis.LLen(key); err == nil {
					report.STMEvents += int(n)
				}
				if err := s.redis.Delete(key); err != nil {
					return fmt.Errorf("failed to delete STM for %s/%s: %w", p.user, p.agent, err)
				}
				if err := s.redis.Delete(GenerateScopedQueueName(tenantID, p.user, p.agent)); err != nil {
					return fmt.Errorf("failed to delete pending work for %s/%s: %w", p.user, p.agent, err)
				}
			}
		}
		return nil
	}
	if err := eraseSTM(); err != nil {
		return nil, err
	}

	// 3. Mark the chains being erased before touching the graph. A promotion that
	// races with this erasure sees the mark and takes back what it wrote; chains stay
	// findable until their documents are deleted, so a retry still finds them.
	chainFilter := bson.M{}
	for k, v := range scope {
		chainFilter[k] = v
	}
	chainFilter["$nor"] = bson.A{sessionRecordMatch()}
	if agentID != "" {
		// Chains merge across a user's agents: a chain of another agent may hold this
		// agent's conversation. Such chains record the agent's sessions, and the
		// agent's events point at them.
		merged, err := s.chainsHoldingScope(ctx, scope, sessions)
		if err != nil {
			return nil, err
		}
		if len(merged) > 0 {
			chainFilter = bson.M{"$or": bson.A{chainFilter, bson.M{"chainId": bson.M{"$in": merged}}}}
		}
	}
	if _, err := chains.UpdateMany(ctx, chainFilter, bson.M{"$set": bson.M{ChainErasingField: true}}); err != nil {
		return nil, fmt.Errorf("failed to mark chains for erasure: %w", err)
	}

	// 4. The LTM graph. A user or tenant erasure removes the whole graph scope; an
	// agent erasure removes only what its chains contributed (step 5).
	if s.graphEraser != nil && agentID == "" {
		n, err := s.graphEraser.EraseScope(ctx, tenantID, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to erase the LTM graph: %w", err)
		}
		report.GraphEntries += n
	}

	// 5. Chains (not session records): their vectors, then the documents.
	for {
		batch, err := findBatch[models.CognitiveChain](ctx, chains, chainFilter, bson.M{"chainId": 1, "userId": 1})
		if err != nil {
			return nil, fmt.Errorf("failed to list chains: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.ChainID)
		}
		if s.milvus != nil {
			if err := s.milvus.DeleteSegmentEmbeddingsByID(ctx, ids); err != nil {
				return nil, fmt.Errorf("failed to delete chain vectors: %w", err)
			}
			report.Vectors += len(ids)
		}
		if s.graphEraser != nil && agentID != "" {
			n, err := s.graphEraser.EraseChains(ctx, tenantID, userID, ids)
			if err != nil {
				return nil, fmt.Errorf("failed to erase chains from the LTM graph: %w", err)
			}
			report.GraphEntries += n
		}
		res, err := chains.DeleteMany(ctx, bson.M{"chainId": bson.M{"$in": ids}})
		if err != nil {
			return nil, fmt.Errorf("failed to delete chains: %w", err)
		}
		if res.DeletedCount == 0 {
			return nil, fmt.Errorf("chains were listed for erasure but none could be deleted")
		}
		report.Chains += int(res.DeletedCount)
	}

	// 6. Events, with the unscoped copies older releases made of them: vectors and
	// blobs, then the documents.
	eraseEvents := func() error {
		for {
			batch, err := findBatch[models.CognitiveEvent](ctx, events, scope, nil)
			if err != nil {
				return fmt.Errorf("failed to list events: %w", err)
			}
			if len(batch) == 0 {
				break
			}
			copies, err := s.legacyFormationCopies(ctx, batch)
			if err != nil {
				return err
			}
			batch = append(batch, copies...)
			if s.milvus != nil {
				ids := eventIDs(batch)
				if err := s.milvus.DeleteSegmentEmbeddingsByID(ctx, ids); err != nil {
					return fmt.Errorf("failed to delete event vectors: %w", err)
				}
				if err := s.milvus.DeleteEventEmbeddingsByID(ctx, ids); err != nil {
					return fmt.Errorf("failed to delete event vectors: %w", err)
				}
				report.Vectors += len(ids)
			}
			if s.blobStore != nil {
				for _, e := range batch {
					if e.BlobURI == "" {
						continue
					}
					if err := s.blobStore.Delete(ctx, e.BlobURI); err != nil {
						return fmt.Errorf("failed to delete blob payload %s: %w", e.BlobURI, err)
					}
					report.Blobs++
				}
			}
			ids := make(bson.A, 0, len(batch))
			for _, e := range batch {
				ids = append(ids, e.ID)
			}
			res, err := events.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
			if err != nil {
				return fmt.Errorf("failed to delete events: %w", err)
			}
			if res.DeletedCount == 0 {
				return fmt.Errorf("events were listed for erasure but none could be deleted")
			}
			report.Events += int(res.DeletedCount)
		}
		return nil
	}
	if err := eraseEvents(); err != nil {
		return nil, err
	}

	// 7. Session records last.
	sessionFilter := bson.M{"$and": bson.A{scope, sessionRecordMatch()}}
	res, err := chains.DeleteMany(ctx, sessionFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to delete sessions: %w", err)
	}
	report.Sessions = int(res.DeletedCount)

	// 8. A write that passed its check just before the scope closed may have landed
	// after the sweeps above. With the sessions gone nothing new can arrive, so one
	// more sweep catches it.
	if err := eraseSTM(); err != nil {
		return nil, err
	}
	if err := eraseEvents(); err != nil {
		return nil, err
	}

	slog.Info("Memory scope erased",
		slog.String("tenant_id", tenantID), slog.String("user_id", userID), slog.String("agent_id", agentID),
		slog.Int("sessions", report.Sessions), slog.Int("events", report.Events), slog.Int("chains", report.Chains),
		slog.Int("vectors", report.Vectors), slog.Int("blobs", report.Blobs), slog.Int("graph_entries", report.GraphEntries))
	return report, nil
}

// ChainErasingField marks a chain whose erasure has begun. The promoter neither
// promotes such a chain nor keeps a promotion of it that finished meanwhile.
const ChainErasingField = "erasing"

// chainsHoldingScope returns the chains that hold memory of the scope without being
// in it: chains merged from the scope's sessions, and chains its events point at.
func (s *STMStore) chainsHoldingScope(ctx context.Context, scope bson.M, sessions []string) ([]string, error) {
	ids := map[string]struct{}{}
	if len(sessions) > 0 {
		cursor, err := s.db.Collection(CognitiveChainsCollection).Find(ctx,
			bson.M{"sessionIds": bson.M{"$in": sessions}}, options.Find().SetProjection(bson.M{"chainId": 1}))
		if err != nil {
			return nil, fmt.Errorf("failed to find chains merged from the scope: %w", err)
		}
		var found []models.CognitiveChain
		if err := cursor.All(ctx, &found); err != nil {
			return nil, err
		}
		for _, c := range found {
			ids[c.ChainID] = struct{}{}
		}
	}
	pointed, err := s.db.Collection(CognitiveEventsCollection).Distinct(ctx, "chainId", scope)
	if err != nil {
		return nil, fmt.Errorf("failed to find chains of the scope's events: %w", err)
	}
	for _, v := range pointed {
		if id, ok := v.(string); ok && id != "" {
			ids[id] = struct{}{}
		}
	}
	for _, id := range sessions {
		delete(ids, id) // session records are erased last, by the session filter
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	return out, nil
}

// sessionRecordMatch matches session records (see sessionRecordFilter), without
// fixing the session ID.
func sessionRecordMatch() bson.M {
	return bson.M{"$or": bson.A{
		bson.M{"kind": models.ChainKindSession},
		bson.M{"kind": bson.M{"$exists": false}, "summary": "", "sessionIds": bson.M{"$exists": false}},
	}}
}

func (s *STMStore) scopeSessions(ctx context.Context, scope bson.M) ([]string, error) {
	cursor, err := s.db.Collection(CognitiveChainsCollection).Find(ctx,
		bson.M{"$and": bson.A{scope, sessionRecordMatch()}},
		options.Find().SetProjection(bson.M{"chainId": 1}))
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	var records []models.CognitiveChain
	if err := cursor.All(ctx, &records); err != nil {
		return nil, fmt.Errorf("failed to decode sessions: %w", err)
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ChainID)
	}
	return ids, nil
}

type userAgent struct{ user, agent string }

// scopeUserAgents returns every (user, agent) pair with sessions, chains or events
// in the scope. STM keys are derived from these exactly; matching keys by pattern
// could reach other tenants whose IDs contain the separator.
func (s *STMStore) scopeUserAgents(ctx context.Context, scope bson.M) ([]userAgent, error) {
	seen := map[userAgent]struct{}{}
	for _, name := range []string{CognitiveChainsCollection, CognitiveEventsCollection} {
		cursor, err := s.db.Collection(name).Aggregate(ctx, mongo.Pipeline{
			{{Key: "$match", Value: scope}},
			{{Key: "$group", Value: bson.M{"_id": bson.M{"u": "$userId", "a": "$agentId"}}}},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list users and agents in %s: %w", name, err)
		}
		var rows []struct {
			ID struct {
				U string `bson:"u"`
				A string `bson:"a"`
			} `bson:"_id"`
		}
		if err := cursor.All(ctx, &rows); err != nil {
			return nil, fmt.Errorf("failed to decode users and agents: %w", err)
		}
		for _, r := range rows {
			if r.ID.U != "" && r.ID.A != "" {
				seen[userAgent{r.ID.U, r.ID.A}] = struct{}{}
			}
		}
	}
	pairs := make([]userAgent, 0, len(seen))
	for p := range seen {
		pairs = append(pairs, p)
	}
	return pairs, nil
}

// findBatch returns up to eraseBatch documents matching filter. Erasure deletes
// each batch before asking for the next, so repeated calls walk the whole set.
func findBatch[T any](ctx context.Context, col *mongo.Collection, filter interface{}, projection interface{}) ([]T, error) {
	opts := options.Find().SetLimit(eraseBatch)
	if projection != nil {
		opts.SetProjection(projection)
	}
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	var out []T
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// erasingScopeTTL bounds how long a scope stays closed to writes if an erasure dies
// without reopening it.
const erasingScopeTTL = time.Hour

// erasingScopeKey identifies a scope being erased. The IDs are hashed with length
// prefixes, so no pair of scopes can share a key whatever characters they contain.
func erasingScopeKey(tenantID, userID, agentID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s|%d:%s|%d:%s", len(tenantID), tenantID, len(userID), userID, len(agentID), agentID)))
	return "scope_erasing:v1:" + hex.EncodeToString(sum[:16])
}

// ErrScopeErasing is returned for a write to memory that is being erased.
var ErrScopeErasing = errors.New("this memory is being erased; retry when the erasure has finished")

// CheckWritable returns ErrScopeErasing when a write to the given scope, or to the
// given session (empty for a new one), must be refused because an erasure covering
// it is running or the session has been erased.
func (s *STMStore) CheckWritable(tenantID, userID, agentID, sessionID string) error {
	if s.redis == nil {
		return nil
	}
	keys := []string{
		erasingScopeKey(tenantID, "", ""),
		erasingScopeKey(tenantID, userID, ""),
		erasingScopeKey(tenantID, userID, agentID),
	}
	if sessionID != "" {
		keys = append(keys, erasedSessionKey(sessionID))
	}
	for _, key := range keys {
		exists, err := s.redis.Exists(key)
		if err != nil {
			return fmt.Errorf("could not check for a running erasure: %w", err)
		}
		if exists {
			return ErrScopeErasing
		}
	}
	return nil
}
