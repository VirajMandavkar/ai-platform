package sandbox

import (
	"errors"
	"sync"
	"time"
)

// Manager holds all active PTY sessions in memory.
// This allows a candidate to reconnect if their Wi-Fi drops.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*PTYSession
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*PTYSession),
	}
}

// CreateAndStart launches a new shell inside our running Docker container.
func (m *Manager) CreateAndStart(sessionID string) (*PTYSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[sessionID]; exists {
		return nil, errors.New("session already exists")
	}

	// The Bridge: Execute bash with the Gateway environment variables forced injected
	session, err := StartSession(
		60*time.Minute,
		"docker", "exec",
		"-it",
		"-e", "ANTHROPIC_AUTH_TOKEN=mock-candidate-token", // Bypasses the Claude login screen
		"-e", "ANTHROPIC_BASE_URL=http://host.docker.internal:8080", // Routes to your Go Proxy
		"ai-sandbox-1",
		"/bin/bash",
	)
	if err != nil {
		return nil, err
	}

	m.sessions[sessionID] = session
	return session, nil
}

// Get retrieves an existing session so a candidate can reconnect.
func (m *Manager) Get(sessionID string) (*PTYSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, exists := m.sessions[sessionID]
	return session, exists
}
