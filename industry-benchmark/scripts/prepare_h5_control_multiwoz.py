#!/usr/bin/env python3
"""
H5 Control Dataset — MultiWOZ 2.4 Hotel Booking Domain

Extracts 100 triplets from the hotel booking domain of MultiWOZ 2.4
for use as the H5 Benchmark 3 (control) dataset.

Each triplet = one hotel booking dialogue with all 3 formats:
  Format A (control_chat/) → Human utterances from the conversation (plain prose)
  Format B (control_json/) → Dialogue state / belief state as structured JSON
  Format C (control_logs/) → System act responses formatted as execution log lines

This domain is semantically distinct from the GHArchive deployment/CI data
used in Benchmarks 1 and 2 — hotel bookings have zero overlap with
container deployments, CI pipelines, or code repositories.

Usage:
    source venv/bin/activate
    python3 scripts/prepare_h5_control_multiwoz.py --output ./data/h5 --count 100
"""

import argparse
import json
import os
import sys

# ─── Entry point ──────────────────────────────────────────────────────────────

MULTIWOZ24_URL = (
    "https://github.com/smartyfh/MultiWOZ2.4/raw/main/data/MULTIWOZ2.4.zip"
)


def load_multiwoz_hotel(count: int):
    """
    Load MultiWOZ 2.4 from the smartyfh/MultiWOZ2.4 GitHub repo (public, no auth).
    Filters for hotel-only dialogues and extracts triplets.
    Returns list of (chat_text, json_text, log_text) tuples.
    """
    import urllib.request
    import zipfile
    import io

    cache_path = "/tmp/multiwoz24_data.json"

    if not os.path.exists(cache_path):
        print(f"Downloading MultiWOZ 2.4 ZIP from GitHub...")
        zip_path = "/tmp/MULTIWOZ2.4.zip"
        urllib.request.urlretrieve(MULTIWOZ24_URL, zip_path)
        print(f"  Extracting data.json...")
        with zipfile.ZipFile(zip_path, "r") as z:
            # Find the data JSON inside the zip
            names = z.namelist()
            data_file = next((n for n in names if n.endswith("data.json")), None)
            if not data_file:
                print(f"  Files in zip: {names[:10]}")
                raise FileNotFoundError("data.json not found in MULTIWOZ2.4.zip")
            with z.open(data_file) as f:
                raw = f.read()
        with open(cache_path, "wb") as f:
            f.write(raw)
        print(f"  Saved to {cache_path}")
    else:
        print(f"Using cached MultiWOZ 2.4 data from {cache_path}")

    with open(cache_path, "r", encoding="utf-8") as f:
        data = json.load(f)

    print(f"  Loaded {len(data)} dialogues")

    triplets = []

    for dialogue_id, dialogue in data.items():
        if len(triplets) >= count:
            break

        goal = dialogue.get("goal", {})

        # Only pure hotel dialogues — hotel goal has content, no other domain goals
        hotel_goal = goal.get("hotel", {})
        has_hotel = bool(hotel_goal.get("info") or hotel_goal.get("book"))
        if not has_hotel:
            continue
        has_other = any(
            bool(goal.get(d, {}).get("info") or goal.get(d, {}).get("book"))
            for d in ["taxi", "train", "restaurant", "attraction"]
        )
        if has_other:
            continue

        log_turns = dialogue.get("log", [])
        if len(log_turns) < 4:
            continue

        # ── Format A: full conversation as prose ─────────────────────────────
        chat_lines = []
        for i, turn in enumerate(log_turns):
            text = turn.get("text", "").strip()
            if not text:
                continue
            role = "Guest" if i % 2 == 0 else "Hotel Agent"
            chat_lines.append(f"{role}: {text}")
        chat_text = "\n".join(chat_lines)

        if len(chat_text) < 100:
            continue

        # ── Format B: belief state from system metadata turns ─────────────────
        hotel_slots = {}
        for turn in log_turns:
            meta = turn.get("metadata", {})
            hotel_meta = meta.get("hotel", {})
            for section in ["book", "semi"]:
                for slot, val in hotel_meta.get(section, {}).items():
                    if val and val not in ("", "not mentioned"):
                        hotel_slots[slot] = val

        structured = {
            "domain": "hotel",
            "dialogue_id": dialogue_id,
            "turn_count": len(log_turns),
            "booking_request": hotel_slots if hotel_slots else {"intent": "hotel_booking"},
        }
        json_text = json.dumps(structured, indent=2)

        # ── Format C: system turns as log lines ───────────────────────────────
        log_lines = [
            "[INFO] Domain: hotel_booking",
            f"[INFO] Dialogue ID: {dialogue_id}",
            f"[INFO] Turn count: {len(log_turns)}",
        ]
        for i, turn in enumerate(log_turns):
            if i % 2 == 0:  # user turns — skip
                continue
            msg = turn.get("text", "").strip().replace("\n", " ")
            if not msg:
                continue
            msg_lower = msg.lower()
            if any(w in msg_lower for w in ["sorry", "cannot", "not available", "unfortunately"]):
                level = "WARN"
            elif any(w in msg_lower for w in ["booked", "reserved", "confirmed", "reference"]):
                level = "INFO"
            else:
                level = "DEBUG"
            log_lines.append(f"[{level}] turn_{i:02d} agent: {msg[:120]}")

        if len(log_lines) < 4:
            continue

        log_text = "\n".join(log_lines)
        triplets.append((chat_text, json_text, log_text))

    return triplets


def write_control_triplets(triplets, output_dir: str):
    dirs = {
        "control_chat": os.path.join(output_dir, "control_chat"),
        "control_json": os.path.join(output_dir, "control_json"),
        "control_logs": os.path.join(output_dir, "control_logs"),
    }
    for d in dirs.values():
        os.makedirs(d, exist_ok=True)

    for i, (chat, js, log) in enumerate(triplets):
        stem = f"issue_{i+1:03d}"
        with open(os.path.join(dirs["control_chat"], f"{stem}.txt"), "w") as f:
            f.write(chat)
        with open(os.path.join(dirs["control_json"], f"{stem}.json"), "w") as f:
            f.write(js)
        with open(os.path.join(dirs["control_logs"], f"{stem}.log"), "w") as f:
            f.write(log)

    print(f"\n✅ Wrote {len(triplets)} control triplets to {output_dir}/")
    print(f"   control_chat/ → {len(triplets)} .txt files  (Format A — hotel guest conversations)")
    print(f"   control_json/ → {len(triplets)} .json files (Format B — booking belief state)")
    print(f"   control_logs/ → {len(triplets)} .log files  (Format C — system act log lines)")
    print()
    print("Next: run Benchmark 3")
    print(f"  go run main.go --exp h5 --mode control --data {output_dir}")


def main():
    parser = argparse.ArgumentParser(description="Prepare H5 control dataset from MultiWOZ 2.4 hotel domain")
    parser.add_argument("--output", default="./data/h5", help="Output directory (default: ./data/h5)")
    parser.add_argument("--count", type=int, default=100, help="Number of triplets to extract (default: 100)")
    args = parser.parse_args()

    print(f"Target:  {args.count} hotel booking triplets")
    print(f"Output:  {args.output}")
    print(f"Domain:  hotel (MultiWOZ 2.4) — semantically distinct from GHArchive CI/deployment data")
    print()

    triplets = load_multiwoz_hotel(args.count)

    if not triplets:
        print("❌ No pure hotel dialogues found. Check dataset loading.")
        sys.exit(1)

    if len(triplets) < args.count:
        print(f"⚠️  Only {len(triplets)} pure hotel dialogues found (wanted {args.count}).")
        print("   Proceeding with what we have — control dataset will be smaller.")

    write_control_triplets(triplets, args.output)


if __name__ == "__main__":
    main()
