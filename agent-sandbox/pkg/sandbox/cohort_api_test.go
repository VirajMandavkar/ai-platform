package sandbox

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCohortAPI_CreateAndAuth(t *testing.T) {
	InitDB(":memory:")
	createHandler := HandleCreateCohort("http://localhost:8081")

	// 1. Create Cohort with 2 authorized emails
	cohortReq := map[string]any{
		"title":               "Q3 Go Senior Screening",
		"scenario_id":         "payments-triage",
		"duration_mins":       45,
		"safe_capacity_seats": 12,
		"candidate_emails":    []string{"alice@example.com", "bob@example.com"},
	}
	body, _ := json.Marshal(cohortReq)

	reqCreate := httptest.NewRequest(http.MethodPost, "/api/admin/cohorts", bytes.NewReader(body))
	recCreate := httptest.NewRecorder()
	createHandler(recCreate, reqCreate)

	if recCreate.Code != http.StatusOK {
		t.Fatalf("expected 200 on cohort create, got %d", recCreate.Code)
	}

	var cohort CohortSession
	_ = json.NewDecoder(recCreate.Body).Decode(&cohort)

	if cohort.ID == "" || cohort.CohortURL == "" {
		t.Fatalf("expected valid cohort ID and URL")
	}

	// 2. Auth with Authorized Email (alice@example.com)
	authReqAlice := map[string]any{
		"cohort_id": cohort.ID,
		"email":     "alice@example.com",
		"name":      "Alice Engineer",
	}
	bodyAlice, _ := json.Marshal(authReqAlice)
	reqAuthAlice := httptest.NewRequest(http.MethodPost, "/api/cohort/auth", bytes.NewReader(bodyAlice))
	recAuthAlice := httptest.NewRecorder()
	HandleCohortAuth(recAuthAlice, reqAuthAlice)

	if recAuthAlice.Code != http.StatusOK {
		t.Fatalf("expected 200 for authorized alice, got %d", recAuthAlice.Code)
	}

	var authResp map[string]any
	_ = json.NewDecoder(recAuthAlice.Body).Decode(&authResp)
	if authResp["authorized"] != true || authResp["session_id"] == "" {
		t.Errorf("expected authorized session for alice, got %v", authResp)
	}

	// 3. Auth with Unauthorized Email (eve@example.com)
	authReqEve := map[string]any{
		"cohort_id": cohort.ID,
		"email":     "eve@example.com",
	}
	bodyEve, _ := json.Marshal(authReqEve)
	reqAuthEve := httptest.NewRequest(http.MethodPost, "/api/cohort/auth", bytes.NewReader(bodyEve))
	recAuthEve := httptest.NewRecorder()
	HandleCohortAuth(recAuthEve, reqAuthEve)

	if recAuthEve.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for unauthorized email, got %d", recAuthEve.Code)
	}
}
