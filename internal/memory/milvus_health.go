package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// ErrMilvusNotConfigured means no Milvus host is set.
var ErrMilvusNotConfigured = errors.New("not configured: set MEMORY_OS_MILVUS_HOST (or MILVUS_HOST)")

// milvusAddress returns the Milvus host and port, each read under its MEMORY_OS_
// name first and its unprefixed alias second, as the configuration reference
// documents. An empty port lets NewMilvusClient default it to 19530.
func milvusAddress() (host, port string) {
	return firstEnv("MEMORY_OS_MILVUS_HOST", "MILVUS_HOST"), firstEnv("MEMORY_OS_MILVUS_PORT", "MILVUS_PORT")
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// MilvusHealth reports whether chain vectors can be stored and searched.
func (s *STMStore) MilvusHealth(ctx context.Context) error {
	if s.milvus == nil {
		if s.milvusErr != nil {
			return s.milvusErr
		}
		return ErrMilvusNotConfigured
	}
	return s.milvus.Ping(ctx)
}

// Ping checks that Milvus answers and holds the STM collection.
func (mc *MilvusClient) Ping(ctx context.Context) error {
	exists, err := mc.client.HasCollection(ctx, STMCollectionName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("collection %s does not exist", STMCollectionName)
	}
	return nil
}
