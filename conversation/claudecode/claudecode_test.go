/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/kit/logger"
)

func TestNewClaudeCode(t *testing.T) {
	l := logger.NewLogger("test")
	c := NewClaudeCode(l)
	require.NotNil(t, c)
}

func TestInit_CLINotFound(t *testing.T) {
	l := logger.NewLogger("test")
	c := NewClaudeCode(l)

	meta := conversation.Metadata{
		Base: metadata.Base{
			Properties: map[string]string{
				"cliPath": "/nonexistent/path/to/claude",
			},
		},
	}
	err := c.Init(context.Background(), meta)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLI not found")
}

func TestInit_DefaultModel(t *testing.T) {
	l := logger.NewLogger("test")
	cc := &ClaudeCode{logger: l}

	meta := conversation.Metadata{
		Base: metadata.Base{
			Properties: map[string]string{
				"cliPath": "echo",
			},
		},
	}
	err := cc.Init(context.Background(), meta)
	require.NoError(t, err)
	assert.Equal(t, conversation.DefaultClaudeCodeModel, cc.meta.Model)
}

func TestInit_CustomModel(t *testing.T) {
	l := logger.NewLogger("test")
	cc := &ClaudeCode{logger: l}

	meta := conversation.Metadata{
		Base: metadata.Base{
			Properties: map[string]string{
				"cliPath": "echo",
				"model":   "claude-opus-4-20250514",
			},
		},
	}
	err := cc.Init(context.Background(), meta)
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-20250514", cc.meta.Model)
}

func TestConverse_NilRequest(t *testing.T) {
	l := logger.NewLogger("test")
	cc := &ClaudeCode{logger: l}

	resp, err := cc.Converse(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.Outputs)
}

func TestConverse_EmptyMessages(t *testing.T) {
	l := logger.NewLogger("test")
	cc := &ClaudeCode{logger: l}

	msgs := []llms.MessageContent{}
	resp, err := cc.Converse(context.Background(), &conversation.Request{
		Message: &msgs,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.Outputs)
}

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name       string
		messages   *[]llms.MessageContent
		wantPrompt string
		wantSystem string
	}{
		{
			name:       "nil messages",
			messages:   nil,
			wantPrompt: "",
			wantSystem: "",
		},
		{
			name: "single text message",
			messages: &[]llms.MessageContent{
				{
					Role: llms.ChatMessageTypeHuman,
					Parts: []llms.ContentPart{
						llms.TextContent{Text: "Hello world"},
					},
				},
			},
			wantPrompt: "Hello world",
			wantSystem: "",
		},
		{
			name: "system and human messages separated",
			messages: &[]llms.MessageContent{
				{
					Role: llms.ChatMessageTypeSystem,
					Parts: []llms.ContentPart{
						llms.TextContent{Text: "You are helpful"},
					},
				},
				{
					Role: llms.ChatMessageTypeHuman,
					Parts: []llms.ContentPart{
						llms.TextContent{Text: "Tell me a joke"},
					},
				},
			},
			wantPrompt: "Tell me a joke",
			wantSystem: "You are helpful",
		},
		{
			name: "tool response",
			messages: &[]llms.MessageContent{
				{
					Role: llms.ChatMessageTypeHuman,
					Parts: []llms.ContentPart{
						llms.ToolCallResponse{
							ToolCallID: "123",
							Name:       "get_weather",
							Content:    "sunny",
						},
					},
				},
			},
			wantPrompt: "[Tool get_weather result]: sunny",
			wantSystem: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt, system := buildPrompt(tt.messages)
			assert.Equal(t, tt.wantPrompt, prompt)
			assert.Equal(t, tt.wantSystem, system)
		})
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"end_turn", "stop"},
		{"tool_use", "tool_calls"},
		{"max_tokens", "length"},
		{"stop_sequence", "stop"},
		{"", "stop"},
		{"unknown_reason", "unknown_reason"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, mapStopReason(tt.input))
		})
	}
}

func TestBuildArgs(t *testing.T) {
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model:        "claude-sonnet-4-20250514",
			AllowedTools: "Read,Bash",
			MaxTurns:     5,
		},
	}

	req := &conversation.Request{
		ConversationContext: "session-123",
	}

	args := cc.buildArgs("hello", "you are helpful", req)

	assert.Contains(t, args, "--bare")
	assert.Contains(t, args, "hello")
	assert.Contains(t, args, "stream-json")
	assert.Contains(t, args, "--verbose")
	assert.Contains(t, args, "claude-sonnet-4-20250514")
	assert.Contains(t, args, "--system-prompt")
	assert.Contains(t, args, "you are helpful")
	assert.Contains(t, args, "Read,Bash")
	assert.Contains(t, args, "--max-turns")
	assert.Contains(t, args, "5")
	assert.Contains(t, args, "--resume")
	assert.Contains(t, args, "session-123")
}

func TestBuildArgs_Minimal(t *testing.T) {
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model: "claude-sonnet-4-20250514",
		},
	}

	req := &conversation.Request{}
	args := cc.buildArgs("hello", "", req)

	assert.Contains(t, args, "--bare")
	assert.NotContains(t, args, "--allowedTools")
	assert.NotContains(t, args, "--max-turns")
	assert.NotContains(t, args, "--resume")
	assert.NotContains(t, args, "--system-prompt")
}

func TestGetComponentMetadata(t *testing.T) {
	l := logger.NewLogger("test")
	cc := &ClaudeCode{logger: l}
	md := cc.GetComponentMetadata()
	require.NotNil(t, md)
}

func TestClose(t *testing.T) {
	l := logger.NewLogger("test")
	cc := NewClaudeCode(l)
	assert.NoError(t, cc.Close())
}

// writeMockScript creates a shell script that echoes the given events as JSON lines.
func writeMockScript(t *testing.T, dir string, name string, events []map[string]any) string {
	t.Helper()
	scriptPath := filepath.Join(dir, name)

	var scriptLines []string
	scriptLines = append(scriptLines, "#!/bin/sh")
	for _, evt := range events {
		b, err := json.Marshal(evt)
		require.NoError(t, err, "failed to marshal test event")
		scriptLines = append(scriptLines, "echo '"+string(b)+"'")
	}

	err := os.WriteFile(scriptPath, []byte(strings.Join(scriptLines, "\n")+"\n"), 0o755)
	require.NoError(t, err)
	return scriptPath
}

// TestConverse_WithMockCLI tests the full Converse flow using a mock script
// that emits stream-json events.
func TestConverse_WithMockCLI(t *testing.T) {
	events := []map[string]any{
		{
			"type":       "stream_event",
			"session_id": "sess-abc",
			"event": map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":    "msg_123",
					"model": "claude-sonnet-4-20250514",
					"usage": map[string]any{
						"input_tokens": 100,
					},
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-abc",
			"event": map[string]any{
				"type":  "content_block_start",
				"index": 0,
				"content_block": map[string]any{
					"type": "text",
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-abc",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": "Hello ",
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-abc",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": "world!",
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-abc",
			"event": map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-abc",
			"event": map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason": "end_turn",
				},
				"usage": map[string]any{
					"output_tokens": 50,
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-abc",
			"event": map[string]any{
				"type": "message_stop",
			},
		},
	}

	mockScript := writeMockScript(t, t.TempDir(), "mock-claude.sh", events)

	l := logger.NewLogger("test")
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model:   "claude-sonnet-4-20250514",
			CLIPath: mockScript,
		},
		logger: l,
	}

	msgs := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "Say hello"}},
		},
	}

	resp, err := cc.Converse(context.Background(), &conversation.Request{
		Message: &msgs,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "claude-sonnet-4-20250514", resp.Model)
	assert.Equal(t, "sess-abc", resp.ConversationContext)
	require.Len(t, resp.Outputs, 1)
	require.Len(t, resp.Outputs[0].Choices, 1)
	assert.Equal(t, "Hello world!", resp.Outputs[0].Choices[0].Message.Content)
	assert.Equal(t, "stop", resp.Outputs[0].Choices[0].FinishReason)
	assert.Equal(t, "stop", resp.Outputs[0].StopReason)

	require.NotNil(t, resp.Usage)
	assert.Equal(t, uint64(100), resp.Usage.PromptTokens)
	assert.Equal(t, uint64(50), resp.Usage.CompletionTokens)
	assert.Equal(t, uint64(150), resp.Usage.TotalTokens)
}

// TestConverse_WithToolCalls tests that tool_use content blocks are parsed correctly.
func TestConverse_WithToolCalls(t *testing.T) {
	events := []map[string]any{
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":    "msg_456",
					"model": "claude-sonnet-4-20250514",
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type":  "content_block_start",
				"index": 0,
				"content_block": map[string]any{
					"type": "text",
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": "Let me check the weather.",
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type":  "content_block_start",
				"index": 1,
				"content_block": map[string]any{
					"type": "tool_use",
					"id":   "toolu_abc",
					"name": "get_weather",
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 1,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": `{"location":`,
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type":  "content_block_delta",
				"index": 1,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": `"San Francisco"}`,
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type":  "content_block_stop",
				"index": 1,
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason": "tool_use",
				},
				"usage": map[string]any{
					"output_tokens": 75,
				},
			},
		},
		{
			"type":       "stream_event",
			"session_id": "sess-tools",
			"event": map[string]any{
				"type": "message_stop",
			},
		},
	}

	mockScript := writeMockScript(t, t.TempDir(), "mock-claude-tools.sh", events)

	l := logger.NewLogger("test")
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model:   "claude-sonnet-4-20250514",
			CLIPath: mockScript,
		},
		logger: l,
	}

	msgs := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "What is the weather?"}},
		},
	}

	resp, err := cc.Converse(context.Background(), &conversation.Request{
		Message: &msgs,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "tool_calls", resp.Outputs[0].StopReason)
	choice := resp.Outputs[0].Choices[0]
	assert.Equal(t, "Let me check the weather.", choice.Message.Content)
	assert.Equal(t, "tool_calls", choice.FinishReason)

	require.NotNil(t, choice.Message.ToolCallRequest)
	toolCalls := *choice.Message.ToolCallRequest
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "toolu_abc", toolCalls[0].ID)
	assert.Equal(t, "get_weather", toolCalls[0].FunctionCall.Name)
	assert.Equal(t, `{"location":"San Francisco"}`, toolCalls[0].FunctionCall.Arguments)
}

// TestConverse_SubprocessError tests behavior when the CLI exits with an error.
func TestConverse_SubprocessError(t *testing.T) {
	tmpDir := t.TempDir()
	mockScript := filepath.Join(tmpDir, "mock-claude-fail.sh")

	err := os.WriteFile(mockScript, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	require.NoError(t, err)

	l := logger.NewLogger("test")
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model:   "claude-sonnet-4-20250514",
			CLIPath: mockScript,
		},
		logger: l,
	}

	msgs := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "hi"}},
		},
	}

	_, err = cc.Converse(context.Background(), &conversation.Request{
		Message: &msgs,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subprocess exited with error")
}

// TestConverse_ContextCancellation tests that context cancellation stops the subprocess.
func TestConverse_ContextCancellation(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}

	tmpDir := t.TempDir()
	mockScript := filepath.Join(tmpDir, "mock-claude-slow.sh")

	err := os.WriteFile(mockScript, []byte("#!/bin/sh\nsleep 60\n"), 0o755)
	require.NoError(t, err)

	l := logger.NewLogger("test")
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model:   "claude-sonnet-4-20250514",
			CLIPath: mockScript,
		},
		logger: l,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	msgs := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "hi"}},
		},
	}

	_, err = cc.Converse(ctx, &conversation.Request{
		Message: &msgs,
	})
	require.Error(t, err)
}

// TestConverse_MalformedJSON tests that malformed JSON lines are skipped gracefully.
func TestConverse_MalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	mockScript := filepath.Join(tmpDir, "mock-claude-malformed.sh")

	// Mix valid events with malformed lines.
	script := `#!/bin/sh
echo 'not valid json'
echo '{"type":"stream_event","session_id":"sess-m","event":{"type":"message_start","message":{"model":"claude-sonnet-4-20250514"}}}'
echo '{broken'
echo '{"type":"stream_event","session_id":"sess-m","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}}'
echo '{"type":"stream_event","session_id":"sess-m","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}}'
`
	err := os.WriteFile(mockScript, []byte(script), 0o755)
	require.NoError(t, err)

	l := logger.NewLogger("test")
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model:   "claude-sonnet-4-20250514",
			CLIPath: mockScript,
		},
		logger: l,
	}

	msgs := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "hi"}},
		},
	}

	resp, err := cc.Converse(context.Background(), &conversation.Request{
		Message: &msgs,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ok", resp.Outputs[0].Choices[0].Message.Content)
}

// TestConverse_APIRetryEvent tests that system/api_retry events are logged and skipped.
func TestConverse_APIRetryEvent(t *testing.T) {
	tmpDir := t.TempDir()
	mockScript := filepath.Join(tmpDir, "mock-claude-retry.sh")

	script := `#!/bin/sh
echo '{"type":"system","subtype":"api_retry","error":"rate limited","attempt":1,"max_retries":3}'
echo '{"type":"stream_event","session_id":"sess-r","event":{"type":"message_start","message":{"model":"claude-sonnet-4-20250514"}}}'
echo '{"type":"stream_event","session_id":"sess-r","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"recovered"}}}'
echo '{"type":"stream_event","session_id":"sess-r","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}}'
`
	err := os.WriteFile(mockScript, []byte(script), 0o755)
	require.NoError(t, err)

	l := logger.NewLogger("test")
	cc := &ClaudeCode{
		meta: ClaudeCodeMetadata{
			Model:   "claude-sonnet-4-20250514",
			CLIPath: mockScript,
		},
		logger: l,
	}

	msgs := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextContent{Text: "hi"}},
		},
	}

	resp, err := cc.Converse(context.Background(), &conversation.Request{
		Message: &msgs,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "recovered", resp.Outputs[0].Choices[0].Message.Content)
}
