package incident

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/db"
)

var (
	cronJobPodSuffixRe    = regexp.MustCompile(`-[0-9]{7,10}-[a-z0-9]{5}$`)
	cronJobSuffixRe       = regexp.MustCompile(`-[0-9]{7,10}$`)
	deploymentPodSuffixRe = regexp.MustCompile(`-[a-z0-9]{8,10}-[a-z0-9]{5}$`)
	stsOrdinalSuffixRe    = regexp.MustCompile(`^(.+?[a-zA-Z].*?)-[0-9]+$`)
)

// NormalizeWorkloadBase strips ephemeral Kubernetes pod and job suffixes to derive the
// stable workload base name for cross-alert GitHub Issue/PR deduplication.
func NormalizeWorkloadBase(target string) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return ""
	}
	if cronJobPodSuffixRe.MatchString(trimmed) {
		return cronJobPodSuffixRe.ReplaceAllString(trimmed, "")
	}
	if cronJobSuffixRe.MatchString(trimmed) {
		return cronJobSuffixRe.ReplaceAllString(trimmed, "")
	}
	if deploymentPodSuffixRe.MatchString(trimmed) {
		return deploymentPodSuffixRe.ReplaceAllString(trimmed, "")
	}
	if matches := stsOrdinalSuffixRe.FindStringSubmatch(trimmed); len(matches) == 2 {
		return matches[1]
	}
	return trimmed
}

// ComputeFingerprint returns a deterministic SHA-256 fingerprint for the incident.
func ComputeFingerprint(alertName, namespace, targetResource string) string {
	raw := namespace + "|" + targetResource + "|" + alertName
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// ExtractNamespace determines the Kubernetes namespace from alert labels,
// prioritizing Prometheus relabeled exported_namespace over namespace.
func ExtractNamespace(labels map[string]string) string {
	if ns := labels["exported_namespace"]; ns != "" {
		return ns
	}
	return labels["namespace"]
}

// IsMetricsScraperLabel reports whether val identifies a Prometheus metrics scraper
// pod or service label that shadows the actual Kubernetes workload target.
func IsMetricsScraperLabel(val string) bool {
	lower := strings.ToLower(strings.TrimSpace(val))
	return strings.HasPrefix(lower, "fleet-kube-state-metrics") ||
		strings.HasPrefix(lower, "kube-state-metrics") ||
		strings.HasPrefix(lower, "victoriametrics")
}

// ExtractTargetResource determines the primary target resource from alert labels.
// Priority order: exported_pod > non-scraper pod > statefulset > deployment > job_name > persistentvolumeclaim > non-scraper service > non-scraper app > http_url > endpoint > url > host > node > instance > "cluster".
func ExtractTargetResource(labels map[string]string) string {
	keys := []string{
		"exported_pod",
		"pod",
		"statefulset",
		"deployment",
		"job_name",
		"persistentvolumeclaim",
		"service",
		"app",
		"http_url",
		"endpoint",
		"url",
		"host",
		"node",
		"instance",
	}
	for _, k := range keys {
		if val, ok := labels[k]; ok && val != "" {
			if (k == "pod" || k == "service" || k == "app") && IsMetricsScraperLabel(val) {
				continue
			}
			if k == "http_url" || k == "url" {
				return cleanURLTarget(val)
			}
			return val
		}
	}
	return "cluster"
}

func cleanURLTarget(rawURL string) string {
	cleaned := strings.TrimPrefix(rawURL, "http://")
	cleaned = strings.TrimPrefix(cleaned, "https://")
	return cleaned
}

// IsEphemeralNodeOutage checks whether an alert is due to a known transient worker reboot / CNI startup issue.
func IsEphemeralNodeOutage(labels map[string]string, telemetry string) bool {
	node := labels["node"]
	if !strings.HasPrefix(node, "k3s-worker-") {
		return false
	}
	if strings.Contains(telemetry, "NodeNotReady") ||
		strings.Contains(telemetry, "flannel") ||
		strings.Contains(telemetry, "FailedCreatePodSandBox") ||
		strings.Contains(telemetry, "NetworkNotReady") {
		return true
	}
	return false
}

// TrackClusterIncident queries the database for existing active cluster incidents.
// If found in 'firing' state, increments occurrence_count and returns isDuplicate = true.
// If not found or in 'resolved' / 'ignored' state, returns isDuplicate = false.
func TrackClusterIncident(fingerprint, alertName, namespace, target string) (isDuplicate bool, existingIssue int, err error) {
	if db.DB == nil {
		return false, 0, nil
	}

	var issueNumber int
	var state string
	err = db.DB.QueryRow("SELECT issue_number, state FROM cluster_incidents WHERE fingerprint = $1", fingerprint).Scan(&issueNumber, &state)
	if err == sql.ErrNoRows {
		return false, 0, nil
	} else if err != nil {
		return false, 0, err
	}

	if state == "firing" {
		_, updateErr := db.DB.Exec("UPDATE cluster_incidents SET occurrence_count = occurrence_count + 1, updated_at = CURRENT_TIMESTAMP WHERE fingerprint = $1", fingerprint)
		if updateErr != nil {
			return true, issueNumber, updateErr
		}
		return true, issueNumber, nil
	}

	return false, 0, nil
}

// RecordClusterIssue inserts or updates a cluster incident with its associated GitHub issue number and state.
func RecordClusterIssue(fingerprint, alertName, namespace, target string, issueNumber int, state string) error {
	if db.DB == nil {
		return nil
	}

	query := `
		INSERT INTO cluster_incidents (fingerprint, alert_name, namespace, target_resource, issue_number, state, occurrence_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (fingerprint) DO UPDATE SET 
			issue_number = EXCLUDED.issue_number, 
			state = EXCLUDED.state, 
			occurrence_count = 1, 
			updated_at = CURRENT_TIMESTAMP;
	`
	_, err := db.DB.Exec(query, fingerprint, alertName, namespace, target, issueNumber, state)
	return err
}

// ResolveClusterIncident marks an active firing incident as resolved and returns the associated issue number.
func ResolveClusterIncident(fingerprint string) (closedIssue int, err error) {
	if db.DB == nil {
		return 0, nil
	}

	var issueNumber int
	err = db.DB.QueryRow("SELECT issue_number FROM cluster_incidents WHERE fingerprint = $1 AND state = 'firing'", fingerprint).Scan(&issueNumber)
	if err == sql.ErrNoRows {
		return 0, nil
	} else if err != nil {
		return 0, err
	}

	_, err = db.DB.Exec("UPDATE cluster_incidents SET state = 'resolved', updated_at = CURRENT_TIMESTAMP WHERE fingerprint = $1", fingerprint)
	if err != nil {
		return 0, err
	}

	return issueNumber, nil
}

// SummarizeRecentIncidents returns a concise summary of recent cluster and application
// incidents within window for inclusion in the daily fleet check report.
func SummarizeRecentIncidents(window time.Duration) string {
	if db.DB == nil {
		return "Database disabled (no persisted 24h incident history)."
	}

	secs := int(window.Seconds())
	var b strings.Builder

	rows, err := db.DB.Query(`
		SELECT alert_name, namespace, target_resource, state, issue_number, occurrence_count
		FROM cluster_incidents
		WHERE updated_at >= NOW() - make_interval(secs => $1)
		ORDER BY updated_at DESC
		LIMIT 20
	`, secs)
	if err != nil {
		return fmt.Sprintf("Failed querying recent cluster incidents: %v", err)
	}
	defer func() { _ = rows.Close() }()

	clusterCount := 0
	b.WriteString("Cluster incidents (last 24h):\n")
	for rows.Next() {
		var alertName, ns, target, st string
		var issueNum, count int
		if scanErr := rows.Scan(&alertName, &ns, &target, &st, &issueNum, &count); scanErr == nil {
			clusterCount++
			fmt.Fprintf(&b, "- %s (%s/%s): state=%s issue=#%d occurrences=%d\n", alertName, ns, target, st, issueNum, count)
		}
	}
	if clusterCount == 0 {
		b.WriteString("- none\n")
	}

	var openApp int
	if appErr := db.DB.QueryRow(`
		SELECT COUNT(*) FROM application_incidents
		WHERE state = 'open' AND updated_at >= NOW() - make_interval(secs => $1)
	`, secs).Scan(&openApp); appErr == nil {
		fmt.Fprintf(&b, "Open application incidents (last 24h): %d\n", openApp)
	}

	return b.String()
}
