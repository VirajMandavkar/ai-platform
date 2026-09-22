package sandbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceAPI_GetScenario(t *testing.T) {
	tempDir := t.TempDir()
	cardsDir := filepath.Join(tempDir, "cards")
	_ = os.MkdirAll(cardsDir, 0755)

	manifest := `{"id": "test-scenario", "title": "Test Title"}`
	_ = os.WriteFile(filepath.Join(tempDir, "manifest.json"), []byte(manifest), 0644)

	card1 := `{"id": "c1", "tab_title": "1. Context", "title": "Incident"}`
	_ = os.WriteFile(filepath.Join(cardsDir, "01_context.json"), []byte(card1), 0644)

	handler := HandleGetScenario(tempDir)

	req := httptest.NewRequest(http.MethodGet, "/api/scenario", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	manifestData, ok := resp["manifest"].(map[string]any)
	if !ok || manifestData["id"] != "test-scenario" {
		t.Errorf("unexpected manifest: %v", manifestData)
	}

	cards, ok := resp["cards"].([]any)
	if !ok || len(cards) != 1 {
		t.Errorf("expected 1 card, got %d", len(cards))
	}
}

func TestWorkspaceAPI_TreeAndFile(t *testing.T) {
	tempWorkspace := t.TempDir()
	_ = os.WriteFile(filepath.Join(tempWorkspace, "main.go"), []byte("package main\n"), 0644)
	subDir := filepath.Join(tempWorkspace, "pkg")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "util.go"), []byte("package pkg\n"), 0644)

	// 1. Test Tree
	treeHandler := HandleGetWorkspaceTree(tempWorkspace)
	reqTree := httptest.NewRequest(http.MethodGet, "/api/workspace/tree", nil)
	recTree := httptest.NewRecorder()
	treeHandler(recTree, reqTree)

	if recTree.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recTree.Code)
	}

	var tree []*FileNode
	if err := json.NewDecoder(recTree.Body).Decode(&tree); err != nil {
		t.Fatalf("decode tree: %v", err)
	}

	if len(tree) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(tree))
	}

	// 2. Test File
	fileHandler := HandleGetWorkspaceFile(tempWorkspace)
	reqFile := httptest.NewRequest(http.MethodGet, "/api/workspace/file?path=main.go", nil)
	recFile := httptest.NewRecorder()
	fileHandler(recFile, reqFile)

	if recFile.Code != http.StatusOK {
		t.Fatalf("expected 200 for file, got %d", recFile.Code)
	}

	var fileResp map[string]any
	_ = json.NewDecoder(recFile.Body).Decode(&fileResp)
	if !strings.Contains(fileResp["content"].(string), "package main") {
		t.Errorf("unexpected file content: %v", fileResp["content"])
	}
}

