package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/models"
	"github.com/redis/go-redis/v9"
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
		} else {
			log.Printf("Worker %d processing task %s (UserID: %s)", workerID, task.ID, payload.UserID)
			processingErr = w.processCognitiveChainCheck(ctx, &payload)
		}
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

	for i, eventStr := range eventStrings {
		var stmEvt STMEvent
		if err := json.Unmarshal([]byte(eventStr), &stmEvt); err != nil {
			log.Printf("WARNING: Failed to unmarshal event at index %d: %v", i, err)
			continue
		}

		meta := make(map[string]interface{})
		for k, v := range stmEvt.Metadata {
			meta[k] = v
		}

		evt := models.CognitiveEvent{
			Role:      stmEvt.Role,
			Type:      stmEvt.Type,
			Content:   stmEvt.Content,
			CreatedAt: stmEvt.Timestamp,
			Metadata:  meta,
		}

		allEvents = append(allEvents, evt)
		if evt.Role == "user" {
			userMessages = append(userMessages, parsedEvent{event: evt, index: i})
		}
	}

	if len(userMessages) < 2 {
		log.Printf("Not enough user messages for chain analysis for user %s (found %d). Assuming new chain.", task.UserID, len(userMessages))
		return nil // Not enough data to compare topics
	}

	// Force chain formation if STM has accumulated too many events.
	// This ensures chains form even for long single-topic conversations
	// (e.g., automation logs, extended workflow design sessions).
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
		if err := w.redis.LTrim(key, 0, int64(splitPoint-1)); err != nil {
			log.Printf("WARNING: Failed to trim STM after forced formation: %v", err)
		}
		log.Printf("Forced chain formation complete for user %s. Kept %d recent events.", task.UserID, splitPoint)
		return nil
	}

	// The newest user message vs the previous user message
	newestUser := userMessages[0].event
	previousUser := userMessages[1].event

	log.Printf("Comparing user messages for topic detection: newest='%s' vs previous='%s'",
		truncateLog(newestUser.Content, 60), truncateLog(previousUser.Content, 60))

	// 3. Get embeddings for the two user messages
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
			meta := make(map[string]interface{})
			for k, v := range stmEvt.Metadata {
				meta[k] = v
			}
			event := models.CognitiveEvent{
				Role:      stmEvt.Role,
				Type:      stmEvt.Type,
				Content:   stmEvt.Content,
				CreatedAt: stmEvt.Timestamp,
				Metadata:  meta,
			}
			oldChainEvents = append(oldChainEvents, event)
		}

		// 7. Trigger MTM formation
		log.Printf("Processing MTM formation for user %s with %d events (keeping %d new-topic events).",
			task.UserID, len(oldChainEvents), keepCount)

		// 8. Implement "Save-Then-Trim" logic
		if err := w.stmStore.ProcessMTMFormation(ctx, task.TenantID, task.UserID, task.AgentID, oldChainEvents); err != nil {
			// MTM FAILED! Return the error. DO NOT TRIM.
			return fmt.Errorf("MTM formation failed: %w", err)
		} else {
			// MTM SUCCEEDED! Trim STM to keep only the new topic events.
			if err := w.redis.LTrim(key, 0, int64(keepCount-1)); err != nil {
				// The MTM save worked, but the trim failed.
				log.Printf("WARNING: MTM save succeeded but failed to trim STM for user %s: %v", task.UserID, err)
			}
		}
	}

	return nil
}

// truncateLog truncates a string for log output
func truncateLog(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
