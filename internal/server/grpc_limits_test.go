package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Prescott-Data/athena/api/grpc/gen"
	"github.com/Prescott-Data/athena/internal/config"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// restOverGRPC serves s the way cmd/memory-server does: a gRPC server on a local
// port and the REST gateway dialling it. It returns the REST base URL.
func restOverGRPC(t *testing.T, s *MemoryServer, serverOpts []grpc.ServerOption, dialOpts []grpc.DialOption) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer(serverOpts...)
	gen.RegisterMemoryServiceServer(grpcServer, s)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mux := runtime.NewServeMux()
	dialOpts = append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, dialOpts...)
	require.NoError(t, gen.RegisterMemoryServiceHandlerFromEndpoint(ctx, mux, lis.Addr().String(), dialOpts))
	rest := httptest.NewServer(mux)
	t.Cleanup(rest.Close)
	return rest.URL
}

// storeEventBody is a StoreEvent REST body whose payload is rawBytes long.
func storeEventBody(t *testing.T, rawBytes int) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"role": "agent", "type": "observation", "mime_type": "text/plain",
		"payload": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), rawBytes)),
	})
	require.NoError(t, err)
	return body
}

func TestRESTStoreEventAcceptsAnEightMiBBody(t *testing.T) {
	s, blobs, _ := payloadServer()
	max := config.DefaultGRPCMaxMessageBytes
	url := restOverGRPC(t, s, MessageSizeServerOptions(max), MessageSizeDialOptions(max))

	// 6,290,000 raw bytes is just under 8 MiB once base64 in JSON.
	body := storeEventBody(t, 6_290_000)
	require.Greater(t, len(body), 8<<20-4096)
	require.LessOrEqual(t, len(body), 8<<20)

	resp, err := http.Post(url+"/api/v1/sessions/s1/events", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	var stored struct {
		EventID string `json:"eventId"`
	}
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Len(t, blobs.blobs, 1)

	// The payload reads back through the gateway a 1 MiB page at a time.
	var got int
	for offset := 0; ; {
		page, err := http.Get(url + "/api/v1/sessions/s1/events/" + stored.EventID + "/payload?offset=" + strconv.Itoa(offset))
		require.NoError(t, err)
		pageRaw, _ := io.ReadAll(page.Body)
		page.Body.Close()
		require.Equal(t, http.StatusOK, page.StatusCode, string(pageRaw))
		var out struct {
			Data       []byte `json:"data"`
			TotalBytes string `json:"totalBytes"`
			Offset     string `json:"offset"`
			MimeType   string `json:"mimeType"`
		}
		require.NoError(t, json.Unmarshal(pageRaw, &out))
		assert.Equal(t, "6290000", out.TotalBytes)
		assert.Equal(t, "text/plain", out.MimeType)
		if len(out.Data) == 0 {
			break
		}
		assert.LessOrEqual(t, len(out.Data), maxPayloadRead)
		got += len(out.Data)
		offset += len(out.Data)
	}
	assert.Equal(t, 6_290_000, got)
}

// Without the options, gRPC's 4 MiB default refuses the same request; this is the
// limit MEMORY_OS_GRPC_MAX_MESSAGE_BYTES replaces.
func TestRESTStoreEventIsRefusedAtTheGRPCDefault(t *testing.T) {
	s, blobs, _ := payloadServer()
	url := restOverGRPC(t, s, nil, nil)

	resp, err := http.Post(url+"/api/v1/sessions/s1/events", "application/json", bytes.NewReader(storeEventBody(t, 6_290_000)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, blobs.blobs)
}

// A message above the configured limit is still refused.
func TestRESTStoreEventIsRefusedAboveTheConfiguredLimit(t *testing.T) {
	s, blobs, _ := payloadServer()
	url := restOverGRPC(t, s, MessageSizeServerOptions(1<<20), MessageSizeDialOptions(1<<20))

	resp, err := http.Post(url+"/api/v1/sessions/s1/events", "application/json", bytes.NewReader(storeEventBody(t, 2<<20)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, blobs.blobs)
}
