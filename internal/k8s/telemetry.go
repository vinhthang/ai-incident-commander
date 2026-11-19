package k8s

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/incident"
)

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
