# Architecture Decisions: Alert Deduplication & Auto-Closing

**Feature ID**: `alert-dedup-and-autoclose`  
**Status**: Accepted  

---

## ADR-0001: Composite Alert Fingerprinting over Flat AlertName

### Context
`state.AcquireIncidentLock` previously accepted only `alertName` (e.g. `PodCrashLoopBackOff`). In a 7-node multi-tenant Kubernetes cluster, different pods crashing at the same time collided on the same lock key. Furthermore, after a 5-minute timeout, the lock was released, allowing duplicate tickets for the same pod to be opened indefinitely.

### Decision
Generate a composite key combining `alertName`, `namespace`, and target resource (`pod`, `statefulset`, `deployment`, `service`). The fingerprint is computed as a SHA-256 hash formatted as `hex(hash(namespace + "|" + targetResource + "|" + alertName))`.

### Consequences
- Eliminates key collisions across disjoint services and pods.
- Establishes a predictable, deterministic unique identifier for every cluster incident.

---

## ADR-0002: Dual-Layer Deduplication (PostgreSQL + GitHub API Fallback)

### Context
`ai-incident-commander` can restart, redeploy, or lose in-memory cache. In-memory locks alone fail across deployments. Conversely, relying solely on GitHub API calls for deduplication risks rate-limit exhaustion during high-frequency alert storms.

### Decision
Implement dual-layer deduplication:
1. **Primary Layer**: A PostgreSQL table `cluster_incidents` storing `fingerprint`, `issue_number`, `state` (`firing`, `resolved`, `ignored`), and `occurrence_count`.
2. **Fallback Layer**: If the database is unreachable or lacks a record, query GitHub API for open issues with the label `ai-incident` matching the target resource title.

### Consequences
- Zero duplicate tickets across pod restarts and deployments.
- Minimal GitHub API usage during repetitive firing loops.

---

## ADR-0003: Suppression of Ephemeral On-Premise Hyper-V Node Outages

### Context
Hyper-V worker nodes `k3s-worker-1` through `k3s-worker-4` are on-premise development machines connected via WireGuard per ADR-0034 and ADR-0038. They are routinely powered off or rebooted. When rebooting, Flannel CNI delays trigger `PodCrashLoopBackOff` on DaemonSets (e.g. `vector`). Attempting GitOps codebase remedies for physical power events is futile and causes 5-minute timeout kills on `agy`.

### Decision
Add an early filter check before Triage: if the alert originates from `k3s-worker-*` and telemetry reveals `NodeNotReady`, `flannel`, or `FailedCreatePodSandBox`, mark the incident as `IGNORED_HARDWARE_REBOOT` and drop it without GitHub ticket generation.

### Consequences
- Prevents ticket storms and runaway `agy` minion timeouts caused by daily PC power cycles.
- Reserves AI incident remediation strictly for actionable cloud and cluster software defects.
