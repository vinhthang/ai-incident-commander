package k8s

import (
	"fmt"
	"os/exec"
	"strings"
)

func FetchTelemetry(labels map[string]string) string {
	namespace := labels["namespace"]
	pod := labels["pod"]
	if pod == "" {
		pod = labels["instance"]
	}

	if namespace == "" || pod == "" {
		return "No namespace or pod specified in labels to fetch telemetry."
	}

	var builder strings.Builder
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

	return builder.String()
}
