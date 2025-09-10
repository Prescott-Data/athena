package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/dromos-org/memory-os/internal/cache"
	"github.com/dromos-org/memory-os/internal/models"

	"github.com/google/uuid"
)

const (
	// MemoryProcessingQueue is the Redis queue key for memory processing tasks
	MemoryProcessingQueue = "memory_processing_queue"
	// TaskResultsPrefix is the Redis key prefix for task results
	TaskResultsPrefix = "task_results"
	
	// TaskTypeMemoryFormation is the task type for memory formation
	TaskTypeMemoryFormation = "memory_formation"
)

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

// EnqueueMemoryTask adds a memory processing task to the background queue
func (tq *TaskQueue) EnqueueMemoryTask(ctx context.Context, userID, userMessage, agentResponse string, metadata map[string]interface{}) error {
	start := time.Now()

	task := models.MemoryProcessingTask{
		ID:            uuid.New().String(),
		Type:          TaskTypeMemoryFormation,
		UserID:        userID,
		UserMessage:   userMessage,
		AgentResponse: agentResponse,
		Timestamp:     time.Now(),
		Metadata:      metadata,
		CreatedAt:     time.Now(),
	}

	// Serialize task
	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal memory task: %w", err)
	}

	// Push to Redis queue (LPUSH for FIFO when using BRPOP)
	if err := tq.redis.LPush(TaskQueueName, string(taskJSON)); err != nil {
		return fmt.Errorf("failed to enqueue memory task: %w", err)
	}

	duration := time.Since(start)
	log.Printf("INFO: Memory task enqueued - UserID: %s, TaskID: %s, Duration: %v", userID, task.ID, duration)

	return nil
}

// DequeueTask blocks waiting for the next task from the queue
func (tq *TaskQueue) DequeueTask(ctx context.Context) (*models.MemoryProcessingTask, error) {
	// Use BRPOP to block and wait for tasks (timeout of 1 second)
	result, err := tq.redis.BRPop(1*time.Second, TaskQueueName)
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue task: %w", err)
	}

	if len(result) < 2 {
		return nil, fmt.Errorf("invalid BRPOP result format")
	}

	// result[0] is queue name, result[1] is the task JSON
	taskJSON := result[1]

	var task models.MemoryProcessingTask
	if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}

	return &task, nil
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
	}, nil
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