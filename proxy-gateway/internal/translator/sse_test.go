package translator

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateAnthropicToOpenAI(t *testing.T) {
	anthropicPayload := `{
		"model": "claude-3-5-sonnet-20241022",
		"system": "You are a code reviewer.",
		"messages": [
			{"role": "user", "content": "Check this out"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "Running test..."},
				{"type": "tool_use", "id": "call_1", "name": "bash", "input": {"command": "ls -la"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "call_1", "content": "total 0\n-rw-r--r-- file.txt"}
			]}
		],
		"tools": [
			{
				"name": "bash",
				"description": "Run shell commands",
				"input_schema": {
					"type": "object",
					"properties": {
						"command": {"type": "string"}
					}
				}
			}
		],
		"max_tokens": 1024,
		"stream": true
	}`

	outBytes, err := TranslateAnthropicToOpenAI([]byte(anthropicPayload), "deepseek-chat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var openAIReq map[string]any
	if err := json.Unmarshal(outBytes, &openAIReq); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if openAIReq["model"] != "deepseek-chat" {
		t.Errorf("expected model deepseek-chat, got %v", openAIReq["model"])
	}

	messages, ok := openAIReq["messages"].([]any)
	if !ok || len(messages) < 4 {
		t.Fatalf("expected at least 4 messages (system, user, assistant, tool), got %d", len(messages))
	}

	// 1. System
	sysMsg := messages[0].(map[string]any)
	if sysMsg["role"] != "system" || sysMsg["content"] != "You are a code reviewer." {
		t.Errorf("unexpected system msg: %v", sysMsg)
	}

	// 2. Assistant with tool_calls
	asstMsg := messages[2].(map[string]any)
	if asstMsg["role"] != "assistant" {
		t.Errorf("expected assistant msg, got %v", asstMsg["role"])
	}
	tcs, ok := asstMsg["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("expected 1 tool call in assistant message, got %v", asstMsg["tool_calls"])
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_1" {
		t.Errorf("expected tool_call_id call_1, got %v", tc["id"])
	}

	// 3. Tool result message
	toolMsg := messages[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("unexpected tool result message: %v", toolMsg)
	}

	// 4. Tools array
	tools, ok := openAIReq["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in openAIReq, got %v", openAIReq["tools"])
	}
	tool0 := tools[0].(map[string]any)
	if tool0["type"] != "function" {
		t.Errorf("expected tool type function, got %v", tool0["type"])
	}
}

func TestTranslateOpenAISSEToAnthropic_TextStream(t *testing.T) {
	openAISSE := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":" World!"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`

	var out bytes.Buffer
	state := NewStreamState("claude-3-5-sonnet-20241022")

	err := TranslateOpenAISSEToAnthropic(strings.NewReader(openAISSE), &out, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()

	if !strings.Contains(output, "event: message_start") {
		t.Errorf("missing message_start event in: %s", output)
	}
	if !strings.Contains(output, "event: content_block_start") {
		t.Errorf("missing content_block_start event in: %s", output)
	}
	if !strings.Contains(output, "event: content_block_delta") {
		t.Errorf("missing content_block_delta event in: %s", output)
	}
	if !strings.Contains(output, "Hello") || !strings.Contains(output, " World!") {
		t.Errorf("missing text chunks in: %s", output)
	}
	if !strings.Contains(output, "event: content_block_stop") {
		t.Errorf("missing content_block_stop in: %s", output)
	}
	if !strings.Contains(output, "event: message_delta") {
		t.Errorf("missing message_delta in: %s", output)
	}
	if !strings.Contains(output, "event: message_stop") {
		t.Errorf("missing message_stop in: %s", output)
	}
}

func TestTranslateOpenAISSEToAnthropic_ToolCallStream(t *testing.T) {
	openAISSE := `data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_99","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]
`

	var out bytes.Buffer
	state := NewStreamState("claude-3-5-sonnet-20241022")

	err := TranslateOpenAISSEToAnthropic(strings.NewReader(openAISSE), &out, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()

	if !strings.Contains(output, "event: content_block_start") {
		t.Errorf("missing content_block_start event in: %s", output)
	}
	if !strings.Contains(output, `"type":"tool_use"`) {
		t.Errorf("missing type: tool_use in: %s", output)
	}
	if !strings.Contains(output, `"name":"bash"`) {
		t.Errorf("missing name: bash in: %s", output)
	}
	if !strings.Contains(output, `"type":"input_json_delta"`) {
		t.Errorf("missing input_json_delta in: %s", output)
	}
	if !strings.Contains(output, `"stop_reason":"tool_use"`) {
		t.Errorf("expected stop_reason tool_use in: %s", output)
	}
}
