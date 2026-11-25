# Autonomous Runtime Remediation and Proactive Daily Audit Dispatch

- Feature ID: 2026-11-23-autonomous-remediation-and-proactive-audit
- Type: enhancement
- Status: In-Testing
- First entered In-Testing: 2026-11-23
- Related specifications: Implemented: Changes [2026-11-22-commander-reliability-and-observability](../2026-11-22-commander-reliability-and-observability/spec.md); Changed by [2026-11-25-daily-report-memos-only](../2026-11-25-daily-report-memos-only/spec.md)

## Problem and source intent

The user requested: `"I want to modify this ai commander to fix other issues as well, if it can see the root cause, then it can fix. just report back the result"`. When asked to scope the additional fix capabilities, merge policy, runtime safety boundary, and reporting channels, the user explicitly selected:

1. **Proactive fixes from `/daily-report` findings**: Auto-dispatch verified root-cause issues discovered during daily fleet audits instead of only listing recommendations.
2. **Merge policy (`AUTO_MERGE`)**: Keep `AUTO_MERGE` environment-controlled (default `false` for human merge; flip `AUTO_MERGE=true` in cluster config when ready for full auto-merge).
3. **Strict Go-enforced runtime Kubernetes allowlist**: Restrict runtime actions in Go code to `rollout restart` (`Deployment` / `StatefulSet`) and deleting failed `Job` objects, excluding core stateful DBs and `kube-system`, with a post-action health verification check.
4. **Dual-sink result reporting**: Report remediation results back to both **GitHub Issues** (auto-closed when a runtime fix is verified, kept open and assigned to `GITHUB_OWNER` on failure or manual-merge PRs) and **Memos** (`#AutoRemediation` note when `MEMOS_DB_DSN` is set).

Previously, `/daily-report` was strictly read-only (`executeDailyReport` never dispatched remediations), and reactive triage had only a 3-way verdict (`IGNORE`, `ESCALATE_HUMAN`, `FIX_VIA_PR`), forcing recoverable runtime issues—such as Grafana's locked SQLite pod in `vinhthang/oci#345`—to escalate without remediation.

## Repository grounding

- **Configuration & Defaults**: `internal/config/config.go` (`GetEnv`, `ParseDurationEnv`, `ParseBoolEnv`, `AutoMerge`, `MemosDBDSN`, `WorkspaceDir`, `GithubOwner`).
- **Kubernetes Telemetry & RBAC State**: `internal/k8s/daily_check.go`, `internal/k8s/telemetry.go`, and live cluster inspection showing `ServiceAccount/ai-incident-commander` currently binds only read-only `ClusterRole/ai-incident-commander-reader` (`get`, `list`, `watch`); core stateful DBs reside in `data` (`postgres-primary`, `mongodb-rs0`, `redis`, `fleet-pulsar-*`) alongside `kube-system` (`coredns`) and `system/ai-incident-commander` itself.
- **Minions & Prompts**: `internal/minion/triage.go` (`TriageAction`, `ParseTriageDecision`, `RunDiagnosticTriage`), `internal/minion/daily_report.go` (`ParseDailyHealthStatus`, `RunDailyReport`), `internal/prompt/defaults/triage.tmpl`, `internal/prompt/defaults/daily_report.tmpl`.
- **Webhook Orchestration, Reporting & Metrics**: `internal/webhook/handler.go` (`processAlert`, `checkExistingOpenGitHubWork`, `remediateClusterIssue`), `internal/webhook/daily_report.go` (`executeDailyReport`, `dailyReportRunning`), `internal/db/memos.go` (`InsertMemosReport`), `internal/metrics/metrics.go` (`HandleMetrics`, `IncRemediationOutcome`), `internal/incident/tracker.go` (`RecordClusterIssue`, `ResolveClusterIncident`).

## Non-goals

- Allowing `agy` subprocesses to execute arbitrary mutating `kubectl` or `helm` commands directly (agents remain read-only for live cluster commands during triage and daily audit; only Go executes allowlisted runtime mutations).
- Allowing runtime restarts or deletions against `kube-system`, `kube-public`, `kube-node-lease`, `data`, core stateful databases, or `system/ai-incident-commander` itself.
- Mutating live cluster RBAC manifests or applying changes to `vinhthang/oci` directly from this repository (operator RBAC YAML changes belong in `vinhthang/oci` and are documented in `README.md`).
- Flipping `AUTO_MERGE` default away from `false`, cloning external application source repositories outside `vinhthang/oci`, or modifying frozen `REQ-011..REQ-023` / `AC-*` text in parent specs.

## Requirements

### REQ-024: Go-Enforced Allowlisted Runtime Kubernetes Remediation Engine

- Source intent: User selections A3 (`"Strict Go-enforced allowlist (only rollout restart Deployment/StatefulSet and delete failed Job, excluding core stateful DBs/kube-system, with post-action health check)"`) and A2/A4 (`"just report back the result"`).
- Statement: WHEN Go executes a runtime Kubernetes remediation request, THE commander SHALL validate `ENABLE_RUNTIME_REMEDIATION`, enforce DNS-1123 identifiers, protected-namespace and workload exclusions, limit actions strictly to `ROLLOUT_RESTART` (`deployment`/`statefulset`) and `DELETE_FAILED_JOB` (`job`), and verify post-action cluster health before reporting success.
- AC-024-1 — Success: Valid `ROLLOUT_RESTART` (e.g., `observability/deployment/grafana`) executes `kubectl -n <ns> rollout restart <kind>/<name>` followed by `kubectl -n <ns> rollout status <kind>/<name> --timeout=<RuntimeVerifyTimeout>` and returns `Executed=true, Verified=true`; valid `DELETE_FAILED_JOB` verifies `status.failed > 0 && status.active == 0 && status.succeeded == 0` via `kubectl get job -o json`, runs `kubectl -n <ns> delete job <name>`, and confirms the Job is no longer present.
- AC-024-2 — Boundary/failure: IF `ENABLE_RUNTIME_REMEDIATION=false`, OR the namespace is in `RUNTIME_PROTECTED_NAMESPACES` (`kube-system,kube-public,kube-node-lease,data`), OR the target is `system/ai-incident-commander` or matches a core stateful DB name (`postgres`, `mongodb`, `redis`, `pulsar`), OR names fail DNS-1123 validation, OR a Job has `active > 0` / `succeeded > 0` / `failed == 0`, THEN Go SHALL reject the action cleanly with `Executed=false` and a non-empty `DeniedReason` without invoking mutating `kubectl`; IF `kubectl` fails with RBAC `Forbidden`, THEN it SHALL return `RBACDenied=true` and an explicit error.

### REQ-025: 4-Way Diagnostic Triage Verdict and Reactive Runtime Dispatch

- Changes: [2026-11-22-commander-reliability-and-observability/REQ-020](../2026-11-22-commander-reliability-and-observability/spec.md#req-020-unescaped-text-prompt-rendering-and-context-aware-agyqueue)
- Source intent: User request (`"if it can see the root cause, then it can fix. just report back the result"`) paired with allowlisted runtime remediation (A3) and dual-sink reporting (A4).
- Previous contract: `RunDiagnosticTriage` / `ParseTriageDecision` only supported 3 verdicts (`IGNORE`, `ESCALATE_HUMAN`, `FIX_VIA_PR`), causing verified runtime-only incidents (such as locked SQLite pods in `vinhthang/oci#345`) to escalate without remediation.
- Statement: WHEN `RunDiagnosticTriage` identifies a root cause solvable without GitOps drift via an allowlisted runtime Kubernetes action, THE commander SHALL support `VERDICT: FIX_RUNTIME` paired with `Confidence: HIGH` and a `RUNTIME_ACTION:` descriptor, execute the allowlisted action via Go, and report the outcome to GitHub and Memos.
- AC-025-1 — Success: Triage output containing `Confidence: HIGH`, a valid `RUNTIME_ACTION: ROLLOUT_RESTART <ns>/<kind>/<name>` or `RUNTIME_ACTION: DELETE_FAILED_JOB <ns>/job/<name>`, and trailing `VERDICT: FIX_RUNTIME` parses to `ActionFixRuntime` with populated `RuntimeAction`; `processAlert` opens a tracking GitHub issue, runs `k8s.ExecuteRuntimeAction`, resolves `cluster_incidents` in Postgres and auto-closes the GitHub issue with telemetry summary when `Verified=true`, and posts a `#AutoRemediation` note to Memos.
- AC-025-2 — Boundary/failure: IF `VERDICT: FIX_RUNTIME` lacks `Confidence: HIGH`, omits or fails to parse `RUNTIME_ACTION:`, OR names a target disallowed by `ValidateRuntimeAction`, THEN `ParseTriageDecision` SHALL safely downgrade to `ActionEscalateHuman`; IF `ExecuteRuntimeAction` fails verification or hits `RBACDenied=true`, THEN `processAlert` SHALL leave `cluster_incidents` `firing`, comment the failure details, assign `GITHUB_OWNER`, and NOT auto-close the GitHub issue.

### REQ-026: Proactive Remediation Dispatch from Daily Fleet Audits

- Current contract: Changed by [2026-11-25-daily-report-memos-only/REQ-029](../2026-11-25-daily-report-memos-only/spec.md#req-029-memos-only-daily-fleet-reports-without-github-report-issues)
- Changes: [2026-11-22-commander-reliability-and-observability/REQ-022](../2026-11-22-commander-reliability-and-observability/spec.md#req-022-signal-clean-daily-fleet-snapshot-filtering)
- Source intent: User selection A1 (`"Proactive fixes from /daily-report findings (auto-dispatch verified root-cause issues from daily audits instead of only listing recommendations)"`).
- Previous contract: `executeDailyReport` ran a single read-only audit pass, published the Markdown report to Memos/GitHub, and exited without dispatching any fixes for discovered root causes.
- Statement: WHEN `POST /daily-report` completes a fleet audit with `ENABLE_DAILY_PROACTIVE_FIX=true`, THE commander SHALL parse structured `PROACTIVE_ACTION:` directives emitted for verified root causes, deduplicate and cap candidates by `DAILY_MAX_PROACTIVE_FIXES`, dispatch each through the GitOps PR or Go runtime pipeline, and report results on the daily issue and Memos.
- AC-026-1 — Success: `ParseDailyProactiveActions` extracts valid `PROACTIVE_ACTION: FIX_VIA_PR | <ns>/<target> | <diagnosis>` and `PROACTIVE_ACTION: FIX_RUNTIME | <ROLLOUT_RESTART|DELETE_FAILED_JOB> <ns>/<kind>/<name> | <diagnosis>` lines while stripping them (or rendering a clean report) before publishing; for up to `DAILY_MAX_PROACTIVE_FIXES` (default `3`) non-duplicate candidates, `executeDailyReport` dispatches `FIX_VIA_PR` via `remediateClusterIssue` (opening an issue/branch/PR honoring `AUTO_MERGE`) and `FIX_RUNTIME` via `k8s.ExecuteRuntimeAction`, then appends a consolidated results comment on the daily report GitHub issue.
- AC-026-2 — Boundary/failure: IF `ENABLE_DAILY_PROACTIVE_FIX=false`, OR no valid `PROACTIVE_ACTION:` line is present, OR a target matches an open GitHub Issue/PR via `checkExistingOpenGitHubWork`, THEN that candidate SHALL be skipped without duplicate work; IF one proactive candidate fails (including RBAC denial or PR exhaustion), THEN the remaining candidates up to `DAILY_MAX_PROACTIVE_FIXES` SHALL still execute and `dailyReportRunning` SHALL still release cleanly.

### REQ-027: Dual-Sink Auto-Remediation Result Reporting and Metrics

- Changes: [2026-11-22-commander-reliability-and-observability/REQ-018](../2026-11-22-commander-reliability-and-observability/spec.md#req-018-hardened-webhook-ingress-state-pruning-and-prometheus-metrics) and [2026-11-22-commander-reliability-and-observability/REQ-021](../2026-11-22-commander-reliability-and-observability/spec.md#req-021-force-clean-git-workspace-push-verification-and-context-safe-github-client)
- Source intent: User selections A2 (`"Keep AUTO_MERGE env-controlled"`) and A4 (`"Both GitHub Issue (auto-closed on verified fix, kept open on failure) and Memos note (#AutoRemediation)"`).
- Previous contract: Remediation outcomes were only commented on GitHub issues/PRs (and `commander_remediation_outcomes_total` only tracked GitOps PR paths), with no Memos `#AutoRemediation` summary and no runtime-fix or proactive-fix counters.
- Statement: WHEN any reactive or proactive remediation (runtime or GitOps PR) concludes, THE commander SHALL record its outcome to Memos (`#AutoRemediation`), apply the explicit GitHub Issue/PR closure or assignment rule, and increment Prometheus remediation and proactive-fix metrics.
- AC-027-1 — Success: When `MEMOS_DB_DSN` is non-empty, `reportRemediationOutcome` inserts `db.FormatRemediationMemosNote` tagged `#AutoRemediation` summarizing source (`reactive` / `daily-report`), mode (`runtime` / `pr`), target, root cause, status, and GitHub issue/PR URL or number; verified runtime fixes auto-close their tracking GitHub issue, `AUTO_MERGE=true` merges passing PRs (closing linked issue via PR), `AUTO_MERGE=false` leaves passing PRs assigned to `GITHUB_OWNER`, and `/metrics` exposes `commander_proactive_fixes_total` alongside updated `commander_remediation_outcomes_total` outcomes (`runtime_verified`, `runtime_failed`, `runtime_rbac_denied`, `merged`, `ready_for_human_merge`, `exhausted`).
- AC-027-2 — Boundary/failure: IF `MEMOS_DB_DSN` is empty or `InsertMemosReport` returns an error, THEN the error SHALL be logged cleanly without blocking GitHub issue closure/assignment or panicking; `AUTO_MERGE=false` SHALL strictly prevent merging PRs automatically.

### REQ-028: Updated Documentation, Operator RBAC Guide, and Agent Invariants

- Changes: [2026-11-22-commander-reliability-and-observability/REQ-023](../2026-11-22-commander-reliability-and-observability/spec.md#req-023-repository-artifact-cleanup-and-updated-documentation)
- Source intent: Explicit user visibility requirement (`"just report back the result"`) and operational dependency discovered during inspection (`ClusterRole/ai-incident-commander-reader` is currently read-only).
- Previous contract: `README.md` and `AGENTS.md` describe only 3-way read-only/GitOps triage, strictly read-only `/daily-report`, and prior env vars up to `REQ-023`.
- Statement: WHEN developers, cluster operators, or AI agents read root documentation, THE repository SHALL document 4-way triage (`FIX_RUNTIME`), proactive `/daily-report` dispatch, dual-sink `#AutoRemediation` reporting, all new environment variables and metrics, least-privilege Kubernetes RBAC requirements for `vinhthang/oci`, and updated `AGENTS.md` invariants and spec ID reservations.
- AC-028-1 — Success: `README.md` documents `VERDICT: FIX_RUNTIME`, `PROACTIVE_ACTION` daily dispatch, `#AutoRemediation` Memos + GitHub reporting, all 5 new env vars (`ENABLE_RUNTIME_REMEDIATION`, `RUNTIME_PROTECTED_NAMESPACES`, `RUNTIME_VERIFY_TIMEOUT`, `ENABLE_DAILY_PROACTIVE_FIX`, `DAILY_MAX_PROACTIVE_FIXES`), `commander_proactive_fixes_total`, and a copy-ready least-privilege `ClusterRole` YAML snippet for `vinhthang/oci`; `AGENTS.md` documents runtime allowlist invariants, fake runner unit test constraints, and reserves `REQ-029+` / `ADR-0058+`.
- AC-028-2 — Boundary/failure: IF any of the 5 new env vars, safety exclusions (`data`, `kube-system`, core DBs, self-target), or RBAC prerequisite notes is missing or contradicts Go implementation, THEN documentation verification SHALL fail.

## Assumptions and owner-deferred questions

- **Assumption 1 (Enabled Defaults with Safe Degradation)**: `ENABLE_RUNTIME_REMEDIATION` and `ENABLE_DAILY_PROACTIVE_FIX` default to `true`, with `DAILY_MAX_PROACTIVE_FIXES=3` and `RUNTIME_VERIFY_TIMEOUT=90s`. If the live cluster `ClusterRole` has not yet been updated in `vinhthang/oci`, runtime `kubectl` mutations return `Forbidden` (`RBACDenied=true`), log clearly, report the failure to GitHub/Memos, and degrade without crashing.
- **Assumption 2 (Daily Audit Budget)**: `HandleDailyReport` extends its background context timeout from `5m` to `15m` so up to 3 proactive remediations (`AgyQueue` triage/fix/review + `rollout status`) can complete sequentially without premature context cancellation.
- **Assumption 3 (Open Decision Status)**: All material decisions (scope A1, `AUTO_MERGE` policy A2, runtime allowlist A3, dual-sink reporting A4) were explicitly answered by the user; zero `[PROVISIONAL]` requirements remain.

## Approvals and lifecycle history

- **2026-11-23 (`Planning`)**: Linked delta package authored from user request (`"I want to modify this ai commander to fix other issues as well, if it can see the root cause, then it can fix. just report back the result"`) and explicit multiple-choice answers (A1 proactive daily fixes, A2 env-controlled `AUTO_MERGE`, A3 Go-enforced runtime allowlist, A4 GitHub + Memos `#AutoRemediation`). Implementation awaiting human review/approval of `implementation_plan.md`.
- **2026-11-23 (`Planning -> In-Progress`)**: User approved `implementation_plan.md` (`"proceed"`). Starting TDD execution of Tasks 1–5.
- **2026-11-23 (`In-Progress -> In-Testing`)**: Completed Tasks 1–5 with `go test -count=1 ./...`, `go vet ./...`, `golangci-lint run ./...`, multi-arch static builds, `arch-guard`, `mutation-guard`, and `code-review` passing ([Run 1](./verification.md#run-1--2026-11-23)); promoted to `In-Testing` awaiting live cluster RBAC application and real-world verification.
- **2026-11-25 (`In-Testing` parent activation)**: Linked delta [2026-11-25-daily-report-memos-only](../2026-11-25-daily-report-memos-only/spec.md) (`REQ-029`) entered `In-Testing`; added 1-hop `- Current contract:` link on `REQ-026`.
