"""
H6 — Temporal Override Ingestor
=================================
Injects the entrenched RBA/Bullock belief (20 articles) and then the contradiction
(1 AFR article confirming rate hike) into Athena, rewrites timestamps to simulate
60 days of reinforcement, and waits for MTM formation before proceeding.

Dataset: GDELT 2.0 real news articles fetched by fetch_h6_bullock.py
Entity:  Michele Bullock — Governor, Reserve Bank of Australia
Old truth: RBA is hawkish, holding/expected to hike rates (20 articles, Day 1-60)
New truth: RBA has lifted interest rates to avoid recession (1 article, Day 61)

Usage:
    python ingestor_h6.py [--url URL] [--key KEY] [--clean] [--dry-run]

Phases:
    0. Clean      — (optional --clean) purge existing H6 data from MongoDB
    1. Inject     — belief-building articles grouped into 2 sessions of 10 (triggers MTM flush)
    2. Wait       — poll MongoDB until MTM formation completes for belief sessions
    3. Rewrite    — patch timestamps on cognitive_chains to simulate 60-day window
    4. Inject     — contradiction article as 11 interactions (triggers MTM flush) on Day 61
    5. Summary    — print what was injected and oracle checks to run next

Design:
    Articles are grouped into 2 sessions of 10 interactions each.
    10 interactions = 20 Redis events → triggers ProcessMTMFormation per session.
    This produces 2 MTM-formed chains about the hawkish RBA belief.
    The contradiction is injected as 11 interactions to guarantee MTM flush.
    Belief chain timestamps are rewritten to 2-6 hours ago (not days) because
    the effective heat time constant τ×S ≈ 24h — anything > 12h has heat < 0.3.
    The promoter then promotes all chains with heat > 0.3 to ArangoDB LTM.
"""

import argparse
import glob
import json
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

import requests
from pymongo import MongoClient

# ── Config ────────────────────────────────────────────────────────────────────

TENANT_ID = "tenant_h6_experiment"
USER_ID   = "user_h6_bullock_watcher"

# Simulated timeline
EXPERIMENT_BASE  = datetime.now(timezone.utc) - timedelta(days=61)
BELIEF_END_DAY   = 60   # belief articles spread Day 1 → Day 60
CONTRADICTION_DAY = 61  # contradiction lands the next day

# Data directories (relative to this script)
SCRIPT_DIR        = Path(__file__).parent
DATA_DIR          = SCRIPT_DIR.parent.parent / "data" / "h6"
BELIEF_DIR        = DATA_DIR / "belief_building"
CONTRADICTION_DIR = DATA_DIR / "contradiction"

MONGO_URI = "mongodb://memory_user:memory_password_2024@localhost:27017/memory_os?authSource=admin"
MONGO_DB  = "memory_os"

# MTM formation polling
MTM_POLL_INTERVAL_S = 15
MTM_STABLE_WINDOW_S = 120   # 2 min stable = formation complete
MTM_TIMEOUT_S       = 600

# Probe queries — run these manually with probe_h6.py after ingestion
PROBE_QUERIES = [
    "What is the RBA's current interest rate stance?",
    "Has Michele Bullock's RBA changed interest rates?",
    "What is the current state of Australian interest rates?",
    "What did the Reserve Bank of Australia decide on rates?",
    "What is Michele Bullock's position on interest rates?",
]

# ── Helpers ───────────────────────────────────────────────────────────────────

def day_to_ts(day: int) -> datetime:
    return EXPERIMENT_BASE + timedelta(days=day)


def athena_post(base_url: str, api_key: str, path: str, body: dict) -> dict:
    resp = requests.post(
        f"{base_url}{path}",
        json=body,
        headers={"X-API-Key": api_key, "Content-Type": "application/json"},
        timeout=30,
    )
    resp.raise_for_status()
    return resp.json()


def create_session(base_url: str, api_key: str, agent_id: str, label: str) -> str:
    body = {
        "tenant_id": TENANT_ID,
        "user_id":   USER_ID,
        "agent_id":  agent_id,
        "metadata":  {"experiment": "h6", "chain_label": label},
    }
    result = athena_post(base_url, api_key, "/api/v1/sessions", body)
    return result["sessionId"]


def store_interaction(base_url: str, api_key: str, session_id: str,
                      user_msg: str, agent_resp: str) -> dict:
    body = {
        "userMessage":   user_msg,
        "agentResponse": agent_resp,
    }
    return athena_post(base_url, api_key, f"/api/v1/sessions/{session_id}/interactions", body)


_mongo_uri = MONGO_URI  # overridable via --mongo-uri

def mongo_connect():
    try:
        client = MongoClient(_mongo_uri, serverSelectionTimeoutMS=5000)
        client.admin.command("ping")
        return client, client[MONGO_DB]
    except Exception as e:
        raise SystemExit(f"Cannot connect to MongoDB: {e}")


def load_articles(directory: Path) -> list[dict]:
    files = sorted(glob.glob(str(directory / "*.json")))
    articles = []
    for f in files:
        with open(f) as fh:
            articles.append(json.load(fh))
    return articles


# ── Phase 0: Clean ────────────────────────────────────────────────────────────

def clean_h6_data() -> None:
    print("Phase 0 — Cleaning existing H6 data from MongoDB...")
    client, db = mongo_connect()
    r_chains = db["cognitive_chains"].delete_many({"tenantId": TENANT_ID})
    r_events = db["cognitive_events"].delete_many({"tenantId": TENANT_ID})
    print(f"  Deleted {r_chains.deleted_count} chains, {r_events.deleted_count} events")
    client.close()


# Varied question phrasings so each interaction feels distinct to the LLM
BELIEF_QUESTIONS = [
    "What is the latest news on Michele Bullock and RBA interest rates?",
    "What is the RBA's current stance on interest rates under Michele Bullock?",
    "Has the Reserve Bank of Australia signalled any rate changes recently?",
    "What are analysts saying about Michele Bullock's interest rate policy?",
    "What is the market expecting from the RBA on interest rates?",
    "How is Michele Bullock responding to inflation and rate hike pressure?",
    "What did the RBA Governor say about monetary policy this week?",
    "Are Australian interest rates expected to rise under Bullock?",
    "What is the latest on RBA rate hike bets and Bullock's position?",
    "How is the Iran conflict affecting RBA rate expectations under Bullock?",
    "What signals has Michele Bullock sent about future interest rate moves?",
]

# ── Phase 1: Inject belief-building articles ──────────────────────────────────

def inject_belief(base_url: str, api_key: str, articles: list[dict],
                  dry_run: bool) -> dict[str, tuple[int, int]]:
    """
    Inject belief-building articles in 5 sessions of 11 interactions each.
    11 interactions = 22 Redis events > STM_CACHE_MAX_TURNS(10) threshold → guaranteed MTM flush.
    5 sessions = 5 MTM-formed chains = strongly entrenched belief.
    Articles are cycled + question phrasings are rotated for variety.
    Returns: { agent_id: (day_start, day_end) }
    """
    sessions_config = [
        ("h6_belief_s01", 11,  1, 12),
        ("h6_belief_s02", 11, 13, 24),
        ("h6_belief_s03", 11, 25, 36),
        ("h6_belief_s04", 11, 37, 48),
        ("h6_belief_s05", 11, 49, 60),
    ]

    total_interactions = sum(n for _, n, _, _ in sessions_config)
    print(f"\nPhase 1 — Injecting {total_interactions} belief interactions across {len(sessions_config)} sessions...")
    print(f"  Each session: 11 interactions = 22 Redis events → triggers MTM flush")

    agent_day_map = {}

    for session_num, (agent_id, n_interactions, day_start, day_end) in enumerate(sessions_config):
        print(f"\n  Session {agent_id} ({n_interactions} interactions, Day {day_start}-{day_end})")

        if dry_run:
            print(f"  [DRY RUN] Would create session and inject {n_interactions} interactions")
            agent_day_map[agent_id] = (day_start, day_end)
            continue

        session_id = create_session(base_url, api_key, agent_id=agent_id, label="belief_building")
        print(f"  Session created: {session_id[:16]}...")

        for idx in range(n_interactions):
            article   = articles[idx % len(articles)]
            question  = BELIEF_QUESTIONS[idx % len(BELIEF_QUESTIONS)]
            user_msg  = f"{question} (ref {session_num * n_interactions + idx + 1})"
            agent_resp = article["text"][:2000]

            try:
                store_interaction(base_url, api_key, session_id, user_msg, agent_resp)
                print(f"    [{idx+1}/{n_interactions}] stored (article {idx % len(articles) + 1}, "
                      f"tone={article['tone']:.2f})")
            except requests.exceptions.RequestException as e:
                print(f"    [{idx+1}/{n_interactions}] ERROR: {e}")

        agent_day_map[agent_id] = (day_start, day_end)

    print(f"\n  Belief injection done: {len(agent_day_map)} sessions")
    return agent_day_map


# ── Phase 2: Wait for MTM formation ──────────────────────────────────────────

def wait_for_mtm_formation(dry_run: bool) -> int:
    if dry_run:
        print("\nPhase 2 — [DRY RUN] Skipping MTM formation wait")
        return 0

    print(f"\nPhase 2 — Waiting for MTM formation on belief sessions...")
    print(f"  Polling every {MTM_POLL_INTERVAL_S}s, stable after {MTM_STABLE_WINDOW_S}s no change")

    client, db = mongo_connect()
    chains_col = db["cognitive_chains"]

    last_count   = -1
    stable_since = None
    start_time   = time.time()

    while True:
        elapsed = time.time() - start_time
        count = chains_col.count_documents({
            "tenantId":            TENANT_ID,
            "agentId":             {"$regex": "^h6_belief_s"},
            "intrinsicImportance": {"$gt": 0},
        })

        if count != last_count:
            print(f"  [{elapsed:>5.0f}s] MTM-formed chains: {count} (was {last_count if last_count >= 0 else '-'})")
            last_count   = count
            stable_since = time.time()
        else:
            stable_for = time.time() - stable_since
            print(f"  [{elapsed:>5.0f}s] MTM-formed chains: {count} (stable {stable_for:.0f}s / {MTM_STABLE_WINDOW_S}s)")
            if stable_for >= MTM_STABLE_WINDOW_S:
                print(f"  Formation stable — {count} MTM-formed belief chains")
                break

        if elapsed >= MTM_TIMEOUT_S:
            print(f"  WARNING: Timed out after {MTM_TIMEOUT_S}s with {count} chains. Proceeding anyway.")
            break

        time.sleep(MTM_POLL_INTERVAL_S)

    client.close()
    return last_count


# ── Phase 3: Rewrite timestamps ───────────────────────────────────────────────

def rewrite_timestamps(agent_day_map: dict[str, tuple[int, int]], dry_run: bool) -> None:
    """
    Patch startedAt / lastEventAt on cognitive_chains.
    Heat formula effective time constant is ~24h (τ × S), so chains need to be
    within the last ~12h to score heat > 0.3 (promoter threshold).
    Belief sessions are spread 2-6 hours ago; contradiction will be set to "now"
    in Phase 4 after MTM formation.
    """
    now = datetime.now(timezone.utc)
    # Map each session to hours-ago rather than days-ago
    # s01 (oldest belief) = 6h ago, s05 (most recent belief) = 2h ago
    session_hours_ago = {
        "h6_belief_s01": 6,
        "h6_belief_s02": 5,
        "h6_belief_s03": 4,
        "h6_belief_s04": 3,
        "h6_belief_s05": 2,
    }

    print("\nPhase 3 — Rewriting timestamps on belief chains (hours ago, not days)...")
    print("  Reason: effective τ×S ≈ 24h → chains must be < 12h old for heat > 0.3")

    if dry_run:
        for agent_id in agent_day_map:
            hours = session_hours_ago.get(agent_id, 4)
            ts = now - timedelta(hours=hours)
            print(f"  [DRY RUN] {agent_id} → {hours}h ago ({ts.strftime('%H:%M UTC')})")
        return

    client, db = mongo_connect()
    chains_col = db["cognitive_chains"]
    total = 0

    for agent_id in agent_day_map:
        hours = session_hours_ago.get(agent_id, 4)
        ts = now - timedelta(hours=hours)
        r = chains_col.update_many(
            {"tenantId": TENANT_ID, "agentId": agent_id},
            {"$set": {"startedAt": ts, "lastEventAt": ts}},
        )
        total += r.modified_count
        print(f"  {agent_id} → {hours}h ago ({ts.strftime('%H:%M UTC')}) — {r.modified_count} chain(s)")

    print(f"  Total: {total} chain docs timestamped")
    client.close()


# ── Phase 4: Inject contradiction ─────────────────────────────────────────────

CONTRADICTION_QUESTIONS = [
    "What is the very latest news on RBA interest rates? Has anything changed?",
    "Has the RBA actually raised interest rates now?",
    "What did Michele Bullock announce about the rate decision?",
    "Is the RBA rate hike confirmed?",
    "What is the new RBA cash rate after today's decision?",
    "How did the RBA lift rates to avoid recession?",
    "What was the RBA Governor's statement on the rate increase?",
    "Has the Reserve Bank of Australia raised rates?",
    "What did the AFR report about the RBA rate hike?",
    "What is the confirmed RBA monetary policy stance now?",
    "What changed in RBA rate policy under Michele Bullock?",
]


def inject_contradiction(base_url: str, api_key: str, articles: list[dict],
                         dry_run: bool) -> str | None:
    """
    Inject the contradiction article as 11 interactions (22 Redis events) to
    guarantee MTM flush. 1 interaction = skeleton only; needs > STM_CACHE_MAX_TURNS(10)
    turn-pairs = > 20 events to trigger formation.
    Timestamps the resulting chain to now so heat ≈ importance (fresh).
    Returns the agent_id used.
    """
    if not articles:
        print("\nPhase 4 — ERROR: No contradiction article found in data/h6/contradiction/")
        return None

    article  = articles[0]
    agent_id = "h6_contradiction_01"
    n        = 11  # 11 interactions = 22 Redis events > threshold

    print(f"\nPhase 4 — Injecting contradiction article ({n} interactions to trigger MTM flush)...")
    print(f"  Source: {article['url'][:80]}...")
    print(f"  Date:   {article['date']}, Tone: {article['tone']:.2f}")

    if dry_run:
        print(f"  [DRY RUN] Would inject {n} contradiction interactions as {agent_id}")
        return agent_id

    # Truncate to 500 chars per interaction — 11 × 2000 = 22k chars bloats the
    # CreateSegmentSummary prompt and causes Azure OpenAI context deadline exceeded.
    agent_resp = article["text"][:500]

    try:
        session_id = create_session(base_url, api_key, agent_id=agent_id, label="contradiction")

        for i in range(n):
            question = CONTRADICTION_QUESTIONS[i % len(CONTRADICTION_QUESTIONS)]
            store_interaction(base_url, api_key, session_id, question, agent_resp)
            print(f"  [{i+1}/{n}] stored")

        # Timestamp to now so heat ≈ importance (not decayed)
        ts = datetime.now(timezone.utc)
        client, db = mongo_connect()
        r = db["cognitive_chains"].update_many(
            {"tenantId": TENANT_ID, "agentId": agent_id},
            {"$set": {"startedAt": ts, "lastEventAt": ts}},
        )
        client.close()

        print(f"  [OK] {n} interactions injected | session={session_id[:16]}... | {r.modified_count} chain(s) timestamped to now")
        return agent_id

    except requests.exceptions.RequestException as e:
        print(f"  ERROR injecting contradiction: {e}")
        return None


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    global _mongo_uri
    parser = argparse.ArgumentParser(description="H6 Temporal Override Ingestor")
    parser.add_argument("--url",       default="https://api.console.dromos.prescottdata.io/memory",
                        help="Athena base URL")
    parser.add_argument("--key",       default="dev-api-key", help="API key")
    parser.add_argument("--mongo-uri", default=MONGO_URI,
                        help="MongoDB URI (default uses localhost — port-forward first)")
    parser.add_argument("--clean",     action="store_true", help="Delete existing H6 data before injecting")
    parser.add_argument("--dry-run",   action="store_true", help="Print plan without calling the API")
    parser.add_argument("--resume",    action="store_true",
                        help="Skip injection (Phase 1) — resume from MTM wait + timestamp rewrite")
    parser.add_argument("--belief-only", action="store_true",
                        help="Stop after Phase 3 (belief injected + timestamps patched). "
                             "Run probe_h6.py --phase pre, then re-run with --inject-contradiction.")
    parser.add_argument("--inject-contradiction", action="store_true",
                        help="Skip to Phase 4 only — inject contradiction after pre-probe was run")
    args = parser.parse_args()
    _mongo_uri = args.mongo_uri

    print("=" * 65)
    print("H6 — Temporal Override Ingestor")
    print("=" * 65)
    print(f"  Target:     {args.url}")
    print(f"  Tenant:     {TENANT_ID}")
    print(f"  User:       {USER_ID}")
    print(f"  Entity:     Michele Bullock / RBA")
    print(f"  Day 1:      {day_to_ts(1).date()}  (first belief article)")
    print(f"  Day 60:     {day_to_ts(60).date()} (last belief article)")
    print(f"  Day 61:     {day_to_ts(61).date()} (contradiction)")
    print(f"  Dry run:    {args.dry_run}")
    print(f"  Clean:      {args.clean}")
    print()

    # Load data
    belief_articles       = load_articles(BELIEF_DIR)
    contradiction_articles = load_articles(CONTRADICTION_DIR)

    print(f"  Loaded {len(belief_articles)} belief articles, "
          f"{len(contradiction_articles)} contradiction article(s)")

    if len(belief_articles) < 10:
        raise SystemExit(f"Need at least 10 belief articles, found {len(belief_articles)}. "
                         f"Re-run fetch_h6_bullock.py first.")

    print()

    # Phase 0
    if args.clean:
        clean_h6_data()
        print()

    # The agent_day_map is fixed regardless of whether we injected or resumed
    agent_day_map = {
        "h6_belief_s01": (1,  12),
        "h6_belief_s02": (13, 24),
        "h6_belief_s03": (25, 36),
        "h6_belief_s04": (37, 48),
        "h6_belief_s05": (49, 60),
    }

    if args.inject_contradiction:
        # Jump straight to Phase 4 — belief already injected and pre-probe already run
        print("[INJECT-CONTRADICTION] Skipping to Phase 4")
        print()
        contradiction_agent = inject_contradiction(
            args.url, args.key, contradiction_articles, args.dry_run
        )
        mtm_count = 0
        print()
        print("Contradiction injected. Now run:")
        print("  python probe_h6.py --url <URL> --phase post --verbose")
        return

    if args.resume:
        print("[RESUME] Skipping Phase 1 (injection already done) — picking up from MTM wait")
        print()
    else:
        # Phase 1: inject belief
        agent_day_map = inject_belief(args.url, args.key, belief_articles, args.dry_run)

    # Phase 2: wait for MTM
    mtm_count = wait_for_mtm_formation(args.dry_run)

    # Phase 3: rewrite timestamps on belief chains
    rewrite_timestamps(agent_day_map, args.dry_run)

    if args.belief_only:
        print()
        print("=" * 65)
        print("BELIEF PHASE COMPLETE — pre-probe checkpoint")
        print("=" * 65)
        print("  Belief sessions injected and timestamps patched.")
        print("  Run pre-probe now:")
        print("    python probe_h6.py --url <URL> --phase pre --verbose")
        print()
        print("  Then inject contradiction:")
        print("    python ingestor_h6.py --url <URL> --key <KEY> --inject-contradiction")
        print("=" * 65)
        return

    # Phase 4: inject contradiction
    contradiction_agent = inject_contradiction(
        args.url, args.key, contradiction_articles, args.dry_run
    )

    # Summary
    print()
    print("=" * 65)
    print("INJECTION COMPLETE")
    print("=" * 65)
    print(f"  Belief sessions injected : {len(agent_day_map)}")
    print(f"  Belief MTM chains formed : {mtm_count}")
    print(f"  Contradiction injected   : {'yes' if contradiction_agent else 'FAILED'}")
    print()
    print("Next steps:")
    print("  1. Wait for promoter to fire (~1 min) — promotes hot chains to ArangoDB")
    print("  2. Run pre-contradiction probe:")
    print("       python probe_h6.py --url <URL> --phase pre")
    print("     All 5 queries must return hawkish/holding stance")
    print("  3. Verify the contradiction chain is in ArangoDB with higher heat")
    print("  4. Run post-contradiction probe:")
    print("       python probe_h6.py --url <URL> --phase post")
    print("     All 5 queries must return 'lifted' / 'raised' / 'rate hike confirmed'")
    print()
    print("Oracle thresholds (locked):")
    print("  Correction Accuracy  >= 0.80  (4/5 queries return new truth)")
    print("  Hedging Frequency    <= 0.10  (0-1 hedging phrases across 5 responses)")
    print()
    print("Probe queries:")
    for i, q in enumerate(PROBE_QUERIES, 1):
        print(f"  {i}. {q}")


if __name__ == "__main__":
    main()
