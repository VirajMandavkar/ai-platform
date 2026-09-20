package main

import (
	"log"
	"net/http"

	"agent-sandbox/pkg/sandbox"

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
	go sandbox.PumpFromPTYToWebsocket(session.PTY, ws)

	// 3. Block on input pump (WebSocket -> PTY) until the client disconnects
	sandbox.PumpFromWebsocketToPTY(ws, session.PTY)

	log.Printf("Client disconnected from session: %s", sessionID)
	// Notice: We deliberately do NOT close session.PTY here so the container session survives drops!
}

func main() {
	http.HandleFunc("/ws", handleWS)

	// Serve static files (we will put index.html here for xterm.js)
	fs := http.FileServer(http.Dir("./web"))
	http.Handle("/", fs)

	log.Println("Sandbox daemon listening on :8081...")
	if err := http.ListenAndServe(":8081", nil); err != nil {
		log.Fatalf("Server crashed: %v", err)
	}
}
