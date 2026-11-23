# Verification: Autonomous Runtime Remediation and Proactive Daily Audit Dispatch

Governing specification: [spec.md](./spec.md)

## Planned checks

### AC-024-1 (REQ-024)

- Method: Run `rtk test go test ./internal/k8s/...` (`TestValidateAndExecuteRuntimeAction_RolloutRestart`, `TestValidateAndExecuteRuntimeAction_DeleteFailedJob`) using an injectable fake `runtimeCmdRunner`.
- Expected outcome: Valid `ROLLOUT_RESTART` runs restart + `rollout status` verification and returns `Executed=true, Verified=true`; valid `DELETE_FAILED_JOB` confirms failed-only JSON status, deletes the Job, and verifies absence.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-024-2 (REQ-024)

- Method: Run `rtk test go test ./internal/k8s/...` (`TestValidateRuntimeAction_BlocksProtectedNamespacesDBsSelfAndInvalidNames`, `TestExecuteRuntimeAction_HandlesRBACForbiddenAndActiveJobs`).
- Expected outcome: `kube-system`, `data`, `system/ai-incident-commander`, DB names (`postgres`, `mongodb`, `redis`, `pulsar`), non-DNS-1123 names, active/succeeded Jobs, and `ENABLE_RUNTIME_REMEDIATION=false` return `Executed=false` with `DeniedReason`; RBAC `Forbidden` sets `RBACDenied=true`.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-025-1 (REQ-025)

- Method: Run `rtk test go test ./internal/minion/... ./internal/webhook/...` (`TestParseTriageDecision_FixRuntime`, `TestProcessAlert_RuntimeFixSuccessAutoClosesAndLogsMemos`).
- Expected outcome: `VERDICT: FIX_RUNTIME` with `Confidence: HIGH` and valid `RUNTIME_ACTION:` parses to `ActionFixRuntime`, executes `ExecuteRuntimeAction`, resolves `cluster_incidents`, auto-closes the GitHub issue, and records a `#AutoRemediation` Memos note.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-025-2 (REQ-025)

- Method: Run `rtk test go test ./internal/minion/... ./internal/webhook/...` covering missing `RUNTIME_ACTION`, non-`HIGH` confidence, protected target downgrade, and `ExecuteRuntimeAction` failure/`RBACDenied`.
- Expected outcome: Invalid or low-confidence `FIX_RUNTIME` downgrades to `ActionEscalateHuman`; failed/RBAC-denied runtime execution leaves the GitHub issue open assigned to `GITHUB_OWNER`.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-026-1 (REQ-026)

- Method: Run `rtk test go test ./internal/minion/... ./internal/webhook/...` (`TestParseDailyProactiveActions`, `TestExecuteDailyReport_DispatchesProactiveFixesWithinCap`).
- Expected outcome: `PROACTIVE_ACTION:` directives are parsed and stripped from the clean report, duplicates skipped via GitHub lookup, dispatches capped by `DAILY_MAX_PROACTIVE_FIXES`, `FIX_VIA_PR` routed through `remediateClusterIssue` (`AUTO_MERGE` respected), and a consolidated summary comment appended to the daily report issue.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-026-2 (REQ-026)

- Method: Run `rtk test go test ./internal/webhook/...` (`TestExecuteDailyReport_DisabledCapIsolationAndDuplicates`).
- Expected outcome: `ENABLE_DAILY_PROACTIVE_FIX=false` or existing open Issue/PR prevents duplicate remediation; a single failed candidate does not abort subsequent candidates or leave `dailyReportRunning` stuck.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-027-1 (REQ-027)

- Method: Run `rtk test go test ./internal/db/... ./internal/metrics/... ./internal/webhook/...` (`TestFormatRemediationMemosNote`, `TestHandleMetrics_ProactiveAndRuntimeOutcomes`, `TestReportRemediationOutcome`).
- Expected outcome: Memos receives `#AutoRemediation` notes when `MEMOS_DB_DSN` is set, GitHub issues follow verified-runtime auto-close vs PR/failure assignment rules, and `/metrics` exports `commander_proactive_fixes_total` + updated `commander_remediation_outcomes_total`.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-027-2 (REQ-027)

- Method: Run `rtk test go test ./internal/db/... ./internal/webhook/...` with empty `MEMOS_DB_DSN`, failing Memos DB insert, and `AUTO_MERGE=false` PR completion.
- Expected outcome: Empty/failing Memos writes never panic or block GitHub updates; `AUTO_MERGE=false` leaves passing PRs unmerged and assigned to `GITHUB_OWNER`.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-028-1 (REQ-028)

- Method: Inspect `README.md` and `AGENTS.md` alongside `internal/config/config.go`, `internal/k8s/remediate.go`, and `internal/metrics/metrics.go`.
- Expected outcome: `README.md` documents 4-way triage, proactive daily fixes, `#AutoRemediation`, all 5 new env vars, `commander_proactive_fixes_total`, and least-privilege `ClusterRole` YAML; `AGENTS.md` documents runtime allowlist invariants and reserves `REQ-029+` / `ADR-0058+`.
- Execution: [Run 1](#run-1--2026-11-23)

### AC-028-2 (REQ-028)

- Method: Cross-check env var names/defaults, protected namespace/DB exclusions, and RBAC verbs between `internal/config/config.go` / `internal/k8s/remediate.go` and `README.md` / `AGENTS.md`, plus `golangci-lint run ./...` and `go vet ./...`.
- Expected outcome: Zero discrepancy between documented env vars/allowlist/RBAC snippet and implemented Go behavior; linters report `0 issues`.
- Execution: [Run 1](#run-1--2026-11-23)

## Execution evidence

### Run 1 — 2026-11-23

- Checked: REQ-024..REQ-028 / AC-024-1..AC-028-2 (working tree above `c65e4ce`)
- Command: `rtk proxy go test -count=1 ./... && rtk go vet ./... && rtk proxy golangci-lint run ./...`, multi-arch `CGO_ENABLED=0` `go build` (`amd64`/`arm64`), `check_boundaries.py --allow-empty`, `prepare_code_review.py`, and `mutation_runner.py` (exit code: `0`)
- Output excerpt:
  ```text
  ok  vinhthang.dev/ai-incident-commander/internal/db        0.013s
  ok  vinhthang.dev/ai-incident-commander/internal/github    0.059s
  ok  vinhthang.dev/ai-incident-commander/internal/incident  0.010s
  ok  vinhthang.dev/ai-incident-commander/internal/k8s       0.226s
  ok  vinhthang.dev/ai-incident-commander/internal/metrics   0.010s
  ok  vinhthang.dev/ai-incident-commander/internal/minion    0.204s
  ok  vinhthang.dev/ai-incident-commander/internal/prompt    0.008s
  ok  vinhthang.dev/ai-incident-commander/internal/state     0.076s
  ok  vinhthang.dev/ai-incident-commander/internal/webhook   0.036s
  ok  vinhthang.dev/ai-incident-commander/internal/workspace 0.342s
  golangci-lint run ./cmd/commander ./internal/... -> 0 issues.
  check_boundaries.py --allow-empty -> CLEAN & PASSING
  Gremlins efficacy: db 100.00%, minion 87.23%, webhook 75.61% (remediation_report.go 72.0%), metrics 73.68% (100% covered), k8s 66.79% (remediate.go 96.7%).
  prepare_code_review.py: zero gofmt, git diff --check, go vet, or golangci-lint issues.
  ```
- Result: AC-024-1 `PASS`, AC-024-2 `PASS`, AC-025-1 `PASS`, AC-025-2 `PASS`, AC-026-1 `PASS`, AC-026-2 `PASS`, AC-027-1 `PASS`, AC-027-2 `PASS`, AC-028-1 `PASS`, AC-028-2 `PASS` — All unit tests (using fake `runtimeCmdRunner`, fake `memosInsertFn`, and `httptest.Server`), `go vet`, `golangci-lint`, multi-arch static builds, `arch-guard`, `mutation-guard`, and `code-review` checks passed (`exit 0`). Real-world cluster execution awaits image build and least-privilege `ClusterRole` update in `vinhthang/oci`.

## Real-world observations

No real-world confirmation has been recorded.
