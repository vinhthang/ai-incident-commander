# Feature Specification: Alert Deduplication, Filtering & Auto-Closing

**Feature ID**: `alert-dedup-and-autoclose`  
**Repository**: `vinhthang/ai-incident-commander`  
**Status**: Draft / Awaiting Approval  
**Authors**: Antigravity & Vinh Thang  

---

## 1. Problem Statement

Between September and October 2026, the `ai-incident-commander` opened 228 GitHub issues on `vinhthang/oci`, resulting in issue tracker pollution and alert storms. Analysis revealed:
1. **Blind Re-Filing**: Every 5 minutes after in-memory cooldown expiration, any recurring alert creates a brand new GitHub issue, even if the previous issue remains open.
2. **Missing Resolution Lifecycle**: When Grafana emits `status == "resolved"`, the commander cleans its in-memory map but never calls the GitHub API to close the corresponding issue.
3. **Flat Memory Keying**: Alerts are keyed in memory solely by `alertName` (e.g. `"PodCrashLoopBackOff"`), colliding across pods and namespaces, and wiped on pod restarts.
4. **Lack of Hardware Failure Awareness**: Power-cycling or rebooting ephemeral on-premise Hyper-V worker nodes (`k3s-worker-1..4`) triggers CNI delays (`flannel subnet.env missing`), which the commander erroneously attempts to treat as code defects in `vinhthang/oci`.

---

## 2. Normative Requirements

### REQ-001: Composite Alert Fingerprinting
The commander MUST generate a deterministic composite key for every incoming alert:
$$\text{Fingerprint} = \text{hash}(\text{alertName} + \text{namespace} + \text{targetResource})$$
where `targetResource` prioritizes `pod` > `statefulset` > `deployment` > `service` > `app`.

### REQ-002: Ephemeral Node Hardware Outage Suppression
When an alert specifies a node matching `k3s-worker-*` AND the failure reason indicates physical node reboot / CNI startup delays (`NodeNotReady`, `FailedCreatePodSandBox`, `flannel`), the commander MUST drop the alert immediately without creating a GitHub issue or invoking Triage/Fixer minions.

### REQ-003: Pre-Creation Open Issue Deduplication
Before calling `github.CreateIssue()`, the commander MUST verify whether an issue for the same alert fingerprint is currently in an `open` state (via database registry or GitHub API check).
If an issue is already open:
- A new issue MUST NOT be created.
- The incident occurrence counter in PostgreSQL MUST be incremented.
- The pipeline MUST halt without creating duplicate git branches.

### REQ-004: Automatic Issue Closure on Resolution
When Grafana emits an alert payload with `status == "resolved"`:
- The commander MUST locate the open issue number associated with the alert fingerprint.
- The commander MUST invoke `github.CloseIssue(issueNumber, comment)` with a resolution comment citing the resolved event timestamp.
- The incident state in PostgreSQL MUST be transitioned to `resolved`.

### REQ-005: Persistent PostgreSQL Incident Registry
The database schema MUST define a `cluster_incidents` table persisting incident fingerprint, alert name, namespace, target resource, associated GitHub issue number, state (`firing`, `resolved`, `ignored`), and occurrence count across commander pod restarts.
