# Benchmarks

Athena's memory claims are testable, and the repo ships the harness that tests them: `industry-benchmark/`, a Go suite that injects controlled event streams into a live deployment and measures what the pipeline does with them.

## H5: Modality-Agnostic Synthesis

The flagship experiment tests whether Athena consolidates **multi-format event streams** into coherent semantic memory: the property that makes one memory serve chat agents and automation pipelines simultaneously.

### Design

100 real GitHub issues, each expressed in three formats:

| Format | Content | Simulates |
|---|---|---|
| A: chat | Human prose (issue comments) | Conversational events |
| B: json | Structured `workflow_run` payloads | Automation telemetry |
| C: logs | CI/CD log lines | System observations |

All three formats of an issue share `workflow_id = execution_id = issue_id`, and the 300 events are injected triplet-first (A→B→C per issue). The oracle is locked before the run: 300 events, a 20-event STM flush threshold, ~15 flush cycles, ~10 expected semantic chains.

### Metrics

**CLR (Co-Location Rate)**, primary: the share of issues whose three formats land in the *same* cognitive chain, measured directly against `cognitive_events.chainId` in MongoDB.

$$
\text{CLR} = \frac{\left|\{\,i : \text{all 3 formats of } i \text{ share one } chainId\,\}\right|}{100} \;\; \geq \; 0.75 \text{ to pass}
$$

**CCR (Chain Consolidation Ratio)**, secondary: events per chain. The null hypothesis (no semantic consolidation, one chain per format burst) predicts CCR ≈ 3.0; the pass threshold is **≥ 10.0**, i.e. at least 3× better than format-blind chunking.

### Modes

| Mode | Purpose |
|---|---|
| `pilot` | Dry run, baseline measurement |
| `clean` | The main experiment: all 300 events |
| `corrupt` | 20% of JSON events corrupted; measures extraction resilience (ERS ≥ 80%) |
| `control` | Different-domain issues that should **not** consolidate (guards against over-merging) |
| `probe` | Reads final metrics from the database without injecting |

### Running it

```bash
cd industry-benchmark
go run main.go --exp h5 --mode clean --data ./data/h5 --url http://localhost:8080
```

Results land in `results/h5_<mode>_<timestamp>.json` with chains found, CLR, CCR, and pass/fail against the locked thresholds. The harness needs a running Athena with workers enabled and a real LLM key; expect meaningful LLM spend for a full `clean` run.

## Research paper

The architectural background (the orbital-mechanics memory metaphor, heat-decay design, and the evolution from event logs to cognitive chains) is written up in the repository's research paper and architecture notes:

- [Research paper](../research_paper.md)
- [Architecture evolution notes](../ARCHITECTURE_EVOLUTION.md)

!!! note
    Benchmark results are environment-dependent (LLM model, thresholds, hardware). Publishable numbers should always cite the mode, model, and threshold configuration of the run that produced them.
