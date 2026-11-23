# Decisions: Autonomous Runtime Remediation and Proactive Daily Audit Dispatch

Governing specification: [spec.md](./spec.md)

## Architectural rationale

- Governs: REQ-024, REQ-025, REQ-026, REQ-027, REQ-028

The user asked to expand `ai-incident-commander` beyond GitOps-only PRs so it can remediate both runtime issues and proactive daily audit findings whenever it has a verified root cause, and report results clearly. Extending the existing architecture cleanly requires preserving `RULE[no-kubectl-mutation]`-style safety against arbitrary LLM shell execution while reusing the proven `remediateClusterIssue`, GitHub dedup, Memos DB, and Prometheus metrics pipelines.

## ADR-0055: Typed Go-Enforced Runtime Allowlist vs Agent-Executed kubectl

- Governs: REQ-024, REQ-025
- State: Accepted
- Context and constraints: `agy` subprocesses run inside the commander pod and consume untrusted pod logs, events, and alert annotations. Giving `agy` prompts direct permission to run mutating `kubectl` commands risks prompt injection or broad cluster disruption across stateful databases in `data/` (`postgres-primary`, `mongodb-rs0`, `redis`, `fleet-pulsar-*`) and `kube-system`. Meanwhile, live cluster checks confirm `ServiceAccount/ai-incident-commander` currently has read-only RBAC (`ai-incident-commander-reader`), so even safe mutations need explicit least-privilege RBAC and clean fallback if RBAC has not yet been updated.
- Viable options:
  - **Option A**: Keep `agy` strictly read-only during Triage and parse a typed, line-oriented directive (`RUNTIME_ACTION: <ROLLOUT_RESTART|DELETE_FAILED_JOB> <ns>/<kind>/<name>`) + `VERDICT: FIX_RUNTIME` (`Confidence: HIGH`), executing the mutation solely inside deterministic Go code (`internal/k8s/remediate.go`) after DNS-1123 validation, protected namespace/workload denylist checks, Job failed-only preflight, and post-action `rollout status` verification.
  - **Option B**: Authorize `agy` CLI sessions directly to execute `kubectl rollout restart` / `kubectl delete job` against the cluster during triage/fixer turns.
- Decision: Select **Option A** (typed Go-enforced runtime allowlist).
- Costs and risks: Limits runtime actions initially to `ROLLOUT_RESTART` (`deployment`, `statefulset`) and `DELETE_FAILED_JOB` (`job`); future action verbs require a deliberate Go code update rather than a ConfigMap prompt tweak. Mitigation: clearly document the allowlist and add extensible validator helpers in `internal/k8s/remediate.go`.
- Rejected alternatives: **Option B** exposes cluster workloads directly to prompt-injection or LLM hallucination without deterministic preflight checks or protected-namespace guarantees.
- Affected components: `internal/config/config.go`, `internal/k8s/remediate.go`, `internal/minion/triage.go`, `internal/prompt/defaults/triage.tmpl`, `internal/webhook/handler.go`.

## ADR-0056: Structured Proactive Actions from Daily Audit Reusing GitOps Pipeline

- Governs: REQ-026
- State: Accepted
- Context and constraints: Previously `/daily-report` emitted human prose (`### Actionable Recommendations`) and exited. Running arbitrary free-form recommendation parsing risks duplicate GitHub PRs/issues and long-running unattended queues on every morning CronJob trigger.
- Viable options:
  - **Option A**: Extend `daily_report.tmpl` to emit machine-parseable `PROACTIVE_ACTION:` lines (`FIX_VIA_PR` or `FIX_RUNTIME`) only for root-cause-verified findings, parse/strip them cleanly in Go (`ParseDailyProactiveActions`), deduplicate against existing open GitHub Issues/PRs (`checkExistingOpenGitHubWork`), cap per-run dispatches via `DAILY_MAX_PROACTIVE_FIXES` (default `3`), and route through the existing `remediateClusterIssue` and `k8s.ExecuteRuntimeAction` functions.
  - **Option B**: Spawn a second generic triage webhook loop by synthetically HTTP-posting every bullet from `Actionable Recommendations` back into `POST /webhook`.
- Decision: Select **Option A** (structured `PROACTIVE_ACTION:` extraction with bounded dispatch).
- Costs and risks: Raises the `/daily-report` background worker timeout from `5m` to `15m` so up to 3 fixes can run serially through `AgyQueue`; depends on LLM formatting compliance (which safely degrades to zero proactive fixes if omitted).
- Rejected alternatives: **Option B** turns free-text advice (including escalations like intentional resource caps or external outages) into noisy webhook storms without root-cause gating or single-flight daily summary consolidation.
- Affected components: `internal/config/config.go`, `internal/prompt/defaults/daily_report.tmpl`, `internal/minion/daily_report.go`, `internal/webhook/daily_report.go`, `internal/webhook/handler.go`.

## ADR-0057: Dual-Sink Auto-Remediation Reporting and Graceful RBAC Degradation

- Governs: REQ-024, REQ-027, REQ-028
- State: Accepted
- Context and constraints: The user requested both **GitHub Issues** (auto-closed on verified runtime fixes, kept open on failure or `AUTO_MERGE=false` PRs) and **Memos** (`#AutoRemediation`) reporting, plus keeping `AUTO_MERGE` env-controlled. In addition, the commander image may be deployed before the operator applies the narrow write `ClusterRole` in `vinhthang/oci`.
- Viable options:
  - **Option A**: Centralize outcome reporting in `webhook.reportRemediationOutcome` (calling `db.InsertMemosReport` with `db.FormatRemediationMemosNote`, updating/closing/assigning the GitHub issue, and recording Prometheus metrics), while classifying `Forbidden` `kubectl` output as `RBACDenied=true` so the issue stays open and informs the operator rather than crashing or spinning.
  - **Option B**: Report runtime fixes only on GitHub and reuse `#DailyFleetCheck` Memos notes, treating any `kubectl` error as an unhandled panic/abort.
- Decision: Select **Option A** (dedicated `#AutoRemediation` Memos + GitHub outcome reporting and explicit RBAC classification).
- Costs and risks: Adds minor Postgres write attempts when `MEMOS_DB_DSN` is set (already fault-tolerant on errors) and adds a small Operator RBAC section in `README.md` that the cluster owner must apply in `vinhthang/oci` for live runtime mutations to succeed.
- Rejected alternatives: **Option B** omits the requested Memos visibility for reactive/proactive fixes and leaves RBAC permission errors indistinguishable from transient pod failures.
- Affected components: `internal/db/memos.go`, `internal/metrics/metrics.go`, `internal/k8s/remediate.go`, `internal/webhook/remediation_report.go`, `README.md`, `AGENTS.md`.

## Gate 1: normative review

| Check | Status | Evidence / required correction |
| --- | --- | --- |
| Codebase grounding | PASS | Grounded in `internal/config/config.go`, `internal/k8s/daily_check.go`, `internal/minion/{triage,daily_report}.go`, `internal/prompt/defaults/{triage,daily_report}.tmpl`, `internal/webhook/{handler,daily_report}.go`, `internal/db/memos.go`, `internal/metrics/metrics.go`, and live cluster `ClusterRole/ai-incident-commander-reader` + namespace inventory (`data`, `kube-system`, `observability`, `system`). |
| Attributable intent and explicit scope | PASS | Quoted user prompt and mapped all 4 explicit multiple-choice selections (A1–A4) directly to `REQ-024..REQ-028`; explicit `Non-goals` exclude direct `agy` mutating shell access, stateful DB/`kube-system`/self restarts, live cluster RBAC mutation from this repo, external app repo cloning, and changing `AUTO_MERGE` default. |
| Observable requirements and meaningful acceptance criteria | PASS | `REQ-024..REQ-028` use EARS statements (`WHEN`/`IF ... THEN`) with falsifiable `AC-*-1` success and `AC-*-2` boundary/failure criteria covering exit states, DB/GitHub/Memos side effects, Prometheus metrics, and documentation checks. |
| Assumptions and owner-deferred questions are explicit | PASS | Material product choices were answered upfront via `ask_question`; reversible defaults (`ENABLE_RUNTIME_REMEDIATION=true`, `ENABLE_DAILY_PROACTIVE_FIX=true`, `DAILY_MAX_PROACTIVE_FIXES=3`, `RUNTIME_VERIFY_TIMEOUT=90s`, `15m` daily context timeout) are explicitly documented; zero `[PROVISIONAL]` items remain. |
| Architectural rationale respects requirements | PASS | `ADR-0055..ADR-0057` compare viable alternatives, preserve `AUTO_MERGE=false` and read-only agent triage invariants, and respect Go standard-library conventions with zero new third-party dependencies. |
| IDs, references, and revision route are consistent | PASS | Parent delta `2026-11-22-commander-reliability-and-observability` has `First entered In-Testing: 2026-11-22`, so `2026-11-23-autonomous-remediation-and-proactive-audit` uses linked delta route (`Proposed: Changes`) with newly allocated `REQ-024..REQ-028` and `ADR-0055..ADR-0057` synchronized in `docs/specs/INDEX.md`. |
