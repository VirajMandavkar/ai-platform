package translator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

// AnthropicRequest represents incoming Anthropic payload from Claude Code
type AnthropicRequest struct {
	Model     string           `json:"model"`
	Messages  []AnthropicMsg   `json:"messages"`
	System    any              `json:"system,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
	Stream    bool             `json:"stream"`
	Tools     []map[string]any `json:"tools,omitempty"`
}

type AnthropicMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// TranslateAnthropicToOpenAI converts Anthropic request body to OpenAI chat completion body
func TranslateAnthropicToOpenAI(bodyBytes []byte, targetModel string) ([]byte, error) {
	var req AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return nil, fmt.Errorf("unmarshal anthropic request: %w", err)
	}

	openAIMessages := make([]map[string]any, 0)

	// 1. Flatten system prompt
	if req.System != nil {
		switch s := req.System.(type) {
		case string:
			if strings.TrimSpace(s) != "" {
				openAIMessages = append(openAIMessages, map[string]any{
					"role":    "system",
					"content": s,
				})
			}
		case []any:
			var sb strings.Builder
			for _, block := range s {
				if m, ok := block.(map[string]any); ok {
					if text, ok := m["text"].(string); ok {
						sb.WriteString(text)
						sb.WriteString("\n")
					}
				}
			}
			if sb.Len() > 0 {
				openAIMessages = append(openAIMessages, map[string]any{
					"role":    "system",
					"content": strings.TrimSpace(sb.String()),
				})
			}
		}
	}

	// 2. Process conversation turns
	for _, msg := range req.Messages {
		switch c := msg.Content.(type) {
		case string:
			openAIMessages = append(openAIMessages, map[string]any{
				"role":    msg.Role,
				"content": c,
			})
		case []any:
			// Array of content blocks
			var textContent strings.Builder
			var toolCalls []map[string]any
			var toolResults []map[string]any

			for _, item := range c {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				bType, _ := block["type"].(string)
				switch bType {
				case "text":
					if t, ok := block["text"].(string); ok {
						textContent.WriteString(t)
					}
				case "tool_use":
					toolID, _ := block["id"].(string)
					name, _ := block["name"].(string)
					input := block["input"]
					inputJSON, _ := json.Marshal(input)

					toolCalls = append(toolCalls, map[string]any{
						"id":   toolID,
						"type": "function",
						"function": map[string]any{
							"name":      name,
							"arguments": string(inputJSON),
						},
					})
				case "tool_result":
					toolUseID, _ := block["tool_use_id"].(string)
					resContent := block["content"]
					var resStr string
					switch rc := resContent.(type) {
					case string:
						resStr = rc
					default:
						rcBytes, _ := json.Marshal(rc)
						resStr = string(rcBytes)
					}
					toolResults = append(toolResults, map[string]any{
						"role":         "tool",
						"tool_call_id": toolUseID,
						"content":      resStr,
					})
				}
			}

			if msg.Role == "assistant" {
				asstMsg := map[string]any{
					"role": "assistant",
				}
				if textContent.Len() > 0 {
					asstMsg["content"] = textContent.String()
				}
				if len(toolCalls) > 0 {
					asstMsg["tool_calls"] = toolCalls
				}
				openAIMessages = append(openAIMessages, asstMsg)
			} else if msg.Role == "user" {
				if textContent.Len() > 0 {
					openAIMessages = append(openAIMessages, map[string]any{
						"role":    "user",
						"content": textContent.String(),
					})
				}
				// Append tool results as role: "tool"
				for _, tr := range toolResults {
					openAIMessages = append(openAIMessages, tr)
				}
			}
		}
	}

	// 3. Cap max_tokens safely
	maxTokens := req.MaxTokens
	if maxTokens > 16384 || maxTokens <= 0 {
		maxTokens = 8192
	}

	payload := map[string]any{
		"model":      targetModel,
		"messages":   openAIMessages,
		"stream":     req.Stream,
		"max_tokens": maxTokens,
	}

	// 4. Translate tools
	if len(req.Tools) > 0 {
		openAITools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			openAITools = append(openAITools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t["name"],
					"description": t["description"],
					"parameters":  t["input_schema"],
				},
			})
		}
		payload["tools"] = openAITools
	}

	return json.Marshal(payload)
}

// OpenAIStreamChunk represents chunk emitted by OpenAI / DeepSeek
type OpenAIStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// StreamState maintains the Anthropic protocol state during stream translation
type StreamState struct {
	MessageID      string
	Model          string
	Started        bool
	BlockIndex     int
	InsideText     bool
	InsideTool     bool
	ActiveToolID   string
	ActiveToolName string
	TotalOutputTok int
}

// NewStreamState creates initial state for streaming
func NewStreamState(model string) *StreamState {
	return &StreamState{
		MessageID:  fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Model:      model,
		BlockIndex: -1,
	}
}

// TranslateOpenAISSEToAnthropic reads an OpenAI SSE stream and writes Anthropic SSE events
func TranslateOpenAISSEToAnthropic(r io.Reader, w io.Writer, state *StreamState) error {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max line length

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "[DONE]" {
			// Close any open block
			if state.InsideText || state.InsideTool {
				writeSSEEvent(w, "content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": state.BlockIndex,
				})
				state.InsideText = false
				state.InsideTool = false
			}

			// Final message_delta and message_stop
			writeSSEEvent(w, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   "end_turn",
					"stop_sequence": nil,
				},
				"usage": map[string]any{
					"output_tokens": state.TotalOutputTok,
				},
			})
			writeSSEEvent(w, "message_stop", map[string]any{
				"type": "message_stop",
			})
			break
		}

		var chunk OpenAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			log.Printf("[translator] failed to parse chunk JSON: %v", err)
			continue
		}

		// 1. Emit message_start on the very first event
		if !state.Started {
			state.Started = true
			writeSSEEvent(w, "message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":            state.MessageID,
					"type":          "message",
					"role":          "assistant",
					"content":       []any{},
					"model":         state.Model,
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage": map[string]any{
						"input_tokens":  1,
						"output_tokens": 1,
					},
				},
			})
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		// 2. Handle Text Delta
		if delta.Content != "" {
			// If we were inside a tool call, close it
			if state.InsideTool {
				writeSSEEvent(w, "content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": state.BlockIndex,
				})
				state.InsideTool = false
			}

			// If text block not yet open, open it
			if !state.InsideText {
				state.BlockIndex++
				state.InsideText = true
				writeSSEEvent(w, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": state.BlockIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				})
			}

			state.TotalOutputTok++
			writeSSEEvent(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.BlockIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": delta.Content,
				},
			})
		}

		// 3. Handle Tool Calls
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				// New tool call starting
				if tc.ID != "" || !state.InsideTool {
					if state.InsideText || state.InsideTool {
						writeSSEEvent(w, "content_block_stop", map[string]any{
							"type":  "content_block_stop",
							"index": state.BlockIndex,
						})
						state.InsideText = false
						state.InsideTool = false
					}

					state.BlockIndex++
					state.InsideTool = true
					state.ActiveToolID = tc.ID
					if state.ActiveToolID == "" {
						state.ActiveToolID = fmt.Sprintf("call_%d", time.Now().UnixNano())
					}
					state.ActiveToolName = tc.Function.Name

					writeSSEEvent(w, "content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": state.BlockIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    state.ActiveToolID,
							"name":  state.ActiveToolName,
							"input": map[string]any{},
						},
					})
				}

				// Stream arguments fragment
				if tc.Function.Arguments != "" {
					state.TotalOutputTok++
					writeSSEEvent(w, "content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": state.BlockIndex,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": tc.Function.Arguments,
						},
					})
				}
			}
		}

		// 4. Handle finish reason
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			if state.InsideText || state.InsideTool {
				writeSSEEvent(w, "content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": state.BlockIndex,
				})
				state.InsideText = false
				state.InsideTool = false
			}

			stopReason := "end_turn"
			if *choice.FinishReason == "tool_calls" || *choice.FinishReason == "function_call" {
				stopReason = "tool_use"
			}

			writeSSEEvent(w, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   stopReason,
					"stop_sequence": nil,
				},
				"usage": map[string]any{
					"output_tokens": state.TotalOutputTok,
				},
			})
			writeSSEEvent(w, "message_stop", map[string]any{
				"type": "message_stop",
			})
			break
		}
	}

	return scanner.Err()
}

func writeSSEEvent(w io.Writer, eventType string, data any) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	var buf bytes.Buffer
	buf.WriteString("event: ")
	buf.WriteString(eventType)
	buf.WriteString("\ndata: ")
	buf.Write(dataBytes)
	buf.WriteString("\n\n")

	_, _ = w.Write(buf.Bytes())
	if flusher, ok := w.(interface{ Flush() }); ok {
		flusher.Flush()
	}
}

// TranslateOpenAIJSONToAnthropic translates a non-streaming OpenAI chat completion to an Anthropic message
func TranslateOpenAIJSONToAnthropic(body []byte, model string) ([]byte, error) {
	var oaiResp struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &oaiResp); err != nil {
		return nil, err
	}

	content := make([]map[string]any, 0)
	stopReason := "end_turn"

	if len(oaiResp.Choices) > 0 {
		c := oaiResp.Choices[0]
		if c.Message.Content != "" {
			content = append(content, map[string]any{
				"type": "text",
				"text": c.Message.Content,
			})
		}
		for _, tc := range c.Message.ToolCalls {
			var input map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			if input == nil {
				input = make(map[string]any)
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Function.Name,
				"input": input,
			})
		}
		if c.FinishReason == "tool_calls" || c.FinishReason == "function_call" {
			stopReason = "tool_use"
		}
	}

	anthropicResp := map[string]any{
		"id":          fmt.Sprintf("msg_%s", oaiResp.ID),
		"type":        "message",
		"role":        "assistant",
		"content":     content,
		"model":       model,
		"stop_reason": stopReason,
		"usage": map[string]any{
			"input_tokens":  oaiResp.Usage.PromptTokens,
			"output_tokens": oaiResp.Usage.CompletionTokens,
		},
	}

	return json.Marshal(anthropicResp)
}
