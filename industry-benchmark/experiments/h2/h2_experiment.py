#!/usr/bin/env python3
"""
H2 — Cross-Service Bridge (Anti-Tunneling) Experiment
======================================================

Tests whether Athena's LTM graph traversal can surface context from two
semantically isolated service domains (clinical / infrastructure) when a
bridge node connects them — while raw Milvus vector search cannot.

Run order in the benchmark suite: H5 → H1 → H3 → H4 → H2 → H6
Prerequisite: community_id must be populated on all LTM nodes (done by H4 K8s job).

Usage:
    python h2_experiment.py \\
        --athena-url  https://api.console.dromos.prescottdata.io/memory \\
        --arango-url  http://memory-os-arangodb.bravesea-cce204b8.eastus.azurecontainerapps.io:8529 \\
        --arango-pass <password> \\
        --api-key     dev-api-key \\
        --tenant-id   tenant_dromos_research \\
        --user-id     user_athena_benchmark \\
        --output      h2_results.json

Phases:
    1. Discover the two largest communities in ArangoDB (clinical / infra proxies)
    2. Inject Domain A (clinical) interactions via Athena API  → MTM chains
    3. Inject Domain B (infra)     interactions via Athena API  → MTM chains
    4. Inject test LTM nodes directly into ArangoDB with community_ids from Phase 1
    5. Baseline SearchMemory test  — bridge does NOT exist yet
    6. Inject bridge event via Athena API + bridge LTM node with cross-community edges
    7. Trigger CalculateBridgeEntities via Athena admin endpoint
    8. Verify bridge node has is_bridge: true, bridge_score >= 2
    9. Post-bridge SearchMemory test
   10. Compare cognitive_chain (Milvus) vs ltm_node (graph) results
   11. Write results to JSON
"""

import argparse
import json
import sys
import time
from datetime import datetime, timezone

import requests
from arango import ArangoClient

# ---------------------------------------------------------------------------
# BEIR-style domain documents
# Domain A: BEIR/TREC-COVID  — biomedical / clinical
# Domain B: BEIR/SciFact     — infrastructure / technical
# ---------------------------------------------------------------------------

CLINICAL_INTERACTIONS = [
    {
        "user": "What are the treatment protocols for COVID-19 patients with pre-existing diabetes?",
        "agent": "AMPATH clinical guidelines recommend HbA1c monitoring every 3 months for diabetic COVID-19 patients. Insulin dose adjustment is required during acute infection due to glucose dysregulation.",
    },
    {
        "user": "How should clinicians manage hyperglycemia in hospitalised COVID-19 patients?",
        "agent": "Continuous glucose monitoring should be initiated within 24 hours of admission. Corticosteroid therapy commonly induces hyperglycemia — target blood glucose 140–180 mg/dL per AMPATH-DM2 protocol.",
    },
    {
        "user": "What does AMPATH-DM2 say about discharge planning for diabetic COVID-19 survivors?",
        "agent": "AMPATH-DM2 guidelines specify a structured 6-week follow-up plan post-discharge including HbA1c re-assessment, medication reconciliation, and community health worker outreach.",
    },
    {
        "user": "Is there evidence that COVID-19 causes new-onset diabetes?",
        "agent": "Multiple TREC-COVID indexed studies (CORD-19 corpus) demonstrate SARS-CoV-2 directly damages pancreatic beta cells. New-onset T1D cases increased 35% in paediatric populations during the pandemic.",
    },
    {
        "user": "What clinical biomarkers predict severe COVID-19 outcomes in diabetic patients?",
        "agent": "Elevated IL-6, CRP > 100 mg/L, and fasting glucose > 200 mg/dL on admission are independent predictors of ICU admission. AMPATH scores these in its risk stratification tool.",
    },
]

INFRA_INTERACTIONS = [
    {
        "user": "How do we configure horizontal pod autoscaling for our production Kubernetes cluster?",
        "agent": "Set the HPA target CPU utilisation to 70% and configure minReplicas=2, maxReplicas=10 in the HPA spec. Ensure resource requests are defined on all containers — HPA ignores pods without them.",
    },
    {
        "user": "What is the recommended Helm chart version for deploying our pipeline to the new cluster?",
        "agent": "Use Helm 3.12.1 with the dromos-pipeline chart v2.4.0. The chart includes resource limits pre-configured for the AKS node pool (Standard_D4s_v3). Run helm upgrade --install with --atomic for safe rollouts.",
    },
    {
        "user": "How should we handle secrets management in the Kubernetes deployment?",
        "agent": "Use Azure Key Vault with the Secrets Store CSI Driver. Mount secrets as volumes rather than environment variables. Rotate secrets every 90 days via the key vault access policy.",
    },
    {
        "user": "What monitoring stack should we deploy alongside the application in Kubernetes?",
        "agent": "Deploy the kube-prometheus-stack Helm chart (Prometheus + Grafana + Alertmanager). Configure PodMonitor resources for each service. Alert on p99 latency > 500ms and error rate > 1%.",
    },
    {
        "user": "How do we set up namespace isolation between the staging and production environments?",
        "agent": "Use Kubernetes NetworkPolicy resources to enforce namespace isolation. Apply RBAC ClusterRoles per team. Separate node pools using taints and tolerations — production nodes tainted NoSchedule for staging workloads.",
    },
]

# The bridge event — deliberately spans both domains
BRIDGE_INTERACTION = {
    "user": "I need to deploy the AMPATH-DM2 clinical guidelines onto our new Kubernetes cluster infrastructure.",
    "agent": (
        "Deploying AMPATH-DM2 onto Kubernetes requires packaging the guideline engine as a container. "
        "Use the dromos-pipeline Helm chart with a custom values.yaml specifying the AMPATH-DM2 image tag. "
        "Clinical data volumes should be mounted as PVCs backed by Azure Disk with GRS redundancy. "
        "Apply a NetworkPolicy that permits only the clinical namespace to reach the guideline service."
    ),
}

# The search query used in all benchmarks
SEARCH_QUERY = "How do we scale the AMPATH deployment?"


# ---------------------------------------------------------------------------
# Athena HTTP client helpers
# ---------------------------------------------------------------------------

class AthenaClient:
    def __init__(self, base_url: str, api_key: str):
        self.base = base_url.rstrip("/")
        self.headers = {"Content-Type": "application/json", "X-API-Key": api_key}

    def _post(self, path: str, payload: dict) -> dict:
        r = requests.post(f"{self.base}{path}", json=payload, headers=self.headers, timeout=30)
        r.raise_for_status()
        return r.json()

    def create_session(self, tenant_id: str, user_id: str, agent_id: str, label: str) -> str:
        resp = self._post("/api/v1/sessions", {
            "tenant_id": tenant_id,
            "user_id": user_id,
            "agent_id": agent_id,
            "metadata": {"experiment": "h2", "label": label},
        })
        return resp["sessionId"]

    def store_interaction(self, session_id: str, user_msg: str, agent_msg: str) -> bool:
        resp = self._post(f"/api/v1/sessions/{session_id}/interactions", {
            "userMessage": user_msg,
            "agentResponse": agent_msg,
        })
        return resp.get("success", False)

    def search_memory(self, session_id: str, query: str, limit: int = 10) -> list:
        resp = self._post(f"/api/v1/sessions/{session_id}/context/search", {
            "query": query,
            "limit": limit,
        })
        return resp.get("results", [])

    def trigger_analytics(self) -> dict:
        resp = self._post("/api/v1/admin/analytics/trigger", {})
        return resp


# ---------------------------------------------------------------------------
# ArangoDB helpers
# ---------------------------------------------------------------------------

def arango_connect(url: str, password: str, db_name: str = "athena_ltm"):
    client = ArangoClient(hosts=url)
    return client.db(db_name, username="root", password=password)


def discover_top_communities(db, n: int = 2) -> list:
    """Return the n largest community IDs by node count across all node collections."""
    aql = """
        FOR doc IN UNION(
            (FOR d IN Concepts    FILTER HAS(d, "community_id") RETURN d.community_id),
            (FOR d IN Identities  FILTER HAS(d, "community_id") RETURN d.community_id),
            (FOR d IN Projects    FILTER HAS(d, "community_id") RETURN d.community_id),
            (FOR d IN Tools       FILTER HAS(d, "community_id") RETURN d.community_id)
        )
        COLLECT cid = doc WITH COUNT INTO cnt
        SORT cnt DESC
        LIMIT @n
        RETURN { community_id: cid, count: cnt }
    """
    cursor = db.aql.execute(aql, bind_vars={"n": n})
    return [row["community_id"] for row in cursor]


def inject_ltm_node(db, collection: str, key: str, name: str, community_id: int) -> str:
    """Upsert a node into the given collection. Returns the full ArangoDB _id."""
    aql = f"""
        UPSERT {{ _key: @key }}
        INSERT {{ _key: @key, name: @name, community_id: @cid, created_at: @now, source: "h2_experiment" }}
        UPDATE {{ name: @name, community_id: @cid, source: "h2_experiment" }}
        IN {collection}
        RETURN NEW
    """
    cursor = db.aql.execute(aql, bind_vars={
        "key": key,
        "name": name,
        "cid": community_id,
        "now": datetime.now(timezone.utc).isoformat(),
    })
    return list(cursor)[0]["_id"]


def inject_ltm_edge(db, from_id: str, to_id: str, relation: str, context: str):
    """Upsert a MemoryEdge between two node _ids."""
    aql = """
        UPSERT { _from: @from, _to: @to, relation: @rel }
        INSERT {
            _from: @from, _to: @to, relation: @rel,
            context_nuance: @ctx, confidence: 0.85, weight: 1, heat_score: 0.7,
            created_at: @now, last_seen: @now
        }
        UPDATE { context_nuance: @ctx, weight: OLD.weight + 1, last_seen: @now }
        IN MemoryEdges
    """
    db.aql.execute(aql, bind_vars={
        "from": from_id,
        "to": to_id,
        "rel": relation,
        "ctx": context,
        "now": datetime.now(timezone.utc).isoformat(),
    })


def get_node_bridge_status(db, node_key: str, collection: str) -> dict:
    """Return is_bridge and bridge_score for a node."""
    aql = f"FOR doc IN {collection} FILTER doc._key == @key LIMIT 1 RETURN doc"
    cursor = db.aql.execute(aql, bind_vars={"key": node_key})
    rows = list(cursor)
    if not rows:
        return {"found": False}
    doc = rows[0]
    return {
        "found": True,
        "is_bridge": doc.get("is_bridge", False),
        "bridge_score": doc.get("bridge_score", 0),
        "community_id": doc.get("community_id"),
    }


def count_cross_community_edges(db, community_a: int, community_b: int) -> int:
    """Count edges that cross between community_a and community_b."""
    aql = """
        FOR edge IN MemoryEdges
            LET from_node = DOCUMENT(edge._from)
            LET to_node   = DOCUMENT(edge._to)
            FILTER (from_node.community_id == @ca AND to_node.community_id == @cb)
                OR (from_node.community_id == @cb AND to_node.community_id == @ca)
            COLLECT WITH COUNT INTO n
            RETURN n
    """
    cursor = db.aql.execute(aql, bind_vars={"ca": community_a, "cb": community_b})
    rows = list(cursor)
    return rows[0] if rows else 0


def classify_results(results: list) -> dict:
    """Split SearchMemory results into vector (MTM) and graph (LTM) buckets."""
    chains = [r for r in results if r.get("sourceType") == "cognitive_chain"]
    graph  = [r for r in results if r.get("sourceType") == "ltm_node"]
    return {"milvus_chains": chains, "ltm_graph_nodes": graph}


# ---------------------------------------------------------------------------
# Main experiment
# ---------------------------------------------------------------------------

def run(args):
    print("\n=== H2 Anti-Tunneling Experiment ===")
    print(f"Athena: {args.athena_url}")
    print(f"ArangoDB: {args.arango_url} / {args.arango_db}")

    athena = AthenaClient(args.athena_url, args.api_key)
    db = arango_connect(args.arango_url, args.arango_pass, args.arango_db)

    results = {
        "hypothesis": "H2 — Cross-Service Bridge (Anti-Tunneling)",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "tenant_id": args.tenant_id,
        "user_id": args.user_id,
        "phases": {},
    }

    # ------------------------------------------------------------------
    # Phase 1 — Discover the two largest communities in the LTM graph
    # ------------------------------------------------------------------
    print("\n[Phase 1] Discovering top-2 communities from H4 Louvain run...")
    top_communities = discover_top_communities(db, n=2)
    if len(top_communities) < 2:
        print("ERROR: Fewer than 2 distinct community_ids found in ArangoDB.")
        print("  → Ensure the H4 K8s job has completed and community_id is written to nodes.")
        sys.exit(1)

    clinical_cid = top_communities[0]
    infra_cid    = top_communities[1]
    print(f"  Clinical community_id proxy : {clinical_cid}")
    print(f"  Infra    community_id proxy : {infra_cid}")
    results["phases"]["phase_1_community_discovery"] = {
        "clinical_community_id": clinical_cid,
        "infra_community_id": infra_cid,
    }

    # ------------------------------------------------------------------
    # Phase 2 — Inject clinical (DocIntel) interactions into Athena MTM
    # ------------------------------------------------------------------
    print("\n[Phase 2] Injecting Domain A — clinical interactions (DocIntel agent)...")
    clinical_session = athena.create_session(
        args.tenant_id, args.user_id, "docintel", "h2-clinical"
    )
    print(f"  Session: {clinical_session}")
    for i, pair in enumerate(CLINICAL_INTERACTIONS):
        ok = athena.store_interaction(clinical_session, pair["user"], pair["agent"])
        print(f"  [{i+1}/{len(CLINICAL_INTERACTIONS)}] {'ok' if ok else 'FAILED'}")
        time.sleep(0.3)
    results["phases"]["phase_2_clinical_injection"] = {
        "session_id": clinical_session,
        "interactions_stored": len(CLINICAL_INTERACTIONS),
    }

    # ------------------------------------------------------------------
    # Phase 3 — Inject infra (Colabra) interactions into Athena MTM
    # ------------------------------------------------------------------
    print("\n[Phase 3] Injecting Domain B — infra interactions (Colabra agent)...")
    infra_session = athena.create_session(
        args.tenant_id, args.user_id, "colabra", "h2-infra"
    )
    print(f"  Session: {infra_session}")
    for i, pair in enumerate(INFRA_INTERACTIONS):
        ok = athena.store_interaction(infra_session, pair["user"], pair["agent"])
        print(f"  [{i+1}/{len(INFRA_INTERACTIONS)}] {'ok' if ok else 'FAILED'}")
        time.sleep(0.3)
    results["phases"]["phase_3_infra_injection"] = {
        "session_id": infra_session,
        "interactions_stored": len(INFRA_INTERACTIONS),
    }

    # ------------------------------------------------------------------
    # Phase 4 — Inject test LTM nodes directly into ArangoDB
    # ------------------------------------------------------------------
    print("\n[Phase 4] Injecting test LTM nodes into ArangoDB...")

    # Clinical nodes (Concepts collection, community = clinical_cid)
    c_nodes = [
        ("ampath_dm2_guidelines",        "AMPATH DM2 Guidelines",            clinical_cid),
        ("covid19_treatment_protocol",   "COVID-19 Treatment Protocol",      clinical_cid),
        ("clinical_diabetes_management", "Clinical Diabetes Management",     clinical_cid),
    ]
    # Infra nodes (Concepts collection, community = infra_cid)
    i_nodes = [
        ("kubernetes_deployment_pipeline", "Kubernetes Deployment Pipeline", infra_cid),
        ("helm_chart_configuration",       "Helm Chart Configuration",       infra_cid),
        ("cluster_autoscaling_policy",     "Cluster Autoscaling Policy",     infra_cid),
    ]

    node_ids = {}
    for key, name, cid in c_nodes + i_nodes:
        nid = inject_ltm_node(db, "Concepts", key, name, cid)
        node_ids[key] = nid
        domain = "clinical" if cid == clinical_cid else "infra"
        print(f"  Injected [{domain}] {name}  →  {nid}")

    # Wire intra-domain edges (clinical ↔ clinical, infra ↔ infra)
    inject_ltm_edge(db,
        node_ids["ampath_dm2_guidelines"],
        node_ids["covid19_treatment_protocol"],
        "RELATES_TO", "AMPATH guidelines applied in COVID-19 treatment")
    inject_ltm_edge(db,
        node_ids["covid19_treatment_protocol"],
        node_ids["clinical_diabetes_management"],
        "RELATES_TO", "COVID-19 worsens diabetes outcomes")
    inject_ltm_edge(db,
        node_ids["kubernetes_deployment_pipeline"],
        node_ids["helm_chart_configuration"],
        "USES", "pipeline deployed via Helm")
    inject_ltm_edge(db,
        node_ids["helm_chart_configuration"],
        node_ids["cluster_autoscaling_policy"],
        "RELATES_TO", "Helm chart defines HPA policy")
    print("  Intra-domain edges written.")

    # Confirm zero cross-community edges before bridge injection
    cross_before = count_cross_community_edges(db, clinical_cid, infra_cid)
    print(f"  Cross-community edges before bridge: {cross_before}")
    results["phases"]["phase_4_ltm_injection"] = {
        "nodes": {k: v for k, v in node_ids.items()},
        "cross_community_edges_before_bridge": cross_before,
    }

    if cross_before > 0:
        print("  WARNING: Cross-community edges already exist — domain isolation is contaminated.")

    # ------------------------------------------------------------------
    # Phase 5 — Baseline test: bridge does NOT exist yet
    # ------------------------------------------------------------------
    print(f"\n[Phase 5] Baseline SearchMemory (no bridge)...")
    print(f"  Query: \"{SEARCH_QUERY}\"")
    baseline_results = athena.search_memory(clinical_session, SEARCH_QUERY, limit=10)
    baseline_classified = classify_results(baseline_results)

    print(f"  Milvus chains returned  : {len(baseline_classified['milvus_chains'])}")
    print(f"  LTM graph nodes returned: {len(baseline_classified['ltm_graph_nodes'])}")
    for r in baseline_results:
        print(f"    [{r.get('sourceType','?')}] {r.get('content','')[:80]}")

    results["phases"]["phase_5_baseline"] = {
        "query": SEARCH_QUERY,
        "raw_results": baseline_results,
        "milvus_chain_count": len(baseline_classified["milvus_chains"]),
        "ltm_node_count": len(baseline_classified["ltm_graph_nodes"]),
        "ltm_node_keys": [r.get("sourceId") for r in baseline_classified["ltm_graph_nodes"]],
    }

    # ------------------------------------------------------------------
    # Phase 6 — Inject bridge event + bridge LTM node
    # ------------------------------------------------------------------
    print("\n[Phase 6] Injecting bridge event + bridge LTM node...")

    # Store bridge interaction through Athena (creates MTM chain)
    bridge_session = athena.create_session(
        args.tenant_id, args.user_id, "docintel", "h2-bridge"
    )
    ok = athena.store_interaction(
        bridge_session,
        BRIDGE_INTERACTION["user"],
        BRIDGE_INTERACTION["agent"],
    )
    print(f"  Bridge interaction stored: {'ok' if ok else 'FAILED'}")

    # Inject bridge LTM node with cross-domain edges
    bridge_id = inject_ltm_node(db, "Concepts",
        "ampath_kubernetes_deployment",
        "AMPATH Kubernetes Deployment",
        clinical_cid)  # bridge lives in clinical community but edges cross into infra
    node_ids["ampath_kubernetes_deployment"] = bridge_id
    print(f"  Bridge node: {bridge_id}")

    # Cross-domain edges: bridge → clinical AND bridge → infra
    inject_ltm_edge(db,
        bridge_id,
        node_ids["ampath_dm2_guidelines"],
        "WORKS_ON", "AMPATH deployment implements DM2 clinical guidelines")
    inject_ltm_edge(db,
        bridge_id,
        node_ids["kubernetes_deployment_pipeline"],
        "USES", "AMPATH guidelines deployed on Kubernetes pipeline")
    print("  Cross-domain edges written.")

    cross_after = count_cross_community_edges(db, clinical_cid, infra_cid)
    print(f"  Cross-community edges after bridge injection: {cross_after}")
    results["phases"]["phase_6_bridge_injection"] = {
        "bridge_session_id": bridge_session,
        "bridge_node_id": bridge_id,
        "cross_community_edges_after_bridge": cross_after,
    }

    # ------------------------------------------------------------------
    # Phase 7 — Trigger CalculateBridgeEntities
    # ------------------------------------------------------------------
    print("\n[Phase 7] Triggering CalculateBridgeEntities...")
    try:
        trigger_resp = athena.trigger_analytics()
        print(f"  Response: {trigger_resp}")
    except Exception as e:
        print(f"  WARNING: Trigger request failed: {e}")
        trigger_resp = {"error": str(e)}

    print("  Waiting 10s for bridge calculation to complete...")
    time.sleep(10)

    # Verify bridge node
    bridge_status = get_node_bridge_status(db, "ampath_kubernetes_deployment", "Concepts")
    print(f"  Bridge node status: {bridge_status}")
    results["phases"]["phase_7_bridge_activation"] = {
        "trigger_response": trigger_resp,
        "bridge_node_status": bridge_status,
        "is_bridge_pass": bridge_status.get("is_bridge", False),
        "bridge_score_pass": bridge_status.get("bridge_score", 0) >= 2,
    }

    # ------------------------------------------------------------------
    # Phase 8 — Post-bridge SearchMemory test
    # ------------------------------------------------------------------
    print(f"\n[Phase 8] Post-bridge SearchMemory...")
    print(f"  Query: \"{SEARCH_QUERY}\"")
    postbridge_results = athena.search_memory(clinical_session, SEARCH_QUERY, limit=10)
    postbridge_classified = classify_results(postbridge_results)

    # Determine which domains the LTM nodes come from
    ltm_node_communities = []
    for r in postbridge_classified["ltm_graph_nodes"]:
        node_key = r.get("sourceId", "")
        if node_key in node_ids:
            if node_ids[node_key].split("/")[1] in [n[0] for n in c_nodes]:
                ltm_node_communities.append("clinical")
            elif node_ids[node_key].split("/")[1] in [n[0] for n in i_nodes]:
                ltm_node_communities.append("infra")
            else:
                ltm_node_communities.append("bridge_or_unknown")

    domains_found = set(ltm_node_communities)
    cross_domain_recall = len(domains_found) > 1

    print(f"  Milvus chains returned  : {len(postbridge_classified['milvus_chains'])}")
    print(f"  LTM graph nodes returned: {len(postbridge_classified['ltm_graph_nodes'])}")
    print(f"  Domains in LTM results  : {domains_found}")
    print(f"  Cross-Domain Recall     : {'PASS' if cross_domain_recall else 'FAIL'}")
    for r in postbridge_results:
        print(f"    [{r.get('sourceType','?')}] {r.get('content','')[:80]}")

    results["phases"]["phase_8_post_bridge"] = {
        "query": SEARCH_QUERY,
        "raw_results": postbridge_results,
        "milvus_chain_count": len(postbridge_classified["milvus_chains"]),
        "ltm_node_count": len(postbridge_classified["ltm_graph_nodes"]),
        "ltm_node_keys": [r.get("sourceId") for r in postbridge_classified["ltm_graph_nodes"]],
        "domains_found": list(domains_found),
        "cross_domain_recall": cross_domain_recall,
    }

    # ------------------------------------------------------------------
    # Phase 9 — Milvus anti-tunneling verification
    # ------------------------------------------------------------------
    print("\n[Phase 9] Anti-tunneling verification...")
    milvus_baseline  = baseline_classified["milvus_chains"]
    milvus_postbridge = postbridge_classified["milvus_chains"]
    milvus_changed = (
        set(r.get("sourceId") for r in milvus_postbridge) !=
        set(r.get("sourceId") for r in milvus_baseline)
    )

    print(f"  Milvus results changed after bridge: {'YES (unexpected)' if milvus_changed else 'NO (expected — vector is blind to bridge)'}")
    print(f"  Athena graph crossed domains: {'YES' if cross_domain_recall else 'NO'}")

    results["phases"]["phase_9_milvus_verification"] = {
        "milvus_results_changed_after_bridge": milvus_changed,
        "milvus_pass": not milvus_changed,
        "athena_cross_domain_pass": cross_domain_recall,
    }

    # ------------------------------------------------------------------
    # Final verdict
    # ------------------------------------------------------------------
    bridge_flag_pass  = results["phases"]["phase_7_bridge_activation"]["is_bridge_pass"]
    bridge_score_pass = results["phases"]["phase_7_bridge_activation"]["bridge_score_pass"]
    milvus_tunnel_pass = not milvus_changed
    athena_bridge_pass = cross_domain_recall

    all_pass = bridge_flag_pass and bridge_score_pass and milvus_tunnel_pass and athena_bridge_pass

    verdict = "PASS" if all_pass else (
        "PARTIAL" if any([bridge_flag_pass, milvus_tunnel_pass, athena_bridge_pass]) else "FAIL"
    )

    print("\n=== H2 Results Summary ===")
    print(f"  is_bridge flagged correctly : {'PASS' if bridge_flag_pass else 'FAIL'}")
    print(f"  bridge_score >= 2           : {'PASS' if bridge_score_pass else 'FAIL'}")
    print(f"  Milvus tunnels (no change)  : {'PASS' if milvus_tunnel_pass else 'FAIL'}")
    print(f"  Athena crosses domains      : {'PASS' if athena_bridge_pass else 'FAIL'}")
    print(f"  Overall verdict             : {verdict}")

    results["verdict"] = {
        "is_bridge_pass": bridge_flag_pass,
        "bridge_score_pass": bridge_score_pass,
        "milvus_tunnels_pass": milvus_tunnel_pass,
        "athena_cross_domain_pass": athena_bridge_pass,
        "overall": verdict,
    }

    with open(args.output, "w") as f:
        json.dump(results, f, indent=2, default=str)
    print(f"\nResults written to {args.output}")
    return verdict


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="H2 Anti-Tunneling Experiment")
    parser.add_argument(
        "--athena-url",
        default="https://api.console.dromos.prescottdata.io/memory",
        help="Athena Memory-OS base URL",
    )
    parser.add_argument(
        "--arango-url",
        default="http://memory-os-arangodb.bravesea-cce204b8.eastus.azurecontainerapps.io:8529",
        help="ArangoDB HTTP URL",
    )
    parser.add_argument(
        "--arango-pass",
        required=True,
        help="ArangoDB root password",
    )
    parser.add_argument(
        "--arango-db",
        default="athena_ltm",
        help="ArangoDB database name",
    )
    parser.add_argument(
        "--api-key",
        default="dev-api-key",
        help="Athena API key (X-API-Key header)",
    )
    parser.add_argument(
        "--tenant-id",
        default="tenant_dromos_research",
        help="Tenant ID",
    )
    parser.add_argument(
        "--user-id",
        default="user_athena_benchmark",
        help="User ID",
    )
    parser.add_argument(
        "--output",
        default="h2_results.json",
        help="Output JSON file path",
    )

    args = parser.parse_args()
    verdict = run(args)
    sys.exit(0 if verdict in ("PASS", "PARTIAL") else 1)
