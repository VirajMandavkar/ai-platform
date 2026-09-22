package sandbox

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Manager holds all active PTY sessions in memory.
// This allows a candidate to reconnect if their Wi-Fi drops.
type Manager struct {
	mu         sync.RWMutex
	sessions   map[string]*PTYSession
	containers map[string]string // sessionID -> containerName
}

func NewManager() *Manager {
	return &Manager{
		sessions:   make(map[string]*PTYSession),
		containers: make(map[string]string),
	}
}

// CreateAndStart launches an isolated shell inside a Docker container.
func (m *Manager) CreateAndStart(sessionID string) (*PTYSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[sessionID]; exists {
		return nil, errors.New("session already exists")
	}

	containerName := fmt.Sprintf("ai-sandbox-%s", sessionID)
	
	// Create unique workspace directory for isolation
	workspacePath, _ := filepath.Abs(fmt.Sprintf("./workspaces/%s", sessionID))
	_ = os.MkdirAll(workspacePath, 0755)

	// 1. Try launching dynamic container for session isolation
	startCmd := exec.Command("docker", "run", "-d",
		"--name", containerName,
		"--add-host=host.docker.internal:host-gateway",
		"-v", fmt.Sprintf("%s:/home/sandboxuser/workspace", workspacePath),
		"ai-sandbox-image",
	)
	if out, err := startCmd.CombinedOutput(); err != nil {
		log.Printf("[sandbox] dynamic container spawn failed (%v: %s), falling back to ai-sandbox-1", err, string(out))
		containerName = "ai-sandbox-1"
	} else {
		log.Printf("[sandbox] spawned dynamic container: %s", containerName)
		m.containers[sessionID] = containerName
	}

	apiKey := "dummy"
	if DB != nil {
		var dbApiKey string
		err := DB.QueryRow("SELECT cohorts.api_key FROM interviews JOIN cohorts ON interviews.cohort_id = cohorts.id WHERE interviews.id = ?", sessionID).Scan(&dbApiKey)
		if err == nil && dbApiKey != "" {
			apiKey = dbApiKey
		}
	}

	// 2. Start PTY session inside target container with warm Claude agent
	// Note: We deliberately remove the bash fallback to enforce Vibe Coding lockdown
	session, err := StartSession(
		60*time.Minute,
		"docker", "exec",
		"-it",
		"-e", "ANTHROPIC_BASE_URL=http://host.docker.internal:8080",
		"-e", "ANTHROPIC_API_KEY="+apiKey,
		containerName,
		"/bin/bash", "-c", "cd /home/sandboxuser/workspace && claude --dangerously-skip-permissions",
	)
	if err != nil {
		// Clean up if dynamic container was spawned
		if containerName != "ai-sandbox-1" {
			_ = exec.Command("docker", "rm", "-f", containerName).Run()
		}
		return nil, err
	}

	// Wait for process in background to reap zombies
	go func() {
		_ = session.Cmd.Wait()
	}()

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

// TerminateSession tears down the PTY and cleans up the container
func (m *Manager) TerminateSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, exists := m.sessions[sessionID]; exists {
		if session.cancelCtx != nil {
			session.cancelCtx()
		}
		delete(m.sessions, sessionID)
	}

	if containerName, exists := m.containers[sessionID]; exists {
		log.Printf("[sandbox] tearing down dynamic container %s", containerName)
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
		delete(m.containers, sessionID)
	}
}
