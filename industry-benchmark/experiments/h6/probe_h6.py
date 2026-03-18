"""
H6 — Temporal Override Probe
=============================
Sends 5 probe queries to Athena's SearchMemory endpoint and scores the responses
against the H6 oracle thresholds (locked before injection).

Usage:
    python probe_h6.py --url URL --phase pre   # before contradiction — expect hawkish stance
    python probe_h6.py --url URL --phase post  # after contradiction  — expect rate hike confirmed

Oracle thresholds (locked):
    Correction Accuracy  >= 0.80  (at least 4/5 queries return new truth keywords)
    Hedging Frequency    <= 0.10  (at most 0-1 hedging phrases across all responses)

Scoring:
    A response is "corrected" if it contains any POST_KEYWORDS.
    A response is "hedging" if it contains any HEDGE_KEYWORDS.
    Correction Accuracy  = corrected_count / total_queries
    Hedging Frequency    = hedging_count   / total_queries
"""

import argparse
import json
import sys
import time

import requests

# ── Config ────────────────────────────────────────────────────────────────────

TENANT_ID = "tenant_h6_experiment"
USER_ID   = "user_h6_bullock_watcher"
AGENT_ID  = "h6_probe"

PROBE_QUERIES = [
    "What is the RBA's current interest rate stance?",
    "Has Michele Bullock's RBA changed interest rates?",
    "What is the current state of Australian interest rates?",
    "What did the Reserve Bank of Australia decide on rates?",
    "What is Michele Bullock's position on interest rates?",
]

# Pre-contradiction: expect hawkish/holding belief
PRE_KEYWORDS = [
    "hold", "hike", "hawkish", "expected", "bets", "rate hike",
    "expectation", "surge", "pressure", "tighten", "unchanged",
]

# Post-contradiction: expect the new truth (rate was lifted/raised — confirmed action)
# Deliberately excludes "increase"/"hike" which also appear in belief chain summaries
# (e.g. "anticipated rate increases", "rate hike bets").
# These keywords signal a CONFIRMED action, not an expectation.
POST_KEYWORDS = [
    "lifted", "raised interest", "decision to raise", "rate hike confirmed",
    "to avoid recession", "pre-emptive", "lifted interest rates",
    "raised the cash rate", "confirmed the rate",
]

# Hedging phrases — Athena should commit to the override, not hedge
HEDGE_KEYWORDS = [
    "might have", "may have", "possibly", "could have", "it is unclear",
    "not confirmed", "rumored", "allegedly", "some reports suggest",
    "expected to", "anticipated to",
]

ORACLE = {
    "pre":  {"metric": "belief_presence",    "threshold": 0.80, "direction": ">="},
    "post": {"metric": "correction_accuracy","threshold": 0.80, "direction": ">="},
    "hedge_max": 0.10,
}

# ── API helpers ───────────────────────────────────────────────────────────────

def create_session(base_url: str, api_key: str) -> str:
    resp = requests.post(
        f"{base_url}/api/v1/sessions",
        json={
            "tenant_id": TENANT_ID,
            "user_id":   USER_ID,
            "agent_id":  AGENT_ID,
            "metadata":  {"experiment": "h6", "role": "probe"},
        },
        headers={"X-API-Key": api_key},
        timeout=15,
    )
    resp.raise_for_status()
    return resp.json()["sessionId"]


def search_memory(base_url: str, api_key: str, session_id: str, query: str) -> list[dict]:
    resp = requests.post(
        f"{base_url}/api/v1/sessions/{session_id}/context/search",
        json={
            "session_id":           session_id,
            "query":                query,
            "limit":                5,
            "similarity_threshold": 0.0,
        },
        headers={"X-API-Key": api_key, "Content-Type": "application/json"},
        timeout=20,
    )
    resp.raise_for_status()
    return resp.json().get("results", [])


# ── Scoring ───────────────────────────────────────────────────────────────────

def score_response(content: str, phase: str) -> dict:
    content_lower = content.lower()
    keywords      = PRE_KEYWORDS if phase == "pre" else POST_KEYWORDS
    matched       = [kw for kw in keywords if kw in content_lower]
    hedges        = [kw for kw in HEDGE_KEYWORDS if kw in content_lower]
    return {
        "hit":       bool(matched),
        "hedging":   bool(hedges),
        "matched":   matched,
        "hedges":    hedges,
    }


# ── Main ──────────────────────────────────────────────────────────────────────

def run_probe(base_url: str, api_key: str, phase: str, verbose: bool) -> None:
    print("=" * 65)
    print(f"H6 Probe — phase: {phase.upper()}")
    print("=" * 65)
    print(f"  Endpoint : {base_url}")
    print(f"  Tenant   : {TENANT_ID}")
    print()

    session_id = create_session(base_url, api_key)
    print(f"  Probe session: {session_id[:16]}...")
    print()

    hits    = 0
    hedges  = 0
    results = []

    for i, query in enumerate(PROBE_QUERIES, 1):
        print(f"  [{i}/{len(PROBE_QUERIES)}] {query}")
        try:
            search_results = search_memory(base_url, api_key, session_id, query)
        except requests.exceptions.RequestException as e:
            print(f"         ERROR: {e}")
            results.append({"query": query, "error": str(e), "hit": False, "hedging": False})
            continue

        if not search_results:
            print(f"         [NO RESULTS] — memory returned empty")
            results.append({"query": query, "content": "", "hit": False, "hedging": False,
                            "matched": [], "hedges": [], "sources": []})
            continue

        # Concatenate all result content for scoring
        combined = " ".join(r.get("content", "") for r in search_results)
        score    = score_response(combined, phase)

        hit_marker = "✓" if score["hit"] else "✗"
        hedge_marker = " [HEDGE]" if score["hedging"] else ""
        print(f"         {hit_marker} hit={score['hit']}{hedge_marker}")

        if verbose:
            for j, r in enumerate(search_results, 1):
                src  = r.get("source_type", "?")
                sim  = r.get("similarity_score", 0)
                snip = r.get("content", "")[:120].replace("\n", " ")
                print(f"           [{j}] {src} sim={sim:.3f} | {snip}...")
            if score["matched"]:
                print(f"           Keywords matched: {score['matched']}")
            if score["hedges"]:
                print(f"           Hedges found:     {score['hedges']}")

        if score["hit"]:
            hits += 1
        if score["hedging"]:
            hedges += 1

        results.append({
            "query":   query,
            "content": combined[:500],
            "sources": [{"type": r.get("source_type"), "sim": r.get("similarity_score")}
                        for r in search_results],
            **score,
        })
        time.sleep(0.3)

    # ── Scores ────────────────────────────────────────────────────────────────
    n = len(PROBE_QUERIES)
    accuracy  = hits   / n
    hedge_rate = hedges / n

    oracle_threshold = ORACLE[phase]["threshold"]
    oracle_pass      = accuracy >= oracle_threshold
    hedge_pass       = hedge_rate <= ORACLE["hedge_max"]

    print()
    print("─" * 65)
    label = "Belief Presence" if phase == "pre" else "Correction Accuracy"
    print(f"  {label:<25}: {hits}/{n} = {accuracy:.2f}  (threshold ≥ {oracle_threshold:.2f})  {'PASS ✓' if oracle_pass else 'FAIL ✗'}")
    print(f"  {'Hedging Frequency':<25}: {hedges}/{n} = {hedge_rate:.2f}  (threshold ≤ {ORACLE['hedge_max']:.2f})  {'PASS ✓' if hedge_pass else 'FAIL ✗'}")
    print("─" * 65)

    if phase == "post":
        overall = oracle_pass and hedge_pass
        print()
        print(f"  H6 RESULT: {'PASS ✓  — Athena correctly overrides entrenched belief' if overall else 'FAIL ✗  — Athena did NOT override entrenched belief within oracle bounds'}")
    else:
        print()
        if accuracy >= oracle_threshold:
            print(f"  Pre-probe PASS — belief is entrenched. Proceed to inject contradiction and run --phase post.")
        else:
            print(f"  Pre-probe FAIL — belief not well entrenched ({hits}/{n} queries hit).")
            print(f"  Check: did the promoter fire? Are chains in ArangoDB?")

    print("=" * 65)


def main():
    parser = argparse.ArgumentParser(description="H6 Temporal Override Probe")
    parser.add_argument("--url",     default="https://api.console.dromos.prescottdata.io/memory")
    parser.add_argument("--key",     default="default-api-key")
    parser.add_argument("--phase",   choices=["pre", "post"], required=True,
                        help="pre = verify belief entrenched; post = verify correction after contradiction")
    parser.add_argument("--verbose", action="store_true", help="Show retrieved content snippets")
    args = parser.parse_args()

    run_probe(args.url, args.key, args.phase, args.verbose)


if __name__ == "__main__":
    main()
