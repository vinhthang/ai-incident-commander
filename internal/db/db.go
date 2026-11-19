package db

import (
	"database/sql"
	"log"
	"os"

	_ "github.com/lib/pq"
)

var DB *sql.DB

func InitDB() {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		log.Println("⚠️ [Database] DB_DSN is not set. Database integration disabled.")
		return
	}

	var err error
	DB, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("🚨 [Database] Failed to open database: %v", err)
	}

	if err := DB.Ping(); err != nil {
		log.Fatalf("🚨 [Database] Failed to ping database: %v", err)
	}

	log.Println("✅ [Database] Connected to PostgreSQL successfully.")
	runMigrations()
}

func runMigrations() {
	query := `
	CREATE TABLE IF NOT EXISTS application_incidents (
		incident_hash VARCHAR(255) PRIMARY KEY,
		service_name VARCHAR(255) NOT NULL,
		endpoint_or_topic VARCHAR(255) NOT NULL,
		status_code VARCHAR(50),
		state VARCHAR(50) DEFAULT 'open',
		occurrence_count INT DEFAULT 1,
		latest_trace_id VARCHAR(255),
		last_triage_analysis TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS cluster_incidents (
		fingerprint VARCHAR(255) PRIMARY KEY,
		alert_name VARCHAR(255) NOT NULL,
		namespace VARCHAR(255) NOT NULL,
		target_resource VARCHAR(255) NOT NULL,
		issue_number INT DEFAULT 0,
		state VARCHAR(50) DEFAULT 'firing',
		occurrence_count INT DEFAULT 1,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`

	_, err := DB.Exec(query)
	if err != nil {
		log.Fatalf("🚨 [Database] Failed to run migrations: %v", err)
	}

	cleanupQuery := `
	DELETE FROM application_incidents
	WHERE (service_name LIKE 'fleet-kube-state-metrics%' OR service_name LIKE 'kube-state-metrics%')
	  AND endpoint_or_topic = ''
	  AND state = 'open';

	UPDATE cluster_incidents
	SET state = 'resolved', updated_at = CURRENT_TIMESTAMP
	WHERE target_resource LIKE 'fleet-kube-state-metrics%'
	  AND state = 'firing';
	`
	if _, err := DB.Exec(cleanupQuery); err != nil {
		log.Printf("⚠️ [Database] Failed to run stale scraper cleanup: %v", err)
	}

	log.Println("✅ [Database] Schema migrations applied successfully.")
}
