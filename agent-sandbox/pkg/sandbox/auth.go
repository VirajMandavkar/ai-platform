package sandbox

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var jwtKey []byte

func init() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "dev_only_jwt_secret_change_me_32"
		log.Println("[WARNING] JWT_SECRET not set, using insecure default. Set JWT_SECRET env var in production.")
	}
	jwtKey = []byte(secret)
}
type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
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
func issueToken(w http.ResponseWriter, username string, optionalRole ...string) (string, error) {
	role := "recruiter"
	if len(optionalRole) > 0 && optionalRole[0] != "" {
		role = optionalRole[0]
	}

	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtKey)
	if err != nil {
		return "", err
	}

	// Set cookies for browser navigation
	http.SetCookie(w, &http.Cookie{
		Name:     "triagehubs_token",
		Value:    tokenString,
		Expires:  expirationTime,
		HttpOnly: true,
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

	if cookie, err := r.Cookie("triagehubs_token"); err == nil && cookie.Value != "" {
		return cookie.Value
	}

	return ""
}

// GetAuthenticatedUser extracts verified recruiter username and role
func GetAuthenticatedUser(r *http.Request) (string, string) {
	tokenStr := extractToken(r)
	if tokenStr != "" {
		claims := &Claims{}
		tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})
		if err == nil && tkn.Valid && claims.Username != "" {
			role := claims.Role
			if role == "" {
				role = "recruiter"
			}
			return claims.Username, role
		}
	}
	return "", ""
}

// GetAuthenticatedRecruiter extracts the verified recruiter username
func GetAuthenticatedRecruiter(r *http.Request) string {
	u, _ := GetAuthenticatedUser(r)
	return u
}

// HandleAdminLogin authenticates existing recruiters using bcrypt
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

	var hash, role string
	err := DB.QueryRow("SELECT password_hash, COALESCE(role, 'recruiter') FROM recruiters WHERE username = ?", identifier).Scan(&hash, &role)
	if err != nil {
		http.Error(w, "Account not found. Please register or verify invite.", http.StatusUnauthorized)
		return
	}

	// Compare bcrypt hash
	bcryptErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password))
	if bcryptErr != nil {
		http.Error(w, "Invalid password", http.StatusUnauthorized)
		return
	}

	tokenString, err := issueToken(w, identifier, role)
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
		"role":    role,
	})
}


// HandleAdminMe returns the currently authenticated recruiter or false if unauthenticated
func HandleAdminMe(w http.ResponseWriter, r *http.Request) {
	tokenStr := extractToken(r)
	if tokenStr == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": false,
			"username":      "",
			"role":          "",
		})
		return
	}

	claims := &Claims{}
	tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		return jwtKey, nil
	})
	if err != nil || !tkn.Valid || claims.Username == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": false,
			"username":      "",
			"role":          "",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authenticated": true,
		"username":      claims.Username,
		"role":          claims.Role,
	})
}

// HandleAdminLogout logs out the recruiter and clears session cookies
func HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "triagehubs_token",
		Value:    "",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "logged_out",
	})
}

// HandleSystemClean wipes test data from Supabase PostgreSQL (Super Admin only)
func HandleSystemClean(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, role := GetAuthenticatedUser(r)
	if role != "admin" {
		http.Error(w, "Forbidden: Only super admin can reset database", http.StatusForbidden)
		return
	}
	if err := CleanDatabase(); err != nil {
		http.Error(w, "Database clean error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Supabase database wiped clean. Super admin preserved.",
	})
}

// AdminAuthMiddleware validates JWT for protected endpoints
func AdminAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := extractToken(r)
		if tokenStr == "" {
			http.Error(w, "Unauthorized: Please log in", http.StatusUnauthorized)
			return
		}

		claims := &Claims{}
		tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})
		if err != nil || !tkn.Valid || claims.Username == "" {
			http.Error(w, "Unauthorized: Session invalid", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
