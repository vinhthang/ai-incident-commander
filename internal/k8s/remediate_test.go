package k8s

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func TestParseRuntimeActionSpec(t *testing.T) {
	validTests := []struct {
		name string
		spec string
		want RuntimeAction
	}{
		{
			name: "rollout restart deployment",
			spec: "ROLLOUT_RESTART observability/deployment/grafana",
			want: RuntimeAction{
				Type:      RuntimeRolloutRestart,
				Namespace: "observability",
				Kind:      "deployment",
				Name:      "grafana",
			},
		},
		{
			name: "rollout restart case normalized",
			spec: "rollout_restart apps/StatefulSet/my-app",
			want: RuntimeAction{
				Type:      RuntimeRolloutRestart,
				Namespace: "apps",
				Kind:      "statefulset",
				Name:      "my-app",
			},
		},
		{
			name: "delete failed job",
			spec: "DELETE_FAILED_JOB system/job/daily-123",
			want: RuntimeAction{
				Type:      RuntimeDeleteFailedJob,
				Namespace: "system",
				Kind:      "job",
				Name:      "daily-123",
			},
		},
	}

	for _, tc := range validTests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRuntimeActionSpec(tc.spec)
			if err != nil {
				t.Fatalf("ParseRuntimeActionSpec(%q) unexpected error: %v", tc.spec, err)
			}
			if got.Type != tc.want.Type || got.Namespace != tc.want.Namespace || got.Kind != tc.want.Kind || got.Name != tc.want.Name {
				t.Errorf("ParseRuntimeActionSpec(%q) = %+v, want %+v", tc.spec, got, tc.want)
			}
		})
	}

	invalidSpecs := []string{
		"",
		"RESTART_POD apps/pod/foo",
		"ROLLOUT_RESTART observability/grafana",
		"ROLLOUT_RESTART observability/deployment/grafana extra",
		"DELETE_FAILED_JOB system/deployment/ai-incident-commander",
	}
	for _, spec := range invalidSpecs {
		if _, err := ParseRuntimeActionSpec(spec); err == nil {
			t.Errorf("expected ParseRuntimeActionSpec(%q) to fail", spec)
		}
	}
}

func TestValidateRuntimeAction_BlocksProtectedNamespacesDBsSelfAndInvalidNames(t *testing.T) {
	origEnabled := config.EnableRuntimeRemediation
	origProtected := config.RuntimeProtectedNamespaces
	defer func() {
		config.EnableRuntimeRemediation = origEnabled
		config.RuntimeProtectedNamespaces = origProtected
	}()

	config.EnableRuntimeRemediation = true
	config.RuntimeProtectedNamespaces = []string{"kube-system", "kube-public", "kube-node-lease", "data"}

	deniedCases := []struct {
		name   string
		action RuntimeAction
	}{
		{"kube-system deployment", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "kube-system", Kind: "deployment", Name: "coredns"}},
		{"kube-public deployment", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "kube-public", Kind: "deployment", Name: "helper"}},
		{"kube-node-lease job", RuntimeAction{Type: RuntimeDeleteFailedJob, Namespace: "kube-node-lease", Kind: "job", Name: "cleanup"}},
		{"data namespace", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "data", Kind: "statefulset", Name: "cache"}},
		{"self target deployment", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "system", Kind: "deployment", Name: "ai-incident-commander"}},
		{"postgres workload name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "apps", Kind: "deployment", Name: "app-postgres"}},
		{"mongodb workload name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "operators", Kind: "statefulset", Name: "mongodb-rs0"}},
		{"redis workload name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "apps", Kind: "deployment", Name: "session-redis"}},
		{"pulsar workload name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "apps", Kind: "statefulset", Name: "fleet-pulsar-broker"}},
		{"etcd workload name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "observability", Kind: "statefulset", Name: "etcd-events"}},
		{"flag injection name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "observability", Kind: "deployment", Name: "--all"}},
		{"uppercase invalid name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "observability", Kind: "deployment", Name: "BadName"}},
		{"path traversal namespace", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "../kube", Kind: "deployment", Name: "grafana"}},
		{"empty name", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "observability", Kind: "deployment", Name: ""}},
		{"wrong kind for restart", RuntimeAction{Type: RuntimeRolloutRestart, Namespace: "observability", Kind: "pod", Name: "grafana-0"}},
		{"wrong kind for job delete", RuntimeAction{Type: RuntimeDeleteFailedJob, Namespace: "observability", Kind: "deployment", Name: "grafana"}},
	}

	origRunner := runtimeCmdRunner
	defer func() { runtimeCmdRunner = origRunner }()
	calls := 0
	runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
		calls++
		return "", nil
	}

	for _, tc := range deniedCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRuntimeAction(tc.action); err == nil {
				t.Errorf("expected ValidateRuntimeAction(%+v) to fail", tc.action)
			}
			res := ExecuteRuntimeAction(context.Background(), tc.action)
			if res.Executed || res.Verified || res.DeniedReason == "" || res.Err == nil {
				t.Errorf("expected denied RuntimeActionResult, got %+v", res)
			}
		})
	}

	// Disabled feature flag must also deny before calling kubectl
	config.EnableRuntimeRemediation = false
	res := ExecuteRuntimeAction(context.Background(), RuntimeAction{
		Type:      RuntimeRolloutRestart,
		Namespace: "observability",
		Kind:      "deployment",
		Name:      "grafana",
	})
	if res.Executed || res.DeniedReason == "" || res.Err == nil {
		t.Errorf("expected disabled flag denial, got %+v", res)
	}
	if calls != 0 {
		t.Errorf("expected zero kubectl invocations on denied/disabled targets, got %d", calls)
	}
}

func TestValidateAndExecuteRuntimeAction_RolloutRestart(t *testing.T) {
	origEnabled := config.EnableRuntimeRemediation
	origProtected := config.RuntimeProtectedNamespaces
	origTimeout := config.RuntimeVerifyTimeout
	origRunner := runtimeCmdRunner
	defer func() {
		config.EnableRuntimeRemediation = origEnabled
		config.RuntimeProtectedNamespaces = origProtected
		config.RuntimeVerifyTimeout = origTimeout
		runtimeCmdRunner = origRunner
	}()

	config.EnableRuntimeRemediation = true
	config.RuntimeProtectedNamespaces = []string{"kube-system", "kube-public", "kube-node-lease", "data"}
	config.RuntimeVerifyTimeout = 90 * time.Second

	var recorded [][]string
	runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
		recorded = append(recorded, append([]string{bin}, args...))
		if len(args) >= 4 && args[2] == "rollout" && args[3] == "restart" {
			return "deployment.apps/grafana restarted", nil
		}
		if len(args) >= 4 && args[2] == "rollout" && args[3] == "status" {
			return `deployment "grafana" successfully rolled out`, nil
		}
		return "", errors.New("unexpected command")
	}

	act := RuntimeAction{
		Type:      RuntimeRolloutRestart,
		Namespace: "observability",
		Kind:      "deployment",
		Name:      "grafana",
		Reason:    "SQLite lock recovery",
	}
	res := ExecuteRuntimeAction(context.Background(), act)
	if !res.Executed || !res.Verified || res.Err != nil || res.RBACDenied {
		t.Fatalf("expected verified rollout restart, got %+v", res)
	}
	wantCmds := [][]string{
		{"kubectl", "-n", "observability", "rollout", "restart", "deployment/grafana"},
		{"kubectl", "-n", "observability", "rollout", "status", "deployment/grafana", "--timeout=90s"},
	}
	if !reflect.DeepEqual(recorded, wantCmds) {
		t.Errorf("recorded commands = %v, want %v", recorded, wantCmds)
	}

	// Verify rollout status timeout marks Executed=true, Verified=false
	runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
		if len(args) >= 4 && args[2] == "rollout" && args[3] == "restart" {
			return "deployment.apps/grafana restarted", nil
		}
		return "error: timed out waiting for the condition", errors.New("exit status 1")
	}
	failRes := ExecuteRuntimeAction(context.Background(), act)
	if !failRes.Executed || failRes.Verified || failRes.Err == nil {
		t.Errorf("expected Executed=true, Verified=false, Err!=nil on status failure, got %+v", failRes)
	}

	// Verify RuntimeVerifyTimeout <= 0 falls back to 90s and sub-second uses duration.String()
	for _, tc := range []struct {
		name        string
		timeout     time.Duration
		wantTimeout string
	}{
		{"zero fallback 90s", 0, "--timeout=90s"},
		{"subsecond 500ms", 500 * time.Millisecond, "--timeout=500ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.RuntimeVerifyTimeout = tc.timeout
			var statusArg string
			runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
				if len(args) >= 5 && args[2] == "rollout" && args[3] == "status" {
					statusArg = args[5]
				}
				return "ok", nil
			}
			r := ExecuteRuntimeAction(context.Background(), act)
			if !r.Verified || statusArg != tc.wantTimeout {
				t.Errorf("got Verified=%v statusArg=%q, want Verified=true %q", r.Verified, statusArg, tc.wantTimeout)
			}
		})
	}
}

func TestValidateAndExecuteRuntimeAction_DeleteFailedJob(t *testing.T) {
	origEnabled := config.EnableRuntimeRemediation
	origProtected := config.RuntimeProtectedNamespaces
	origRunner := runtimeCmdRunner
	defer func() {
		config.EnableRuntimeRemediation = origEnabled
		config.RuntimeProtectedNamespaces = origProtected
		runtimeCmdRunner = origRunner
	}()

	config.EnableRuntimeRemediation = true
	config.RuntimeProtectedNamespaces = []string{"kube-system", "kube-public", "kube-node-lease", "data"}

	var recorded [][]string
	getCount := 0
	runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
		recorded = append(recorded, append([]string{bin}, args...))
		if len(args) >= 3 && args[2] == "get" {
			getCount++
			if getCount == 1 {
				return `{"status":{"active":0,"succeeded":0,"failed":1}}`, nil
			}
			return `Error from server (NotFound): jobs.batch "daily-old" not found`, errors.New("exit status 1")
		}
		if len(args) >= 3 && args[2] == "delete" {
			return `job.batch "daily-old" deleted`, nil
		}
		return "", errors.New("unexpected command")
	}

	act := RuntimeAction{
		Type:      RuntimeDeleteFailedJob,
		Namespace: "system",
		Kind:      "job",
		Name:      "daily-old",
		Reason:    "Prune failed job",
	}
	res := ExecuteRuntimeAction(context.Background(), act)
	if !res.Executed || !res.Verified || res.Err != nil {
		t.Fatalf("expected verified failed job deletion, got %+v", res)
	}
	wantCmds := [][]string{
		{"kubectl", "-n", "system", "get", "job", "daily-old", "-o", "json"},
		{"kubectl", "-n", "system", "delete", "job", "daily-old", "--wait=false"},
		{"kubectl", "-n", "system", "get", "job", "daily-old"},
	}
	if !reflect.DeepEqual(recorded, wantCmds) {
		t.Errorf("recorded commands = %v, want %v", recorded, wantCmds)
	}
}

func TestExecuteRuntimeAction_HandlesRBACForbiddenAndActiveJobs(t *testing.T) {
	origEnabled := config.EnableRuntimeRemediation
	origProtected := config.RuntimeProtectedNamespaces
	origRunner := runtimeCmdRunner
	defer func() {
		config.EnableRuntimeRemediation = origEnabled
		config.RuntimeProtectedNamespaces = origProtected
		runtimeCmdRunner = origRunner
	}()

	config.EnableRuntimeRemediation = true
	config.RuntimeProtectedNamespaces = []string{"kube-system", "kube-public", "kube-node-lease", "data"}

	// Active or succeeded Jobs must never execute delete
	for _, tc := range []struct {
		name    string
		jobJSON string
	}{
		{"active job", `{"status":{"active":1,"succeeded":0,"failed":0}}`},
		{"succeeded job", `{"status":{"active":0,"succeeded":1,"failed":0}}`},
		{"zero failed job", `{"status":{"active":0,"succeeded":0,"failed":0}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleteCalled := false
			runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
				if len(args) >= 3 && args[2] == "delete" {
					deleteCalled = true
				}
				return tc.jobJSON, nil
			}
			res := ExecuteRuntimeAction(context.Background(), RuntimeAction{
				Type:      RuntimeDeleteFailedJob,
				Namespace: "system",
				Kind:      "job",
				Name:      "report-run",
			})
			if deleteCalled || res.Executed || res.DeniedReason == "" || res.Err == nil {
				t.Errorf("expected preflight block without delete call for %s, got %+v (deleteCalled=%v)", tc.name, res, deleteCalled)
			}
		})
	}

	// RBAC Forbidden must set RBACDenied=true cleanly
	runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
		return `Error from server (Forbidden): deployments.apps "grafana" is forbidden: User "system:serviceaccount:system:ai-incident-commander" cannot patch resource "deployments" in API group "apps"`, errors.New("exit status 1")
	}
	rbacRes := ExecuteRuntimeAction(context.Background(), RuntimeAction{
		Type:      RuntimeRolloutRestart,
		Namespace: "observability",
		Kind:      "deployment",
		Name:      "grafana",
	})
	if !rbacRes.RBACDenied || rbacRes.Executed || rbacRes.Verified || rbacRes.Err == nil {
		t.Errorf("expected RBACDenied=true, Executed=false, got %+v", rbacRes)
	}
	if !strings.Contains(rbacRes.Output, "Forbidden") {
		t.Errorf("expected RBAC output preserved, got %q", rbacRes.Output)
	}

	// Verify isRBACForbidden also matches error text when command output is empty
	runtimeCmdRunner = func(ctx context.Context, dir, bin string, args ...string) (string, error) {
		return "", errors.New("server rejected: Forbidden")
	}
	rbacErrOnly := ExecuteRuntimeAction(context.Background(), RuntimeAction{
		Type:      RuntimeRolloutRestart,
		Namespace: "observability",
		Kind:      "deployment",
		Name:      "grafana",
	})
	if !rbacErrOnly.RBACDenied || rbacErrOnly.Executed {
		t.Errorf("expected RBACDenied=true from error text when stdout empty, got %+v", rbacErrOnly)
	}
}

func TestConfigHelpers_ParseIntAndCSV(t *testing.T) {
	t.Setenv("DAILY_MAX_PROACTIVE_FIXES", "5")
	if got := config.ParseIntEnv("DAILY_MAX_PROACTIVE_FIXES", 3); got != 5 {
		t.Errorf("ParseIntEnv valid = %d, want 5", got)
	}
	t.Setenv("DAILY_MAX_PROACTIVE_FIXES", "-2")
	if got := config.ParseIntEnv("DAILY_MAX_PROACTIVE_FIXES", 3); got != 3 {
		t.Errorf("ParseIntEnv negative = %d, want fallback 3", got)
	}
	t.Setenv("DAILY_MAX_PROACTIVE_FIXES", "not-an-int")
	if got := config.ParseIntEnv("DAILY_MAX_PROACTIVE_FIXES", 3); got != 3 {
		t.Errorf("ParseIntEnv invalid = %d, want fallback 3", got)
	}

	t.Setenv("RUNTIME_PROTECTED_NAMESPACES", " kube-system , DATA , custom-ns ")
	gotCSV := config.ParseCSVEnv("RUNTIME_PROTECTED_NAMESPACES", []string{"default"})
	wantCSV := []string{"kube-system", "data", "custom-ns"}
	if !reflect.DeepEqual(gotCSV, wantCSV) {
		t.Errorf("ParseCSVEnv = %v, want %v", gotCSV, wantCSV)
	}
}
