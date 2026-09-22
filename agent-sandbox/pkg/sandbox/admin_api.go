package sandbox

import (
	"crypto/rand"
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
	DurationMins   int       `json:"duration_mins"`
	Status         string    `json:"status"` // "INVITED", "IN_PROGRESS", "COMPLETED"
	CreatedAt      time.Time `json:"created_at"`
	InviteURL      string    `json:"invite_url"`
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

		inviteURL := fmt.Sprintf("%s/sandbox.html?token=%s", baseURL, sessionID)

		session := &InterviewSession{
			ID:             sessionID,
			CandidateName:  req.CandidateName,
			CandidateEmail: req.CandidateEmail,
			ScenarioID:     req.ScenarioID,
			ScenarioTitle:  req.ScenarioTitle,
			APIKey:         req.APIKey,
			TargetProvider: req.TargetProvider,
			DurationMins:   req.DurationMins,
			Status:         "INVITED",
			CreatedAt:      time.Now(),
			InviteURL:      inviteURL,
		}

		_, err := DB.Exec(`INSERT INTO interviews (id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, created_at, invite_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			session.ID, session.CandidateName, session.CandidateEmail, session.ScenarioID, session.ScenarioTitle, session.APIKey, session.TargetProvider, session.DurationMins, session.Status, session.CreatedAt, session.InviteURL)
		if err != nil {
			http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(session)
	}
}

// HandleListInterviews lists all interviews
func HandleListInterviews(w http.ResponseWriter, r *http.Request) {
	rows, err := DB.Query(`SELECT id, candidate_name, candidate_email, scenario_id, scenario_title, api_key, target_provider, duration_mins, status, created_at, invite_url FROM interviews ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := make([]*InterviewSession, 0)
	for rows.Next() {
		var s InterviewSession
		if err := rows.Scan(&s.ID, &s.CandidateName, &s.CandidateEmail, &s.ScenarioID, &s.ScenarioTitle, &s.APIKey, &s.TargetProvider, &s.DurationMins, &s.Status, &s.CreatedAt, &s.InviteURL); err == nil {
			list = append(list, &s)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}
