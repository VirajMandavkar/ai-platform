package byok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CapacityReport defines the result of benchmarking a company's API key
type CapacityReport struct {
	Valid              bool     `json:"valid"`
	Provider           string   `json:"provider"`
	AvailableModels    []string `json:"available_models"`
	RecommendedModel   string   `json:"recommended_model"`
	RPM                int      `json:"rpm"`
	TPM                int      `json:"tpm"`
	LatencyMs          int64    `json:"latency_ms"`
	MaxConcurrentSeats int      `json:"max_concurrent_seats"`
	Status             string   `json:"status"` // "EXCELLENT", "ADEQUATE", "WARNING", "INSUFFICIENT"
	Message            string   `json:"message"`
	RawError           string   `json:"raw_error,omitempty"`
}

// BenchmarkKey runs a pre-flight probe and stress calculation on a company API key
func BenchmarkKey(ctx context.Context, targetURLStr, apiKey string) (*CapacityReport, error) {
	if targetURLStr == "" {
		if strings.HasPrefix(apiKey, "gsk_") {
			targetURLStr = "https://api.groq.com/openai"
		} else if strings.HasPrefix(apiKey, "sk-ant") {
			targetURLStr = "https://api.anthropic.com"
		} else if strings.HasPrefix(apiKey, "sk-") {
			targetURLStr = "https://api.openai.com/v1"
		} else {
			targetURLStr = "https://api.openai.com/v1" // Fallback
		}
	} else {
		// Prevent SSRF by validating the requested URL is a known provider
		allowedPrefixes := []string{
			"https://api.openai.com",
			"https://api.anthropic.com",
			"https://api.groq.com",
			"https://api.deepseek.com",
			"http://127.0.0.1",
		}
		allowed := false
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(targetURLStr, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("target URL is not in the allowed list of LLM providers")
		}
	}
	
	report := &CapacityReport{
		Valid:           false,
		AvailableModels: make([]string, 0),
		Status:          "INSUFFICIENT",
	}

	targetURLStr = strings.TrimRight(targetURLStr, "/")
	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Discover Models via /v1/models
	modelsURL := fmt.Sprintf("%s/v1/models", targetURLStr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		report.RawError = err.Error()
		report.Message = "Failed to connect to LLM endpoint: " + err.Error()
		return report, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		report.RawError = string(body)
		report.Message = fmt.Sprintf("Authentication failed with HTTP %d", resp.StatusCode)
		return report, nil
	}

	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err == nil {
		for _, m := range modelsResp.Data {
			report.AvailableModels = append(report.AvailableModels, m.ID)
		}
	}

	report.Valid = true

	// 2. Select test candidate model
	testModel := selectBestModel(report.AvailableModels)
	report.RecommendedModel = testModel

	// 3. Probe with a minimal chat completion to measure latency & capture rate-limit headers
	chatURL := fmt.Sprintf("%s/v1/chat/completions", targetURLStr)
	samplePayload := map[string]any{
		"model":      testModel,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 5,
	}
	payloadBytes, _ := json.Marshal(samplePayload)

	chatReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return report, nil
	}
	chatReq.Header.Set("Authorization", "Bearer "+apiKey)
	chatReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	chatResp, err := client.Do(chatReq)
	if err != nil {
		report.RawError = err.Error()
		report.Message = "Probe request failed: " + err.Error()
		return report, nil
	}
	defer chatResp.Body.Close()

	report.LatencyMs = time.Since(start).Milliseconds()

	// 4. Parse Rate Limit Headers
	report.RPM = parseHeaderInt(chatResp.Header.Get("x-ratelimit-limit-requests"))
	report.TPM = parseHeaderInt(chatResp.Header.Get("x-ratelimit-limit-tokens"))

	// Default fallback heuristics if headers are absent (e.g. standard DeepSeek or OpenAI defaults)
	if report.TPM == 0 {
		if strings.Contains(targetURLStr, "deepseek") {
			report.TPM = 500000 // DeepSeek Tier 1 default is ~500k+ TPM
			report.RPM = 120
		} else if strings.Contains(targetURLStr, "groq") {
			report.TPM = 7000
			report.RPM = 30
		} else {
			report.TPM = 200000 // Generic OpenAI tier 1 default
			report.RPM = 60
		}
	}
	if report.RPM == 0 {
		report.RPM = 60
	}

	// 5. Calculate concurrent candidate seat capacity
	// Assumption: Claude Code generates ~18,000 tokens per prompt (with tools/system prompt).
	// A candidate prompts roughly every 1.5 minutes = ~12,000 tokens per minute per candidate.
	tokensPerMinutePerCandidate := 15000
	seats := report.TPM / tokensPerMinutePerCandidate
	if seats <= 0 {
		seats = 1
	}

	// Also bounded by RPM (e.g. candidate generates ~2 requests per minute including agent loops)
	rpmSeats := report.RPM / 2
	if rpmSeats < seats {
		seats = rpmSeats
	}

	report.MaxConcurrentSeats = seats

	// 6. Set status and guidance message
	if report.TPM < 10000 {
		report.Status = "WARNING"
		report.Message = fmt.Sprintf("Extremely low token limit (%d TPM). Single prompts from Claude Code (~18k tokens) will exceed this quota. Upgrade to a paid tier.", report.TPM)
		report.MaxConcurrentSeats = 0
	} else if report.MaxConcurrentSeats >= 10 {
		report.Status = "EXCELLENT"
		report.Message = fmt.Sprintf("High capacity verified (%d TPM). Safe to host up to %d concurrent candidates simultaneously.", report.TPM, report.MaxConcurrentSeats)
	} else {
		report.Status = "ADEQUATE"
		report.Message = fmt.Sprintf("Moderate capacity (%d TPM). Safe for up to %d concurrent candidate sessions.", report.TPM, report.MaxConcurrentSeats)
	}

	return report, nil
}

func selectBestModel(models []string) string {
	priorities := []string{
		"deepseek-chat",
		"deepseek-coder",
		"qwen/qwen3.8-27b",
		"openai/gpt-oss-120b",
		"llama-3.3-70b-versatile",
		"gpt-4o-mini",
		"gpt-4o",
	}

	for _, p := range priorities {
		for _, m := range models {
			if strings.EqualFold(m, p) || strings.Contains(strings.ToLower(m), strings.ToLower(p)) {
				return m
			}
		}
	}

	if len(models) > 0 {
		return models[0]
	}
	return "deepseek-chat"
}

func parseHeaderInt(val string) int {
	if val == "" {
		return 0
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return n
}
