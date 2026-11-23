# Design: Autonomous Runtime Remediation and Proactive Daily Audit Dispatch

Sources: [spec.md](./spec.md), [decisions.md](./decisions.md)

## Components

| Requirement / AC | Path and symbol | Responsibility / change |
| --- | --- | --- |
| REQ-024 / AC-024-1, AC-024-2 | `internal/config/config.go` | Parse `ENABLE_RUNTIME_REMEDIATION` (`true`), `RUNTIME_PROTECTED_NAMESPACES` (`kube-system,kube-public,kube-node-lease,data`), `RUNTIME_VERIFY_TIMEOUT` (`90s`), `ENABLE_DAILY_PROACTIVE_FIX` (`true`), `DAILY_MAX_PROACTIVE_FIXES` (`3`, via new `ParseIntEnv` helper). |
| REQ-024 / AC-024-1, AC-024-2 | `internal/k8s/remediate.go` (`RuntimeAction`, `RuntimeActionResult`, `ValidateRuntimeAction`, `ExecuteRuntimeAction`) | Deterministic Go-only allowlist validator & executor (`ROLLOUT_RESTART` for `deployment`/`statefulset` + `rollout status` verification; `DELETE_FAILED_JOB` preflight failed-only JSON check + delete + absence check) using injectable `runtimeCmdRunner`. |
| REQ-025 / AC-025-1, AC-025-2 | `internal/prompt/defaults/triage.tmpl`, `internal/minion/triage.go` (`ActionFixRuntime`, `TriageResult.RuntimeAction`, `ParseTriageDecision`) | Add 4th verdict `VERDICT: FIX_RUNTIME` + `RUNTIME_ACTION:` parsing and fail-closed validation against `k8s.ValidateRuntimeAction` (`Confidence: HIGH` required). |
| REQ-026 / AC-026-1, AC-026-2 | `internal/prompt/defaults/daily_report.tmpl`, `internal/minion/daily_report.go` (`DailyProactiveAction`, `ParseDailyProactiveActions`) | Emit, extract, validate, and strip `PROACTIVE_ACTION:` directives from daily audit reports prior to publishing. |
| REQ-025, REQ-026, REQ-027 / AC-025-1..AC-027-2 | `internal/webhook/remediation_report.go`, `internal/webhook/handler.go`, `internal/webhook/daily_report.go` | Wire reactive `ActionFixRuntime` dispatch, bounded daily proactive loop (`dailyReportTimeout = 15m`, dedup, max cap), and unified Memos + GitHub outcome reporter. |
| REQ-027 / AC-027-1, AC-027-2 | `internal/db/memos.go` (`FormatRemediationMemosNote`), `internal/metrics/metrics.go` (`IncProactiveFix`) | Format `#AutoRemediation` notes for Memos and export `commander_proactive_fixes_total{mode="...",outcome="..."}` on `GET /metrics`. |
| REQ-028 / AC-028-1, AC-028-2 | `README.md`, `AGENTS.md` | Document 4-way triage, proactive daily fixes, `#AutoRemediation`, new env vars/metrics, least-privilege `ClusterRole` YAML for `vinhthang/oci`, runtime allowlist invariants, and reserve `REQ-029+` / `ADR-0058+`. |

## Interfaces and flow

### 1. Runtime Action Allowlist & Execution (`internal/k8s/remediate.go`)
- Wire types:
  - `RuntimeActionType`: `RuntimeRolloutRestart = "ROLLOUT_RESTART"`, `RuntimeDeleteFailedJob = "DELETE_FAILED_JOB"`.
  - `RuntimeAction`: `Type RuntimeActionType`, `Namespace string`, `Kind string` (`deployment` | `statefulset` | `job`), `Name string`, `Reason string`.
  - `RuntimeActionResult`: `Action RuntimeAction`, `Executed bool`, `Verified bool`, `RBACDenied bool`, `DeniedReason string`, `Output string`, `VerifyOutput string`, `Err error`.
- `ValidateRuntimeAction(act RuntimeAction) error`:
  1. Checks `config.EnableRuntimeRemediation` (error if `false`).
  2. Normalizes `Kind = strings.ToLower(act.Kind)` and validates `act.Namespace` and `act.Name` match strict DNS-1123 regex (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` <= 63 chars, blocking flags/paths/shell metacharacters).
  3. Checks `act.Namespace` is not in `config.RuntimeProtectedNamespaces` (`kube-system`, `kube-public`, `kube-node-lease`, `data`).
  4. Rejects self-restart (`Namespace == "system" && Kind == "deployment" && Name == "ai-incident-commander"`) and core DB substring/workload guards (`postgres`, `mongodb`, `redis`, `pulsar`, `etcd`).
  5. Enforces `(Type == ROLLOUT_RESTART && (Kind == "deployment" || Kind == "statefulset")) || (Type == DELETE_FAILED_JOB && Kind == "job")`.
- `ExecuteRuntimeAction(ctx context.Context, act RuntimeAction) RuntimeActionResult`:
  - Uses injectable `runtimeCmdRunner` (defaults to `runCmd`).
  - For `ROLLOUT_RESTART`: runs `kubectl -n <ns> rollout restart <kind>/<name>`, then `kubectl -n <ns> rollout status <kind>/<name> --timeout=<RuntimeVerifyTimeout>`.
  - For `DELETE_FAILED_JOB`: runs `kubectl -n <ns> get job <name> -o json`, verifies `status.failed > 0 && status.active == 0 && status.succeeded == 0`, runs `kubectl -n <ns> delete job <name>`, then confirms deletion (`get job` returns NotFound).

### 2. Triage & Proactive Action Wire Formats (`internal/minion/`)
- `triage.tmpl` / `ParseTriageDecision`:
  - When `VERDICT: FIX_RUNTIME` appears as the final line, parser requires `Confidence: HIGH` and a `RUNTIME_ACTION: <ROLLOUT_RESTART|DELETE_FAILED_JOB> <ns>/<kind>/<name>` line validated by `k8s.ValidateRuntimeAction`; otherwise it downgrades to `ActionEscalateHuman`.
- `daily_report.tmpl` / `ParseDailyProactiveActions(output string) (cleanReport string, actions []DailyProactiveAction)`:
  - Scans structured `PROACTIVE_ACTION:` lines placed before `HEALTH_STATUS:` (`PROACTIVE_ACTION: FIX_VIA_PR | <ns>/<target> | <diagnosis>` or `PROACTIVE_ACTION: FIX_RUNTIME | <ROLLOUT_RESTART|DELETE_FAILED_JOB> <ns>/<kind>/<name> | <diagnosis>`).
  - Strips valid/invalid `PROACTIVE_ACTION:` protocol lines from `cleanReport` so `#DailyFleetCheck` Memos notes and the daily GitHub Issue body stay clean, and preserves trailing `HEALTH_STATUS:` parsing (`ParseDailyHealthStatus` continues reading the clean report tail cleanly).

### 3. Control Flow & Reporting (`internal/webhook/`)
```text
POST /webhook -> processAlert -> RunDiagnosticTriage
  |-- ActionFixRuntime -> CreateIssue -> ExecuteRuntimeAction -> reportRemediationOutcome (Memos + CloseIssue if Verified / Assign if Failed)
  |-- ActionFixViaPR   -> CreateIssue -> remediateClusterIssue -> reportRemediationOutcome (Memos + Merged / AUTO_MERGE=false / Failed)

POST /daily-report (15m ctx) -> CollectSnapshot + RunDailyReport -> ParseDailyProactiveActions
  -> publish daily issue & #DailyFleetCheck Memos note
  -> loop up to DailyMaxProactiveFixes:
       checkExistingOpenGitHubWork -> skip duplicates
       |-- FIX_RUNTIME -> CreateIssue -> ExecuteRuntimeAction -> reportRemediationOutcome
       |-- FIX_VIA_PR  -> CreateIssue -> remediateClusterIssue  -> reportRemediationOutcome
  -> append consolidated "### Automated Remediation Results" comment on daily issue
```

## Boundaries and recovery

- **Disallowed runtime target or disabled flag**: `ValidateRuntimeAction` / `ParseTriageDecision` / `ParseDailyProactiveActions` rejects or downgrades cleanly without running any mutating `kubectl` command; `ExecuteRuntimeAction` returns `Executed=false` and `DeniedReason`.
- **RBAC `Forbidden` on live cluster**: If `vinhthang/oci` still only has read-only `ai-incident-commander-reader` permissions, `ExecuteRuntimeAction` sets `RBACDenied=true` and `metrics.IncRemediationOutcome("runtime_rbac_denied")`, leaves the GitHub issue open assigned to `GITHUB_OWNER`, and logs the outcome cleanly to Memos (`#AutoRemediation`).
- **Rollout timeout or Job still active**: `rollout status` timeout returns `Executed=true, Verified=false` and leaves the issue open for human inspection; active or succeeded Jobs fail preflight before `kubectl delete` is ever called.
- **Memos DB failure or unset `MEMOS_DB_DSN`**: Logged with a warning; GitHub Issue closure/assignment and Prometheus metric updates proceed unhindered.
- **Partial proactive failure**: Each proactive candidate executes inside its own bounded step so one failure does not abort subsequent candidates (up to `DAILY_MAX_PROACTIVE_FIXES`) and `defer dailyReportRunning.Store(false)` always releases the single-flight lock.

## Dependencies and unresolved design work

- Operator ClusterRole expansion in `vinhthang/oci` (`deployments/rollouts` write verb `patch`/`get`, `statefulsets` `patch`/`get`, `jobs` `get`/`delete`, excluding Secret/DB/Pod exec permissions) is an external deployment prerequisite documented in `README.md` per `REQ-028`. No Go design items remain unresolved.
