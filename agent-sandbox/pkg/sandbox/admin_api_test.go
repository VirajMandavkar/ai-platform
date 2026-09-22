package sandbox

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAdminAPI_ListAndSaveScenarios(t *testing.T) {
	tempBase := t.TempDir()

	// 1. Save new scenario
	savePayload := map[string]any{
		"id":               "custom-test-1",
		"title":            "Custom Test Title",
		"track":            "DevOps",
		"duration_minutes": 30,
		"language":         "bash",
		"verify_command":   "echo pass",
		"cards": []map[string]any{
			{"id": "c1", "tab_title": "1. Context", "title": "Context title"},
		},
	}
	body, _ := json.Marshal(savePayload)

	saveHandler := HandleSaveScenario(tempBase)
	reqSave := httptest.NewRequest(http.MethodPost, "/api/admin/scenarios", bytes.NewReader(body))
	recSave := httptest.NewRecorder()
	saveHandler(recSave, reqSave)

	if recSave.Code != http.StatusOK {
		t.Fatalf("expected 200 on save, got %d", recSave.Code)
	}

	// 2. List scenarios
	listHandler := HandleListScenarios(tempBase)
	reqList := httptest.NewRequest(http.MethodGet, "/api/admin/scenarios", nil)
	recList := httptest.NewRecorder()
	listHandler(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("expected 200 on list, got %d", recList.Code)
	}

	var results []map[string]any
	_ = json.NewDecoder(recList.Body).Decode(&results)
	if len(results) != 1 {
		t.Fatalf("expected 1 scenario in list, got %d", len(results))
	}
	if results[0]["title"] != "Custom Test Title" {
		t.Errorf("unexpected scenario title: %v", results[0]["title"])
	}

	// Verify manifest exists on disk
	mPath := filepath.Join(tempBase, "custom-test-1", "manifest.json")
	if _, err := os.Stat(mPath); err != nil {
		t.Errorf("manifest file was not written: %v", err)
	}
}

func TestAdminAPI_CreateAndListInterviews(t *testing.T) {
	InitDB(":memory:")
	createHandler := HandleCreateInterview("http://localhost:8081")

	intPayload := map[string]any{
		"candidate_name":  "Alice Engineer",
		"candidate_email": "alice@example.com",
		"scenario_id":     "payments-triage",
		"scenario_title":  "Payment Triage",
		"target_provider": "DeepSeek-V3",
		"duration_mins":   45,
	}
	body, _ := json.Marshal(intPayload)

	reqCreate := httptest.NewRequest(http.MethodPost, "/api/admin/interviews", bytes.NewReader(body))
	recCreate := httptest.NewRecorder()
	createHandler(recCreate, reqCreate)

	if recCreate.Code != http.StatusOK {
		t.Fatalf("expected 200 on create interview, got %d", recCreate.Code)
	}

	var created InterviewSession
	_ = json.NewDecoder(recCreate.Body).Decode(&created)

	if created.CandidateName != "Alice Engineer" {
		t.Errorf("expected Alice Engineer, got %s", created.CandidateName)
	}
	if created.InviteURL == "" {
		t.Errorf("expected invite URL to be generated")
	}

	// List interviews
	reqList := httptest.NewRequest(http.MethodGet, "/api/admin/interviews", nil)
	recList := httptest.NewRecorder()
	HandleListInterviews(recList, reqList)

	var list []*InterviewSession
	_ = json.NewDecoder(recList.Body).Decode(&list)
	if len(list) == 0 {
		t.Errorf("expected at least 1 interview listed")
	}
}
