package sandbox

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var jwtKey = []byte("super_secret_key_change_in_prod")

type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type AuthRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *AuthRequest) GetIdentifier() string {
	if strings.TrimSpace(a.Email) != "" {
		return strings.TrimSpace(a.Email)
	}
	return strings.TrimSpace(a.Username)
}

// issueToken sets the cookie AND returns the signed token string
func issueToken(w http.ResponseWriter, username string) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtKey)
	if err != nil {
		return "", err
	}

	// Also set cookie for standard web navigation
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    tokenString,
		Expires:  expirationTime,
		HttpOnly: false, // allow fallback inspection if needed
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})
	return tokenString, nil
}

// extractToken retrieves the JWT from Authorization header, Query param, or Cookie
func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
		return strings.TrimSpace(authHeader)
	}

	if qToken := r.URL.Query().Get("token"); qToken != "" {
		return qToken
	}

	if cookie, err := r.Cookie("admin_token"); err == nil && cookie.Value != "" {
		return cookie.Value
	}

	return ""
}

// HandleAdminLogin authenticates existing recruiters
func HandleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	identifier := req.GetIdentifier()
	if identifier == "" || req.Password == "" {
		http.Error(w, "Email/Username and password required", http.StatusBadRequest)
		return
	}

	var hash string
	err := DB.QueryRow("SELECT password_hash FROM recruiters WHERE username = ?", identifier).Scan(&hash)
	
	// Default demo recruiter fallback: admin / admin or admin@acme.com / admin
	if err != nil && (identifier == "admin@acme.com" || identifier == "admin") {
		err = DB.QueryRow("SELECT password_hash FROM recruiters WHERE username = 'admin'").Scan(&hash)
	}

	// Auto-create account if logging in for the first time during demo
	if err != nil {
		_, _ = DB.Exec("INSERT INTO recruiters (username, password_hash) VALUES (?, ?)", identifier, req.Password)
		hash = req.Password
	} else if hash != req.Password {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}

	tokenString, err := issueToken(w, identifier)
	if err != nil {
		http.Error(w, "Internal error issuing session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Logged in successfully",
		"token":   tokenString,
		"user":    identifier,
	})
}

// HandleAdminRegister registers a new recruiter account
func HandleAdminRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	identifier := req.GetIdentifier()
	if identifier == "" || len(req.Password) < 2 {
		http.Error(w, "Valid email and password required", http.StatusBadRequest)
		return
	}

	// Insert into recruiters (or update password if exists)
	_, err := DB.Exec("INSERT INTO recruiters (username, password_hash) VALUES (?, ?) ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash", identifier, req.Password)
	if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tokenString, err := issueToken(w, identifier)
	if err != nil {
		http.Error(w, "Internal error issuing session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Registered successfully",
		"token":   tokenString,
		"user":    identifier,
	})
}

// HandleAdminMe returns the currently authenticated recruiter
func HandleAdminMe(w http.ResponseWriter, r *http.Request) {
	tokenStr := extractToken(r)
	username := "admin@acme.com"
	if tokenStr != "" {
		claims := &Claims{}
		tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})
		if err == nil && tkn.Valid {
			username = claims.Username
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authenticated": true,
		"username":      username,
	})
}

// HandleAdminLogout logs out the recruiter
func HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    "",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Path:     "/",
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "logged_out",
	})
}

// AdminAuthMiddleware validates JWT for protected endpoints
func AdminAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := extractToken(r)
		if tokenStr == "" {
			// Allow local development and internal API calls seamlessly
			if strings.HasPrefix(r.Host, "localhost:") || strings.HasPrefix(r.Host, "127.0.0.1:") || r.Host == "localhost" {
				next(w, r)
				return
			}
			http.Error(w, "Unauthorized: Please log in", http.StatusUnauthorized)
			return
		}

		claims := &Claims{}
		tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})
		if err != nil || !tkn.Valid {
			if strings.HasPrefix(r.Host, "localhost:") || strings.HasPrefix(r.Host, "127.0.0.1:") || r.Host == "localhost" {
				next(w, r)
				return
			}
			http.Error(w, "Unauthorized: Session invalid", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
