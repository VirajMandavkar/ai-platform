package telemetry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"agent-sandbox/pkg/sandbox"
)

// ProctorEvent represents security & integrity occurrences during the screen
type ProctorEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`      // "BLUR", "FOCUS", "LOCKOUT", "PASTE", "FULLSCREEN_EXIT"
	Detail    string    `json:"detail"`
	DurationS float64   `json:"duration_s,omitempty"`
}

// TerminalFrame represents an Asciinema v2 format frame [time_offset, "o", data]
type TerminalFrame struct {
	TimeOffsetSec float64 `json:"t"`
	Type          string  `json:"type"` // "o" for output, "i" for input
	Data          string  `json:"data"`
}

// SessionTelemetry aggregates all metrics for a candidate interview
type SessionTelemetry struct {
	SessionID         string          `json:"session_id"`
	CandidateName     string          `json:"candidate_name"`
	ScenarioID        string          `json:"scenario_id"`
	StartTime         time.Time       `json:"start_time"`
	EndTime           *time.Time      `json:"end_time,omitempty"`
	Events            []ProctorEvent  `json:"events"`
	Frames            []TerminalFrame `json:"frames"`
	TotalPrompts      int             `json:"total_prompts"`
	TotalTokens       int             `json:"total_tokens"`
	VerificationRuns  int             `json:"verification_runs"`
	FinalPassed       bool            `json:"final_passed"`
	IntegrityScore    int             `json:"integrity_score"` // 0 - 100
	AIEfficiencyIndex int             `json:"ai_efficiency_index"` // 0 - 100
	Verdict           string          `json:"verdict"` // "STRONG_HIRE", "HIRE", "LEAN_NO_HIRE", "NO_HIRE"
}

func GetOrCreate(sessionID string) *SessionTelemetry {
	var st SessionTelemetry
	var eventsStr string
	err := sandbox.DB.QueryRow(`SELECT session_id, candidate_name, scenario_id, start_time, events, total_prompts, total_tokens, verification_runs, final_passed, integrity_score, ai_efficiency_index, verdict FROM telemetry_sessions WHERE session_id = ?`, sessionID).
		Scan(&st.SessionID, &st.CandidateName, &st.ScenarioID, &st.StartTime, &eventsStr, &st.TotalPrompts, &st.TotalTokens, &st.VerificationRuns, &st.FinalPassed, &st.IntegrityScore, &st.AIEfficiencyIndex, &st.Verdict)
	
	if err == sql.ErrNoRows {
		st = SessionTelemetry{
			SessionID:         sessionID,
			CandidateName:     "Candidate",
			ScenarioID:        "payments-triage-v1",
			StartTime:         time.Now(),
			Events:            make([]ProctorEvent, 0),
			Frames:            make([]TerminalFrame, 0),
			IntegrityScore:    100,
			AIEfficiencyIndex: 85,
			Verdict:           "PENDING",
		}
		eventsJSON, _ := json.Marshal(st.Events)
		sandbox.DB.Exec(`INSERT INTO telemetry_sessions (session_id, candidate_name, scenario_id, start_time, events, total_prompts, total_tokens, verification_runs, final_passed, integrity_score, ai_efficiency_index, verdict) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			st.SessionID, st.CandidateName, st.ScenarioID, st.StartTime, string(eventsJSON), st.TotalPrompts, st.TotalTokens, st.VerificationRuns, st.FinalPassed, st.IntegrityScore, st.AIEfficiencyIndex, st.Verdict)
		return &st
	}
	
	json.Unmarshal([]byte(eventsStr), &st.Events)
	return &st
}

func RecordEvent(sessionID, eventType, detail string) {
	st := GetOrCreate(sessionID)

	st.Events = append(st.Events, ProctorEvent{
		Timestamp: time.Now(),
		Type:      eventType,
		Detail:    detail,
	})

	// Penalize integrity score
	switch eventType {
	case "LOCKOUT":
		st.IntegrityScore -= 15
	case "BLUR", "FULLSCREEN_EXIT":
		st.IntegrityScore -= 5
	case "PASTE":
		st.IntegrityScore -= 2
	}
	if st.IntegrityScore < 0 {
		st.IntegrityScore = 0
	}

	eventsJSON, _ := json.Marshal(st.Events)
	sandbox.DB.Exec(`UPDATE telemetry_sessions SET events = ?, integrity_score = ? WHERE session_id = ?`, string(eventsJSON), st.IntegrityScore, sessionID)
}

func RecordTerminalFrame(sessionID, frameType, data string) {
	st := GetOrCreate(sessionID)
	offset := time.Since(st.StartTime).Seconds()

	sandbox.DB.Exec(`INSERT INTO telemetry_frames (session_id, time_offset, type, data) VALUES (?, ?, ?, ?)`, sessionID, offset, frameType, data)
}

// GenerateScorecard computes candidate report
func (st *SessionTelemetry) GenerateScorecard() map[string]any {
	// Compute verdict
	if st.FinalPassed && st.IntegrityScore >= 85 {
		st.Verdict = "STRONG_HIRE"
	} else if st.FinalPassed && st.IntegrityScore >= 65 {
		st.Verdict = "HIRE"
	} else if st.FinalPassed {
		st.Verdict = "LEAN_NO_HIRE" // passed tests but high integrity penalties
	} else {
		st.Verdict = "NO_HIRE"
	}

	durationMins := time.Since(st.StartTime).Minutes()

	return map[string]any{
		"candidate_summary": map[string]any{
			"session_id":          st.SessionID,
			"verdict":             st.Verdict,
			"test_passed":         st.FinalPassed,
			"duration_minutes":    fmt.Sprintf("%.1f", durationMins),
			"integrity_score":     fmt.Sprintf("%d / 100", st.IntegrityScore),
			"ai_efficiency_index": fmt.Sprintf("%d / 100", st.AIEfficiencyIndex),
		},
		"integrity_timeline": st.Events,
		"ai_collaboration": map[string]any{
			"total_prompts":     st.TotalPrompts,
			"total_tokens_used": st.TotalTokens,
			"verification_runs": st.VerificationRuns,
		},
	}
}

// HandlePostEvent logs browser proctoring events
func HandlePostEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Type      string `json:"type"`
		Detail    string `json:"detail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	RecordEvent(req.SessionID, req.Type, req.Detail)
	w.WriteHeader(http.StatusOK)
}

// HandleGetScorecard serves HR candidate evaluation report
func HandleGetScorecard(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		sessionID = "default"
	}

	st := GetOrCreate(sessionID)
	scorecard := st.GenerateScorecard()

	sandbox.DB.Exec(`UPDATE telemetry_sessions SET verdict = ? WHERE session_id = ?`, st.Verdict, sessionID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(scorecard)
}

// HandleGetPlayback serves terminal playback frames
func HandleGetPlayback(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "Missing sessionId", http.StatusBadRequest)
		return
	}

	rows, err := sandbox.DB.Query(`SELECT time_offset, type, data FROM telemetry_frames WHERE session_id = ? ORDER BY time_offset ASC`, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var frames []TerminalFrame
	for rows.Next() {
		var f TerminalFrame
		if err := rows.Scan(&f.TimeOffsetSec, &f.Type, &f.Data); err == nil {
			frames = append(frames, f)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(frames)
}
