package sandbox

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type InterviewSession struct {
	ID                string     `json:"id"`
	CandidateName     string     `json:"candidate_name"`
	CandidateEmail    string     `json:"candidate_email"`
	ScenarioID        string     `json:"scenario_id"`
	ScenarioTitle     string     `json:"scenario_title"`
	APIKey            string     `json:"api_key,omitempty"`
	TargetProvider    string     `json:"target_provider"`
	DurationMins      int        `json:"duration_mins"`
	Status            string     `json:"status"` // "INVITED", "IN_PROGRESS", "COMPLETED", "EXPIRED"
	RecruiterUsername string     `json:"recruiter_username,omitempty"`
	ScheduledAt       *time.Time `json:"scheduled_at,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	InviteURL         string     `json:"invite_url"`
}

// HandleListScenarios lists all available scenario templates
func HandleListScenarios(scenariosBaseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(scenariosBaseDir)
		if err != nil {
			log.Printf("Error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
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
			ID        string           `json:"id"`
			Title     string           `json:"title"`
			Track     string           `json:"track"`
			Duration  int              `json:"duration_minutes"`
			Language  string           `json:"language"`
			Cards     []map[string]any `json:"cards"`
			VerifyCmd string           `json:"verify_command"`
		}

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			log.Printf("Error: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
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
			ScheduledAt    string `json:"scheduled_at"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("Error: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if req.DurationMins <= 0 {
			req.DurationMins = 45
		}

		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			http.Error(w, "Internal error generating session", http.StatusInternalServerError)
			return
		}
		sessionID := fmt.Sprintf("cand_%x", b)

		recruiter := GetAuthenticatedRecruiter(r)

		inviteURL := fmt.Sprintf("%s/sandbox.html?token=%s", baseURL, sessionID)

		// Parse scheduled_at if provided
		var schedTime *time.Time
		var schedVal any
		if strings.TrimSpace(req.ScheduledAt) != "" {
			t, err := time.Parse(time.RFC3339, req.ScheduledAt)
			if err != nil {
				t, err = time.Parse("2006-01-02T15:04", req.ScheduledAt)
			}
			if err == nil {
				schedTime = &t
				schedVal = t
			}
		}

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
			ScheduledAt:       schedTime,
			CreatedAt:         time.Now(),
			InviteURL:         inviteURL,
		}

		_, err := DB.Exec(`INSERT INTO interviews (id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, cohort_id, recruiter_username, scheduled_at, created_at, invite_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			session.ID, session.CandidateName, session.CandidateEmail, session.ScenarioID, session.ScenarioTitle, session.APIKey, session.TargetProvider, session.DurationMins, session.Status, "", session.RecruiterUsername, schedVal, session.CreatedAt, session.InviteURL)
		if err != nil {
			log.Printf("Error: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
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
		rows, err = DB.Query(`SELECT id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, COALESCE(recruiter_username, 'admin'), scheduled_at, expires_at, created_at, invite_url FROM interviews ORDER BY created_at DESC`)
	} else {
		rows, err = DB.Query(`SELECT id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, COALESCE(recruiter_username, 'admin'), scheduled_at, expires_at, created_at, invite_url FROM interviews WHERE recruiter_username = ? ORDER BY created_at DESC`, user)
	}

	if err != nil {
		log.Printf("Error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := make([]*InterviewSession, 0)
	for rows.Next() {
		var s InterviewSession
		var schedAt, expAt sql.NullTime
		if err := rows.Scan(&s.ID, &s.CandidateName, &s.CandidateEmail, &s.ScenarioID, &s.ScenarioTitle, &s.APIKey, &s.TargetProvider, &s.DurationMins, &s.Status, &s.RecruiterUsername, &schedAt, &expAt, &s.CreatedAt, &s.InviteURL); err == nil {
			if schedAt.Valid {
				s.ScheduledAt = &schedAt.Time
			}
			if expAt.Valid {
				s.ExpiresAt = &expAt.Time
			}
			s.APIKey = MaskAPIKey(s.APIKey)
			list = append(list, &s)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Error reading interviews", http.StatusInternalServerError)
		return
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
		if err := rows.Err(); err != nil {
			log.Printf("[platform] error iterating pilot requests: %v", err)
		}
	}

	// Fetch organizations
	recRows, err := DB.Query("SELECT username, role FROM recruiters ORDER BY id DESC LIMIT 20")
	var organizations []map[string]any
	if err == nil {
		defer recRows.Close()
		for recRows.Next() {
			var username, role string
			if err := recRows.Scan(&username, &role); err == nil {
				organizations = append(organizations, map[string]any{
					"username": username,
					"role":     role,
				})
			}
		}
	}

	// Fetch recent interviews globally
	intRows, err := DB.Query("SELECT id, candidate_name, candidate_email, status, recruiter_username FROM interviews ORDER BY id DESC LIMIT 20")
	var globalInterviews []map[string]any
	if err == nil {
		defer intRows.Close()
		for intRows.Next() {
			var id, name, email, status, recruiter string
			if err := intRows.Scan(&id, &name, &email, &status, &recruiter); err == nil {
				globalInterviews = append(globalInterviews, map[string]any{
					"id":                 id,
					"candidate_name":     name,
					"candidate_email":    email,
					"status":             status,
					"recruiter_username": recruiter,
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
		"organizations":        organizations,
		"global_interviews":    globalInterviews,
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
		log.Printf("Error: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	token := strings.TrimSpace(req.Token)
	name := strings.TrimSpace(req.Name)
	email := strings.ToLower(strings.TrimSpace(req.Email))

	if token == "" {
		http.Error(w, "Assessment token is required.", http.StatusBadRequest)
		return
	}

	var candName, candEmail, scenarioTitle, status string
	var durationMins int
	var scheduledAt, expiresAt sql.NullTime
	err := DB.QueryRow(`SELECT candidate_name, candidate_email, scenario_title, status, duration_mins, scheduled_at, expires_at FROM interviews WHERE id = ?`, token).
		Scan(&candName, &candEmail, &scenarioTitle, &status, &durationMins, &scheduledAt, &expiresAt)
	if err == nil {
		if status == "COMPLETED" {
			http.Error(w, "Assessment already completed.", http.StatusForbidden)
			return
		}

		if status == "EXPIRED" {
			http.Error(w, "Assessment start window has expired. Contact your recruiter.", http.StatusForbidden)
			return
		}

		// 5-minute start window check: if scheduled_at is set, candidate must start within 5 minutes of scheduled time
		if scheduledAt.Valid && time.Now().After(scheduledAt.Time.Add(5*time.Minute)) && status == "INVITED" {
			_, _ = DB.Exec(`UPDATE interviews SET status = 'EXPIRED' WHERE id = ?`, token)
			http.Error(w, "Assessment start window has expired (must start within 5 minutes of scheduled time). Please contact your recruiter.", http.StatusForbidden)
			return
		}
		
		if expiresAt.Valid && time.Now().After(expiresAt.Time) {
			_, _ = DB.Exec(`UPDATE interviews SET status = 'COMPLETED' WHERE id = ?`, token)
			http.Error(w, "Assessment time has expired.", http.StatusForbidden)
			return
		}

		// Verify email matches HR registration
		if !strings.EqualFold(strings.TrimSpace(candEmail), email) {
			http.Error(w, "Candidate email does not match the assessment record for this invite.", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"verified":       true,
			"candidate_name": candName,
			"scenario_title": scenarioTitle,
			"duration_mins":  durationMins,
			"status":         status,
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
			"duration_mins":  45,
			"status":         "INVITED",
		})
		return
	}

	http.Error(w, "Assessment session not found or invalid token.", http.StatusNotFound)
}

// HandleGetInterviewDetail returns full session details for session.html
// GET /api/admin/interview/detail?id=cand_...
func HandleGetInterviewDetail(w http.ResponseWriter, r *http.Request) {
	user, role := GetAuthenticatedUser(r)
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "Missing session id parameter", http.StatusBadRequest)
		return
	}

	var s InterviewSession
	var scheduledAt, expiresAt sql.NullTime
	var recruiter string
	err := DB.QueryRow(`SELECT id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, COALESCE(recruiter_username, 'admin'), scheduled_at, expires_at, created_at, invite_url FROM interviews WHERE id = ?`, id).
		Scan(&s.ID, &s.CandidateName, &s.CandidateEmail, &s.ScenarioID, &s.ScenarioTitle, &s.APIKey, &s.TargetProvider, &s.DurationMins, &s.Status, &recruiter, &scheduledAt, &expiresAt, &s.CreatedAt, &s.InviteURL)
	if err != nil {
		http.Error(w, "Assessment session not found", http.StatusNotFound)
		return
	}

	// Recruiter can only view their own sessions; Super Admin can view all
	if role != "admin" && recruiter != user {
		http.Error(w, "Forbidden: You do not have access to view this assessment", http.StatusForbidden)
		return
	}

	s.RecruiterUsername = recruiter
	if scheduledAt.Valid {
		s.ScheduledAt = &scheduledAt.Time
	}
	if expiresAt.Valid {
		s.ExpiresAt = &expiresAt.Time
	}
	s.APIKey = MaskAPIKey(s.APIKey)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

// HandleUpdateInterview updates session details (allowed only while status == 'INVITED')
// POST /api/admin/interview/update?id=cand_...
func HandleUpdateInterview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, role := GetAuthenticatedUser(r)
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "Missing session id parameter", http.StatusBadRequest)
		return
	}

	var currentStatus, currentRecruiter string
	err := DB.QueryRow("SELECT status, COALESCE(recruiter_username, 'admin') FROM interviews WHERE id = ?", id).Scan(&currentStatus, &currentRecruiter)
	if err != nil {
		http.Error(w, "Assessment session not found", http.StatusNotFound)
		return
	}

	if role != "admin" && currentRecruiter != user {
		http.Error(w, "Forbidden: You do not own this assessment session", http.StatusForbidden)
		return
	}

	// Strictly lock editability once session moves beyond INVITED
	if currentStatus != "INVITED" {
		http.Error(w, fmt.Sprintf("Assessment session cannot be edited because it is %s. Edits are only permitted prior to candidate entry.", currentStatus), http.StatusForbidden)
		return
	}

	var req struct {
		CandidateName  string `json:"candidate_name"`
		CandidateEmail string `json:"candidate_email"`
		ScenarioID     string `json:"scenario_id"`
		ScenarioTitle  string `json:"scenario_title"`
		DurationMins   int    `json:"duration_mins"`
		ScheduledAt    string `json:"scheduled_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request JSON", http.StatusBadRequest)
		return
	}

	var schedVal any
	if strings.TrimSpace(req.ScheduledAt) != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduledAt)
		if err != nil {
			t, err = time.Parse("2006-01-02T15:04", req.ScheduledAt)
		}
		if err == nil {
			schedVal = t
		}
	}

	_, err = DB.Exec(`UPDATE interviews SET 
		candidate_name = CASE WHEN ? != '' THEN ? ELSE candidate_name END,
		candidate_email = CASE WHEN ? != '' THEN ? ELSE candidate_email END,
		scenario_id = CASE WHEN ? != '' THEN ? ELSE scenario_id END,
		scenario_title = CASE WHEN ? != '' THEN ? ELSE scenario_title END,
		duration_mins = CASE WHEN ? > 0 THEN ? ELSE duration_mins END,
		scheduled_at = CASE WHEN ? IS NOT NULL THEN ? ELSE scheduled_at END
		WHERE id = ?`,
		req.CandidateName, req.CandidateName,
		req.CandidateEmail, req.CandidateEmail,
		req.ScenarioID, req.ScenarioID,
		req.ScenarioTitle, req.ScenarioTitle,
		req.DurationMins, req.DurationMins,
		schedVal, schedVal, id)
	if err != nil {
		log.Printf("Error updating interview %s: %v", id, err)
		http.Error(w, "Database error updating assessment", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Assessment details updated successfully",
	})
}

func extractZip(zipReader *zip.Reader, destDir string) error {
	cleanDest := filepath.Clean(destDir)
	var totalExtracted int64
	const maxTotalSize = 50 << 20 // 50MB total uncompressed limit
	const maxFileSize = 25 << 20  // 25MB per file limit

	for _, f := range zipReader.File {
		// Reject symlinks to prevent arbitrary file overwrite outside destDir
		if f.Mode()&os.ModeSymlink != 0 {
			log.Printf("[zip] skipping symlink entry %s for security", f.Name)
			continue
		}

		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			continue
		}
		targetPath := filepath.Join(cleanDest, cleanName)
		if !strings.HasPrefix(targetPath, cleanDest+string(os.PathSeparator)) && targetPath != cleanDest {
			continue
		}

		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(targetPath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		// Enforce safe permission mask
		mode := (f.Mode() & 0755) | 0644
		if strings.HasSuffix(targetPath, ".sh") {
			mode = 0755
		}

		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		written, err := io.Copy(outFile, io.LimitReader(rc, maxFileSize))
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}

		totalExtracted += written
		if totalExtracted > maxTotalSize {
			return fmt.Errorf("zip extraction exceeded maximum size limit of %d bytes", maxTotalSize)
		}

		if strings.HasSuffix(targetPath, ".sh") {
			_ = os.Chmod(targetPath, 0755)
		}
	}
	return nil
}

// HandleUploadScenario allows admins/recruiters to create a custom scenario with problem statement, cards, and workspace ZIP
// POST /api/admin/scenarios/upload
func HandleUploadScenario(scenariosBaseDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Allow up to 50MB uploads
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			http.Error(w, "Upload too large or invalid multipart form", http.StatusBadRequest)
			return
		}

		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			http.Error(w, "Scenario title is required", http.StatusBadRequest)
			return
		}

		track := strings.TrimSpace(r.FormValue("track"))
		if track == "" {
			track = "Custom Systems Engineering"
		}

		language := strings.TrimSpace(r.FormValue("language"))
		if language == "" {
			language = "go"
		}

		verifyCmd := strings.TrimSpace(r.FormValue("verify_command"))
		if verifyCmd == "" {
			verifyCmd = "bash verify.sh"
		}

		durationMins := 45
		if dStr := strings.TrimSpace(r.FormValue("duration_minutes")); dStr != "" {
			var d int
			if _, err := fmt.Sscanf(dStr, "%d", &d); err == nil && d > 0 {
				durationMins = d
			}
		}

		// Slugify title for ID
		slug := strings.ToLower(title)
		slug = strings.ReplaceAll(slug, " ", "-")
		var sb strings.Builder
		for _, ch := range slug {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				sb.WriteRune(ch)
			}
		}
		scenarioID := sb.String()
		if scenarioID == "" {
			scenarioID = fmt.Sprintf("custom-%d", time.Now().Unix())
		}

		scenarioDir := filepath.Join(scenariosBaseDir, scenarioID)
		workspaceDir := filepath.Join(scenarioDir, "workspace")
		cardsDir := filepath.Join(scenarioDir, "cards")
		evalDir := filepath.Join(scenarioDir, "evaluation")

		_ = os.MkdirAll(workspaceDir, 0755)
		_ = os.MkdirAll(cardsDir, 0755)
		_ = os.MkdirAll(evalDir, 0755)

		// Unzip workspace zip if uploaded
		file, _, err := r.FormFile("workspace_zip")
		if err == nil && file != nil {
			defer file.Close()
			buf := new(bytes.Buffer)
			_, _ = io.Copy(buf, file)
			zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err == nil {
				_ = extractZip(zipReader, workspaceDir)
			}
		}

		// Write manifest.json
		manifest := map[string]any{
			"id":               scenarioID,
			"title":            title,
			"track":            track,
			"duration_minutes": durationMins,
			"language":         language,
			"verification": map[string]any{
				"command": verifyCmd,
			},
		}
		mBytes, _ := json.MarshalIndent(manifest, "", "  ")
		_ = os.WriteFile(filepath.Join(scenarioDir, "manifest.json"), mBytes, 0644)

		// Write cards
		writeCard := func(filename, id, cardTitle, badge, badgeColor, summary, detail string) {
			if cardTitle == "" {
				cardTitle = "Problem Statement"
			}
			card := map[string]any{
				"id":          id,
				"tab_title":   cardTitle,
				"title":       cardTitle,
				"badge":       badge,
				"badge_color": badgeColor,
				"summary":     summary,
				"stack_trace": detail,
			}
			b, _ := json.MarshalIndent(card, "", "  ")
			_ = os.WriteFile(filepath.Join(cardsDir, filename), b, 0644)
		}

		c1Title := r.FormValue("card1_title")
		if c1Title == "" {
			c1Title = "1. Context"
		}
		c1Sum := r.FormValue("card1_summary")
		if c1Sum == "" {
			c1Sum = r.FormValue("problem_statement")
		}
		writeCard("01_context.json", "card_1", c1Title, "Incident", "#f43f5e", c1Sum, r.FormValue("card1_detail"))

		c2Title := r.FormValue("card2_title")
		if c2Title == "" {
			c2Title = "2. Architecture"
		}
		writeCard("02_architecture.json", "card_2", c2Title, "System Flow", "#6366f1", r.FormValue("card2_summary"), r.FormValue("card2_detail"))

		c3Title := r.FormValue("card3_title")
		if c3Title == "" {
			c3Title = "3. Constraints"
		}
		writeCard("03_constraints.json", "card_3", c3Title, "Rules", "#f59e0b", r.FormValue("card3_summary"), r.FormValue("card3_detail"))

		c4Title := r.FormValue("card4_title")
		if c4Title == "" {
			c4Title = "4. Deliverables"
		}
		writeCard("04_deliverables.json", "card_4", c4Title, "Deliverables", "#10b981", r.FormValue("card4_summary"), r.FormValue("card4_detail"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "ok",
			"scenario_id": scenarioID,
			"title":       title,
			"message":     "Custom assessment scenario and workspace provisioned successfully",
		})
	}
}
