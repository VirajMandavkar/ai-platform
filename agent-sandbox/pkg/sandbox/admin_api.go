package sandbox

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type InterviewSession struct {
	ID             string    `json:"id"`
	CandidateName  string    `json:"candidate_name"`
	CandidateEmail string    `json:"candidate_email"`
	ScenarioID     string    `json:"scenario_id"`
	ScenarioTitle  string    `json:"scenario_title"`
	APIKey         string    `json:"api_key,omitempty"`
	TargetProvider string    `json:"target_provider"`
	DurationMins      int       `json:"duration_mins"`
	Status            string    `json:"status"` // "INVITED", "IN_PROGRESS", "COMPLETED"
	RecruiterUsername string    `json:"recruiter_username,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	InviteURL         string    `json:"invite_url"`
}

// HandleListScenarios lists all available scenario templates
func HandleListScenarios(scenariosBaseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(scenariosBaseDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		results := make([]map[string]any, 0)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			manifestPath := filepath.Join(scenariosBaseDir, entry.Name(), "manifest.json")
			mBytes, err := os.ReadFile(manifestPath)
			if err == nil {
				var manifest map[string]any
				if err := json.Unmarshal(mBytes, &manifest); err == nil {
					manifest["dir_name"] = entry.Name()
					results = append(results, manifest)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(results)
	}
}

// HandleSaveScenario creates or updates a custom assessment scenario
func HandleSaveScenario(scenariosBaseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var payload struct {
			ID          string           `json:"id"`
			Title       string           `json:"title"`
			Track       string           `json:"track"`
			Duration    int              `json:"duration_minutes"`
			Language    string           `json:"language"`
			Cards       []map[string]any `json:"cards"`
			VerifyCmd   string           `json:"verify_command"`
		}

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if payload.ID == "" {
			payload.ID = fmt.Sprintf("custom-scenario-%d", time.Now().Unix())
		}
		
		// Prevent path traversal
		cleanID := filepath.Clean(payload.ID)
		if strings.Contains(cleanID, "..") || strings.Contains(cleanID, "/") || strings.Contains(cleanID, "\\") {
			http.Error(w, "Invalid scenario ID", http.StatusBadRequest)
			return
		}

		scenarioDir := filepath.Join(scenariosBaseDir, cleanID)
		cardsDir := filepath.Join(scenarioDir, "cards")
		workspaceDir := filepath.Join(scenarioDir, "workspace")
		evaluationDir := filepath.Join(scenarioDir, "evaluation")

		_ = os.MkdirAll(cardsDir, 0755)
		_ = os.MkdirAll(workspaceDir, 0755)
		_ = os.MkdirAll(evaluationDir, 0755)

		// 1. Write manifest.json
		manifest := map[string]any{
			"id":               payload.ID,
			"title":            payload.Title,
			"track":            payload.Track,
			"duration_minutes": payload.Duration,
			"language":         payload.Language,
			"verification": map[string]any{
				"command": payload.VerifyCmd,
			},
		}
		mBytes, _ := json.MarshalIndent(manifest, "", "  ")
		_ = os.WriteFile(filepath.Join(scenarioDir, "manifest.json"), mBytes, 0644)

		// 2. Write cards
		cardFiles := []string{
			"01_context.json",
			"02_architecture.json",
			"03_constraints.json",
			"04_deliverables.json",
		}
		for i, cardData := range payload.Cards {
			if i < len(cardFiles) {
				cBytes, _ := json.MarshalIndent(cardData, "", "  ")
				_ = os.WriteFile(filepath.Join(cardsDir, cardFiles[i]), cBytes, 0644)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "created",
			"id":      payload.ID,
			"message": "Scenario saved successfully",
		})
	}
}

// HandleCreateInterview generates candidate invite token and records interview
func HandleCreateInterview(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			CandidateName  string `json:"candidate_name"`
			CandidateEmail string `json:"candidate_email"`
			ScenarioID     string `json:"scenario_id"`
			ScenarioTitle  string `json:"scenario_title"`
			APIKey         string `json:"api_key"`
			TargetProvider string `json:"target_provider"`
			DurationMins   int    `json:"duration_mins"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if req.DurationMins <= 0 {
			req.DurationMins = 45
		}

		b := make([]byte, 8)
		_, _ = rand.Read(b)
		sessionID := fmt.Sprintf("cand_%x", b)

		recruiter := GetAuthenticatedRecruiter(r)

		inviteURL := fmt.Sprintf("%s/sandbox.html?token=%s", baseURL, sessionID)

		// Encrypt API key with AES-256-GCM before database persistence
		storedKey := ""
		if strings.TrimSpace(req.APIKey) != "" {
			enc, err := EncryptAPIKey(strings.TrimSpace(req.APIKey))
			if err == nil {
				storedKey = enc
			}
		}

		session := &InterviewSession{
			ID:                sessionID,
			CandidateName:     req.CandidateName,
			CandidateEmail:    req.CandidateEmail,
			ScenarioID:        req.ScenarioID,
			ScenarioTitle:     req.ScenarioTitle,
			APIKey:            storedKey,
			TargetProvider:    req.TargetProvider,
			DurationMins:      req.DurationMins,
			Status:            "INVITED",
			RecruiterUsername: recruiter,
			CreatedAt:         time.Now(),
			InviteURL:         inviteURL,
		}

		_, err := DB.Exec(`INSERT INTO interviews (id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, cohort_id, recruiter_username, created_at, invite_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			session.ID, session.CandidateName, session.CandidateEmail, session.ScenarioID, session.ScenarioTitle, session.APIKey, session.TargetProvider, session.DurationMins, session.Status, "", session.RecruiterUsername, session.CreatedAt, session.InviteURL)
		if err != nil {
			http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Never return raw or cipher API key in JSON response
		session.APIKey = MaskAPIKey(storedKey)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(session)
	}
}

// HandleListInterviews lists interviews scoped to the authenticated recruiter, or all if Super Admin
func HandleListInterviews(w http.ResponseWriter, r *http.Request) {
	user, role := GetAuthenticatedUser(r)

	var rows *sql.Rows
	var err error
	if role == "admin" {
		rows, err = DB.Query(`SELECT id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, COALESCE(recruiter_username, 'admin'), created_at, invite_url FROM interviews ORDER BY created_at DESC`)
	} else {
		rows, err = DB.Query(`SELECT id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, COALESCE(recruiter_username, 'admin'), created_at, invite_url FROM interviews WHERE recruiter_username = ? ORDER BY created_at DESC`, user)
	}

	if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := make([]*InterviewSession, 0)
	for rows.Next() {
		var s InterviewSession
		if err := rows.Scan(&s.ID, &s.CandidateName, &s.CandidateEmail, &s.ScenarioID, &s.ScenarioTitle, &s.APIKey, &s.TargetProvider, &s.DurationMins, &s.Status, &s.RecruiterUsername, &s.CreatedAt, &s.InviteURL); err == nil {
			s.APIKey = MaskAPIKey(s.APIKey)
			list = append(list, &s)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// HandlePlatformOverview provides system-wide telemetry and lead metrics (Super Admin only)
func HandlePlatformOverview(w http.ResponseWriter, r *http.Request) {
	_, role := GetAuthenticatedUser(r)
	if role != "admin" {
		http.Error(w, "Unauthorized: Super Admin access required", http.StatusForbidden)
		return
	}

	var totalInterviews, totalCohorts, totalRecruiters, totalPilotRequests int
	_ = DB.QueryRow("SELECT COUNT(*) FROM interviews").Scan(&totalInterviews)
	_ = DB.QueryRow("SELECT COUNT(*) FROM cohorts").Scan(&totalCohorts)
	_ = DB.QueryRow("SELECT COUNT(*) FROM recruiters").Scan(&totalRecruiters)
	_ = DB.QueryRow("SELECT COUNT(*) FROM pilot_requests").Scan(&totalPilotRequests)

	// Fetch recent pilot requests
	rows, err := DB.Query("SELECT id, full_name, work_email, company_name, team_size, notes, created_at FROM pilot_requests ORDER BY created_at DESC LIMIT 20")
	var requests []map[string]any
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int
			var fullName, workEmail, companyName, teamSize, notes string
			var createdAt time.Time
			if err := rows.Scan(&id, &fullName, &workEmail, &companyName, &teamSize, &notes, &createdAt); err == nil {
				requests = append(requests, map[string]any{
					"id":           id,
					"full_name":    fullName,
					"work_email":   workEmail,
					"company_name": companyName,
					"team_size":    teamSize,
					"notes":        notes,
					"created_at":   createdAt,
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_interviews":     totalInterviews,
		"total_cohorts":        totalCohorts,
		"total_recruiters":     totalRecruiters,
		"total_pilot_requests": totalPilotRequests,
		"pilot_requests":       requests,
	})
}

// HandleVerifyCandidate checks candidate identity against the session before granting sandbox entry
func HandleVerifyCandidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token string `json:"token"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	token := strings.TrimSpace(req.Token)
	name := strings.TrimSpace(req.Name)
	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Allow demo tokens without strict match
	if strings.HasPrefix(token, "demo") || strings.HasPrefix(token, "cand_demo") || token == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"verified":       true,
			"candidate_name": name,
			"scenario_title": "Fix Priority Queue Slot Leak (Go)",
		})
		return
	}

	var candName, candEmail, scenarioTitle string
	err := DB.QueryRow(`SELECT candidate_name, candidate_email, scenario_title FROM interviews WHERE id = ?`, token).
		Scan(&candName, &candEmail, &scenarioTitle)
	if err == nil {
		// Verify email matches HR registration
		if !strings.EqualFold(strings.TrimSpace(candEmail), email) {
			http.Error(w, "Candidate email does not match the assessment record for this invite.", http.StatusForbidden)
			return
		}

		// Transition status to IN_PROGRESS upon verification
		_, _ = DB.Exec(`UPDATE interviews SET status = 'IN_PROGRESS' WHERE id = ? AND status = 'INVITED'`, token)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"verified":       true,
			"candidate_name": candName,
			"scenario_title": scenarioTitle,
		})
		return
	}

	// Also check cohorts table if token matches a cohort ID
	var cohortTitle, scenarioID, authEmailsStr string
	cohortErr := DB.QueryRow(`SELECT title, scenario_id, authorized_emails FROM cohorts WHERE id = ?`, token).
		Scan(&cohortTitle, &scenarioID, &authEmailsStr)
	if cohortErr == nil {
		var authorizedEmails []string
		_ = json.Unmarshal([]byte(authEmailsStr), &authorizedEmails)
		authorized := len(authorizedEmails) == 0
		for _, ae := range authorizedEmails {
			if strings.EqualFold(strings.TrimSpace(ae), email) {
				authorized = true
				break
			}
		}
		if !authorized {
			http.Error(w, "Candidate email is not on the authorized cohort roster for this assessment.", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"verified":       true,
			"candidate_name": name,
			"scenario_title": cohortTitle,
		})
		return
	}

	http.Error(w, "Assessment session not found or invalid token.", http.StatusNotFound)
}

