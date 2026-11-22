package server

import (
	"github.com/Prescott-Data/athena/internal/config"
	"google.golang.org/grpc"
)

// MessageSizeServerOptions lets the gRPC server receive messages of up to maxBytes
// (a StoreEvent carrying a payload), instead of gRPC's default of 4 MiB. Replies
// are not capped on the server side. A non-positive maxBytes means the default.
func MessageSizeServerOptions(maxBytes int) []grpc.ServerOption {
	return []grpc.ServerOption{grpc.MaxRecvMsgSize(messageSizeOrDefault(maxBytes))}
}

// MessageSizeDialOptions gives the REST gateway's connection to the gRPC server the
// same limit in both directions, so a request the server would accept is not
// refused on the way in and a reply is not refused on the way back.
func MessageSizeDialOptions(maxBytes int) []grpc.DialOption {
	n := messageSizeOrDefault(maxBytes)
	return []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(n), grpc.MaxCallSendMsgSize(n))}
}

func messageSizeOrDefault(maxBytes int) int {
	if maxBytes <= 0 {
		return config.DefaultGRPCMaxMessageBytes
	}
	return maxBytes
}
