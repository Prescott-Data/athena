package memory

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"bitbucket.org/dromos/memory-os/internal/cache"
	"bitbucket.org/dromos/memory-os/internal/database"
	"bitbucket.org/dromos/memory-os/internal/models"

	"go.mongodb.org/mongo-driver/mongo"
)

// Worker processes background memory formation tasks using goroutines
type Worker struct {
	taskQueue   *TaskQueue
	stmStore    *STMStore
	redis       cache.Interface
	db          *mongo.Database
	quit        chan struct{}
	wg          sync.WaitGroup
	workerCount int
}

// WorkerConfig holds configuration for the memory worker
type WorkerConfig struct {
	WorkerCount int
	Redis       cache.Interface
	Database    *mongo.Database
}

// NewWorker creates a new memory worker instance
func NewWorker(config WorkerConfig) *Worker {
	taskQueue := NewTaskQueue(config.Redis)
	stmStore := NewSTMStore(config.Database, config.Redis)

	workerCount := config.WorkerCount
	if workerCount <= 0 {
		workerCount = 1 // Default to 1 worker
	}

	return &Worker{
		taskQueue:   taskQueue,
		stmStore:    stmStore,
		redis:       config.Redis,
		db:          config.Database,
		quit:        make(chan struct{}),
		workerCount: workerCount,
	}
}

// Start begins processing background tasks with goroutines
func (mw *Worker) Start(ctx context.Context) {
	log.Printf("INFO: Starting %d memory worker goroutines", mw.workerCount)

	// Start worker goroutines
	for i := 0; i < mw.workerCount; i++ {
		mw.wg.Add(1)
		go mw.workerLoop(ctx, i)
	}

	// Set up graceful shutdown
	go mw.handleShutdown()

	log.Println("INFO: Memory worker goroutines started successfully")
}

// Stop gracefully stops all worker goroutines
func (mw *Worker) Stop() {
	log.Println("INFO: Stopping memory worker goroutines...")
	close(mw.quit)
	mw.wg.Wait()
	log.Println("INFO: All memory worker goroutines stopped")
}

// workerLoop is the main processing loop for a single worker goroutine
func (mw *Worker) workerLoop(ctx context.Context, workerID int) {
	defer mw.wg.Done()

	log.Printf("INFO: Memory worker goroutine %d started", workerID)

	for {
		select {
		case <-mw.quit:
			log.Printf("INFO: Memory worker goroutine %d shutting down", workerID)
			return
		case <-ctx.Done():
			log.Printf("INFO: Memory worker goroutine %d context cancelled", workerID)
			return
		default:
			// Try to process a task
			if err := mw.processNextTask(ctx, workerID); err != nil {
				// Only log errors that aren't timeouts or cache misses (empty queue)
				if err.Error() != "redis: nil" && err.Error() != "failed to dequeue task: cache miss" {
					log.Printf("WARN: Worker %d task processing error: %v", workerID, err)
				}
				// Brief pause to prevent busy-waiting
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

// processNextTask dequeues and processes a single task
func (mw *Worker) processNextTask(ctx context.Context, workerID int) error {
	// Create timeout context for task processing
	taskCtx, cancel := context.WithTimeout(ctx, TaskTimeout)
	defer cancel()

	// Dequeue next task
	task, err := mw.taskQueue.DequeueTask(taskCtx)
	if err != nil {
		return err // This includes timeout/empty queue errors
	}

	start := time.Now()
	log.Printf("INFO: Worker %d processing task %s (UserID: %s)", workerID, task.ID, task.UserID)

	// Process the task based on type
	var processErr error
	switch task.Type {
	case TaskTypeMemoryFormation:
		processErr = mw.processMemoryFormationTask(taskCtx, task)
	default:
		processErr = fmt.Errorf("unknown task type: %s", task.Type)
	}

	// Log result
	duration := time.Since(start)
	if processErr != nil {
		log.Printf("ERROR: Worker %d task %s failed after %v: %v", workerID, task.ID, duration, processErr)
		_ = mw.taskQueue.MarkTaskResult(taskCtx, task.ID, false, processErr.Error())
	} else {
		log.Printf("INFO: Worker %d task %s completed successfully in %v", workerID, task.ID, duration)
		_ = mw.taskQueue.MarkTaskResult(taskCtx, task.ID, true, "Task completed successfully")
	}

	return processErr
}

// processMemoryFormationTask handles the 3-step memory formation pipeline
func (mw *Worker) processMemoryFormationTask(ctx context.Context, task *models.MemoryProcessingTask) error {
	// Step A: Dialogue Chain Analysis
	chainID, err := mw.stmStore.DetermineDialogueChain(ctx, task.TenantID, task.UserID, task.AgentID, task.UserMessage, task.AgentResponse)
	if err != nil {
		return fmt.Errorf("dialogue chain analysis failed: %w", err)
	}

	// Step B: Vector Embedding Creation
	embedding, err := mw.stmStore.CreateEmbedding(ctx, task.UserMessage, task.AgentResponse)
	if err != nil {
		return fmt.Errorf("vector embedding creation failed: %w", err)
	}

	// Step C: Write to STM Store
	pageID, err := mw.stmStore.StoreDialoguePage(ctx, &models.DialoguePage{
		UserID:        task.UserID,
		ChainID:       chainID,
		UserMessage:   task.UserMessage,
		AgentResponse: task.AgentResponse,
		Status:        "in_stm",
		Metadata:      task.Metadata,
		CreatedAt:     task.Timestamp,
		UpdatedAt:     time.Now(),
	})
	if err != nil {
		return fmt.Errorf("STM store write failed: %w", err)
	}

	// Store vector embedding with reference to the page (tenant/user/agent enforced from context)
	// Set tenant/user/agent from task context on the embedding
	embedding.TenantID = task.TenantID
	embedding.UserID = task.UserID
	embedding.AgentID = task.AgentID

	if err := mw.stmStore.StoreEmbedding(ctx, task.TenantID, task.UserID, task.AgentID, pageID.Hex(), embedding); err != nil {
		log.Printf("WARN: Failed to store embedding for page %s: %v", pageID.Hex(), err)
		// Don't fail the entire task for embedding storage issues
	}

	log.Printf("INFO: Memory formation completed - UserID: %s, ChainID: %s, PageID: %s",
		task.UserID, chainID, pageID.Hex())

	return nil
}

// handleShutdown sets up graceful shutdown handling
func (mw *Worker) handleShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.Printf("INFO: Received signal %v, shutting down workers", sig)
		mw.Stop()
	case <-mw.quit:
		// Already shutting down
	}
}

// StartMemoryWorkers is a convenience function to start workers with default config
func StartMemoryWorkers(ctx context.Context, workerCount int) *Worker {
	config := WorkerConfig{
		WorkerCount: workerCount,
		Redis:       nil, // Will need to be provided by caller
		Database:    database.GetDatabase(),
	}

	worker := NewWorker(config)
	worker.Start(ctx)
	return worker
}
