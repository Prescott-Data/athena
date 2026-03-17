"""
H1 — Keplerian Survival Ingestor
==============================================
Injects Chain A (persistent fact), Chain B (throwaway), and Noise into
a local Athena instance, then rewrites timestamps directly in MongoDB to
simulate 180 days of time passing without actually waiting.

Usage:
    python ingestor_h1.py [--url http://localhost:8080] [--noise 1000] [--dry-run] [--clean]

Phases:
    0. Clean      — (optional --clean) purge existing H1 data from MongoDB
    1. Inject     — Chain A, Chain B, Noise via StoreInteraction API
    2. Wait       — poll MongoDB until ProcessMTMFormation completes for Chain A
    3. Rewrite    — patch startedAt / lastEventAt on cognitive_chains to simulated times
    4. Summary    — print what was injected and what to probe next

Key design decision:
    Each session uses a UNIQUE agentId (e.g. h1_chain_a_s01, h1_noise_0042).
    This gives every session its own Redis STM queue so ProcessMTMFormation
    fires independently per session once the queue hits STM_MAX_EVENTS_BEFORE_FLUSH.
    If all sessions shared one agentId they would share one queue and formation
    would only fire a handful of times across all 32 sessions.

    Timestamps are rewritten by agentId on cognitive_chains — this catches
    both the skeleton chain (created by CreateSession) and any MTM-formed
    chain docs (created by ProcessMTMFormation with the same agentId).
"""

import argparse
import json
import random
import string
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timedelta, timezone

import requests
from pymongo import MongoClient

# ── Config ────────────────────────────────────────────────────────────────────

LOCOMO_URL    = "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"
CHAIN_A_CONV  = 2          # John & Maria — 32 sessions, 663 turns
CHAIN_B_CAT   = 3          # commonsense/inference — one-off questions
CHAIN_B_IDX   = 0          # first category-3 pair from conv 0

TENANT_ID     = "tenant_h1_experiment"
USER_ID       = "user_h1_john_maria"

# Simulated timeline
EXPERIMENT_BASE   = datetime.now(timezone.utc) - timedelta(days=180)  # Day 0 anchor
CHAIN_A_END_DAY   = 150    # Chain A sessions compressed to Day 1 → Day 150
CHAIN_B_DAY       = 1      # Chain B fires once on Day 1
NOISE_END_DAY     = 180    # Noise spread across Day 1 → Day 180

MONGO_URI     = "mongodb://admin:admin123@localhost:27017/memory_os?authSource=admin"
MONGO_DB      = "memory_os"

# MTM formation polling
MTM_POLL_INTERVAL_S  = 15    # seconds between MongoDB polls
MTM_STABLE_WINDOW_S  = 60    # seconds of no new chains = stable
MTM_TIMEOUT_S        = 600   # max wait before giving up

# Noise phrases — synthetic random single-turn interactions
NOISE_TEMPLATES = [
    ("What time does the pharmacy close?", "Usually around 9 PM on weekdays."),
    ("Can you remind me to call the dentist?", "Sure, I'll remind you tomorrow morning."),
    ("What's the weather like today?", "Partly cloudy with a high of 22°C."),
    ("How do I reset my password?", "Click 'forgot password' on the login page."),
    ("Is the meeting still on for Friday?", "Yes, same time and place."),
    ("What's the capital of Peru?", "Lima."),
    ("Can you convert 100 USD to EUR?", "That's approximately 92 euros right now."),
    ("What movies are playing tonight?", "Several options — want action or comedy?"),
    ("How long does it take to boil an egg?", "About 7 minutes for a soft boil."),
    ("What's a good recipe for banana bread?", "Mash 3 ripe bananas, mix with 1 egg, 80g sugar, 200g flour, bake at 180°C for 50 min."),
]

# ── Helpers ───────────────────────────────────────────────────────────────────

def day_to_ts(day: int) -> datetime:
    """Convert simulated day number to an absolute UTC datetime."""
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
        "metadata":  {"experiment": "h1", "chain_label": label},
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


def mongo_connect() -> tuple:
    """Return (client, db). Raises SystemExit on connection failure."""
    try:
        client = MongoClient(MONGO_URI, serverSelectionTimeoutMS=5000)
        client.admin.command("ping")
        return client, client[MONGO_DB]
    except Exception as e:
        raise SystemExit(f"Cannot connect to local MongoDB: {e}")

# ── Phase 0: Clean ────────────────────────────────────────────────────────────

def clean_h1_data() -> None:
    """Delete all H1 experiment documents from MongoDB before a fresh run."""
    print("Phase 0 — Cleaning existing H1 data from MongoDB...")
    client, db = mongo_connect()

    r_chains = db["cognitive_chains"].delete_many({"tenantId": TENANT_ID})
    r_events = db["cognitive_events"].delete_many({"tenantId": TENANT_ID})

    print(f"  Deleted {r_chains.deleted_count} chains, {r_events.deleted_count} events")
    client.close()

# ── Phase 1: Load LoCoMo ──────────────────────────────────────────────────────

def load_locomo() -> list:
    print("Fetching LoCoMo dataset...")
    try:
        resp = requests.get(LOCOMO_URL, timeout=30)
        resp.raise_for_status()
        data = resp.json()
        print(f"  Loaded {len(data)} conversations ({len(resp.content):,} bytes)")
        return data
    except requests.exceptions.RequestException as e:
        raise SystemExit(f"Failed to fetch LoCoMo: {e}")
    except json.JSONDecodeError as e:
        raise SystemExit(f"Invalid JSON from LoCoMo: {e}")


def extract_chain_a_sessions(locomo: list) -> list[dict]:
    """
    Extract all sessions from conv_idx=2 (John & Maria).
    Returns list of: { session_num, date_str, turns: [{user, agent}] }
    Each LoCoMo session maps to one Athena session.
    Turns are paired: speaker_a = user, speaker_b = agent.
    Unpaired trailing turns are kept as user-only interactions.
    """
    conv = locomo[CHAIN_A_CONV]["conversation"]
    session_nums = sorted(set(
        int(k.split("_")[1])
        for k in conv
        if k.startswith("session_") and k.split("_")[1].isdigit()
        and not k.endswith("_time")
    ))

    sessions = []
    for snum in session_nums:
        date_str = conv.get(f"session_{snum}_date_time", f"Day {snum}")
        turns    = conv.get(f"session_{snum}", [])
        pairs    = []
        i = 0
        while i < len(turns):
            user_text  = turns[i].get("text", "").strip()
            agent_text = turns[i + 1].get("text", "").strip() if i + 1 < len(turns) else ""
            if user_text:
                pairs.append({"user": user_text, "agent": agent_text})
            i += 2

        if pairs:
            sessions.append({
                "session_num": snum,
                "date_str":    date_str,
                "pairs":       pairs,
            })

    print(f"  Chain A: {len(sessions)} sessions, "
          f"{sum(len(s['pairs']) for s in sessions)} turn-pairs from conv_idx={CHAIN_A_CONV}")
    return sessions


def extract_chain_b(locomo: list) -> dict:
    """Pick one category-3 (commonsense/inference) QA pair — throwaway, no follow-up."""
    qa_list = [q for q in locomo[0]["qa"] if q.get("category") == CHAIN_B_CAT]
    chosen  = qa_list[CHAIN_B_IDX]
    print(f"  Chain B: Q={chosen['question'][:80]!r}")
    return chosen

# ── Phase 2: Inject ───────────────────────────────────────────────────────────

def inject_chain_a(base_url: str, api_key: str, sessions: list[dict],
                   dry_run: bool) -> dict[str, int]:
    """
    Inject all Chain A sessions. Returns mapping: agent_id → simulated_day.

    Each session gets a unique agentId (h1_chain_a_s{session_num:02d}) so it
    has its own isolated Redis STM queue. This ensures ProcessMTMFormation
    fires per-session once the queue hits STM_MAX_EVENTS_BEFORE_FLUSH (20 events
    = 10 turn-pairs), rather than all sessions sharing one queue.
    """
    print(f"\nPhase 1 — Injecting Chain A ({len(sessions)} sessions)...")
    total_sessions = len(sessions)
    mapping = {}  # agent_id → day

    for idx, sess in enumerate(sessions):
        # Linear interpolation: session 0 → Day 1, last session → Day CHAIN_A_END_DAY
        day      = 1 + round(idx / max(total_sessions - 1, 1) * (CHAIN_A_END_DAY - 1))
        agent_id = f"h1_chain_a_s{sess['session_num']:02d}"

        if dry_run:
            print(f"  [DRY RUN] Session {sess['session_num']:>2} | agent={agent_id} "
                  f"→ Day {day} ({len(sess['pairs'])} pairs)")
            mapping[agent_id] = day
            continue

        try:
            session_id = create_session(base_url, api_key,
                                        agent_id=agent_id,
                                        label="chain_a")
            for pair in sess["pairs"]:
                store_interaction(base_url, api_key, session_id,
                                  pair["user"], pair["agent"])
            mapping[agent_id] = day
            print(f"  Session {sess['session_num']:>2} | {agent_id} → Day {day:>3} | "
                  f"{len(sess['pairs'])} pairs | session={session_id[:8]}...")
        except requests.exceptions.RequestException as e:
            print(f"  ERROR on session {sess['session_num']}: {e}")

    print(f"  Chain A done: {len(mapping)} sessions injected")
    return mapping


def inject_chain_b(base_url: str, api_key: str, qa: dict,
                   dry_run: bool) -> dict[str, int]:
    """
    Inject Chain B — single interaction on Day 1.
    Returns mapping: agent_id → day.

    Note: 1 interaction = 2 Redis events, which won't reach the forced-flush
    threshold. The skeleton chain (intrinsicImportance=0) will have heat=0
    and will be archived by the archiver — correct behavior for a throwaway.
    """
    print(f"\nPhase 2 — Injecting Chain B (Day {CHAIN_B_DAY})...")
    agent_id = "h1_chain_b_main"

    if dry_run:
        print(f"  [DRY RUN] agent={agent_id} | Q={qa['question'][:60]!r}")
        return {agent_id: CHAIN_B_DAY}

    try:
        session_id = create_session(base_url, api_key,
                                    agent_id=agent_id,
                                    label="chain_b")
        store_interaction(base_url, api_key, session_id,
                          qa["question"], str(qa.get("answer", "")))
        print(f"  Chain B | {agent_id} → Day {CHAIN_B_DAY} | session={session_id[:8]}...")
        return {agent_id: CHAIN_B_DAY}
    except requests.exceptions.RequestException as e:
        print(f"  ERROR injecting Chain B: {e}")
        return {}


def _inject_one_noise(base_url: str, api_key: str, idx: int) -> tuple[str, int]:
    """Inject a single noise interaction (called from thread pool)."""
    user_msg, agent_resp = random.choice(NOISE_TEMPLATES)
    suffix   = "".join(random.choices(string.ascii_lowercase, k=4))
    user_msg = f"{user_msg} [{suffix}]"
    day      = random.randint(1, NOISE_END_DAY)
    agent_id = f"h1_noise_{idx:04d}"

    session_id = create_session(base_url, api_key, agent_id=agent_id, label="noise")
    store_interaction(base_url, api_key, session_id, user_msg, agent_resp)
    return agent_id, day


def inject_noise(base_url: str, api_key: str, count: int,
                 dry_run: bool) -> dict[str, int]:
    """Inject N noise interactions concurrently. Returns agent_id → day mapping."""
    print(f"\nPhase 3 — Injecting {count} noise interactions (concurrently)...")

    if dry_run:
        print(f"  [DRY RUN] Would inject {count} noise sessions")
        return {f"h1_noise_{i:04d}": random.randint(1, NOISE_END_DAY)
                for i in range(min(count, 5))}

    mapping   = {}
    errors    = 0
    done      = 0
    report_at = max(count // 10, 1)

    with ThreadPoolExecutor(max_workers=10) as pool:
        futures = {pool.submit(_inject_one_noise, base_url, api_key, i): i
                   for i in range(count)}
        for future in as_completed(futures):
            try:
                agent_id, day = future.result()
                mapping[agent_id] = day
            except Exception as e:
                errors += 1
            done += 1
            if done % report_at == 0:
                print(f"  Progress: {done}/{count} ({errors} errors)")

    print(f"  Noise done: {len(mapping)} injected, {errors} errors")
    return mapping

# ── Phase 3: Wait for MTM formation ──────────────────────────────────────────

def wait_for_mtm_formation(chain_a_map: dict, dry_run: bool) -> int:
    """
    Poll MongoDB until ProcessMTMFormation has completed for Chain A sessions.

    We look for cognitive_chains documents where:
      - tenantId = TENANT_ID
      - agentId starts with "h1_chain_a_s"
      - intrinsicImportance > 0  (only MTM-formed chains have this set by the LLM)

    We consider formation complete when the count of such chains hasn't grown
    for MTM_STABLE_WINDOW_S seconds, or MTM_TIMEOUT_S seconds have elapsed.

    Returns the final count of MTM-formed chains found.
    """
    if dry_run:
        print("\nPhase 3 — [DRY RUN] Skipping MTM formation wait")
        return 0

    expected_sessions = len(chain_a_map)
    print(f"\nPhase 3 — Waiting for MTM formation ({expected_sessions} Chain A sessions)...")
    print(f"  Polling every {MTM_POLL_INTERVAL_S}s, stable after {MTM_STABLE_WINDOW_S}s no change")
    print(f"  (ProcessMTMFormation fires when a session's Redis queue hits 20 events = 10 turn-pairs)")

    client, db = mongo_connect()
    chains_col = db["cognitive_chains"]

    last_count    = -1
    stable_since  = None
    start_time    = time.time()

    while True:
        elapsed = time.time() - start_time

        count = chains_col.count_documents({
            "tenantId":            TENANT_ID,
            "agentId":             {"$regex": "^h1_chain_a_s"},
            "intrinsicImportance": {"$gt": 0},
        })

        if count != last_count:
            print(f"  [{elapsed:>5.0f}s] MTM-formed chains: {count} (was {last_count if last_count >= 0 else '—'})")
            last_count   = count
            stable_since = time.time()
        else:
            stable_for = time.time() - stable_since
            print(f"  [{elapsed:>5.0f}s] MTM-formed chains: {count} (stable {stable_for:.0f}s / {MTM_STABLE_WINDOW_S}s)")
            if stable_for >= MTM_STABLE_WINDOW_S:
                print(f"  Formation stable — proceeding with {count} MTM-formed Chain A chains")
                break

        if elapsed >= MTM_TIMEOUT_S:
            print(f"  WARNING: Timed out after {MTM_TIMEOUT_S}s with {count} MTM-formed chains. Proceeding anyway.")
            break

        time.sleep(MTM_POLL_INTERVAL_S)

    client.close()
    return last_count

# ── Phase 4: MongoDB timestamp rewrite ───────────────────────────────────────

def rewrite_timestamps(chain_a_map: dict, chain_b_map: dict,
                       noise_map: dict, dry_run: bool) -> None:
    """
    Patch startedAt / lastEventAt on cognitive_chains to the simulated experiment
    timestamps. Queries by agentId (not chainId) so both skeleton chains and any
    MTM-formed chains for the same session get the correct timestamp.

    We do NOT rewrite cognitive_events — the promoter and archiver only look at
    chain-level timestamps for heat scoring and archival decisions.
    """
    print("\nPhase 4 — Rewriting MongoDB timestamps...")

    all_maps = [
        ("Chain A", chain_a_map),
        ("Chain B", chain_b_map),
        ("Noise",   noise_map),
    ]

    if dry_run:
        for label, mapping in all_maps:
            for agent_id, day in list(mapping.items())[:2]:
                ts = day_to_ts(day)
                print(f"  [DRY RUN] {label} {agent_id} → Day {day} ({ts.date()})")
        return

    client, db = mongo_connect()
    chains_col = db["cognitive_chains"]

    total_chains = 0

    for label, mapping in all_maps:
        label_count = 0
        for agent_id, day in mapping.items():
            ts = day_to_ts(day)
            r = chains_col.update_many(
                {"tenantId": TENANT_ID, "agentId": agent_id},
                {"$set": {"startedAt": ts, "lastEventAt": ts}},
            )
            label_count  += r.modified_count
            total_chains += r.modified_count

        print(f"  {label}: updated {label_count} chain docs")

    print(f"  Total: {total_chains} chain docs timestamped")
    client.close()

# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="H1 Keplerian Survival Ingestor")
    parser.add_argument("--url",     default="http://localhost:8080", help="Athena base URL")
    parser.add_argument("--key",     default="dev-api-key",           help="API key")
    parser.add_argument("--noise",   type=int, default=1000,          help="Noise interaction count (default 1000)")
    parser.add_argument("--dry-run", action="store_true",             help="Print plan without calling the API")
    parser.add_argument("--clean",   action="store_true",             help="Delete existing H1 data before injecting")
    args = parser.parse_args()

    print("=" * 60)
    print("H1 — Keplerian Survival Ingestor")
    print("=" * 60)
    print(f"  Target:    {args.url}")
    print(f"  Tenant:    {TENANT_ID}")
    print(f"  User:      {USER_ID}")
    print(f"  Noise:     {args.noise}")
    print(f"  Base date: {EXPERIMENT_BASE.date()} (Day 1)")
    print(f"  End date:  {day_to_ts(NOISE_END_DAY).date()} (Day {NOISE_END_DAY})")
    print(f"  Dry run:   {args.dry_run}")
    print(f"  Clean:     {args.clean}")
    print()

    # Phase 0: optional clean
    if args.clean:
        clean_h1_data()
        print()

    # Load dataset
    locomo   = load_locomo()
    sessions = extract_chain_a_sessions(locomo)
    chain_b  = extract_chain_b(locomo)

    # Phase 1–3: Inject
    chain_a_map = inject_chain_a(args.url, args.key, sessions,  args.dry_run)
    chain_b_map = inject_chain_b(args.url, args.key, chain_b,   args.dry_run)
    noise_map   = inject_noise(  args.url, args.key, args.noise, args.dry_run)

    # Phase 3: Wait for MTM formation
    mtm_count = wait_for_mtm_formation(chain_a_map, args.dry_run)

    # Phase 4: Rewrite timestamps
    rewrite_timestamps(chain_a_map, chain_b_map, noise_map, args.dry_run)

    # Summary
    total = len(chain_a_map) + len(chain_b_map) + len(noise_map)
    print()
    print("=" * 60)
    print("INJECTION COMPLETE")
    print("=" * 60)
    print(f"  Chain A sessions    : {len(chain_a_map)}")
    print(f"  Chain A MTM-formed  : {mtm_count}  (intrinsicImportance > 0)")
    print(f"  Chain B sessions    : {len(chain_b_map)}")
    print(f"  Noise sessions      : {len(noise_map)}")
    print(f"  Total sessions      : {total}")
    print()
    print("Next steps:")
    print("  1. Promoter fires every 1 min → computes heat, promotes hot chains to ArangoDB")
    print("  2. Archiver fires every 2 min → archives cold chains (heat < 0.10)")
    print("  3. Run: python probe_h1.py")
    print()
    print("Oracle checks:")
    print("  [OK] Chain A max heat >= 0.30 (recent sessions near Day 150 survive)")
    print("  [OK] Chain A fact in ArangoDB LTM (John's community/campaign work)")
    print("  [OK] Chain B heat <= 0.10 or archived (throwaway decays)")
    print("  [OK] Noise compression >= 90% archived")


if __name__ == "__main__":
    main()
