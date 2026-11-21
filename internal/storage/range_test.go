package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	gcstorage "cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

const rangeTestBody = "0123456789abcdefghij"

// objectServer is a stand-in object store holding one 20-byte object at
// /bucket/dir/obj. It answers HEAD, ranged GET (Range or x-ms-range) and the GCS
// JSON metadata call, and records the range header of every GET, so the tests see
// what each SDK asks the provider for.
type objectServer struct {
	*httptest.Server
	mu     sync.Mutex
	ranges []string
}

func newObjectServer(t *testing.T) *objectServer {
	t.Helper()
	o := &objectServer{}
	o.Server = httptest.NewServer(http.HandlerFunc(o.serve))
	t.Cleanup(o.Close)
	return o
}

func (o *objectServer) serve(w http.ResponseWriter, r *http.Request) {
	// GCS object metadata (Attrs).
	if strings.HasPrefix(r.URL.Path, "/storage/v1/b/") || strings.HasPrefix(r.URL.Path, "/b/") {
		if !strings.HasSuffix(r.URL.EscapedPath(), "/o/dir%2Fobj") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":404,"message":"No such object"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"bucket":"bucket","name":"dir/obj","size":"%d"}`, len(rangeTestBody))
		return
	}
	if r.URL.Path != "/bucket/dir/obj" {
		w.Header().Set("x-ms-error-code", "BlobNotFound")
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		if r.Method != http.MethodHead {
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>not found</Message></Error>`)
		}
		return
	}
	w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
	w.Header().Set("ETag", `"abc"`)
	w.Header().Set("Content-Type", "text/plain")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(rangeTestBody)))
		return
	}
	rng := r.Header.Get("x-ms-range")
	if rng == "" {
		rng = r.Header.Get("Range")
	}
	o.mu.Lock()
	o.ranges = append(o.ranges, rng)
	o.mu.Unlock()
	var start, end int
	if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil || start >= len(rangeTestBody) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if end >= len(rangeTestBody) {
		end = len(rangeTestBody) - 1
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(rangeTestBody)))
	w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	w.WriteHeader(http.StatusPartialContent)
	fmt.Fprint(w, rangeTestBody[start:end+1])
}

// checkRangedReads runs the same reads against any provider.
func checkRangedReads(t *testing.T, store BlobStore, o *objectServer, uri, missingURI string) {
	t.Helper()
	ctx := context.Background()

	size, err := store.Size(ctx, uri)
	require.NoError(t, err)
	assert.Equal(t, int64(len(rangeTestBody)), size)

	r, err := store.DownloadRange(ctx, uri, 5, 7)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, "56789ab", string(got))
	// The provider was asked for those seven bytes, not the object.
	assert.Equal(t, []string{"bytes=5-11"}, o.ranges)

	_, err = store.Size(ctx, missingURI)
	assert.ErrorIs(t, err, ErrBlobNotFound)
	_, err = store.DownloadRange(ctx, missingURI, 0, 4)
	assert.ErrorIs(t, err, ErrBlobNotFound)
}

func TestS3RangedRead(t *testing.T) {
	o := newObjectServer(t)
	store, err := NewS3BlobStore(strings.TrimPrefix(o.URL, "http://"), "key", "secret", "bucket", "us-east-1", false)
	require.NoError(t, err)
	checkRangedReads(t, store, o, "s3://bucket/dir/obj", "s3://bucket/dir/missing")
}

func TestGCSRangedRead(t *testing.T) {
	o := newObjectServer(t)
	client, err := gcstorage.NewClient(context.Background(), option.WithEndpoint(o.URL), option.WithoutAuthentication())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	store := &GCSBlobStore{client: client, bucket: "bucket"}
	checkRangedReads(t, store, o, "gs://bucket/dir/obj", "gs://bucket/dir/missing")
}

func TestAzureRangedRead(t *testing.T) {
	o := newObjectServer(t)
	client, err := azblob.NewClientWithNoCredential(o.URL, nil)
	require.NoError(t, err)
	store := &AzureBlobStore{client: client, container: "bucket", account: "acct"}
	checkRangedReads(t, store, o, "https://acct.blob.core.windows.net/bucket/dir/obj", "https://acct.blob.core.windows.net/bucket/dir/missing")
}
