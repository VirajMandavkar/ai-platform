package middleware

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"proxy-gateway/internal/translator"
	"strconv"
	"strings"
)

// PromptCacheMiddleware transforms Anthropic /v1/messages into OpenAI format for Groq
func PromptCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[incoming request] %s %s", r.Method, r.URL.Path)
		if r.Method == http.MethodPost && (r.URL.Path == "/v1/messages" || r.URL.Path == "/messages") {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			_ = r.Body.Close()

			targetModel := os.Getenv("GROQ_MODEL")
			if targetModel == "" {
				targetModel = os.Getenv("DEFAULT_MODEL")
			}
			if targetModel == "" {
				targetModel = "qwen/qwen3.8-27b"
			}

			newBody, err := translator.TranslateAnthropicToOpenAI(bodyBytes, targetModel)
			if err == nil {
				bodyBytes = newBody
				r.URL.Path = "/v1/chat/completions"
				log.Printf("[proxy] Translated Anthropic request to %s /v1/chat/completions", targetModel)
			} else {
				log.Printf("[proxy] Translation warning: %v", err)
			}

			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
			r.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
		}

		if strings.HasPrefix(r.URL.Path, "/v1/models") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			modelID := "claude-3-5-sonnet-20241022"
			if len(r.URL.Path) > len("/v1/models/") {
				modelID = r.URL.Path[len("/v1/models/"):]
			}
			
			if r.URL.Path == "/v1/models" {
				w.Write([]byte(`{"data":[{"type":"model","id":"` + modelID + `","display_name":"` + modelID + `","created_at":"2024-01-01T00:00:00Z"}],"has_more":false}`))
			} else {
				w.Write([]byte(`{"type":"model","id":"` + modelID + `","display_name":"` + modelID + `","created_at":"2024-01-01T00:00:00Z"}`))
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}
