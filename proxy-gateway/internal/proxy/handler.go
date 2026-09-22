package proxy

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"proxy-gateway/internal/limiter"
	"proxy-gateway/internal/translator"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config holds setting for upstream forwarding and limits
type Config struct {
	TargetURL *url.URL
	APIKeys   []string
	Limiter   *limiter.Bucket
}

// Handler Orchestrates rate-limiting and reverse-proxying LLM traffic
type Handler struct {
	proxy      *httputil.ReverseProxy
	limiter    *limiter.Bucket
	apiKeys    []string
	keyCounter int
	mu         sync.Mutex
}

// NewHandler creates a tuned reverse proxy and binds our concurrency limiter
func NewHandler(cfg Config) *Handler {
	transport := &http.Transport{
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 250,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	
	var mu sync.Mutex
	var keyCounter int

	rp := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			apiKey := pr.Out.Header.Get("x-api-key")
			if apiKey == "" {
				auth := pr.Out.Header.Get("Authorization")
				if strings.HasPrefix(auth, "Bearer ") {
					apiKey = strings.TrimPrefix(auth, "Bearer ")
				}
			}
			
			// Round-robin selection if multiple platform keys exist
			if apiKey == "" && len(cfg.APIKeys) > 0 {
				mu.Lock()
				apiKey = cfg.APIKeys[keyCounter%len(cfg.APIKeys)]
				keyCounter++
				mu.Unlock()
			}

			targetStr := cfg.TargetURL.String()
			if strings.HasPrefix(apiKey, "gsk_") {
				targetStr = "https://api.groq.com/openai"
			} else if strings.HasPrefix(apiKey, "sk-ant") {
				targetStr = "https://api.anthropic.com"
			} else if strings.HasPrefix(apiKey, "sk-") {
				// Default to openai for sk- assuming it might be deepseek or openai
				targetStr = "https://api.openai.com/v1"
			}

			parsedTarget, _ := url.Parse(targetStr)
			pr.SetURL(parsedTarget)
			pr.Out.Host = parsedTarget.Host

			if apiKey != "" {
				pr.Out.Header.Set("x-api-key", apiKey)
				pr.Out.Header.Set("Authorization", "Bearer "+apiKey)
			}

			pr.Out.Header.Del("X-Agent-Priority")
		},

		ModifyResponse: func(res *http.Response) error {
			log.Printf("[upstream status] %d %s", res.StatusCode, res.Status)
			if res.StatusCode >= 400 {
				body, _ := io.ReadAll(res.Body)
				log.Printf("[upstream error body] %s", string(body))
				res.Body = io.NopCloser(bytes.NewReader(body))
				return nil
			}

			contentType := res.Header.Get("Content-Type")

			// Case 1: Streamed SSE chunks
			if strings.Contains(contentType, "text/event-stream") {
				pr, pw := io.Pipe()
				origBody := res.Body
				res.Body = pr
				res.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
				res.Header.Del("Content-Length")

				go func() {
					defer pw.Close()
					defer origBody.Close()
					state := translator.NewStreamState("claude-3-5-sonnet-20241022")
					if err := translator.TranslateOpenAISSEToAnthropic(origBody, pw, state); err != nil {
						log.Printf("[proxy] error in SSE translation: %v", err)
					}
				}()
			} else if strings.Contains(contentType, "application/json") {
				// Case 2: Non-streamed JSON response
				body, err := io.ReadAll(res.Body)
				_ = res.Body.Close()
				if err == nil {
					anthropicBody, err := translator.TranslateOpenAIJSONToAnthropic(body, "claude-3-5-sonnet-20241022")
					if err == nil {
						res.Body = io.NopCloser(bytes.NewReader(anthropicBody))
						res.ContentLength = int64(len(anthropicBody))
						res.Header.Set("Content-Length", strconv.Itoa(len(anthropicBody)))
					} else {
						res.Body = io.NopCloser(bytes.NewReader(body))
					}
				}
			}

			return nil
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[proxy error] forwarding failed: %v", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return &Handler{
		proxy:   rp,
		limiter: cfg.Limiter,
		apiKeys: cfg.APIKeys,
	}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Detect priority: default to Standard, promote if flagged by agent sandbox
	priority := limiter.StandardPriority
	if r.Header.Get("X-Agent-Priority") == "high" {
		priority = limiter.HighPriority
	}

	// 1. Acquire concurrency slot (sleeps if active >= limit, aborts on client disconnect)
	if err := h.limiter.Acquire(r.Context(), priority); err != nil {
		// Client dropped connection while queued
		http.Error(w, "Request canceled while queued", http.StatusRequestTimeout)
		return
	}

	// 2. Guarantee slot is returned when request completes or fails
	defer h.limiter.Release()

	// 3. Delegate to reverse proxy
	h.proxy.ServeHTTP(w, r)
}
