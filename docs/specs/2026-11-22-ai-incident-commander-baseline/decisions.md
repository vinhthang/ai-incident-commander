# Decisions: AI Incident Commander Baseline System Specification

Governing specification: [spec.md](./spec.md)

## Architectural rationale

- Governs: REQ-011, REQ-012, REQ-013, REQ-016, REQ-017

Existing codebase constraints and runtime topology fix the baseline choices for the remaining requirements:

1. **Standard-Library HTTP Control Plane & OTel (`REQ-011`)**: `cmd/commander/main.go` binds `http.ServeMux` with explicit server timeouts (`ReadTimeout: 15s`, `ReadHeaderTimeout: 5s`, `WriteTimeout: 30s`, `IdleTimeout: 60s`), OpenTelemetry tracing (`OTEL_EXPORTER_OTLP_ENDPOINT` gRPC or stdout fallback), and `SIGINT`/`SIGTERM` graceful shutdown without an external web framework.
2. **Composite Identity & Multi-Layer Deduplication (`REQ-012`)**: Prometheus `kube-state-metrics` and `victoriametrics` scrape labels shadow the failing workload unless `exported_namespace`/`exported_pod` and `IsMetricsScraperLabel` filtering are applied, and in-memory state resets across pod restarts unless backed by PostgreSQL (`cluster_incidents`, `application_incidents`) plus GitHub open Issue/PR lookup (`NormalizeWorkloadBase`).
3. **Stabilization Soak & Read-Only Telemetry (`REQ-013`)**: Waiting `SOAK_DURATION` (`60s` default) with early `resolved` interruption, checking `CheckWorkloadStatus`, suppressing `k3s-worker-*` CNI/reboot blips, and bundling live `kubectl` + `charts/vinhthang-fleet/` Helm context avoids acting on transient rollouts or power cycles.
4. **Single-Flight Daily Fleet Check (`REQ-016`)**: `POST /daily-report` guards overlapping audits with `dailyReportRunning` (`atomic.Bool`), persists private archives directly to Memos PostgreSQL (`MEMOS_DB_DSN`), and uses GitHub `daily-report` issues that auto-close only on `HEALTH_STATUS: HEALTHY`.
5. **Documentation Synchronization (`REQ-017`)**: `README.md` and `AGENTS.md` derive strictly from observed source files and CI workflow definitions rather than obsolete prototype README text.

## ADR-0050: Single-Concurrency Serialized agy CLI Worker Queue

- Governs: REQ-014, REQ-016
- State: Accepted
- Context and constraints: Each AI minion invocation (`RunDiagnosticTriage`, `RunFixer`, `RunSeniorFixer`, `RunReviewer`, `RunDailyReport`) executes `/usr/local/bin/agy` inside the shared container pod and against `WORKSPACE_DIR` (`/app/workspace/oci`), while sharing OAuth credentials (`AGY_HOST_AUTH_DIR`).
- Viable options: Option A — Single background worker consuming buffered `AgyQueue` (`chan AgyJob` size `100`, concurrency `1`) with per-job runtime auth sync and empty `mcp_config.json`; Option B — Unbounded concurrent `agy` subprocess spawning per webhook goroutine; Option C — Direct stateless Gemini REST API calls in Go.
- Decision: Option A (`internal/minion/queue.go`). Serializing `agy` subprocesses prevents OOM spikes on constrained nodes, eliminates OAuth token/session file races, isolates MCP tools via empty `mcp_config.json`, and keeps tool-using CLI reasoning intact.
- Costs and risks: Burst alert storms queue sequentially behind the active 3–5 minute `agy` subprocess, increasing tail latency and risking 10-minute `processAlert` context expiration if multiple fixes pile up. Mitigated by pre-triage deduplication, `SOAK_DURATION`, hardware outage filtering, and `/healthz` exposing `queue_depth`.
- Rejected alternatives: Option B risks concurrent git/memory contention inside the pod; Option C lacks autonomous repo inspection and `helm template` validation inside `WORKSPACE_DIR`.
- Affected components: `internal/minion/queue.go`, `internal/webhook/handler.go`, `internal/config/config.go`, `internal/prompt/prompt.go`.

## ADR-0051: Diagnostic-First 3-Way Verdict Gate with High-Confidence Downgrade

- Governs: REQ-013, REQ-014
- State: Accepted
- Context and constraints: Early alert triage only differentiated `"IGNORED"` from fixing, causing uncertain, secret-related, or application-code alerts to open automated GitOps branches and burn `Fixer` retries.
- Viable options: Option A — 3-way verdict (`IGNORE`, `ESCALATE_HUMAN`, `FIX_VIA_PR`) requiring explicit `Confidence: HIGH` for `FIX_VIA_PR` and defaulting any lower confidence or unrecognized output to `ESCALATE_HUMAN`; Option B — Binary ignore-or-fix decision relying on `Reviewer` to catch unfixable alerts later.
- Decision: Option A (`internal/minion/triage.go`, `internal/prompt/defaults/triage.tmpl`). `ParseTriageDecision` enforces conservative escalation before branch creation or code modification.
- Costs and risks: False-conservative `MEDIUM` confidence verdicts escalate to a human issue (`needs-human-review`) instead of attempting an automated PR. Mitigated by providing rich pre-collected `FetchDiagnosticBundle` evidence (Service/Endpoints, `lastState`, previous logs, matched Helm templates/values).
- Rejected alternatives: Option B wastes `agy` tokens, pollutes GitHub with futile branches/PRs, and risks premature automated edits for non-declarative failures.
- Affected components: `internal/minion/triage.go`, `internal/prompt/defaults/triage.tmpl`, `internal/webhook/handler.go`.

## ADR-0052: Multi-Stage Review, CI Check Gate, and Opt-In Auto-Merge

- Governs: REQ-015
- State: Accepted
- Context and constraints: The legacy prototype committed directly to `main` and relied on post-failure rollback (`Rollback Minion`), risking broken ArgoCD reconciliations on `vinhthang/oci`.
- Viable options: Option A — Isolated feature branch (`fix/alert-...`), local `SeniorFixer` pre-review gate, GitHub PR + independent `Reviewer` gate, `WaitForPRChecks` CI Check Run polling, and `AUTO_MERGE=false` safe default across up to 3 bounded retries; Option B — Direct commits to `main` with automated git revert on repeat alerts.
- Decision: Option A (`internal/webhook/handler.go`, `internal/workspace/git.go`, `internal/minion/senior_fixer.go`, `internal/minion/reviewer.go`, `internal/github/client.go`). Every fix is validated locally (`helm template`), pre-reviewed (`SeniorFixer`), peer-reviewed on PR diff (`Reviewer`), CI-verified (`WaitForPRChecks`), and held for human merge unless `AUTO_MERGE=true` is explicitly enabled.
- Costs and risks: Up to 3 retries plus 5-minute CI polling can approach the 10-minute `processAlert` budget, and `AUTO_MERGE=false` requires human click-to-merge. Mitigated by assigning `GITHUB_OWNER` with clear check summaries on timeout or manual-merge handoff.
- Rejected alternatives: Option B exposes production cluster reconciliation to unreviewed manifests and lacks CI validation before hitting `main`.
- Affected components: `internal/webhook/handler.go`, `internal/workspace/git.go`, `internal/minion/fixer.go`, `internal/minion/senior_fixer.go`, `internal/minion/reviewer.go`, `internal/github/client.go`, `internal/config/config.go`.

## Gate 1: normative review

| Check | Status | Evidence / required correction |
| --- | --- | --- |
| Codebase grounding | PASS | Every path and symbol cited in `spec.md` (`cmd/commander/main.go`, `internal/{config,db,github,incident,k8s,minion,prompt,state,webhook,workspace}`) was read and verified directly at commit `c65e4ce`. |
| Attributable intent and explicit scope | PASS | `Problem and source intent` quotes the exact user request, and `Non-goals` explicitly excludes `.go` edits, mutating `kubectl`, `apps` PR generation, and altering legacy specs. |
| Observable requirements and meaningful acceptance criteria | PASS | `REQ-011` through `REQ-017` use EARS `WHEN`/`WHILE`/`IF ... THEN` statements, each with deterministic `AC-NNN-1` success and `AC-NNN-2` boundary/failure criteria. |
| Assumptions and owner-deferred questions are explicit | PASS | Baseline read-only scope (`c65e4ce`) and `INDEX.md` catalog preservation assumptions are explicit in `spec.md`; zero unowned `TODO`/`TBD` markers exist. |
| Architectural rationale respects requirements | PASS | `Architectural rationale` and `ADR-0050..ADR-0052` govern `REQ-011..REQ-017` without overriding any requirement or introducing unrequested features. |
| IDs, references, and revision route are consistent | PASS | Allocates `REQ-011..REQ-017` and `ADR-0050..ADR-0052` strictly above existing `alert-dedup-and-autoclose` (`REQ-001..REQ-006`, `ADR-0001..ADR-0003`) and `pc1-qa-router-multicluster-refactor` (`REQ-001..REQ-010`, `ADR-0047..ADR-0049`); registered in `docs/specs/INDEX.md`. |
