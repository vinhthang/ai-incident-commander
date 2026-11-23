# Tasks: AI Incident Commander Baseline System Specification

Sources: [spec.md](./spec.md), [design.md](./design.md)

## Task 1: Baseline

- [x] Complete the work and its applicable checks.
- Covers: REQ-011, REQ-012, REQ-013, REQ-014, REQ-015, REQ-016 / AC-011-1, AC-011-2, AC-012-1, AC-012-2, AC-013-1, AC-013-2, AC-014-1, AC-014-2, AC-015-1, AC-015-2, AC-016-1, AC-016-2
- Files: internal/webhook/handler_test.go, internal/minion/triage_test.go, internal/minion/queue_test.go, internal/minion/daily_report_test.go, internal/state/manager_test.go, internal/github/client_test.go, internal/incident/tracker_test.go, internal/k8s/telemetry_test.go, internal/k8s/daily_check_test.go, internal/db/memos_test.go, internal/workspace/git_test.go, internal/prompt/prompt_test.go
- Dependencies: None
- Blocked by: None
- Work: Run `go test -v -count=1 ./...` and `go vet ./...` to confirm all baseline packages pass without modifying Go source code.
- Completion evidence: Executed `go test -v -count=1 ./...` and `go vet ./...` at `c65e4ce` (exit code 0 across all 9 tested packages); recorded in [Run 1](./verification.md#run-1--2026-11-22).

## Task 2: README

- [x] Complete the work and its applicable checks.
- Covers: REQ-017 / AC-017-1, AC-017-2
- Files: README.md
- Dependencies: None
- Blocked by: None
- Work: Replace obsolete root `README.md` with accurate Go 1.25 architecture, Mermaid pipeline diagram, endpoints, full environment variable table, ConfigMap prompt override docs, test commands, and container build/pull instructions.
- Completion evidence: Wrote `README.md` and verified all routes, env vars, ConfigMap overrides, and absence of obsolete prototype claims (`DOCS_VERIFIED_OK`); recorded in [Run 1](./verification.md#run-1--2026-11-22).

## Task 3: AGENTS

- [x] Complete the work and its applicable checks.
- Covers: REQ-017 / AC-017-1, AC-017-2
- Files: AGENTS.md
- Dependencies: None
- Blocked by: None
- Work: Author root `AGENTS.md` documenting repo scope boundary, build/test/lint/spec commands, package concurrency map, safety invariants, and known codebase gotchas.
- Completion evidence: Wrote `AGENTS.md` and verified repo boundary, concurrency primitives, invariants, ID allocation, and gotchas (`DOCS_VERIFIED_OK`); recorded in [Run 1](./verification.md#run-1--2026-11-22).

## Gate 2: traceability and readiness

| Requirement | AC | ADR or rationale | Design | Task | Planned verification | Status / reason |
| --- | --- | --- | --- | --- | --- | --- |
| REQ-011 | AC-011-1 | [Rationale](./decisions.md#architectural-rationale) | [Components](./design.md#components) | [Task 1](#task-1-baseline) | [AC-011-1](./verification.md#ac-011-1-req-011) | PASS — Mapped end-to-end. |
| REQ-011 | AC-011-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-baseline) | [AC-011-2](./verification.md#ac-011-2-req-011) | PASS — Mapped end-to-end. |
| REQ-012 | AC-012-1 | [Rationale](./decisions.md#architectural-rationale) | [Interfaces](./design.md#interfaces-and-flow) | [Task 1](#task-1-baseline) | [AC-012-1](./verification.md#ac-012-1-req-012) | PASS — Mapped end-to-end. |
| REQ-012 | AC-012-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-baseline) | [AC-012-2](./verification.md#ac-012-2-req-012) | PASS — Mapped end-to-end. |
| REQ-013 | AC-013-1 | [ADR-0051](./decisions.md#adr-0051-diagnostic-first-3-way-verdict-gate-with-high-confidence-downgrade) | [Components](./design.md#components) | [Task 1](#task-1-baseline) | [AC-013-1](./verification.md#ac-013-1-req-013) | PASS — Mapped end-to-end. |
| REQ-013 | AC-013-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-baseline) | [AC-013-2](./verification.md#ac-013-2-req-013) | PASS — Mapped end-to-end. |
| REQ-014 | AC-014-1 | [ADR-0050](./decisions.md#adr-0050-single-concurrency-serialized-agy-cli-worker-queue) | [Interfaces](./design.md#interfaces-and-flow) | [Task 1](#task-1-baseline) | [AC-014-1](./verification.md#ac-014-1-req-014) | PASS — Mapped end-to-end. |
| REQ-014 | AC-014-2 | [ADR-0051](./decisions.md#adr-0051-diagnostic-first-3-way-verdict-gate-with-high-confidence-downgrade) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-baseline) | [AC-014-2](./verification.md#ac-014-2-req-014) | PASS — Mapped end-to-end. |
| REQ-015 | AC-015-1 | [ADR-0052](./decisions.md#adr-0052-multi-stage-review-ci-check-gate-and-opt-in-auto-merge) | [Components](./design.md#components) | [Task 1](#task-1-baseline) | [AC-015-1](./verification.md#ac-015-1-req-015) | PASS — Mapped end-to-end. |
| REQ-015 | AC-015-2 | [ADR-0052](./decisions.md#adr-0052-multi-stage-review-ci-check-gate-and-opt-in-auto-merge) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-baseline) | [AC-015-2](./verification.md#ac-015-2-req-015) | PASS — Mapped end-to-end. |
| REQ-016 | AC-016-1 | [ADR-0050](./decisions.md#adr-0050-single-concurrency-serialized-agy-cli-worker-queue) | [Interfaces](./design.md#interfaces-and-flow) | [Task 1](#task-1-baseline) | [AC-016-1](./verification.md#ac-016-1-req-016) | PASS — Mapped end-to-end. |
| REQ-016 | AC-016-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-baseline) | [AC-016-2](./verification.md#ac-016-2-req-016) | PASS — Mapped end-to-end. |
| REQ-017 | AC-017-1 | [Rationale](./decisions.md#architectural-rationale) | [Components](./design.md#components) | [Task 2](#task-2-readme), [Task 3](#task-3-agents) | [AC-017-1](./verification.md#ac-017-1-req-017) | PASS — Mapped end-to-end. |
| REQ-017 | AC-017-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 2](#task-2-readme), [Task 3](#task-3-agents) | [AC-017-2](./verification.md#ac-017-2-req-017) | PASS — Mapped end-to-end. |

- Backward scope check: PASS — Every component and task maps to REQ-011..REQ-017.
- Reference and catalog consistency: PASS — Synchronized with INDEX.md and spec.md.
