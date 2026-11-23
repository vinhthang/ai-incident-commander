# Design: AI Incident Commander Baseline System Specification

Sources: [spec.md](./spec.md), [decisions.md](./decisions.md)

## Components

| Requirement / AC | Path and symbol | Responsibility / change |
| --- | --- | --- |
| REQ-011 / AC-011-1, AC-011-2 | `cmd/commander/main.go` (`initTracer`, `main`), `internal/config/config.go`, `internal/webhook/handler.go` (`HandleWebhook`, `HandleHealthz`, `isAuthorizedBearerToken`), `internal/webhook/daily_report.go` (`HandleDailyReport`) | Bind HTTP mux on `PORT` (`8085`), enforce `WEBHOOK_AUTH_TOKEN` Bearer auth and method checks, report DB/queue health, configure OTel tracing, and shut down gracefully. |
| REQ-012 / AC-012-1, AC-012-2 | `internal/incident/tracker.go`, `internal/state/manager.go`, `internal/minion/app_fixer.go`, `internal/db/db.go`, `internal/github/client.go` (`CloseIssue`, `FindOpenIssueByTitle`, `FindOpenIssueForTarget`, `FindOpenPRForTarget`), `internal/webhook/handler.go` (`processAlert`, `checkExistingOpenGitHubWork`) | Derive SHA-256 identity (`exported_namespace`/`namespace`, non-scraper target, `alertname`), run `apps` Postgres dedup via `application_incidents`, track `cluster_incidents`, check normalized open GitHub Issues/PRs, and auto-close resolved incidents. |
| REQ-013 / AC-013-1, AC-013-2 | `internal/state/manager.go` (`WaitForSoak`, `NotifyResolved`), `internal/k8s/telemetry.go` (`CheckWorkloadStatus`, `FetchDiagnosticBundle`, `FetchTelemetry`), `internal/incident/tracker.go` (`IsEphemeralNodeOutage`) | Wait `SOAK_DURATION` with early cancel on resolve, verify rollout/endpoint recovery, gather Layer 1 HTTP/TLS + Kubernetes + `charts/vinhthang-fleet/` Helm telemetry, and suppress `k3s-worker-*` CNI/reboot outages. |
| REQ-014 / AC-014-1, AC-014-2 | `internal/minion/queue.go` (`StartAgyWorker`, `EnqueueAgyJob`, `syncAgyRuntimeDir`), `internal/minion/triage.go` (`RunDiagnosticTriage`, `ParseTriageDecision`), `internal/prompt/prompt.go`, `internal/prompt/defaults/*.tmpl` | Serialize `agy` CLI invocations via concurrency-1 `AgyQueue`, isolate runtime auth and `mcp_config.json`, render `/etc/commander/templates` or embedded templates, and gate `FIX_VIA_PR` on `Confidence: HIGH`. |
| REQ-015 / AC-015-1, AC-015-2 | `internal/webhook/handler.go` (`remediateClusterIssue`), `internal/workspace/git.go`, `internal/minion/fixer.go`, `internal/minion/senior_fixer.go`, `internal/minion/reviewer.go`, `internal/github/client.go` (`CreatePullRequest`, `GetPullRequestDiff`, `WaitForPRChecks`, `MergePullRequest`) | Lock git workspace, branch `fix/alert-<issue>-<ts>` from `main`, iterate up to 3 attempts of `Fixer` → `SeniorFixer` → PR → `Reviewer` → CI Check Runs polling, and merge only when `AUTO_MERGE=true`. |
| REQ-016 / AC-016-1, AC-016-2 | `internal/webhook/daily_report.go`, `internal/k8s/daily_check.go` (`CollectDailyFleetSnapshot`), `internal/incident/tracker.go` (`SummarizeRecentIncidents`), `internal/minion/daily_report.go`, `internal/db/memos.go` | Enforce single-flight `POST /daily-report`, gather read-only cluster/Helm/VictoriaMetrics + 24h incident snapshot, run `RunDailyReport`, insert private Memos note, and open/auto-close `daily-report` GitHub issue. |
| REQ-017 / AC-017-1, AC-017-2 | `README.md`, `AGENTS.md` | Document accurate architecture, endpoints, configuration, build/lint/test commands, safety/concurrency invariants, and known codebase gotchas. |

## Interfaces and flow

- **HTTP Ingress (`cmd/commander/main.go`)**:
  - `POST /webhook`: Optional `Authorization: Bearer <WEBHOOK_AUTH_TOKEN>`, decodes `GrafanaAlertPayload`, spawns background goroutines per alert with detached `context.WithoutCancel` and a `10m` lifecycle timeout.
  - `POST /daily-report`: Method-checked (`405`), Bearer-authenticated (`401`), single-flight guarded (`409 Conflict` if active), executes `executeDailyReport` with a `5m` context (`202 Accepted`).
  - `GET /healthz`: Method-checked (`405`), pings `db.DB` with a `2s` timeout (`disabled`, `connected`, or `error` → `503`), reports `queue_depth: len(minion.AgyQueue)`.
- **Alert Processing Pipeline (`internal/webhook/handler.go`)**:
  - `status == "resolved"` → `state.AcquireResolveLock` (`1m` debounce + soak cancel) → `incident.ResolveClusterIncident` → `github.CloseIssue` (fallback `FindOpenIssueByTitle`) → delete `ActiveIncidents[fingerprint]`.
  - `namespace == "apps"` and `isApplicationErrorAlert` → `TrackAppIncident` (Postgres SHA-256 `service|endpointOrTopic|statusCode`) → if new, `k8s.FetchTelemetry` + `RunTriage` → `SaveNewAppIncident` (`open` or `ignored`) → return without PR.
  - Cluster/edge firing alert → `WaitForSoak(SOAK_DURATION)` → `CheckWorkloadStatus` → `FetchDiagnosticBundle` → `IsEphemeralNodeOutage` → `TrackClusterIncident` → `checkExistingOpenGitHubWork` → `RunDiagnosticTriage` → `CreateIssue` + `RecordClusterIssue` → `remediateClusterIssue` (`FIX_VIA_PR` + `HIGH` confidence only).
- **Persistence Schemas (`internal/db/db.go`, `internal/db/memos.go`)**:
  - `application_incidents`: PK `incident_hash`, `service_name`, `endpoint_or_topic`, `status_code`, `state`, `occurrence_count`, `latest_trace_id`, `last_triage_analysis`, timestamps.
  - `cluster_incidents`: PK `fingerprint`, `alert_name`, `namespace`, `target_resource`, `issue_number`, `state`, `occurrence_count`, timestamps.
  - Memos `memo`: `INSERT` (`uid`, `creator_id=1`, `content`, `visibility='PRIVATE'`, `pinned`, epoch timestamps).
- **Minion Execution (`internal/minion/queue.go`, `internal/prompt/prompt.go`)**:
  - Single worker drains `AgyQueue` (`chan AgyJob`, cap `100`), optionally copies `AGY_HOST_AUTH_DIR/{antigravity-oauth-token,installation_id,settings.json}` to `~/.gemini/antigravity-cli`, writes empty `~/.gemini/config/mcp_config.json`, and executes `/usr/local/bin/agy -p <prompt> --dangerously-skip-permissions --disable-slash-commands [--model <AgyModel>]` in `WORKSPACE_DIR`.

## Boundaries and recovery

- **Auth & Input Boundaries (`REQ-011`)**: Empty `WEBHOOK_AUTH_TOKEN` leaves endpoints open for local dev; invalid JSON on `/webhook` returns `400 Bad Request`; DB outage degrades `/healthz` to `503` without crashing webhook intake.
- **Database Nil-Safety & Concurrency (`REQ-012`)**: When `DB_DSN` is unset (`db.DB == nil`), `TrackAppIncident`, `SaveNewAppIncident`, `TrackClusterIncident`, `RecordClusterIssue`, `ResolveClusterIncident`, and `SummarizeRecentIncidents` return safe defaults without panicking; `StateMu` (`sync.RWMutex`) protects `ActiveIncidents`, `resolvedDebounce`, and `soakWaiters`.
- **Transient & Hardware Noise Recovery (`REQ-013`)**: Early `resolved` webhook cancels `WaitForSoak` immediately; in-progress Helm rollouts or recovered HTTP `2xx`/`3xx` endpoints short-circuit before `FetchDiagnosticBundle`; `k3s-worker-*` CNI/reboot strings drop before DB/GitHub/AI calls.
- **Triage & Queue Safety (`REQ-014`)**: `ParseTriageDecision` defaults any non-`HIGH` `FIX_VIA_PR` or unrecognized final line to `ESCALATE_HUMAN`; `RunDiagnosticTriage` errors halt safely before `CreateIssue`.
- **Workspace & Remediation Bounds (`REQ-015`)**: `workspace.Lock()` serializes git mutations with `AppendHelmDiagnostics` / `collectHelmGitOpsSnapshot`; `CreateAndCheckoutBranch` forbids `main`/`master`; up to 3 attempts pass `SeniorFixer`, `Reviewer`, or `WaitForPRChecks` feedback back to `Fixer` before assigning `GITHUB_OWNER`.
- **Daily Audit Safety (`REQ-016`)**: `ParseDailyHealthStatus` defaults non-matching outputs to `ATTENTION_NEEDED`; `ExecuteDailyReport` creates an `(ERROR)` issue on `agy` failure and resets `dailyReportRunning` via `defer`.
- **Documentation Integrity (`REQ-017`)**: `README.md` and `AGENTS.md` stay strictly grounded in inspected Go sources and CI config without executing unrequested Go edits.

## Dependencies and unresolved design work

None.
