package storage

import (
	"context"
	"errors"
	"io"
)

// ErrBlobNotFound is returned by Size and DownloadRange when the object behind a
// URI does not exist (for example after its chain was archived).
var ErrBlobNotFound = errors.New("blob not found")

// BlobStore defines the unified interface for interacting with Blob Storage providers.
type BlobStore interface {
	// Upload streams data to the blob store using a specific key and returns the full provider-specific URI.
	Upload(ctx context.Context, key string, data io.Reader, mimeType string) (string, error)

	// Download returns a reader for the blob data given its URI.
	Download(ctx context.Context, uri string) (io.ReadCloser, error)

	// Size returns the size in bytes of the blob at the given URI.
	Size(ctx context.Context, uri string) (int64, error)

	// DownloadRange returns a reader for length bytes of the blob starting at
	// offset, fetched with the provider's range read. The range must start inside
	// the blob (offset < Size) and length must be positive; a range that runs past
	// the end is cut at the end.
	DownloadRange(ctx context.Context, uri string, offset, length int64) (io.ReadCloser, error)

	// Delete removes a blob from the store given its URI. Deleting a blob that does
	// not exist is not an error, so deletions can be retried.
	Delete(ctx context.Context, uri string) error
}
