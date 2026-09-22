package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type FileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"is_dir"`
	Size     int64       `json:"size,omitempty"`
	Children []*FileNode `json:"children,omitempty"`
}

// HandleGetScenario serves scenario manifest and all cards
func HandleGetScenario(scenarioDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		manifestPath := filepath.Join(scenarioDir, "manifest.json")
		manifestBytes, err := os.ReadFile(manifestPath)
		if err != nil {
			http.Error(w, "Failed to load manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var manifest map[string]any
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			http.Error(w, "Invalid manifest JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Load all cards
		cardsDir := filepath.Join(scenarioDir, "cards")
		cardsList := make([]map[string]any, 0)
		cardFiles := []string{
			"01_context.json",
			"02_architecture.json",
			"03_constraints.json",
			"04_deliverables.json",
		}

		for _, cf := range cardFiles {
			cBytes, err := os.ReadFile(filepath.Join(cardsDir, cf))
			if err == nil {
				var cData map[string]any
				if err := json.Unmarshal(cBytes, &cData); err == nil {
					cardsList = append(cardsList, cData)
				}
			}
		}

		resp := map[string]any{
			"manifest": manifest,
			"cards":    cardsList,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// HandleGetWorkspaceTree returns the tree structure of workspace files
func HandleGetWorkspaceTree(workspaceDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tree, err := buildTree(workspaceDir, "")
		if err != nil {
			http.Error(w, "Failed to build tree: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tree)
	}
}

func buildTree(root, relPath string) ([]*FileNode, error) {
	currentDir := filepath.Join(root, relPath)
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return nil, err
	}

	nodes := make([]*FileNode, 0)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "verify.sh" {
			continue // Skip hidden files and evaluation script
		}

		entryRel := filepath.Join(relPath, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		node := &FileNode{
			Name:  name,
			Path:  entryRel,
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		}

		if entry.IsDir() {
			children, _ := buildTree(root, entryRel)
			node.Children = children
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// HandleGetWorkspaceFile returns file content for code viewer
func HandleGetWorkspaceFile(workspaceDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relPath := r.URL.Query().Get("path")
		if relPath == "" || strings.Contains(relPath, "..") {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}

		fullPath := filepath.Join(workspaceDir, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			http.Error(w, "File not found: "+err.Error(), http.StatusNotFound)
			return
		}

		resp := map[string]any{
			"path":    relPath,
			"content": string(content),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// HandleRunVerification executes the verification script inside the container
func HandleRunVerification() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		
		sessionID := r.URL.Query().Get("sessionId")
		if sessionID == "" {
			sessionID = "default-session"
		}
		containerName := "ai-sandbox-" + sessionID

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "docker", "exec", containerName, "/bin/bash", "/home/sandboxuser/workspace/verify.sh")
		out, err := cmd.CombinedOutput()

		passed := (err == nil)
		if DB != nil {
			status := "IN_PROGRESS"
			if passed {
				status = "COMPLETED"
			}
			_, _ = DB.Exec(`UPDATE interviews SET status = ? WHERE id = ?`, status, sessionID)
		}

		resp := map[string]any{
			"passed":    passed,
			"output":    string(out),
			"timestamp": time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
