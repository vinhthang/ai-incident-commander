# Design: Commander Reliability, Signal Hygiene & Observability Delta

Sources: [spec.md](./spec.md), [decisions.md](./decisions.md)

## Components

| Requirement / AC | Path and symbol | Responsibility / change |
| --- | --- | --- |
| REQ-018 / AC-018-1, AC-018-2 | `internal/metrics/metrics.go` (`HandleMetrics`, counters, duration histogram, `SetQueueDepthFunc`), `cmd/commander/main.go`, `internal/webhook/handler.go` (`HandleWebhook`, `MaxWebhookBodyBytes`), `internal/webhook/daily_report.go`, `internal/state/manager.go` (`pruneExpiredLocked`) | Enforce `POST`-only + `2 MiB` `MaxBytesReader` on `/webhook`, prune expired `COOLING_DOWN` and `resolvedDebounce` keys, and serve Prometheus exposition on `GET /metrics`. |
| REQ-019 / AC-019-1, AC-019-2 | `internal/github/client.go` (`IsIssueOpen`), `internal/incident/tracker.go` (`ReconcileClosedClusterIssue`), `internal/config/config.go` (`VictoriaLogsURL`), `internal/k8s/victorialogs.go` (`FetchVictoriaLogsContext`), `internal/db/memos.go` (`FormatAppIncidentMemosNote`), `internal/webhook/handler.go` (`publishOpenAppIncidentIssue`) | Reconcile closed GitHub issues back to `resolved` in `cluster_incidents`, enrich `apps` alerts via VictoriaLogs LogsQL, and publish new `open` app alerts to Memos (`#AppIncident`) and deduplicated GitHub issues. |
| REQ-020 / AC-020-1, AC-020-2 | `internal/prompt/prompt.go` (`text/template`), `internal/minion/queue.go` (`EnqueueAgyJob`, `executeAgy`) | Render unescaped prompt templates with `text/template`, honor `ctx.Done()` in `EnqueueAgyJob`, and fast-skip expired jobs in `executeAgy` while recording `agy` job metrics. |
| REQ-021 / AC-021-1, AC-021-2 | `internal/workspace/git.go` (`ResetToMain`, `VerifyBranchPushed`), `internal/github/client.go` (`APIBaseURL`, `HTTPClient`, `GetPullRequestDiff`, `ctx` propagation), `internal/webhook/handler.go` (`remediateClusterIssue`), `internal/webhook/daily_report.go` | Force-clean local workspace on reset, require non-empty diff and clean pushed branch SHA before `SeniorFixer`/PR/`Reviewer`, propagate `ctx` across `internal/github`, and check HTTP `200` on `GetPullRequestDiff`. |
| REQ-022 / AC-022-1, AC-022-2 | `internal/k8s/daily_check.go` (`collectPodsSnapshot`, `collectJobsAndPVCsSnapshot`) | Filter `ai-commander-daily-report` trigger pods, flag `CronJob` child failures only on the latest run, and count restarts as unhealthy only when terminated/restarted within `24h`. |
| REQ-023 / AC-023-1, AC-023-2 | `.gitignore`, root `commander` & `patch-*.py`, `README.md`, `AGENTS.md` | Ignore `/commander`, untrack root `commander` and 8 legacy `patch-*.py` files via `git rm`, and update `README.md` + `AGENTS.md`. |

## Interfaces and flow

- **Prometheus Metrics (`internal/metrics/metrics.go`, `cmd/commander/main.go`)**:
  - `GET /metrics` writes `Content-Type: text/plain; version=0.0.4; charset=utf-8` with `commander_alerts_total{status}`, `commander_triage_verdicts_total{verdict}`, `commander_remediation_outcomes_total{outcome}`, `commander_daily_reports_total{status}`, `commander_agy_jobs_total{outcome}`, `commander_agy_duration_seconds` histogram buckets (`5, 15, 30, 60, 120, 180, 300, +Inf`), and `commander_queue_depth` gauge backed by `len(minion.AgyQueue)`.
- **Closed-Issue Reconciliation & App Dual-Sink (`internal/incident/tracker.go`, `internal/k8s/victorialogs.go`, `internal/webhook/handler.go`)**:
  - When `TrackClusterIncident` returns `(isDuplicate=true, existingIssue > 0)`, `ReconcileClosedClusterIssue` calls `github.IsIssueOpen(ctx, existingIssue)`; if `open == false`, it calls `ResolveClusterIncident(fingerprint)` so `processAlert` continues as a fresh incident.
  - For `apps` alerts, `FetchVictoriaLogsContext(ctx, config.VictoriaLogsURL, serviceName, traceID)` queries `POST/GET /select/logsql/query` NDJSON (limiting lines/bytes), appends context to `telemetry`, and if triage is not `IGNORED`, inserts `FormatAppIncidentMemosNote` into `MEMOS_DB_DSN` and opens a deduplicated GitHub issue (`ai-incident`, `app-incident`, `needs-human-review`).
- **Workspace Force-Clean & Push Check (`internal/workspace/git.go`)**:
  - `ResetToMain()` checks `git remote get-url origin`: if present, runs `git fetch origin main`, `git checkout -f main`, `git reset --hard origin/main`, `git clean -fd`; otherwise `git checkout -f main` + `git clean -fd`.
  - `VerifyBranchPushed(branchName)` verifies `git status --porcelain` is empty and compares `git rev-parse HEAD` against `git rev-parse refs/remotes/origin/<branchName>`.
- **Signal-Clean Daily Snapshot (`internal/k8s/daily_check.go`)**:
  - `collectPodsSnapshotWithNow(..., now time.Time)` skips pods where `labels["app"] == "ai-commander-daily-report"` or `job-name` starts with `ai-commander-daily-report-`, and inspects `lastState.terminated.finishedAt` / `state.running.startedAt` against `now.Add(-24 * time.Hour)`.
  - `collectJobsAndPVCsSnapshot` parses `metadata.creationTimestamp`, `metadata.ownerReferences` (`Kind == "CronJob"`), and `status.failed`; `CronJob`-owned jobs are grouped by `namespace/cronJobName` and evaluated only on the newest `Job`.

## Boundaries and recovery

- **HTTP Ingress & Map Pruning (`REQ-018`)**: `/webhook` rejects non-`POST` (`405`) and oversized payloads > `2 MiB` (`413`) before spawning goroutines; `/metrics` rejects non-`GET` (`405`). State pruning skips active `SOAKING`, `TRIAGING`, `FIXING`, and `RESOLVING` entries.
- **Reconciliation & External Query Safety (`REQ-019`)**: GitHub API errors in `IsIssueOpen` conservatively keep duplicate suppression (`stillOpen = true`); `FetchVictoriaLogsContext` sanitizes query tokens against LogsQL injection and degrades to `""` on timeout/non-`200`.
- **Queue & Template Safety (`REQ-020`)**: `EnqueueAgyJob` handles nil `ctx` safely (`context.Background()`), returns `ctx.Err()` if `ctx.Done()` fires before send or result receipt, and `executeAgy` skips auth sync + subprocess execution when `job.Ctx.Err() != nil`.
- **Git & Diff Validation (`REQ-021`)**: Empty `localDiff` or `VerifyBranchPushed` errors feed actionable feedback back into the 3-attempt `Fixer` retry loop instead of creating empty PRs or reviewing stale diffs; `GetPullRequestDiff` enforces `HTTPClient` (`30s` timeout) and non-`200` error returns.
- **24h Snapshot Edge Cases (`REQ-022`)**: Containers with `restartCount > 0` whose `finishedAt`/`startedAt` timestamps are missing/unparseable conservatively stay flagged so silent failures are never hidden.
- **Cleanup & Docs (`REQ-023`)**: Untracking `commander` and `patch-*.py` removes only legacy root artifacts without touching `docs/specs/` or `internal/`.

## Dependencies and unresolved design work

None.
