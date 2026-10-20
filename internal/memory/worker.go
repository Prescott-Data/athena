package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/Prescott-Data/athena/internal/cache"
	"github.com/Prescott-Data/athena/internal/models"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	HighSimilarityThreshold = 0.9
	LowSimilarityThreshold  = 0.7
	STMEventTTL             = 24 * time.Hour
)

// Worker struct manages the background processing of cognitive tasks
type Worker struct {
	taskQueue TaskQueuer
	stmStore  STMStorer
	redis     cache.Interface
}

// NewWorker creates a new Worker
func NewWorker(taskQueue TaskQueuer, stmStore STMStorer, redis cache.Interface) *Worker {
	return &Worker{
		taskQueue: taskQueue,
		stmStore:  stmStore,
		redis:     redis,
	}
}

// Start processing tasks from the queue
func (w *Worker) Start(ctx context.Context, workerID int) {
	log.Printf("Worker %d starting...", workerID)
	for {
		select {
		case <-ctx.Done():
			log.Printf("Worker %d stopping...", workerID)
			return
		default:
			if err := w.processNextTask(ctx, workerID); err != nil {
				log.Printf("ERROR: Worker %d failed to process task: %v", workerID, err)
			}
		}
	}
}

// processNextTask fetches and processes the next task from the queue
func (w *Worker) processNextTask(ctx context.Context, workerID int) error {
	// Step 1: Block and wait for a scoped queue name from the global work queue
	result, err := w.redis.BRPop(1*time.Second, GlobalWorkQueueName)
	if err != nil {
		if err == redis.Nil {
			// No tasks in the queue, wait a bit
			time.Sleep(1 * time.Second)
			return nil
		}
		return fmt.Errorf("failed to dequeue from global work queue: %w", err)
	}

	// BRPop returns nil with no error when the queue is empty (timeout)
	if len(result) < 2 {
		time.Sleep(1 * time.Second)
		return nil
	}

	scopedQueueName := result[1]

	// Step 2: Pop the actual task from the scoped queue
	envelopeJSON, err := w.redis.RPop(scopedQueueName)
	if err != nil {
		if err == redis.Nil {
			// This might happen in a race condition, it's not a fatal error
			log.Printf("WARN: Worker %d found empty scoped queue: %s", workerID, scopedQueueName)
			return nil
		}
		return fmt.Errorf("failed to dequeue from scoped queue %s: %w", scopedQueueName, err)
	}

	var task TaskEnvelope
	if err := json.Unmarshal([]byte(envelopeJSON), &task); err != nil {
		return fmt.Errorf("failed to unmarshal task envelope: %w", err)
	}

	startTime := time.Now()

	var processingErr error
	var payload models.CognitiveChainCheckTask

	switch task.Type {
	case TaskTypeCognitiveChainCheck:
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			processingErr = fmt.Errorf("failed to unmarshal task payload: %w", err)
			break
		}
		// One worker at a time reads, summarises and trims a scope's STM: two workers
		// forming chains from the same window produce duplicate chains.
		release, held := w.lockScope(&payload)
		if !held {
			time.Sleep(scopeBusyBackoff)
			if err := w.reEnqueueTask(ctx, scopedQueueName, &task, &payload); err != nil {
				return fmt.Errorf("failed to requeue task for a busy scope: %w", err)
			}
			return nil
		}
		log.Printf("Worker %d processing task %s (UserID: %s)", workerID, task.ID, payload.UserID)
		processingErr = w.processCognitiveChainCheck(ctx, &payload)
		release()
	case TaskTypeIdleFlush:
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			processingErr = fmt.Errorf("failed to unmarshal task payload: %w", err)
			break
		}
		// The idle flush reads and trims the same STM, so it takes the same lock.
		release, held := w.lockScope(&payload)
		if !held {
			// A chain check is running for this scope; the next sweep retries.
			return nil
		}
		log.Printf("Worker %d flushing idle STM (UserID: %s)", workerID, payload.UserID)
		processingErr = w.processIdleFlush(ctx, &payload)
		release()
	default:
		processingErr = fmt.Errorf("unknown task type: %s", task.Type)
	}

	duration := time.Since(startTime)
	if processingErr != nil {
		log.Printf("Worker %d task %s failed after %v: %v", workerID, task.ID, duration, processingErr)

		// Retry logic
		const MaxRetries = 3
		if payload.RetryCount < MaxRetries {
			payload.RetryCount++
			log.Printf("Retrying task %s (Attempt %d/%d)", task.ID, payload.RetryCount, MaxRetries)
			if err := w.reEnqueueTask(ctx, scopedQueueName, &task, &payload); err != nil {
				log.Printf("ERROR: Failed to re-enqueue task %s: %v", task.ID, err)
			}
		} else {
			log.Printf("Task %s exceeded max retries (%d). Marking as failed.", task.ID, MaxRetries)
			// Optionally mark the task as failed in the queue
			w.taskQueue.MarkTaskResult(ctx, payload.TenantID, payload.UserID, payload.AgentID, task.ID, false, processingErr.Error())
		}
		return processingErr
	}

	log.Printf("Worker %d task %s completed successfully in %v", workerID, task.ID, duration)
	w.taskQueue.MarkTaskResult(ctx, payload.TenantID, payload.UserID, payload.AgentID, task.ID, true, "success")
	return nil
}

// scopeBusyBackoff is how long a worker waits before requeueing a task whose scope
// another worker is processing.
const scopeBusyBackoff = 200 * time.Millisecond

// lockScope takes the scope's processing lock. It returns a release function and
// true, or false if another worker holds it. Without the lock (Redis error) it
// proceeds, as before.
func (w *Worker) lockScope(task *models.CognitiveChainCheckTask) (func(), bool) {
	key := "stm_scope_lock:v1:" + GenerateScopedQueueName(task.TenantID, task.UserID, task.AgentID)
	holder := uuid.New().String()
	acquired, err := w.redis.SetNX(key, holder, TaskTimeout)
	if err != nil {
		log.Printf("WARNING: scope lock unavailable for %s, proceeding: %v", task.UserID, err)
		return func() {}, true
	}
	if !acquired {
		return nil, false
	}
	return func() {
		if err := w.redis.DeleteIfEquals(key, holder); err != nil {
			log.Printf("WARNING: failed to release scope lock for %s: %v", task.UserID, err)
		}
	}, true
}

// reEnqueueTask puts the failed task back into the queue system
func (w *Worker) reEnqueueTask(_ context.Context, scopedQueueName string, task *TaskEnvelope, payload *models.CognitiveChainCheckTask) error {
	// Update payload in task envelope
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload for retry: %w", err)
	}
	task.Payload = payloadBytes

	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task envelope for retry: %w", err)
	}

	// 1. Push to scoped queue (Right Push to put it at the back, giving time for transient errors to clear?)
	// Original logic uses LPUSH (LIFO/Stack for hot path?).
	// If we use RPush, it goes to the "oldest" end.
	// If we want immediate retry, LPUSH. If we want backoff-like behavior in a busy queue, RPUSH.
	// Given "Hot Path" architecture, usually we want newest first.
	// If we LPUSH, it retries immediately. This might be bad if the error is persistent (e.g. API down).
	// But we don't have a delayed queue.
	// Let's stick to LPUSH to maintain the "Stack" semantics of the hot path, but maybe sleep briefly in the worker loop before next pop?
	// No, the worker loop handles popping.
	// Let's use LPUSH to be consistent with `server.go` producer logic.
	if err := w.redis.LPush(scopedQueueName, string(taskJSON)); err != nil {
		return fmt.Errorf("failed to push to scoped queue: %w", err)
	}

	// 2. Notify global queue
	if err := w.redis.LPush(GlobalWorkQueueName, scopedQueueName); err != nil {
		return fmt.Errorf("failed to push to global queue: %w", err)
	}

	return nil
}

// processCognitiveChainCheck performs the core logic of analyzing the cognitive chain
func (w *Worker) processCognitiveChainCheck(ctx context.Context, task *models.CognitiveChainCheckTask) error {

	// Use the shared key generation function from stm_cache.go
	key := GenerateSTMKey(task.TenantID, task.UserID, task.AgentID)

	// Check total STM size before fetching — if a flush will be triggered we must
	// fetch all events, not just the most recent 10, to avoid silently losing older ones.
	maxSTMEvents := parseIntEnv("STM_MAX_EVENTS_BEFORE_FLUSH", 20)
	totalSTMCount, _ := w.redis.LLen(key)

	fetchCount := int64(10)
	if totalSTMCount >= int64(maxSTMEvents) {
		fetchCount = totalSTMCount
	}

	// 1. Get events from STM — we need enough to find two different user messages.
	//    LPUSH means index 0 is newest. A StoreInteraction pushes user then agent,
	//    so events alternate: [agent_new, user_new, agent_prev, user_prev, ...]
	//    We need to compare the newest user message against the previous user message
	//    to detect topic changes across interaction boundaries (not within a single turn).
	eventStrings, err := w.redis.LRange(key, 0, fetchCount-1)
	if err != nil {
		return fmt.Errorf("failed to get recent events from STM: %w", err)
	}

	// Unmarshal all events and find the two most recent user messages
	type parsedEvent struct {
		event models.CognitiveEvent
		index int
	}
	var userMessages []parsedEvent
	var allEvents []models.CognitiveEvent
	var allRaw []string // allRaw[i] is the STM entry allEvents[i] was parsed from

	for i, eventStr := range eventStrings {
		var stmEvt STMEvent
		if err := json.Unmarshal([]byte(eventStr), &stmEvt); err != nil {
			log.Printf("WARNING: Failed to unmarshal event at index %d: %v", i, err)
			continue
		}

		evt := cognitiveEventFromSTM(stmEvt)

		allEvents = append(allEvents, evt)
		allRaw = append(allRaw, eventStr)
		if evt.Role == "user" {
			userMessages = append(userMessages, parsedEvent{event: evt, index: i})
		}
	}

	// Force chain formation if STM has accumulated too many events.
	// This ensures chains form even for long single-topic conversations
	// (e.g., automation logs, extended workflow design sessions).
	// NOTE: this must run BEFORE the user-message-count guard — agent-heavy
	// streams (autonomous agents emitting thought/action events) may have few
	// or no user-role events in the window, but still need periodic flushing.
	if totalSTMCount >= int64(maxSTMEvents) {
		log.Printf("STM event count (%d) exceeds threshold (%d) for user %s. Forcing chain formation.", totalSTMCount, maxSTMEvents, task.UserID)
		// Treat the oldest half of events as a completed chain
		splitPoint := len(allEvents) / 2
		oldChainEvents := make([]models.CognitiveEvent, 0)
		for i := len(allEvents) - 1; i >= splitPoint; i-- {
			oldChainEvents = append(oldChainEvents, allEvents[i])
		}
		if err := w.stmStore.ProcessMTMFormation(ctx, task.TenantID, task.UserID, task.AgentID, oldChainEvents); err != nil {
			return fmt.Errorf("forced MTM formation failed: %w", err)
		}
		w.removeFormedEntries(key, allRaw[splitPoint:])
		log.Printf("Forced chain formation complete for user %s. Kept %d recent events.", task.UserID, splitPoint)
		return nil
	}

	if len(userMessages) < 2 {
		log.Printf("Not enough user messages for chain analysis for user %s (found %d). Assuming new chain.", task.UserID, len(userMessages))
		return nil // Not enough data to compare topics
	}

	// The newest user message vs the previous user message
	newestUser := userMessages[0].event
	previousUser := userMessages[1].event

	// If both events share the same workflow_id or execution_id they belong to the
	// same execution run — skip topic analysis and keep them in one chain.
	for _, metaKey := range []string{"workflow_id", "execution_id"} {
		newVal, newOk := newestUser.Metadata[metaKey]
		prevVal, prevOk := previousUser.Metadata[metaKey]
		if newOk && prevOk && newVal != "" && newVal == prevVal {
			log.Printf("Events share %s=%s for user %s — skipping chain-break check.", metaKey, newVal, task.UserID)
			return nil
		}
	}

	log.Printf("Comparing user messages for topic detection: newest='%s' vs previous='%s'",
		truncateLog(newestUser.Content, 60), truncateLog(previousUser.Content, 60))

	// 3. Get embeddings for the two user messages
	// Guard: skip embedding if either message has empty content (can happen
	// when the REST gateway maps events without a content field).
	if newestUser.Content == "" || previousUser.Content == "" {
		log.Printf("Skipping chain analysis for user %s: one or both user messages have empty content (newest=%d chars, previous=%d chars)",
			task.UserID, len(newestUser.Content), len(previousUser.Content))
		return nil
	}
	embeddingNew, err := w.stmStore.CreateEmbedding(ctx, newestUser.Content)
	if err != nil {
		return fmt.Errorf("failed to create embedding for newest user message: %w", err)
	}
	embeddingPrev, err := w.stmStore.CreateEmbedding(ctx, previousUser.Content)
	if err != nil {
		return fmt.Errorf("failed to create embedding for previous user message: %w", err)
	}

	// 4. Calculate cosine similarity between user messages across turns
	chainSimHigh := parseFloatEnv("CHAIN_SIM_HIGH", 0.72)
	chainSimLow := parseFloatEnv("CHAIN_SIM_LOW", 0.52)

	similarity, err := cosineSimilarity(embeddingNew.Vector, embeddingPrev.Vector)
	if err != nil {
		return fmt.Errorf("failed to calculate cosine similarity: %w", err)
	}

	log.Printf("Topic similarity between user messages: %.4f (low=%.2f, high=%.2f)", similarity, chainSimLow, chainSimHigh)

	// 5. Decide if the chain continues
	chainBreak := false
	if similarity >= chainSimHigh {
		// Chain continues, do nothing
		log.Printf("Chain continues for user %s (similarity %.4f >= %.2f).", task.UserID, similarity, chainSimHigh)
		return nil
	} else if similarity < chainSimLow {
		// Chain breaks
		log.Printf("Chain break detected for user %s (similarity %.4f < %.2f).", task.UserID, similarity, chainSimLow)
		chainBreak = true
	} else {
		// Gray area, requires LLM to check for continuity
		log.Printf("Cosine similarity is in the gray area (%.4f). Using LLM to check for continuity.", similarity)

		llmContinues, err := w.stmStore.analyzeTopicContinuity(ctx, task.UserID, previousUser.Content, newestUser.Content)
		if err != nil {
			return fmt.Errorf("failed to analyze topic continuity with LLM: %w", err)
		}

		if llmContinues {
			log.Printf("LLM determined chain continues for user %s.", task.UserID)
			return nil // Chain continues
		}

		// Chain breaks
		log.Printf("LLM determined chain break for user %s.", task.UserID)
		chainBreak = true
	}

	if chainBreak {
		// 6. If the chain breaks, partition events into new topic vs old chain.
		//    The newest user message (index userMessages[0].index) starts the new topic.
		//    Everything from the previous user message backwards is the old chain.
		//    Events between index 0 and the newest user message index might include
		//    an agent response for the new topic (if it was LPUSHed before the worker ran).
		newTopicBoundary := userMessages[0].index // index of newest user message in allEvents
		keepCount := newTopicBoundary + 1         // how many events to keep (new topic)

		// Old chain = everything after the new topic boundary, in chronological order
		oldChainStrings, err := w.redis.LRange(key, int64(keepCount), -1)
		if err != nil {
			return fmt.Errorf("failed to get old chain from STM: %w", err)
		}

		var oldChainEvents []models.CognitiveEvent
		// Iterate in reverse to maintain chronological order (LPUSH makes newest first)
		for i := len(oldChainStrings) - 1; i >= 0; i-- {
			var stmEvt STMEvent
			if err := json.Unmarshal([]byte(oldChainStrings[i]), &stmEvt); err != nil {
				log.Printf("WARNING: Failed to unmarshal event in old chain: %v", err)
				continue
			}
			oldChainEvents = append(oldChainEvents, cognitiveEventFromSTM(stmEvt))
		}

		// 7. Trigger MTM formation
		log.Printf("Processing MTM formation for user %s with %d events (keeping %d new-topic events).",
			task.UserID, len(oldChainEvents), keepCount)

		// 8. Implement "Save-Then-Trim" logic
		if err := w.stmStore.ProcessMTMFormation(ctx, task.TenantID, task.UserID, task.AgentID, oldChainEvents); err != nil {
			// MTM FAILED! Return the error. DO NOT TRIM.
			return fmt.Errorf("MTM formation failed: %w", err)
		} else {
			// MTM SUCCEEDED! Remove the formed events, keeping only the new topic.
			w.removeFormedEntries(key, oldChainStrings)
		}
	}

	return nil
}

// STMIdleFlushAfter is how long a scope may be quiet before its remaining STM events
// are summarised (STM_IDLE_FLUSH_MINUTES, default 30). It must be shorter than
// STM_CACHE_TTL, or a quiet scope's events expire unsummarised.
func STMIdleFlushAfter() time.Duration {
	return time.Duration(parseIntEnv("STM_IDLE_FLUSH_MINUTES", 30)) * time.Minute
}

// RunIdleSweeper queues an idle flush for every scope that has been quiet for
// STMIdleFlushAfter, until ctx is done. Run one per process; flushes are tasks on the
// scope's own queue, processed like any other.
func (w *Worker) RunIdleSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := w.SweepIdle(ctx, time.Now()); err != nil {
				log.Printf("WARNING: STM idle sweep failed: %v", err)
			} else if n > 0 {
				log.Printf("INFO: STM idle sweep queued %d flush(es)", n)
			}
		}
	}
}

// SweepIdle queues an idle flush for scopes quiet since before now-STMIdleFlushAfter.
// Returns how many it queued.
func (w *Worker) SweepIdle(ctx context.Context, now time.Time) (int, error) {
	scopes, err := w.redis.HGetAll(STMActivityKey)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-STMIdleFlushAfter()).Unix()
	queued := 0
	for field, last := range scopes {
		seconds, err := strconv.ParseInt(last, 10, 64)
		if err != nil || seconds > cutoff {
			continue
		}
		tenantID, userID, agentID, ok := ParseSTMScopeField(field)
		if !ok {
			_ = w.redis.HDel(STMActivityKey, field)
			continue
		}
		if err := enqueueTask(w.redis, TaskTypeIdleFlush, tenantID, userID, agentID); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}

// processIdleFlush summarises everything left in a quiet scope's STM, so it is in
// mid-term memory before STM expires.
func (w *Worker) processIdleFlush(ctx context.Context, task *models.CognitiveChainCheckTask) error {
	key := GenerateSTMKey(task.TenantID, task.UserID, task.AgentID)
	field := STMScopeField(task.TenantID, task.UserID, task.AgentID)
	raw, err := w.redis.LRange(key, 0, -1)
	if err != nil {
		return fmt.Errorf("failed to read STM for idle flush: %w", err)
	}
	if len(raw) == 0 {
		return w.redis.HDel(STMActivityKey, field)
	}
	events := make([]models.CognitiveEvent, 0, len(raw))
	newest := time.Time{}
	for i := len(raw) - 1; i >= 0; i-- { // oldest first
		var stmEvt STMEvent
		if err := json.Unmarshal([]byte(raw[i]), &stmEvt); err != nil {
			continue
		}
		if stmEvt.Timestamp.After(newest) {
			newest = stmEvt.Timestamp
		}
		events = append(events, cognitiveEventFromSTM(stmEvt))
	}
	if time.Since(newest) < STMIdleFlushAfter() {
		return nil // it became active again: the normal path handles it
	}
	if len(events) > 0 {
		if err := w.stmStore.ProcessMTMFormation(ctx, task.TenantID, task.UserID, task.AgentID, events); err != nil {
			return fmt.Errorf("idle MTM formation failed: %w", err)
		}
	}
	w.removeFormedEntries(key, raw)
	if remaining, err := w.redis.LLen(key); err == nil && remaining == 0 {
		return w.redis.HDel(STMActivityKey, field)
	}
	return nil
}

// removeFormedEntries removes the STM entries a chain was formed from. It removes
// them by value rather than trimming by index: the list changes while a formation
// runs (new events are pushed, a deleted session's entries are removed), so the
// positions read before the formation no longer identify the same entries.
func (w *Worker) removeFormedEntries(key string, raws []string) {
	for _, raw := range raws {
		// Formed entries are the oldest, so search from the tail.
		if err := w.redis.LRem(key, -1, raw); err != nil {
			log.Printf("WARNING: Failed to remove a formed event from STM %s: %v", key, err)
		}
	}
}

// cognitiveEventFromSTM converts a cached STM event into the cognitive event that
// chain formation persists. It keeps the event's session and, when the event was
// stored durably on arrival, the ID of that cognitive_events document.
func cognitiveEventFromSTM(e STMEvent) models.CognitiveEvent {
	meta := make(map[string]interface{}, len(e.Metadata))
	for k, v := range e.Metadata {
		meta[k] = v
	}
	sessionID := e.SessionID
	if sessionID == "" {
		// Entries cached before sessionId existed carry the session as ChainID.
		sessionID = e.ChainID
	}
	evt := models.CognitiveEvent{
		SessionID:    sessionID,
		Role:         e.Role,
		Type:         e.Type,
		Content:      e.Content,
		BlobURI:      e.BlobURI,
		BlobMimeType: e.BlobMimeType,
		CreatedAt:    e.Timestamp,
		Metadata:     meta,
	}
	if e.EventID != "" {
		if id, err := primitive.ObjectIDFromHex(e.EventID); err == nil {
			evt.ID = id
		}
	}
	return evt
}

// truncateLog truncates a string for log output
func truncateLog(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
