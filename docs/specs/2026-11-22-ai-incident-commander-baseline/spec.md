# AI Incident Commander Baseline System Specification

- Feature ID: 2026-11-22-ai-incident-commander-baseline
- Type: new feature
- Status: In-Testing
- First entered In-Testing: 2026-11-22
- Related specifications: Implemented: Changed by [2026-11-22-commander-reliability-and-observability](../2026-11-22-commander-reliability-and-observability/spec.md)

## Problem and source intent

The user requested: `"review this project, what is it doing, and then write spec for it and also readme, agent md file"`.

While partial feature specs exist under `docs/specs/` (historical `alert-dedup-and-autoclose` and an unrelated cross-repo `pc1-qa-router-multicluster-refactor` draft), `vinhthang/ai-incident-commander` has no complete system-wide baseline specification, no `AGENTS.md`, and its root `README.md` still describes an obsolete v0 prototype (direct Gemini 3.5 Flash REST calls, direct commits to `main`, and a `Rollback Minion` that no longer exist in code). This package establishes the code-grounded baseline specification for the Go 1.25 service at commit `c65e4ce` and governs updated `README.md` and `AGENTS.md` documentation.

## Repository grounding

- **Entrypoint & HTTP/OTel Runtime**: `cmd/commander/main.go` (`initTracer`, `main`), `internal/config/config.go` (`GetEnv`, `ParseDurationEnv`, `ParseBoolEnv`, environment defaults).
- **HTTP Handlers & Pipelines**: `internal/webhook/handler.go` (`HandleWebhook`, `HandleHealthz`, `processAlert`, `remediateClusterIssue`), `internal/webhook/daily_report.go` (`HandleDailyReport`, `executeDailyReport`).
- **Alert Identity, State & Tracking**: `internal/incident/tracker.go` (`ComputeFingerprint`, `ExtractNamespace`, `ExtractTargetResource`, `IsMetricsScraperLabel`, `NormalizeWorkloadBase`, `IsEphemeralNodeOutage`, `TrackClusterIncident`, `RecordClusterIssue`, `ResolveClusterIncident`, `SummarizeRecentIncidents`), `internal/state/manager.go` (`AcquireIncidentLock`, `AcquireResolveLock`, `WaitForSoak`, `ReleaseIncidentLock`).
- **Kubernetes, Edge & Fleet Telemetry**: `internal/k8s/telemetry.go` (`CheckWorkloadStatus`, `FetchDiagnosticBundle`, `FetchTelemetry`), `internal/k8s/daily_check.go` (`CollectDailyFleetSnapshot`).
- **Minion Queue & Agents**: `internal/minion/queue.go` (`StartAgyWorker`, `EnqueueAgyJob`, `syncAgyRuntimeDir`), `internal/minion/triage.go` (`RunDiagnosticTriage`, `ParseTriageDecision`), `internal/minion/fixer.go` (`RunFixer`), `internal/minion/senior_fixer.go` (`RunSeniorFixer`), `internal/minion/reviewer.go` (`RunReviewer`, `ParseReviewDecision`), `internal/minion/app_fixer.go` (`TrackAppIncident`, `SaveNewAppIncident`), `internal/minion/daily_report.go` (`RunDailyReport`, `ParseDailyHealthStatus`).
- **GitHub, Workspace, DB & Prompts**: `internal/github/client.go` (`CreateIssue`, `CreatePullRequest`, `GetPullRequestDiff`, `WaitForPRChecks`, `EvaluateCheckRuns`, `MergePullRequest`, `FindOpenIssueForTarget`, `FindOpenPRForTarget`, `CloseIssue`), `internal/workspace/git.go` (`InitWorkspace`, `Lock`, `ResetToMain`, `CreateAndCheckoutBranch`, `GetLocalDiff`), `internal/db/db.go` (`InitDB`, `runMigrations`), `internal/db/memos.go` (`FormatDailyMemosNote`, `InsertMemosReport`), `internal/prompt/prompt.go` and `internal/prompt/defaults/*.tmpl`.
- **Container & CI**: `Dockerfile`, `.github/workflows/ci-cd.yml`, `go.mod` (`go 1.25.0`).

## Non-goals

- Modifying `.go` production code or fixing pre-existing implementation defects in this baseline specification package (any runtime changes belong in separate TDD delta packages).
- Mutating live Kubernetes resources via imperative `kubectl` commands (`kubectl apply`/`patch`/`delete`).
- Generating automated GitHub pull requests for `apps` namespace application-level errors (those halt after Postgres deduplication, triage, and persistence).
- Rewriting or migrating pre-existing `docs/specs/alert-dedup-and-autoclose/` or untracked `docs/specs/pc1-qa-router-multicluster-refactor/`.

## Requirements

### REQ-011: Authenticated HTTP Control Plane and Health Telemetry

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-027](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-027-dual-sink-auto-remediation-result-reporting-and-metrics)
- Source intent: Grounded in `cmd/commander/main.go`, `internal/webhook/handler.go`, and `internal/webhook/daily_report.go`, and requested as part of full-project specification coverage.
- Statement: WHEN an HTTP client sends requests to `/webhook`, `/daily-report`, or `/healthz` on `PORT` (default `8085`), THE service SHALL enforce configured `WEBHOOK_AUTH_TOKEN` Bearer authentication on action endpoints, validate HTTP methods on `/daily-report` and `/healthz`, report Postgres connectivity and `AgyQueue` depth on `/healthz`, export OpenTelemetry spans, and shut down gracefully on `SIGINT`/`SIGTERM`.
- AC-011-1 — Success: `POST /webhook` returns `200 OK` and dispatches asynchronous per-alert processing; `POST /daily-report` returns `202 Accepted`; `GET /healthz` returns `200 OK` with JSON `{"status":"ok","db":"connected"|"disabled","queue_depth":<N>}`.
- AC-011-2 — Boundary/failure: IF `WEBHOOK_AUTH_TOKEN` is non-empty and the `Authorization: Bearer <token>` header is missing or mismatched (constant-time comparison), THEN `/webhook` and `/daily-report` SHALL return `401 Unauthorized`; IF `/healthz` receives non-`GET` or `/daily-report` receives non-`POST`, THEN the handler SHALL return `405 Method Not Allowed`; IF `db.DB` is configured and `PingContext` fails, THEN `/healthz` SHALL return `503 Service Unavailable` with `status: "degraded"`.

### REQ-012: Composite Alert Identity, Multi-Layer Deduplication, and Resolution Auto-Close

- Current contract: Changed by [2026-11-22-commander-reliability-and-observability/REQ-019](../2026-11-22-commander-reliability-and-observability/spec.md#req-019-closed-github-issue-reconciliation-and-enriched-application-incident-sink)
- Source intent: Grounded in `internal/incident/tracker.go`, `internal/state/manager.go`, `internal/minion/app_fixer.go`, `internal/github/client.go`, `internal/db/db.go`, and `internal/webhook/handler.go`.
- Statement: WHEN a Grafana alert payload arrives at `/webhook`, THE commander SHALL compute a SHA-256 identity from `exported_namespace`/`namespace`, prioritized non-scraper target resource, and `alertname`, deduplicate across in-memory locks/cooldowns, PostgreSQL state (`cluster_incidents` or `application_incidents`), and open GitHub Issues/PRs normalized by `NormalizeWorkloadBase`, and auto-close resolved cluster issues.
- AC-012-1 — Success: `apps` namespace application error alerts (`category=application` or `trace_id`/`status_code`/`topic`) deduplicate in `application_incidents` before triage and persist `open` or `ignored` diagnosis without opening PRs; cluster alerts deduplicate against `cluster_incidents` and open `ai-incident` GitHub issues or `fix/alert-*` PRs, while `status == "resolved"` closes tracked GitHub issues and sets `cluster_incidents.state = 'resolved'`.
- AC-012-2 — Boundary/failure: IF `DB_DSN` is unset (`db.DB == nil`), THEN incident tracking functions SHALL degrade safely without panicking and rely on in-memory locking (`5m` cooldown, `1m` resolve debounce) plus GitHub API lookup; IF concurrent firing alerts or duplicate `resolved` webhooks within `1m` target the same fingerprint, THEN duplicates SHALL be dropped silently.

### REQ-013: Stabilization Soak, Pre-Triage Workload Recovery, and Diagnostic Bundle Collection

- Source intent: Grounded in `internal/webhook/handler.go`, `internal/state/manager.go`, `internal/k8s/telemetry.go`, and `internal/incident/tracker.go`.
- Statement: WHILE a non-application cluster or edge alert is firing, THE commander SHALL wait up to `SOAK_DURATION` (default `60s`), verify rollout/endpoint recovery via `k8s.CheckWorkloadStatus`, gather read-only Kubernetes, HTTP/TLS, and Helm diagnostics via `k8s.FetchDiagnosticBundle`, and suppress transient Hyper-V worker node reboots before invoking Triage Minion.
- AC-013-1 — Success: Post-soak alerts collect Layer 1 HTTP/TLS probe results, Services/Endpoints, highest-scoring pod termination `lastState`/logs/events, live workload limits, matched `charts/vinhthang-fleet/` templates, `values.yaml` sections, `helm lint` output, and recent git commits into the diagnostic bundle.
- AC-013-2 — Boundary/failure: IF a matching `resolved` webhook or context cancellation arrives during `SOAK_DURATION`, OR `CheckWorkloadStatus` detects an active rollout or recovered workload/HTTP endpoint, OR `IsEphemeralNodeOutage` matches `k3s-worker-*` with `NodeNotReady`/`flannel`/`FailedCreatePodSandBox`/`NetworkNotReady`, THEN the commander SHALL halt processing cleanly without creating a GitHub issue or invoking `agy`.

### REQ-014: Serialized agy Worker Queue and 3-Way Diagnostic Triage Verdict Gate

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-025](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-025-4-way-diagnostic-triage-verdict-and-reactive-runtime-dispatch)
- Source intent: Grounded in `internal/minion/queue.go`, `internal/minion/triage.go`, and `internal/prompt/prompt.go`.
- Statement: WHEN any minion executes an AI prompt, THE commander SHALL serialize execution through the single-worker `AgyQueue` (buffer `100`, concurrency `1`) with isolated runtime credentials and embedded/ConfigMap prompt templates, and `RunDiagnosticTriage` SHALL gate automated remediation strictly on an explicit `FIX_VIA_PR` verdict with `Confidence: HIGH`.
- AC-014-1 — Success: `StartAgyWorker` runs `/usr/local/bin/agy` one job at a time with `--dangerously-skip-permissions --disable-slash-commands`, syncs `AGY_HOST_AUTH_DIR` auth files with empty `mcp_config.json`, loads `/etc/commander/templates/*.tmpl` when present (falling back to embedded defaults), and routes `IGNORE` to `cluster_incidents.state='ignored'`, `ESCALATE_HUMAN` to a labeled GitHub issue assigned to `GITHUB_OWNER`, and `FIX_VIA_PR` (`HIGH` confidence) to remediation.
- AC-014-2 — Boundary/failure: IF `ParseTriageDecision` receives `FIX_VIA_PR` with `MEDIUM`, `LOW`, or `UNKNOWN` confidence or any unrecognized trailing line, THEN it SHALL safely downgrade the action to `ESCALATE_HUMAN`; IF `RunDiagnosticTriage` fails with a prompt or CLI error, THEN `processAlert` SHALL halt safely without creating a GitHub issue or PR.

### REQ-015: Bounded GitOps Remediation Loop, Multi-Stage Review, and CI/Merge Gate

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-027](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-027-dual-sink-auto-remediation-result-reporting-and-metrics)
- Source intent: Grounded in `internal/webhook/handler.go`, `internal/workspace/git.go`, `internal/minion/{fixer,senior_fixer,reviewer}.go`, and `internal/github/client.go`.
- Statement: WHEN diagnostic triage authorizes `FIX_VIA_PR`, THE commander SHALL acquire `workspace.Lock()`, check out a new `fix/alert-<issue>-<ts>` branch from `main`, and run up to `3` bounded attempts through `Fixer`, `SeniorFixer`, GitHub PR creation, `Reviewer`, GitHub Check Runs polling, and the `AUTO_MERGE` gate.
- AC-015-1 — Success: When `Fixer` produces changes approved by `SeniorFixer` (`git diff main`) and `Reviewer` (PR diff), and `WaitForPRChecks` confirms all CI check runs completed with `success`/`neutral`/`skipped`, the commander assigns `GITHUB_OWNER` when `AUTO_MERGE=false` (default) or merges the PR when `AUTO_MERGE=true`.
- AC-015-2 — Boundary/failure: IF `CreateAndCheckoutBranch` receives `main`, `master`, or empty string, THEN it SHALL return an error; IF `SeniorFixer`, `Reviewer`, or `WaitForPRChecks` rejects/fails on attempts `1` or `2`, THEN feedback SHALL be passed back to `Fixer` on the next attempt; IF `3` attempts are exhausted, `WaitForPRChecks` times out, or merge fails, THEN the commander SHALL post status comments and assign `GITHUB_OWNER` on the issue and PR without auto-merging.

### REQ-016: Single-Flight Daily Fleet Health Audit and Multi-Sink Publishing

- Current contract: Changed by [2026-11-25-daily-report-memos-only/REQ-029](../2026-11-25-daily-report-memos-only/spec.md#req-029-memos-only-daily-fleet-reports-without-github-report-issues)
- Source intent: Grounded in `internal/webhook/daily_report.go`, `internal/k8s/daily_check.go`, `internal/minion/daily_report.go`, `internal/db/memos.go`, and `internal/incident/tracker.go`.
- Statement: WHEN `POST /daily-report` triggers an audit, THE commander SHALL enforce single-flight execution via `dailyReportRunning`, collect a read-only cluster/GitOps/VictoriaMetrics snapshot and 24-hour incident summary, evaluate fleet status via `RunDailyReport`, and publish results to Memos and GitHub Issues.
- AC-016-1 — Success: Concurrent `/daily-report` is accepted once (`202 Accepted`), executes `CollectDailyFleetSnapshot` + `SummarizeRecentIncidents(24h)` + `RunDailyReport`, inserts a private `#DailyFleetCheck` note into Memos when `MEMOS_DB_DSN` is configured, and opens a `daily-report` GitHub issue that auto-closes when the final line is `HEALTH_STATUS: HEALTHY` or assigns `GITHUB_OWNER` with `needs-human-review` when `ATTENTION_NEEDED`.
- AC-016-2 — Boundary/failure: IF a daily report is already running, THEN `POST /daily-report` SHALL return `409 Conflict`; IF `agy` output lacks `HEALTH_STATUS: HEALTHY`, THEN `ParseDailyHealthStatus` SHALL default conservatively to `ATTENTION_NEEDED`; IF `RunDailyReport` fails, THEN the handler SHALL create an `(ERROR)` GitHub issue assigned to `GITHUB_OWNER` and release `dailyReportRunning`.

### REQ-017: Repository Documentation and Agent Operational Guidance

- Current contract: Changed by [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-028](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-028-updated-documentation-operator-rbac-guide-and-agent-invariants)
- Source intent: Explicit user request (`"write spec for it and also readme, agent md file"`).
- Statement: WHEN developers or AI coding agents inspect the repository root, THE repository SHALL provide an up-to-date `README.md` and `AGENTS.md` that match the live Go 1.25 architecture, HTTP endpoints, environment variables, build/lint/test commands, safety invariants, and documented codebase gotchas.
- AC-017-1 — Success: `README.md` accurately documents `POST /webhook`, `POST /daily-report`, `GET /healthz`, all configuration environment variables (including `DB_DSN`, `MEMOS_DB_DSN`, `SOAK_DURATION`, `AUTO_MERGE`, `AGY_MODEL`, `AGY_HOST_AUTH_DIR`, `WEBHOOK_AUTH_TOKEN`, `VICTORIAMETRICS_URL`, `OTEL_EXPORTER_OTLP_ENDPOINT`), ConfigMap prompt overrides, and the multi-stage PR/CI architecture; `AGENTS.md` documents repo scope, package concurrency primitives, `go test`/`golangci-lint`/`validate_specs.py` commands, invariants, and known gotchas.
- AC-017-2 — Boundary/failure: IF `README.md` or `AGENTS.md` retains obsolete claims (`Rollback Minion`, direct commits to `main`, or Gemini 3.5 Flash REST as the active remediation path) or omits required endpoints/env vars, THEN documentation verification SHALL fail until corrected.

## Assumptions and owner-deferred questions

- **Assumption 1 (Baseline Scope)**: This specification documents the exact implemented behavior at commit `c65e4ce` without modifying `.go` files. Pre-existing defects identified during project review (`html/template` escaping in `internal/prompt/prompt.go`, dirty-tree risk in `internal/workspace/git.go`, unchecked HTTP status in `internal/github/client.go`, tracked binaries/patch scripts) are documented in `AGENTS.md` and reported to the user rather than altered out-of-band.
- **Assumption 2 (Catalog Hygiene)**: Existing rows in `docs/specs/INDEX.md` (`alert-dedup-and-autoclose`, `pc1-qa-router-multicluster-refactor`) are preserved untouched while allocating `REQ-011..REQ-017` and `ADR-0050..ADR-0052` strictly above them.

## Approvals and lifecycle history

- **2026-11-22 (`Planning`)**: Baseline specification package authored per user request (`"review this project, what is it doing, and then write spec for it and also readme, agent md file"`).
- **2026-11-22 (`Planning -> In-Testing`)**: All baseline unit tests (`go test -v -count=1 ./...`), `go vet ./...`, and `README.md` / `AGENTS.md` checks passed ([Run 1](./verification.md#run-1--2026-11-22)); promoted to `In-Testing` awaiting user review.
- **2026-11-22 (`In-Testing` parent activation)**: Linked delta [2026-11-22-commander-reliability-and-observability](../2026-11-22-commander-reliability-and-observability/spec.md) (`REQ-018..REQ-023`) entered `In-Testing`; added 1-hop `- Current contract:` links on `REQ-011`, `REQ-012`, `REQ-014`, `REQ-015`, `REQ-016`, and `REQ-017`.
- **2026-11-23 (`In-Testing` chain activation)**: Linked delta [2026-11-23-autonomous-remediation-and-proactive-audit](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md) (`REQ-024..REQ-028`) entered `In-Testing`; updated 1-hop `- Current contract:` links on `REQ-011`, `REQ-014`, `REQ-015`, `REQ-016`, and `REQ-017`.
- **2026-11-25 (`In-Testing` chain activation)**: Linked delta [2026-11-25-daily-report-memos-only](../2026-11-25-daily-report-memos-only/spec.md) (`REQ-029`) entered `In-Testing`; updated 1-hop `- Current contract:` link on `REQ-016`.
