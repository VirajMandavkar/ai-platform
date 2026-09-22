package sandbox

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// HandleGetWorkspaceFile returns file content for code viewer or saves file edits
func HandleGetWorkspaceFile(workspaceDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var payload struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			if payload.Path == "" || strings.Contains(payload.Path, "..") {
				http.Error(w, "Invalid path", http.StatusBadRequest)
				return
			}
			fullPath := filepath.Join(workspaceDir, payload.Path)
			if err := os.WriteFile(fullPath, []byte(payload.Content), 0644); err != nil {
				http.Error(w, "Failed to save file: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "saved",
				"path":    payload.Path,
				"message": "File saved successfully to workspace",
			})
			return
		}

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

// HandleRunVerification executes the verification script inside the container with seed assurance
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

		// Ensure the session workspace directory exists and is properly seeded with verify.sh
		workspacePath, _ := filepath.Abs(fmt.Sprintf("./workspaces/%s", sessionID))
		if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
			_ = os.MkdirAll(workspacePath, 0755)
		}
		seedWorkspace(workspacePath)

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// Attempt 1: Run inside docker container
		cmd := exec.CommandContext(ctx, "docker", "exec", containerName, "/bin/bash", "-c", "cd /home/sandboxuser/workspace && if [ -f verify.sh ]; then bash verify.sh; else go test -v -race ./...; fi")
		out, err := cmd.CombinedOutput()

		// Attempt 2: If docker exec fails or container not running, run directly against workspacePath
		if err != nil && (len(out) == 0 || strings.Contains(string(out), "No such container") || strings.Contains(string(out), "No such file")) {
			localCmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("cd %s && (bash verify.sh 2>/dev/null || go test -v -race ./...)", workspacePath))
			if localOut, localErr := localCmd.CombinedOutput(); len(localOut) > 0 {
				out = localOut
				err = localErr
			}
		}

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

// HandleDownloadWorkspace streams a .zip archive of the candidate's workspace code
func HandleDownloadWorkspace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("sessionId")
		if sessionID == "" {
			sessionID = "default-session"
		}

		workspacePath := fmt.Sprintf("./workspaces/%s", sessionID)
		if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
			workspacePath = "./workspace"
		}

		buf := new(bytes.Buffer)
		zipWriter := zip.NewWriter(buf)

		_ = filepath.Walk(workspacePath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}

			relPath, err := filepath.Rel(workspacePath, path)
			if err != nil {
				return nil
			}
			// Skip hidden files or verification runner
			if strings.HasPrefix(relPath, ".") || relPath == "verify.sh" {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			f, err := zipWriter.Create(relPath)
			if err != nil {
				return err
			}
			_, _ = f.Write(data)
			return nil
		})

		_ = zipWriter.Close()

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"solution-%s.zip\"", sessionID))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
		_, _ = w.Write(buf.Bytes())
	}
}
