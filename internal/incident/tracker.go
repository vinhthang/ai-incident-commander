package incident

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"

	"vinhthang.dev/ai-incident-commander/internal/db"
)

// ComputeFingerprint returns a deterministic SHA-256 fingerprint for the incident.
func ComputeFingerprint(alertName, namespace, targetResource string) string {
	raw := namespace + "|" + targetResource + "|" + alertName
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// ExtractTargetResource determines the primary target resource from alert labels.
// Priority order: pod > statefulset > deployment > service > app > instance > "cluster".
func ExtractTargetResource(labels map[string]string) string {
	keys := []string{"pod", "statefulset", "deployment", "service", "app", "instance"}
	for _, k := range keys {
		if val, ok := labels[k]; ok && val != "" {
			return val
		}
	}
	return "cluster"
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
