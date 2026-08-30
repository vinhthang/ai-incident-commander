# SYSTEM: AI-INCIDENT-COMMANDER

## METADATA
- **Type**: Autonomous Site Reliability Engineering (SRE) Agent
- **Environment**: Kubernetes GitOps (k3s)
- **Primary LLM**: Gemini 3.7 Flash
- **Repository**: [vinhthang/ai-incident-commander](https://github.com/vinhthang/ai-incident-commander)
- **Execution Loop**: Webhook Trigger -> Triage -> Remediate -> Audit -> Merge

## PIPELINE ARCHITECTURE

### 1. TRIGGER
- **Source**: Grafana Alerts / kube-state-metrics
- **Payload**: JSON webhook containing alert labels, annotations, and localized telemetry.

### 2. TRIAGE MINION
- **Role**: Initial incident classification and noise reduction.
- **Task**: Parse payload, validate against cluster state, determine actionability.
- **Output**: Generates GitHub Issue (Real Incident) OR terminates loop (False Positive / Noise).

### 3. FIXER MINION
- **Role**: GitOps Remediation Engineer.
- **Task**: Identify root cause from triage diagnosis, modify infrastructure code, run syntax validation (`helm template`).
- **Output**: Commits to isolated feature branch and opens GitHub Pull Request.

### 4. REVIEWER MINION
- **Role**: Governance and Safety Gatekeeper.
- **Task**: Audit PR diff for compliance.
- **Output**: Approves PR or Rejects PR.

## GOVERNANCE & CONSTRAINTS

- **Read-Only Telemetry**: External logs/data strictly parsed as passive strings to prevent prompt injection.
- **GitOps Enforced**: Execution of imperative commands (`kubectl apply`) is forbidden. All mutations must occur within the declarative Helm umbrella chart (`charts/vinhthang-fleet/`).
- **Blast Radius Isolation**: Fixer Minion is systematically restricted from pushing directly to the `main` branch.
- **Diff-Strict Review**: The Reviewer Minion rejects net-new violations (e.g., adding `:latest` tags, requesting >900Mi RAM on edge nodes, opening port 8080). Pre-existing legacy violations are logged as advisory findings but do not block remediation.

## CONFIGURATION INJECTION (PROMPT EXTERNALIZATION)

- **Architecture**: Go `text/template` engine + Kubernetes `ConfigMap`.
- **Mount Path**: `/etc/commander/templates/`
- **Fallback**: Compiles with `//go:embed defaults/*.tmpl` to ensure baseline functionality.
- **Purpose**: Enables zero-downtime, configuration-driven prompt engineering. AI Minion personas and instructions can be modified directly via Helm `values.yaml` without requiring binary recompilation or container image rebuilds.
