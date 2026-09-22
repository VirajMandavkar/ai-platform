package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"proxy-gateway/internal/limiter"
	"strings"
	"testing"
)

func TestHandler_EndToEnd_SSETranslation(t *testing.T) {
	// 1. Mock upstream OpenAI server that emits OpenAI SSE
	mockOpenAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(interface{ Flush() })
		if !ok {
			t.Fatal("expected flusher")
		}

		chunks := []string{
			`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{"content":"Hello from "}}]}`,
			`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{"content":"DeepSeek!"}}]}`,
			`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}

		for _, c := range chunks {
			fmt.Fprintf(w, "%s\n\n", c)
			flusher.Flush()
		}
	}))
	defer mockOpenAIServer.Close()

	// 2. Setup proxy targeting the mock upstream
	targetURL, err := url.Parse(mockOpenAIServer.URL)
	if err != nil {
		t.Fatalf("parse target URL: %v", err)
	}

	bucket := limiter.NewBucket(10)
	cfg := Config{
		TargetURL: targetURL,
		APIKeys: []string{    "test-key"},
		Limiter:   bucket,
	}

	handler := NewHandler(cfg)

	// 3. Make client request with Anthropic path and headers
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model": "deepseek-chat",
		"messages": [{"role": "user", "content": "hi"}],
		"stream": true
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	output := string(bodyBytes)

	// 4. Verify Anthropic SSE protocol events in the output
	if !strings.Contains(output, "event: message_start") {
		t.Errorf("missing message_start event: %s", output)
	}
	if !strings.Contains(output, "event: content_block_start") {
		t.Errorf("missing content_block_start event: %s", output)
	}
	if !strings.Contains(output, "event: content_block_delta") {
		t.Errorf("missing content_block_delta event: %s", output)
	}
	if !strings.Contains(output, "Hello from ") || !strings.Contains(output, "DeepSeek!") {
		t.Errorf("missing translated content: %s", output)
	}
	if !strings.Contains(output, "event: message_delta") {
		t.Errorf("missing message_delta event: %s", output)
	}
	if !strings.Contains(output, "event: message_stop") {
		t.Errorf("missing message_stop event: %s", output)
	}
}

func TestHandler_EndToEnd_NonStreamingTranslation(t *testing.T) {
	mockOpenAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"id": "chatcmpl-nonstream",
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "Non-streamed response"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 15,
				"completion_tokens": 5
			}
		}`))
	}))
	defer mockOpenAIServer.Close()

	targetURL, _ := url.Parse(mockOpenAIServer.URL)
	bucket := limiter.NewBucket(10)
	handler := NewHandler(Config{
		TargetURL: targetURL,
		APIKeys: []string{    "test-key"},
		Limiter:   bucket,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	respStr := string(body)

	if !strings.Contains(respStr, `"type":"message"`) {
		t.Errorf("expected Anthropic message type, got: %s", respStr)
	}
	if !strings.Contains(respStr, "Non-streamed response") {
		t.Errorf("expected translated text, got: %s", respStr)
	}
}
