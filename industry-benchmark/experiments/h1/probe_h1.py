"""
H1 — Keplerian Survival Probe
==============================================
Queries MongoDB and ArangoDB to evaluate the H1 hypothesis:
  - Chain A (32 sessions, John & Maria persistent fact) survives 180 simulated days
  - Chain B (1 session, throwaway QA) is archived/purged
  - Noise compression: >= 90% of noise chains archived
  - Chain A heatScore >= MEMORY_OS_PROMOTER_THRESHOLD (0.30)
  - Chain B heatScore <= MTM_FREEZING_POINT (0.10)
  - Chain A appears in ArangoDB LTM

Usage:
    python probe_h1.py [--wait] [--verbose]

    --wait    : poll every 60s until promoter + archiver have both run
    --verbose : show per-chain heat scores
"""

import argparse
import time
import sys
from datetime import datetime, timezone

from pymongo import MongoClient
from arango import ArangoClient

# ── Config ────────────────────────────────────────────────────────────────────

MONGO_URI       = "mongodb://admin:admin123@localhost:27017/memory_os?authSource=admin"
MONGO_DB        = "memory_os"

ARANGO_URL      = "http://localhost:8529"
ARANGO_USER     = "root"
ARANGO_PASSWORD = "athena_dev"
ARANGO_DB       = "athena_ltm"

TENANT_ID       = "tenant_h1_experiment"

# Oracle thresholds (match server defaults)
PROMOTER_THRESHOLD  = 0.30   # MEMORY_OS_PROMOTER_THRESHOLD
FREEZING_POINT      = 0.10   # MTM_FREEZING_POINT
NOISE_PURGE_MIN     = 0.90   # must archive >= 90% of noise


# ── MongoDB helpers ───────────────────────────────────────────────────────────

def connect_mongo():
    client = MongoClient(MONGO_URI, serverSelectionTimeoutMS=5000)
    client.admin.command("ping")
    return client[MONGO_DB]


def get_chain_stats(db, label: str, agent_prefix: str = None) -> dict:
    """
    Return count, archived count, heat scores for chains matching label.

    Queries by metadata.chain_label OR (optionally) by agentId prefix.
    The OR is needed because ProcessMTMFormation does not copy session metadata
    into the new chain doc — so MTM-formed chains lack chain_label entirely.
    Using the agentId prefix (e.g. "h1_chain_a_s") captures both skeleton chains
    (which do have chain_label set at CreateSession time) and MTM-formed chains.
    """
    chains_col = db["cognitive_chains"]

    if agent_prefix:
        base_filter = {
            "tenantId": TENANT_ID,
            "$or": [
                {"metadata.chain_label": label},
                {"agentId": {"$regex": f"^{agent_prefix}"}},
            ]
        }
    else:
        base_filter = {"tenantId": TENANT_ID, "metadata.chain_label": label}

    total    = chains_col.count_documents(base_filter)
    archived = chains_col.count_documents({**base_filter, "status": "archived"})
    active   = chains_col.count_documents({**base_filter, "status": "active"})
    promoted = chains_col.count_documents({**base_filter, "status": "promoted"})

    heat_docs = list(chains_col.find(
        base_filter,
        {"heatScore": 1, "status": 1, "chainId": 1, "agentId": 1,
         "intrinsicImportance": 1, "lastEventAt": 1}
    ))
    heats = [d.get("heatScore") or 0.0 for d in heat_docs]

    return {
        "total":    total,
        "active":   active,
        "archived": archived,
        "promoted": promoted,
        "heats":    heats,
        "docs":     heat_docs,
    }


# ── ArangoDB helpers ──────────────────────────────────────────────────────────

def connect_arango():
    client = ArangoClient(hosts=ARANGO_URL)
    return client.db(ARANGO_DB, username=ARANGO_USER, password=ARANGO_PASSWORD)


def get_ltm_node_count(arango_db) -> dict:
    """Return counts of nodes in each LTM collection."""
    counts = {}
    for col in ["Identities", "Concepts", "Tools", "Projects"]:
        try:
            counts[col] = arango_db.collection(col).count()
        except Exception:
            counts[col] = -1
    return counts


def search_ltm_for_chain_a(arango_db, verbose: bool = False) -> list[str]:
    """
    Look for nodes related to John's campaign / community work
    (the persistent fact in Chain A — conv_idx=2 John & Maria).
    Returns list of matching node names.
    """
    keywords = ["john", "campaign", "community", "election", "politics",
                "volunteer", "council", "activism", "neighborhood", "proposal",
                "advocacy", "organizing", "initiative"]
    found = []
    for col in ["Identities", "Concepts", "Projects"]:
        try:
            cursor = arango_db.aql.execute(
                """
                FOR doc IN @@col
                  LET name_lower = LOWER(doc.name)
                  FILTER (
                    name_lower IN @kw
                    OR LENGTH(
                      FOR kw IN @kw
                        FILTER CONTAINS(name_lower, kw)
                        LIMIT 1
                        RETURN 1
                    ) > 0
                  )
                  RETURN doc.name
                """,
                bind_vars={"@col": col, "kw": keywords}
            )
            nodes = list(cursor)
            if verbose and nodes:
                print(f"  [{col}] matched: {nodes}")
            found.extend(nodes)
        except Exception as e:
            if verbose:
                print(f"  ArangoDB query error on {col}: {e}")
    return found


# ── Oracle evaluation ─────────────────────────────────────────────────────────

def evaluate(mongo_db, arango_db, verbose: bool) -> dict:
    """Run all checks and return results dict."""
    print("\nProbing MongoDB...")
    chain_a = get_chain_stats(mongo_db, "chain_a", agent_prefix="h1_chain_a_s")
    chain_b = get_chain_stats(mongo_db, "chain_b", agent_prefix="h1_chain_b")
    noise   = get_chain_stats(mongo_db, "noise",   agent_prefix="h1_noise_")

    # Heat scores
    chain_a_max_heat = max(chain_a["heats"]) if chain_a["heats"] else 0.0
    chain_a_avg_heat = sum(chain_a["heats"]) / len(chain_a["heats"]) if chain_a["heats"] else 0.0
    chain_b_heat     = chain_b["heats"][0] if chain_b["heats"] else 0.0

    # Noise purge rate
    noise_purge_rate = (chain_a["archived"] + noise["archived"]) / max(noise["total"] + chain_a["total"], 1)
    # More precisely: noise-specific
    noise_purge_rate = noise["archived"] / max(noise["total"], 1)

    # ArangoDB
    print("Probing ArangoDB LTM...")
    ltm_counts   = get_ltm_node_count(arango_db)
    ltm_total    = sum(v for v in ltm_counts.values() if v >= 0)
    chain_a_ltm  = search_ltm_for_chain_a(arango_db, verbose=verbose)

    results = {
        # Chain A
        "chain_a_total":       chain_a["total"],
        "chain_a_active":      chain_a["active"],
        "chain_a_promoted":    chain_a["promoted"],
        "chain_a_archived":    chain_a["archived"],
        "chain_a_max_heat":    round(chain_a_max_heat, 4),
        "chain_a_avg_heat":    round(chain_a_avg_heat, 4),
        "chain_a_in_ltm":      len(chain_a_ltm) > 0,
        "chain_a_ltm_nodes":   chain_a_ltm,
        # Chain B
        "chain_b_total":       chain_b["total"],
        "chain_b_archived":    chain_b["archived"],
        "chain_b_heat":        round(chain_b_heat, 4),
        # Noise
        "noise_total":         noise["total"],
        "noise_archived":      noise["archived"],
        "noise_active":        noise["active"],
        "noise_purge_rate":    round(noise_purge_rate, 4),
        # LTM
        "ltm_counts":          ltm_counts,
        "ltm_total_nodes":     ltm_total,
    }

    # Oracle pass/fail
    results["CHECK_chain_a_heat"]    = chain_a_max_heat >= PROMOTER_THRESHOLD
    results["CHECK_chain_b_purged"]  = chain_b["archived"] >= 1 or chain_b_heat <= FREEZING_POINT
    results["CHECK_noise_purge"]     = noise_purge_rate >= NOISE_PURGE_MIN
    # Note: promoter.go never writes status="promoted" — check ArangoDB directly
    results["CHECK_chain_a_ltm"]     = results["chain_a_in_ltm"]

    results["ALL_PASS"] = all([
        results["CHECK_chain_a_heat"],
        results["CHECK_chain_b_purged"],
        results["CHECK_noise_purge"],
        results["CHECK_chain_a_ltm"],
    ])

    return results


def print_results(r: dict, verbose: bool) -> None:
    print()
    print("=" * 65)
    print("H1 — KEPLERIAN SURVIVAL RESULTS")
    print("=" * 65)

    print(f"\n  Chain A  (conv_idx=2 — John & Maria, 32 sessions)")
    print(f"    Total chains  : {r['chain_a_total']}")
    print(f"    Active        : {r['chain_a_active']}")
    print(f"    Archived      : {r['chain_a_archived']}")
    print(f"    Max heatScore : {r['chain_a_max_heat']}  (threshold >= {PROMOTER_THRESHOLD})")
    print(f"    Avg heatScore : {r['chain_a_avg_heat']}")
    print(f"    In ArangoDB   : {r['chain_a_in_ltm']}  nodes={r['chain_a_ltm_nodes']}")

    print(f"\n  Chain B  (throwaway QA, 1 session)")
    print(f"    Total chains  : {r['chain_b_total']}")
    print(f"    Archived      : {r['chain_b_archived']}")
    print(f"    heatScore     : {r['chain_b_heat']}  (freezing <= {FREEZING_POINT})")

    print(f"\n  Noise    (1000 random interactions)")
    print(f"    Total chains  : {r['noise_total']}")
    print(f"    Active        : {r['noise_active']}")
    print(f"    Archived      : {r['noise_archived']}")
    print(f"    Purge rate    : {r['noise_purge_rate']:.1%}  (target >= {NOISE_PURGE_MIN:.0%})")

    print(f"\n  ArangoDB LTM")
    for col, count in r["ltm_counts"].items():
        print(f"    {col:<14}: {count}")
    print(f"    Total nodes   : {r['ltm_total_nodes']}")

    print()
    print("  Oracle Checks")
    checks = [
        ("Chain A heat >= 0.30",       "CHECK_chain_a_heat"),
        ("Chain B archived/cold",       "CHECK_chain_b_purged"),
        ("Noise purge >= 90%",          "CHECK_noise_purge"),
        ("Chain A in LTM (promoted)",   "CHECK_chain_a_ltm"),
    ]
    for label, key in checks:
        status = "PASS" if r[key] else "FAIL"
        print(f"    [{status}] {label}")

    print()
    overall = "PASS" if r["ALL_PASS"] else "FAIL"
    print(f"  H1 Overall: {overall}")

    # RAOT and NCE metrics
    raot = 1.0 if r["CHECK_chain_a_heat"] and r["CHECK_chain_a_ltm"] else 0.0
    nce  = r["noise_purge_rate"]
    print()
    print("  Benchmark Metrics")
    print(f"    RAOT (Recall Accuracy Over Time delta) : {raot:.0%}  (target 100%)")
    print(f"    NCE  (Noise Compression Efficiency)    : {nce:.1%}  (target >= 90%)")
    print("=" * 65)


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="H1 Keplerian Survival Probe")
    parser.add_argument("--wait",    action="store_true",
                        help="Poll every 60s until promoter + archiver have run")
    parser.add_argument("--verbose", action="store_true",
                        help="Show per-chain heat scores and AQL debug")
    args = parser.parse_args()

    print("Connecting to MongoDB...", end=" ", flush=True)
    try:
        mongo_db = connect_mongo()
        print("OK")
    except Exception as e:
        raise SystemExit(f"FAIL\n  {e}")

    print("Connecting to ArangoDB...", end=" ", flush=True)
    try:
        arango_db = connect_arango()
        print("OK")
    except Exception as e:
        print(f"WARN — ArangoDB unavailable: {e}")
        arango_db = None

    if args.wait:
        print("\nWaiting for promoter + archiver cycles...")
        while True:
            r = evaluate(mongo_db, arango_db or type("FakeDB", (), {})(), args.verbose)
            if r["chain_a_promoted"] > 0 or r["noise_archived"] > 10:
                print("  Cycles detected — running final probe.")
                break
            print(f"  [{datetime.now(timezone.utc).strftime('%H:%M:%S')}] "
                  f"promoted={r['chain_a_promoted']} noise_archived={r['noise_archived']} — waiting 60s...")
            time.sleep(60)

    r = evaluate(mongo_db, arango_db, args.verbose)
    print_results(r, args.verbose)

    if args.verbose:
        print("\nPer-chain heatScores (chain_a, sorted by heat desc):")
        stats = get_chain_stats(mongo_db, "chain_a", agent_prefix="h1_chain_a_s")
        for doc in sorted(stats["docs"], key=lambda d: d.get("heatScore") or 0, reverse=True):
            print(f"  {doc.get('agentId','?'):<22} heat={doc.get('heatScore', 'N/A')!s:<8} "
                  f"importance={doc.get('intrinsicImportance','?')!s:<5} "
                  f"status={doc.get('status')} lastEvent={str(doc.get('lastEventAt',''))[:10]}")

    sys.exit(0 if r["ALL_PASS"] else 1)


if __name__ == "__main__":
    main()
