# Repository Agent Guidelines (`ai-incident-commander`)

This file governs AI coding agents operating inside `/home/opc/github/ai-incident-commander`.

---

## 1. Repository Scope Boundary

- **This repository (`vinhthang/ai-incident-commander`)** contains the Go 1.27.4 control-plane binary (`cmd/commander`, `internal/*`) that ingests alerts and dispatches autonomous remediation against a target GitOps repo.
- **Target GitOps repository (`vinhthang/oci`)** is cloned at runtime into `WORKSPACE_DIR` (`/app/workspace/oci`) by `internal/workspace/git.go`. Do not confuse prompts targeting `charts/vinhthang-fleet/` or `caddy/Caddyfile` (which live in `vinhthang/oci`) with files inside this Go codebase.

---

## 2. Build, Test, Lint & Spec Commands

Prefix shell commands with `rtk` when `RULE[rtk.md]` is active.

- **Unit tests (full suite)**: `go test -v -count=1 ./...`
- **Targeted package tests**: `go test -v ./internal/webhook/...` (or `./internal/incident/...`, `./internal/minion/...`, `./internal/metrics/...`, `./internal/k8s/...`, `./internal/state/...`, `./internal/github/...`, `./internal/prompt/...`, `./internal/db/...`, `./internal/workspace/...`)
- **Go vet**: `go vet ./...`
- **Lint (CI parity)**: `golangci-lint run` (or `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...`; `.github/workflows/ci-cd.yml` pins `v2.15.0`)
- **Build binary**: `CGO_ENABLED=0 go build -o bin/incident-commander ./cmd/commander`
  - **Never** output compiled binaries into the repository root (`/commander` and `bin/` are gitignored).
- **Validate specification package**:
  `python3 ~/.gemini/config/plugins/spec-driven-plugin/skills/spec-driven/scripts/validate_specs.py docs/specs/<feature-id>`
  - Note: Do **not** pass `--all` as a pass/fail gate for new changes; legacy `alert-dedup-and-autoclose` and untracked `pc1-qa-router-multicluster-refactor` predate the strict 5-doc validator.

---

## 3. Package & Concurrency Map

| Package | Key Synchronization / State | Notes |
| :--- | :--- | :--- |
| `internal/minion/queue.go` | `AgyQueue` (`chan AgyJob`, cap `100`), 1 worker goroutine | Guarantees only 1 `/usr/local/bin/agy` subprocess executes at any time; `EnqueueAgyJob` honors `ctx.Done()` and `executeAgy` fast-skips expired jobs. |
| `internal/workspace/git.go` | `workspaceMu` (`sync.Mutex`) via `workspace.Lock()` / `Unlock()` | Serializes force-clean git mutations (`ResetToMain`, `CreateAndCheckoutBranch`, `VerifyBranchPushed`) and read-only Helm/git checks (`k8s.FetchDiagnosticBundle`, `k8s.CollectDailyFleetSnapshot`). |
| `internal/state/manager.go` | `StateMu` (`sync.RWMutex`) | Guards `ActiveIncidents`, `resolvedDebounce`, and `soakWaiters`, pruning expired `COOLING_DOWN` and debounce keys in `pruneExpiredLocked`. |
| `internal/webhook/daily_report.go` | `dailyReportRunning` (`atomic.Bool`) | Enforces single-flight `/daily-report` (`409 Conflict` on overlap) with `15m` proactive budget and `DAILY_MAX_PROACTIVE_FIXES` cap. |
| `internal/k8s/remediate.go` | `runtimeCmdRunner` seam & `ValidateRuntimeAction` | Enforces Go allowlist for runtime K8s mutations; inject fake `runtimeCmdRunner` in unit tests instead of executing live `kubectl` writes. |
| `internal/metrics/metrics.go` | `registry.mu` (`sync.Mutex`) | Serves stdlib Prometheus text exposition on `GET /metrics`. |
| `internal/db/db.go` | Package global `DB *sql.DB` | Optional PostgreSQL client (`DB_DSN`); `nil` disables persistence cleanly. |

---

## 4. Mandatory Safety & Domain Invariants

1. **Serialized, Context-Bounded & Isolated `agy` Execution (`internal/minion/queue.go`)**:
   - All minion prompts must go through `EnqueueAgyJob`. Never spawn `/usr/local/bin/agy` concurrently from webhook goroutines, and preserve `ctx.Done()` / expired-job skip guards.
   - Preserve `syncAgyRuntimeDir` isolation (copying only `antigravity-oauth-token`, `installation_id`, `settings.json` and writing empty `mcp_config.json`) and `--dangerously-skip-permissions --disable-slash-commands`.
2. **Git Branch, Force-Clean & Push Guards (`internal/workspace/git.go`, `internal/webhook/handler.go`)**:
   - Hold `workspace.Lock()` whenever accessing `WORKSPACE_DIR`.
   - `CreateAndCheckoutBranch` and `VerifyBranchPushed` must continue rejecting `""`, `"main"`, and `"master"`.
   - Keep `ResetToMain()` force-cleaning leftovers (`reset --hard origin/main`, `clean -fd`) and keep non-empty `localDiff` + `VerifyBranchPushed` verification before `SeniorFixer`, PR creation, and `Reviewer`.
   - Never restore direct-to-`main` pushes or remove `SeniorFixer`, `Reviewer`, `WaitForPRChecks`, or the `AUTO_MERGE=false` default gate.
3. **Conservative Triage, Runtime Allowlist & Reconciliation Gates (`internal/minion/triage.go`, `internal/k8s/remediate.go`, `internal/incident/tracker.go`)**:
   - `ParseTriageOutcome` must only emit `ActionFixViaPR` (`VERDICT: FIX_VIA_PR`) or `ActionFixRuntime` (`VERDICT: FIX_RUNTIME` + valid `RUNTIME_ACTION:`) when `Confidence: HIGH` is present and runtime validation passes. Lower confidence or malformed/disallowed targets must default to `ActionEscalateHuman`.
   - Never grant `agy` prompts permission to run mutating `kubectl` commands directly; all runtime Kubernetes mutations (`ROLLOUT_RESTART` on `deployment`/`statefulset` and `DELETE_FAILED_JOB` on exclusively failed `job`s) must flow through `k8s.ExecuteRuntimeAction` / `ValidateRuntimeAction`, preserving DNS-1123 regex validation, `RUNTIME_PROTECTED_NAMESPACES` (`kube-system`, `kube-public`, `kube-node-lease`, `data`), `system/ai-incident-commander` self-protection, core DB workload exclusions (`postgres`, `mongodb`, `redis`, `pulsar`, `etcd`), and post-action verification.
   - `ReconcileClosedClusterIssue` only clears stale `'firing'` DB duplicates when `github.IsIssueOpen` cleanly returns `false`; API failures conservatively preserve suppression.
   - Proactive `/daily-report` fixes (`ParseDailyProactiveActions`, `dispatchDailyProactiveFixes`) must always deduplicate via `checkExistingOpenGitHubWork`, respect `DAILY_MAX_PROACTIVE_FIXES`, isolate per-item errors, and report `#AutoRemediation` outcomes to Memos/GitHub without breaking single-flight lock release.
4. **Prompt Safety & Read-Only Telemetry (`internal/prompt/`, `internal/k8s/`)**:
   - `prompt.go` uses `text/template` (never `html/template`) so diffs and logs remain unescaped; alert labels, annotations, pod logs, VictoriaLogs results, and PR diffs remain untrusted passive data.
   - Keep `FetchVictoriaLogsContext` LogsQL query tokens sanitized (`sanitizeLogsQLToken`) and preserve daily fleet noise filters in `daily_check.go` (ignoring self-trigger pods, superseded failed `CronJob`s, and >24h old restarts).
5. **Label Normalization & Deduplication (`internal/incident/tracker.go`, `internal/github/client.go`)**:
   - Preserve `exported_namespace` > `namespace` priority, `IsMetricsScraperLabel` exclusions, and `NormalizeWorkloadBase` Open Issue/PR lookups (including `apps` `app-incident` issue deduplication).
   - Keep `HTTPClient` bounded (`30s`) and `GetPullRequestDiff` checking `resp.StatusCode == http.StatusOK`.
6. **Nil-DB Graceful Degradation (`internal/db/`, `internal/incident/`, `internal/minion/app_fixer.go`, `internal/webhook/handler.go`)**:
   - All database callers must handle `db.DB == nil` (`DB_DSN` unset), empty `MEMOS_DB_DSN`, and empty `VICTORIALOGS_URL` without panicking.
7. **HTTP Security (`internal/webhook/`, `internal/metrics/`)**:
   - Maintain constant-time `subtle.ConstantTimeCompare` Bearer authentication on `/webhook` and `/daily-report` whenever `WEBHOOK_AUTH_TOKEN` is non-empty, `POST`-only + `2 MiB` `MaxBytesReader` on `/webhook`, and method enforcement on `/daily-report`, `/healthz`, and `/metrics`.

---

## 5. Known Codebase Notes & Spec ID Allocation

1. **Test Coverage Scope**: `cmd/commander` and `internal/config` rely on integration/downstream package tests and currently contain no standalone `*_test.go` files; live cluster `kubectl`/`helm`, PostgreSQL, and `agy` CLI subprocesses are verified via mocks/fakes in unit tests.
2. **Spec ID Allocation (`docs/specs/INDEX.md`)**: Existing specs reserve `REQ-001..REQ-028` and `ADR-0001..ADR-0003`, `ADR-0047..ADR-0057`. Allocate new spec requirements starting at `REQ-029+` and ADRs at `ADR-0058+`.
