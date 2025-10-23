package config

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// NewHTTPClient creates a new HTTP client with a custom dialer that can be used to resolve
// DNS names to private IP addresses within a VNet.
func NewHTTPClient() *http.Client {
	// Check if a custom DNS resolver is configured
	resolverIP := os.Getenv("AZURE_PRIVATE_DNS_RESOLVER_IP")
	if resolverIP == "" {
		// If not configured, use the default HTTP client
		return http.DefaultClient
	}

	// Create a custom dialer that uses the specified DNS resolver
	dialer := &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				// The address will be in the format "host:port", but we only need the host for the resolver
				// The resolver address should be in the format "ip:port"
				resolverAddr := net.JoinHostPort(resolverIP, "53")
				d := net.Dialer{
					Timeout: 10 * time.Second,
				}
				return d.DialContext(ctx, "udp", resolverAddr)
			},
		},
	}

	// Create a custom transport that uses the custom dialer
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// The network will be "tcp", and the addr will be "host:port"
			// We need to resolve the host to a private IP address using the custom dialer
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			// Only resolve Azure OpenAI endpoints
			if !strings.HasSuffix(host, ".openai.azure.com") {
				return dialer.DialContext(ctx, network, addr)
			}

			// Resolve the host to a private IP address
			ips, err := dialer.Resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}

			// Use the first IP address found
			if len(ips) > 0 {
				addr = net.JoinHostPort(ips[0].String(), port)
			}

			return dialer.DialContext(ctx, network, addr)
		},
	}

	// Create a new HTTP client with the custom transport
	return &http.Client{
		Transport: transport,
	}
}
