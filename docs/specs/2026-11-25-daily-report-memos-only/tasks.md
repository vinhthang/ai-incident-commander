# Tasks: Memos-Only Daily Fleet Reports Delta

Sources: [spec.md](./spec.md), [design.md](./design.md)

## Task 1: Daily

- [x] Remove `[Daily Fleet Check]` GitHub issue creation/assignment/closing from `internal/webhook/daily_report.go`, consolidate remediation results into `#DailyFleetCheck` Memos notes via `deliverDailyMemosNote`, label proactive issues `["ai-incident", "proactive-fix"]`, verify TDD Red -> Green in `daily_report_test.go`, and update `README.md` / `AGENTS.md`.
- Covers: REQ-029 / AC-029-1, AC-029-2
- Files: internal/webhook/daily_report.go, internal/webhook/daily_report_test.go, README.md, AGENTS.md
- Dependencies: None
- Blocked by: None
- Work: Update `daily_report_test.go` for Red failure asserting 0 `[Daily Fleet Check]` GitHub issues and `proactive-fix` labels + consolidated Memos note; refactor `executeDailyReport` with `deliverDailyMemosNote` until Green; update `README.md` and `AGENTS.md`.
- Completion evidence: Executed TDD Red -> Green on `internal/webhook/daily_report.go` + `daily_report_test.go` and updated `README.md` + `AGENTS.md`.

## Gate 2: traceability and readiness

| Requirement | AC | ADR or rationale | Design | Task | Planned verification | Status / reason |
| --- | --- | --- | --- | --- | --- | --- |
| REQ-029 | AC-029-1 | [Rationale](./decisions.md#architectural-rationale) | [Interfaces](./design.md#interfaces-and-flow) | [Task 1](#task-1-daily) | [AC-029-1](./verification.md#ac-029-1-req-029) | PASS |
| REQ-029 | AC-029-2 | [Rationale](./decisions.md#architectural-rationale) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-daily) | [AC-029-2](./verification.md#ac-029-2-req-029) | PASS |

- Backward scope check: PASS — All touched files and helpers directly serve `REQ-029`.
- Reference and catalog consistency: PASS — Synchronized with `spec.md`, `decisions.md`, `design.md`, `verification.md`, and `docs/specs/INDEX.md`.
