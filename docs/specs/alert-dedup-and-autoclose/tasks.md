# Implementation Tasks: Alert Deduplication & Auto-Closing

**Feature ID**: `alert-dedup-and-autoclose`  
**Status**: Completed  

---

## Task Checklist

- [x] **Task 1: GitHub Client Extension (`internal/github/client.go`)**
  - Implement `CloseIssue(issueNumber int, comment string) error`
  - Implement `FindOpenIssueByTitle(titlePrefix string) (*github.Issue, error)`
  - Traceability: `REQ-003`, `REQ-004`

- [x] **Task 2: Persistent Incident Schema Migration (`internal/db/db.go`)**
  - Add migration creating `cluster_incidents` table with indices on `fingerprint` and `state`
  - Traceability: `REQ-005`

- [x] **Task 3: Incident Registry & Deduplication Module (`internal/incident/tracker.go`)**
  - Implement `ComputeFingerprint(alertName, namespace, targetResource string) string`
  - Implement `TrackClusterIncident(fingerprint, alertName, namespace, target string) (isDuplicate bool, existingIssue int, err error)`
  - Implement `RecordClusterIssue(fingerprint, alertName, namespace, target string, issueNumber int, state string) error`
  - Implement `ResolveClusterIncident(fingerprint string) (closedIssue int, err error)`
  - Traceability: `REQ-001`, `REQ-003`, `REQ-004`, `REQ-005`

- [x] **Task 4: Webhook Handler Integration (`internal/webhook/handler.go`)**
  - Add Ephemeral Worker Node suppression logic (`REQ-002`)
  - Hook in `ResolveClusterIncident` and `github.CloseIssue` on `status == "resolved"` (`REQ-004`)
  - Hook in `TrackClusterIncident` deduplication check on `status == "firing"` (`REQ-003`)
  - Format GitHub issue titles with structured namespace & target prefix (`REQ-001`)

- [x] **Task 5: Automated Testing & Verification**
  - Unit tests for fingerprint calculation and deduplication logic
  - Webhook simulation test for `firing` (first create, second duplicate drop) and `resolved` (auto-close)
  - Traceability: `REQ-001` through `REQ-005`
