package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)


// Test Helpers
func setupTaskQueueTest() (*TaskQueue, *MockRedis) {
	mockRedis := new(MockRedis)
	taskQueue := NewTaskQueue(mockRedis)
	return taskQueue, mockRedis
}

func Test_GetQueueStats(t *testing.T) {
	taskQueue, mockRedis := setupTaskQueueTest()
	ctx := context.Background()

	expectedLength := int64(5)

	mockRedis.On("LLen", GlobalWorkQueueName).Return(expectedLength, nil).Once()

	stats, err := taskQueue.GetQueueStats(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, stats)

	assert.Equal(t, expectedLength, stats["queue_length"])

	mockRedis.AssertExpectations(t)
}
