# Commander Reliability, Signal Hygiene & Observability Delta

- Feature ID: 2026-11-22-commander-reliability-and-observability
- Type: enhancement
- Status: In-Testing
- First entered In-Testing: 2026-11-22
- Related specifications: Implemented: Changes [2026-11-22-ai-incident-commander-baseline](../2026-11-22-ai-incident-commander-baseline/spec.md); Changed by [2026-11-23-autonomous-remediation-and-proactive-audit](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md)

## Problem and source intent

The user reviewed the 10 Tier 1–3 improvement proposals grounded in codebase inspection and live `arm10` cluster behavior (including stale firing Issue `#339` and daily fleet report `#344`) and explicitly authorized `"proceed all of them"`. This delta closes reliability, noise, security, and observability gaps across webhook ingress, daily snapshots, prompt escaping, git workspace cleanup, GitHub client safety, `AgyQueue` cancellation, `apps` alert enrichment/notification, Prometheus metrics, and repository hygiene.

## Repository grounding

- **Webhook, State & Metrics**: `cmd/commander/main.go`, `internal/webhook/handler.go`, `internal/webhook/daily_report.go`, `internal/state/manager.go`, `internal/metrics/metrics.go` (new).
- **Prompts, Queue & Workspace**: `internal/prompt/prompt.go`, `internal/minion/queue.go`, `internal/workspace/git.go`.
- **Daily Audit, VictoriaLogs & Memos**: `internal/config/config.go`, `internal/k8s/daily_check.go`, `internal/k8s/victorialogs.go` (new), `internal/db/memos.go`.
- **GitHub & Incident Tracking**: `internal/github/client.go`, `internal/incident/tracker.go`.
- **Repository Hygiene & Docs**: `.gitignore`, root `commander` binary and `patch-*.py` scripts, `README.md`, `AGENTS.md`.

## Non-goals

- Executing mutating `kubectl` commands against live cluster workloads or deleting live Kubernetes `Job` objects on `arm10`.
- Rebuilding or pushing container images to `ghcr.io` or modifying `vinhthang/oci` manifests directly from this repository.
- Modifying frozen `REQ-011..REQ-017` or `AC-*` text in `2026-11-22-ai-incident-commander-baseline/spec.md` (beyond adding 1-hop `- Current contract:` links upon entering `In-Testing`).

## Requirements

### REQ-018: Hardened Webhook Ingress, State Pruning, and Prometheus Metrics

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-027](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-027-dual-sink-auto-remediation-result-reporting-and-metrics)
- Changes: [2026-11-22-ai-incident-commander-baseline/REQ-011](../2026-11-22-ai-incident-commander-baseline/spec.md#req-011-authenticated-http-control-plane-and-health-telemetry)
- Source intent: User authorization (`"proceed all of them"`) on Tier 2 Item 7 (`/webhook` hardening + map pruning) and Tier 3 Item 10 (`/metrics` Prometheus endpoint).
- Previous contract: `/webhook` accepted any HTTP method and unbounded JSON bodies, `ActiveIncidents` (`COOLING_DOWN`) and `resolvedDebounce` maps grew without eviction, and no `/metrics` route existed.
- Statement: WHEN HTTP traffic hits `/webhook` or `/metrics` or state locks are acquired, THE commander SHALL restrict `/webhook` to `POST` with a `2 MiB` body cap, prune expired `COOLING_DOWN` and `resolvedDebounce` entries during lock acquisition, and expose Prometheus text metrics on `GET /metrics`.
- AC-018-1 — Success: `GET /metrics` returns `200 OK` (`text/plain; version=0.0.4`) exporting `commander_alerts_total`, `commander_triage_verdicts_total`, `commander_remediation_outcomes_total`, `commander_daily_reports_total`, `commander_agy_jobs_total`, `commander_agy_duration_seconds`, and `commander_queue_depth`; `AcquireIncidentLock` / `AcquireResolveLock` evict expired `COOLING_DOWN` (`>= CooldownMins`) and `resolvedDebounce` (`>= resolveDebounceWindow`) keys while preserving active locks.
- AC-018-2 — Boundary/failure: IF `/webhook` receives a non-`POST` method or `/metrics` receives a non-`GET` method, THEN it SHALL return `405 Method Not Allowed`; IF `/webhook` receives a request body larger than `2 MiB`, THEN it SHALL return `413 Request Entity Too Large` without dispatching goroutines.

### REQ-019: Closed GitHub Issue Reconciliation and Enriched Application Incident Sink

- Changes: [2026-11-22-ai-incident-commander-baseline/REQ-012](../2026-11-22-ai-incident-commander-baseline/spec.md#req-012-composite-alert-identity-multi-layer-deduplication-and-resolution-auto-close)
- Source intent: User authorization (`"proceed all of them"`) on Tier 1 Item 2 (Postgres vs closed GitHub issue drift observed on `#339`) and Tier 3 Item 9 (VictoriaLogs enrichment & Memos/GitHub sink for `apps` incidents).
- Previous contract: `TrackClusterIncident` treated `cluster_incidents` rows with `state = 'firing'` as open duplicates forever even if a human closed the GitHub issue, and `apps` application alerts halted silently after Postgres persistence without VictoriaLogs query or Memos/GitHub notification.
- Statement: WHEN a cluster incident matches a `'firing'` DB row OR a new `apps` application error fires, THE commander SHALL reconcile `'firing'` cluster rows against live GitHub issue state (`IsIssueOpen`), enrich `apps` telemetry from `VICTORIALOGS_URL`, and publish new `open` app incidents to Memos and deduplicated GitHub issues.
- AC-019-1 — Success: `ReconcileClosedClusterIssue` transitions `cluster_incidents` to `resolved` when `IsIssueOpen` reports `false` so recurring regressions are not masked; `apps` alerts query `VICTORIALOGS_URL` (`/select/logsql/query`) by `trace_id`/service before `RunTriage`, and new `open` app incidents publish `#AppIncident` notes to Memos (`MEMOS_DB_DSN`) and create a deduplicated `app-incident` GitHub issue.
- AC-019-2 — Boundary/failure: IF GitHub issue state lookup errors or returns an unexpected status, THEN `ReconcileClosedClusterIssue` SHALL conservatively leave the duplicate suppressed; IF `VICTORIALOGS_URL` is empty, unreachable, or returns non-200, THEN `FetchVictoriaLogsContext` SHALL return an empty string and let `apps` triage proceed safely; `"ignored"` app incidents SHALL NOT publish to Memos or GitHub.

### REQ-020: Unescaped Text Prompt Rendering and Context-Aware AgyQueue

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-025](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-025-4-way-diagnostic-triage-verdict-and-reactive-runtime-dispatch)
- Changes: [2026-11-22-ai-incident-commander-baseline/REQ-014](../2026-11-22-ai-incident-commander-baseline/spec.md#req-014-serialized-agy-worker-queue-and-3-way-diagnostic-triage-verdict-gate)
- Source intent: User authorization (`"proceed all of them"`) on Tier 1 Item 3 (`html/template` escaping bug) and Tier 2 Item 6 (context-aware `AgyQueue`).
- Previous contract: `internal/prompt/prompt.go` imported `html/template` (escaping `<`, `>`, `&`, `"`, `'` in telemetry/diffs), `EnqueueAgyJob` blocked unconditionally on channel send/receive, and `executeAgy` ran auth sync and spawned `agy` even when `job.Ctx` was already expired.
- Statement: WHEN rendering prompts or queuing `agy` jobs, THE commander SHALL render prompts with `text/template` without HTML escaping and respect `ctx.Done()` during `EnqueueAgyJob` admission/waiting and before `executeAgy` launches subprocesses.
- AC-020-1 — Success: All five prompt renderers preserve raw `<`, `>`, `&`, `"`, and `'` characters verbatim, `EnqueueAgyJob` observes execution duration and job outcome metrics, and active jobs execute sequentially via `AgyQueue`.
- AC-020-2 — Boundary/failure: IF `ctx` cancels or expires before `EnqueueAgyJob` can enqueue or receive a result, THEN `EnqueueAgyJob` SHALL return promptly with `ctx.Err()`; IF `job.Ctx` is already cancelled/expired when `executeAgy` dequeues it, THEN `executeAgy` SHALL record `skipped_expired`, return `job.Ctx.Err()`, and NOT sync auth files or spawn `/usr/local/bin/agy`.

### REQ-021: Force-Clean Git Workspace, Push Verification, and Context-Safe GitHub Client

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-027](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-027-dual-sink-auto-remediation-result-reporting-and-metrics)
- Changes: [2026-11-22-ai-incident-commander-baseline/REQ-015](../2026-11-22-ai-incident-commander-baseline/spec.md#req-015-bounded-gitops-remediation-loop-multi-stage-review-and-cimerge-gate)
- Source intent: User authorization (`"proceed all of them"`) on Tier 1 Item 4 (git dirty-tree reset & push verification) and Tier 1 Item 5 (`GetPullRequestDiff` status/timeout + GitHub `ctx` propagation).
- Previous contract: `ResetToMain()` failed if dirty/untracked files remained from prior `Fixer` runs, `remediateClusterIssue` invoked `SeniorFixer` and `Reviewer` without checking non-empty diffs or branch push sync, `GetPullRequestDiff` ignored HTTP error codes and used `http.DefaultClient`, and GitHub helpers ignored caller `context.Context`.
- Statement: WHEN resetting the remediation workspace, verifying `Fixer` output, or calling GitHub APIs, THE commander SHALL force-clean the local checkout back to `origin/main`, require non-empty diffs and matching local/remote branch heads via `VerifyBranchPushed` before PR creation/review, and enforce bounded `context.Context` + HTTP `200` checks across `internal/github`.
- AC-021-1 — Success: `ResetToMain()` discards tracked/untracked leftovers cleanly (`git fetch origin main`, `git checkout -f main`, `git reset --hard origin/main`, `git clean -fd`), `VerifyBranchPushed` succeeds only when the working tree is clean and `HEAD` matches `refs/remotes/origin/<branch>`, and `internal/github` propagates caller `ctx` with a 30s `HTTPClient`.
- AC-021-2 — Boundary/failure: IF `VerifyBranchPushed` detects dirty uncommitted files, an unpushed branch, or SHA mismatch with `origin`, OR `localDiff` is empty, THEN `remediateClusterIssue` SHALL feed the reason back to `Fixer` instead of calling `SeniorFixer` or `Reviewer`; IF `GetPullRequestDiff` receives non-`200` HTTP status or cancelled `ctx`, THEN it SHALL return an explicit error rather than error JSON body text.

### REQ-022: Signal-Clean Daily Fleet Snapshot Filtering

- Current contract: Changed by [2026-11-25-daily-report-memos-only/REQ-029](../2026-11-25-daily-report-memos-only/spec.md#req-029-memos-only-daily-fleet-reports-without-github-report-issues)
- Changes: [2026-11-22-ai-incident-commander-baseline/REQ-016](../2026-11-22-ai-incident-commander-baseline/spec.md#req-016-single-flight-daily-fleet-health-audit-and-multi-sink-publishing)
- Source intent: User authorization (`"proceed all of them"`) on Tier 1 Item 1 derived from live `arm10` `ai-commander-daily-report` behavior in Issue `#344`.
- Previous contract: `CollectDailyFleetSnapshot` flagged its own transient `ai-commander-daily-report` trigger pod, flagged historical failed `CronJob` child `Job`s even after newer runs succeeded, and treated all-time `restartCount > 0` identically to active crash loops.
- Statement: WHEN `CollectDailyFleetSnapshot` inspects cluster pods and jobs, THE commander SHALL exclude `ai-commander-daily-report` trigger pods, flag `CronJob`-owned `Job` failures only when the latest scheduled run failed, and restrict pod restart warnings to containers restarted within the last `24h`.
- AC-022-1 — Success: Self-trigger pods (`app=ai-commander-daily-report` or `job-name` prefix `ai-commander-daily-report-`) are skipped, superseded failed `CronJob` jobs (such as `29918895` superseded by `29921775`) are omitted while standalone failed jobs and latest-run `CronJob` failures remain reported, and stable `Running`/`Ready` pods whose last termination occurred `> 24h` ago count as healthy.
- AC-022-2 — Boundary/failure: IF a `Running`/`Ready` pod restarted within the last `24h` (`lastState.terminated.finishedAt` or `state.running.startedAt`), OR a container is currently `NotReady` / `Waiting` / non-`Running`, THEN it SHALL still be flagged in `Unhealthy or Restarting Pods`.

### REQ-023: Repository Artifact Cleanup and Updated Documentation

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-028](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-028-updated-documentation-operator-rbac-guide-and-agent-invariants)
- Changes: [2026-11-22-ai-incident-commander-baseline/REQ-017](../2026-11-22-ai-incident-commander-baseline/spec.md#req-017-repository-documentation-and-agent-operational-guidance)
- Source intent: User authorization (`"proceed all of them"`) on Tier 2 Item 8 (repo hygiene) and synchronizing `README.md` / `AGENTS.md` with REQ-018..REQ-022.
- Previous contract: Git tracked the `22.3M` root `commander` binary and 8 legacy `patch-*.py` helper scripts, `.gitignore` omitted `/commander`, and `README.md` / `AGENTS.md` pre-dated `/metrics`, `VICTORIALOGS_URL`, `apps` dual-sink notifications, and the resolved gotchas.
- Statement: WHEN inspecting the repository root and documentation, THE repository SHALL untrack `commander` and `patch-*.py`, ignore `/commander` in `.gitignore`, and keep `README.md` and `AGENTS.md` synchronized with all endpoints, env vars, behaviors, and remaining tech debt.
- AC-023-1 — Success: `.gitignore` lists `/commander`, `git ls-files` tracks neither `commander` nor `patch-*.py`, `README.md` documents `GET /metrics`, `VICTORIALOGS_URL`, `apps` Memos/GitHub sinks, closed-issue reconciliation, and `/daily-report` 24h filtering, and `AGENTS.md` updates §5 gotchas and reserves `REQ-024+` / `ADR-0055+`.
- AC-023-2 — Boundary/failure: IF any tracked `patch-*.py` or root `commander` file remains in git index OR `README.md`/`AGENTS.md` omits `VICTORIALOGS_URL` / `/metrics` or falsely lists fixed gotchas as unresolved, THEN documentation verification SHALL fail.

## Assumptions and owner-deferred questions

- **Assumption 1 (Stdlib Metrics & LogsQL)**: `internal/metrics` uses Go standard library primitives (`sync/atomic`, `net/http`) and `internal/k8s/victorialogs.go` queries VictoriaLogs `/select/logsql/query` NDJSON via `net/http` with `go.mod` unchanged.
- **Assumption 2 (24h Restart Window Clock)**: `collectPodsSnapshot` measures the 24h restart window relative to `time.Now()` (injectable in unit tests via helper parameter so tests are deterministic and non-flaky).

## Approvals and lifecycle history

- **2026-11-22 (`Planning`)**: Linked delta package created per user authorization (`"proceed all of them"`).
- **2026-11-22 (`Planning -> In-Progress`)**: Gate 1 and Gate 2 passed; user authorized all 10 improvements (`"proceed all of them"`). Starting TDD execution of Tasks 1–5.
- **2026-11-22 (`In-Progress -> In-Testing`)**: Completed Tasks 1–5 with `go test -v -count=1 ./...`, `go vet ./...`, static build, `arch-guard`, `mutation-guard`, and `code-review` passing ([Run 1](./verification.md#run-1--2026-11-22)); promoted to `In-Testing` awaiting real-world cluster confirmation.
- **2026-11-23 (`In-Testing` parent activation)**: Linked delta [2026-11-23-autonomous-remediation-and-proactive-audit](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md) (`REQ-024..REQ-028`) entered `In-Testing`; added 1-hop `- Current contract:` links on `REQ-018`, `REQ-020`, `REQ-021`, `REQ-022`, and `REQ-023`.
- **2026-11-25 (`In-Testing` chain activation)**: Linked delta [2026-11-25-daily-report-memos-only](../2026-11-25-daily-report-memos-only/spec.md) (`REQ-029`) entered `In-Testing`; updated 1-hop `- Current contract:` link on `REQ-022`.
