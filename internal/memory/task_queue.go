package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/models"

	"github.com/google/uuid"
)

// TaskQueuer defines the interface for task queue operations.
type TaskQueuer interface {
	EnqueueCognitiveCheckTask(ctx context.Context, tenantID, userID, agentID string) error
	DequeueTask(ctx context.Context) (*TaskEnvelope, error)
	GetQueueStats(ctx context.Context) (map[string]interface{}, error)
	MarkTaskResult(ctx context.Context, taskID string, success bool, message string) error
}

const (
	// MemoryProcessingQueue is the Redis queue key for memory processing tasks (legacy)
	MemoryProcessingQueue = "memory_processing_queue"
	// TaskResultsPrefix is the Redis key prefix for task results (legacy)
	TaskResultsPrefix = "task_results"

	// TaskTypeCognitiveChainCheck is the task type for cognitive chain analysis
	TaskTypeCognitiveChainCheck = "cognitive_chain_check"
)

// generateScopedQueueName creates a tenant-scoped queue name
func generateScopedQueueName(tenantID, userID, agentID string) string {
	return fmt.Sprintf("memory_processing_queue:v1:%s:%s:%s", tenantID, userID, agentID)
}

// generateScopedTaskResultKey creates a tenant-scoped task result key
func generateScopedTaskResultKey(tenantID, userID, agentID, taskID string) string {
	return fmt.Sprintf("task_results:v1:%s:%s:%s:%s", tenantID, userID, agentID, taskID)
}

var (
	// TaskQueueName is the name of the Redis queue for memory tasks
	TaskQueueName = "memory_processing_queue"
	// TaskTimeout is the timeout for processing individual tasks
	TaskTimeout = 300 * time.Second // 5 minutes
)

func init() {
	// Parse task timeout from environment
	if timeoutStr := os.Getenv("STM_TASK_TIMEOUT"); timeoutStr != "" {
		if parsed, err := strconv.Atoi(timeoutStr); err == nil {
			TaskTimeout = time.Duration(parsed) * time.Second
		} else {
			log.Printf("WARN: Invalid STM_TASK_TIMEOUT value: %s, using default: %v", timeoutStr, TaskTimeout)
		}
	}
}

// TaskEnvelope is a generic wrapper for all task types.
type TaskEnvelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	EnqueuedAt time.Time       `json:"enqueued_at"`
}

// TaskQueue handles background task processing for memory formation
type TaskQueue struct {
	redis cache.Interface
}

// NewTaskQueue creates a new task queue instance
func NewTaskQueue(redisClient cache.Interface) *TaskQueue {
	return &TaskQueue{
		redis: redisClient,
	}
}

// EnqueueCognitiveCheckTask enqueues a lightweight trigger for the worker to check for a chain break.
func (tq *TaskQueue) EnqueueCognitiveCheckTask(ctx context.Context, tenantID, userID, agentID string) error {
	start := time.Now()

	// Create the specific task payload
	taskPayload := models.CognitiveChainCheckTask{
		ID:        uuid.New().String(),
		Type:      TaskTypeCognitiveChainCheck,
		TenantID:  tenantID,
		UserID:    userID,
		AgentID:   agentID,
		Timestamp: time.Now(),
	}

	payloadJSON, err := json.Marshal(taskPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal cognitive check task payload: %w", err)
	}

	// Wrap the payload in a generic TaskEnvelope
	envelope := TaskEnvelope{
		ID:         taskPayload.ID,
		Type:       taskPayload.Type,
		Payload:    payloadJSON,
		EnqueuedAt: time.Now(),
	}

	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal task envelope: %w", err)
	}

	// Push to Redis queue
	if err := tq.redis.LPush(TaskQueueName, string(envelopeJSON)); err != nil {
		return fmt.Errorf("failed to enqueue cognitive check task: %w", err)
	}

	duration := time.Since(start)
	log.Printf("INFO: Cognitive check task enqueued - UserID: %s, TaskID: %s, Duration: %v", userID, envelope.ID, duration)

	return nil
}

// DequeueTask blocks waiting for the next task from the queue.
func (tq *TaskQueue) DequeueTask(ctx context.Context) (*TaskEnvelope, error) {
	// Use BRPOP to block and wait for tasks (timeout of 1 second)
	result, err := tq.redis.BRPop(1*time.Second, TaskQueueName)
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue task: %w", err)
	}

	if len(result) < 2 {
		return nil, fmt.Errorf("invalid BRPOP result format")
	}

	// result[0] is queue name, result[1] is the task JSON
	envelopeJSON := result[1]

	var envelope TaskEnvelope
	if err := json.Unmarshal([]byte(envelopeJSON), &envelope); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task envelope: %w", err)
	}

	return &envelope, nil
}

// GetQueueStats returns statistics about the task queue
func (tq *TaskQueue) GetQueueStats(ctx context.Context) (map[string]interface{}, error) {
	queueLength, err := tq.redis.LLen(TaskQueueName)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue length: %w", err)
	}

	return map[string]interface{}{
		"queue_name":   TaskQueueName,
		"queue_length": queueLength,
		"task_timeout": TaskTimeout.String(),
	},
	nil
}

// MarkTaskResult stores the result of a processed task (for debugging/monitoring)
func (tq *TaskQueue) MarkTaskResult(ctx context.Context, taskID string, success bool, message string) error {
	result := map[string]interface{}{
		"success":      success,
		"message":      message,
		"processed_at": time.Now(),
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal task result: %w", err)
	}

	resultKey := fmt.Sprintf("%s:%s", TaskResultsPrefix, taskID)

	// Store result with 1 hour expiration
	if err := tq.redis.SetEX(resultKey, string(resultJSON), time.Hour); err != nil {
		return fmt.Errorf("failed to store task result: %w", err)
	}

	return nil
}