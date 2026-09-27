package sandbox

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleActivateInvite_AccountTakeoverProtection(t *testing.T) {
	InitDB(":memory:")

	// 1. Seed an admin account
	_, err := DB.Exec("INSERT INTO recruiters (username, password_hash, role) VALUES ('admin@triagehubs.com', 'hashed_pw', 'admin')")
	if err != nil {
		t.Fatalf("failed to insert existing recruiter: %v", err)
	}

	// 2. Create an invite code for that exact email
	code := "inv_test_takeover"
	_, err = DB.Exec("INSERT INTO recruiter_invites (code, company_name, email, used, created_at) VALUES (?, ?, ?, ?, ?)",
		code, "Attacker Corp", "admin@triagehubs.com", false, time.Now())
	if err != nil {
		t.Fatalf("failed to insert invite: %v", err)
	}

	// 3. Attempt to activate invite with new password
	reqBody, _ := json.Marshal(map[string]string{
		"code":     code,
		"password": "new_attacker_password",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/invite/activate", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleActivateInvite(rec, req)

	// 4. Verify rejection
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when attempting account takeover, got %d", rec.Code)
	}

	// Verify original password hash is unchanged
	var currentHash string
	err = DB.QueryRow("SELECT password_hash FROM recruiters WHERE username = 'admin@triagehubs.com'").Scan(&currentHash)
	if err != nil || currentHash != "hashed_pw" {
		t.Fatalf("password hash was modified! Expected hashed_pw, got %s", currentHash)
	}
}
