package k8s

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/incident"
	"vinhthang.dev/ai-incident-commander/internal/workspace"
)

// WorkloadStatus summarizes whether a target resource is actively rolling out
// or has already recovered after stabilization soak.
type WorkloadStatus struct {
	InProgress bool
	Recovered  bool
	Summary    string
}

type k8sWorkloadSpec struct {
	Metadata struct {
		Name       string `json:"name"`
		Generation int64  `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int32 `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct {
					Name      string `json:"name"`
					Image     string `json:"image"`
					Resources struct {
						Limits   map[string]string `json:"limits"`
						Requests map[string]string `json:"requests"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration int64 `json:"observedGeneration"`
		Replicas           int32 `json:"replicas"`
		UpdatedReplicas    int32 `json:"updatedReplicas"`
		ReadyReplicas      int32 `json:"readyReplicas"`
		AvailableReplicas  int32 `json:"availableReplicas"`
		Conditions         []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"conditions"`
	} `json:"status"`
}

// CheckWorkloadStatus inspects live Deployment/StatefulSet rollout status or HTTP endpoint
// health so transient helm upgrades or self-healed blips do not trigger premature actions.
func CheckWorkloadStatus(labels map[string]string) WorkloadStatus {
	namespace := incident.ExtractNamespace(labels)
	kind, name := resolveTargetWorkload(labels)
	alertName := labels["alertname"]

	if namespace != "" && kind != "" && name != "" {
		out, err := exec.Command("kubectl", "get", kind, name, "-n", namespace, "-o", "json").CombinedOutput()
		if err == nil {
			var w k8sWorkloadSpec
			if jsonErr := json.Unmarshal(out, &w); jsonErr == nil {
				desired := int32(1)
				if w.Spec.Replicas != nil {
					desired = *w.Spec.Replicas
				}
				progressing := false
				for _, cond := range w.Status.Conditions {
					if cond.Type == "Progressing" && cond.Status == "True" && cond.Reason != "NewReplicaSetAvailable" {
						progressing = true
					}
				}
				if (w.Metadata.Generation > 0 && w.Status.ObservedGeneration < w.Metadata.Generation) ||
					(desired > 0 && w.Status.UpdatedReplicas < desired && progressing) {
					return WorkloadStatus{
						InProgress: true,
						Summary: fmt.Sprintf("%s/%s rollout in progress (gen %d/%d, updated %d/%d, ready %d)",
							kind, name, w.Status.ObservedGeneration, w.Metadata.Generation,
							w.Status.UpdatedReplicas, desired, w.Status.ReadyReplicas),
					}
				}
				if strings.Contains(alertName, "ReplicasUnavailable") && desired > 0 &&
					w.Status.ReadyReplicas >= desired && w.Status.AvailableReplicas >= desired && w.Status.UpdatedReplicas >= desired {
					return WorkloadStatus{
						Recovered: true,
						Summary:   fmt.Sprintf("%s/%s healthy after soak (%d/%d ready)", kind, name, w.Status.ReadyReplicas, desired),
					}
				}
			}
		}
	}

	if targetURL := extractTargetURL(labels); targetURL != "" && (alertName == "EndpointDown" || strings.HasPrefix(alertName, "Ingress")) {
		if code, err := probeEndpointStatusCode(targetURL); err == nil && code >= 200 && code < 400 {
			return WorkloadStatus{
				Recovered: true,
				Summary:   fmt.Sprintf("Endpoint %s recovered with HTTP %d after soak", targetURL, code),
			}
		}
	}

	return WorkloadStatus{}
}

// FetchDiagnosticBundle gathers enriched pre-triage diagnostics covering
// Service/Endpoints state, Workload/Pod telemetry, and Helm chart/values drift.
func FetchDiagnosticBundle(labels map[string]string, workspaceDir string) string {
	var builder strings.Builder
	builder.WriteString(FetchTelemetry(labels))
	if !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteString("\n")
	}

	namespace := incident.ExtractNamespace(labels)
	pod := resolveTargetPod(labels)
	kind, workloadName := resolveTargetWorkload(labels)

	if namespace != "" {
		appendKubectlTelemetry(&builder, "Services & Endpoints", 60, "get", "svc,endpoints", "-n", namespace, "-o", "wide")
		appendKubectlTelemetry(&builder, "Namespace Workloads", 80, "get", "deploy,sts,rs,pods", "-n", namespace, "-o", "wide")
		if pod != "" {
			appendKubectlTelemetry(&builder, "Pod Describe", 80, "describe", "pod", pod, "-n", namespace)
			appendKubectlTelemetry(&builder, "Previous Container Logs", 30, "logs", "-n", namespace, pod, "--previous", "--tail=30")
			appendKubectlTelemetry(&builder, "Recent Namespace Events", 30, "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
		}
		if kind != "" && workloadName != "" {
			appendLiveWorkloadSpec(&builder, namespace, kind, workloadName)
		}
	}

	if workspaceDir != "" {
		appendHelmDiagnostics(&builder, labels, workspaceDir)
	}

	return builder.String()
}

func appendLiveWorkloadSpec(builder *strings.Builder, namespace, kind, name string) {
	out, err := exec.Command("kubectl", "get", kind, name, "-n", namespace, "-o", "json").CombinedOutput()
	if err != nil {
		return
	}
	var w k8sWorkloadSpec
	if err := json.Unmarshal(out, &w); err != nil {
		return
	}
	fmt.Fprintf(builder, "=== Live %s Spec vs Limits ===\n", kind)
	for _, c := range w.Spec.Template.Spec.Containers {
		fmt.Fprintf(builder, "- container=%s image=%s limits=%v requests=%v\n",
			c.Name, c.Image, c.Resources.Limits, c.Resources.Requests)
	}
	builder.WriteString("\n")
}

func appendHelmDiagnostics(builder *strings.Builder, labels map[string]string, workspaceDir string) {
	workspace.Lock()
	defer workspace.Unlock()

	chartDir := filepath.Join(workspaceDir, "charts", "vinhthang-fleet")
	if _, err := os.Stat(chartDir); err != nil {
		return
	}

	target := incident.ExtractTargetResource(labels)
	candidates := helmMatchTokens(labels, target)

	builder.WriteString("=== Helm Chart & Values Check ===\n")

	var matchedTemplates []string
	templatesDir := filepath.Join(chartDir, "templates")
	_ = filepath.WalkDir(templatesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		lowerName := strings.ToLower(d.Name())
		for _, tok := range candidates {
			if tok != "" && strings.Contains(lowerName, tok) {
				if rel, relErr := filepath.Rel(workspaceDir, path); relErr == nil {
					matchedTemplates = append(matchedTemplates, rel)
				}
				break
			}
		}
		return nil
	})
	if len(matchedTemplates) > 0 {
		fmt.Fprintf(builder, "Matched Helm templates: %s\n", strings.Join(matchedTemplates, ", "))
	}

	valuesPath := filepath.Join(chartDir, "values.yaml")
	if snippet := extractValuesSection(valuesPath, candidates, 60); snippet != "" {
		builder.WriteString("Relevant values.yaml section:\n")
		builder.WriteString(snippet)
		if !strings.HasSuffix(snippet, "\n") {
			builder.WriteString("\n")
		}
	}

	if helmOut, err := exec.Command("helm", "lint", chartDir).CombinedOutput(); err == nil || len(helmOut) > 0 {
		fmt.Fprintf(builder, "Helm lint: %s\n", strings.TrimSpace(string(helmOut)))
	}

	gitArgs := []string{"-C", workspaceDir, "log", "-n", "5", "--oneline", "--"}
	if len(matchedTemplates) > 0 {
		gitArgs = append(gitArgs, matchedTemplates...)
		gitArgs = append(gitArgs, filepath.Join("charts", "vinhthang-fleet", "values.yaml"))
	} else {
		gitArgs = append(gitArgs, filepath.Join("charts", "vinhthang-fleet"))
	}
	if gitOut, err := exec.Command("git", gitArgs...).CombinedOutput(); err == nil && len(gitOut) > 0 {
		builder.WriteString("Recent Git commits for component/chart:\n")
		builder.WriteString(string(gitOut))
	}
	builder.WriteString("\n")
}

func helmMatchTokens(labels map[string]string, target string) []string {
	raw := []string{
		labels["deployment"],
		labels["statefulset"],
		labels["app"],
		labels["service"],
		target,
	}
	seen := make(map[string]bool)
	var out []string
	for _, r := range raw {
		norm := normalizeToken(r)
		if norm == "" || norm == "cluster" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
		// Strip trailing replica/pod suffixes (e.g., mypod-79d86cc9c-abcde -> mypod)
		if idx := strings.IndexByte(r, '-'); idx > 2 {
			prefix := normalizeToken(r[:idx])
			if prefix != "" && !seen[prefix] {
				seen[prefix] = true
				out = append(out, prefix)
			}
		}
	}
	return out
}

func normalizeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

func extractValuesSection(valuesPath string, tokens []string, maxLines int) string {
	data, err := os.ReadFile(valuesPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	capturing := false
	var block []string

	for _, line := range lines {
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' && strings.Contains(line, ":") {
			if capturing {
				break
			}
			topKey := normalizeToken(strings.SplitN(line, ":", 2)[0])
			for _, tok := range tokens {
				if tok != "" && (strings.Contains(topKey, tok) || strings.Contains(tok, topKey)) {
					capturing = true
					break
				}
			}
		}
		if capturing {
			block = append(block, line)
			if len(block) >= maxLines {
				break
			}
		}
	}
	return strings.Join(block, "\n")
}

func probeEndpointStatusCode(rawURL string) (int, error) {
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "AI-Incident-Commander-SoakCheck/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
	return resp.StatusCode, nil
}

// FetchTelemetry gathers diagnostic information for an incident based on alert labels.
// It supports both in-cluster Kubernetes workloads/pods (logs, describe, events) and Layer 1 edge services/probes.
func FetchTelemetry(labels map[string]string) string {
	var builder strings.Builder

	targetURL := extractTargetURL(labels)
	namespace := incident.ExtractNamespace(labels)
	pod := resolveTargetPod(labels)

	// 1. Layer 1 Synthetic / Edge HTTP Probe Tier
	if targetURL != "" {
		fmt.Fprintf(&builder, "Telemetry for Endpoint: %s\n\n", targetURL)
		probeOutput := probeEndpoint(targetURL)
		builder.WriteString("=== Active HTTP/TLS Probe ===\n")
		builder.WriteString(probeOutput + "\n\n")

		if node := labels["node"]; node != "" {
			fmt.Fprintf(&builder, "Target Edge Node: %s\n", node)
		}
		if svc := labels["service"]; svc != "" && !incident.IsMetricsScraperLabel(svc) {
			fmt.Fprintf(&builder, "Target Service: %s\n", svc)
		}
		if job := labels["job"]; job != "" {
			fmt.Fprintf(&builder, "Scrape Job: %s\n", job)
		}
		builder.WriteString("\n")
	}

	// 2. Kubernetes Pod Tier (never query Prometheus scraper pods like fleet-kube-state-metrics)
	if namespace != "" && pod != "" {
		fmt.Fprintf(&builder, "Telemetry for Pod %s in Namespace %s:\n\n", pod, namespace)
		appendKubectlTelemetry(&builder, "Recent Logs", 0, "logs", "-n", namespace, pod, "--tail=50")
		appendKubectlTelemetry(&builder, "Recent Events", 0, "get", "events", "-n", namespace, "--field-selector", "involvedObject.name="+pod)
	} else if namespace != "" {
		// 3. Workload Tier fallback (Deployment / StatefulSet) when pod is absent or shadowed by a scraper label
		if kind, name := resolveTargetWorkload(labels); kind != "" && name != "" {
			fmt.Fprintf(&builder, "Telemetry for %s %s in Namespace %s:\n\n", kind, name, namespace)
			appendKubectlTelemetry(&builder, "Describe "+kind, 0, "describe", kind, name, "-n", namespace)
			appendKubectlTelemetry(&builder, "Workload Pods", 0, "get", "pods", "-n", namespace, "-l", "app="+name)
			appendKubectlTelemetry(&builder, "Recent Namespace Events", 30, "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
		}
	}

	if builder.Len() == 0 {
		return "No namespace or pod specified in labels to fetch telemetry."
	}

	return builder.String()
}

func resolveTargetPod(labels map[string]string) string {
	if p := labels["exported_pod"]; p != "" && !incident.IsMetricsScraperLabel(p) {
		return p
	}
	if p := labels["pod"]; p != "" && !incident.IsMetricsScraperLabel(p) {
		return p
	}
	return ""
}

func resolveTargetWorkload(labels map[string]string) (string, string) {
	if dep := labels["deployment"]; dep != "" {
		return "deployment", dep
	}
	if sts := labels["statefulset"]; sts != "" {
		return "statefulset", sts
	}
	return "", ""
}

func appendKubectlTelemetry(builder *strings.Builder, heading string, maxTailLines int, args ...string) {
	cmd := exec.Command("kubectl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return
	}
	text := string(output)
	if maxTailLines > 0 {
		text = tailLines(text, maxTailLines)
	}
	fmt.Fprintf(builder, "=== %s ===\n", heading)
	builder.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
}

func tailLines(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
}

func extractTargetURL(labels map[string]string) string {
	keys := []string{"http_url", "url", "endpoint"}
	for _, k := range keys {
		if val := labels[k]; val != "" {
			if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
				return val
			}
			return "http://" + val
		}
	}

	if host := labels["host"]; host != "" {
		if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
			return host
		}
		return "https://" + host
	}

	// If instance resembles host:port and no pod label is present
	instance := labels["instance"]
	if instance != "" && labels["pod"] == "" && labels["exported_pod"] == "" {
		if strings.Contains(instance, ":") && !strings.Contains(instance, "svc.cluster.local") {
			return "http://" + instance
		}
	}

	return ""
}

func probeEndpoint(rawURL string) string {
	client := &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return fmt.Sprintf("Probe Status: FAILED (Invalid URL)\nError: %v", err)
	}
	req.Header.Set("User-Agent", "AI-Incident-Commander-Telemetry/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Probe Status: FAILED (Network/Socket Error)\nError: %v", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("probeEndpoint: failed closing response body for %s: %v", rawURL, closeErr)
		}
	}()

	var out strings.Builder
	fmt.Fprintf(&out, "Probe Status: HTTP %d %s\n", resp.StatusCode, resp.Status)
	fmt.Fprintf(&out, "Proto: %s\n", resp.Proto)

	if s := resp.Header.Get("Server"); s != "" {
		fmt.Fprintf(&out, "Server: %s\n", s)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(&out, "Content-Type: %s\n", ct)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		fmt.Fprintf(&out, "Redirect Location: %s\n", loc)
	}

	// Read small snippet of body
	bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if len(bodySnippet) > 0 {
		cleanBody := strings.TrimSpace(string(bodySnippet))
		if cleanBody != "" {
			fmt.Fprintf(&out, "Response Body Snippet:\n%s\n", cleanBody)
		}
	}

	return out.String()
}
