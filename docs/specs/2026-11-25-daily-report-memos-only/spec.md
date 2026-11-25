# Memos-Only Daily Fleet Reports Delta

- Feature ID: 2026-11-25-daily-report-memos-only
- Type: enhancement
- Status: In-Testing
- First entered In-Testing: 2026-11-25
- Related specifications: Implemented: Changes [2026-11-23-autonomous-remediation-and-proactive-audit](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md)

## Problem and source intent

When inspecting daily reports on `vinhthang/oci` (e.g., `#351`), the user stated: `"remove that github daily issue, it is not for report"`. While `POST /daily-report` already archives full daily reports to Memos (`#DailyFleetCheck`), creating `[Daily Fleet Check]` GitHub issues added non-actionable report clutter to the `vinhthang/oci` issue tracker (which is meant for actionable incident triage and fix PRs).

## Repository grounding

- **Daily Report Handler**: `internal/webhook/daily_report.go` (`HandleDailyReport`, `executeDailyReport`, `deliverDailyMemosNote`, `dispatchDailyProactiveFixes`, `dispatchSingleProactiveFix`).
- **Memos Formatter**: `internal/db/memos.go` (`FormatDailyMemosNote`, `InsertMemosReport`).
- **Tests & Docs**: `internal/webhook/daily_report_test.go`, `README.md`, `AGENTS.md`.

## Non-goals

- Removing Memos `#DailyFleetCheck` persistence (`MEMOS_DB_DSN`).
- Removing GitHub tracking issues/PRs opened for individual actionable incidents or proactive remediation candidates (`ai-incident`, `proactive-fix`).
- Closing or deleting historical `daily-report` GitHub issues in `vinhthang/oci` automatically without explicit user approval.

## Requirements

### REQ-029: Memos-Only Daily Fleet Reports Without GitHub Report Issues

- Changes: [2026-11-23-autonomous-remediation-and-proactive-audit/REQ-026](../2026-11-23-autonomous-remediation-and-proactive-audit/spec.md#req-026-proactive-remediation-dispatch-from-daily-fleet-audits)
- Source intent: Explicit user request (`"remove that github daily issue, it is not for report"`).
- Previous contract: `executeDailyReport` opened `[Daily Fleet Check] <date> (<HEALTHY|ATTENTION NEEDED|ERROR>)` GitHub issues on `vinhthang/oci`, appended consolidated `### Automated Remediation Results` comments to that daily issue, auto-closed `HEALTHY` issues, and labeled proactive tracking issues with `"daily-report"`.
- Statement: WHEN `POST /daily-report` completes a fleet audit (healthy, attention needed, or error), THE commander SHALL publish the full audit report (plus any appended automated remediation summary) solely to Memos (`#DailyFleetCheck`) without creating `[Daily Fleet Check]` GitHub issues, and SHALL label individual proactive fix tracking issues `ai-incident` and `proactive-fix`.
- AC-029-1 — Success: `executeDailyReport` strips `PROACTIVE_ACTION:` directives, dispatches bounded proactive fixes (creating tracking issues labeled `["ai-incident", "proactive-fix"]`), appends `### Automated Remediation Results` to `finalReport`, writes the `#DailyFleetCheck` note to Memos via `deliverDailyMemosNote`, and creates zero `[Daily Fleet Check]` GitHub issues.
- AC-029-2 — Boundary/failure: IF the daily fleet report is `HEALTHY`, OR `ATTENTION_NEEDED` with no proactive actions, OR `dailyReportEvaluator` returns an error, THEN zero GitHub `POST /issues` calls SHALL occur and any error report SHALL be delivered to Memos (`statusLabel="ERROR"`); IF `MEMOS_DB_DSN` is empty or `memosInsertFn` errors, THEN the handler SHALL log cleanly without panicking or blocking proactive dispatches.

## Assumptions and owner-deferred questions

- **Assumption 1 (Historical GitHub Cleanup)**: Existing historical open `[Daily Fleet Check]` issues (`#346`, `#344`, `#341`, `#340`, `#334`) in `vinhthang/oci` are left untouched unless the user explicitly authorizes closing them.

## Approvals and lifecycle history

- **2026-11-25 (`Planning -> In-Progress`)**: Delta package created and authorized directly by user request (`"remove that github daily issue, it is not for report"`). Starting TDD execution.
- **2026-11-25 (`In-Progress -> In-Testing`)**: Completed Task 1 with `go test -count=1 ./...`, `go vet ./...`, `golangci-lint run ./...`, multi-arch static builds, `prepare_code_review.py`, and `mutation-guard` passing ([Run 1](./verification.md#run-1--2026-11-25)); promoted to `In-Testing` awaiting real-world deployment verification.
