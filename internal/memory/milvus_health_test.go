package memory

import (
	"context"
	"errors"
	"testing"
)

func TestMilvusAddressReadsBothNames(t *testing.T) {
	for _, tt := range []struct {
		name               string
		env                map[string]string
		wantHost, wantPort string
	}{
		{"prefixed", map[string]string{"MEMORY_OS_MILVUS_HOST": "a", "MEMORY_OS_MILVUS_PORT": "1"}, "a", "1"},
		{"alias", map[string]string{"MILVUS_HOST": "b", "MILVUS_PORT": "2"}, "b", "2"},
		{"prefixed wins", map[string]string{"MEMORY_OS_MILVUS_HOST": "a", "MILVUS_HOST": "b"}, "a", ""},
		// The 0.2 trap: host under one name, port under the other.
		{"mixed", map[string]string{"MILVUS_HOST": "b", "MEMORY_OS_MILVUS_PORT": "1"}, "b", "1"},
		{"unset", nil, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"MEMORY_OS_MILVUS_HOST", "MEMORY_OS_MILVUS_PORT", "MILVUS_HOST", "MILVUS_PORT"} {
				t.Setenv(k, tt.env[k])
			}
			if host, port := milvusAddress(); host != tt.wantHost || port != tt.wantPort {
				t.Errorf("milvusAddress() = %q, %q; want %q, %q", host, port, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestMilvusHealthWithoutClient(t *testing.T) {
	if err := (&STMStore{}).MilvusHealth(context.Background()); !errors.Is(err, ErrMilvusNotConfigured) {
		t.Errorf("no client: got %v, want ErrMilvusNotConfigured", err)
	}
	failed := errors.New("connection failed: dial tcp: refused")
	if err := (&STMStore{milvusErr: failed}).MilvusHealth(context.Background()); err != failed {
		t.Errorf("failed connection: got %v, want %v", err, failed)
	}
}
