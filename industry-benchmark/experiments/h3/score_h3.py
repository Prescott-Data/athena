"""
H3 — Asymptotic Latency Scorer
================================
Reads tier result files produced by load_test_h3.js and computes
the final H3 pass/fail against oracle thresholds.

Oracle (locked before experiment):
  Tier 2 pass : P95(T2) / P95(T1) < 1.5
  Error rate  : < 1% on all tiers

Evals:
  Latency Scalability Factor α = slope of P95 = f(log10(nodes))
  α ≈ 0 → flat (PASS),  α >> 0 → linear scan (FAIL)

Usage:
    python score_h3.py                        # reads h3_results_tier1.json + tier2.json
    python score_h3.py --tiers 1 2            # explicit tiers
    python score_h3.py --tiers 1 2 3          # all three tiers (prod run)
"""

import argparse
import json
import math
import sys
from pathlib import Path


ORACLE = {
    "t2_ratio_max": 1.5,   # P95(T2) / P95(T1) < 1.5
    "t3_ratio_max": 3.0,   # P95(T3) / P95(T1) < 3.0
    "error_rate_max": 1.0, # % errors < 1%
}

TIER_NODES = {1: 10_000, 2: 100_000, 3: 1_000_000}


def load_result(tier: int) -> dict:
    path = Path(f"h3_results_tier{tier}.json")
    if not path.exists():
        print(f"  ERROR: {path} not found. Run the Tier {tier} load test first.")
        sys.exit(1)
    with open(path) as f:
        return json.load(f)


def compute_alpha(tiers: list, results: dict) -> float:
    """
    Fit P95 = α * log10(nodes) + b.
    Returns α (slope). Near 0 = asymptotic, large = linear.
    """
    if len(tiers) < 2:
        return 0.0
    xs = [math.log10(TIER_NODES[t]) for t in tiers]
    ys = [results[t]["p95_ms"] for t in tiers]
    n  = len(xs)
    mx = sum(xs) / n
    my = sum(ys) / n
    num = sum((xs[i] - mx) * (ys[i] - my) for i in range(n))
    den = sum((xs[i] - mx) ** 2 for i in range(n))
    return round(num / den, 4) if den != 0 else 0.0


def main():
    parser = argparse.ArgumentParser(description="H3 Latency Scorer")
    parser.add_argument("--tiers", nargs="+", type=int, default=[1, 2],
                        choices=[1, 2, 3], help="Tiers to score")
    args = parser.parse_args()

    tiers = sorted(args.tiers)
    results = {t: load_result(t) for t in tiers}

    t1_p95 = results[1]["p95_ms"] if 1 in results else None

    print("=" * 65)
    print("  H3 — Asymptotic Latency Results")
    print("=" * 65)
    print()

    # ── Per-tier table ────────────────────────────────────────────────
    print(f"  {'Tier':<6} {'Nodes':>10}  {'P95 (ms)':>10}  {'P99 (ms)':>10}  {'Errors':>8}  {'Ratio vs T1':>12}")
    print("  " + "-" * 62)

    tier_pass = {}
    for t in tiers:
        r     = results[t]
        nodes = TIER_NODES[t]
        ratio = round(r["p95_ms"] / t1_p95, 2) if t1_p95 and t != 1 else 1.0
        err   = r["error_rate_pct"]

        if t == 1:
            oracle_pass = err < ORACLE["error_rate_max"]
            ratio_str   = "  (baseline)"
        elif t == 2:
            oracle_pass = ratio < ORACLE["t2_ratio_max"] and err < ORACLE["error_rate_max"]
            ratio_str   = f"  {ratio:.2f}x  {'✓' if ratio < ORACLE['t2_ratio_max'] else '✗'} (<{ORACLE['t2_ratio_max']})"
        else:
            oracle_pass = ratio < ORACLE["t3_ratio_max"] and err < ORACLE["error_rate_max"]
            ratio_str   = f"  {ratio:.2f}x  {'✓' if ratio < ORACLE['t3_ratio_max'] else '✗'} (<{ORACLE['t3_ratio_max']})"

        tier_pass[t] = oracle_pass
        status = "PASS ✓" if oracle_pass else "FAIL ✗"
        print(f"  T{t:<5} {nodes:>10,}  {r['p95_ms']:>10}  {r['p99_ms']:>10}  {err:>7.2f}%  {ratio_str}")

    print()

    # ── Scalability factor α ──────────────────────────────────────────
    alpha = compute_alpha(tiers, results)
    alpha_verdict = "near-zero (asymptotic ✓)" if abs(alpha) < 50 else "high (linear scan risk ✗)"
    print(f"  Latency Scalability Factor α : {alpha} ms / decade")
    print(f"  Interpretation               : {alpha_verdict}")
    print()

    # ── Overall verdict ───────────────────────────────────────────────
    overall = all(tier_pass.values())
    print("-" * 65)
    print(f"  H3 RESULT: {'PASS ✓  — Retrieval latency is asymptotically flat' if overall else 'FAIL ✗  — Latency degraded beyond oracle threshold'}")
    print("=" * 65)

    if not overall:
        sys.exit(1)


if __name__ == "__main__":
    main()
