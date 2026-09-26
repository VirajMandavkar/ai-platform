package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"proxy-gateway/internal/limiter"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStress_ConcurrentSSEStreams(t *testing.T) {
	// 1. Mock upstream server returning OpenAI SSE format with latency
	mockOpenAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(interface{ Flush() })
		if !ok {
			t.Fatal("expected flusher")
		}

		chunks := []string{
			`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			fmt.Sprintf(`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{"content":"ReqID: %s "}}]}`, reqID),
			`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{"content":"Done!"}}]}`,
			`data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}

		for _, c := range chunks {
			fmt.Fprintf(w, "%s\n\n", c)
			flusher.Flush()
			time.Sleep(50 * time.Millisecond) // configurable latency
		}
	}))
	defer mockOpenAIServer.Close()

	targetURL, _ := url.Parse(mockOpenAIServer.URL)
	bucket := limiter.NewBucket(10) // Set limiter to 10
	handler := NewHandler(Config{
		TargetURL: targetURL,
		APIKeys:   []string{"test-key"},
		Limiter:   bucket,
	})

	var wg sync.WaitGroup
	numRequests := 30
	successCount := int32(0)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			reqID := fmt.Sprintf("req-%d", id)
			reqBody := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-Id", reqID)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			resp := rec.Result()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("req %s expected 200, got %d", reqID, resp.StatusCode)
				return
			}

			body, _ := io.ReadAll(resp.Body)
			output := string(body)

			if !strings.Contains(output, "event: message_start") ||
				!strings.Contains(output, "event: content_block_start") ||
				!strings.Contains(output, "event: content_block_delta") ||
				!strings.Contains(output, "event: content_block_stop") ||
				!strings.Contains(output, "event: message_delta") ||
				!strings.Contains(output, "event: message_stop") {
				t.Errorf("req %s missing complete anthropic event sequence: %s", reqID, output)
				return
			}

			if !strings.Contains(output, reqID) {
				t.Errorf("req %s missing unique ID in response: %s", reqID, output)
				return
			}

			atomic.AddInt32(&successCount, 1)
		}(i)
	}

	wg.Wait()

	if successCount != int32(numRequests) {
		t.Fatalf("expected %d successes, got %d", numRequests, successCount)
	}
}

func TestStress_ClientDisconnectSlotRecovery(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		// Keep connection open and trickle data
		for i := 0; i < 5; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
				fmt.Fprintf(w, `data: {"id":"1","choices":[{"index":0,"delta":{"content":"..."}}]}`+"\n\n")
				w.(http.Flusher).Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer mockServer.Close()

	targetURL, _ := url.Parse(mockServer.URL)
	bucket := limiter.NewBucket(2) // Limiter to 2
	handler := NewHandler(Config{TargetURL: targetURL, Limiter: bucket})

	var wg sync.WaitGroup
	successCount := int32(0)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			req := httptest.NewRequest(http.MethodPost, "/v1", nil)
			ctx, cancel := context.WithCancel(context.Background())
			req = req.WithContext(ctx)

			// Cancel 2 of them mid-stream
			if id < 2 {
				go func() {
					time.Sleep(100 * time.Millisecond)
					cancel()
				}()
			} else {
				defer cancel()
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if id >= 2 && rec.Result().StatusCode == http.StatusOK {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if successCount != 2 {
		t.Fatalf("expected 2 successful requests, got %d", successCount)
	}

	// Verify limiter slot is 0
	ctx := context.Background()
	if err := bucket.Acquire(ctx, limiter.StandardPriority); err != nil {
		t.Fatalf("failed to acquire slot 1")
	}
	if err := bucket.Acquire(ctx, limiter.StandardPriority); err != nil {
		t.Fatalf("failed to acquire slot 2")
	}
	bucket.Release()
	bucket.Release()
}

func TestStress_UpstreamErrorPropagation(t *testing.T) {
	reqCount := int32(0)
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&reqCount, 1)
		if count <= 5 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "Too Many Requests"}`))
			return
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"id": "1",
			"choices": [{"message": {"role": "assistant", "content": "success"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 1}
		}`))
	}))
	defer mockServer.Close()

	targetURL, _ := url.Parse(mockServer.URL)
	bucket := limiter.NewBucket(10)
	handler := NewHandler(Config{TargetURL: targetURL, Limiter: bucket})

	var wg sync.WaitGroup
	errorCount := int32(0)
	successCount := int32(0)
	numRequests := 10

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			resp := rec.Result()
			if resp.StatusCode == http.StatusTooManyRequests {
				atomic.AddInt32(&errorCount, 1)
			} else if resp.StatusCode == http.StatusOK {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	if errorCount != 5 || successCount != 5 {
		t.Fatalf("expected 5 errors and 5 successes, got %d errors, %d successes", errorCount, successCount)
	}

	// Verify limiter slots are released
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := bucket.Acquire(ctx, limiter.StandardPriority); err != nil {
			t.Fatalf("failed to acquire slot %d, slot leaked on error path", i)
		}
	}
	for i := 0; i < 10; i++ {
		bucket.Release()
	}
}

func TestStress_ToolCallStreaming(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		chunks := []string{
			`data: {"id":"1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
			`data: {"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}}]}}]}`,
			`data: {"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\": \"NY\"}"}}]}}]}`,
			`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}

		for _, c := range chunks {
			fmt.Fprintf(w, "%s\n\n", c)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer mockServer.Close()

	targetURL, _ := url.Parse(mockServer.URL)
	handler := NewHandler(Config{TargetURL: targetURL, Limiter: limiter.NewBucket(5)})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			body, _ := io.ReadAll(rec.Result().Body)
			output := string(body)

			if !strings.Contains(output, `"type":"tool_use"`) {
				t.Errorf("missing tool_use type: %s", output)
			}
			if !strings.Contains(output, `"type":"input_json_delta"`) {
				t.Errorf("missing input_json_delta type: %s", output)
			}
			if !strings.Contains(output, `"stop_reason":"tool_use"`) {
				t.Errorf("missing stop_reason tool_use: %s", output)
			}
		}()
	}
	wg.Wait()
}

func TestStress_GoroutineLeakCheck(t *testing.T) {
	// Let existing goroutines settle
	time.Sleep(100 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	mockServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(w, `data: {"id":"1","choices":[{"index":0,"delta":{"content":"hi"}}],"finish_reason":"stop"}`+"\n\n")
		w.(http.Flusher).Flush()
		
		fmt.Fprintf(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	mockServer.Config.SetKeepAlivesEnabled(false)
	mockServer.Start()
	defer mockServer.Close()

	targetURL, _ := url.Parse(mockServer.URL)
	handler := NewHandler(Config{TargetURL: targetURL, Limiter: limiter.NewBucket(20)})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1", strings.NewReader(`{}`))
			req.Close = true // Prevent keep-alive on client side
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			resp := rec.Result()
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}()
	}
	wg.Wait()
	
	mockServer.Close() // Force close connections

	// Give time for cleanup
	time.Sleep(500 * time.Millisecond)
	current := runtime.NumGoroutine()

	if current > baseline+5 {
		t.Errorf("Goroutine leak detected: baseline=%d, current=%d", baseline, current)
	}
}
