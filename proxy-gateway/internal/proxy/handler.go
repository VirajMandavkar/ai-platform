package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"proxy-gateway/internal/limiter"
	"time"
)

// Config holds setting for upstream forwarding and limits
type Config struct {
	TargetURL *url.URL
	APIKey    string
	Limiter   *limiter.Bucket
}

// Handler Orchestrates rate-limiting and reverse-proxying LLM traffic
type Handler struct {
	proxy   *httputil.ReverseProxy
	limiter *limiter.Bucket
	apiKey  string
}

// NewHandler creates a tuned reverse proxy and binds our concurrency limiter
func NewHandler(cfg Config) *Handler {
	transport := &http.Transport{
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 250,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}

	rp := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.TargetURL)
			pr.Out.Host = cfg.TargetURL.Host
			if cfg.APIKey != "" {
				pr.Out.Header.Set("x-api-key", cfg.APIKey)
				if pr.In.Header.Get("Authorization") == "" {
					pr.Out.Header.Set("Authorization", "Bearer"+cfg.APIKey)
				}
			}

			pr.Out.Header.Del("X-Agent-Priority")
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[proxy error] forwarding failed: %v", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return &Handler{
		proxy:   rp,
		limiter: cfg.Limiter,
		apiKey:  cfg.APIKey,
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
