package sandbox

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager holds all active PTY sessions in memory.
// This allows candidates to reconnect if their connection drops,
// and supports multiple concurrent terminal tabs within the same container.
type Manager struct {
	mu         sync.RWMutex
	sessions   map[string]*PTYSession // key: "sessionID:termID" (or "sessionID" for legacy)
	containers map[string]string      // sessionID -> containerName
}

func NewManager() *Manager {
	return &Manager{
		sessions:   make(map[string]*PTYSession),
		containers: make(map[string]string),
	}
}

// CreateAndStart launches an isolated shell inside a Docker container (defaults to term 1: claude).
func (m *Manager) CreateAndStart(sessionID string) (*PTYSession, error) {
	return m.GetOrCreateTerminal(sessionID, "1", "claude")
}

// GetOrCreateTerminal retrieves an existing terminal session or creates a new one inside the session container.
// termType can be "claude" (for AI pair programmer CLI) or "bash" (for direct tests, git, curl shell).
func (m *Manager) GetOrCreateTerminal(sessionID, termID, termType string) (*PTYSession, error) {
	if termID == "" {
		termID = "1"
	}
	if termType == "" {
		if termID == "1" {
			termType = "claude"
		} else {
			termType = "bash"
		}
	}

	sessionKey := fmt.Sprintf("%s:%s", sessionID, termID)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already active
	if session, exists := m.sessions[sessionKey]; exists {
		return session, nil
	}
	if termID == "1" {
		if session, exists := m.sessions[sessionID]; exists {
			m.sessions[sessionKey] = session
			return session, nil
		}
	}

	// 1. Ensure target Docker container exists for this sessionID
	containerName, exists := m.containers[sessionID]
	if !exists || containerName == "" {
		containerName = fmt.Sprintf("ai-sandbox-%s", sessionID)

		// Create unique workspace directory for isolation
		workspacePath, _ := filepath.Abs(fmt.Sprintf("./workspaces/%s", sessionID))
		_ = os.MkdirAll(workspacePath, 0755)
		seedWorkspace(workspacePath)

		// Clean up any stale container with the same name before running
		_ = exec.Command("docker", "rm", "-f", containerName).Run()

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
		}
		m.containers[sessionID] = containerName
	}

	// 2. Fetch Anthropic API key if DB exists and Claude CLI requested
	apiKey := "dummy"
	if DB != nil {
		var dbApiKey string
		err := DB.QueryRow(`
			SELECT COALESCE(NULLIF(interviews.api_key, ''), cohorts.api_key, '') 
			FROM interviews 
			LEFT JOIN cohorts ON interviews.cohort_id = cohorts.id 
			WHERE interviews.id = ?
		`, sessionID).Scan(&dbApiKey)
		if err == nil && dbApiKey != "" {
			decKey, decErr := DecryptAPIKey(dbApiKey)
			if decErr == nil && decKey != "" {
				apiKey = decKey
			} else {
				apiKey = dbApiKey
			}
		}
	}

	// 3. Build exec command based on terminal type
	var execArgs []string
	if termType == "bash" {
		// Standard interactive bash shell for tests, git, curl
		execArgs = []string{
			"exec", "-it",
			"-e", "TERM=xterm-256color",
			containerName,
			"/bin/bash", "-c", "cd /home/sandboxuser/workspace && exec /bin/bash",
		}
	} else {
		// Claude Code CLI agent
		execArgs = []string{
			"exec", "-it",
			"-e", "TERM=xterm-256color",
			"-e", "ANTHROPIC_BASE_URL=http://host.docker.internal:8080",
			"-e", "ANTHROPIC_API_KEY=" + apiKey,
			containerName,
			"/bin/bash", "-c", "cd /home/sandboxuser/workspace && claude --dangerously-skip-permissions",
		}
	}

	session, err := StartSession(60*time.Minute, "docker", execArgs...)
	if err != nil {
		log.Printf("[sandbox] failed to start PTY session for %s (term: %s, type: %s): %v", sessionID, termID, termType, err)
		return nil, err
	}

	// Wait for process in background to reap zombies and clean session map
	go func(sKey string, sess *PTYSession) {
		_ = sess.Cmd.Wait()
		sess.Close()
		m.mu.Lock()
		delete(m.sessions, sKey)
		m.mu.Unlock()
	}(sessionKey, session)

	m.sessions[sessionKey] = session
	if termID == "1" {
		m.sessions[sessionID] = session
	}

	return session, nil
}

// Get retrieves an existing session so a candidate can reconnect.
func (m *Manager) Get(sessionID string, optionalTermID ...string) (*PTYSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	termID := "1"
	if len(optionalTermID) > 0 && optionalTermID[0] != "" {
		termID = optionalTermID[0]
	}

	sessionKey := fmt.Sprintf("%s:%s", sessionID, termID)
	if session, exists := m.sessions[sessionKey]; exists {
		return session, true
	}
	session, exists := m.sessions[sessionID]
	return session, exists
}

// CloseTerminal terminates an individual terminal tab without killing the container
func (m *Manager) CloseTerminal(sessionID, termID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionKey := fmt.Sprintf("%s:%s", sessionID, termID)
	if session, exists := m.sessions[sessionKey]; exists {
		session.Close()
		delete(m.sessions, sessionKey)
	}
	if termID == "1" {
		delete(m.sessions, sessionID)
	}
}

// TerminateSession tears down all PTY sessions for the sessionID and cleans up the container
func (m *Manager) TerminateSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := sessionID + ":"
	for key, session := range m.sessions {
		if key == sessionID || strings.HasPrefix(key, prefix) {
			session.Close()
			delete(m.sessions, key)
		}
	}

	if containerName, exists := m.containers[sessionID]; exists {
		log.Printf("[sandbox] tearing down dynamic container %s", containerName)
		if containerName != "ai-sandbox-1" {
			_ = exec.Command("docker", "rm", "-f", containerName).Run()
		}
		delete(m.containers, sessionID)
	}
}

// seedWorkspace populates a candidate workspace with scenario files and verify.sh
func seedWorkspace(dst string) {
	_ = os.MkdirAll(dst, 0755)

	// Source directories to check for scenario files
	srcDirs := []string{
		"./scenarios/payments-triage/workspace",
		"./workspace",
	}
	for _, src := range srcDirs {
		if entries, err := os.ReadDir(src); err == nil && len(entries) > 0 {
			for _, entry := range entries {
				srcFile := filepath.Join(src, entry.Name())
				dstFile := filepath.Join(dst, entry.Name())
				if !entry.IsDir() {
					if data, err := os.ReadFile(srcFile); err == nil {
						_ = os.WriteFile(dstFile, data, 0644)
					}
				}
			}
			break
		}
	}

	// Always ensure verify.sh is present with executable permissions
	verifySources := []string{
		"./scenarios/payments-triage/evaluation/verify.sh",
		"./workspace/verify.sh",
	}
	for _, vs := range verifySources {
		if data, err := os.ReadFile(vs); err == nil {
			_ = os.WriteFile(filepath.Join(dst, "verify.sh"), data, 0755)
			break
		}
	}
}
