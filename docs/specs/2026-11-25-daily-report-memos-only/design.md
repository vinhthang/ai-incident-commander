# Design: Memos-Only Daily Fleet Reports Delta

Sources: [spec.md](./spec.md), [decisions.md](./decisions.md)

## Components

| Requirement / AC | Path and symbol | Responsibility / change |
| --- | --- | --- |
| REQ-029 / AC-029-1, AC-029-2 | `internal/webhook/daily_report.go` (`deliverDailyMemosNote`, `executeDailyReport`, `dispatchSingleProactiveFix`) | Remove `[Daily Fleet Check]` GitHub issue creation/closing on `HEALTHY`, `ATTENTION NEEDED`, and `ERROR` paths; deliver report + appended proactive results solely to Memos (`#DailyFleetCheck`); label proactive fix issues `["ai-incident", "proactive-fix"]`. |
| REQ-029 / AC-029-1, AC-029-2 | `internal/webhook/daily_report_test.go` | Enforce zero `[Daily Fleet Check]` issues and `"daily-report"` labels across healthy, error, and proactive dispatch runs; verify consolidated `#DailyFleetCheck` Memos note. |
| REQ-029 / AC-029-1 | `README.md`, `AGENTS.md` | Document Memos-only `#DailyFleetCheck` reporting and reserve `REQ-030+`. |

## Interfaces and flow

```text
executeDailyReport(ctx)
  |-- err != nil -> metrics("error") -> deliverDailyMemosNote("ERROR", err + snapshot) -> return (0 GitHub issues)
  |-- err == nil -> ParseDailyProactiveActions(report) -> cleanReport + actions
       |-- proactive enabled & actions > 0 -> dispatchDailyProactiveFixes (issues labeled ["ai-incident", "proactive-fix"])
       |     -> append "### Automated Remediation Results" to finalReport
       -> deliverDailyMemosNote(statusLabel, finalReport) (#DailyFleetCheck, 0 GitHub report issues)
```

## Boundaries and recovery

- Empty `MEMOS_DB_DSN` or `memosInsertFn` errors log a warning via `deliverDailyMemosNote` without panicking or aborting proactive fix tracking issues.
- Disabling `ENABLE_DAILY_PROACTIVE_FIX=false` or finding zero `PROACTIVE_ACTION` items skips dispatch cleanly and archives `cleanReport` to Memos with zero GitHub `POST /issues` traffic.

## Dependencies and unresolved design work

None.
