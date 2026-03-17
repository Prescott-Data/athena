package h5

// Oracle is the pre-declared ground truth for Hypothesis 5.
//
// These numbers are locked before the experiment runs.
// You do NOT adjust them after seeing results — that would invalidate the research.
//
// Design rationale (2026-03-14):
//   Athena receives events from multiple services in different formats.
//   Each triplet simulates 3 services reporting on the same workflow event:
//     Format A (role=user, type=message)      — human chat / Colabra message
//     Format B (role=agent, type=action)      — structured JSON / tool call result
//     Format C (role=agent, type=observation) — system log / automation trace
//
//   All 3 formats share workflow_id = execution_id = issue_id. This activates
//   the STM worker's binding guard (keeps same-workflow events in one chain)
//   and enables CLR measurement via direct MongoDB query on cognitive_events.
//
//   Injection order is TRIPLET-FIRST: A_001→B_001→C_001→A_002→B_002→C_002→...
//   When the worker detects a chain break between A_001 and A_002 (different topics),
//   everything from A_001's boundary (including B_001 and C_001) goes to MTM together.
//   This is the co-location mechanism for the H5 hypothesis.
//
// Two oracle metrics:
//
//   CCR (secondary): events / chains — proves topic-based consolidation across flush cycles.
//     Null hypothesis (no consolidation):
//       300 events / 15 flush cycles = ~20 events per flush
//       ~6–7 complete triplets per flush → ~6–7 new chains (one per issue, no consolidation)
//       Total: ~100 chains → CCR = 300/100 = 3.0
//     Threshold = 3× null = 10.0. Any CCR ≥ 10 proves Athena is consolidating events
//     across flush cycles into semantic clusters rather than one chain per triplet.
//
//   CLR (primary): % of issues where all 3 formats share the same chainId in MongoDB.
//     Measured directly from cognitive_events — no proxy, no keyword matching.
//     Expected splits come from STM batch boundaries:
//       300 events / 20 per flush = 15 flush boundaries
//       ~1 triplet straddles each boundary → ~15 expected splits out of 100
//     Threshold = 75%: allows for up to 25 splits (15 boundary + ~10 margin for
//     topic similarity edge cases). Getting ≥ 75% proves the co-location mechanism
//     works for the large majority of events despite flush boundary effects.
var Oracle = struct {
	// Data volume
	EventsPerFormat  int // files per format directory (chat, json, logs)
	TotalEventsClean int // total events injected in --mode clean (3 per triplet)

	// STM behaviour
	STMFlushThreshold   int // events before worker force-flushes STM into MTM
	ExpectedFlushCycles int // TotalEventsClean / STMFlushThreshold

	// Expected outcome
	// GitHub data has ~5 semantic topic types (deployment, API, security, DB, infra).
	// Athena groups by cosine similarity → expect 5–15 chains across 100 issues.
	ExpectedChains int

	// Injection rule
	InjectionOrder string // triplet-first order is required for co-location

	// CCR threshold (secondary metric)
	// Null hypothesis CCR = 3.0 (no consolidation across flush cycles).
	// Threshold = 3× null = 10.0. Proves semantic consolidation is happening.
	CCRPassThreshold float64

	// CLR threshold (primary H5 metric)
	// Direct MongoDB measurement: % of issues where all 3 formats share the same chainId.
	// Threshold = 75%: accounts for ~15 STM boundary splits + ~10 edge case margin.
	CLRPassThreshold float64
	CLRSampleSize    int // full population — all 100 issues checked via MongoDB

	// ERS threshold (Benchmark 2 — corrupted JSON)
	// Corrupting Format B must not break chain formation or CLR.
	ERSPassThreshold float64

	// Corruption config (Benchmark 2)
	CorruptionRate float64
	CorruptedJSONs int
}{
	EventsPerFormat:     100,
	TotalEventsClean:    300,
	STMFlushThreshold:   20,
	ExpectedFlushCycles: 15,
	ExpectedChains:      10,
	InjectionOrder:      "triplet-first (A→B→C per issue)",
	CCRPassThreshold:    10.0,
	CLRPassThreshold:    0.75,
	CLRSampleSize:       100, // full population — MongoDB query checks all 100 issues
	ERSPassThreshold:    0.80,
	CorruptionRate:      0.20,
	CorruptedJSONs:      20,
}
