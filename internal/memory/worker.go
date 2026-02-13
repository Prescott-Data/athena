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

	if len(result) < 2 {
		return fmt.Errorf("invalid BRPOP result from global work queue")
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
		// Optionally mark the task as failed in the queue
		w.taskQueue.MarkTaskResult(ctx, payload.TenantID, payload.UserID, payload.AgentID, task.ID, false, processingErr.Error())
		return processingErr
	}

	log.Printf("Worker %d task %s completed successfully in %v", workerID, task.ID, duration)
	w.taskQueue.MarkTaskResult(ctx, payload.TenantID, payload.UserID, payload.AgentID, task.ID, true, "success")
	return nil
}

// processCognitiveChainCheck performs the core logic of analyzing the cognitive chain
func (w *Worker) processCognitiveChainCheck(ctx context.Context, task *models.CognitiveChainCheckTask) error {

	// Use the shared key generation function from stm_cache.go
	key := GenerateSTMKey(task.TenantID, task.UserID, task.AgentID)

	// 1. Get the last two events from the user's STM
	eventStrings, err := w.redis.LRange(key, 0, 1)
	if err != nil {
		return fmt.Errorf("failed to get recent events from STM: %w", err)
	}

	if len(eventStrings) < 2 {
		log.Printf("Not enough events for chain analysis for user %s. Assuming new chain.", task.UserID)
		return nil // Not an error, just not enough data to process
	}

	// 2. Unmarshal events
	var event1, event2 models.CognitiveEvent
	if err := json.Unmarshal([]byte(eventStrings[0]), &event1); err != nil {
		return fmt.Errorf("failed to unmarshal event 1: %w", err)
	}
	if err := json.Unmarshal([]byte(eventStrings[1]), &event2); err != nil {
		return fmt.Errorf("failed to unmarshal event 2: %w", err)
	}

	// 3. Get embeddings for the content of the last two events
	embedding1, err := w.stmStore.CreateEmbedding(ctx, event1.Content)
	if err != nil {
		return fmt.Errorf("failed to create embedding for event 1: %w", err)
	}
	embedding2, err := w.stmStore.CreateEmbedding(ctx, event2.Content)
	if err != nil {
		return fmt.Errorf("failed to create embedding for event 2: %w", err)
	}

	// 4. Calculate cosine similarity
	// --- THIS IS THE FIX ---
	// Read variables inside the function
	chainSimHigh := parseFloatEnv("CHAIN_SIM_HIGH", 0.72)
	chainSimLow := parseFloatEnv("CHAIN_SIM_LOW", 0.52)
	// --- END FIX ---

	similarity, err := cosineSimilarity(embedding1.Vector, embedding2.Vector)
	if err != nil {
		return fmt.Errorf("failed to calculate cosine similarity: %w", err)
	}

	// 5. Decide if the chain continues
	chainBreak := false
	if similarity >= chainSimHigh {
		// Chain continues, do nothing
		log.Printf("Chain continues for user %s.", task.UserID)
		return nil
	} else if similarity < chainSimLow {
		// Chain breaks
		log.Printf("Chain break detected for user %s.", task.UserID)
		chainBreak = true
	} else {
		// Gray area, requires LLM to check for continuity
		log.Printf("Cosine similarity is in the gray area (%f). Using LLM to check for continuity.", similarity)

		llmContinues, err := w.stmStore.analyzeTopicContinuity(ctx, task.UserID, event1.Content, event2.Content)
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
		// 6. If the chain breaks, get the entire old chain (all but the most recent event)
		oldChainStrings, err := w.redis.LRange(key, 1, -1)
		if err != nil {
			return fmt.Errorf("failed to get old chain from STM: %w", err)
		}

		var oldChainEvents []models.CognitiveEvent
		// Iterate in reverse to maintain chronological order (LPUSH makes newest first)
		for i := len(oldChainStrings) - 1; i >= 0; i-- {
			var event models.CognitiveEvent
			if err := json.Unmarshal([]byte(oldChainStrings[i]), &event); err != nil {
				log.Printf("WARNING: Failed to unmarshal event in old chain: %v", err)
				continue
			}
			oldChainEvents = append(oldChainEvents, event)
		}

		// 7. Trigger MTM formation
		log.Printf("Processing MTM formation for user %s with %d events.", task.UserID, len(oldChainEvents))

		// 8. Implement "Save-Then-Trim" logic
		if err := w.stmStore.ProcessMTMFormation(ctx, task.TenantID, task.UserID, task.AgentID, oldChainEvents); err != nil {
			// MTM FAILED! Return the error. DO NOT TRIM.
			return fmt.Errorf("MTM formation failed: %w", err)
		} else {
			// MTM SUCCEEDED! It is now safe to trim the STM.
			if err := w.redis.LTrim(key, 0, 0); err != nil {
				// The MTM save worked, but the trim failed.
				log.Printf("WARNING: MTM save succeeded but failed to trim STM for user %s: %v", task.UserID, err)
			}
		}
	}

	return nil
}

