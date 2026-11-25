# Decisions: Memos-Only Daily Fleet Reports Delta

Governing specification: [spec.md](./spec.md)

## Architectural rationale

- Governs: REQ-029

Because Memos (`MEMOS_DB_DSN`, `#DailyFleetCheck`) is already wired as the report archive and the user explicitly requested removing the `[Daily Fleet Check]` GitHub report issues, there is no multi-option architectural trade-off requiring an `ADR-NNN`. Running bounded `dispatchDailyProactiveFixes` prior to `deliverDailyMemosNote` lets a single `#DailyFleetCheck` note carry both the fleet audit and the `### Automated Remediation Results` block, while individual actionable proactive fixes continue creating dedicated `["ai-incident", "proactive-fix"]` GitHub tracking issues/PRs.

## Gate 1: normative review

| Check | Status | Evidence / required correction |
| --- | --- | --- |
| Codebase grounding | PASS | Grounded in `internal/webhook/daily_report.go`, `internal/webhook/daily_report_test.go`, `internal/db/memos.go`, `README.md`, and `AGENTS.md`. |
| Attributable intent and explicit scope | PASS | Direct quote `"remove that github daily issue, it is not for report"` mapped to `REQ-029`; `Non-goals` preserve Memos `#DailyFleetCheck`, proactive `ai-incident` / `proactive-fix` tracking issues, and historical GitHub issues unless user requests cleanup. |
| Observable requirements and meaningful acceptance criteria | PASS | `REQ-029` uses EARS `WHEN`/`IF ... THEN` statements with falsifiable `AC-029-1` (Memos delivery + `proactive-fix` labels + zero daily report issues) and `AC-029-2` (zero issues on `HEALTHY` or `ERROR` + Memos error resilience). |
| Assumptions and owner-deferred questions are explicit | PASS | Assumption on historical open GitHub `daily-report` issues is stated explicitly; zero `[PROVISIONAL]` items exist. |
| Architectural rationale respects requirements | PASS | Single-path architectural rationale explains Memos-first consolidation without introducing new dependencies or violating `NO_KUBECTL_MUTATION`. |
| IDs, references, and revision route are consistent | PASS | Parent `2026-11-23-autonomous-remediation-and-proactive-audit` has `First entered In-Testing: 2026-11-23`, so linked delta `2026-11-25-daily-report-memos-only` mints `REQ-029` and synchronizes `INDEX.md`. |
