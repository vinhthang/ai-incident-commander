# Tasks: Autonomous Runtime Remediation and Proactive Daily Audit Dispatch

Sources: [spec.md](./spec.md), [design.md](./design.md)

## Task 1: Runtime

- [x] Implement runtime remediation env configuration (`ENABLE_RUNTIME_REMEDIATION`, `RUNTIME_PROTECTED_NAMESPACES`, `RUNTIME_VERIFY_TIMEOUT`, `ENABLE_DAILY_PROACTIVE_FIX`, `DAILY_MAX_PROACTIVE_FIXES`) and the Go-enforced allowlisted runtime engine (`ValidateRuntimeAction`, `ExecuteRuntimeAction`) with strict Red -> Green -> Refactor unit tests using a fake command runner.
- Covers: REQ-024 / AC-024-1, AC-024-2
- Files: internal/config/config.go, internal/k8s/remediate.go, internal/k8s/remediate_test.go
- Dependencies: None
- Blocked by: None
- Work: Write failing RED tests in `internal/k8s/remediate_test.go` covering `ROLLOUT_RESTART`, `DELETE_FAILED_JOB`, protected namespaces (`kube-system`, `data`), self-target (`system/ai-incident-commander`), core DB substrings, invalid DNS-1123 names, active/succeeded Jobs, disabled flag, and RBAC `Forbidden`; verify `rtk test go test ./internal/k8s/...` fails RED; implement `internal/config/config.go` vars/`ParseIntEnv` and `internal/k8s/remediate.go` to pass GREEN; refactor for clean error messages and zero duplication.
- Completion evidence: Implemented `internal/config/config.go` (`ParseIntEnv`, `ParseCSVEnv`, 5 env vars) and `internal/k8s/remediate.go` (`ParseRuntimeActionSpec`, `ValidateRuntimeAction`, `ExecuteRuntimeAction`); all unit tests in `internal/k8s/remediate_test.go` passed via fake `runtimeCmdRunner`.

## Task 2: Triage

- [x] Extend `triage.tmpl` and `ParseTriageDecision` with 4-way `VERDICT: FIX_RUNTIME` + `RUNTIME_ACTION:` parsing and fail-closed validation, and extend `daily_report.tmpl` and `ParseDailyProactiveActions` to emit/extract/strip bounded `PROACTIVE_ACTION:` directives while preserving `ParseDailyHealthStatus` compatibility.
- Covers: REQ-025 / AC-025-1, AC-025-2; REQ-026 / AC-026-1, AC-026-2
- Files: internal/prompt/defaults/triage.tmpl, internal/prompt/defaults/daily_report.tmpl, internal/minion/triage.go, internal/minion/triage_test.go, internal/minion/daily_report.go, internal/minion/daily_report_test.go
- Dependencies: Task 1
- Blocked by: None
- Work: Write failing RED tests in `internal/minion/triage_test.go` (valid `FIX_RUNTIME` + `HIGH`, missing `RUNTIME_ACTION`, non-`HIGH` downgrade, protected resource downgrade) and `internal/minion/daily_report_test.go` (`ParseDailyProactiveActions` for `FIX_VIA_PR` and `FIX_RUNTIME`, stripping `PROACTIVE_ACTION:` lines without breaking `ParseDailyHealthStatus`, rejecting malformed/denied directives); verify `rtk test go test ./internal/minion/... ./internal/prompt/...` fails RED; implement template and parser updates to pass GREEN.
- Completion evidence: Updated `triage.tmpl`, `daily_report.tmpl`, `internal/minion/triage.go`, and `internal/minion/daily_report.go`; `TestParseTriageDecision_FixRuntime`, `TestParseDailyProactiveActions`, and all prompt template tests passed.

## Task 3: Memos

- [x] Add `FormatRemediationMemosNote` (`#AutoRemediation`) in `internal/db/memos.go` and `IncProactiveFix(mode, outcome)` (`commander_proactive_fixes_total`) in `internal/metrics/metrics.go` with label escaping and Prometheus exposition tests.
- Covers: REQ-027 / AC-027-1, AC-027-2
- Files: internal/db/memos.go, internal/db/memos_test.go, internal/metrics/metrics.go, internal/metrics/metrics_test.go
- Dependencies: None
- Blocked by: None
- Work: Write failing RED tests in `internal/db/memos_test.go` and `internal/metrics/metrics_test.go` checking `#AutoRemediation` formatting and `commander_proactive_fixes_total` output on `GET /metrics`; verify `rtk test go test ./internal/db/... ./internal/metrics/...` fails RED; implement minimal additions to pass GREEN.
- Completion evidence: Added `FormatRemediationMemosNote` and `IncProactiveFix` / `writeProactiveCounter`; `TestFormatRemediationMemosNote` and `TestHandleMetrics_ProactiveAndRuntimeOutcomes` passed.

## Task 4: Dispatch

- [x] Implement unified `reportRemediationOutcome` (Memos + GitHub issue close/assign + metrics), wire `ActionFixRuntime` dispatch in `internal/webhook/handler.go`, and wire bounded proactive daily remediation dispatch (`DAILY_MAX_PROACTIVE_FIXES`, dedup via `checkExistingOpenGitHubWork`, daily summary comment, `15m` worker timeout) in `internal/webhook/daily_report.go`.
- Covers: REQ-025 / AC-025-1, AC-025-2; REQ-026 / AC-026-1, AC-026-2; REQ-027 / AC-027-1, AC-027-2
- Files: internal/webhook/remediation_report.go, internal/webhook/handler.go, internal/webhook/handler_test.go, internal/webhook/daily_report.go, internal/webhook/daily_report_test.go
- Dependencies: Task 1, Task 2, Task 3
- Blocked by: None
- Work: Write failing RED tests covering reactive `ActionFixRuntime` verified auto-close + Memos recording, runtime failure/RBAC-denied keeping issue open assigned to owner, proactive daily report dispatching `FIX_RUNTIME` and `FIX_VIA_PR` within cap while skipping open GitHub duplicates and isolating per-item errors, and `AUTO_MERGE=false` preservation on PR outcomes; verify `rtk test go test ./internal/webhook/...` fails RED; implement `remediation_report.go`, `handler.go`, and `daily_report.go` changes to pass GREEN; refactor cleanly.
- Completion evidence: Added `internal/webhook/remediation_report.go` and updated `handler.go` + `daily_report.go`; all 12 tests in `internal/webhook` (`handler_test.go` + `daily_report_test.go`) passed.

## Task 5: Docs

- [x] Update `README.md` (architecture flow, 4-way triage, proactive daily fixes, `#AutoRemediation` reporting, 5 new env vars, `commander_proactive_fixes_total`, and least-privilege `ClusterRole` YAML for `vinhthang/oci`) and `AGENTS.md` (runtime allowlist invariants, fake runner test discipline, `REQ-029+` / `ADR-0058+` reservation), then run full verification gates (`go test`, `go vet`, `golangci-lint`, static build, `arch-guard`, `mutation-guard`, `code-review`, `validate_specs.py`).
- Covers: REQ-028 / AC-028-1, AC-028-2
- Files: README.md, AGENTS.md
- Dependencies: Task 4
- Blocked by: None
- Work: Update `README.md` and `AGENTS.md`, verify `rtk proxy go test -count=1 ./...`, `rtk go vet ./...`, `rtk proxy golangci-lint run ./...`, `CGO_ENABLED=0 go build`, `check_boundaries.py`, `mutation_runner.py`, `prepare_code_review.py`, and `validate_specs.py`, and update spec status/activation upon passing all checks.
- Completion evidence: Updated `README.md` and `AGENTS.md`; all packages passed `go test -count=1 ./...`, `go vet ./...`, `golangci-lint run ./...` (`0 issues.`), multi-arch static builds, `arch-guard`, `mutation-guard`, and `code-review` ([Run 1](./verification.md#run-1--2026-11-23)).

## Gate 2: traceability and readiness

| Requirement | AC | ADR or rationale | Design | Task | Planned verification | Status / reason |
| --- | --- | --- | --- | --- | --- | --- |
| REQ-024 | AC-024-1 | [ADR-0055](./decisions.md#adr-0055-typed-go-enforced-runtime-allowlist-vs-agent-executed-kubectl) | [Interfaces](./design.md#interfaces-and-flow) | [Task 1](#task-1-runtime) | [AC-024-1](./verification.md#ac-024-1-req-024) | PASS |
| REQ-024 | AC-024-2 | [ADR-0055](./decisions.md#adr-0055-typed-go-enforced-runtime-allowlist-vs-agent-executed-kubectl) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 1](#task-1-runtime) | [AC-024-2](./verification.md#ac-024-2-req-024) | PASS |
| REQ-025 | AC-025-1 | [ADR-0055](./decisions.md#adr-0055-typed-go-enforced-runtime-allowlist-vs-agent-executed-kubectl) | [Interfaces](./design.md#interfaces-and-flow) | [Task 2](#task-2-triage), [Task 4](#task-4-dispatch) | [AC-025-1](./verification.md#ac-025-1-req-025) | PASS |
| REQ-025 | AC-025-2 | [ADR-0055](./decisions.md#adr-0055-typed-go-enforced-runtime-allowlist-vs-agent-executed-kubectl) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 2](#task-2-triage), [Task 4](#task-4-dispatch) | [AC-025-2](./verification.md#ac-025-2-req-025) | PASS |
| REQ-026 | AC-026-1 | [ADR-0056](./decisions.md#adr-0056-structured-proactive-actions-from-daily-audit-reusing-gitops-pipeline) | [Interfaces](./design.md#interfaces-and-flow) | [Task 2](#task-2-triage), [Task 4](#task-4-dispatch) | [AC-026-1](./verification.md#ac-026-1-req-026) | PASS |
| REQ-026 | AC-026-2 | [ADR-0056](./decisions.md#adr-0056-structured-proactive-actions-from-daily-audit-reusing-gitops-pipeline) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 2](#task-2-triage), [Task 4](#task-4-dispatch) | [AC-026-2](./verification.md#ac-026-2-req-026) | PASS |
| REQ-027 | AC-027-1 | [ADR-0057](./decisions.md#adr-0057-dual-sink-auto-remediation-reporting-and-graceful-rbac-degradation) | [Interfaces](./design.md#interfaces-and-flow) | [Task 3](#task-3-memos), [Task 4](#task-4-dispatch) | [AC-027-1](./verification.md#ac-027-1-req-027) | PASS |
| REQ-027 | AC-027-2 | [ADR-0057](./decisions.md#adr-0057-dual-sink-auto-remediation-reporting-and-graceful-rbac-degradation) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 3](#task-3-memos), [Task 4](#task-4-dispatch) | [AC-027-2](./verification.md#ac-027-2-req-027) | PASS |
| REQ-028 | AC-028-1 | [ADR-0057](./decisions.md#adr-0057-dual-sink-auto-remediation-reporting-and-graceful-rbac-degradation) | [Components](./design.md#components) | [Task 5](#task-5-docs) | [AC-028-1](./verification.md#ac-028-1-req-028) | PASS |
| REQ-028 | AC-028-2 | [ADR-0057](./decisions.md#adr-0057-dual-sink-auto-remediation-reporting-and-graceful-rbac-degradation) | [Boundaries](./design.md#boundaries-and-recovery) | [Task 5](#task-5-docs) | [AC-028-2](./verification.md#ac-028-2-req-028) | PASS |

- Backward scope check: PASS — Every file and symbol in Tasks 1–5 maps directly to `REQ-024..REQ-028`; parallel Tasks 1 and 3 own strictly disjoint `- Files:` write-sets, and Tasks 2, 4, and 5 serialize cleanly via `- Dependencies:`.
- Reference and catalog consistency: PASS — `spec.md`, `decisions.md`, `design.md`, `tasks.md`, `verification.md`, and `docs/specs/INDEX.md` agree on `REQ-024..REQ-028`, `ADR-0055..ADR-0057`, and current lifecycle state.
