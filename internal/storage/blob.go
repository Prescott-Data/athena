package storage

import (
	"context"
	"io"
)

// BlobStore defines the unified interface for interacting with Blob Storage providers.
type BlobStore interface {
	// Upload streams data to the blob store using a specific key and returns the full provider-specific URI.
	Upload(ctx context.Context, key string, data io.Reader, mimeType string) (string, error)

	// Download returns a reader for the blob data given its URI.
	Download(ctx context.Context, uri string) (io.ReadCloser, error)

	// Delete removes a blob from the store given its URI. Deleting a blob that does
	// not exist is not an error, so deletions can be retried.
	Delete(ctx context.Context, uri string) error
}
