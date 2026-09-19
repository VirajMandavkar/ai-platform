package main

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"proxy-gateway/internal/limiter"
	"proxy-gateway/internal/middleware"
	"proxy-gateway/internal/proxy"
	"strconv"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	targetURLStr := os.Getenv("TARGET_URL")
	apiKey := os.Getenv("API_KEY")

	maxConcurrencyStr := os.Getenv("MAX_CONCURRENCY")
	maxConcurrency, err := strconv.Atoi(maxConcurrencyStr)
	if err != nil || maxConcurrency <= 0 {
		maxConcurrency = 50
	}

	parsedURL, err := url.Parse(targetURLStr)
	if err != nil || targetURLStr == "" {
		log.Fatalf("Failed to parse TARGET_URL %q: %v", targetURLStr, err)
	}

	bucket := limiter.NewBucket(maxConcurrency)

	proxyConfig := proxy.Config{
		TargetURL: parsedURL,
		APIKey:    apiKey,
		Limiter:   bucket,
	}

	proxyHandler := proxy.NewHandler(proxyConfig)

	wrappedHandler := middleware.PromptCacheMiddleware(proxyHandler)

	log.Printf("Starting proxy gateway server on port %s...", port)
	log.Printf("Proxying traffic to %s with maximum concurrency of %d", targetURLStr, maxConcurrency)

	if err := http.ListenAndServe(":"+port, wrappedHandler); err != nil {
		log.Fatalf("Server failed to start or crashed unexpectedly: %v", err)
	}
}
