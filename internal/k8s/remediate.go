package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

// RuntimeActionType enumerates Go-enforced allowlisted runtime Kubernetes mutations.
type RuntimeActionType string

const (
	RuntimeRolloutRestart  RuntimeActionType = "ROLLOUT_RESTART"
	RuntimeDeleteFailedJob RuntimeActionType = "DELETE_FAILED_JOB"
)

var (
	dns1123LabelRe    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	protectedDBTokens = []string{"postgres", "mongodb", "redis", "pulsar", "etcd"}
	runtimeCmdRunner  = runCmd
)

// RuntimeAction describes a bounded, validated runtime Kubernetes operation.
type RuntimeAction struct {
	Type      RuntimeActionType
	Namespace string
	Kind      string
	Name      string
	Reason    string
}

// TargetString returns "<namespace>/<kind>/<name>" for compact logging and reporting.
func (a RuntimeAction) TargetString() string {
	return fmt.Sprintf("%s/%s/%s", a.Namespace, strings.ToLower(a.Kind), a.Name)
}

// RuntimeActionResult captures execution outcome, post-action verification, and denial/RBAC status.
type RuntimeActionResult struct {
	Action       RuntimeAction
	Executed     bool
	Verified     bool
	RBACDenied   bool
	DeniedReason string
	Output       string
	VerifyOutput string
	Err          error
}

// ParseRuntimeActionSpec parses "<ROLLOUT_RESTART|DELETE_FAILED_JOB> <ns>/<kind>/<name>".
func ParseRuntimeActionSpec(spec string) (RuntimeAction, error) {
	fields := strings.Fields(strings.TrimSpace(spec))
	if len(fields) != 2 {
		return RuntimeAction{}, fmt.Errorf("invalid RUNTIME_ACTION format %q: expected '<ACTION> <ns>/<kind>/<name>'", spec)
	}

	actionType := RuntimeActionType(strings.ToUpper(fields[0]))
	parts := strings.Split(fields[1], "/")
	if len(parts) != 3 {
		return RuntimeAction{}, fmt.Errorf("invalid RUNTIME_ACTION target %q: expected '<ns>/<kind>/<name>'", fields[1])
	}

	act := RuntimeAction{
		Type:      actionType,
		Namespace: strings.TrimSpace(parts[0]),
		Kind:      strings.ToLower(strings.TrimSpace(parts[1])),
		Name:      strings.TrimSpace(parts[2]),
	}
	if err := ValidateRuntimeAction(act); err != nil {
		return RuntimeAction{}, err
	}
	return act, nil
}

// ValidateRuntimeAction enforces configuration flags, DNS-1123 naming, and workload/namespace exclusions.
func ValidateRuntimeAction(act RuntimeAction) error {
	if !config.EnableRuntimeRemediation {
		return errors.New("runtime remediation disabled (ENABLE_RUNTIME_REMEDIATION=false)")
	}

	ns := act.Namespace
	kind := strings.ToLower(strings.TrimSpace(act.Kind))
	name := act.Name

	if !dns1123LabelRe.MatchString(ns) {
		return fmt.Errorf("invalid Kubernetes namespace %q", ns)
	}
	if !dns1123LabelRe.MatchString(name) {
		return fmt.Errorf("invalid Kubernetes workload name %q", name)
	}

	for _, protectedNS := range config.RuntimeProtectedNamespaces {
		if strings.EqualFold(ns, strings.TrimSpace(protectedNS)) {
			return fmt.Errorf("namespace %q is protected against autonomous runtime remediation", ns)
		}
	}

	if ns == "system" && name == "ai-incident-commander" {
		return errors.New("self-remediation of system/ai-incident-commander is prohibited")
	}

	lowerName := strings.ToLower(name)
	for _, token := range protectedDBTokens {
		if strings.Contains(lowerName, token) {
			return fmt.Errorf("workload %q matches protected stateful database token %q", name, token)
		}
	}

	switch RuntimeActionType(strings.ToUpper(string(act.Type))) {
	case RuntimeRolloutRestart:
		if kind != "deployment" && kind != "statefulset" {
			return fmt.Errorf("ROLLOUT_RESTART only permits deployment or statefulset, got %q", act.Kind)
		}
	case RuntimeDeleteFailedJob:
		if kind != "job" {
			return fmt.Errorf("DELETE_FAILED_JOB only permits job, got %q", act.Kind)
		}
	default:
		return fmt.Errorf("unsupported runtime action %q", act.Type)
	}
	return nil
}

// ExecuteRuntimeAction validates and runs an allowlisted runtime action with post-action health verification.
func ExecuteRuntimeAction(ctx context.Context, act RuntimeAction) RuntimeActionResult {
	normalized := act
	normalized.Type = RuntimeActionType(strings.ToUpper(string(act.Type)))
	normalized.Kind = strings.ToLower(strings.TrimSpace(act.Kind))

	res := RuntimeActionResult{Action: normalized}
	if err := ValidateRuntimeAction(normalized); err != nil {
		res.DeniedReason = err.Error()
		res.Err = err
		return res
	}

	switch normalized.Type {
	case RuntimeRolloutRestart:
		return executeRolloutRestart(ctx, normalized)
	case RuntimeDeleteFailedJob:
		return executeDeleteFailedJob(ctx, normalized)
	default:
		res.DeniedReason = fmt.Sprintf("unsupported runtime action %q", normalized.Type)
		res.Err = errors.New(res.DeniedReason)
		return res
	}
}

func executeRolloutRestart(ctx context.Context, act RuntimeAction) RuntimeActionResult {
	res := RuntimeActionResult{Action: act}
	target := act.Kind + "/" + act.Name

	out, err := runtimeCmdRunner(ctx, "", "kubectl", "-n", act.Namespace, "rollout", "restart", target)
	res.Output = out
	if err != nil {
		res.RBACDenied = isRBACForbidden(out, err)
		res.Err = fmt.Errorf("kubectl rollout restart %s/%s: %w", act.Namespace, target, err)
		return res
	}
	res.Executed = true

	timeout := config.RuntimeVerifyTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	timeoutFlag := "--timeout=" + timeout.String()
	if timeout >= time.Second {
		timeoutFlag = fmt.Sprintf("--timeout=%ds", int(timeout.Seconds()))
	}
	verifyOut, verifyErr := runtimeCmdRunner(ctx, "", "kubectl", "-n", act.Namespace, "rollout", "status", target, timeoutFlag)
	res.VerifyOutput = verifyOut
	if verifyErr != nil {
		res.RBACDenied = isRBACForbidden(verifyOut, verifyErr)
		res.Err = fmt.Errorf("kubectl rollout status %s/%s: %w", act.Namespace, target, verifyErr)
		return res
	}

	res.Verified = true
	return res
}

func executeDeleteFailedJob(ctx context.Context, act RuntimeAction) RuntimeActionResult {
	res := RuntimeActionResult{Action: act}

	jobJSON, err := runtimeCmdRunner(ctx, "", "kubectl", "-n", act.Namespace, "get", "job", act.Name, "-o", "json")
	if err != nil {
		res.Output = jobJSON
		res.RBACDenied = isRBACForbidden(jobJSON, err)
		res.Err = fmt.Errorf("preflight get job %s/%s: %w", act.Namespace, act.Name, err)
		return res
	}

	var parsed struct {
		Status struct {
			Active    int `json:"active"`
			Succeeded int `json:"succeeded"`
			Failed    int `json:"failed"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(jobJSON), &parsed); err != nil {
		res.DeniedReason = fmt.Sprintf("failed to parse job status for %s/%s", act.Namespace, act.Name)
		res.Output = jobJSON
		res.Err = fmt.Errorf("%s: %w", res.DeniedReason, err)
		return res
	}
	if parsed.Status.Active > 0 || parsed.Status.Succeeded > 0 || parsed.Status.Failed <= 0 {
		res.DeniedReason = fmt.Sprintf("job %s/%s is not exclusively failed (active=%d succeeded=%d failed=%d)",
			act.Namespace, act.Name, parsed.Status.Active, parsed.Status.Succeeded, parsed.Status.Failed)
		res.Err = errors.New(res.DeniedReason)
		return res
	}

	delOut, delErr := runtimeCmdRunner(ctx, "", "kubectl", "-n", act.Namespace, "delete", "job", act.Name, "--wait=false")
	res.Output = delOut
	if delErr != nil {
		res.RBACDenied = isRBACForbidden(delOut, delErr)
		res.Err = fmt.Errorf("kubectl delete job %s/%s: %w", act.Namespace, act.Name, delErr)
		return res
	}
	res.Executed = true

	verifyOut, verifyErr := runtimeCmdRunner(ctx, "", "kubectl", "-n", act.Namespace, "get", "job", act.Name)
	res.VerifyOutput = verifyOut
	if verifyErr != nil && isNotFoundError(verifyOut, verifyErr) {
		res.Verified = true
		return res
	}
	if verifyErr == nil {
		res.Err = fmt.Errorf("job %s/%s still present after deletion", act.Namespace, act.Name)
		return res
	}
	res.RBACDenied = isRBACForbidden(verifyOut, verifyErr)
	res.Err = fmt.Errorf("verify job deletion %s/%s: %w", act.Namespace, act.Name, verifyErr)
	return res
}

func isRBACForbidden(out string, err error) bool {
	if strings.Contains(strings.ToLower(out), "forbidden") {
		return true
	}
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")
}

func isNotFoundError(out string, err error) bool {
	lowOut := strings.ToLower(out)
	if strings.Contains(lowOut, "notfound") || strings.Contains(lowOut, "not found") {
		return true
	}
	if err == nil {
		return false
	}
	lowErr := strings.ToLower(err.Error())
	return strings.Contains(lowErr, "notfound") || strings.Contains(lowErr, "not found")
}
