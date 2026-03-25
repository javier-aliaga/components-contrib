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
	"encoding/json"
	"strings"
)

// streamLine represents a single line of JSON output from Claude Code's
// --output-format stream-json mode.
type streamLine struct {
	Type      string          `json:"type"`
	UUID      string          `json:"uuid,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`

	// For "system" type events
	Subtype string `json:"subtype,omitempty"`
	Error   string `json:"error,omitempty"`
}

// streamEvent is the inner event within a stream_event line.
type streamEvent struct {
	Type         string        `json:"type"`
	Message      *messageInfo  `json:"message,omitempty"`
	Index        int           `json:"index,omitempty"`
	ContentBlock *contentBlock `json:"content_block,omitempty"`
	Delta        *delta        `json:"delta,omitempty"`
	Usage        *usageInfo    `json:"usage,omitempty"`
}

// messageInfo appears in message_start events.
type messageInfo struct {
	ID    string     `json:"id,omitempty"`
	Model string     `json:"model,omitempty"`
	Usage *usageInfo `json:"usage,omitempty"`
}

// contentBlock appears in content_block_start events.
type contentBlock struct {
	Type string `json:"type"` // "text" or "tool_use"
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// delta appears in content_block_delta and message_delta events.
type delta struct {
	Type        string `json:"type,omitempty"`         // "text_delta" or "input_json_delta"
	Text        string `json:"text,omitempty"`         // For text_delta
	PartialJSON string `json:"partial_json,omitempty"` // For input_json_delta
	StopReason  string `json:"stop_reason,omitempty"`  // For message_delta
}

// usageInfo represents token usage from Claude Code.
type usageInfo struct {
	InputTokens              uint64 `json:"input_tokens,omitempty"`
	OutputTokens             uint64 `json:"output_tokens,omitempty"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens,omitempty"`
}

// toolAccumulator tracks a tool_use content block being streamed.
type toolAccumulator struct {
	Index       int
	ID          string
	Name        string
	JSONBuilder strings.Builder
}
