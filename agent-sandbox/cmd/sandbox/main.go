package main

import (
	"context"
	"log"
	"net/http"

	"agent-sandbox/pkg/sandbox"
	"agent-sandbox/pkg/telemetry"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for local dev/testing
		return true
	},
}

var mgr = sandbox.NewManager()

func handleWS(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		sessionID = "default-session"
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	// 1. Check if session already exists (reconnection scenario)
	session, exists := mgr.Get(sessionID)
	if !exists {
		log.Printf("Starting new PTY session: %s", sessionID)
		session, err = mgr.CreateAndStart(sessionID)
		if err != nil {
			log.Printf("Failed to spawn PTY session: %v", err)
			return
		}
	} else {
		log.Printf("Reconnected to existing PTY session: %s", sessionID)
	}

	// 2. Launch output pump concurrently (PTY -> WebSocket)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go sandbox.PumpFromPTYToWebsocket(ctx, session.PTY, ws)

	// 3. Block on input pump (WebSocket -> PTY) until the client disconnects
	sandbox.PumpFromWebsocketToPTY(ws, session.PTY)

	log.Printf("Client disconnected from session: %s", sessionID)
}

func main() {
	// Initialize SQLite Database
	sandbox.InitDB("./vibescout.db")

	http.HandleFunc("/ws", handleWS)

	scenarioDir := "./scenarios/payments-triage"
	workspaceDir := "./workspace"

	http.HandleFunc("/api/scenario", sandbox.HandleGetScenario(scenarioDir))
	http.HandleFunc("/api/workspace/tree", sandbox.HandleGetWorkspaceTree(workspaceDir))
	http.HandleFunc("/api/workspace/file", sandbox.HandleGetWorkspaceFile(workspaceDir))
	http.HandleFunc("/api/workspace/verify", sandbox.HandleRunVerification())
	http.HandleFunc("/api/telemetry/event", telemetry.HandlePostEvent)
	
	// Open Telemetry endpoints (could be protected in prod, but keeping simple)
	http.HandleFunc("/api/telemetry/report", telemetry.HandleGetScorecard)
	http.HandleFunc("/api/telemetry/playback", telemetry.HandleGetPlayback)
	http.HandleFunc("/api/cohort/auth", sandbox.HandleCohortAuth)
	http.HandleFunc("/api/interview/verify", sandbox.HandleVerifyCandidate)

	// Admin API - Auth
	http.HandleFunc("/api/admin/login", sandbox.HandleAdminLogin)
	http.HandleFunc("/api/admin/register", sandbox.HandleAdminRegister)
	http.HandleFunc("/api/admin/me", sandbox.HandleAdminMe)
	http.HandleFunc("/api/admin/logout", sandbox.HandleAdminLogout)

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

	http.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/admin.html")
	})

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
