// Package h5 implements the Hypothesis 5 (Modality-Agnostic Synthesis) experiment.
//
// It reads real GitHub data from the data/h5/ directory and sends it to Athena
// via the REST API, then probes chain count and calculates CCR and ERS.
//
// Directory layout expected under --data flag:
//
//	data/h5/
//	  chat/          100 .txt files  — GitHub Issue comment threads (Format A)
//	  json/          100 .json files — GitHub Actions workflow_run payloads (Format B)
//	  logs/          100 .log files  — CI/CD execution log lines (Format C)
//	  control_chat/  100 .txt files  — different-domain issues (docs, deps, etc.)
//	  control_json/  100 .json files — matching control JSON payloads
//	  control_logs/  100 .log files  — matching control log lines
//
// Files must share a common stem across their format directories:
//
//	chat/issue_042.txt  ←→  json/issue_042.json  ←→  logs/issue_042.log
//
// Usage:
//
//	go run main.go --exp h5 --mode pilot   --data ./data/h5 --url http://localhost:8080
//	go run main.go --exp h5 --mode clean   --data ./data/h5
//	go run main.go --exp h5 --mode corrupt --data ./data/h5
//	go run main.go --exp h5 --mode control --data ./data/h5
package h5

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bitbucket.org/dromos/industry-benchmark/athena"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Triplet holds the three matched files for a single GitHub issue.
// All three must describe the same event — that is what we are testing.
type Triplet struct {
	IssueID string // the shared stem, e.g. "issue_042"
	Chat    string // content of chat/issue_042.txt   (Format A — human prose)
	JSON    string // content of json/issue_042.json  (Format B — structured payload)
	Log     string // content of logs/issue_042.log   (Format C — system log)
}

// Result holds every metric reading from one benchmark run.
// Written to results/h5_<mode>_<timestamp>.json after the run.
type Result struct {
	Mode             string    `json:"mode"`
	RunAt            time.Time `json:"run_at"`
	TenantID         string    `json:"tenant_id"`
	UserID           string    `json:"user_id"`
	SessionID        string    `json:"session_id"`
	TripletsLoaded   int       `json:"triplets_loaded"`
	EventsInjected   int       `json:"events_injected"`
	CorruptedJSONs   int       `json:"corrupted_jsons,omitempty"`
	ChainsFound      int       `json:"chains_found"`
	STMEventsFound   int       `json:"stm_events_found"`

	// CCR: secondary metric — events / chains. Shows topic-based consolidation.
	// A high CCR means many events compressed into few chains (good).
	CCR           float64 `json:"ccr"`
	OracleCCRPass bool    `json:"oracle_ccr_pass"`

	// CLR: primary H5 metric — co-location rate.
	// % of sampled issues where SearchMemory returned content containing all 3 formats.
	// Full CLR requires worker to write chainId back onto in_mtm events (not yet implemented).
	// This is an automated spot-check proxy over CLRSampleSize issues.
	CLRSampleSize int     `json:"clr_sample_size"`
	CLRPassed     int     `json:"clr_passed"`
	CLRRate       float64 `json:"clr_rate"`
	OracleCLRPass bool    `json:"oracle_clr_pass"`

	ExtractionErrors int      `json:"extraction_errors"`
	Notes            []string `json:"notes,omitempty"`
}

// Experiment is the H5 runner.
type Experiment struct {
	client *athena.Client
}

func New(client *athena.Client) *Experiment {
	return &Experiment{client: client}
}

// Run is the top-level entry point called from main.go.
func (e *Experiment) Run(tenantID, userID, mode, dataDir string) error {
	log.Println("════════════════════════════════════════════════")
	log.Println("  H5 — MODALITY-AGNOSTIC SYNTHESIS EXPERIMENT  ")
	log.Println("════════════════════════════════════════════════")
	log.Printf("Mode:     %s", mode)
	log.Printf("Data dir: %s", dataDir)
	log.Printf("Oracle:   CCR ≥ %.1f | ERS ≥ %.0f%% | Expected chains ~%d",
		Oracle.CCRPassThreshold, Oracle.ERSPassThreshold*100, Oracle.ExpectedChains)
	log.Printf("Injection: %s", Oracle.InjectionOrder)
	fmt.Println()

	switch mode {
	case "pilot":
		return e.runPilot(tenantID, userID, dataDir)
	case "clean":
		return e.runClean(tenantID, userID, dataDir)
	case "corrupt":
		return e.runCorrupt(tenantID, userID, dataDir)
	case "control":
		return e.runControl(tenantID, userID, dataDir)
	case "probe":
		return e.runProbe(tenantID, userID)
	default:
		return fmt.Errorf("unknown mode %q — use: pilot, clean, corrupt, control, probe", mode)
	}
}

// ─────────────────────────────────────────────
//  Benchmark 1 — Clean formats
// ─────────────────────────────────────────────

// runClean injects 100 interactions (one per triplet) with clean format data.
// Format A → UserMessage, Format B + Format C → AgentResponse.
// Expected: ~10 chains (semantic topic clusters), CCR ≥ 5.0.
func (e *Experiment) runClean(tenantID, userID, dataDir string) error {
	log.Println("BENCHMARK 1 — Clean formats (100 interactions, 1 per triplet)")
	log.Printf("Expected: ~%d chains | CCR ≥ %.1f", Oracle.ExpectedChains, Oracle.CCRPassThreshold)
	fmt.Println()

	triplets, err := loadTriplets(dataDir, "chat", "json", "logs")
	if err != nil {
		return fmt.Errorf("failed to load triplets: %w", err)
	}
	log.Printf("Loaded %d triplets from %s", len(triplets), dataDir)

	session, err := e.createSession(tenantID, userID, "h5-clean")
	if err != nil {
		return err
	}

	result, err := e.ingestAndProbe(session.SessionID, triplets, "clean", 0)
	if err != nil {
		return err
	}
	result.TenantID = tenantID
	result.UserID = userID

	return e.printAndSave(result)
}

// ─────────────────────────────────────────────
//  Benchmark 2 — Corrupted JSON (20% of payloads)
// ─────────────────────────────────────────────

// runCorrupt injects 100 interactions with 20% of JSON payloads (Format B) truncated.
// Because chain formation is driven by Format A (UserMessage), corrupted Format B
// in AgentResponse must NOT break chain grouping. CCR must still pass.
// This validates the modality-agnostic claim: Format B corruption is invisible to chains.
func (e *Experiment) runCorrupt(tenantID, userID, dataDir string) error {
	log.Println("BENCHMARK 2 — Corrupted JSON (20% of Format B payloads truncated mid-field)")
	log.Printf("Corrupting %d of %d JSON files", Oracle.CorruptedJSONs, Oracle.EventsPerFormat)
	log.Printf("Expected: CCR ≥ %.1f still | ERS ≥ %.0f%% (Format B corruption must not break chains)", Oracle.CCRPassThreshold, Oracle.ERSPassThreshold*100)
	fmt.Println()

	triplets, err := loadTriplets(dataDir, "chat", "json", "logs")
	if err != nil {
		return fmt.Errorf("failed to load triplets: %w", err)
	}

	// Corrupt 20% of JSON payloads by truncating at a random mid-field position.
	// Use a fixed seed so the same 20 issues are corrupted on every run —
	// reproducibility is required for a valid experiment.
	rng := rand.New(rand.NewSource(42))
	corruptedCount := 0
	indices := rng.Perm(len(triplets))[:Oracle.CorruptedJSONs]
	corruptedSet := make(map[int]bool, len(indices))
	for _, i := range indices {
		corruptedSet[i] = true
	}

	for i := range triplets {
		if corruptedSet[i] {
			triplets[i].JSON = truncateJSON(triplets[i].JSON)
			corruptedCount++
		}
	}
	log.Printf("Corrupted %d JSON payloads (seed=42, reproducible)", corruptedCount)

	session, err := e.createSession(tenantID, userID, "h5-corrupt")
	if err != nil {
		return err
	}

	result, err := e.ingestAndProbe(session.SessionID, triplets, "corrupt", corruptedCount)
	if err != nil {
		return err
	}
	result.TenantID = tenantID
	result.UserID = userID

	return e.printAndSave(result)
}

// ─────────────────────────────────────────────
//  Benchmark 3 — Control (different domain)
// ─────────────────────────────────────────────

// runControl injects 100 interactions from a completely different domain (hotel bookings).
// These use the same triplet structure but about hotel conversations (MultiWOZ 2.4).
// They must NOT consolidate with Benchmark 1 chains. CCR ≥ 5.0 must still pass,
// proving the format-agnostic grouping holds for any domain, not just CI/deployment.
func (e *Experiment) runControl(tenantID, userID, dataDir string) error {
	log.Println("BENCHMARK 3 — Control domain (hotel bookings — MultiWOZ 2.4)")
	log.Println("These must NOT merge with Benchmark 1 chains if run after clean.")
	log.Printf("Expected: ~%d chains | CCR ≥ %.1f from the control domain alone", Oracle.ExpectedChains, Oracle.CCRPassThreshold)
	fmt.Println()

	triplets, err := loadTriplets(dataDir, "control_chat", "control_json", "control_logs")
	if err != nil {
		return fmt.Errorf("failed to load control triplets: %w", err)
	}
	log.Printf("Loaded %d control triplets", len(triplets))

	session, err := e.createSession(tenantID, userID, "h5-control")
	if err != nil {
		return err
	}

	result, err := e.ingestAndProbe(session.SessionID, triplets, "control", 0)
	if err != nil {
		return err
	}
	result.TenantID = tenantID
	result.UserID = userID
	result.Notes = append(result.Notes,
		"Control domain: docs/deps issues. CCR pass proves format-agnostic grouping holds across domains.",
		"Cross-domain isolation must be verified separately by checking community_id in ArangoDB.",
	)

	return e.printAndSave(result)
}

// ─────────────────────────────────────────────
//  Pilot
// ─────────────────────────────────────────────

// runPilot injects the first triplet only (3 events: A=user, B=agent, C=agent).
// All 3 share the same workflow_id so the worker's binding guard keeps them together.
// Goal: confirm plumbing — events land in STM, worker fires, formats are accepted.
// No oracle evaluation — just plumbing verification.
func (e *Experiment) runPilot(tenantID, userID, dataDir string) error {
	log.Println("PILOT RUN — 1 triplet (3 events). Confirming plumbing only.")
	log.Println("Do NOT evaluate CCR/CLR from this run. Check Grafana metrics.")
	fmt.Println()

	triplets, err := loadTriplets(dataDir, "chat", "json", "logs")
	if err != nil {
		return fmt.Errorf("failed to load triplets: %w", err)
	}
	if len(triplets) == 0 {
		return fmt.Errorf("no triplets found in %s — check data directory layout", dataDir)
	}

	// Only use the first triplet for the pilot
	pilot := triplets[:1]

	session, err := e.createSession(tenantID, userID, "h5-pilot")
	if err != nil {
		return err
	}

	for i, ev := range buildEvents(pilot, "pilot") {
		resp, err := e.client.StoreEvent(session.SessionID, ev)
		if err != nil {
			return fmt.Errorf("pilot: failed to store event %d: %w", i+1, err)
		}
		log.Printf("Event %d [%s format=%s] stored — id=%s",
			i+1, ev.Metadata["issue_id"], ev.Metadata["h5_format"], resp.EventID)
		time.Sleep(500 * time.Millisecond)
	}

	log.Println()
	log.Println("Pilot complete. Verify the following before proceeding:")
	log.Println("  Redis STM: LRANGE stm:<tenant>:<user>:h5-pilot 0 -1 — should show 3 events")
	log.Println("  Grafana:   memos_extractor_schema_failures_total — should be 0")
	log.Println("  Worker log: should show 'Not enough user messages' (only 1 user event)")
	log.Println()
	log.Println("If all checks pass → run --mode clean")
	log.Println("If any check fails → fix the plumbing before scaling up")
	return nil
}

// ─────────────────────────────────────────────
//  Probe-only (re-query without re-injecting)
// ─────────────────────────────────────────────

// runProbe skips injection and re-runs only the GetContext + SearchMemory probe
// against whatever chains are already in the DB for agentId "h5-clean".
// Chains are scoped by tenantId+userId+agentId (not sessionId), so a new session
// with the same agentId will return the same chains.
// EventsInjected is read from the most recent clean results file if present.
func (e *Experiment) runProbe(tenantID, userID string) error {
	log.Println("PROBE-ONLY — re-querying existing chains (no injection)")
	log.Println("Chains are scoped by agentId, so a new session returns the same MTM state.")
	fmt.Println()

	session, err := e.createSession(tenantID, userID, "h5-clean")
	if err != nil {
		return err
	}

	// Read last known events_injected from most recent clean result, default 300.
	eventsInjected := 300
	if data, err := os.ReadFile(latestResultFile("clean")); err == nil {
		var prev Result
		if json.Unmarshal(data, &prev) == nil && prev.EventsInjected > 0 {
			eventsInjected = prev.EventsInjected
		}
	}
	log.Printf("Using events_injected=%d for CCR calculation", eventsInjected)

	result := &Result{
		Mode:           "clean",
		RunAt:          time.Now(),
		SessionID:      session.SessionID,
		TenantID:       tenantID,
		UserID:         userID,
		TripletsLoaded: eventsInjected / 3,
		EventsInjected: eventsInjected,
	}

	log.Println("─── Probe: chain count (GetContext) ───")
	ctx, err := e.client.GetContext(session.SessionID, athena.GetContextRequest{
		Limit: 200,
		Query: "deployment failure API error container",
	})
	if err != nil {
		log.Printf("⚠️  GetContext failed: %v", err)
		result.Notes = append(result.Notes, "GetContext failed: "+err.Error())
	} else {
		result.ChainsFound = len(ctx.RelevantPages)
		result.STMEventsFound = len(ctx.STMEvents)
		log.Printf("  STM events:  %d", result.STMEventsFound)
		log.Printf("  MTM chains:  %d", result.ChainsFound)
		for i, p := range ctx.RelevantPages {
			log.Printf("  Chain %3d:   topic=%s", i+1, p.Topic)
		}
	}

	log.Println("─── Probe: SearchMemory (cross-format retrieval spot check) ───")
	search, err := e.client.SearchMemory(session.SessionID, athena.SearchMemoryRequest{
		Query: "billing API deployment container failed health check",
		Limit: 5,
	})
	if err != nil {
		log.Printf("⚠️  SearchMemory failed: %v", err)
	} else {
		log.Printf("  SearchMemory returned %d results", len(search.Results))
		for i, r := range search.Results {
			snippet := r.Content
			if len(snippet) > 100 {
				snippet = snippet[:100] + "..."
			}
			log.Printf("  Result %d [score=%.4f][%s]: %s", i+1, r.SimilarityScore, r.SourceType, snippet)
		}
	}

	if result.ChainsFound > 0 {
		result.CCR = float64(result.EventsInjected) / float64(result.ChainsFound)
	}
	result.OracleCCRPass = result.CCR >= Oracle.CCRPassThreshold

	log.Println("─── Probe: CLR — direct MongoDB co-location check ───")
	clrPassed := e.runCLRSpotCheck(session.SessionID, nil, "clean")
	result.CLRSampleSize = Oracle.CLRSampleSize
	result.CLRPassed = clrPassed
	if Oracle.CLRSampleSize > 0 {
		result.CLRRate = float64(clrPassed) / float64(Oracle.CLRSampleSize)
	}
	result.OracleCLRPass = result.CLRRate >= Oracle.CLRPassThreshold

	return e.printAndSave(result)
}

// latestResultFile returns the path of the most recent results file for a given mode.
func latestResultFile(mode string) string {
	entries, err := os.ReadDir("results")
	if err != nil {
		return ""
	}
	prefix := "h5_" + mode + "_"
	latest := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".json") {
			if e.Name() > latest {
				latest = e.Name()
			}
		}
	}
	if latest == "" {
		return ""
	}
	return filepath.Join("results", latest)
}

// ─────────────────────────────────────────────
//  Core: inject + probe + CCR
// ─────────────────────────────────────────────

// probeQueries returns the GetContext and SearchMemory query strings for a given mode.
// The control mode uses hotel-domain queries — the CI/deployment queries used for
// clean/corrupt would return 0 results against hotel booking chains (which is correct
// isolation behavior but breaks CCR measurement).
func probeQueries(mode string) (contextQuery, searchQuery string) {
	if mode == "control" {
		return "hotel booking reservation room availability check in",
			"hotel room booking confirmation reference number stay"
	}
	return "deployment failure API error container",
		"billing API deployment container failed health check"
}

func (e *Experiment) ingestAndProbe(sessionID string, triplets []Triplet, mode string, corruptedCount int) (*Result, error) {
	result := &Result{
		Mode:           mode,
		RunAt:          time.Now(),
		SessionID:      sessionID,
		TripletsLoaded: len(triplets),
		CorruptedJSONs: corruptedCount,
	}

	events := buildEvents(triplets, mode)
	result.EventsInjected = len(events)

	log.Printf("Injecting %d events in triplet order (A→B→C per issue)...", len(events))
	log.Println("Each triplet shares workflow_id so the worker keeps all 3 formats in one chain.")
	fmt.Println()

	for i, ev := range events {
		resp, err := e.client.StoreEvent(sessionID, ev)
		if err != nil {
			log.Printf("  ⚠️  Event %d [%s format=%s] failed: %v",
				i+1, ev.Metadata["issue_id"], ev.Metadata["h5_format"], err)
			result.ExtractionErrors++
			continue
		}

		log.Printf("  ✅ [%d/%d] %s | format=%-12s | id=%s",
			i+1, len(events), ev.Metadata["issue_id"], ev.Metadata["h5_format"], resp.EventID)

		// Pause at every STMFlushThreshold boundary to let the worker process the queue.
		// The threshold is 20 events — matching STM_MAX_EVENTS_BEFORE_FLUSH in memory-os.
		if (i+1)%Oracle.STMFlushThreshold == 0 {
			log.Printf("  ⏳ STM flush boundary (%d events). Waiting 15s for worker...", i+1)
			time.Sleep(15 * time.Second)
		} else {
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Final wait for the last partial batch and chain embedding writes.
	log.Println()
	log.Println("⏳ Waiting 30s for final worker flush and embedding storage...")
	time.Sleep(30 * time.Second)

	// ── Probe 1: CCR — chain count via GetContext ──────────────────────────────
	contextQuery, searchQuery := probeQueries(mode)
	log.Println("─── Probe 1: CCR — chain count (GetContext) ───")
	log.Printf("  Query: %q", contextQuery)
	ctx, err := e.client.GetContext(sessionID, athena.GetContextRequest{
		Limit: 200,
		Query: contextQuery,
	})
	if err != nil {
		log.Printf("⚠️  GetContext failed: %v", err)
		result.Notes = append(result.Notes, "GetContext failed: "+err.Error())
	} else {
		result.ChainsFound = len(ctx.RelevantPages)
		result.STMEventsFound = len(ctx.STMEvents)
		log.Printf("  STM events:  %d", result.STMEventsFound)
		log.Printf("  MTM chains:  %d  (expected ~%d)", result.ChainsFound, Oracle.ExpectedChains)
		for i, p := range ctx.RelevantPages {
			log.Printf("  Chain %3d:   topic=%s", i+1, p.Topic)
		}
	}

	// ── Probe 2: CLR spot-check — cross-format co-location ────────────────────
	// For each sampled issue, search using Format A's content.
	// If the returned chain summary contains keywords from Format B (JSON) or
	// Format C (log), all 3 formats co-located in the same chain.
	// This is an automated proxy — full CLR requires chainId written back to
	// in_mtm events (pending worker fix).
	log.Println("─── Probe 2: CLR spot-check (cross-format co-location) ───")
	log.Printf("  Sampling %d issues to verify B+C co-locate with A", Oracle.CLRSampleSize)
	clrPassed := e.runCLRSpotCheck(sessionID, triplets, mode)
	result.CLRSampleSize = Oracle.CLRSampleSize
	result.CLRPassed = clrPassed
	if Oracle.CLRSampleSize > 0 {
		result.CLRRate = float64(clrPassed) / float64(Oracle.CLRSampleSize)
	}
	result.OracleCLRPass = result.CLRRate >= Oracle.CLRPassThreshold
	log.Printf("  CLR spot-check: %d/%d passed (%.0f%%) — pass threshold %.0f%%",
		clrPassed, Oracle.CLRSampleSize, result.CLRRate*100, Oracle.CLRPassThreshold*100)

	// ── Probe 3: SearchMemory general spot-check ──────────────────────────────
	log.Println("─── Probe 3: SearchMemory general retrieval ───")
	log.Printf("  Query: %q", searchQuery)
	search, err := e.client.SearchMemory(sessionID, athena.SearchMemoryRequest{
		Query: searchQuery,
		Limit: 5,
	})
	if err != nil {
		log.Printf("⚠️  SearchMemory failed: %v", err)
	} else {
		log.Printf("  SearchMemory returned %d results", len(search.Results))
		for i, r := range search.Results {
			snippet := r.Content
			if len(snippet) > 120 {
				snippet = snippet[:120] + "..."
			}
			log.Printf("  Result %d [score=%.4f][%s]: %s", i+1, r.SimilarityScore, r.SourceType, snippet)
		}
	}

	// ── Calculate metrics ─────────────────────────────────────────────────────
	if result.ChainsFound > 0 {
		result.CCR = float64(result.EventsInjected) / float64(result.ChainsFound)
	}
	result.OracleCCRPass = result.CCR >= Oracle.CCRPassThreshold

	return result, nil
}

// runCLRSpotCheck queries MongoDB directly to measure the true Co-location Rate.
// For each issue, it finds all cognitive_events with the matching workflow_id and
// checks whether all 3 formats (A_chat, B_json, C_log) share the same chainId.
// This is the authoritative CLR — no proxy, no keyword matching in summaries.
func (e *Experiment) runCLRSpotCheck(sessionID string, triplets []Triplet, mode string) int {
	mongoURI := os.Getenv("MONGODB_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://admin:admin123@localhost:27017/memory_os?authSource=admin"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Printf("  CLR: ⚠️  MongoDB connect failed: %v — falling back to 0", err)
		return 0
	}
	defer client.Disconnect(ctx)

	coll := client.Database("memory_os").Collection("cognitive_events")

	type result struct {
		WorkflowID string   `bson:"_id"`
		ChainIDs   []string `bson:"chainIds"`
		Formats    []string `bson:"formats"`
		Count      int      `bson:"count"`
	}

	pipeline := bson.A{
		bson.M{"$match": bson.M{"metadata.h5_benchmark": mode}},
		bson.M{"$group": bson.M{
			"_id":      "$metadata.workflow_id",
			"chainIds": bson.M{"$addToSet": "$chainId"},
			"formats":  bson.M{"$addToSet": "$metadata.h5_format"},
			"count":    bson.M{"$sum": 1},
		}},
		bson.M{"$sort": bson.M{"_id": 1}},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		log.Printf("  CLR: ⚠️  MongoDB aggregate failed: %v", err)
		return 0
	}
	defer cursor.Close(ctx)

	var results []result
	if err := cursor.All(ctx, &results); err != nil {
		log.Printf("  CLR: ⚠️  cursor decode failed: %v", err)
		return 0
	}

	if len(results) == 0 {
		log.Printf("  CLR: ⚠️  no events found in MongoDB for benchmark=%q", mode)
		return 0
	}

	passed := 0
	split := 0
	for _, r := range results {
		colocated := len(r.ChainIDs) == 1 && len(r.Formats) == 3
		if colocated {
			passed++
		} else {
			split++
		}
	}

	log.Printf("  CLR [MongoDB]: %d/%d issues have all 3 formats in same chain", passed, len(results))
	log.Printf("  CLR [MongoDB]: %d issues split across multiple chains", split)

	return passed
}


// ─────────────────────────────────────────────
//  Data loading
// ─────────────────────────────────────────────

// loadTriplets reads matched files from the three format subdirectories.
// Files are matched by stem: chat/issue_042.txt ↔ json/issue_042.json ↔ logs/issue_042.log
// Returns triplets sorted by stem so injection order is deterministic across runs.
func loadTriplets(dataDir, chatSub, jsonSub, logSub string) ([]Triplet, error) {
	chatDir := filepath.Join(dataDir, chatSub)
	jsonDir := filepath.Join(dataDir, jsonSub)
	logDir := filepath.Join(dataDir, logSub)

	// Read stems from the chat directory (it drives the list)
	stems, err := readStems(chatDir)
	if err != nil {
		return nil, fmt.Errorf("reading chat dir %s: %w", chatDir, err)
	}
	if len(stems) == 0 {
		return nil, fmt.Errorf("no files found in %s — have you downloaded the dataset?", chatDir)
	}

	triplets := make([]Triplet, 0, len(stems))
	skipped := 0

	for _, stem := range stems {
		chat, err := readFile(chatDir, stem)
		if err != nil {
			log.Printf("  ⚠️  Skipping %s — missing chat file: %v", stem, err)
			skipped++
			continue
		}
		jsonContent, err := readFile(jsonDir, stem)
		if err != nil {
			log.Printf("  ⚠️  Skipping %s — missing json file: %v", stem, err)
			skipped++
			continue
		}
		logContent, err := readFile(logDir, stem)
		if err != nil {
			log.Printf("  ⚠️  Skipping %s — missing log file: %v", stem, err)
			skipped++
			continue
		}

		triplets = append(triplets, Triplet{
			IssueID: stem,
			Chat:    chat,
			JSON:    jsonContent,
			Log:     logContent,
		})
	}

	if skipped > 0 {
		log.Printf("⚠️  Skipped %d stems (missing files in one or more format dirs)", skipped)
	}

	return triplets, nil
}

// readStems returns sorted file stems (no extension) from a directory.
func readStems(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	stems := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		stems = append(stems, stem)
	}
	sort.Strings(stems)
	return stems, nil
}

// readFile finds the first file in dir matching stem (any extension) and returns its content.
func readFile(dir, stem string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())) == stem {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return "", err
			}
			return string(data), nil
		}
	}
	return "", fmt.Errorf("no file with stem %q in %s", stem, dir)
}

// ─────────────────────────────────────────────
//  Event construction
// ─────────────────────────────────────────────

// buildEvents converts triplets into a flat StoreEvent slice in TRIPLET ORDER.
// Order: A_001 → B_001 → C_001 → A_002 → B_002 → C_002 → ...
//
// How co-location works:
//   Format A (role=user) is the anchor. The STM worker compares consecutive
//   user-role messages for topic similarity. When issue_N's A and issue_N+1's A
//   are semantically different (chain break), the worker partitions the STM:
//   everything from issue_N's A onwards (including its B and C) goes into MTM
//   as one chain. B and C co-locate with their A because they sit between
//   two consecutive A messages in the STM buffer.
//
//   The workflow_id + execution_id metadata on all 3 events serves two purposes:
//   1. Activates the worker's binding guard — if two consecutive user messages
//      share the same workflow_id, no chain break is triggered (prevents splits
//      within a single workflow run that spans multiple user turns).
//   2. Enables CLR measurement — we can query in_mtm events by workflow_id to
//      verify all 3 formats reached MTM.
func buildEvents(triplets []Triplet, benchmark string) []athena.StoreEventRequest {
	events := make([]athena.StoreEventRequest, 0, len(triplets)*3)
	for _, t := range triplets {
		// Format A — human prose (GitHub Issue comment / Colabra chat message)
		// role=user → evaluated by worker for chain break detection
		events = append(events, athena.StoreEventRequest{
			Role:    "user",
			Type:    "message",
			Content: t.Chat,
			Metadata: map[string]string{
				"h5_format":    "A_chat",
				"h5_benchmark": benchmark,
				"issue_id":     t.IssueID,
				"workflow_id":  t.IssueID,
				"execution_id": t.IssueID,
				"source":       "colabra_chat",
			},
		})

		// Format B — structured JSON payload (GitHub Actions / tool call result)
		// role=agent, type=action → not evaluated for chain break, rides with Format A
		events = append(events, athena.StoreEventRequest{
			Role:    "agent",
			Type:    "action",
			Content: t.JSON,
			Metadata: map[string]string{
				"h5_format":    "B_json",
				"h5_benchmark": benchmark,
				"issue_id":     t.IssueID,
				"workflow_id":  t.IssueID,
				"execution_id": t.IssueID,
				"source":       "github_actions",
			},
		})

		// Format C — system execution log (CI/CD output / automation trace)
		// role=agent, type=observation → not evaluated for chain break, rides with Format A
		events = append(events, athena.StoreEventRequest{
			Role:    "agent",
			Type:    "observation",
			Content: t.Log,
			Metadata: map[string]string{
				"h5_format":    "C_log",
				"h5_benchmark": benchmark,
				"issue_id":     t.IssueID,
				"workflow_id":  t.IssueID,
				"execution_id": t.IssueID,
				"source":       "automation_logs",
			},
		})
	}
	return events
}

// ─────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────

func (e *Experiment) createSession(tenantID, userID, agentID string) (*athena.CreateSessionResponse, error) {
	session, err := e.client.CreateSession(athena.CreateSessionRequest{
		TenantID: tenantID,
		UserID:   userID,
		AgentID:  agentID,
		Metadata: map[string]string{
			"experiment": "h5_modality_agnostic",
			"origin":     "industry_benchmark",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	log.Printf("✅ Session created: %s", session.SessionID)
	return session, nil
}

// truncateJSON cuts a JSON string at a random mid-field position,
// producing structurally invalid JSON that still contains readable keywords.
// Uses a fixed offset (60% through the string) for reproducibility.
func truncateJSON(s string) string {
	if len(s) < 20 {
		return s[:len(s)/2]
	}
	cutAt := int(float64(len(s)) * 0.6)
	return s[:cutAt]
}

// printAndSave logs the final result summary and writes it to results/.
func (e *Experiment) printAndSave(result *Result) error {
	fmt.Println()
	log.Println("════════════════ RESULT ════════════════")
	log.Printf("Mode:            %s", result.Mode)
	log.Printf("Triplets loaded: %d", result.TripletsLoaded)
	log.Printf("Events injected: %d  (3 per triplet: A=user B=agent C=agent)", result.EventsInjected)
	if result.CorruptedJSONs > 0 {
		log.Printf("Corrupted JSONs: %d (Format B)", result.CorruptedJSONs)
	}
	log.Printf("Chains found:    %d  (expected ~%d)", result.ChainsFound, Oracle.ExpectedChains)
	log.Printf("CCR:             %.2f  (pass ≥ %.1f) — secondary metric", result.CCR, Oracle.CCRPassThreshold)

	if result.OracleCCRPass {
		log.Println("Oracle CCR:      ✅ PASS")
	} else {
		log.Println("Oracle CCR:      ❌ FAIL")
		log.Printf("               Got %.2f, need ≥ %.1f", result.CCR, Oracle.CCRPassThreshold)
		if result.CCR < 2.0 {
			log.Println("               CCR near 1.0 → events are NOT consolidating.")
			log.Println("               Check: are issues semantically distinct across topics?")
			log.Println("               Check: is CHAIN_SIM_HIGH too tight? Check worker logs.")
		}
	}

	log.Printf("CLR spot-check:  %d/%d (%.0f%%)  (pass ≥ %.0f%%) — primary H5 metric",
		result.CLRPassed, result.CLRSampleSize, result.CLRRate*100, Oracle.CLRPassThreshold*100)
	if result.OracleCLRPass {
		log.Println("Oracle CLR:      ✅ PASS")
	} else {
		log.Println("Oracle CLR:      ❌ FAIL")
		log.Printf("               Got %.0f%%, need ≥ %.0f%%", result.CLRRate*100, Oracle.CLRPassThreshold*100)
		log.Println("               Check: did chain breaks fire? Are B+C events in same STM batch as A?")
		log.Println("               Note: full CLR needs worker to write chainId back to in_mtm events.")
	}

	if len(result.Notes) > 0 {
		log.Println("Notes:")
		for _, n := range result.Notes {
			log.Printf("  - %s", n)
		}
	}

	log.Println()
	log.Println("Metrics to record in RESEARCH_HYPOTHESES.md:")
	log.Printf("  MTM chains formed:                %d", result.ChainsFound)
	log.Printf("  CCR (events/chains):              %.2f", result.CCR)
	log.Printf("  CLR spot-check (%d issues):       %.0f%%", result.CLRSampleSize, result.CLRRate*100)
	log.Printf("  Extraction errors:                %d", result.ExtractionErrors)
	log.Println("════════════════════════════════════════")

	// Save to results/
	return saveResult(result)
}

func saveResult(result *Result) error {
	if err := os.MkdirAll("results", 0755); err != nil {
		return err
	}
	filename := fmt.Sprintf("results/h5_%s_%s.json", result.Mode, result.RunAt.Format("20060102_150405"))
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return err
	}
	log.Printf("Results saved → %s", filename)
	return nil
}
