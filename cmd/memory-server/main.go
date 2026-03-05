package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	gw "bitbucket.org/dromos/memory-os/api/grpc/gen"
	"bitbucket.org/dromos/memory-os/api/middleware"
	"bitbucket.org/dromos/memory-os/internal/config"
	"bitbucket.org/dromos/memory-os/internal/memory"
	"bitbucket.org/dromos/memory-os/internal/server"
)

func main() {
	// Configure structured JSON logging
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

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

	// Prometheus Metrics endpoint (no auth required for scraping)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

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

	// Start background worker(s) for cognitive chain processing
	workerCtx, workerCancel := context.WithCancel(context.Background())
	numWorkers := getEnvInt("MEMORY_OS_NUM_WORKERS", 2)
	worker := memory.NewWorker(
		memoryServer.GetTaskQueue(),
		memoryServer.GetSTMStore(),
		memoryServer.GetRedisClient(),
	)
	for i := 1; i <= numWorkers; i++ {
		go worker.Start(workerCtx, i)
	}
	log.Printf("Started %d background worker(s)", numWorkers)

	// Start promoter scheduler (MTM → LTM promotion)
	promoterCtx, promoterCancel := context.WithCancel(context.Background())
	promoterIntervalMin := getEnvInt("MEMORY_OS_PROMOTER_INTERVAL_MIN", 30)
	promoterThreshold := getEnvFloat("MEMORY_OS_PROMOTER_THRESHOLD", 0.3)
	mongoDB := memoryServer.GetMongoClient().Database(cfg.Database.MongoDB.Database)

	// Initialize LTMWriter
	dbURL := os.Getenv("ARANGODB_URL")
	if dbURL == "" {
		dbURL = "http://localhost:8529"
	}
	dbUser := os.Getenv("ARANGODB_USER")
	if dbUser == "" {
		dbUser = "root"
	}
	dbPass := os.Getenv("ARANGODB_PASSWORD")
	if dbPass == "" {
		dbPass = "athena_dev"
	}
	dbName := os.Getenv("ARANGODB_DATABASE")
	if dbName == "" {
		dbName = "athena_ltm"
	}

	ltmWriter, err := memory.NewLTMWriter(promoterCtx, dbURL, dbUser, dbPass, dbName)
	if err != nil {
		log.Printf("WARN: Failed to initialize LTMWriter (is ArangoDB running and InitializeLTMGraph called?): %v", err)
	}

	ltmReader, err := memory.NewLTMReader(promoterCtx, dbURL, dbUser, dbPass, dbName)
	if err != nil {
		log.Printf("WARN: Failed to initialize LTMReader: %v", err)
	} else {
		memoryServer.SetLTMReader(ltmReader)
		log.Printf("INFO: LTMReader initialized — SearchMemory will include ArangoDB graph results")
	}

	promoter := memory.NewPromoter(mongoDB, memoryServer.GetSTMStore(), ltmWriter)
	memoryServer.SetPromoter(promoter)

	go func() {
		ticker := time.NewTicker(time.Duration(promoterIntervalMin) * time.Minute)
		defer ticker.Stop()
		log.Printf("Promoter scheduler started (interval: %dm, threshold: %.2f)", promoterIntervalMin, promoterThreshold)
		for {
			select {
			case <-promoterCtx.Done():
				log.Println("Promoter scheduler stopped")
				return
			case <-ticker.C:
				if err := promoter.RunOnce(promoterCtx, promoterThreshold); err != nil {
					log.Printf("WARN: Promoter run failed: %v", err)
				}
			}
		}
	}()

	// Start Archiver scheduler
	archiverCtx, archiverCancel := context.WithCancel(context.Background())
	archiverIntervalMin := getEnvInt("MEMORY_OS_ARCHIVER_INTERVAL_MIN", 60)

	go func() {
		ticker := time.NewTicker(time.Duration(archiverIntervalMin) * time.Minute)
		defer ticker.Stop()
		log.Printf("Archiver scheduler started (interval: %dm)", archiverIntervalMin)
		for {
			select {
			case <-archiverCtx.Done():
				log.Println("Archiver scheduler stopped")
				return
			case <-ticker.C:
				count, err := memoryServer.GetSTMStore().ArchiveColdChains(archiverCtx)
				if err != nil {
					log.Printf("ERROR: Archiver run failed: %v", err)
				} else if count > 0 {
					log.Printf("INFO: Archiver run completed, archived %d chains", count)
				}
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

	// Stop background workers and promoter
	workerCancel()
	promoterCancel()
	archiverCancel()

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

// getEnvInt reads an integer from env with a default
func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

// getEnvFloat reads a float from env with a default
func getEnvFloat(key string, defaultVal float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return defaultVal
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
