package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// CacheControl defines the Anthropic prompt caching flag.
type CacheControl struct {
	Type string `json:"type"`
}

// SystemBlock represents an element in the structured system prompt array
type SystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// PromptCacheMiddleware intercepts /v1/messages erquests and ensures the static system prompts is tagged for Anthropic's ephemeral prompt caching
func PromptCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			next.ServeHTTP(w, r)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request Body", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		var payload map[string]any

		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			next.ServeHTTP(w, r)
			return
		}

		modified := false
		if rawSystem, exists := payload["system"]; exists && rawSystem != nil {
			modified = injectSystemCacheControl(payload, rawSystem)
		}

		if modified {
			newBytes, err := json.Marshal(payload)
			if err == nil {
				bodyBytes = newBytes
			}
		}

		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		r.ContentLength = int64(len(bodyBytes))
		r.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))

		next.ServeHTTP(w, r)
	})
}

// injectSystemCacheControl handles both string and array representations of the system field.
// It modifies the payload map in-place and returns true if modifications were made.
func injectSystemCacheControl(payload map[string]any, rawSystem any) bool {
	switch v := rawSystem.(type) {
	case string:
		if v == "" {
			return false
		}
		// Convert string system prompt to block format with cache_control
		payload["system"] = []SystemBlock{
			{
				Type: "text",
				Text: v,
				CacheControl: &CacheControl{
					Type: "ephemeral",
				},
			},
		}
		return true

	case []any:
		if len(v) == 0 {
			return false
		}
		// Check the last block; if not already cached, tag it
		lastIndex := len(v) - 1
		if lastBlock, ok := v[lastIndex].(map[string]any); ok {
			if _, hasCache := lastBlock["cache_control"]; !hasCache {
				lastBlock["cache_control"] = map[string]string{"type": "ephemeral"}
				return true
			}
		}
	}
	return false
}
