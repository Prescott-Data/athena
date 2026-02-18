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
)

// TaskQueuer defines the interface for task queue operations.
type TaskQueuer interface {
	GetQueueStats(ctx context.Context) (map[string]interface{}, error)
	MarkTaskResult(ctx context.Context, tenantID, userID, agentID, taskID string, success bool, message string) error
}

const (
	// TaskResultsPrefix is the Redis key prefix for task results (legacy)
	TaskResultsPrefix = "task_results"

	// TaskTypeCognitiveChainCheck is the task type for cognitive chain analysis
	TaskTypeCognitiveChainCheck = "cognitive_chain_check"
)

// GenerateScopedQueueName creates a tenant-scoped queue name
func GenerateScopedQueueName(tenantID, userID, agentID string) string {
	return fmt.Sprintf("memory_processing_queue:v1:%s:%s:%s", tenantID, userID, agentID)
}

// generateScopedTaskResultKey creates a tenant-scoped task result key
func generateScopedTaskResultKey(tenantID, userID, agentID, taskID string) string {
	return fmt.Sprintf("task_results:v1:%s:%s:%s:%s", tenantID, userID, agentID, taskID)
}

var (
	// GlobalWorkQueueName is the name of the Redis queue that holds the names of the scoped work queues.
	GlobalWorkQueueName = "cognitive_work_queue"
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

// GetQueueStats returns statistics about the task queue
func (tq *TaskQueue) GetQueueStats(ctx context.Context) (map[string]interface{}, error) {
	queueLength, err := tq.redis.LLen(GlobalWorkQueueName)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue length: %w", err)
	}

	return map[string]interface{}{
			"queue_name":   GlobalWorkQueueName,
			"queue_length": queueLength,
			"task_timeout": TaskTimeout.String(),
		},
		nil
}

// MarkTaskResult stores the result of a processed task (for debugging/monitoring)
func (tq *TaskQueue) MarkTaskResult(ctx context.Context, tenantID, userID, agentID, taskID string, success bool, message string) error {
	result := map[string]interface{}{
		"success":      success,
		"message":      message,
		"processed_at": time.Now(),
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal task result: %w", err)
	}

	resultKey := generateScopedTaskResultKey(tenantID, userID, agentID, taskID)

	// Store result with 1 hour expiration
	if err := tq.redis.SetEX(resultKey, string(resultJSON), time.Hour); err != nil {
		return fmt.Errorf("failed to store task result: %w", err)
	}

	return nil
}
