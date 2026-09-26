package sandbox

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func generateSecureID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("[FATAL] crypto/rand failed: %v", err)
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

// CohortSession represents an interview batch for multiple candidates sharing a scenario & key
type CohortSession struct {
	ID                   string            `json:"id"`
	Title                string            `json:"title"`
	ScenarioID           string            `json:"scenario_id"`
	APIKey               string            `json:"api_key,omitempty"`
	DurationMins         int               `json:"duration_mins"`
	SafeCapacitySeats    int               `json:"safe_capacity_seats"`
	AuthorizedEmails     []string          `json:"authorized_emails"`
	RegisteredCandidates map[string]string `json:"registered_candidates"` // email -> sessionId
	RecruiterUsername    string            `json:"recruiter_username,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	CohortURL            string            `json:"cohort_url"`
}

// HandleCreateCohort creates a new candidate batch and master cohort link
func HandleCreateCohort(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Title             string   `json:"title"`
			ScenarioID        string   `json:"scenario_id"`
			APIKey            string   `json:"api_key"`
			DurationMins      int      `json:"duration_mins"`
			SafeCapacitySeats int      `json:"safe_capacity_seats"`
			CandidateEmails   []string `json:"candidate_emails"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("Error: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if req.DurationMins <= 0 {
			req.DurationMins = 45
		}
		if req.SafeCapacitySeats <= 0 {
			req.SafeCapacitySeats = 10
		}

		cleanedEmails := make([]string, 0)
		for _, e := range req.CandidateEmails {
			trimmed := strings.ToLower(strings.TrimSpace(e))
			if trimmed != "" {
				cleanedEmails = append(cleanedEmails, trimmed)
			}
		}

		candidatesCount := len(cleanedEmails)
		if candidatesCount > 0 {
			requiredTPM := int(float64(candidatesCount*10000) * 1.5)
			availableTPM := 2000000
			if req.APIKey == "" {
				availableTPM = 50000000
			}
			if requiredTPM > availableTPM {
				http.Error(w, fmt.Sprintf("API Capacity Warning: Your key supports ~%d concurrent candidates.", availableTPM/15000), http.StatusInsufficientStorage)
				return
			}
		}

		recruiter := GetAuthenticatedRecruiter(r)
		cohortID := generateSecureID("cohort")
		cohortURL := fmt.Sprintf("%s/?cohort=%s", baseURL, cohortID)

		authEmailsJSON, _ := json.Marshal(cleanedEmails)
		regCandJSON, _ := json.Marshal(make(map[string]string))

		// Encrypt API key with AES-256-GCM before database persistence
		storedKey := ""
		if strings.TrimSpace(req.APIKey) != "" {
			enc, err := EncryptAPIKey(strings.TrimSpace(req.APIKey))
			if err == nil {
				storedKey = enc
			}
		}

		_, err := DB.Exec(`INSERT INTO cohorts (id, title, scenario_id, api_key, duration_mins, safe_capacity_seats, authorized_emails, registered_candidates, recruiter_username, created_at, cohort_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			cohortID, req.Title, req.ScenarioID, storedKey, req.DurationMins, req.SafeCapacitySeats, string(authEmailsJSON), string(regCandJSON), recruiter, time.Now(), cohortURL)
		if err != nil {
			log.Printf("Error: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		cohort := &CohortSession{
			ID:                   cohortID,
			Title:                req.Title,
			ScenarioID:           req.ScenarioID,
			APIKey:               MaskAPIKey(storedKey),
			DurationMins:         req.DurationMins,
			SafeCapacitySeats:    req.SafeCapacitySeats,
			AuthorizedEmails:     cleanedEmails,
			RegisteredCandidates: make(map[string]string),
			RecruiterUsername:    recruiter,
			CreatedAt:            time.Now(),
			CohortURL:            cohortURL,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cohort)
	}
}

// HandleListCohorts lists all active cohorts scoped to the authenticated recruiter, or all if Super Admin
func HandleListCohorts(w http.ResponseWriter, r *http.Request) {
	user, role := GetAuthenticatedUser(r)

	var rows *sql.Rows
	var err error
	if role == "admin" {
		rows, err = DB.Query(`SELECT id, title, scenario_id, api_key, duration_mins, safe_capacity_seats, authorized_emails, registered_candidates, COALESCE(recruiter_username, 'admin'), created_at, cohort_url FROM cohorts ORDER BY created_at DESC`)
	} else {
		rows, err = DB.Query(`SELECT id, title, scenario_id, api_key, duration_mins, safe_capacity_seats, authorized_emails, registered_candidates, COALESCE(recruiter_username, 'admin'), created_at, cohort_url FROM cohorts WHERE recruiter_username = ? ORDER BY created_at DESC`, user)
	}

	if err != nil {
		log.Printf("Error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := make([]*CohortSession, 0)
	for rows.Next() {
		var c CohortSession
		var authEmailsStr, regCandStr string
		if err := rows.Scan(&c.ID, &c.Title, &c.ScenarioID, &c.APIKey, &c.DurationMins, &c.SafeCapacitySeats, &authEmailsStr, &regCandStr, &c.RecruiterUsername, &c.CreatedAt, &c.CohortURL); err == nil {
			json.Unmarshal([]byte(authEmailsStr), &c.AuthorizedEmails)
			json.Unmarshal([]byte(regCandStr), &c.RegisteredCandidates)
			c.APIKey = MaskAPIKey(c.APIKey)
			list = append(list, &c)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Error reading cohorts", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// HandleCohortAuth authenticates a candidate entering via the cohort master link
func HandleCohortAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CohortID string `json:"cohort_id"`
		Email    string `json:"email"`
		Name     string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Error: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	cohortID := strings.TrimSpace(req.CohortID)
	email := strings.ToLower(strings.TrimSpace(req.Email))

	var c CohortSession
	var authEmailsStr, regCandStr string
	err := DB.QueryRow(`SELECT id, title, scenario_id, api_key, duration_mins, safe_capacity_seats, authorized_emails, registered_candidates, created_at, cohort_url FROM cohorts WHERE id = ?`, cohortID).
		Scan(&c.ID, &c.Title, &c.ScenarioID, &c.APIKey, &c.DurationMins, &c.SafeCapacitySeats, &authEmailsStr, &regCandStr, &c.CreatedAt, &c.CohortURL)
	if err != nil {
		http.Error(w, "Assessment cohort not found or expired", http.StatusNotFound)
		return
	}
	json.Unmarshal([]byte(authEmailsStr), &c.AuthorizedEmails)
	json.Unmarshal([]byte(regCandStr), &c.RegisteredCandidates)

	authorized := false
	if len(c.AuthorizedEmails) == 0 {
		authorized = true
	} else {
		for _, ae := range c.AuthorizedEmails {
			if strings.EqualFold(ae, email) {
				authorized = true
				break
			}
		}
	}

	if !authorized {
		http.Error(w, "This email address is not authorized for this assessment cohort. Please verify with your recruiter.", http.StatusForbidden)
		return
	}

	sessionID, alreadyRegistered := c.RegisteredCandidates[email]
	if !alreadyRegistered {
		sessionID = generateSecureID("cand")
		c.RegisteredCandidates[email] = sessionID
		regCandJSON, _ := json.Marshal(c.RegisteredCandidates)

		_, _ = DB.Exec(`UPDATE cohorts SET registered_candidates = ? WHERE id = ?`, string(regCandJSON), cohortID)

		name := req.Name
		if name == "" {
			name = strings.Split(email, "@")[0]
		}

		inviteURL := fmt.Sprintf("/?token=%s", sessionID)
		_, _ = DB.Exec(`INSERT INTO interviews (id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, created_at, invite_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionID, name, email, c.ScenarioID, c.Title, c.APIKey, "BYOK", c.DurationMins, "IN_PROGRESS", time.Now(), inviteURL)
	}

	resp := map[string]any{
		"authorized":    true,
		"session_id":    sessionID,
		"scenario_id":   c.ScenarioID,
		"duration_mins": c.DurationMins,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
