"""
H3 — Asymptotic Latency Data Generator
=======================================
Generates a Stochastic Block Model (SBM) graph and bulk-loads it into ArangoDB.

Clusters:
  0 = clinical
  1 = infrastructure
  2 = personal

Node distribution (per tier):
  60% Concepts, 20% Identities, 10% Projects, 10% Tools

Edge density:
  ~8 edges per node average
  80% intra-cluster, 20% cross-cluster

Usage:
    python generate_h3_data.py --nodes 10000 --tier 1 --url http://localhost:8529 --pass <password>
    python generate_h3_data.py --nodes 100000 --tier 2 --url http://localhost:8529 --pass <password>
"""

import argparse
import json
import os
import random
import sys
import time
from typing import List, Dict, Any

import requests

# ── Config ─────────────────────────────────────────────────────────────────────

CLUSTERS       = ["clinical", "infrastructure", "personal"]
COLLECTIONS    = ["Concepts", "Identities", "Projects", "Tools"]
EDGE_COLLECTION = "MemoryEdges"
TENANT_ID      = "tenant_h3_experiment"

# Node distribution ratios
DIST = {"Concepts": 0.60, "Identities": 0.20, "Projects": 0.10, "Tools": 0.10}

# Edge density (avg edges per node)
AVG_DEGREE     = 8
INTRA_RATIO    = 0.80   # 80% edges stay within cluster
BATCH_SIZE     = 500    # docs per HTTP insert call

RELATIONS = [
    "related_to", "part_of", "depends_on", "owns", "uses",
    "produces", "consumes", "monitors", "configures", "triggers",
]

CLINICAL_NAMES = [
    "patient_diagnosis", "treatment_protocol", "lab_results", "medication_dosage",
    "clinical_trial", "diagnostic_imaging", "patient_history", "symptom_analysis",
    "surgical_procedure", "rehabilitation_plan", "infection_control", "vital_signs",
    "genetic_marker", "pathology_report", "nursing_care_plan",
]
INFRA_NAMES = [
    "kubernetes_cluster", "deployment_pipeline", "load_balancer", "database_migration",
    "api_gateway", "service_mesh", "container_registry", "monitoring_dashboard",
    "ci_cd_workflow", "infrastructure_as_code", "secret_management", "network_policy",
    "auto_scaling_policy", "backup_strategy", "disaster_recovery",
]
PERSONAL_NAMES = [
    "user_preference", "communication_style", "project_ownership", "skill_profile",
    "team_membership", "work_schedule", "feedback_history", "goal_tracking",
    "meeting_notes", "decision_log", "learning_objective", "performance_review",
    "stakeholder_relationship", "action_item", "personal_milestone",
]

CLUSTER_NAMES = [CLINICAL_NAMES, INFRA_NAMES, PERSONAL_NAMES]

# ── ArangoDB helpers ────────────────────────────────────────────────────────────

class ArangoClient:
    def __init__(self, url: str, password: str, db: str = "athena_ltm"):
        self.base   = f"{url}/_db/{db}"
        self.auth   = ("root", password)
        self.session = requests.Session()
        self.session.auth = self.auth
        self.session.headers.update({"Content-Type": "application/json"})

    def truncate(self, collection: str):
        resp = self.session.put(f"{self.base}/_api/collection/{collection}/truncate")
        resp.raise_for_status()

    def insert_batch(self, collection: str, docs: List[Dict[str, Any]]):
        resp = self.session.post(
            f"{self.base}/_api/document/{collection}",
            data=json.dumps(docs),
        )
        resp.raise_for_status()

    def count(self, collection: str) -> int:
        resp = self.session.get(f"{self.base}/_api/collection/{collection}/count")
        resp.raise_for_status()
        return resp.json()["count"]

    def get_sample_keys(self, collection: str, n: int = 200) -> List[str]:
        aql = f"FOR doc IN {collection} LIMIT {n} RETURN doc._id"
        resp = self.session.post(
            f"{self.base}/_api/cursor",
            data=json.dumps({"query": aql, "count": False}),
        )
        resp.raise_for_status()
        return resp.json().get("result", [])

# ── Node generation ─────────────────────────────────────────────────────────────

def make_node(idx: int, collection: str, cluster: int) -> Dict[str, Any]:
    names    = CLUSTER_NAMES[cluster]
    base_name = names[idx % len(names)]
    return {
        "_key":        f"{collection.lower()}_{idx:08d}",
        "name":        f"{base_name}_{idx}",
        "summary":     f"H3 synthetic {CLUSTERS[cluster]} node {idx} in {collection}.",
        "community_id": cluster,
        "bridge_score": 0,
        "tenant_id":   TENANT_ID,
        "source_type": collection.lower(),
        "heat_score":  round(random.uniform(0.3, 1.0), 3),
        "created_at":  int(time.time()),
    }

def generate_nodes(n_nodes: int) -> Dict[str, List[Dict[str, Any]]]:
    """Returns dict of collection → list of node dicts, with cluster IDs assigned."""
    counts = {col: max(1, int(n_nodes * ratio)) for col, ratio in DIST.items()}
    # Fix rounding
    total = sum(counts.values())
    counts["Concepts"] += n_nodes - total

    nodes_by_collection: Dict[str, List[Dict[str, Any]]] = {c: [] for c in COLLECTIONS}
    global_idx = 0
    for col, count in counts.items():
        for i in range(count):
            cluster = (global_idx * 3) // n_nodes  # distribute evenly across clusters
            cluster = min(cluster, 2)
            nodes_by_collection[col].append(make_node(global_idx, col, cluster))
            global_idx += 1

    return nodes_by_collection

# ── Edge generation ─────────────────────────────────────────────────────────────

def generate_edges(
    nodes_by_collection: Dict[str, List[Dict[str, Any]]],
    n_nodes: int,
) -> List[Dict[str, Any]]:
    # Flatten all nodes with their full ArangoDB IDs and cluster IDs
    all_nodes: List[tuple] = []  # (full_id, cluster)
    for col, nodes in nodes_by_collection.items():
        for node in nodes:
            full_id = f"{col}/{node['_key']}"
            all_nodes.append((full_id, node["community_id"]))

    # Group by cluster for fast intra-cluster sampling
    cluster_nodes: Dict[int, List[str]] = {0: [], 1: [], 2: []}
    for full_id, cluster in all_nodes:
        cluster_nodes[cluster].append(full_id)

    n_edges = n_nodes * AVG_DEGREE
    edges: List[Dict[str, Any]] = []
    seen = set()

    print(f"  Generating ~{n_edges:,} edges ({INTRA_RATIO*100:.0f}% intra-cluster)...")

    attempts = 0
    max_attempts = n_edges * 3
    while len(edges) < n_edges and attempts < max_attempts:
        attempts += 1
        from_id, from_cluster = random.choice(all_nodes)

        if random.random() < INTRA_RATIO:
            # Intra-cluster: pick target from same cluster
            pool = cluster_nodes[from_cluster]
        else:
            # Cross-cluster: pick a different cluster
            other = random.choice([c for c in range(3) if c != from_cluster])
            pool = cluster_nodes[other]

        to_id = random.choice(pool)

        if from_id == to_id:
            continue

        key = (from_id, to_id)
        if key in seen:
            continue
        seen.add(key)

        edges.append({
            "_from":        from_id,
            "_to":          to_id,
            "relation":     random.choice(RELATIONS),
            "context_nuance": "H3 synthetic edge",
            "confidence":   round(random.uniform(0.5, 1.0), 3),
            "weight":       round(random.uniform(0.1, 5.0), 2),
            "heat_score":   round(random.uniform(0.3, 1.0), 3),
            "tenant_id":    TENANT_ID,
        })

    return edges


def bulk_insert(client: ArangoClient, collection: str, docs: List[Dict[str, Any]], label: str):
    total   = len(docs)
    batches = (total + BATCH_SIZE - 1) // BATCH_SIZE
    inserted = 0
    for i in range(batches):
        batch = docs[i * BATCH_SIZE : (i + 1) * BATCH_SIZE]
        client.insert_batch(collection, batch)
        inserted += len(batch)
        pct = inserted / total * 100
        print(f"\r    {label}: {inserted:,}/{total:,} ({pct:.0f}%)", end="", flush=True)
    print()


# ── Main ────────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="H3 SBM data generator")
    parser.add_argument("--nodes",      type=int, required=True,  help="Total node count (e.g. 10000)")
    parser.add_argument("--tier",       type=int, required=True,  choices=[1, 2, 3], help="Tier label")
    parser.add_argument("--url",        default="http://localhost:8529", help="ArangoDB URL")
    parser.add_argument("--pass",       dest="password", default=None, help="ArangoDB root password")
    parser.add_argument("--db",         default="athena_ltm", help="ArangoDB database name")
    parser.add_argument("--no-truncate", action="store_true", help="Skip truncating existing data")
    parser.add_argument("--output-dir", dest="output_dir", default=None,
                        help="Write JSONL files here instead of inserting into ArangoDB")
    args = parser.parse_args()

    if args.output_dir is None and args.password is None:
        print("ERROR: --pass is required unless --output-dir is set")
        sys.exit(1)

    print("=" * 65)
    print(f"H3 Data Generator — Tier {args.tier} ({args.nodes:,} nodes)")
    print("=" * 65)
    print(f"  ArangoDB : {args.url}")
    print(f"  Database : {args.db}")
    print(f"  Clusters : {CLUSTERS}")
    print(f"  Avg degree: {AVG_DEGREE}  ({INTRA_RATIO*100:.0f}% intra-cluster)")
    print()

    t0 = time.time()

    print("  Generating nodes...")
    nodes_by_collection = generate_nodes(args.nodes)
    for col, nodes in nodes_by_collection.items():
        print(f"    {col}: {len(nodes):,} nodes")
    print()

    print("  Generating edges...")
    edges = generate_edges(nodes_by_collection, args.nodes)
    print(f"    Generated {len(edges):,} edges")
    print()

    if args.output_dir:
        # ── File mode: write JSONL for arangoimport ──────────────────────────
        os.makedirs(args.output_dir, exist_ok=True)
        print(f"  Writing JSONL files to {args.output_dir}/")
        for col, nodes in nodes_by_collection.items():
            path = os.path.join(args.output_dir, f"{col}.jsonl")
            with open(path, "w") as f:
                for doc in nodes:
                    f.write(json.dumps(doc) + "\n")
            print(f"    Wrote {path}  ({len(nodes):,} docs)")

        edge_path = os.path.join(args.output_dir, f"MemoryEdges.jsonl")
        with open(edge_path, "w") as f:
            for edge in edges:
                f.write(json.dumps(edge) + "\n")
        print(f"    Wrote {edge_path}  ({len(edges):,} docs)")
        print()

        # Sample from in-memory data (no ArangoDB needed)
        samples = []
        for col, nodes in nodes_by_collection.items():
            sample = random.sample(nodes, min(50, len(nodes)))
            samples.extend(f"{col}/{n['_key']}" for n in sample)
        random.shuffle(samples)
        samples_file = f"h3_sample_nodes_tier{args.tier}.json"
        with open(samples_file, "w") as f:
            json.dump(samples, f)
        print(f"  Saved {len(samples)} sample node IDs → {samples_file}")
        print()

        elapsed = time.time() - t0
        print(f"  Done in {elapsed:.1f}s")
        print(f"  Total nodes: {args.nodes:,}  |  Edges: {len(edges):,}")

    else:
        # ── HTTP mode: insert directly into ArangoDB ─────────────────────────
        client = ArangoClient(args.url, args.password, args.db)

        if not args.no_truncate:
            print("  Truncating existing collections...")
            for col in COLLECTIONS + [EDGE_COLLECTION]:
                try:
                    client.truncate(col)
                    print(f"    Truncated {col}")
                except Exception as e:
                    print(f"    WARN: could not truncate {col}: {e}")
            print()

        print("  Inserting nodes into ArangoDB...")
        for col, nodes in nodes_by_collection.items():
            bulk_insert(client, col, nodes, col)
        print()

        print("  Inserting edges into ArangoDB...")
        bulk_insert(client, EDGE_COLLECTION, edges, EDGE_COLLECTION)
        print()

        print("  Verifying counts...")
        total_nodes = 0
        for col in COLLECTIONS:
            n = client.count(col)
            print(f"    {col}: {n:,}")
            total_nodes += n
        edge_count = client.count(EDGE_COLLECTION)
        print(f"    MemoryEdges: {edge_count:,}")
        print()

        elapsed = time.time() - t0
        print(f"  Done in {elapsed:.1f}s")
        print(f"  Total nodes: {total_nodes:,}  |  Edges: {edge_count:,}")

        samples_file = f"h3_sample_nodes_tier{args.tier}.json"
        print(f"\n  Sampling 200 node IDs for load test → {samples_file}")
        samples = []
        for col in COLLECTIONS:
            samples.extend(client.get_sample_keys(col, 50))
        random.shuffle(samples)
        with open(samples_file, "w") as f:
            json.dump(samples, f)
        print(f"  Saved {len(samples)} sample node IDs")

    print("=" * 65)


if __name__ == "__main__":
    main()
