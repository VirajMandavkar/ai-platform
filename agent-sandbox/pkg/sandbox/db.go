package sandbox

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDB(dsn string) {
	var err error
	DB, err = sql.Open("sqlite3", dsn)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	createTables := `
	CREATE TABLE IF NOT EXISTS recruiters (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS cohorts (
		id TEXT PRIMARY KEY,
		title TEXT,
		scenario_id TEXT,
		api_key TEXT,
		duration_mins INTEGER,
		safe_capacity_seats INTEGER,
		authorized_emails TEXT,
		registered_candidates TEXT,
		created_at DATETIME,
		cohort_url TEXT
	);
	CREATE TABLE IF NOT EXISTS interviews (
		id TEXT PRIMARY KEY,
		candidate_name TEXT,
		candidate_email TEXT,
		scenario_id TEXT,
		scenario_title TEXT,
		api_key TEXT,
		target_provider TEXT,
		duration_mins INTEGER,
		status TEXT,
		created_at DATETIME,
		invite_url TEXT
	);
	CREATE TABLE IF NOT EXISTS telemetry_sessions (
		session_id TEXT PRIMARY KEY,
		candidate_name TEXT,
		scenario_id TEXT,
		start_time DATETIME,
		end_time DATETIME,
		events TEXT,
		total_prompts INTEGER,
		total_tokens INTEGER,
		verification_runs INTEGER,
		final_passed BOOLEAN,
		integrity_score INTEGER,
		ai_efficiency_index INTEGER,
		verdict TEXT
	);
	CREATE TABLE IF NOT EXISTS telemetry_frames (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		time_offset REAL,
		type TEXT,
		data TEXT,
		FOREIGN KEY(session_id) REFERENCES telemetry_sessions(session_id)
	);
	`
	_, err = DB.Exec(createTables)
	if err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	// Insert dummy recruiter
	_, err = DB.Exec(`INSERT OR IGNORE INTO recruiters (username, password_hash) VALUES ('admin', 'admin')`)
	if err != nil {
		log.Fatalf("Failed to insert dummy recruiter: %v", err)
	}
}
