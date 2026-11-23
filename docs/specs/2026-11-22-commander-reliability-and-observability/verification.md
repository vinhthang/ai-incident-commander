# Verification: Commander Reliability, Signal Hygiene & Observability Delta

Governing specification: [spec.md](./spec.md)

## Planned checks

### AC-018-1 (REQ-018)

- Method: Run `rtk test go test ./internal/metrics/... ./internal/state/... ./internal/webhook/...` verifying Prometheus exposition on `GET /metrics` and TTL pruning of `COOLING_DOWN` / `resolvedDebounce` entries.
- Expected outcome: `GET /metrics` returns `200 OK` with expected Prometheus metric names, labels, buckets, and live `commander_queue_depth`; expired state entries are pruned while active locks remain intact.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-018-2 (REQ-018)

- Method: Run `rtk test go test ./internal/webhook/... ./internal/metrics/...` (`TestHandleWebhook_MethodAndMaxBodySize`, `TestHandleMetrics_PrometheusExpositionAndMethodCheck`).
- Expected outcome: Non-`POST` on `/webhook` and non-`GET` on `/metrics` return `405`; `/webhook` payload > `2 MiB` returns `413 Request Entity Too Large`.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-019-1 (REQ-019)

- Method: Run `rtk test go test ./internal/github/... ./internal/incident/... ./internal/k8s/... ./internal/db/... ./internal/webhook/...` (`TestIsIssueOpen`, `TestReconcileClosedClusterIssue`, `TestFetchVictoriaLogsContext`, `TestFormatAppIncidentMemosNote`, `TestPublishOpenAppIncident`).
- Expected outcome: Closed GitHub issues transition `cluster_incidents` to `resolved`, VictoriaLogs returns trace/service log context, and `open` `apps` incidents publish to Memos (`#AppIncident`) and deduplicated GitHub issues.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-019-2 (REQ-019)

- Method: Run `rtk test go test ./internal/incident/... ./internal/k8s/... ./internal/webhook/...` covering GitHub API errors, unreachable/empty `VICTORIALOGS_URL`, and `"ignored"` `apps` incidents.
- Expected outcome: `IsIssueOpen` errors conservatively keep duplicate suppression, `FetchVictoriaLogsContext` returns `""` cleanly on failure/empty URL, and `"ignored"` app incidents do not publish to Memos or GitHub.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-020-1 (REQ-020)

- Method: Run `rtk test go test ./internal/prompt/... ./internal/minion/...` (`TestRenderPrompts_PreservesUnescapedDiffsAndTelemetry`, `TestStartAgyWorker_Concurrency`).
- Expected outcome: All 5 prompt templates preserve `<`, `>`, `&`, `"`, `'` verbatim without HTML escaping, and normal `AgyQueue` execution runs sequentially and records metrics.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-020-2 (REQ-020)

- Method: Run `rtk test go test ./internal/minion/...` (`TestEnqueueAgyJob_RespectsCancelledContext`, `TestExecuteAgy_SkipsExpiredJobWithoutSpawningProcess`).
- Expected outcome: Cancelled/expired `ctx` unblocks `EnqueueAgyJob` immediately, and expired `job.Ctx` in `executeAgy` skips `syncAgyRuntimeDir` and `/usr/local/bin/agy`, recording `skipped_expired`.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-021-1 (REQ-021)

- Method: Run `rtk test go test ./internal/workspace/... ./internal/github/...` (`TestResetToMain_CleansDirtyTrackedAndUntrackedFiles`, `TestVerifyBranchPushed`, `TestGetPullRequestDiff_ChecksStatusCodeAndRespectsContext`).
- Expected outcome: `ResetToMain` cleanly resets dirty tracked and untracked files back to `origin/main`, `VerifyBranchPushed` succeeds when clean and pushed, and `internal/github` propagates caller `ctx`.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-021-2 (REQ-021)

- Method: Run `rtk test go test ./internal/workspace/... ./internal/github/...` and inspect `remediateClusterIssue` checks on empty `localDiff` and `VerifyBranchPushed` errors.
- Expected outcome: Dirty tree, unpushed branch, or SHA mismatch fails `VerifyBranchPushed`, `remediateClusterIssue` loops back to `Fixer` on empty/unpushed work, and non-`200` / cancelled `GetPullRequestDiff` returns an error.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-022-1 (REQ-022)

- Method: Run `rtk test go test ./internal/k8s/...` (`TestCollectDailyFleetSnapshot_IgnoresSelfTriggerSupersededCronJobsAndOldRestarts`).
- Expected outcome: `ai-commander-daily-report` trigger pods, superseded historical failed `CronJob` runs, and pods with restarts > `24h` ago are omitted from unhealthy/failed sections while standalone and latest-run failures still appear.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-022-2 (REQ-022)

- Method: Run `rtk test go test ./internal/k8s/...` (`TestCollectDailyFleetSnapshot_FlagsUnhealthyPodsAndEmptyEndpoints`, recent-restart subcases).
- Expected outcome: Pods restarted within `24h` (`lastState.terminated.finishedAt` / `state.running.startedAt`), `NotReady`, `Waiting`, or non-`Running` remain flagged in `Unhealthy or Restarting Pods`.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-023-1 (REQ-023)

- Method: Inspect `.gitignore`, `git ls-files`, `README.md`, and `AGENTS.md` after cleanup and doc updates.
- Expected outcome: `/commander` is gitignored, `commander` and all 8 `patch-*.py` files are untracked, and `README.md` / `AGENTS.md` accurately cover `/metrics`, `VICTORIALOGS_URL`, `apps` dual-sink, reconciliation, 24h snapshot rules, and updated gotchas/`REQ-024+`.
- Execution: [Run 1](#run-1--2026-11-22)

### AC-023-2 (REQ-023)

- Method: Run `git ls-files` and `prepare_code_review.py` + inspect `README.md` / `AGENTS.md` for completeness and absence of fixed gotcha claims.
- Expected outcome: Zero tracked legacy artifacts remain and zero stale gotcha claims or missing endpoints/env vars appear in `README.md` or `AGENTS.md`.
- Execution: [Run 1](#run-1--2026-11-22)

## Execution evidence

### Run 1 — 2026-11-22

- Checked: REQ-018..REQ-023 / AC-018-1..AC-023-2 (working tree above `c65e4ce`)
- Command: `rtk proxy go test -v -count=1 ./... && rtk go vet ./... && rtk env CGO_ENABLED=0 go build -o bin/incident-commander ./cmd/commander`, `check_boundaries.py`, `mutation_runner.py`, and `prepare_code_review.py` (exit code: `0`)
- Output excerpt:
  ```text
  ok  vinhthang.dev/ai-incident-commander/internal/db        0.012s
  ok  vinhthang.dev/ai-incident-commander/internal/github    0.062s
  ok  vinhthang.dev/ai-incident-commander/internal/incident  0.008s
  ok  vinhthang.dev/ai-incident-commander/internal/k8s       0.244s (initial full run)
  ok  vinhthang.dev/ai-incident-commander/internal/metrics   0.009s (initial full run)
  ok  vinhthang.dev/ai-incident-commander/internal/minion    0.213s (initial full run)
  ok  vinhthang.dev/ai-incident-commander/internal/prompt    0.009s
  ok  vinhthang.dev/ai-incident-commander/internal/state     0.079s
  ok  vinhthang.dev/ai-incident-commander/internal/webhook   0.026s
  ok  vinhthang.dev/ai-incident-commander/internal/workspace 0.339s
  Arch Guard: Zero forbidden cross-boundary imports; Zero circular dependencies.
  Gremlins efficacy: prompt 100%, db 100%, webhook 93.33%, incident 90.48%, workspace 90.48%, metrics 84.62%, minion 84.38%, github 76.92% (100% on new hunks), k8s 63.16%, state 50.00%.
  prepare_code_review.py: zero gofmt, git diff --check, or go vet issues.
  ```
- Result: AC-018-1 `PASS`, AC-018-2 `PASS`, AC-019-1 `PASS`, AC-019-2 `PASS`, AC-020-1 `PASS`, AC-020-2 `PASS`, AC-021-1 `PASS`, AC-021-2 `PASS`, AC-022-1 `PASS`, AC-022-2 `PASS`, AC-023-1 `PASS`, AC-023-2 `PASS` — Full Go test suite, `go vet`, static binary build, `arch-guard`, `mutation-guard`, and `code-review` linters passed (`exit 0`).

## Real-world observations

No real-world confirmation has been recorded.
