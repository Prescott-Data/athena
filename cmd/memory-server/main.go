package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	gw "bitbucket.org/dromos/memory-os/api/grpc/gen"
	"bitbucket.org/dromos/memory-os/api/middleware"
	"bitbucket.org/dromos/memory-os/internal/config"
	"bitbucket.org/dromos/memory-os/internal/server"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Initialize server
	memoryServer, err := server.NewMemoryServer(cfg)
	if err != nil {
		log.Fatalf("Failed to create memory server: %v", err)
	}

	// Create authentication middleware
	authMiddleware := middleware.NewAuthMiddleware(&cfg.Auth)

	// Start gRPC server
	grpcServer := grpc.NewServer(
	// Add gRPC interceptors here if needed
	)

	// Register gRPC service
	gw.RegisterMemoryServiceServer(grpcServer, memoryServer)

	grpcListener, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Server.GRPCPort))
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port %d: %v", cfg.Server.GRPCPort, err)
	}

	// Start gRPC server in background
	go func() {
		log.Printf("Starting gRPC server on port %d", cfg.Server.GRPCPort)
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Fatalf("Failed to serve gRPC: %v", err)
		}
	}()

	// Create REST gateway
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := runtime.NewServeMux()

	// Register gRPC gateway endpoints
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	err = registerGatewayEndpoints(ctx, mux, fmt.Sprintf("localhost:%d", cfg.Server.GRPCPort), opts)
	if err != nil {
		log.Fatalf("Failed to register gateway endpoints: %v", err)
	}

	// Create Gin router for REST API
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	// Add authentication middleware
	router.Use(authMiddleware.GinAuthMiddleware())

	// Add CORS middleware
	router.Use(corsMiddleware())

	// Health check endpoint (no auth required)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().UTC(),
			"service":   "memory-os",
		})
	})

	// Mount gRPC gateway
	router.Any("/api/*path", func(c *gin.Context) {
		mux.ServeHTTP(c.Writer, c.Request)
	})

	// Start HTTP server
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start HTTP server in background
	go func() {
		log.Printf("Starting HTTP server on port %d", cfg.Server.Port)
		if cfg.Server.EnableTLS {
			if err := httpServer.ListenAndServeTLS(cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Failed to start HTTPS server: %v", err)
			}
		} else {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Failed to start HTTP server: %v", err)
			}
		}
	}()

	log.Printf("Memory OS started successfully")
	log.Printf("HTTP API: http://localhost:%d", cfg.Server.Port)
	log.Printf("gRPC API: localhost:%d", cfg.Server.GRPCPort)
	log.Printf("Health check: http://localhost:%d/health", cfg.Server.Port)

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down Memory OS...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server forced to shutdown: %v", err)
	}

	// Shutdown gRPC server
	grpcServer.GracefulStop()

	// Cleanup server resources
	if err := memoryServer.Close(); err != nil {
		log.Printf("Error closing memory server: %v", err)
	}

	log.Println("Memory OS shutdown complete")
}

// registerGatewayEndpoints registers the gRPC gateway endpoints
func registerGatewayEndpoints(ctx context.Context, mux *runtime.ServeMux, grpcEndpoint string, opts []grpc.DialOption) error {
	return gw.RegisterMemoryServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts)
}

// corsMiddleware adds CORS headers
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-API-Key, X-JWT-Token")
		c.Header("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
