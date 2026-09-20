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

	// The Bridge: Instead of running `bash` on the host, we execute it inside the locked-down container.
	// Container name MUST match what you passed to `docker run --name` (ai-sandbox-1).
	session, err := StartSession(
		60*time.Minute,
		"docker", "exec", "-it", "ai-sandbox-1", "/bin/bash",
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
