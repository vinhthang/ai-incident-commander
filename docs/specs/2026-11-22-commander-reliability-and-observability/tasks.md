# Tasks: Commander Reliability, Signal Hygiene & Observability Delta

Sources: [spec.md](./spec.md), [design.md](./design.md)

## Task 1: Metrics

- [x] Complete the work and its applicable checks.
- Covers: REQ-018, REQ-020 / AC-018-1, AC-018-2, AC-020-1, AC-020-2
- Files: internal/prompt/prompt.go, internal/prompt/prompt_test.go, internal/minion/queue.go, internal/minion/queue_test.go, internal/metrics/metrics.go, internal/metrics/metrics_test.go
- Dependencies: None
- Blocked by: None
- Work: Follow Red -> Green -> Refactor in `internal/prompt`, `internal/metrics`, and `internal/minion/queue`: write failing tests for unescaped prompt characters (`text/template`), stdlib Prometheus metrics exposition, and `AgyQueue` context cancellation/expired-job skip, then implement until green.
- Completion evidence: Executed TDD Red -> Green across `internal/prompt`, `internal/metrics`, and `internal/minion/queue`; verified in [Run 1](./verification.md#run-1--2026-11-22).

## Task 2: Git State

- [x] Complete the work and its applicable checks.
- Covers: REQ-018, REQ-021 / AC-018-1, AC-018-2, AC-021-1, AC-021-2
- Files: internal/workspace/git.go, internal/workspace/git_test.go, internal/state/manager.go, internal/state/manager_test.go
- Dependencies: None
- Blocked by: None
- Work: Follow Red -> Green -> Refactor in `internal/workspace` and `internal/state`: write failing tests for dirty-tree `ResetToMain`, `VerifyBranchPushed`, and `pruneExpiredLocked` eviction of `COOLING_DOWN` / `resolvedDebounce` entries, then implement until green.
- Completion evidence: Executed TDD Red -> Green for force-clean `ResetToMain`, `VerifyBranchPushed`, and `pruneExpiredLocked`; verified in [Run 1](./verification.md#run-1--2026-11-22).

## Task 3: Daily Logs

- [x] Complete the work and its applicable checks.
- Covers: REQ-019, REQ-022 / AC-019-1, AC-019-2, AC-022-1, AC-022-2
- Files: internal/config/config.go, internal/k8s/daily_check.go, internal/k8s/daily_check_test.go, internal/k8s/victorialogs.go, internal/k8s/victorialogs_test.go, internal/db/memos.go, internal/db/memos_test.go
- Dependencies: None
- Blocked by: None
- Work: Follow Red -> Green -> Refactor in `internal/config`, `internal/k8s`, and `internal/db`: write failing tests for filtering `ai-commander-daily-report` trigger pods, superseded `CronJob` failures, >24h old restarts, `FetchVictoriaLogsContext`, and `FormatAppIncidentMemosNote`, then implement until green.
- Completion evidence: Executed TDD Red -> Green for daily snapshot noise filters, `FetchVictoriaLogsContext`, and `FormatAppIncidentMemosNote`; verified in [Run 1](./verification.md#run-1--2026-11-22).

## Task 4: Webhook

- [x] Complete the work and its applicable checks.
- Covers: REQ-018, REQ-019, REQ-021 / AC-018-1, AC-018-2, AC-019-1, AC-019-2, AC-021-1, AC-021-2
- Files: internal/github/client.go, internal/github/client_test.go, internal/incident/tracker.go, internal/incident/tracker_test.go, internal/webhook/handler.go, internal/webhook/handler_test.go, internal/webhook/daily_report.go, cmd/commander/main.go
- Dependencies: Task 1, Task 2, Task 3
- Blocked by: None
- Work: Follow Red -> Green -> Refactor to propagate `ctx` and HTTP status checks in `internal/github` (`IsIssueOpen`, `GetPullRequestDiff`), add `ReconcileClosedClusterIssue` in `internal/incident`, harden `/webhook` (`POST` + `2 MiB` cap), wire VictoriaLogs + Memos/GitHub `apps` publishing, wire `VerifyBranchPushed` in `remediateClusterIssue`, and register `GET /metrics` in `cmd/commander/main.go`.
- Completion evidence: Executed TDD Red -> Green for `internal/github`, `internal/incident`, `internal/webhook`, and `cmd/commander/main.go`; verified in [Run 1](./verification.md#run-1--2026-11-22).

## Task 5: Docs

- [x] Complete the work and its applicable checks.
- Covers: REQ-023 / AC-023-1, AC-023-2
- Files: .gitignore, README.md, AGENTS.md, commander, patch-fixer-go.py, patch-fixer.py, patch-git.py, patch-handler-fallback.py, patch-handler-loop.py, patch-handler.py, patch-prompt.py, patch-reviewer.py
- Dependencies: Task 4
- Blocked by: None
- Work: Ignore `/commander` in `.gitignore`, remove tracked `commander` and 8 legacy `patch-*.py` files via `git rm`, and update `README.md` + `AGENTS.md` to document all new capabilities and resolve fixed gotchas.
- Completion evidence: Added `/commander` to `.gitignore`, untracked `commander` and 8 `patch-*.py` files via `git rm`, and synchronized `README.md` + `AGENTS.md`; verified in [Run 1](./verification.md#run-1--2026-11-22).

## Gate 2: traceability and readiness

| Requirement | AC | ADR or rationale | Design | Task | Planned verification | Status / reason |
| --- | --- | --- | --- | --- | --- | --- |
| REQ-018 | AC-018-1 | [ADR-0053](./decisions.md#adr-0053-zero-dependency-stdlib-prometheus-exposition-over-external-metrics-sdk) | [Interfaces](./design.md#interfaces-and-flow) | [Task 1](#task-1-metrics), [Task 2](#task-2-git-state), [Task 4](#task-4-webhook) | [AC-018-1](./verification.md#ac-018-1-req-018) | PASS — Mapped end-to-end. |
| REQ-018 | AC-018-2 | [ADR-0053](./decisions.md#adr-0053-zero-dependency-stdlib-prometheus-exposition-over-external-metrics-sdk) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-metrics), [Task 4](#task-4-webhook) | [AC-018-2](./verification.md#ac-018-2-req-018) | PASS — Mapped end-to-end. |
| REQ-019 | AC-019-1 | [ADR-0054](./decisions.md#adr-0054-on-demand-github-state-reconciliation-and-dual-sink-application-incident-publishing) | [Interfaces](./design.md#interfaces-and-flow) | [Task 3](#task-3-daily-logs), [Task 4](#task-4-webhook) | [AC-019-1](./verification.md#ac-019-1-req-019) | PASS — Mapped end-to-end. |
| REQ-019 | AC-019-2 | [ADR-0054](./decisions.md#adr-0054-on-demand-github-state-reconciliation-and-dual-sink-application-incident-publishing) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 3](#task-3-daily-logs), [Task 4](#task-4-webhook) | [AC-019-2](./verification.md#ac-019-2-req-019) | PASS — Mapped end-to-end. |
| REQ-020 | AC-020-1 | [Rationale](./decisions.md#architectural-rationale) | [Components](./design.md#components) | [Task 1](#task-1-metrics) | [AC-020-1](./verification.md#ac-020-1-req-020) | PASS — Mapped end-to-end. |
| REQ-020 | AC-020-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-metrics) | [AC-020-2](./verification.md#ac-020-2-req-020) | PASS — Mapped end-to-end. |
| REQ-021 | AC-021-1 | [Rationale](./decisions.md#architectural-rationale) | [Interfaces](./design.md#interfaces-and-flow) | [Task 2](#task-2-git-state), [Task 4](#task-4-webhook) | [AC-021-1](./verification.md#ac-021-1-req-021) | PASS — Mapped end-to-end. |
| REQ-021 | AC-021-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 2](#task-2-git-state), [Task 4](#task-4-webhook) | [AC-021-2](./verification.md#ac-021-2-req-021) | PASS — Mapped end-to-end. |
| REQ-022 | AC-022-1 | [Rationale](./decisions.md#architectural-rationale) | [Interfaces](./design.md#interfaces-and-flow) | [Task 3](#task-3-daily-logs) | [AC-022-1](./verification.md#ac-022-1-req-022) | PASS — Mapped end-to-end. |
| REQ-022 | AC-022-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 3](#task-3-daily-logs) | [AC-022-2](./verification.md#ac-022-2-req-022) | PASS — Mapped end-to-end. |
| REQ-023 | AC-023-1 | [Rationale](./decisions.md#architectural-rationale) | [Components](./design.md#components) | [Task 5](#task-5-docs) | [AC-023-1](./verification.md#ac-023-1-req-023) | PASS — Mapped end-to-end. |
| REQ-023 | AC-023-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 5](#task-5-docs) | [AC-023-2](./verification.md#ac-023-2-req-023) | PASS — Mapped end-to-end. |

- Backward scope check: PASS — Every component and task maps to REQ-018..REQ-023.
- Reference and catalog consistency: PASS — Synchronized with INDEX.md and spec.md.
