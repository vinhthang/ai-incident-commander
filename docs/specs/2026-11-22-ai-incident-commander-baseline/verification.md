# Verification: AI Incident Commander Baseline System Specification

Governing specification: [spec.md](./spec.md)

## Planned checks

### AC-011-1 (REQ-011)

- Method: Run `go test -v -count=1 ./internal/webhook/...` (`TestHandleWebhook_BearerAuth`, `TestHandleHealthz`, `TestHandleDailyReport_AuthAndMethod`) and inspect `cmd/commander/main.go`.
- Expected outcome: `/webhook`, `/daily-report`, and `/healthz` return expected `200`/`202` JSON responses and graceful shutdown + OTel initialization compile and pass.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-011-2 (REQ-011)

- Method: Run `go test -v -count=1 ./internal/webhook/...` and inspect `HandleHealthz`, `HandleWebhook`, and `HandleDailyReport` boundary branches.
- Expected outcome: Missing/invalid Bearer token returns `401`, wrong HTTP method returns `405`, and DB ping error yields HTTP `503` with `status: "degraded"`.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-012-1 (REQ-012)

- Method: Run `go test -v -count=1 ./internal/incident/... ./internal/github/... ./internal/webhook/...` (`TestComputeAlertIdentity_DistinctTargetsDoNotCollideOnLock`, `TestIsApplicationErrorAlert`, `TestExtractTargetResource`, `TestFindOpenIssueForTarget`, `TestFindOpenPRForTarget`).
- Expected outcome: Namespace/pod priority, scraper label exclusion, `apps` routing, workload-base GitHub Issue/PR lookup, and `resolved` issue closure behaviors verify cleanly.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-012-2 (REQ-012)

- Method: Run `go test -v -count=1 ./internal/incident/... ./internal/state/...` (`TestDatabaseNilSafety`, `TestIncidentLockAcquisition`, `TestAcquireResolveLock_RestartSafeAndDebounced`).
- Expected outcome: `db.DB == nil` returns safe non-panicking defaults, concurrent firing alerts drop under lock/cooldown, and duplicate `resolved` events within `1m` debounce cleanly.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-013-1 (REQ-013)

- Method: Run `go test -v -count=1 ./internal/k8s/... ./internal/state/...` (`TestWaitForSoak_CompletesFullWindowWhenNotResolved` and telemetry bundle tests).
- Expected outcome: Full soak elapses when unresolved and `FetchDiagnosticBundle` captures HTTP/TLS probes, pod termination status, container logs, workload specs, and Helm chart/values diagnostics.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-013-2 (REQ-013)

- Method: Run `go test -v -count=1 ./internal/state/... ./internal/incident/... ./internal/k8s/...` (`TestWaitForSoak_CancelsEarlyOnResolve`, `TestIsEphemeralNodeOutage`, workload status tests).
- Expected outcome: Early `resolved` cancels soak immediately, active rollouts / recovered workloads suppress triage, and `k3s-worker-*` CNI/NodeNotReady alerts are suppressed.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-014-1 (REQ-014)

- Method: Run `go test -v -count=1 ./internal/minion/... ./internal/prompt/...` (`queue_test.go`, `triage_test.go`, `prompt_test.go`).
- Expected outcome: Single-worker queue args/auth isolation, embedded/ConfigMap prompt rendering, and `ParseTriageDecision` verdicts (`IGNORE`, `ESCALATE_HUMAN`, `HIGH`-confidence `FIX_VIA_PR`) pass.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-014-2 (REQ-014)

- Method: Run `go test -v -count=1 ./internal/minion/...` (`TestParseTriageDecision` non-HIGH/ambiguous cases) and inspect `RunDiagnosticTriage` error handling in `internal/webhook/handler.go`.
- Expected outcome: `FIX_VIA_PR` without `HIGH` confidence and unrecognized trailing lines downgrade to `ESCALATE_HUMAN`, and triage `Err != nil` halts without creating an issue.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-015-1 (REQ-015)

- Method: Run `go test -v -count=1 ./internal/workspace/... ./internal/github/... ./internal/minion/...` (`TestWorkspaceLock_SerializesConcurrentAccess`, `TestEvaluateCheckRuns`, `TestWaitForPRChecks`, `reviewer` verdict tests) and inspect `remediateClusterIssue`.
- Expected outcome: Workspace mutex serializes git mutations, `SeniorFixer` and `Reviewer` verdicts parse cleanly, CI check polling gates completion, and `AUTO_MERGE=false` assigns `GITHUB_OWNER` instead of merging.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-015-2 (REQ-015)

- Method: Run `go test -v -count=1 ./internal/workspace/... ./internal/github/...` and inspect `CreateAndCheckoutBranch` and `remediateClusterIssue` retry exhaustion paths.
- Expected outcome: Protected branches (`main`, `master`, empty) are rejected, CI check failure returns failure summary to `Fixer`, and retry exhaustion/timeout assigns human reviewers without merging.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-016-1 (REQ-016)

- Method: Run `go test -v -count=1 ./internal/webhook/... ./internal/k8s/... ./internal/minion/... ./internal/db/...` (`TestHandleDailyReport_AuthAndMethod`, `daily_check_test.go`, `daily_report_test.go`, `TestFormatDailyMemosNote`).
- Expected outcome: Accepted `/daily-report` triggers read-only fleet snapshot + Memos note formatting + `HEALTH_STATUS` parsing cleanly.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-016-2 (REQ-016)

- Method: Run `go test -v -count=1 ./internal/webhook/... ./internal/minion/...` (`TestHandleDailyReport_AuthAndMethod` `409 Conflict` check, `ParseDailyHealthStatus` conservative fallback) and inspect `executeDailyReport` error branch.
- Expected outcome: Concurrent `/daily-report` returns `409 Conflict`, unrecognized audit output returns `ATTENTION_NEEDED`, and `RunDailyReport` failure files an `(ERROR)` issue assigned to `GITHUB_OWNER`.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-017-1 (REQ-017)

- Method: Inspect generated `README.md` and `AGENTS.md` against `cmd/commander/main.go`, `internal/config/config.go`, `.github/workflows/ci-cd.yml`, and all internal packages.
- Expected outcome: Both documents list accurate routes (`/webhook`, `/daily-report`, `/healthz`), all configuration env vars, package responsibilities, test/lint/spec commands, and known gotchas.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-017-2 (REQ-017)

- Method: Inspect `README.md` and `AGENTS.md` for absence of obsolete claims (`Rollback Minion` active pipeline, direct-to-`main` automated remediation, Gemini 3.5 Flash REST).
- Expected outcome: Zero obsolete v0 prototype claims appear as current system behavior in `README.md` or `AGENTS.md`.
- Execution: [Run 1](#run-1--2026-11-22)

## Execution evidence

### Run 1 — 2026-11-22

- Checked: REQ-011..REQ-017 / AC-011-1..AC-017-2 (`c65e4ce`)
- Command: `go test -v -count=1 ./...`, `go test -count=1 ./... && go vet ./...`, and Python inspection of `README.md` and `AGENTS.md` (exit code: `0`)
- Output excerpt:
  ```text
  ?   	vinhthang.dev/ai-incident-commander/cmd/commander	[no test files]
  ?   	vinhthang.dev/ai-incident-commander/internal/config	[no test files]
  ok  	vinhthang.dev/ai-incident-commander/internal/db	0.011s
  ok  	vinhthang.dev/ai-incident-commander/internal/github	0.060s
  ok  	vinhthang.dev/ai-incident-commander/internal/incident	0.010s
  ok  	vinhthang.dev/ai-incident-commander/internal/k8s	0.315s
  ok  	vinhthang.dev/ai-incident-commander/internal/minion	0.173s
  ok  	vinhthang.dev/ai-incident-commander/internal/prompt	0.011s
  ok  	vinhthang.dev/ai-incident-commander/internal/state	0.080s
  ok  	vinhthang.dev/ai-incident-commander/internal/webhook	0.017s
  ok  	vinhthang.dev/ai-incident-commander/internal/workspace	0.082s
  DOCS_VERIFIED_OK
  ```
- Result: AC-011-1 `PASS`, AC-011-2 `PASS`, AC-012-1 `PASS`, AC-012-2 `PASS`, AC-013-1 `PASS`, AC-013-2 `PASS`, AC-014-1 `PASS`, AC-014-2 `PASS`, AC-015-1 `PASS`, AC-015-2 `PASS`, AC-016-1 `PASS`, AC-016-2 `PASS`, AC-017-1 `PASS`, AC-017-2 `PASS` — All 9 tested Go packages and `go vet ./...` passed uncached, and `README.md` + `AGENTS.md` passed content/invariant verification (`DOCS_VERIFIED_OK`); live cluster/Postgres/`agy` end-to-end tests remain unit/mocked locally.

## Real-world observations

No real-world confirmation has been recorded.
