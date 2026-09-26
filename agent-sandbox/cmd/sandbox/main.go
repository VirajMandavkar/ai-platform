package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"agent-sandbox/pkg/sandbox"
	"agent-sandbox/pkg/telemetry"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Allow non-browser clients
		}
		allowed := os.Getenv("ALLOWED_ORIGINS")
		if allowed == "" {
			return true // Dev mode: allow all
		}
		for _, o := range strings.Split(allowed, ",") {
			if strings.TrimSpace(o) == origin {
				return true
			}
		}
		return false
	},
}

var mgr = sandbox.NewManager()

func handleWS(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		sessionID = "default-session"
	}
	termID := r.URL.Query().Get("termId")
	if termID == "" {
		termID = "1"
	}
	termType := r.URL.Query().Get("termType")
	if termType == "" {
		if termID == "1" {
			termType = "claude"
		} else {
			termType = "bash"
		}
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	// 1. Get or create PTY terminal session inside container
	session, err := mgr.GetOrCreateTerminal(sessionID, termID, termType)
	if err != nil {
		log.Printf("Failed to spawn PTY session (%s, term: %s, type: %s): %v", sessionID, termID, termType, err)
		return
	}
	log.Printf("Terminal connected: %s (term: %s, type: %s)", sessionID, termID, termType)

	// 2. Launch output pump concurrently (PTY -> WebSocket)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go sandbox.PumpFromPTYToWebsocket(ctx, session.PTY, ws)

	// 3. Block on input pump (WebSocket -> PTY) until the client disconnects
	sandbox.PumpFromWebsocketToPTY(ws, session.PTY)

	log.Printf("Client disconnected from session: %s", sessionID)
}

func loadEnv(filepath string) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), "\"")
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

func main() {
	// Load environment variables from .env
	loadEnv(".env")
	loadEnv("../.env")

	// Initialize Database (Supabase PostgreSQL if DATABASE_URL is set, otherwise SQLite)
	sandbox.InitDB("./triagehubs.db")

	http.HandleFunc("/ws", handleWS)

	scenarioDir := "./scenarios/payments-triage"
	workspaceDir := "./workspace"

	http.HandleFunc("/api/scenario", sandbox.HandleGetScenario(scenarioDir))
	http.HandleFunc("/api/workspace/tree", sandbox.HandleGetWorkspaceTree(workspaceDir))
	http.HandleFunc("/api/workspace/file", sandbox.HandleGetWorkspaceFile(workspaceDir))
	http.HandleFunc("/api/workspace/verify", sandbox.HandleRunVerification())
	http.HandleFunc("/api/workspace/download", sandbox.HandleDownloadWorkspace())
	http.HandleFunc("/api/telemetry/event", telemetry.HandlePostEvent)
	
	// Open Telemetry endpoints (could be protected in prod, but keeping simple)
	http.HandleFunc("/api/telemetry/report", telemetry.HandleGetScorecard)
	http.HandleFunc("/api/telemetry/playback", telemetry.HandleGetPlayback)
	http.HandleFunc("/api/cohort/auth", sandbox.HandleCohortAuth)
	http.HandleFunc("/api/interview/verify", sandbox.HandleVerifyCandidate)

	// Admin API - Auth
	http.HandleFunc("/api/admin/login", sandbox.HandleAdminLogin)
	http.HandleFunc("/api/admin/me", sandbox.HandleAdminMe)
	http.HandleFunc("/api/admin/logout", sandbox.HandleAdminLogout)

	// B2B Enterprise Invite & Pilot Activation API
	http.HandleFunc("/api/admin/invite/verify", sandbox.HandleVerifyInvite)
	http.HandleFunc("/api/admin/invite/activate", sandbox.HandleActivateInvite)
	http.HandleFunc("/api/pilot/request", sandbox.HandlePilotRequest)
	http.HandleFunc("/api/admin/invites/generate", sandbox.AdminAuthMiddleware(sandbox.HandleGenerateInvite))
	http.HandleFunc("/api/admin/invites", sandbox.AdminAuthMiddleware(sandbox.HandleListInvites))

	// Super Admin Platform Overview & Maintenance
	http.HandleFunc("/api/admin/platform/overview", sandbox.AdminAuthMiddleware(sandbox.HandlePlatformOverview))
	http.HandleFunc("/api/admin/system/clean", sandbox.AdminAuthMiddleware(sandbox.HandleSystemClean))

	// Admin API - Protected
	scenariosBaseDir := "./scenarios"
	http.HandleFunc("/api/admin/scenarios", sandbox.AdminAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sandbox.HandleSaveScenario(scenariosBaseDir)(w, r)
		} else {
			sandbox.HandleListScenarios(scenariosBaseDir)(w, r)
		}
	}))
	http.HandleFunc("/api/admin/interviews", sandbox.AdminAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sandbox.HandleCreateInterview("http://localhost:8081")(w, r)
		} else {
			sandbox.HandleListInterviews(w, r)
		}
	}))
	http.HandleFunc("/api/admin/cohorts", sandbox.AdminAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sandbox.HandleCreateCohort("http://localhost:8081")(w, r)
		} else {
			sandbox.HandleListCohorts(w, r)
		}
	}))



	// Serve static files
	fs := http.FileServer(http.Dir("./web"))
	http.Handle("/", fs)

	log.Println("[sandbox] starting server on :8081")
	logHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[HTTP] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		http.DefaultServeMux.ServeHTTP(w, r)
	})
	log.Fatal(http.ListenAndServe(":8081", logHandler))
}
