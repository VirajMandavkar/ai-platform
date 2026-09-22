package byok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBenchmarkKey_SuccessHighCapacity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{
					{"id": "deepseek-chat"},
					{"id": "deepseek-coder"},
				},
			})
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("x-ratelimit-limit-requests", "1000")
			w.Header().Set("x-ratelimit-limit-tokens", "500000")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	report, err := BenchmarkKey(context.Background(), server.URL, "test-valid-key")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if !report.Valid {
		t.Errorf("expected key to be valid")
	}
	if report.Status != "EXCELLENT" {
		t.Errorf("expected EXCELLENT status, got %s", report.Status)
	}
	if report.RecommendedModel != "deepseek-chat" {
		t.Errorf("expected deepseek-chat, got %s", report.RecommendedModel)
	}
	if report.MaxConcurrentSeats < 10 {
		t.Errorf("expected at least 10 seats for 500k TPM, got %d", report.MaxConcurrentSeats)
	}
}

func TestBenchmarkKey_LowCapacityWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "qwen/qwen3.8-27b"}},
			})
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("x-ratelimit-limit-tokens", "7000")
			w.Header().Set("x-ratelimit-limit-requests", "30")
			w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			return
		}
	}))
	defer server.Close()

	report, err := BenchmarkKey(context.Background(), server.URL, "test-low-key")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if report.Status != "WARNING" {
		t.Errorf("expected WARNING status for 7k TPM, got %s", report.Status)
	}
	if report.MaxConcurrentSeats != 0 {
		t.Errorf("expected 0 seats for 7k TPM, got %d", report.MaxConcurrentSeats)
	}
}

func TestBenchmarkKey_InvalidAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized key", http.StatusUnauthorized)
	}))
	defer server.Close()

	report, err := BenchmarkKey(context.Background(), server.URL, "invalid-key")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if report.Valid {
		t.Errorf("expected invalid report")
	}
	if report.Status != "INSUFFICIENT" {
		t.Errorf("expected INSUFFICIENT status, got %s", report.Status)
	}
}
