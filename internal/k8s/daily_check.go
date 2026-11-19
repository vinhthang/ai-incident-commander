package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/workspace"
)

type vmQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// CollectDailyFleetSnapshot gathers read-only cluster, workload, Service/Endpoints,
// Helm/GitOps, and VictoriaMetrics telemetry for the daily agy fleet audit.
func CollectDailyFleetSnapshot(ctx context.Context, workspaceDir, vmURL string) string {
	var b strings.Builder

	collectNodesSnapshot(ctx, &b)
	collectPodsSnapshot(ctx, &b)
	collectWorkloadsSnapshot(ctx, &b)
	collectServicesAndEndpointsSnapshot(ctx, &b)
	collectJobsAndPVCsSnapshot(ctx, &b)

	if evtOut, err := runCmd(ctx, "", "kubectl", "get", "events", "-A", "--field-selector", "type=Warning", "--sort-by=.lastTimestamp"); err == nil && evtOut != "" {
		b.WriteString("=== Recent Warning Events ===\n")
		b.WriteString(tailLines(evtOut, 30) + "\n\n")
	}

	collectHelmGitOpsSnapshot(ctx, &b, workspaceDir)
	collectVictoriaMetricsSnapshot(ctx, &b, vmURL)

	return b.String()
}

func runCmd(ctx context.Context, dir, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func collectNodesSnapshot(ctx context.Context, b *strings.Builder) {
	out, err := runCmd(ctx, "", "kubectl", "get", "nodes", "-o", "json")
	if err != nil {
		fmt.Fprintf(b, "=== Nodes ===\nError fetching nodes: %v\n\n", err)
		return
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return
	}
	b.WriteString("=== Nodes ===\n")
	for _, n := range list.Items {
		ready := "Unknown"
		var pressure []string
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" {
				ready = c.Status
			} else if c.Status == "True" {
				pressure = append(pressure, c.Type)
			}
		}
		fmt.Fprintf(b, "- %s: Ready=%s pressure=%v\n", n.Metadata.Name, ready, pressure)
	}
	b.WriteString("\n")
}

func collectPodsSnapshot(ctx context.Context, b *strings.Builder) {
	out, err := runCmd(ctx, "", "kubectl", "get", "pods", "-A", "-o", "json")
	if err != nil {
		return
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name         string `json:"name"`
					Ready        bool   `json:"ready"`
					RestartCount int    `json:"restartCount"`
					State        struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return
	}

	healthy := 0
	var issues []string
	for _, p := range list.Items {
		if p.Status.Phase == "Succeeded" {
			continue
		}
		unhealthyReasons := []string{}
		totalRestarts := 0
		for _, cs := range p.Status.ContainerStatuses {
			totalRestarts += cs.RestartCount
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				unhealthyReasons = append(unhealthyReasons, cs.State.Waiting.Reason)
			} else if !cs.Ready {
				unhealthyReasons = append(unhealthyReasons, cs.Name+"=NotReady")
			}
		}
		if p.Status.Phase != "Running" || len(unhealthyReasons) > 0 || totalRestarts > 0 {
			issues = append(issues, fmt.Sprintf("- %s/%s: phase=%s restarts=%d issues=%v",
				p.Metadata.Namespace, p.Metadata.Name, p.Status.Phase, totalRestarts, unhealthyReasons))
		} else {
			healthy++
		}
	}

	fmt.Fprintf(b, "=== Pods Summary ===\nHealthy Running Pods: %d\n", healthy)
	if len(issues) == 0 {
		b.WriteString("Unhealthy or Restarting Pods: none\n\n")
		return
	}
	b.WriteString("Unhealthy or Restarting Pods:\n")
	for _, line := range issues {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

func collectWorkloadsSnapshot(ctx context.Context, b *strings.Builder) {
	out, err := runCmd(ctx, "", "kubectl", "get", "deployments,statefulsets", "-A", "-o", "json")
	if err != nil {
		return
	}
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int32 `json:"replicas"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas     int32 `json:"readyReplicas"`
				AvailableReplicas int32 `json:"availableReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return
	}

	b.WriteString("=== Degraded Deployments & StatefulSets ===\n")
	degraded := 0
	for _, w := range list.Items {
		desired := int32(1)
		if w.Spec.Replicas != nil {
			desired = *w.Spec.Replicas
		}
		if w.Status.ReadyReplicas < desired {
			degraded++
			fmt.Fprintf(b, "- %s %s/%s: ready=%d/%d available=%d\n",
				w.Kind, w.Metadata.Namespace, w.Metadata.Name, w.Status.ReadyReplicas, desired, w.Status.AvailableReplicas)
		}
	}
	if degraded == 0 {
		b.WriteString("None\n")
	}
	b.WriteString("\n")
}

func collectServicesAndEndpointsSnapshot(ctx context.Context, b *strings.Builder) {
	svcOut, svcErr := runCmd(ctx, "", "kubectl", "get", "svc", "-A", "-o", "json")
	epOut, epErr := runCmd(ctx, "", "kubectl", "get", "endpoints", "-A", "-o", "json")
	if svcErr != nil || epErr != nil {
		return
	}

	var svcList struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Type     string            `json:"type"`
				Selector map[string]string `json:"selector"`
			} `json:"spec"`
		} `json:"items"`
	}
	var epList struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Subsets []struct {
				Addresses []struct {
					IP string `json:"ip"`
				} `json:"addresses"`
			} `json:"subsets"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(svcOut), &svcList); err != nil {
		return
	}
	if err := json.Unmarshal([]byte(epOut), &epList); err != nil {
		return
	}

	readyAddrs := make(map[string]int)
	for _, ep := range epList.Items {
		count := 0
		for _, sub := range ep.Subsets {
			count += len(sub.Addresses)
		}
		readyAddrs[ep.Metadata.Namespace+"/"+ep.Metadata.Name] = count
	}

	b.WriteString("=== Services With 0 Ready Endpoints ===\n")
	emptyCount := 0
	for _, svc := range svcList.Items {
		if svc.Spec.Type == "ExternalName" {
			continue
		}
		key := svc.Metadata.Namespace + "/" + svc.Metadata.Name
		if readyAddrs[key] == 0 {
			emptyCount++
			fmt.Fprintf(b, "- %s (%s): 0 ready endpoints\n", key, svc.Spec.Type)
		}
	}
	if emptyCount == 0 {
		b.WriteString("None\n")
	}
	b.WriteString("\n")
}

func collectJobsAndPVCsSnapshot(ctx context.Context, b *strings.Builder) {
	b.WriteString("=== Failed Jobs & Unbound PVCs ===\n")
	foundIssue := false
	if jobOut, err := runCmd(ctx, "", "kubectl", "get", "jobs", "-A", "-o", "json"); err == nil {
		var jobs struct {
			Items []struct {
				Metadata struct {
					Namespace string `json:"namespace"`
					Name      string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Failed int32 `json:"failed"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(jobOut), &jobs) == nil {
			for _, j := range jobs.Items {
				if j.Status.Failed > 0 {
					foundIssue = true
					fmt.Fprintf(b, "- Job %s/%s: failed=%d\n", j.Metadata.Namespace, j.Metadata.Name, j.Status.Failed)
				}
			}
		}
	}
	if pvcOut, err := runCmd(ctx, "", "kubectl", "get", "pvc", "-A", "-o", "json"); err == nil {
		var pvcs struct {
			Items []struct {
				Metadata struct {
					Namespace string `json:"namespace"`
					Name      string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(pvcOut), &pvcs) == nil {
			for _, p := range pvcs.Items {
				if p.Status.Phase != "Bound" {
					foundIssue = true
					fmt.Fprintf(b, "- PVC %s/%s: phase=%s\n", p.Metadata.Namespace, p.Metadata.Name, p.Status.Phase)
				}
			}
		}
	}
	if !foundIssue {
		b.WriteString("None\n")
	}
	b.WriteString("\n")
}

func collectHelmGitOpsSnapshot(ctx context.Context, b *strings.Builder, workspaceDir string) {
	if workspaceDir == "" {
		return
	}
	workspace.Lock()
	defer workspace.Unlock()

	chartDir := filepath.Join(workspaceDir, "charts", "vinhthang-fleet")
	b.WriteString("=== Helm & GitOps Check ===\n")
	if lintOut, err := runCmd(ctx, workspaceDir, "helm", "lint", chartDir); err == nil || lintOut != "" {
		fmt.Fprintf(b, "helm lint: %s\n", lintOut)
	}
	if logOut, err := runCmd(ctx, workspaceDir, "git", "log", "-n", "5", "--oneline"); err == nil && logOut != "" {
		b.WriteString("Recent commits on main:\n" + logOut + "\n")
	}
	b.WriteString("\n")
}

func collectVictoriaMetricsSnapshot(ctx context.Context, b *strings.Builder, vmURL string) {
	if strings.TrimSpace(vmURL) == "" {
		return
	}
	b.WriteString("=== VictoriaMetrics Live Metrics ===\n")

	memRes, err := queryVM(ctx, vmURL, `(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100`)
	if err != nil {
		fmt.Fprintf(b, "Node RAM query unavailable: %v\n", err)
	} else if len(memRes.Data.Result) > 0 {
		b.WriteString("Node Memory Utilization:\n")
		for _, item := range memRes.Data.Result {
			label := item.Metric["node"]
			if label == "" {
				label = item.Metric["instance"]
			}
			val := "?"
			if len(item.Value) == 2 {
				val = fmt.Sprintf("%v", item.Value[1])
			}
			fmt.Fprintf(b, "- %s: %s%%\n", label, val)
		}
	}

	httpRes, err := queryVM(ctx, vmURL, `max by (http_url) (httpcheck_status)`)
	if err == nil && len(httpRes.Data.Result) > 0 {
		b.WriteString("Edge HTTP Probes (httpcheck_status):\n")
		for _, item := range httpRes.Data.Result {
			target := item.Metric["http_url"]
			val := "?"
			if len(item.Value) == 2 {
				val = fmt.Sprintf("%v", item.Value[1])
			}
			fmt.Fprintf(b, "- %s: status=%s\n", target, val)
		}
	}
	b.WriteString("\n")
}

func queryVM(ctx context.Context, baseURL, promQL string) (vmQueryResponse, error) {
	var parsed vmQueryResponse
	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/query?query=" + url.QueryEscape(promQL)
	req, err := http.NewRequestWithContext(qCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return parsed, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return parsed, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return parsed, err
	}
	if resp.StatusCode != http.StatusOK {
		return parsed, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return parsed, err
	}
	return parsed, nil
}
