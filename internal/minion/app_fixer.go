package minion

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"

	"vinhthang.dev/ai-incident-commander/internal/db"
)

func getHash(serviceName, endpointOrTopic, statusCode string) string {
	hashInput := serviceName + "|" + endpointOrTopic + "|" + statusCode
	hasher := sha256.New()
	hasher.Write([]byte(hashInput))
	return hex.EncodeToString(hasher.Sum(nil))
}

// TrackAppIncident checks if the incident exists. If it does, it increments the count and returns isDuplicate = true.
// If it doesn't exist, it returns isDuplicate = false, allowing the caller to proceed with LLM Triage.
func TrackAppIncident(serviceName, endpointOrTopic, statusCode, traceID string) (isDuplicate bool, err error) {
	if db.DB == nil {
		log.Println("⚠️ [AppFixer] Database is not initialized. Assuming new incident.")
		return false, nil
	}

	incidentHash := getHash(serviceName, endpointOrTopic, statusCode)

	var state string
	err = db.DB.QueryRow("SELECT state FROM application_incidents WHERE incident_hash = $1", incidentHash).Scan(&state)

	if err == sql.ErrNoRows {
		// New incident
		return false, nil
	} else if err != nil {
		log.Printf("🚨 [AppFixer] Failed to query database: %v", err)
		return false, err
	}

	// Existing incident
	if state == "ignored" {
		log.Printf("🤖 [AppFixer] Incident %s is marked as 'ignored'. Dropping silently.", incidentHash)
		return true, nil
	}

	// Update existing incident
	log.Printf("🤖 [AppFixer] Duplicate incident %s detected. Incrementing occurrence count. Skipping LLM Triage.", incidentHash)
	_, updateErr := db.DB.Exec(`
		UPDATE application_incidents 
		SET occurrence_count = occurrence_count + 1, 
		    latest_trace_id = $1, 
		    updated_at = CURRENT_TIMESTAMP 
		WHERE incident_hash = $2`,
		traceID, incidentHash)

	if updateErr != nil {
		log.Printf("🚨 [AppFixer] Failed to update incident: %v", updateErr)
		return true, updateErr
	}

	return true, nil
}

// SaveNewAppIncident saves a brand new incident to the database after LLM Triage.
func SaveNewAppIncident(serviceName, endpointOrTopic, statusCode, traceID, state, triageAnalysis string) error {
	if db.DB == nil {
		return nil
	}
	incidentHash := getHash(serviceName, endpointOrTopic, statusCode)
	log.Printf("🤖 [AppFixer] Saving new incident (%s) to database. State: %s", incidentHash, state)

	_, insertErr := db.DB.Exec(`
		INSERT INTO application_incidents 
		(incident_hash, service_name, endpoint_or_topic, status_code, state, latest_trace_id, last_triage_analysis) 
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		incidentHash, serviceName, endpointOrTopic, statusCode, state, traceID, triageAnalysis)

	if insertErr != nil {
		log.Printf("🚨 [AppFixer] Failed to insert new incident: %v", insertErr)
		return insertErr
	}
	return nil
}
