# 🚨 AI Incident Commander

Autonomous Kubernetes & GitOps Incident Commander with **Gemini 3.5 Flash AI Triage** and **Self-Healing Antigravity Minions**.

---

## 🌟 Overview

AI Incident Commander acts as an intelligent SRE & DevOps responder:
1. **Webhook Ingestion**: Receives real-time alert notifications from Grafana Unified Alerting / Alertmanager on dedicated port `:8085`.
2. **AI Triage Minion**: Queries Google Gemini 3.5 Flash (`generateContent`) with cluster context and alert metadata to formulate an immediate root cause hypothesis.
3. **GitHub Issue Tracking**: Automatically opens a categorized GitHub issue with alert labels, firing metrics, and AI diagnosis.
4. **Fixer Minion**: Spawns an in-pod `agy` agent that analyzes the GitOps repository, diagnoses configuration flaws, applies code/manifest remedies, and commits directly to `main` for reconciliation.
5. **Rollback Minion**: Initiates automatic git rollback and marks incident for manual intervention if 3 consecutive fix attempts fail.

---

## ⚙️ Configuration

Environment variables configured via Kubernetes Helm chart:

| Variable | Description | Default |
| :--- | :--- | :--- |
| `PORT` | Listening HTTP port for Grafana alerts | `8085` |
| `GEMINI_MODEL` | Google AI Studio model identifier | `gemini-3.5-flash` |
| `GEMINI_API_KEY` | Google AI Studio API Key | *Required* |
| `GITHUB_TOKEN` | GitHub Personal Access Token with repo read/write | *Required* |
| `GITHUB_OWNER` | Target GitHub repository owner | `vinhthang` |
| `GITHUB_REPO` | Target GitHub repository name | `oci` |

---

## 🚀 Running Tests

```bash
# Run unit and integration tests (including Gemini model availability)
GEMINI_API_KEY="<your-api-key>" go test -v ./...
```

---

## 🐳 Container Image

Multi-arch container images (`linux/amd64`, `linux/arm64`) are automatically published to GitHub Container Registry on push:

```bash
docker pull ghcr.io/vinhthang/ai-incident-commander:latest
```
