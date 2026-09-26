package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type InviteVerifyResponse struct {
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
	Email       string `json:"email,omitempty"`
	CompanyName string `json:"company_name,omitempty"`
}

type InviteActivateRequest struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

type PilotLeadRequest struct {
	FullName    string `json:"fullName"`
	WorkEmail   string `json:"workEmail"`
	CompanyName string `json:"companyName"`
	TeamSize    string `json:"teamSize"`
	Notes       string `json:"notes"`
}

type InviteRecord struct {
	Code        string     `json:"code"`
	CompanyName string     `json:"company_name"`
	Email       string     `json:"email"`
	Used        bool       `json:"used"`
	UsedBy      string     `json:"used_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
}

func generateInviteCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("[FATAL] crypto/rand failed: %v", err)
	}
	return fmt.Sprintf("inv_%s", hex.EncodeToString(b))
}

// HandleVerifyInvite validates whether an invite code exists and has not been redeemed.
// GET /api/admin/invite/verify?code=inv_...
func HandleVerifyInvite(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	w.Header().Set("Content-Type", "application/json")

	if code == "" {
		_ = json.NewEncoder(w).Encode(InviteVerifyResponse{
			Valid: false,
			Error: "Invite token parameter is missing",
		})
		return
	}

	var email, companyName string
	var used bool

	err := DB.QueryRow("SELECT company_name, email, used FROM recruiter_invites WHERE code = ?", code).Scan(&companyName, &email, &used)
	if err != nil {
		_ = json.NewEncoder(w).Encode(InviteVerifyResponse{
			Valid: false,
			Error: "Invalid or unrecognized invite token",
		})
		return
	}

	if used {
		_ = json.NewEncoder(w).Encode(InviteVerifyResponse{
			Valid: false,
			Error: "This invite has already been activated and claimed",
		})
		return
	}

	_ = json.NewEncoder(w).Encode(InviteVerifyResponse{
		Valid:       true,
		Email:       email,
		CompanyName: companyName,
	})
}

// HandleActivateInvite sets the password for an invited organization and issues a session.
// POST /api/admin/invite/activate
func HandleActivateInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req InviteActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" || len(req.Password) < 3 {
		http.Error(w, "Valid invite code and password (min 3 characters) required", http.StatusBadRequest)
		return
	}

	var email, companyName string
	var used bool

	err := DB.QueryRow("SELECT company_name, email, used FROM recruiter_invites WHERE code = ?", req.Code).Scan(&companyName, &email, &used)
	if err != nil {
		http.Error(w, "Invite code not found or expired", http.StatusBadRequest)
		return
	}

	if used {
		http.Error(w, "This invite token has already been activated", http.StatusConflict)
		return
	}

	// 1. Hash password with bcrypt
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to hash password: "+err.Error(), http.StatusInternalServerError)
		return
	}
	passwordHash := string(hashedBytes)

	// 2. Insert or update recruiter account
	_, err = DB.Exec("INSERT INTO recruiters (username, password_hash) VALUES (?, ?) ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash", email, passwordHash)
	if err != nil {
		http.Error(w, "Failed to create recruiter workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Mark invite code as used
	now := time.Now()
	_, _ = DB.Exec("UPDATE recruiter_invites SET used = ?, used_by = ?, used_at = ? WHERE code = ?", true, email, now, req.Code)

	// 4. Issue session token
	tokenString, err := issueToken(w, email)
	if err != nil {
		http.Error(w, "Internal error issuing session", http.StatusInternalServerError)
		return
	}

	log.Printf("[invites] successfully activated workspace for company=%s email=%s with invite=%s", companyName, email, req.Code)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"message":      "Workspace activated successfully",
		"token":        tokenString,
		"user":         email,
		"company_name": companyName,
	})
}

// HandlePilotRequest records enterprise pilot requests and prepares an invite token.
// POST /api/pilot/request
func HandlePilotRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PilotLeadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	req.WorkEmail = strings.TrimSpace(req.WorkEmail)
	req.CompanyName = strings.TrimSpace(req.CompanyName)
	if req.WorkEmail == "" || req.CompanyName == "" {
		http.Error(w, "Work email and company name are required", http.StatusBadRequest)
		return
	}

	now := time.Now()
	_, err := DB.Exec("INSERT INTO pilot_requests (full_name, work_email, company_name, team_size, notes, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		req.FullName, req.WorkEmail, req.CompanyName, req.TeamSize, req.Notes, now,
	)
	if err != nil {
		log.Printf("[pilot] failed to insert pilot request: %v", err)
	}

	// Pre-generate an invite code for this organization so it is ready for approval
	inviteCode := generateInviteCode()
	_, _ = DB.Exec("INSERT INTO recruiter_invites (code, company_name, email, used, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (code) DO NOTHING",
		inviteCode, req.CompanyName, req.WorkEmail, false, now,
	)

	log.Printf("[pilot] new pilot request received from company=%s email=%s (pre-generated invite: %s)", req.CompanyName, req.WorkEmail, inviteCode)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Your pilot request has been received. Our enterprise team will send your activation link shortly.",
	})
}

// HandleGenerateInvite allows authenticated recruiters/admins to issue new invite links.
// POST /api/admin/invites/generate
func HandleGenerateInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CompanyName string `json:"company_name"`
		Email       string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.CompanyName = strings.TrimSpace(req.CompanyName)
	req.Email = strings.TrimSpace(req.Email)
	if req.CompanyName == "" || req.Email == "" {
		http.Error(w, "Company name and email required", http.StatusBadRequest)
		return
	}

	code := generateInviteCode()
	now := time.Now()
	_, err := DB.Exec("INSERT INTO recruiter_invites (code, company_name, email, used, created_at) VALUES (?, ?, ?, ?, ?)",
		code, req.CompanyName, req.Email, false, now,
	)
	if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"code":         code,
		"invite_url":   fmt.Sprintf("/login.html?invite=%s", code),
		"company_name": req.CompanyName,
		"email":        req.Email,
	})
}

// HandleListInvites returns all generated invites for the admin console.
// GET /api/admin/invites
func HandleListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := DB.Query("SELECT code, company_name, email, used, COALESCE(used_by, ''), created_at, used_at FROM recruiter_invites ORDER BY created_at DESC")
	if err != nil {
		http.Error(w, "Failed to query invites: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var invites []InviteRecord
	for rows.Next() {
		var inv InviteRecord
		var usedAt sqlNullTime
		if err := rows.Scan(&inv.Code, &inv.CompanyName, &inv.Email, &inv.Used, &inv.UsedBy, &inv.CreatedAt, &usedAt); err == nil {
			if usedAt.Valid {
				inv.UsedAt = &usedAt.Time
			}
			invites = append(invites, inv)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Error reading invites", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(invites)
}

type sqlNullTime struct {
	Time  time.Time
	Valid bool
}

func (nt *sqlNullTime) Scan(value any) error {
	if value == nil {
		nt.Time, nt.Valid = time.Time{}, false
		return nil
	}
	switch v := value.(type) {
	case time.Time:
		nt.Time, nt.Valid = v, true
		return nil
	case []byte:
		t, err := time.Parse(time.RFC3339, string(v))
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04:05", string(v))
		}
		nt.Time, nt.Valid = t, err == nil
		return nil
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t, err = time.Parse("2006-01-02 15:04:05", v)
		}
		nt.Time, nt.Valid = t, err == nil
		return nil
	}
	return nil
}
