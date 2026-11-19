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
)

// FetchTelemetry gathers diagnostic information for an incident based on alert labels.
// It supports both in-cluster Kubernetes pods (logs, events) and Layer 1 edge services/probes (HTTP/TLS active probes, node context).
func FetchTelemetry(labels map[string]string) string {
	var builder strings.Builder

	targetURL := extractTargetURL(labels)
	hasK8sPod := (labels["pod"] != "" || labels["exported_pod"] != "")

	// 1. Layer 1 Synthetic / Edge HTTP Probe Tier
	if targetURL != "" {
		fmt.Fprintf(&builder, "Telemetry for Endpoint: %s\n\n", targetURL)
		probeOutput := probeEndpoint(targetURL)
		builder.WriteString("=== Active HTTP/TLS Probe ===\n")
		builder.WriteString(probeOutput + "\n\n")

		if node := labels["node"]; node != "" {
			fmt.Fprintf(&builder, "Target Edge Node: %s\n", node)
		}
		if svc := labels["service"]; svc != "" {
			fmt.Fprintf(&builder, "Target Service: %s\n", svc)
		}
		if job := labels["job"]; job != "" {
			fmt.Fprintf(&builder, "Scrape Job: %s\n", job)
		}
		builder.WriteString("\n")
	}

	// 2. Kubernetes Pod Tier
	if hasK8sPod {
		namespace := labels["exported_namespace"]
		if namespace == "" {
			namespace = labels["namespace"]
		}
		pod := labels["exported_pod"]
		if pod == "" {
			pod = labels["pod"]
		}

		if namespace != "" && pod != "" {
			fmt.Fprintf(&builder, "Telemetry for Pod %s in Namespace %s:\n\n", pod, namespace)

			logCmd := exec.Command("kubectl", "logs", "-n", namespace, pod, "--tail=50")
			logOutput, err := logCmd.CombinedOutput()
			if err == nil {
				builder.WriteString("=== Recent Logs ===\n")
				builder.WriteString(string(logOutput) + "\n\n")
			}

			evtCmd := exec.Command("kubectl", "get", "events", "-n", namespace, "--field-selector", "involvedObject.name="+pod)
			evtOutput, err := evtCmd.CombinedOutput()
			if err == nil {
				builder.WriteString("=== Recent Events ===\n")
				builder.WriteString(string(evtOutput) + "\n\n")
			}
		}
	}

	if builder.Len() == 0 {
		return "No namespace or pod specified in labels to fetch telemetry."
	}

	return builder.String()
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
