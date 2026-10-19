package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestCheckLLM(t *testing.T) {
	ctx := context.Background()
	ok := make([]float64, VectorDimension)

	p := new(MockLLMProvider)
	p.On("GenerateCompletion", mock.Anything, mock.Anything).Return("OK", nil)
	p.On("CreateEmbedding", mock.Anything, mock.Anything).Return(ok, nil)
	assert.NoError(t, (&STMStore{llmProvider: p}).CheckLLM(ctx))

	assert.ErrorContains(t, (&STMStore{}).CheckLLM(ctx), "no LLM provider")

	p = new(MockLLMProvider)
	p.On("GenerateCompletion", mock.Anything, mock.Anything).Return("", errors.New("status 404: DeploymentNotFound"))
	assert.ErrorContains(t, (&STMStore{llmProvider: p}).CheckLLM(ctx), "DeploymentNotFound")

	p = new(MockLLMProvider)
	p.On("GenerateCompletion", mock.Anything, mock.Anything).Return("OK", nil)
	p.On("CreateEmbedding", mock.Anything, mock.Anything).Return(make([]float64, VectorDimension*2), nil)
	err := (&STMStore{llmProvider: p}).CheckLLM(ctx)
	assert.ErrorContains(t, err, "MILVUS_VECTOR_DIMENSION")
}
