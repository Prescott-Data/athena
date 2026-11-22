package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// liveS3 returns a blob store on a real S3-compatible service (MinIO locally). Set
// ATHENA_TEST_BLOB_ENDPOINT, ATHENA_TEST_BLOB_ACCESS_KEY, ATHENA_TEST_BLOB_SECRET_KEY
// and ATHENA_TEST_BLOB_BUCKET (an existing bucket) to run.
func liveS3(t *testing.T) *S3BlobStore {
	t.Helper()
	endpoint := os.Getenv("ATHENA_TEST_BLOB_ENDPOINT")
	if endpoint == "" {
		t.Skip("set ATHENA_TEST_BLOB_ENDPOINT to run range reads against a real S3-compatible store")
	}
	store, err := NewS3BlobStore(endpoint, os.Getenv("ATHENA_TEST_BLOB_ACCESS_KEY"),
		os.Getenv("ATHENA_TEST_BLOB_SECRET_KEY"), os.Getenv("ATHENA_TEST_BLOB_BUCKET"), "us-east-1", false)
	require.NoError(t, err)
	return store
}

func TestS3RangeReadsAgainstARealStore(t *testing.T) {
	rangeReads(t, liveS3(t))
}

// TestGCSRangeReadsAgainstARealStore runs against the bucket named by
// ATHENA_TEST_GCS_BUCKET, with the credentials the environment gives the client
// (or an emulator, when STORAGE_EMULATOR_HOST is set).
func TestGCSRangeReadsAgainstARealStore(t *testing.T) {
	bucket := os.Getenv("ATHENA_TEST_GCS_BUCKET")
	if bucket == "" {
		t.Skip("set ATHENA_TEST_GCS_BUCKET to run range reads against Google Cloud Storage")
	}
	store, err := NewGCSBlobStore(context.Background(), bucket)
	require.NoError(t, err)
	rangeReads(t, store)
}

// rangeReads stores an object and reads it back by range, whole and after deletion.
func rangeReads(t *testing.T, store BlobStore) {
	t.Helper()
	ctx := context.Background()

	// Bytes that say where they are, so a wrong range cannot pass by accident.
	payload := make([]byte, 3<<20)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	uri, err := store.Upload(ctx, fmt.Sprintf("range-test/%d", time.Now().UnixNano()), bytes.NewReader(payload), "application/octet-stream")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Delete(context.Background(), uri) })

	size, err := store.Size(ctx, uri)
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), size)

	read := func(offset, length int64) []byte {
		body, err := store.DownloadRange(ctx, uri, offset, length)
		require.NoError(t, err)
		defer body.Close()
		data, err := io.ReadAll(body)
		require.NoError(t, err)
		return data
	}
	require.Equal(t, payload[:1<<20], read(0, 1<<20))
	require.Equal(t, payload[1<<20:2<<20], read(1<<20, 1<<20))
	require.Equal(t, payload[12345:12345+777], read(12345, 777))
	// A range that runs past the end is cut at the end.
	require.Equal(t, payload[len(payload)-10:], read(int64(len(payload)-10), 1<<20))

	// The whole, a page at a time, is the payload.
	var whole []byte
	for offset := int64(0); offset < size; offset += 1 << 20 {
		whole = append(whole, read(offset, 1<<20)...)
	}
	require.True(t, bytes.Equal(payload, whole))

	require.NoError(t, store.Delete(ctx, uri))
	_, err = store.Size(ctx, uri)
	require.True(t, errors.Is(err, ErrBlobNotFound), "a deleted object reads as not found, got %v", err)
	_, err = store.DownloadRange(ctx, uri, 0, 10)
	require.True(t, errors.Is(err, ErrBlobNotFound), "a deleted object reads as not found, got %v", err)
}
