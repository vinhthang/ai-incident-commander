# Decisions: Commander Reliability, Signal Hygiene & Observability Delta

Governing specification: [spec.md](./spec.md)

## Architectural rationale

- Governs: REQ-019, REQ-020, REQ-021, REQ-022, REQ-023

Constraints from the existing codebase fix the design for the remaining delta requirements:

1. **Prompt Escaping & Context-Aware `AgyQueue` (`REQ-020`)**: Switching `internal/prompt/prompt.go` from `html/template` to `text/template` eliminates HTML entity corruption (`&#34;`, `&lt;`, `&amp;`) in raw diffs and logs with identical template API semantics, while `select` on `ctx.Done()` in `EnqueueAgyJob` plus an early `job.Ctx.Err()` guard in `executeAgy` eliminates zombie subprocess spawns after the 10-minute alert deadline.
2. **Force-Clean Workspace & Verified GitHub Client (`REQ-021`)**: Using `git fetch origin main` + `git checkout -f main` + `git reset --hard origin/main` + `git clean -fd` inside `workspace.Lock()` guarantees a clean base even after aborted `Fixer` edits, `VerifyBranchPushed` ensures `Reviewer` never reviews an unpushed or empty branch, and passing `ctx` + checking `resp.StatusCode == http.StatusOK` in `GetPullRequestDiff` prevents GitHub API errors from masquerading as diffs.
3. **Signal-Clean Daily Fleet Snapshots (`REQ-022`)**: Inspecting `ownerReferences` on `Job`s and container `lastState.terminated.finishedAt` / `state.running.startedAt` directly inside existing `kubectl get pods -A -o json` and `kubectl get jobs -A -o json` payloads requires zero extra cluster API calls or external metric dependencies.
4. **Repository Hygiene & Docs Sync (`REQ-023`)**: Removing tracked `commander` + `patch-*.py` via `git rm` and adding `/commander` to `.gitignore` removes 22.3 MB of binary clutter while updating `README.md` and `AGENTS.md` keeps docs aligned with runtime behavior.

## ADR-0053: Zero-Dependency Stdlib Prometheus Exposition over External Metrics SDK

- Governs: REQ-018
- State: Accepted
- Context and constraints: `ai-incident-commander` needs a `GET /metrics` endpoint for VictoriaMetrics/Grafana scraping alongside stricter `/webhook` (`POST`-only, `2 MiB` limit) and in-memory state TTL eviction, while maintaining fast builds and offline/sandbox testability without modifying `go.mod` dependencies unless justified.
- Viable options: Option A — Implement a compact `internal/metrics` registry using `sync` + `sync/atomic` emitting standard Prometheus text exposition (`text/plain; version=0.0.4`); Option B — Add `github.com/prometheus/client_golang` to `go.mod`.
- Decision: Option A (`internal/metrics/metrics.go`). The service needs ~6 bounded counters/gauges/histograms (`commander_alerts_total`, `commander_triage_verdicts_total`, `commander_remediation_outcomes_total`, `commander_daily_reports_total`, `commander_agy_jobs_total`, `commander_agy_duration_seconds`, `commander_queue_depth`), which stdlib `sync/atomic` handles in ~150 lines with zero dependency churn and full deterministic `ResetForTest()` support.
- Costs and risks: Custom text formatting must escape metric label values accurately and emit valid histogram bucket cumulative counts (`+Inf` included). Mitigated via strict unit tests verifying Prometheus exposition syntax and cumulative histogram monotonicity.
- Rejected alternatives: Option B pulls over a dozen transitive dependencies (`prometheus/common`, `procfs`, `model`) into `go.mod` and `go.sum` for a handful of metrics.
- Affected components: `internal/metrics/metrics.go`, `cmd/commander/main.go`, `internal/webhook/handler.go`, `internal/webhook/daily_report.go`, `internal/minion/queue.go`, `internal/state/manager.go`.

## ADR-0054: On-Demand GitHub State Reconciliation and Dual-Sink Application Incident Publishing

- Governs: REQ-019
- State: Accepted
- Context and constraints: Live incident `#339` demonstrated that manually closing a GitHub issue without a Grafana `resolved` webhook leaves `cluster_incidents.state = 'firing'` stale indefinitely. Meanwhile, `apps` namespace application error alerts currently persist only to Postgres without log trace context or human visibility.
- Viable options: Option A — Perform an on-demand `github.IsIssueOpen` check inside `TrackClusterIncident` duplicate handling (reconciling closed issues to `'resolved'` in Postgres while conservatively keeping duplicate suppression if GitHub errors), enrich `apps` alerts via `VICTORIALOGS_URL` (`/select/logsql/query`), and publish new `open` app alerts to both Memos (`#AppIncident`) and deduplicated GitHub issues (`app-incident`); Option B — Run a background polling daemon periodically syncing all `cluster_incidents` rows against GitHub.
- Decision: Option A (`internal/github/client.go`, `internal/incident/tracker.go`, `internal/k8s/victorialogs.go`, `internal/db/memos.go`, `internal/webhook/handler.go`). Lazy reconciliation happens only when an active alert actually hits an open DB entry, avoiding background GitHub rate-limit consumption, while dual-sink publishing surfaces actionable `apps` failures immediately.
- Costs and risks: Adds one GitHub `Issues.Get` request per duplicate firing alert and one HTTP query to VictoriaLogs per new `apps` incident. Mitigated by bounding `HTTPClient` timeouts (`30s` GitHub, `5s` VictoriaLogs), keeping `IsIssueOpen` error behavior conservative, and gating GitHub issue creation on `FindOpenIssueForTarget` dedup.
- Rejected alternatives: Option B consumes GitHub API quota continuously even when no alerts are firing.
- Affected components: `internal/github/client.go`, `internal/incident/tracker.go`, `internal/k8s/victorialogs.go`, `internal/db/memos.go`, `internal/config/config.go`, `internal/webhook/handler.go`.

## Gate 1: normative review

| Check | Status | Evidence / required correction |
| --- | --- | --- |
| Codebase grounding | PASS | Grounded directly in inspected files (`internal/{k8s/daily_check.go,prompt/prompt.go,workspace/git.go,github/client.go,minion/queue.go,state/manager.go,webhook/handler.go,db/memos.go}`, `cmd/commander/main.go`, `.gitignore`) and live `arm10` observations (`#339`, `#344`). |
| Attributable intent and explicit scope | PASS | Quotes user `"proceed all of them"` against the 10-item improvement proposal; excludes mutating cluster resources or container publishing in `Non-goals`. |
| Observable requirements and meaningful acceptance criteria | PASS | `REQ-018..REQ-023` use `Changes:` links to frozen baseline `REQ-011..REQ-017` and specify testable `AC-NNN-1` success and `AC-NNN-2` boundary/failure criteria. |
| Assumptions and owner-deferred questions are explicit | PASS | Stdlib metrics/VictoriaLogs and deterministic 24h clock assumptions documented explicitly; zero unowned placeholders or `TODO`/`TBD` text. |
| Architectural rationale respects requirements | PASS | `Architectural rationale` and `ADR-0053..ADR-0054` govern `REQ-018..REQ-023` cleanly with concrete trade-offs and mitigations. |
| IDs, references, and revision route are consistent | PASS | Follows Case 3 linked delta route against `In-Testing` parent `2026-11-22-ai-incident-commander-baseline`, minting `REQ-018..REQ-023` and `ADR-0053..ADR-0054` and registering in `docs/specs/INDEX.md`. |
