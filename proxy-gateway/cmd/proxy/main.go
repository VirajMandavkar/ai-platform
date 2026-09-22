package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"proxy-gateway/internal/byok"
	"proxy-gateway/internal/limiter"
	"proxy-gateway/internal/middleware"
	"proxy-gateway/internal/proxy"
	"strconv"
	"strings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	targetURLStr := os.Getenv("TARGET_URL")
	apiKeyStr := os.Getenv("API_KEY")
	apiKeys := strings.Split(apiKeyStr, ",")
	var validKeys []string
	for _, k := range apiKeys {
		k = strings.TrimSpace(k)
		if k != "" {
			validKeys = append(validKeys, k)
		}
	}

	maxConcurrencyStr := os.Getenv("MAX_CONCURRENCY")
	maxConcurrency, err := strconv.Atoi(maxConcurrencyStr)
	if err != nil || maxConcurrency <= 0 {
		maxConcurrency = 50
	}

	parsedURL, _ := url.Parse(targetURLStr)
	if targetURLStr == "" {
		parsedURL, _ = url.Parse("https://api.openai.com/v1") // Default fallback
	}

	bucket := limiter.NewBucket(maxConcurrency)

	proxyConfig := proxy.Config{
		TargetURL: parsedURL,
		APIKeys:   validKeys,
		Limiter:   bucket,
	}

	proxyHandler := proxy.NewHandler(proxyConfig)

	wrappedHandler := middleware.PromptCacheMiddleware(proxyHandler)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/byok/benchmark", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			TargetURL string `json:"target_url"`
			APIKey    string `json:"api_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.TargetURL == "" {
			req.TargetURL = targetURLStr
		}
		if req.APIKey == "" {
			req.APIKey = apiKeyStr
		}

		report, err := byok.BenchmarkKey(r.Context(), req.TargetURL, req.APIKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	})

	mux.Handle("/", wrappedHandler)

	log.Printf("Starting proxy gateway server on port %s...", port)
	log.Printf("Proxying traffic to %s with maximum concurrency of %d", targetURLStr, maxConcurrency)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed to start or crashed unexpectedly: %v", err)
	}
}
