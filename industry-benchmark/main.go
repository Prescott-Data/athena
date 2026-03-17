// industry-benchmark is the entry point for all Athena research hypothesis experiments.
//
// Each experiment reads a real industry dataset from the data/ directory,
// injects it into Athena via the REST API, probes the results, and writes
// a results JSON file for the eval scripts to score.
//
// Usage:
//
//	go run main.go --exp h5 --mode pilot   --data ./data/h5
//	go run main.go --exp h5 --mode clean   --data ./data/h5 --url http://localhost:8080
//	go run main.go --exp h5 --mode corrupt --data ./data/h5
//	go run main.go --exp h5 --mode control --data ./data/h5
package main

import (
	"flag"
	"log"

	"bitbucket.org/dromos/industry-benchmark/athena"
	"bitbucket.org/dromos/industry-benchmark/experiments/h5"
)

const (
	DefaultTenantID = "tenant_dromos_research"
	DefaultUserID   = "user_athena_benchmark"
)

func main() {
	exp     := flag.String("exp",  "",                      "Experiment to run: h5, h1, h3, h4, h2, h6")
	mode    := flag.String("mode", "pilot",                 "Experiment mode (pilot, clean, corrupt, control, ...)")
	dataDir := flag.String("data", "./data",                "Path to dataset directory for this experiment")
	url     := flag.String("url",  "http://localhost:8080", "Athena Memory-OS base URL")
	apiKey  := flag.String("key",  "dev-api-key",           "Athena API key")
	tenant  := flag.String("tenant", DefaultTenantID,       "Tenant ID")
	user    := flag.String("user",   DefaultUserID,         "User ID")
	flag.Parse()

	if *exp == "" {
		log.Fatal("--exp is required. Available: h5, h1, h3, h4, h2, h6")
	}

	client := athena.New(*url, *apiKey)

	// Health check before running anything
	health, err := client.HealthCheck()
	if err != nil {
		log.Fatalf("❌ Athena health check failed: %v\n\nIs Athena running at %s?\n", err, *url)
	}
	log.Printf("✅ Athena is healthy: %s (status=%s)\n", health.Service, health.Status)

	switch *exp {
	case "h5":
		if err := h5.New(client).Run(*tenant, *user, *mode, *dataDir); err != nil {
			log.Fatalf("❌ H5 experiment failed: %v", err)
		}
	default:
		log.Fatalf("Unknown experiment %q — available: h5 (h1, h3, h4, h2, h6 coming next)", *exp)
	}
}
