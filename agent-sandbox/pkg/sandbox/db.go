package sandbox

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

// DBWrapper wraps sql.DB to provide transparent query rebinding (PostgreSQL $1 vs SQLite ?)
type DBWrapper struct {
	*sql.DB
	Driver string
}

func (w *DBWrapper) Exec(query string, args ...any) (sql.Result, error) {
	return w.DB.Exec(Rebind(query, w.Driver), args...)
}

func (w *DBWrapper) Query(query string, args ...any) (*sql.Rows, error) {
	return w.DB.Query(Rebind(query, w.Driver), args...)
}

func (w *DBWrapper) QueryRow(query string, args ...any) *sql.Row {
	return w.DB.QueryRow(Rebind(query, w.Driver), args...)
}

// Rebind converts ? parameter placeholders to $1, $2, ... for PostgreSQL
func Rebind(query string, driver string) string {
	if driver != "postgres" {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 32)
	paramIdx := 1
	inQuote := false
	var quoteChar rune

	for i, r := range query {
		if inQuote {
			b.WriteRune(r)
			if r == quoteChar {
				if quoteChar == '\'' && i+1 < len(query) && rune(query[i+1]) == '\'' {
					continue
				}
				inQuote = false
			}
			continue
		}

		if r == '\'' || r == '"' {
			inQuote = true
			quoteChar = r
			b.WriteRune(r)
			continue
		}

		if r == '?' {
			b.WriteString(fmt.Sprintf("$%d", paramIdx))
			paramIdx++
			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

var DB *DBWrapper

func InitDB(dsn string) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("SUPABASE_DB_URL")
	}

	var rawDB *sql.DB
	var driver string
	var err error

	if strings.HasPrefix(dbURL, "postgres://") || strings.HasPrefix(dbURL, "postgresql://") {
		driver = "postgres"
		log.Printf("[database] Connecting to Supabase PostgreSQL...")
		rawDB, err = sql.Open("postgres", dbURL)
		if err != nil {
			log.Fatalf("Failed to open Supabase PostgreSQL database: %v", err)
		}

		rawDB.SetMaxOpenConns(25)
		rawDB.SetMaxIdleConns(10)
		rawDB.SetConnMaxLifetime(5 * time.Minute)

		if err = rawDB.Ping(); err != nil {
			log.Printf("[database] WARNING: Supabase PostgreSQL ping failed: %v. Fallback to SQLite?", err)
			log.Fatalf("Supabase PostgreSQL connection failed: %v", err)
		}
		log.Printf("[database] Successfully connected to Supabase PostgreSQL!")
	} else {
		driver = "sqlite3"
		log.Printf("[database] Using local SQLite database (dsn: %s)...", dsn)
		rawDB, err = sql.Open("sqlite3", dsn)
		if err != nil {
			log.Fatalf("Failed to open local SQLite database: %v", err)
		}
	}

	DB = &DBWrapper{DB: rawDB, Driver: driver}

	createTables := `
	CREATE TABLE IF NOT EXISTS recruiters (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		role TEXT DEFAULT 'recruiter'
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
		recruiter_username TEXT DEFAULT 'admin',
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
		cohort_id TEXT,
		recruiter_username TEXT DEFAULT 'admin',
		scheduled_at DATETIME,
		expires_at DATETIME,
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
		data TEXT
	);
	CREATE TABLE IF NOT EXISTS recruiter_invites (
		code TEXT PRIMARY KEY,
		company_name TEXT NOT NULL,
		email TEXT NOT NULL,
		used BOOLEAN DEFAULT 0,
		used_by TEXT DEFAULT '',
		created_at DATETIME,
		used_at DATETIME
	);
	CREATE TABLE IF NOT EXISTS pilot_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		full_name TEXT NOT NULL,
		work_email TEXT NOT NULL,
		company_name TEXT NOT NULL,
		team_size TEXT,
		notes TEXT,
		created_at DATETIME
	);
	`

	if driver == "postgres" {
		createTables = `
		CREATE TABLE IF NOT EXISTS recruiters (
			id SERIAL PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role VARCHAR(20) DEFAULT 'recruiter',
			created_at TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS cohorts (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			scenario_id TEXT NOT NULL,
			api_key TEXT,
			duration_mins INTEGER DEFAULT 45,
			safe_capacity_seats INTEGER DEFAULT 1,
			authorized_emails TEXT DEFAULT '[]',
			registered_candidates TEXT DEFAULT '{}',
			recruiter_username TEXT DEFAULT 'admin',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			cohort_url TEXT
		);
		CREATE TABLE IF NOT EXISTS interviews (
			id TEXT PRIMARY KEY,
			candidate_name TEXT NOT NULL,
			candidate_email TEXT NOT NULL,
			scenario_id TEXT NOT NULL,
			scenario_title TEXT,
			api_key TEXT,
			target_provider TEXT DEFAULT 'managed',
			duration_mins INTEGER DEFAULT 45,
			status TEXT DEFAULT 'INVITED',
			cohort_id TEXT,
			recruiter_username TEXT DEFAULT 'admin',
			scheduled_at TIMESTAMPTZ,
			expires_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			invite_url TEXT
		);
		CREATE TABLE IF NOT EXISTS telemetry_sessions (
			session_id TEXT PRIMARY KEY,
			candidate_name TEXT,
			scenario_id TEXT,
			start_time TIMESTAMPTZ DEFAULT NOW(),
			end_time TIMESTAMPTZ,
			events TEXT DEFAULT '[]',
			total_prompts INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			verification_runs INTEGER DEFAULT 0,
			final_passed BOOLEAN DEFAULT FALSE,
			integrity_score INTEGER DEFAULT 100,
			ai_efficiency_index INTEGER DEFAULT 100,
			verdict TEXT DEFAULT 'PENDING'
		);
		CREATE TABLE IF NOT EXISTS telemetry_frames (
			id SERIAL PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_offset DOUBLE PRECISION NOT NULL,
			type TEXT NOT NULL,
			data TEXT NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS recruiter_invites (
			code TEXT PRIMARY KEY,
			company_name TEXT NOT NULL,
			email TEXT NOT NULL,
			used BOOLEAN DEFAULT FALSE,
			used_by TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT NOW(),
			used_at TIMESTAMPTZ
		);
		CREATE TABLE IF NOT EXISTS pilot_requests (
			id SERIAL PRIMARY KEY,
			full_name TEXT NOT NULL,
			work_email TEXT NOT NULL,
			company_name TEXT NOT NULL,
			team_size TEXT,
			notes TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);
		`
	}

	_, err = DB.RawDB().Exec(createTables)
	if err != nil {
		log.Fatalf("Failed to initialize database tables: %v", err)
	}

	// Migrate role column if not present in existing table
	if driver == "postgres" {
		_, _ = DB.RawDB().Exec(`ALTER TABLE recruiters ADD COLUMN IF NOT EXISTS role VARCHAR(20) DEFAULT 'recruiter';`)
	}

	// Bootstrap Super Admin account from environment
	adminPassword := os.Getenv("ADMIN_PASSWORD")
	if adminPassword != "" {
		adminHash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
		if err == nil {
			// Only insert if no admin exists yet
			var count int
			_ = DB.QueryRow(`SELECT COUNT(*) FROM recruiters WHERE role = 'admin'`).Scan(&count)
			if count == 0 {
				_, _ = DB.Exec(`INSERT INTO recruiters (username, password_hash, role) VALUES ('admin@triagehubs.com', ?, 'admin') ON CONFLICT (username) DO NOTHING`, string(adminHash))
				log.Println("[database] Super Admin account bootstrapped from ADMIN_PASSWORD")
			}
		}
	}
}

// CleanDatabase wipes all test records across all tables, keeping only the Super Admin account
func CleanDatabase() error {
	if DB == nil {
		return nil
	}
	if DB.Driver == "postgres" {
		_, err := DB.RawDB().Exec(`
			TRUNCATE interviews, cohorts, telemetry_sessions, telemetry_frames, recruiter_invites, pilot_requests CASCADE;
			DELETE FROM recruiters WHERE username != 'admin@triagehubs.com';
		`)
		return err
	}
	_, _ = DB.Exec(`DELETE FROM interviews;`)
	_, _ = DB.Exec(`DELETE FROM cohorts;`)
	_, _ = DB.Exec(`DELETE FROM telemetry_sessions;`)
	_, _ = DB.Exec(`DELETE FROM telemetry_frames;`)
	_, _ = DB.Exec(`DELETE FROM recruiter_invites;`)
	_, _ = DB.Exec(`DELETE FROM pilot_requests;`)
	_, _ = DB.Exec(`DELETE FROM recruiters WHERE username != 'admin@triagehubs.com';`)
	return nil
}

// RawDB returns underlying *sql.DB for low-level access if needed
func (w *DBWrapper) RawDB() *sql.DB {
	return w.DB
}
