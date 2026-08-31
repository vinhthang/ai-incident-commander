package minion

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"

	"vinhthang.dev/ai-incident-commander/internal/db"
)

// AppFixer handles alerts coming from the 'apps' namespace or custom application code.
// It tracks incidents in PostgreSQL, deduplicating by Service + Endpoint/Topic + Status Code.
func AppFixer(serviceName, endpointOrTopic, statusCode, traceID, triageAnalysis string) error {
	if db.DB == nil {
		log.Println("⚠️ [AppFixer] Database is not initialized. Cannot track application incident.")
		log.Println("Triage Analysis:\n", triageAnalysis)
		return nil
	}

	// Generate deduplication hash
	hashInput := serviceName + "|" + endpointOrTopic + "|" + statusCode
	hasher := sha256.New()
	hasher.Write([]byte(hashInput))
	incidentHash := hex.EncodeToString(hasher.Sum(nil))

	log.Printf("🤖 [AppFixer] Processing app incident. Service: %s, Endpoint: %s, Status: %s, Hash: %s",
		serviceName, endpointOrTopic, statusCode, incidentHash)

	// Check existing incident
	var state string
	err := db.DB.QueryRow("SELECT state FROM application_incidents WHERE incident_hash = $1", incidentHash).Scan(&state)

	if err == sql.ErrNoRows {
		// New incident
		log.Println("🤖 [AppFixer] New incident detected. Saving to database.")
		_, insertErr := db.DB.Exec(`
			INSERT INTO application_incidents 
			(incident_hash, service_name, endpoint_or_topic, status_code, state, latest_trace_id, last_triage_analysis) 
			VALUES ($1, $2, $3, $4, 'open', $5, $6)`,
			incidentHash, serviceName, endpointOrTopic, statusCode, traceID, triageAnalysis)
		if insertErr != nil {
			log.Printf("🚨 [AppFixer] Failed to insert new incident: %v", insertErr)
			return insertErr
		}
		return nil
	} else if err != nil {
		log.Printf("🚨 [AppFixer] Failed to query database: %v", err)
		return err
	}

	// Existing incident
	if state == "ignored" {
		log.Printf("🤖 [AppFixer] Incident %s is marked as 'ignored'. Dropping silently.", incidentHash)
		return nil
	}

	// Update existing incident
	log.Printf("🤖 [AppFixer] Duplicate incident detected. Incrementing occurrence count.")
	_, updateErr := db.DB.Exec(`
		UPDATE application_incidents 
		SET occurrence_count = occurrence_count + 1, 
		    latest_trace_id = $1, 
		    last_triage_analysis = $2, 
		    updated_at = CURRENT_TIMESTAMP 
		WHERE incident_hash = $3`,
		traceID, triageAnalysis, incidentHash)

	if updateErr != nil {
		log.Printf("🚨 [AppFixer] Failed to update incident: %v", updateErr)
		return updateErr
	}

	return nil
}
