package telemetry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-sandbox/pkg/sandbox"
)

func TestTelemetry_RecordEventsAndScorecard(t *testing.T) {
	sandbox.InitDB(":memory:")
	sessionID := "test_candidate_123"

	st := GetOrCreate(sessionID)
	if st.IntegrityScore != 100 {
		t.Errorf("initial integrity score should be 100, got %d", st.IntegrityScore)
	}

	// Record blur event (-10)
	RecordEvent(sessionID, "BLUR", "Tab switched")
	st = GetOrCreate(sessionID)
	if st.IntegrityScore != 90 {
		t.Errorf("expected score 90 after blur, got %d", st.IntegrityScore)
	}

	// Record lockout (-15)
	RecordEvent(sessionID, "LOCKOUT", "10s penalty")
	st = GetOrCreate(sessionID)
	if st.IntegrityScore != 75 {
		t.Errorf("expected score 75 after lockout, got %d", st.IntegrityScore)
	}

	// Record terminal frame
	RecordTerminalFrame(sessionID, "o", "ls -la\n")

	// Mark as passed and check verdict
	st.FinalPassed = true
	scorecard := st.GenerateScorecard()

	summary := scorecard["candidate_summary"].(map[string]any)
	if summary["verdict"] != "HIRE" {
		t.Errorf("expected verdict HIRE, got %v", summary["verdict"])
	}
}

func TestTelemetry_HTTPHandlers(t *testing.T) {
	sandbox.InitDB(":memory:")
	sessionID := "http_test_sess"

	// 1. Post event
	eventPayload := map[string]string{
		"session_id": sessionID,
		"type":       "BLUR",
		"detail":     "Window blur detected",
	}
	body, _ := json.Marshal(eventPayload)
	req := httptest.NewRequest(http.MethodPost, "/api/telemetry/event", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandlePostEvent(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on event post, got %d", rec.Code)
	}

	// 2. Get Scorecard
	reqScorecard := httptest.NewRequest(http.MethodGet, "/api/telemetry/report?sessionId="+sessionID, nil)
	recScorecard := httptest.NewRecorder()
	HandleGetScorecard(recScorecard, reqScorecard)

	if recScorecard.Code != http.StatusOK {
		t.Errorf("expected 200 on report get, got %d", recScorecard.Code)
	}
}
